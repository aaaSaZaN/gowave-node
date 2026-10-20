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
id) case "$*" in "-u") echo 0 ;; "-u remnanode") echo 10001 ;; esac ;;
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
	unit := filepath.Join(units, "remnanode.service")
	if err := os.WriteFile(unit, []byte(oldUnit), 0644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		cmd := exec.Command("bash", "-s", "--", dir, units)
		cmd.Stdin = strings.NewReader(installer)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("install: %v: %s", err, out)
		}
	}
	data, err := os.ReadFile(filepath.Join(units, "remnanode.service.d/90-unprivileged.conf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, directive := range []string{"User=remnanode", "Group=remnanode", "ProtectSystem=strict", "ProtectHome=true", "PrivateDevices=true", "PrivateTmp=true", "NoNewPrivileges=true", "MemoryDenyWriteExecute=true", "ReadWritePaths=" + dir, "Restart=always"} {
		if !strings.Contains(string(data), directive+"\n") {
			t.Errorf("missing %s", directive)
		}
	}
	unchanged, _ := os.ReadFile(unit)
	if string(unchanged) != oldUnit {
		t.Fatal("installer overwrote existing unit configuration")
	}
	calls, _ := os.ReadFile(events)
	if !strings.Contains(string(calls), "chown -hR remnanode:remnanode "+dir) || !strings.Contains(string(calls), "systemctl daemon-reload") {
		t.Fatalf("missing migration operations: %s", calls)
	}
	// A path pointing at the system binary directory must never be recursively chowned.
	cmd := exec.Command("bash", "-s", "--", "/usr/local/bin", units)
	cmd.Stdin = strings.NewReader(installer)
	if err := cmd.Run(); err == nil {
		t.Fatal("accepted a shared system directory")
	}
}
