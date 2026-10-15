package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"errors"
	"github.com/remnawave/node-go/internal/config"
	"github.com/remnawave/node-go/internal/logger"
	"github.com/remnawave/node-go/internal/updater"
	"syscall"
)

var versionRe = regexp.MustCompile(`(?:Xray|rw-core)\s+v?([0-9]+\.[0-9]+\.[0-9]+[a-zA-Z0-9.-]*)`)

type ProcessStatus struct {
	Up  bool
	PID int
	Raw string
}

type ProcessManager struct {
	custom        bool
	pendingCustom bool
	activated     bool
	mu            sync.Mutex
	serviceDir    string
	controlFifo   string
	s6Available   bool
	directCmd     *exec.Cmd
	done          chan struct{}
	exitErr       error
	assetDir      string
	pendingCore   string
	configPath    string
	execPath      string
	ringBuffer    *logger.RingBuffer
}

func FindCorePath(s6Available bool) string {
	if path := os.Getenv("XRAY_CORE_PATH"); path != "" {
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			return path
		}
		return ""
	}
	// In S6 Docker container, Xray is pre-baked in /usr/local/bin
	if s6Available {
		for _, p := range []string{"/usr/local/bin/rw-core", "/usr/local/bin/xray"} {
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p
			}
		}
	}

	// 1. Check adjacent to remnanode binary (e.g. /opt/remnanode/rw-core or /opt/remnanode/xray)
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		for _, name := range []string{"rw-core", "xray"} {
			p := filepath.Join(exeDir, name)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p
			}
		}
	}

	// 2. Check current working directory
	for _, name := range []string{"./rw-core", "./xray"} {
		if fi, err := os.Stat(name); err == nil && !fi.IsDir() {
			if abs, err := filepath.Abs(name); err == nil {
				return abs
			}
			return name
		}
	}

	for _, path := range []string{"/usr/local/bin/rw-core", "/usr/local/bin/xray"} {
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			return path
		}
	}
	return ""
}

func NewProcessManager(serviceDir string, ringBuffer *logger.RingBuffer) *ProcessManager {
	if serviceDir == "" {
		serviceDir = "/run/service/xray"
	}
	controlFifo := filepath.Join(serviceDir, "supervise", "control")
	_, err := os.Stat(controlFifo)
	s6Available := err == nil

	execPath := FindCorePath(s6Available)
	if !s6Available && execPath == "" {
		targetDir := "."
		if exe, err := os.Executable(); err == nil {
			targetDir = filepath.Dir(exe)
		}
		targetPath := filepath.Join(targetDir, "rw-core")
		if downloaded, err := updater.EnsureCore(targetPath); err == nil && downloaded != "" {
			execPath = downloaded
		} else {
			log.Printf("[CORE] Warning: Xray core not found and auto-download failed: %v", err)
		}
	} else if execPath != "" {
		if err := updater.EnsureGeodata(assetDirectory(execPath)); err != nil {
			log.Printf("[CORE] geodata preparation failed: %v", err)
		}
	}

	return &ProcessManager{
		serviceDir:  serviceDir,
		controlFifo: controlFifo,
		s6Available: s6Available,
		configPath:  filepath.Join(config.RuntimeDir(), "xray.json"),
		assetDir:    assetDirectory(execPath),
		custom:      updater.IsCustomCore(execPath),
		execPath:    execPath,
		ringBuffer:  ringBuffer,
	}
}

func (p *ProcessManager) GetExecPath() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.execPath
}

func (p *ProcessManager) SetExecPath(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.execPath = path
}

func (p *ProcessManager) IsControlAvailable() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.s6Available || p.execPath != ""
}

func assetDirectory(core string) string {
	if dir := os.Getenv("XRAY_LOCATION_ASSET"); dir != "" {
		return dir
	}
	for _, dir := range []string{filepath.Dir(core), "/usr/local/share/xray", "/opt/remnanode", "/usr/share/xray"} {
		if _, err := os.Stat(filepath.Join(dir, "geoip.dat")); err == nil {
			return dir
		}
	}
	return filepath.Dir(core)
}
func (p *ProcessManager) Prepare(ctx context.Context, geodata interface{}) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	candidate, err := updater.PrepareCore(ctx, p.execPath, geodata)
	if err != nil {
		return err
	}
	if p.pendingCore != "" && p.pendingCore != p.execPath && p.pendingCore != p.execPath+".stock" {
		os.Remove(p.pendingCore)
	}
	p.pendingCore = candidate
	p.pendingCustom = false
	if cfg, ok := geodata.(map[string]interface{}); ok {
		p.pendingCustom = cfg["core"] != nil
	}
	return updater.PrepareAssets(ctx, p.assetDir, geodata)
}
func (p *ProcessManager) command(ctx context.Context, args ...string) *exec.Cmd {
	path := p.execPath
	if p.pendingCore != "" {
		path = p.pendingCore
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+p.assetDir, "xray.location.asset="+p.assetDir)
	return cmd
}
func (p *ProcessManager) ValidateConfig(ctx context.Context, cfg map[string]interface{}) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	stage := p.configPath + ".validate"
	if err = config.WritePrivate(stage, data); err != nil {
		return err
	}
	defer os.Remove(stage)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := p.command(ctx, "run", "-test", "-format", "json", "-config", stage).CombinedOutput()
	if err != nil {
		return fmt.Errorf("invalid Xray configuration: %w: %s", err, out)
	}
	return nil
}
func (p *ProcessManager) Start(cfg map[string]interface{}) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err = config.WritePrivate(p.configPath, data); err != nil {
		return err
	}
	if p.pendingCore != "" && p.pendingCore != p.execPath {
		candidate := p.pendingCore
		if err := updater.ActivateCore(candidate, p.execPath); err != nil {
			return err
		}
		p.activated = true
		if candidate != p.execPath+".stock" {
			os.Remove(candidate)
		}
		p.pendingCore = ""
	}
	p.custom = p.pendingCustom
	if p.s6Available {
		if err := p.prepareS6Run(); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, s6Command("s6-svc"), "-wu", "-T", "10000", "-o", p.serviceDir).CombinedOutput()
		if err != nil {
			return fmt.Errorf("s6 start: %w: %s", err, out)
		}
		return nil
	}
	if p.directCmd != nil {
		return errors.New("core is already running")
	}
	cmd := exec.Command(p.execPath, "run", "-config", p.configPath)
	cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+p.assetDir, "xray.location.asset="+p.assetDir)
	if p.ringBuffer != nil {
		cmd.Stdout = io.MultiWriter(os.Stdout, p.ringBuffer)
		cmd.Stderr = io.MultiWriter(os.Stderr, p.ringBuffer)
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	p.done = done
	p.directCmd = cmd
	p.exitErr = nil
	go func() {
		err := cmd.Wait()
		close(done)
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.directCmd == cmd {
			p.directCmd = nil
			p.exitErr = err
		}
	}()
	return nil
}
func (p *ProcessManager) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.s6Available {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, s6Command("s6-svc"), "-wd", "-T", "10000", "-d", p.serviceDir).CombinedOutput()
		if err != nil {
			return fmt.Errorf("s6 stop: %w: %s", err, out)
		}
		return p.restoreS6Run()
	}
	if p.directCmd == nil {
		return nil
	}
	cmd := p.directCmd
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		<-p.done
	}
	p.directCmd = nil
	return nil
}

func (p *ProcessManager) GetCoreVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, p.GetExecPath(), "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}

	matches := versionRe.FindSubmatch(out)
	if len(matches) > 1 {
		return string(matches[1]), nil
	}

	lines := strings.Split(string(out), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0]), nil
	}
	return "unknown", nil
}

func (p *ProcessManager) GetStatus() ProcessStatus {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.s6Available {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, s6Command("s6-svstat"), "-o", "up,pid", p.serviceDir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return ProcessStatus{Up: false, PID: 0, Raw: err.Error()}
		}
		fields := strings.Fields(string(out))
		up := len(fields) > 0 && fields[0] == "true"
		pid := 0
		if len(fields) > 1 {
			pid, _ = strconv.Atoi(fields[1])
		}
		return ProcessStatus{Up: up, PID: pid, Raw: strings.TrimSpace(string(out))}
	}

	if p.directCmd != nil && p.directCmd.Process != nil {
		select {
		case <-p.done:
			return ProcessStatus{Raw: "exited"}
		default:
		}
		pid := p.directCmd.Process.Pid
		return ProcessStatus{Up: true, PID: pid, Raw: fmt.Sprintf("up, pid %d", pid)}
	}

	return ProcessStatus{Up: false, PID: 0, Raw: "stopped"}
}

func (p *ProcessManager) GetStatusLine() string {
	st := p.GetStatus()
	if st.Up {
		return fmt.Sprintf("up (pid %d)", st.PID)
	}
	return "down"
}

func (p *ProcessManager) TailLog(lines int) []string {
	if p.ringBuffer != nil && p.ringBuffer.Size() > 0 {
		return p.ringBuffer.GetLines(lines)
	}

	logPaths := []string{"/var/log/xray/current", "/var/log/xray.log"}
	for _, lp := range logPaths {
		if _, err := os.Stat(lp); err == nil {
			cmd := exec.Command("tail", "-n", strconv.Itoa(lines), lp)
			out, err := cmd.Output()
			if err == nil {
				var res []string
				for _, line := range strings.Split(string(out), "\n") {
					line = strings.TrimSpace(line)
					if line != "" {
						res = append(res, line)
					}
				}
				return res
			}
		}
	}
	return nil
}

func (p *ProcessManager) RollbackActivation() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.activated {
		return nil
	}
	if err := updater.Rollback(p.execPath); err != nil {
		return err
	}
	p.activated = false
	p.custom = updater.IsCustomCore(p.execPath)
	return nil
}
func (p *ProcessManager) CommitActivation() { p.mu.Lock(); defer p.mu.Unlock(); p.activated = false }

func (p *ProcessManager) IsCustom() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.custom }

func s6Command(name string) string {
	if dir := os.Getenv("S6_COMMAND_DIR"); dir != "" {
		return filepath.Join(dir, name)
	}
	fixed := filepath.Join("/command", name)
	if _, err := os.Stat(fixed); err == nil {
		return fixed
	}
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	return fixed
}
