package internalapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/remnawave/node-go/internal/config"
	"github.com/remnawave/node-go/internal/logger"
	"github.com/remnawave/node-go/internal/plugin"
	"github.com/remnawave/node-go/internal/xray"
)

var sourceRegex = regexp.MustCompile(`^(?:(?:tcp|udp):)?(?:\[(.+?)\]|(.+?))(?::(\d+))?$`)

type Server struct {
	cfg            *config.Config
	state          *xray.StateManager
	plugin         *plugin.Service
	procMgr        *xray.ProcessManager
	xrayRingBuffer *logger.RingBuffer
	nodeRingBuffer *logger.RingBuffer
	listener       net.Listener
	httpServer     *http.Server
}

func NewServer(
	cfg *config.Config,
	state *xray.StateManager,
	pluginSvc *plugin.Service,
	procMgr *xray.ProcessManager,
	xrayRingBuffer *logger.RingBuffer,
	nodeRingBuffer *logger.RingBuffer,
) *Server {
	return &Server{
		cfg:            cfg,
		state:          state,
		plugin:         pluginSvc,
		procMgr:        procMgr,
		xrayRingBuffer: xrayRingBuffer,
		nodeRingBuffer: nodeRingBuffer,
	}
}

func (s *Server) Start() error {
	r := chi.NewRouter()

	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.URL.Query().Get("token")
			if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.InternalRestToken)) != 1 {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	})

	r.Get("/internal/get-config", s.handleGetConfig)
	r.Post("/internal/webhook", s.HandleWebhook)
	r.Get("/internal/logs", s.handleLogs)
	r.Get("/internal/ready", func(w http.ResponseWriter, r *http.Request) {
		if !s.state.HasConfig() || !s.procMgr.GetStatus().Up {
			w.WriteHeader(503)
			return
		}
		client := xray.NewClient(s.cfg.XtlsApiSocketPath)
		defer client.Close()
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if _, err := client.GetSysStats(ctx); err != nil {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	})

	network, addr, err := config.SocketAddress(s.cfg.InternalSocketPath)
	if err != nil {
		return err
	}
	if network == "unix" && filepath.IsAbs(addr) {
		if info, err := os.Lstat(addr); err == nil && info.Mode()&os.ModeSocket != 0 {
			if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) == os.Geteuid() {
				conn, dialErr := net.DialTimeout("unix", addr, time.Second)
				if dialErr == nil {
					conn.Close()
					return fmt.Errorf("internal socket is already active")
				}
				if err := os.Remove(addr); err != nil {
					return err
				}
			}
		}
	}
	l, err := net.Listen(network, addr)
	if err != nil {
		return fmt.Errorf("failed to listen on internal socket: %w", err)
	}
	if err := config.SaveDiscovery(s.cfg); err != nil {
		l.Close()
		return err
	}
	s.listener = l

	s.httpServer = &http.Server{
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		if err := s.httpServer.Serve(l); err != nil && err != http.ErrServerClosed {
			log.Printf("[INTERNAL-API] Server error: %v", err)
		}
	}()

	log.Printf("[INTERNAL-API] Listening on internal socket: %s", s.cfg.InternalSocketPath)
	return nil
}

func (s *Server) Stop() {
	if d, err := config.ReadDiscovery(); err == nil && d.PID == os.Getpid() {
		_ = os.Remove(filepath.Join(config.RuntimeDir(), "connection.json"))
	}
	if s.httpServer != nil {
		_ = s.httpServer.Close()
	}
	if s.listener != nil {
		_ = s.listener.Close()
	}
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.state.GetXrayConfig()
	if cfg == nil {
		cfg = make(map[string]interface{})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cfg)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	logType := r.URL.Query().Get("type")
	if logType == "" {
		logType = "xray"
	}
	linesStr := r.URL.Query().Get("lines")
	lines := 100
	if l, err := strconv.Atoi(linesStr); err == nil && l > 0 {
		lines = l
	}

	var logs []string
	if logType == "node" {
		if s.nodeRingBuffer != nil {
			logs = s.nodeRingBuffer.GetLines(lines)
		}
	} else {
		if s.xrayRingBuffer != nil && s.xrayRingBuffer.Size() > 0 {
			logs = s.xrayRingBuffer.GetLines(lines)
		} else if s.procMgr != nil {
			logs = s.procMgr.TailLog(lines)
		}
	}

	if logs == nil {
		logs = []string{}
	}

	if r.URL.Query().Get("format") == "plain" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		for _, line := range logs {
			_, _ = fmt.Fprintln(w, line)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"response": map[string]interface{}{
			"logs": logs,
		},
	})
}

func (s *Server) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	var webhook plugin.XrayWebhookModel
	if err := json.NewDecoder(r.Body).Decode(&webhook); err != nil {
		log.Printf("[INTERNAL-API] Invalid webhook body: %v", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusOK)

	go s.processWebhook(webhook)
}

func extractIP(source *string) string {
	if source == nil || *source == "" {
		return ""
	}
	s := *source
	matches := sourceRegex.FindStringSubmatch(s)
	candidate := s
	if len(matches) > 1 {
		if matches[1] != "" {
			candidate = matches[1]
		} else if matches[2] != "" {
			candidate = matches[2]
		}
	}

	if addr, err := netip.ParseAddr(candidate); err == nil {
		return addr.String()
	}
	return ""
}

func (s *Server) processWebhook(webhook plugin.XrayWebhookModel) {
	tb := s.plugin.TorrentBlocker
	if !tb.IsEnabled() {
		return
	}

	ip := extractIP(webhook.Source)
	email := ""
	if webhook.Email != nil {
		email = *webhook.Email
	}

	if ip == "" || email == "" {
		return
	}

	if tb.IsIPIgnored(ip) || tb.IsUserIgnored(email) {
		log.Printf("[TORRENT-BLOCKER] IP %s or user %s is whitelisted, ignoring", ip, email)
		return
	}

	duration := tb.BlockDuration()
	blocked := false

	if s.plugin.NftManager.IsAvailable() {
		if err := s.plugin.NftManager.BlockIP(ip, duration); err == nil {
			blocked = true
			log.Printf("[TORRENT-BLOCKER] IP: %s, user: %s, blocked: true, duration: %ds", ip, email, duration)
		} else {
			log.Printf("[TORRENT-BLOCKER] Failed to block IP %s: %v", ip, err)
		}
	}

	s.plugin.DropConnections([]string{ip})

	report := plugin.TorrentBlockerReport{
		ActionReport: plugin.ActionReport{
			Blocked:       blocked,
			IP:            ip,
			BlockDuration: duration,
			WillUnblockAt: time.Now().Add(time.Duration(duration) * time.Second).UTC(),
			UserID:        email,
			ProcessedAt:   time.Now().UTC(),
		},
		XrayReport: webhook,
	}

	tb.AddReport(report)
}
