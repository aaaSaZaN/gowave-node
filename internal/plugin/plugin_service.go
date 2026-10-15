package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/remnawave/node-go/internal/apiutil"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SharedListItem struct {
	Type  string        `json:"type"` // ipList or asList
	Name  string        `json:"name"` // starts with ext:
	Items []interface{} `json:"items"`
}

type PostStartConfig struct {
	Enabled bool `json:"enabled"`
	Webhook *struct {
		Enabled bool   `json:"enabled"`
		URL     string `json:"url"`
	} `json:"webhook"`
}

type NodePluginConfig struct {
	PostStart   *PostStartConfig `json:"postStart"`
	SharedLists []SharedListItem `json:"sharedLists"`

	TorrentBlocker *struct {
		Enabled         bool     `json:"enabled"`
		BlockDuration   int      `json:"blockDuration"`
		RulePlacement   *int     `json:"rulePlacement"`
		WebhookURL      *string  `json:"webhookUrl"`
		IncludeRuleTags []string `json:"includeRuleTags"`
		IgnoreLists     *struct {
			IP     []string `json:"ip"`
			UserID []int    `json:"userId"`
		} `json:"ignoreLists"`
	} `json:"torrentBlocker"`

	IngressFilter *struct {
		Enabled    bool     `json:"enabled"`
		BlockedIps []string `json:"blockedIps"`
	} `json:"ingressFilter"`

	EgressFilter *struct {
		Enabled      bool     `json:"enabled"`
		BlockedIps   []string `json:"blockedIps"`
		BlockedPorts []int    `json:"blockedPorts"`
	} `json:"egressFilter"`

	ConnectionDrop *struct {
		Enabled      bool     `json:"enabled"`
		WhitelistIps []string `json:"whitelistIps"`
	} `json:"connectionDrop"`

	PreStart *struct {
		Enabled        bool `json:"enabled"`
		CleanupSockets *struct {
			Enabled bool     `json:"enabled"`
			Files   []string `json:"files"`
		} `json:"cleanupSockets"`
	} `json:"preStart"`
}

type Service struct {
	mu             sync.RWMutex
	NftManager     *NftManager
	TorrentBlocker *TorrentBlockerState
	configHash     string
	pluginUUID     string
	pluginName     string
	whitelistIPs   map[string]struct{}
	cleanupFiles   []string
	postStart      *PostStartConfig
	resolveASN     func(uint32) ([]string, error)
}

func NewService(nft *NftManager) *Service {
	return &Service{
		NftManager:     nft,
		TorrentBlocker: NewTorrentBlockerState(),
		whitelistIPs:   make(map[string]struct{}),
	}
}

func (s *Service) IsWhitelisted(ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	if a.IsLoopback() || a.IsUnspecified() || ip == "255.255.255.255" {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.whitelistIPs[a.String()]; ok {
		return true
	}
	for raw := range s.whitelistIPs {
		if p, err := netip.ParsePrefix(raw); err == nil && p.Contains(a) {
			return true
		}
	}
	return false
}
func (s *Service) DropConnections(ips []string) error {
	filtered := []string{}
	seen := map[string]bool{}
	for _, ip := range ips {
		if !seen[ip] && !s.IsWhitelisted(ip) {
			filtered = append(filtered, ip)
			seen[ip] = true
		}
	}
	return KillSockets(filtered)
}
func (s *Service) SetASNResolver(resolve func(uint32) ([]string, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolveASN = resolve
}

func (s *Service) RunPreStart() error {
	s.mu.RLock()
	files := append([]string(nil), s.cleanupFiles...)
	s.mu.RUnlock()
	for _, pattern := range files {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		if len(matches) > 256 {
			return fmt.Errorf("socket cleanup pattern exceeds 256 matches: %s", pattern)
		}
		for _, file := range matches {
			fi, err := os.Lstat(file)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if fi.Mode()&os.ModeSocket == 0 {
				continue
			}
			if err := os.Remove(file); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) TorrentConfig() (bool, []string, int, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	enabled := s.TorrentBlocker.IsEnabled()
	tags := s.TorrentBlocker.IncludeRuleTags()
	position := s.TorrentBlocker.RulePlacement()
	normalized := append([]string{}, tags...)
	sort.Strings(normalized)
	data, _ := json.Marshal(struct {
		Enabled  bool
		Tags     []string
		Position int
	}{enabled, normalized, position})
	revision := sha256.Sum256(data)
	return enabled, tags, position, hex.EncodeToString(revision[:])
}
func (s *Service) RunPostStart(metadata map[string]interface{}) {
	s.mu.RLock()
	post := s.postStart
	s.mu.RUnlock()
	if post == nil || !post.Enabled || post.Webhook == nil || !post.Webhook.Enabled {
		return
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	go func() {
		data, _ := json.Marshal(map[string]interface{}{"scope": "service", "event": "service.core_started", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "metadata": metadata})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "POST", post.Webhook.URL, strings.NewReader(string(data)))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("[POST-START] webhook failed: %v", err)
			return
		}
		res.Body.Close()
	}()
}

type SyncRequest struct {
	Plugin *struct {
		Config map[string]interface{} `json:"config"`
		UUID   string                 `json:"uuid"`
		Name   string                 `json:"name"`
	} `json:"plugin"`
}

func (s *Service) Sync(req SyncRequest) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.Plugin == nil {
		return s.resetLocked() == nil
	}
	data, err := json.Marshal(req.Plugin.Config)
	if err != nil {
		return false
	}
	hash := sha256.Sum256(data)
	hashString := hex.EncodeToString(hash[:])
	if hashString == s.configHash {
		return true
	}
	var parsed NodePluginConfig
	if err = json.Unmarshal(data, &parsed); err != nil {
		return false
	}
	shared := map[string][]string{}
	for _, list := range parsed.SharedLists {
		if !strings.HasPrefix(list.Name, "ext:") {
			return false
		}
		switch list.Type {
		case "ipList":
			for _, v := range list.Items {
				ip, ok := v.(string)
				if !ok {
					return false
				}
				if _, _, err := validateAddress(ip); err != nil {
					return false
				}
				shared[list.Name] = append(shared[list.Name], ip)
			}
		case "asList":
			if s.resolveASN == nil {
				return false
			}
			for _, v := range list.Items {
				n, ok := v.(float64)
				if !ok || n < 1 || n > 4294967295 || n != float64(uint32(n)) {
					return false
				}
				prefixes, err := s.resolveASN(uint32(n))
				if err != nil {
					log.Printf("[PLUGIN] ASN resolution: %v", err)
					return false
				}
				shared[list.Name] = append(shared[list.Name], prefixes...)
			}
		default:
			return false
		}
		if _, ok := shared[list.Name]; !ok {
			shared[list.Name] = []string{}
		}
	}
	resolve := func(in []string) ([]string, error) {
		out := []string{}
		for _, ip := range in {
			if strings.HasPrefix(ip, "ext:") {
				values, ok := shared[ip]
				if !ok {
					return nil, fmt.Errorf("unknown shared list %s", ip)
				}
				out = append(out, values...)
			} else {
				if _, _, err := validateAddress(ip); err != nil {
					return nil, err
				}
				out = append(out, ip)
			}
		}
		return out, nil
	}
	var ingress, egress, ignored, whitelist []string
	var ports []int
	needsFirewall := false
	if p := parsed.IngressFilter; p != nil && p.Enabled {
		needsFirewall = true
		ingress, err = resolve(p.BlockedIps)
		if err != nil {
			return false
		}
	}
	if p := parsed.EgressFilter; p != nil && p.Enabled {
		needsFirewall = true
		egress, err = resolve(p.BlockedIps)
		if err != nil {
			return false
		}
		ports = p.BlockedPorts
	}
	if p := parsed.ConnectionDrop; p != nil && p.Enabled {
		whitelist, err = resolve(p.WhitelistIps)
		if err != nil {
			return false
		}
	}
	if p := parsed.TorrentBlocker; p != nil && p.Enabled {
		needsFirewall = true
		if p.BlockDuration < 0 {
			return false
		}
		if p.IgnoreLists != nil {
			ignored, err = resolve(p.IgnoreLists.IP)
			if err != nil {
				return false
			}
		}
		if p.WebhookURL != nil && *p.WebhookURL != "" && !validWebhook(*p.WebhookURL) {
			return false
		}
	}
	if p := parsed.PostStart; p != nil && p.Enabled && p.Webhook != nil && p.Webhook.Enabled && !validWebhook(p.Webhook.URL) {
		return false
	}
	if _, err := portCommand(ports, EgressFilterPortSetName); err != nil {
		return false
	}
	if needsFirewall && !s.NftManager.IsAvailable() {
		return false
	}
	if s.NftManager.IsAvailable() {
		if err := s.NftManager.SyncFilters(ingress, egress, ports); err != nil {
			log.Printf("[PLUGIN] sync: %v", err)
			return false
		}
	}
	// Publish only after the entire firewall transaction succeeds.
	s.whitelistIPs = map[string]struct{}{}
	for _, ip := range whitelist {
		s.whitelistIPs[ip] = struct{}{}
	}
	s.cleanupFiles = nil
	if p := parsed.PreStart; p != nil && p.Enabled && p.CleanupSockets != nil && p.CleanupSockets.Enabled {
		s.cleanupFiles = append([]string(nil), p.CleanupSockets.Files...)
	}
	s.postStart = parsed.PostStart
	if p := parsed.TorrentBlocker; p != nil && p.Enabled {
		users := []string{}
		if p.IgnoreLists != nil {
			for _, id := range p.IgnoreLists.UserID {
				users = append(users, strconv.Itoa(id))
			}
		}
		position := 0
		if p.RulePlacement != nil {
			position = *p.RulePlacement
		}
		url := ""
		if p.WebhookURL != nil {
			url = *p.WebhookURL
		}
		s.TorrentBlocker.Configure(true, p.BlockDuration, position, url, p.IncludeRuleTags, ignored, users)
	} else {
		s.TorrentBlocker.Reset()
	}
	s.configHash = hashString
	s.pluginUUID = req.Plugin.UUID
	s.pluginName = req.Plugin.Name
	return true
}
func validWebhook(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}
func (s *Service) Reset() error { s.mu.Lock(); defer s.mu.Unlock(); return s.resetLocked() }
func (s *Service) resetLocked() error {
	if s.NftManager.IsAvailable() {
		if err := s.NftManager.RecreateTables(); err != nil {
			return err
		}
	}
	s.TorrentBlocker.Reset()
	s.whitelistIPs = map[string]struct{}{}
	s.cleanupFiles = nil
	s.postStart = nil
	s.configHash = ""
	s.pluginUUID = ""
	s.pluginName = ""
	return nil
}

// http controllers

func (s *Service) HandleSync(w http.ResponseWriter, r *http.Request) {
	var req SyncRequest
	if err := apiutil.Decode(r, &req); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": map[string]interface{}{"accepted": false},
		})
		return
	}

	accepted := s.Sync(req)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{"accepted": accepted},
	})
}

func (s *Service) HandleCollectReports(w http.ResponseWriter, r *http.Request) {
	reports := s.TorrentBlocker.FlushReports()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"reports": reports,
		},
	})
}

type BlockIPsRequest struct {
	IPs []struct {
		IP      string `json:"ip"`
		Timeout int    `json:"timeout"`
	} `json:"ips"`
}

func (s *Service) HandleBlockIPs(w http.ResponseWriter, r *http.Request) {
	var req BlockIPsRequest
	if err := apiutil.Decode(r, &req); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": map[string]interface{}{"accepted": false},
		})
		return
	}

	for _, item := range req.IPs {
		if _, err := netip.ParseAddr(item.IP); err != nil || item.Timeout < 0 {
			writeJSON(w, 200, map[string]interface{}{"response": map[string]bool{"accepted": false}})
			return
		}
	}
	accepted := s.NftManager.IsAvailable()
	for _, item := range req.IPs {
		if err := s.NftManager.BlockIP(item.IP, item.Timeout); err != nil {
			accepted = false
			break
		}
		if err := s.DropConnections([]string{item.IP}); err != nil {
			log.Printf("[PLUGIN] connection drop: %v", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{"accepted": accepted},
	})
}

type UnblockIPsRequest struct {
	IPs []string `json:"ips"`
}

func (s *Service) HandleUnblockIPs(w http.ResponseWriter, r *http.Request) {
	var req UnblockIPsRequest
	if err := apiutil.Decode(r, &req); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": map[string]interface{}{"accepted": false},
		})
		return
	}

	err1 := s.NftManager.UnblockIPs(req.IPs, TorrentBlockerSetName)
	err2 := s.NftManager.UnblockIPs(req.IPs, IngressFilterIPSetName)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{"accepted": err1 == nil && err2 == nil},
	})
}

func (s *Service) HandleRecreateTables(w http.ResponseWriter, r *http.Request) {
	err := s.NftManager.RecreateTables()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{"accepted": err == nil},
	})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (r *SyncRequest) UnmarshalJSON(data []byte) error {
	type plain SyncRequest
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields["plugin"] == nil {
		return errors.New("plugin is required (use null to disable)")
	}
	return json.Unmarshal(data, (*plain)(r))
}
func (r *SyncRequest) Validate() error {
	if r.Plugin == nil {
		return nil
	}
	if r.Plugin.Config == nil || r.Plugin.Name == "" || !validUUID(r.Plugin.UUID) {
		return errors.New("plugin config, name, and UUID are required")
	}
	return nil
}
func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

func (r *BlockIPsRequest) Validate() error {
	if len(r.IPs) == 0 {
		return errors.New("ips are required")
	}
	for _, item := range r.IPs {
		if _, err := netip.ParseAddr(item.IP); err != nil || item.Timeout < 0 {
			return errors.New("invalid IP or timeout")
		}
	}
	return nil
}
func (r *UnblockIPsRequest) Validate() error {
	if r.IPs == nil {
		return errors.New("ips are required")
	}
	for _, ip := range r.IPs {
		if _, err := netip.ParseAddr(ip); err != nil {
			return err
		}
	}
	return nil
}
