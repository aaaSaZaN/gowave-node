package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func request(t *testing.T, config string) SyncRequest {
	t.Helper()
	var req SyncRequest
	if err := json.Unmarshal([]byte(`{"plugin":{"uuid":"00000000-0000-4000-8000-000000000001","name":"test","config":`+config+`}}`), &req); err != nil {
		t.Fatal(err)
	}
	return req
}
func TestPluginSyncPublishesOnlyAfterFirewallSuccess(t *testing.T) {
	var scripts []string
	fail := false
	manager := &NftManager{available: true, logging: true, run: func(_ context.Context, script string) error {
		scripts = append(scripts, script)
		if fail {
			return errors.New("kernel rejected transaction")
		}
		return nil
	}}
	s := NewService(manager)
	if !s.Sync(request(t, `{"torrentBlocker":{"enabled":true,"blockDuration":60,"ignoreLists":{"userId":[65],"ip":[]}}}`)) {
		t.Fatal("valid sync rejected")
	}
	if !s.TorrentBlocker.IsUserIgnored("65") || s.TorrentBlocker.IsUserIgnored("A") {
		t.Fatal("numeric user ID converted to a rune")
	}
	before := s.configHash
	fail = true
	if s.Sync(request(t, `{"torrentBlocker":{"enabled":false}}`)) {
		t.Fatal("failed firewall acknowledged")
	}
	if s.configHash != before || !s.TorrentBlocker.IsEnabled() {
		t.Fatal("failed transaction changed plugin state")
	}
	if len(scripts) == 0 || !strings.Contains(scripts[0], "log prefix") {
		t.Fatal("logging was not generated")
	}
}
func TestASNSyncAndInvalidInputs(t *testing.T) {
	manager := &NftManager{available: true, run: func(_ context.Context, script string) error {
		if !strings.Contains(script, "192.0.2.0/24") {
			t.Error("ASN prefixes absent")
		}
		return nil
	}}
	s := NewService(manager)
	s.SetASNResolver(func(asn uint32) ([]string, error) {
		if asn != 64500 {
			t.Error(asn)
		}
		return []string{"192.0.2.0/24"}, nil
	})
	if !s.Sync(request(t, `{"sharedLists":[{"type":"asList","name":"ext:test","items":[64500]}],"ingressFilter":{"enabled":true,"blockedIps":["ext:test"]}}`)) {
		t.Fatal("ASN list rejected")
	}
	if s.Sync(request(t, `{"ingressFilter":{"enabled":true,"blockedIps":["1.2.3.4; flush ruleset"]}}`)) {
		t.Fatal("nft injection accepted")
	}
}
func TestCleanupOnlySockets(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "important")
	os.WriteFile(file, []byte("keep"), 0600)
	socket := filepath.Join(dir, "stale.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	s := NewService(&NftManager{})
	s.cleanupFiles = []string{dir + "/*"}
	if err := s.RunPreStart(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("regular file deleted")
	}
	if _, err := os.Lstat(socket); !os.IsNotExist(err) {
		t.Fatal("socket was not removed")
	}
}
func TestDefaultConnectionExclusions(t *testing.T) {
	s := NewService(&NftManager{})
	for _, ip := range []string{"127.0.0.1", "127.1.2.3", "::1", "0.0.0.0"} {
		if !s.IsWhitelisted(ip) {
			t.Fatal(ip)
		}
	}
	if s.IsWhitelisted("192.0.2.1") {
		t.Fatal("public IP whitelisted")
	}
}

func TestRealASNIndex(t *testing.T) {
	path := os.Getenv("REMNANODE_TEST_ASN")
	if path == "" {
		t.Skip("set REMNANODE_TEST_ASN for the real portable asset")
	}
	index := NewASNIndex(path)
	prefixes, err := index.Resolve(13335)
	if err != nil || len(prefixes) == 0 {
		t.Fatalf("ASN lookup: count=%d err=%v", len(prefixes), err)
	}
	cached, err := index.Resolve(13335)
	if err != nil || len(cached) != len(prefixes) {
		t.Fatal("cached lookup differs")
	}
}

func TestFirewallOnlyChangesDoNotRestartCore(t *testing.T) {
	manager := &NftManager{available: true, run: func(context.Context, string) error { return nil }}
	service := NewService(manager)
	if !service.Sync(request(t, `{"ingressFilter":{"enabled":true,"blockedIps":["192.0.2.1"]}}`)) {
		t.Fatal("sync failed")
	}
	_, _, _, before := service.TorrentConfig()
	if !service.Sync(request(t, `{"ingressFilter":{"enabled":true,"blockedIps":["192.0.2.2"]}}`)) {
		t.Fatal("sync failed")
	}
	_, _, _, after := service.TorrentConfig()
	if before != after {
		t.Fatal("firewall-only update invalidated core routing")
	}
}
