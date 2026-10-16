package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/remnawave/node-go/internal/auth"
	"github.com/remnawave/node-go/internal/config"
	"github.com/remnawave/node-go/internal/handler"
	"github.com/remnawave/node-go/internal/internalapi"
	"github.com/remnawave/node-go/internal/logger"
	"github.com/remnawave/node-go/internal/plugin"
	"github.com/remnawave/node-go/internal/stats"
	"github.com/remnawave/node-go/internal/testutil"
	"github.com/remnawave/node-go/internal/xray"
	api "github.com/remnawave/node-go/internal/xrayapi"
	commands "github.com/xtls/xray-core/app/proxyman/command"
	"google.golang.org/protobuf/proto"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOriginalRoutesAndResponseContracts(t *testing.T) {
	path := testutil.XrayServer(t, map[string]testutil.RPC{
		"StatsService/GetSysStats": func(context.Context, proto.Message) (proto.Message, error) {
			return &api.SysStatsResponse{NumGoroutine: 1}, nil
		},
		"StatsService/QueryStats": func(context.Context, proto.Message) (proto.Message, error) { return &api.QueryStatsResponse{}, nil },
		"StatsService/GetStatsOnline": func(context.Context, proto.Message) (proto.Message, error) {
			return &api.GetStatsResponse{Stat: &api.Stat{Value: 1}}, nil
		},
		"StatsService/GetStatsOnlineIpList": func(context.Context, proto.Message) (proto.Message, error) {
			return &api.GetStatsOnlineIpListResponse{}, nil
		},
		"StatsService/GetUsersStats": func(context.Context, proto.Message) (proto.Message, error) { return &api.GetUsersStatsResponse{}, nil },
		"HandlerService/AlterInbound": func(context.Context, proto.Message) (proto.Message, error) {
			return &commands.AlterInboundResponse{}, nil
		},
	})
	client := xray.NewClient(path)
	defer client.Close()
	state := xray.NewStateManager()
	process := &xray.ProcessManager{}
	plugins := plugin.NewService(&plugin.NftManager{})
	ring := logger.NewRingBuffer(10)
	cfg := &config.Config{InternalRestToken: "test"}
	controller := xray.NewController(cfg, state, process, client, plugins, func() interface{} { return stats.GetSystemCombined(nil) }, AppVersion)
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := x509.MarshalPKIXPublicKey(&private.PublicKey)
	cfg.Payload = &config.NodePayload{JWTPublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}))}
	authentication, err := auth.NewAuthManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()}).SignedString(private)
	if err != nil {
		t.Fatal(err)
	}
	router := newRouter(authentication, controller, stats.NewStatsService(client, nil, plugins), handler.NewService(client, state, plugins), plugins, state, process, internalapi.NewServer(cfg, state, plugins, process, ring, ring), ring, ring)
	id := "00000000-0000-4000-8000-000000000001"
	fixtures := []struct {
		method, path, body, key string
		status                  int
	}{
		{"POST", "/node/xray/start", `{}`, "errorCode", 400},
		{"GET", "/node/xray/stop", "", "isStopped", 200},
		{"GET", "/node/xray/healthcheck", "", "isAlive", 200},
		{"POST", "/node/stats/get-user-online-status", `{"username":"1"}`, "isOnline", 200},
		{"GET", "/node/stats/get-system-stats", "", "system", 200},
		{"POST", "/node/stats/get-users-stats", `{"reset":true}`, "users", 200},
		{"POST", "/node/stats/get-inbound-stats", `{"tag":"in","reset":false}`, "inbound", 200},
		{"POST", "/node/stats/get-outbound-stats", `{"tag":"out","reset":false}`, "outbound", 200},
		{"POST", "/node/stats/get-all-inbounds-stats", `{"reset":false}`, "inbounds", 200},
		{"POST", "/node/stats/get-all-outbounds-stats", `{"reset":false}`, "outbounds", 200},
		{"POST", "/node/stats/get-combined-stats", `{"reset":false}`, "inbounds", 200},
		{"POST", "/node/stats/get-user-ip-list", `{"userId":"1"}`, "ips", 200},
		{"GET", "/node/stats/get-users-ip-list", "", "users", 200},
		{"POST", "/node/stats/get-geocheck", `{"ip":"invalid"}`, "errorCode", 500},
		{"POST", "/node/handler/add-user", `{"data":[{"type":"vless","tag":"in","username":"1","uuid":"` + id + `","flow":""}],"hashData":{"vlessUuid":"` + id + `"}}`, "success", 200},
		{"POST", "/node/handler/remove-user", `{"username":"1","hashData":{"vlessUuid":"` + id + `"}}`, "success", 200},
		{"POST", "/node/handler/add-users", `{"affectedInboundTags":[],"users":[]}`, "success", 200},
		{"POST", "/node/handler/remove-users", `{"users":[]}`, "success", 200},
		{"POST", "/node/handler/drop-users-connections", `{"userIds":["1"]}`, "success", 200},
		{"POST", "/node/handler/drop-ips", `{"ips":["127.0.0.1"]}`, "success", 200},
		{"POST", "/node/plugin/sync", `{"plugin":null}`, "accepted", 200},
		{"POST", "/node/plugin/torrent-blocker/collect", `{}`, "reports", 200},
		{"POST", "/node/plugin/nftables/block-ips", `{"ips":[{"ip":"192.0.2.1","timeout":60}]}`, "accepted", 200},
		{"POST", "/node/plugin/nftables/unblock-ips", `{"ips":["192.0.2.1"]}`, "accepted", 200},
		{"POST", "/node/plugin/nftables/recreate-tables", `{}`, "accepted", 200},
	}
	routes := map[string]bool{}
	chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[method+" "+route] = true
		return nil
	})
	for _, fixture := range fixtures {
		t.Run(fixture.path, func(t *testing.T) {
			if !routes[fixture.method+" "+fixture.path] {
				t.Fatal("baseline route missing")
			}
			request := httptest.NewRequest(fixture.method, fixture.path, strings.NewReader(fixture.body))
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			var body map[string]interface{}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("invalid JSON: %s", response.Body)
			}
			if response.Code != fixture.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			if fixture.status == 200 {
				value, ok := body["response"].(map[string]interface{})
				if !ok {
					t.Fatal(body)
				}
				body = value
			}
			if _, ok := body[fixture.key]; !ok {
				t.Fatalf("missing %s: %s", fixture.key, response.Body)
			}
		})
	}
}
