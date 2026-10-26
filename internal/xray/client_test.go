package xray

import (
	"context"
	"errors"
	"github.com/aaaSaZaN/gowave-node/internal/testutil"
	stats "github.com/aaaSaZaN/gowave-node/internal/xrayapi"
	hysteria "github.com/aaaSaZaN/gowave-node/internal/xrayapi/hysteria"
	proxymancommand "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOnlineRPCAndStringIDs(t *testing.T) {
	path := testutil.XrayServer(t, map[string]testutil.RPC{
		"StatsService/GetStatsOnline": func(_ context.Context, m proto.Message) (proto.Message, error) {
			if m.(*stats.GetStatsRequest).Name != "user>>>00123>>>online" {
				t.Error(m)
			}
			return &stats.GetStatsResponse{Stat: &stats.Stat{Value: 1}}, nil
		},
		"StatsService/GetStatsOnlineIpList": func(_ context.Context, m proto.Message) (proto.Message, error) {
			return &stats.GetStatsOnlineIpListResponse{Ips: map[string]int64{"192.0.2.1": 100}}, nil
		},
		"StatsService/GetUsersStats": func(_ context.Context, m proto.Message) (proto.Message, error) {
			return &stats.GetUsersStatsResponse{Users: []*stats.UserStat{{Email: "00123", Ips: []*stats.OnlineIPEntry{{Ip: "192.0.2.1", LastSeen: 100}}}}}, nil
		},
	})
	c := NewClient(path)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	online, err := c.GetUserOnlineStatus(ctx, "00123")
	if err != nil || !online {
		t.Fatalf("online=%v err=%v", online, err)
	}
	ips, err := c.GetStatsOnlineIPList(ctx, "00123", true)
	if err != nil || len(ips) != 1 || ips[0].LastSeen.Unix() != 100 {
		t.Fatalf("ips=%v err=%v", ips, err)
	}
	users, err := c.GetUsersIPList(ctx)
	if err != nil || len(users) != 1 || users[0].UserID != "00123" {
		t.Fatalf("users=%v err=%v", users, err)
	}
}
func TestCoreExitIsReaped(t *testing.T) {
	dir := t.TempDir()
	core := filepath.Join(dir, "core")
	if err := os.WriteFile(core, []byte("#!/bin/sh\nexit 17\n"), 0700); err != nil {
		t.Fatal(err)
	}
	p := &ProcessManager{execPath: core, assetDir: dir, configPath: filepath.Join(dir, "run", "config.json")}
	if err := p.Start(map[string]interface{}{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		t.Fatal("child was not reaped")
	}
	if p.GetStatus().Up {
		t.Fatal("exited core reported up")
	}
	if err := p.Stop(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatal(err)
	}
	info, err := os.Stat(p.configPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private configuration: %v %v", info, err)
	}
}
func TestCleanupPreservesRegularFiles(t *testing.T) {
	file := filepath.Join(t.TempDir(), "important")
	os.WriteFile(file, []byte("keep"), 0600)
	cfg := map[string]interface{}{"inbounds": []interface{}{map[string]interface{}{"streamSettings": map[string]interface{}{"dsSettings": map[string]interface{}{"path": file}, "xhttpSettings": map[string]interface{}{"path": file}}}}}
	if err := cleanupInboundSockets(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("regular file removed")
	}
}
func TestTorrentConfigAndPolicy(t *testing.T) {
	base := map[string]interface{}{"inbounds": []interface{}{}, "routing": map[string]interface{}{"rules": []interface{}{map[string]interface{}{"ruleTag": "test", "outboundTag": "direct"}}}}
	cfg := GenerateApiConfig(base, TorrentBlockerState{Enabled: true, IncludeRuleTags: []string{"test"}}, InternalConfig{XtlsApiSocketPath: "/run/test/api.sock", SocketPath: "private", Token: "token"}, false)
	rules := cfg["routing"].(map[string]interface{})["rules"].([]interface{})
	if len(rules) != 3 || rules[1].(map[string]interface{})["outboundTag"] != "RW_TB_OUTBOUND_BLOCK" {
		t.Fatalf("rules=%v", rules)
	}
	if cfg["api"].(map[string]interface{})["listen"] != nil {
		t.Fatal("Unix API silently changed to TCP")
	}
}

func BenchmarkUserStats(b *testing.B) {
	counters := make([]*stats.Stat, 0, 16000)
	for i := 0; i < 8000; i++ {
		user := strconv.Itoa(i)
		counters = append(counters, &stats.Stat{Name: "user>>>" + user + ">>>traffic>>>uplink", Value: 100}, &stats.Stat{Name: "user>>>" + user + ">>>traffic>>>downlink", Value: 200})
	}
	path := testutil.XrayServer(b, map[string]testutil.RPC{"StatsService/QueryStats": func(context.Context, proto.Message) (proto.Message, error) {
		return &stats.QueryStatsResponse{Stat: counters}, nil
	}})
	client := NewClient(path)
	defer client.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		users, err := client.GetAllUsersStats(context.Background(), false)
		if err != nil || len(users) != 8000 {
			b.Fatalf("users=%d err=%v", len(users), err)
		}
	}
}

func TestAllAccountWireTypes(t *testing.T) {
	expected := map[string]string{"vless": "xray.proxy.vless.Account", "trojan": "xray.proxy.trojan.Account", "shadowsocks": "xray.proxy.shadowsocks.Account", "shadowsocks22": "xray.proxy.shadowsocks_2022.Account", "hysteria": "xray.proxy.hysteria.account.Account"}
	calls := 0
	path := testutil.XrayServer(t, map[string]testutil.RPC{"HandlerService/AlterInbound": func(_ context.Context, m proto.Message) (proto.Message, error) {
		request := m.(*proxymancommand.AlterInboundRequest)
		var operation proxymancommand.AddUserOperation
		if err := proto.Unmarshal(request.Operation.Value, &operation); err != nil {
			return nil, err
		}
		want := expected[request.Tag]
		if operation.User.Account.Type != want || operation.User.Email != "00123" {
			t.Errorf("wrong account: %s", operation.User.Account.Type)
		}
		if request.Tag == "hysteria" {
			var account hysteria.Account
			if err := proto.Unmarshal(operation.User.Account.Value, &account); err != nil || len(account.Auth) != 200 {
				t.Errorf("long Hysteria password not encoded correctly: %v", err)
			}
		}
		if request.Tag == "shadowsocks22" {
			var account shadowsocks_2022.Account
			if err := proto.Unmarshal(operation.User.Account.Value, &account); err != nil || account.Key != strings.Repeat("x", 200) {
				t.Errorf("Shadowsocks 2022 key not encoded correctly: %v", err)
			}
		}
		calls++
		return &proxymancommand.AlterInboundResponse{}, nil
	}})
	client := NewClient(path)
	defer client.Close()
	for kind := range expected {
		if err := client.AddUser(context.Background(), UserConfig{Type: kind, Tag: kind, Username: "00123", UUID: "00000000-0000-4000-8000-000000000001", Password: strings.Repeat("x", 200), CipherType: 5}); err != nil {
			t.Fatal(kind, err)
		}
	}
	if calls != len(expected) {
		t.Fatal(calls)
	}
}
