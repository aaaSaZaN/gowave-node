package stats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type GeocheckService struct {
	mu        sync.Mutex
	isRunning bool
}

func NewGeocheckService() *GeocheckService {
	return &GeocheckService{}
}

type GeocheckRequest struct {
	IP        *string `json:"ip"`
	Interface *string `json:"interface"`
}

func (g *GeocheckService) Run(parent context.Context, req GeocheckRequest) (map[string]interface{}, error) {
	g.mu.Lock()
	if g.isRunning {
		g.mu.Unlock()
		return nil, errors.New("Geocheck: a run is already in progress.")
	}
	g.isRunning = true
	g.mu.Unlock()

	defer func() {
		g.mu.Lock()
		g.isRunning = false
		g.mu.Unlock()
	}()

	var bindTo string
	if req.IP != nil && strings.TrimSpace(*req.IP) != "" {
		ip := strings.TrimSpace(*req.IP)
		if net.ParseIP(ip) == nil {
			return nil, fmt.Errorf("Geocheck: \"%s\" is not a valid IP address.", ip)
		}
		bindTo = ip
	} else if req.Interface != nil && strings.TrimSpace(*req.Interface) != "" {
		bindTo = strings.TrimSpace(*req.Interface)
	}

	args := []string{"--json", "--svg-base64", "--quiet"}
	if bindTo != "" {
		args = append([]string{"--interface", bindTo}, args...)
	}

	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/usr/local/bin/geocheck", args...)
	output := &limitedOutput{limit: 32 * 1024 * 1024}
	cmd.Stdout = output
	err := cmd.Run()
	out := output.Bytes()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, errors.New("Geocheck: execution exceeded 45s and was killed.")
		}
		return nil, fmt.Errorf("Geocheck failed: %w", err)
	}

	var report map[string]interface{}
	if err := json.Unmarshal(out, &report); err != nil {
		return nil, fmt.Errorf("Geocheck produced invalid JSON: %w", err)
	}

	image, ok := report["image"].(map[string]interface{})
	if !ok || image["data"] == nil || image["data"] == "" {
		return nil, errors.New("geocheck report carries no image")
	}
	return report, nil
}

type limitedOutput struct {
	bytes.Buffer
	limit int
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("geocheck output exceeds 32 MiB")
	}
	return b.Buffer.Write(p)
}
