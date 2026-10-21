package serviceunit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerMigratesExistingUnitAndKeepsBoundary(t *testing.T) {
	root := t.TempDir()
	dir, units, bin := filepath.Join(root, "install"), filepath.Join(root, "units"), filepath.Join(root, "bin")
	for _, d := range []string{dir, units, bin} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	// Ownership commands are observed; account lookup never touches system accounts.
	mock := `#!/bin/sh
case "${0##*/}" in
id) case "$*" in "-u") echo 0 ;; "-u gowave-node") echo 10001 ;; esac ;;
*) printf '%s %s\n' "${0##*/}" "$*" >> "$SERVICE_TEST_EVENTS" ;;
esac
`
	for _, name := range []string{"id", "chown", "useradd", "systemctl", "getent", "groupadd"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(mock), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	events := filepath.Join(root, "events")
	t.Setenv("SERVICE_TEST_EVENTS", events)
	oldUnit := "[Service]\nUser=root\nEnvironment=EXISTING_SETTING=preserved\n"
	unit := filepath.Join(units, "gowave-node.service")
	if err := os.WriteFile(unit, []byte(oldUnit), 0644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		cmd := exec.Command("sh", "-s", "--", dir, units)
		cmd.Stdin = strings.NewReader(installer)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("install: %v: %s", err, out)
		}
	}
	data, err := os.ReadFile(filepath.Join(units, "gowave-node.service.d/90-unprivileged.conf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, directive := range []string{"User=gowave-node", "Group=gowave-node", "ProtectSystem=strict", "ProtectHome=true", "PrivateDevices=true", "PrivateTmp=true", "NoNewPrivileges=true", "MemoryDenyWriteExecute=true", "ReadWritePaths=" + dir, "Restart=always"} {
		if !strings.Contains(string(data), directive+"\n") {
			t.Errorf("missing %s", directive)
		}
	}
	unchanged, _ := os.ReadFile(unit)
	if string(unchanged) != oldUnit {
		t.Fatal("installer overwrote existing unit configuration")
	}
	calls, _ := os.ReadFile(events)
	if !strings.Contains(string(calls), "chown -hR gowave-node:gowave-node "+dir) || !strings.Contains(string(calls), "systemctl daemon-reload") {
		t.Fatalf("missing migration operations: %s", calls)
	}
	// A path pointing at the system binary directory must never be recursively chowned.
	cmd := exec.Command("sh", "-s", "--", "/usr/local/bin", units)
	cmd.Stdin = strings.NewReader(installer)
	if err := cmd.Run(); err == nil {
		t.Fatal("accepted a shared system directory")
	}
}

func TestNonSystemdAccountSetupUsesPortableTools(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, manager := range []string{"openrc", "procd"} {
		t.Run(manager, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			if err := os.Mkdir(bin, 0755); err != nil {
				t.Fatal(err)
			}
			events := filepath.Join(root, "events")
			// Only BusyBox-style account commands are available on this PATH.
			// Resolve the real filesystem tools before restricting the PATH.
			for _, name := range []string{"mkdir", "cp", "mv", "chmod"} {
				path, err := exec.LookPath(name)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path, filepath.Join(bin, name)); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(bin, "awk"), []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
				t.Fatal(err)
			}
			mock := `#!/bin/sh
case "${0##*/}" in
id) case "$*" in "-u") echo 0 ;; "-u gowave-node") echo 10001 ;; *) exit 1 ;; esac ;;
*) printf '%s %s\n' "${0##*/}" "$*" >> "$SERVICE_TEST_EVENTS" ;;
esac
`
			for _, name := range []string{"id", "chown", "addgroup", "adduser", "systemctl"} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(mock), 0755); err != nil {
					t.Fatal(err)
				}
			}
			dir, units := filepath.Join(root, "install"), filepath.Join(root, "units")
			cmd := exec.Command(shell, "-s", "--", dir, units, "", manager)
			cmd.Env = append(os.Environ(), "PATH="+bin, "SERVICE_TEST_EVENTS="+events)
			cmd.Stdin = strings.NewReader(installer)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("setup: %v: %s", err, out)
			}
			data, err := os.ReadFile(events)
			if err != nil {
				t.Fatal(err)
			}
			calls := string(data)
			for _, operation := range []string{"addgroup -S gowave-node", "adduser -S -D -H", "chown -hR gowave-node:gowave-node"} {
				if !strings.Contains(calls, operation) {
					t.Errorf("missing %s: %s", operation, calls)
				}
			}
			if strings.Contains(calls, "systemctl") {
				t.Fatalf("non-systemd setup called systemctl: %s", calls)
			}
			if _, err := os.Stat(units); !os.IsNotExist(err) {
				t.Fatalf("non-systemd setup wrote systemd configuration: %v", err)
			}
		})
	}
}

func TestRejectUnknownServiceManager(t *testing.T) {
	if err := InstallForManager(t.TempDir(), t.TempDir(), "unknown"); err == nil {
		t.Fatal("accepted an unknown service manager")
	}
}
