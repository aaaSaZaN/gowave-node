package xray

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStockS6AdaptationIsReversible(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run")
	original := []byte("#!/bin/sh\n\n" + stockS6Body + "\n")
	os.WriteFile(path, original, 0755)
	p := &ProcessManager{serviceDir: dir, execPath: "/a/core with 'quote'", configPath: "/private/config.json", assetDir: "/assets"}
	if err := p.prepareS6Run(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), managedS6Marker) || strings.Contains(string(data), "INTERNAL_REST_TOKEN") {
		t.Fatal(string(data))
	}
	if err := p.restoreS6Run(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != string(original) {
		t.Fatal("stock run script not restored")
	}
	if err := p.prepareS6Run(); err != nil {
		t.Fatal("adaptation is not repeatable:", err)
	}
}
func TestCustomS6RunIsPreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run")
	original := []byte("#!/bin/sh\nexec /custom/core --custom-options\n")
	os.WriteFile(path, original, 0755)
	p := &ProcessManager{serviceDir: dir}
	if err := p.prepareS6Run(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != string(original) {
		t.Fatal("custom service changed")
	}
}
