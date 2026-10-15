package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/remnawave/node-go/internal/apiutil"
	"github.com/remnawave/node-go/internal/plugin"
	"github.com/remnawave/node-go/internal/xray"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http"
	"sort"
	"sync"
	"time"
)

type XrayClient interface {
	AddUser(context.Context, xray.UserConfig) error
	RemoveUser(context.Context, string, string) error
	GetStatsOnlineIPList(context.Context, string, bool) ([]xray.UserIPSeen, error)
}
type Service struct {
	users      [64]sync.Mutex
	xrayClient XrayClient
	state      *xray.StateManager
	plugin     *plugin.Service
}

func NewService(client XrayClient, state *xray.StateManager, plugins *plugin.Service) *Service {
	return &Service{xrayClient: client, state: state, plugin: plugins}
}

type InboundUserData struct {
	Type       string `json:"type"` // trojan, vless, shadowsocks, shadowsocks22, hysteria
	Tag        string `json:"tag"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	UUID       string `json:"uuid"`
	Flow       string `json:"flow"`
	CipherType int32  `json:"cipherType"`
}

type AddUserRequest struct {
	Data     []InboundUserData `json:"data"`
	HashData struct {
		VlessUUID     string  `json:"vlessUuid"`
		PrevVlessUUID *string `json:"prevVlessUuid"`
	} `json:"hashData"`
}

type GenericSuccessResponse struct {
	Success bool    `json:"success"`
	Error   *string `json:"error"`
}

type RemoveUserRequest struct {
	Username string `json:"username"`
	HashData struct {
		VlessUUID string `json:"vlessUuid"`
	} `json:"hashData"`
}

type AddUsersRequest struct {
	AffectedInboundTags []string `json:"affectedInboundTags"`
	Users               []struct {
		InboundData []struct {
			Type string `json:"type"`
			Tag  string `json:"tag"`
			Flow string `json:"flow"`
		} `json:"inboundData"`
		UserData struct {
			UserID         string `json:"userId"`
			HashUUID       string `json:"hashUuid"`
			VlessUUID      string `json:"vlessUuid"`
			TrojanPassword string `json:"trojanPassword"`
			SSPassword     string `json:"ssPassword"`
		} `json:"userData"`
	} `json:"users"`
}

type RemoveUsersRequest struct {
	Users []struct {
		UserID   string `json:"userId"`
		HashUUID string `json:"hashUuid"`
	} `json:"users"`
}

type DropUsersConnectionsRequest struct {
	UserIDs []string `json:"userIds"`
}

type DropIPsRequest struct {
	IPs []string `json:"ips"`
}

func respond(w http.ResponseWriter, err error) {
	var message *string
	if err != nil {
		s := err.Error()
		message = &s
	}
	writeJSON(w, 200, map[string]interface{}{"response": GenericSuccessResponse{Success: err == nil, Error: message}})
}
func (s *Service) remove(ctx context.Context, user, hash string) error {
	var result error
	for _, tag := range s.state.GetXtlsConfigInbounds() {
		err := s.xrayClient.RemoveUser(ctx, tag, user)
		if err == nil || status.Code(err) == codes.NotFound {
			s.state.RemoveUserFromInbound(tag, hash)
		} else {
			result = errors.Join(result, err)
		}
	}
	return result
}
func (s *Service) dropUser(ctx context.Context, user string) error {
	ips, err := s.xrayClient.GetStatsOnlineIPList(ctx, user, true)
	if err != nil {
		return err
	}
	addresses := make([]string, 0, len(ips))
	for _, ip := range ips {
		addresses = append(addresses, ip.IP)
	}
	return s.plugin.DropConnections(addresses)
}
func (s *Service) HandleAddUser(w http.ResponseWriter, r *http.Request) {
	var req AddUserRequest
	if err := apiutil.Decode(r, &req); err != nil {
		respond(w, err)
		return
	}
	unlock := s.lockUsers([]string{req.Data[0].Username})
	defer unlock()
	s.state.Operation.RLock()
	defer s.state.Operation.RUnlock()
	if err := r.Context().Err(); err != nil {
		respond(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	user := req.Data[0].Username
	hash := req.HashData.VlessUUID
	if req.HashData.PrevVlessUUID != nil {
		hash = *req.HashData.PrevVlessUUID
	}
	for _, item := range req.Data {
		s.state.AddXtlsConfigInbound(item.Tag)
	}
	if req.HashData.PrevVlessUUID != nil {
		if err := s.dropUser(ctx, user); err != nil {
			respond(w, err)
			return
		}
	}
	if err := s.remove(ctx, user, hash); err != nil {
		respond(w, err)
		return
	}
	var result error
	for _, item := range req.Data {
		err := s.xrayClient.AddUser(ctx, xray.UserConfig{Type: item.Type, Tag: item.Tag, Username: item.Username, UUID: item.UUID, Password: item.Password, Flow: item.Flow, CipherType: item.CipherType})
		if err == nil {
			s.state.AddUserToInbound(item.Tag, req.HashData.VlessUUID)
		} else {
			result = errors.Join(result, err)
		}
	}
	respond(w, result)
}
func (s *Service) HandleRemoveUser(w http.ResponseWriter, r *http.Request) {
	var req RemoveUserRequest
	if err := apiutil.Decode(r, &req); err != nil {
		respond(w, err)
		return
	}
	unlock := s.lockUsers([]string{req.Username})
	defer unlock()
	s.state.Operation.RLock()
	defer s.state.Operation.RUnlock()
	if err := r.Context().Err(); err != nil {
		respond(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	// Revoke credentials even if IP collection/socket destruction is unavailable.
	dropErr := s.dropUser(ctx, req.Username)
	err := s.remove(ctx, req.Username, req.HashData.VlessUUID)
	respond(w, errors.Join(err, dropErr))
}
func (s *Service) HandleAddUsers(w http.ResponseWriter, r *http.Request) {
	var req AddUsersRequest
	if err := apiutil.Decode(r, &req); err != nil {
		respond(w, err)
		return
	}
	unlock := s.lockUsers(userIDs(req))
	defer unlock()
	s.state.Operation.RLock()
	defer s.state.Operation.RUnlock()
	if err := r.Context().Err(); err != nil {
		respond(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 55*time.Second)
	defer cancel()
	for _, tag := range req.AffectedInboundTags {
		s.state.AddXtlsConfigInbound(tag)
	}
	var result error
	for _, u := range req.Users {
		if err := s.remove(ctx, u.UserData.UserID, u.UserData.HashUUID); err != nil {
			result = errors.Join(result, err)
			continue
		}
		for _, item := range u.InboundData {
			cfg := xray.UserConfig{Type: item.Type, Tag: item.Tag, Username: u.UserData.UserID, UUID: u.UserData.VlessUUID, Flow: item.Flow}
			switch item.Type {
			case "trojan":
				cfg.Password = u.UserData.TrojanPassword
			case "shadowsocks":
				cfg.Password = u.UserData.SSPassword
			case "shadowsocks22":
				cfg.Password = base64.StdEncoding.EncodeToString([]byte(u.UserData.SSPassword))
			case "hysteria":
				cfg.Password = u.UserData.VlessUUID
			}
			if err := s.xrayClient.AddUser(ctx, cfg); err != nil {
				result = errors.Join(result, err)
			} else {
				s.state.AddUserToInbound(item.Tag, u.UserData.VlessUUID)
			}
		}
	}
	respond(w, result)
}
func (s *Service) HandleRemoveUsers(w http.ResponseWriter, r *http.Request) {
	var req RemoveUsersRequest
	if err := apiutil.Decode(r, &req); err != nil {
		respond(w, err)
		return
	}
	unlock := s.lockUsers(removedIDs(req))
	defer unlock()
	s.state.Operation.RLock()
	defer s.state.Operation.RUnlock()
	if err := r.Context().Err(); err != nil {
		respond(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 55*time.Second)
	defer cancel()
	var result error
	for _, u := range req.Users {
		dropErr := s.dropUser(ctx, u.UserID)
		result = errors.Join(result, s.remove(ctx, u.UserID, u.HashUUID), dropErr)
	}
	respond(w, result)
}
func (s *Service) HandleDropUsersConnections(w http.ResponseWriter, r *http.Request) {
	var req DropUsersConnectionsRequest
	if err := apiutil.Decode(r, &req); err != nil {
		respond(w, err)
		return
	}
	unlock := s.lockUsers(req.UserIDs)
	defer unlock()
	s.state.Operation.RLock()
	defer s.state.Operation.RUnlock()
	if err := r.Context().Err(); err != nil {
		respond(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var result error
	for _, user := range req.UserIDs {
		result = errors.Join(result, s.dropUser(ctx, user))
	}
	writeJSON(w, 200, map[string]interface{}{"response": map[string]bool{"success": result == nil}})
}
func (s *Service) HandleDropIPs(w http.ResponseWriter, r *http.Request) {
	var req DropIPsRequest
	if err := apiutil.Decode(r, &req); err != nil {
		respond(w, err)
		return
	}
	err := s.plugin.DropConnections(req.IPs)
	writeJSON(w, 200, map[string]interface{}{"response": map[string]bool{"success": err == nil}})
}
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Service) lockUsers(ids []string) func() {
	slots := []int{}
	seen := map[int]bool{}
	for _, id := range ids {
		var hash uint32 = 5381
		for _, b := range []byte(id) {
			hash = hash*33 + uint32(b)
		}
		slot := int(hash % uint32(len(s.users)))
		if !seen[slot] {
			seen[slot] = true
			slots = append(slots, slot)
		}
	}
	sort.Ints(slots)
	for _, slot := range slots {
		s.users[slot].Lock()
	}
	return func() {
		for i := len(slots) - 1; i >= 0; i-- {
			s.users[slots[i]].Unlock()
		}
	}
}
func userIDs(r AddUsersRequest) []string {
	ids := make([]string, 0, len(r.Users))
	for _, u := range r.Users {
		ids = append(ids, u.UserData.UserID)
	}
	return ids
}
func removedIDs(r RemoveUsersRequest) []string {
	ids := make([]string, 0, len(r.Users))
	for _, u := range r.Users {
		ids = append(ids, u.UserID)
	}
	return ids
}
