package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aaaSaZaN/gowave-node/internal/hashedset"
	"github.com/aaaSaZaN/gowave-node/internal/plugin"
	"github.com/aaaSaZaN/gowave-node/internal/xray"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeClient struct {
	removeErr error
	added     []xray.UserConfig
}

func (c *fakeClient) RemoveUser(context.Context, string, string) error { return c.removeErr }
func (c *fakeClient) AddUser(_ context.Context, u xray.UserConfig) error {
	c.added = append(c.added, u)
	return nil
}
func (c *fakeClient) GetStatsOnlineIPList(context.Context, string, bool) ([]xray.UserIPSeen, error) {
	return nil, nil
}

const id = "00000000-0000-4000-8000-000000000001"

func TestFailedRemovalPreservesHash(t *testing.T) {
	state := xray.NewStateManager()
	state.ExtractUsersFromConfig(xray.HashesInfo{EmptyConfig: "base", Inbounds: []xray.InboundHashInfo{{Tag: "in"}}}, map[string]interface{}{"inbounds": []interface{}{map[string]interface{}{"tag": "in", "settings": map[string]interface{}{"clients": []interface{}{map[string]interface{}{"id": id}}}}}})
	client := &fakeClient{removeErr: errors.New("RPC failed")}
	service := NewService(client, state, plugin.NewService(&plugin.NftManager{}))
	req := httptest.NewRequest("POST", "/node/handler/remove-user", strings.NewReader(`{"username":"1","hashData":{"vlessUuid":"`+id+`"}}`))
	response := httptest.NewRecorder()
	service.HandleRemoveUser(response, req)
	var body struct{ Response GenericSuccessResponse }
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Response.Success {
		t.Fatal("failed removal acknowledged")
	}
	if len(state.GetXtlsConfigInbounds()) != 1 {
		t.Fatal("failed removal changed hash state")
	}
	if state.IsNeedRestartCore(xray.HashesInfo{EmptyConfig: "base", Inbounds: []xray.InboundHashInfo{{Tag: "in", Hash: hashedset.New(id).Hash64String(), UsersCount: 1}}}) {
		t.Fatal("failed removal changed the user's hash")
	}
}
func TestBulkShadowsocks2022Encoding(t *testing.T) {
	client := &fakeClient{}
	service := NewService(client, xray.NewStateManager(), plugin.NewService(&plugin.NftManager{}))
	body := `{"affectedInboundTags":["in"],"users":[{"inboundData":[{"type":"shadowsocks22","tag":"in"}],"userData":{"userId":"00123","hashUuid":"` + id + `","vlessUuid":"` + id + `","trojanPassword":"t","ssPassword":"password"}}]}`
	response := httptest.NewRecorder()
	service.HandleAddUsers(response, httptest.NewRequest("POST", "/", strings.NewReader(body)))
	if len(client.added) != 1 || client.added[0].Password != "cGFzc3dvcmQ=" || client.added[0].Username != "00123" {
		t.Fatalf("accounts=%v response=%s", client.added, response.Body)
	}
}
func TestInvalidUserCannotMutate(t *testing.T) {
	client := &fakeClient{}
	service := NewService(client, xray.NewStateManager(), plugin.NewService(&plugin.NftManager{}))
	for _, handler := range []http.HandlerFunc{service.HandleAddUser, service.HandleRemoveUser, service.HandleAddUsers, service.HandleRemoveUsers, service.HandleDropUsersConnections, service.HandleDropIPs} {
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest("POST", "/", strings.NewReader(`{"data":[]}`)))
		if response.Code != http.StatusOK || len(client.added) != 0 {
			t.Fatalf("status=%d body=%s", response.Code, response.Body)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		var result GenericSuccessResponse
		if len(body) != 1 || body["response"] == nil {
			t.Fatalf("unexpected envelope: %s", response.Body)
		}
		if err := json.Unmarshal(body["response"], &result); err != nil {
			t.Fatal(err)
		}
		if result.Success || result.Error == nil || *result.Error == "" {
			t.Fatalf("unexpected response: %s", response.Body)
		}
	}
}

func BenchmarkBulk(b *testing.B) {
	client := &fakeClient{}
	service := NewService(client, xray.NewStateManager(), plugin.NewService(&plugin.NftManager{}))
	var users []map[string]interface{}
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
		users = append(users, map[string]interface{}{"inboundData": []map[string]string{{"type": "vless", "tag": "in", "flow": ""}}, "userData": map[string]string{"userId": strconv.Itoa(i), "hashUuid": id, "vlessUuid": id, "trojanPassword": "t", "ssPassword": "s"}})
	}
	body, _ := json.Marshal(map[string]interface{}{"affectedInboundTags": []string{"in"}, "users": users})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client.added = nil
		response := httptest.NewRecorder()
		service.HandleAddUsers(response, httptest.NewRequest("POST", "/", bytes.NewReader(body)))
		if response.Code != 200 || len(client.added) != 1000 {
			b.Fatal(response.Body)
		}
	}
}

func TestMutationWaitsForLifecycleOperation(t *testing.T) {
	state := xray.NewStateManager()
	client := &fakeClient{}
	service := NewService(client, state, plugin.NewService(&plugin.NftManager{}))
	state.Operation.Lock()
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(started)
		response := httptest.NewRecorder()
		service.HandleRemoveUsers(response, httptest.NewRequest("POST", "/", strings.NewReader(`{"users":[]}`)))
		close(done)
	}()
	<-started
	select {
	case <-done:
		state.Operation.Unlock()
		t.Fatal("mutation ran during lifecycle operation")
	case <-time.After(20 * time.Millisecond):
	}
	state.Operation.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("mutation did not resume")
	}
}
