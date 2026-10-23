package updater

import (
	"context"
	"fmt"
	"github.com/aaaSaZaN/gowave-node/internal/config"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// initScripts are the OpenRC/OpenWrt procd and Entware services that
// install.sh writes.
var initScripts = []string{"/etc/init.d/gowave-node", "/opt/etc/init.d/S99gowave-node"}

// InitScript returns the installed init.d service script, or "" without one.
func InitScript() string {
	for _, path := range initScripts {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// RestartNodeProcess restarts the node through its service manager.
func RestartNodeProcess() error {
	// Entware's script waits up to 30 seconds for the node to stop.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if _, err := os.Stat("/run/service/node/supervise/control"); err == nil {
		cmd = exec.CommandContext(ctx, "/command/s6-svc", "-t", "/run/service/node")
	} else if _, err := os.Stat("/run/systemd/system"); err == nil {
		cmd = exec.CommandContext(ctx, "systemctl", "restart", "gowave-node")
	} else if script := InitScript(); script != "" {
		cmd = exec.CommandContext(ctx, script, "restart")
	} else if _, err := exec.LookPath("systemctl"); err == nil {
		cmd = exec.CommandContext(ctx, "systemctl", "restart", "gowave-node")
	} else {
		return fmt.Errorf("no supported service manager; restart gowave-node manually")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("restart: %w: %s", err, out)
	}
	return nil
}

// ScheduleRestart launches a separate systemd unit: restarting the daemon cannot
// kill the update monitor along with the service's control group.
func ScheduleRestart(target string, core bool) error {
	if err := config.PrivateDir(config.RuntimeDir()); err != nil {
		return err
	}
	// The runtime directory lives under /run, which distributions such as
	// Ubuntu mount noexec; systemd-run then cannot start the monitor. The
	// node's own directory is executable by definition.
	dir := filepath.Dir(target)
	if exe, err := os.Executable(); err == nil {
		dir = filepath.Dir(exe)
	}
	monitor, err := os.CreateTemp(dir, ".update-monitor-*")
	if err != nil {
		return err
	}
	monitor.Close()
	source := "/proc/self/exe"
	if err := CopyFile(source, monitor.Name()); err != nil {
		os.Remove(monitor.Name())
		return err
	}
	if err := os.Chmod(monitor.Name(), 0700); err != nil {
		os.Remove(monitor.Name())
		return err
	}
	exe := monitor.Name()
	if _, err := exec.LookPath("systemd-run"); err == nil {
		cmd := exec.Command("systemd-run", "--quiet", "--collect", "--setenv=GOWAVE_NODE_RUNTIME_DIR="+config.RuntimeDir(), "--unit=gowave-node-update-"+fmt.Sprint(time.Now().UnixNano()), exe, "_finish-update", target, fmt.Sprint(core), fmt.Sprint(os.Getpid()))
		if out, err := cmd.CombinedOutput(); err != nil {
			os.Remove(exe)
			return fmt.Errorf("schedule update: %w: %s", err, out)
		}
		return nil
	}
	os.Remove(exe)
	return fmt.Errorf("HTTP restart supervision requires systemd-run; use the synchronous CLI on s6")
}

func Rollback(target string) error {
	source := target + ".previous"
	f, err := os.CreateTemp(filepath.Dir(target), ".rollback-*")
	if err != nil {
		return err
	}
	f.Close()
	defer os.Remove(f.Name())
	if err = CopyFile(source, f.Name()); err != nil {
		return err
	}
	if err := AtomicReplace(f.Name(), target); err != nil {
		return err
	}
	if marker, err := os.ReadFile(target + ".stock.pending"); err == nil {
		if string(marker) == "restore" {
			if err := CopyFile(target+".stock.previous", target+".stock"); err != nil {
				return err
			}
		} else {
			_ = os.Remove(target + ".stock")
		}
		CommitUpdate(target)
	}
	return nil
}
