package stats

import (
	"context"
	"encoding/json"
	"github.com/aaaSaZaN/gowave-node/internal/plugin"
	"github.com/aaaSaZaN/gowave-node/internal/testutil"
	"github.com/aaaSaZaN/gowave-node/internal/xray"
	stats "github.com/aaaSaZaN/gowave-node/internal/xrayapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTrafficFailureUsesOriginalErrorContract(t *testing.T) {
	path := testutil.XrayServer(t, map[string]testutil.RPC{"StatsService/QueryStats": func(context.Context, proto.Message) (proto.Message, error) {
		return nil, status.Error(codes.Unavailable, "core unavailable")
	}})
	client := xray.NewClient(path)
	defer client.Close()
	service := NewStatsService(client, nil, plugin.NewService(&plugin.NftManager{}))
	response := httptest.NewRecorder()
	service.HandleGetUsersStats(response, httptest.NewRequest("POST", "/node/stats/get-users-stats", strings.NewReader(`{"reset":true}`)))
	var body map[string]interface{}
	json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != 500 || body["errorCode"] != "A011" || body["response"] != nil {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}
func TestBulkIPResponsePreservesIDs(t *testing.T) {
	path := testutil.XrayServer(t, map[string]testutil.RPC{"StatsService/GetUsersStats": func(context.Context, proto.Message) (proto.Message, error) {
		return &stats.GetUsersStatsResponse{Users: []*stats.UserStat{{Email: "00123", Ips: []*stats.OnlineIPEntry{{Ip: "192.0.2.1", LastSeen: 100}}}}}, nil
	}})
	client := xray.NewClient(path)
	defer client.Close()
	service := NewStatsService(client, nil, plugin.NewService(&plugin.NftManager{}))
	response := httptest.NewRecorder()
	service.HandleGetUsersIPList(response, httptest.NewRequest("GET", "/node/stats/get-users-ip-list", nil))
	var body struct {
		Response struct {
			Users []struct {
				UserID string `json:"userId"`
			}
		}
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Response.Users) != 1 || body.Response.Users[0].UserID != "00123" {
		t.Fatal(response.Body)
	}
}
