package xray

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRealCoreLifecycle(t *testing.T) {
	core := os.Getenv("REMNANODE_TEST_CORE")
	if core == "" {
		t.Skip("set REMNANODE_TEST_CORE to run real-core integration")
	}
	dir := t.TempDir()
	socket := filepath.Join(dir, "api.sock")
	p := &ProcessManager{execPath: core, assetDir: dir, configPath: filepath.Join(dir, "run", "config.json")}
	cfg := GenerateApiConfig(map[string]interface{}{"inbounds": []interface{}{}, "outbounds": []interface{}{map[string]interface{}{"tag": "direct", "protocol": "freedom"}}}, TorrentBlockerState{}, InternalConfig{XtlsApiSocketPath: socket}, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.ValidateConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err := p.Start(cfg); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	client := NewClient(socket)
	defer client.Close()
	var err error
	for ctx.Err() == nil {
		call, done := context.WithTimeout(ctx, 200*time.Millisecond)
		_, err = client.GetSysStats(call)
		done()
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Stop(); err != nil {
		t.Fatal(err)
	}
	if p.GetStatus().Up {
		t.Fatal("core still reported up after stop")
	}
}
