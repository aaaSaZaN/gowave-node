package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
