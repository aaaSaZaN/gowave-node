package updater

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagedUpdateRequestUsesPrivateRuntimeAndSeparateMonitor(t *testing.T) {
	dir, runtime := t.TempDir(), t.TempDir()
	t.Setenv("REMNANODE_RUNTIME_DIR", runtime)
	target := filepath.Join(dir, "remnanode")
	if err := writeManagedUpdate(dir, target, false, 123); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, ".update-monitor"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("monitor permissions: %o", info.Mode().Perm())
	}
	info, err = os.Stat(filepath.Join(runtime, "update-request.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("request permissions: %o", info.Mode().Perm())
	}
	request, err := ReadUpdateRequest()
	if err != nil {
		t.Fatal(err)
	}
	if request.Target != target || request.PID != 123 || request.Core {
		t.Fatalf("unexpected request: %+v", request)
	}
	if _, err := ReadUpdateRequest(); !os.IsNotExist(err) {
		t.Fatalf("request consumed twice: %v", err)
	}
}

func TestInvalidManagedUpdateRequestIsRejected(t *testing.T) {
	runtime := t.TempDir()
	t.Setenv("REMNANODE_RUNTIME_DIR", runtime)
	for _, body := range []string{`{`, `{"target":"relative/path","pid":123}`, `{"target":"/opt/remnanode/remnanode","pid":0}`} {
		if err := os.WriteFile(filepath.Join(runtime, "update-request.json"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadUpdateRequest(); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestManagedRestartDoesNotInvokeServiceManager(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("unprivileged request path")
	}
	runtime := t.TempDir()
	t.Setenv("REMNANODE_RUNTIME_DIR", runtime)
	t.Setenv("REMNANODE_MANAGED_SERVICE", "1")
	t.Setenv("PATH", t.TempDir())
	if err := RestartNodeProcess(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(runtime, "restart-request")); err != nil {
		t.Fatal(err)
	}
}
