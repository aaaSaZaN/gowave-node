package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
)

func RunLogsCLI(logType string, args []string) {
	lines := 100
	follow := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-f" || arg == "--follow" {
			follow = true
		} else if arg == "-n" || arg == "--lines" {
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					lines = n
					i++
				}
			}
		} else if n, err := strconv.Atoi(arg); err == nil && n > 0 {
			lines = n
		}
	}

	client, token, connectionErr := internalClient()
	chosenPath := "managed runtime socket"
	socketPaths := []string{chosenPath}
	if connectionErr != nil {
		client = nil
	}
	fetch := func(count int) ([]string, error) {
		url := fmt.Sprintf("http://unix/internal/logs?type=%s&lines=%d&format=json&token=%s", logType, count, token)
		resp, err := client.Get(url)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("internal API HTTP %d", resp.StatusCode)
		}

		var data struct {
			Response struct {
				Logs []string `json:"logs"`
			} `json:"response"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			return nil, err
		}
		return data.Response.Logs, nil
	}

	var initialLogs []string
	if client != nil {
		defer client.CloseIdleConnections()
		var err error
		initialLogs, err = fetch(lines)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s[ERROR]%s Failed to fetch logs from socket %s: %v\n", colorRed, colorReset, chosenPath, err)
			client = nil
		}
	}
	if client == nil {
		if logType == "xray" {
			logPaths := []string{"/var/log/xray/current", "/var/log/xray.log"}
			for _, lp := range logPaths {
				if _, err := os.Stat(lp); err == nil {
					tailArgs := []string{"-n", strconv.Itoa(lines)}
					if follow {
						tailArgs = append(tailArgs, "-f")
					}
					tailArgs = append(tailArgs, lp)
					cmd := exec.Command("tail", tailArgs...)
					cmd.Stdout = os.Stdout
					cmd.Stderr = os.Stderr
					_ = cmd.Run()
					return
				}
			}
		}

		fmt.Fprintf(os.Stderr, "%s[ERROR]%s Remnanode daemon is not running or unix socket not found.\n", colorRed, colorReset)
		fmt.Fprintf(os.Stderr, "Checked sockets: %v\n", socketPaths)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	seen := make(map[string]struct{})

	for _, line := range initialLogs {
		printFormattedLine(line)
		seen[line] = struct{}{}
	}

	if !follow {
		return
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			return
		case <-ticker.C:
			recent, err := fetch(50)
			if err != nil {
				continue
			}
			for _, line := range recent {
				if _, ok := seen[line]; !ok {
					printFormattedLine(line)
					seen[line] = struct{}{}
					if len(seen) > 2000 {
						seen = make(map[string]struct{})
						seen[line] = struct{}{}
					}
				}
			}
		}
	}
}

func printFormattedLine(line string) {
	if strings.Contains(line, "[Warning]") || strings.Contains(line, "WARN") {
		fmt.Println(colorYellow + line + colorReset)
	} else if strings.Contains(line, "[Error]") || strings.Contains(line, "ERROR") || strings.Contains(line, "FATAL") {
		fmt.Println(colorRed + line + colorReset)
	} else if strings.Contains(line, "[Info]") || strings.Contains(line, "INFO") {
		fmt.Println(colorCyan + line + colorReset)
	} else if strings.Contains(line, "started") || strings.Contains(line, "Listening") || strings.Contains(line, "OK") {
		fmt.Println(colorGreen + line + colorReset)
	} else {
		fmt.Println(line)
	}
}

var _ = io.EOF
