package cli

import (
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aaaSaZaN/gowave-node/internal/config"
)

func readinessServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GOWAVE_NODE_RUNTIME_DIR", dir)
	socket := filepath.Join(dir, "internal.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	t.Cleanup(func() { server.Close() })
	go server.Serve(listener)
	if err := config.SaveDiscovery(&config.Config{InternalSocketPath: socket, InternalRestToken: "test"}); err != nil {
		t.Fatal(err)
	}
}

func TestWaitRunningWithoutPanelConfiguration(t *testing.T) {
	readinessServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/internal/ready" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})
	if err := WaitReady(false, time.Second, 0); err != nil {
		t.Fatalf("daemon running without configuration: %v", err)
	}
	if Ready(true) {
		t.Fatal("unconfigured core reported ready")
	}
}

func TestWaitReadyDeadlineCancelsUnresponsiveDaemon(t *testing.T) {
	readinessServer(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	start := time.Now()
	if err := WaitReady(false, 50*time.Millisecond, 0); err == nil {
		t.Fatal("unresponsive daemon reported running")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("readiness request exceeded deadline: %s", elapsed)
	}
}

func TestUpdateFailureRestoresPreviousExecutable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "node")
	os.WriteFile(target, []byte("new"), 0700)
	os.WriteFile(target+".previous", []byte("old"), 0700)
	oldRestart, oldWait, oldDelay := restartNode, waitForReady, updateDelay
	defer func() { restartNode = oldRestart; waitForReady = oldWait; updateDelay = oldDelay }()
	calls := 0
	restartNode = func() error { calls++; return nil }
	waitForReady = func(bool, time.Duration, int) error { return errors.New("not ready") }
	updateDelay = 0
	if err := CompleteUpdate(target, true, 1); err == nil {
		t.Fatal("failed update acknowledged")
	}
	if calls != 2 {
		t.Fatalf("restart count=%d", calls)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "old" {
		t.Fatal("previous executable not restored")
	}
}

func TestUpdateWaitsForNodeWithoutCoreConfiguration(t *testing.T) {
	oldRestart, oldWait, oldDelay := restartNode, waitForReady, updateDelay
	defer func() { restartNode = oldRestart; waitForReady = oldWait; updateDelay = oldDelay }()
	restartNode = func() error { return nil }
	waitForReady = func(core bool, timeout time.Duration, oldPID int) error {
		if core || timeout != 120*time.Second || oldPID != 42 {
			t.Fatalf("readiness arguments: core=%v timeout=%s pid=%d", core, timeout, oldPID)
		}
		return nil
	}
	updateDelay = 0
	if err := CompleteUpdate(filepath.Join(t.TempDir(), "node"), true, 42); err != nil {
		t.Fatal(err)
	}
}
