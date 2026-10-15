package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/remnawave/node-go/internal/apiutil"
	"github.com/remnawave/node-go/internal/auth"
	"github.com/remnawave/node-go/internal/cli"
	"github.com/remnawave/node-go/internal/config"
	"github.com/remnawave/node-go/internal/handler"
	"github.com/remnawave/node-go/internal/internalapi"
	"github.com/remnawave/node-go/internal/lock"
	"github.com/remnawave/node-go/internal/logger"
	"github.com/remnawave/node-go/internal/plugin"
	"github.com/remnawave/node-go/internal/stats"
	"github.com/remnawave/node-go/internal/tlsutil"
	"github.com/remnawave/node-go/internal/updater"
	"github.com/remnawave/node-go/internal/xray"
)

var AppVersion = "1.0.5"

func main() {
	config.LoadEnv()
	progName := filepath.Base(os.Args[0])
	if progName == "xlogs" {
		cli.RunLogsCLI("xray", os.Args[1:])
		return
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "xlogs":
			cli.RunLogsCLI("xray", os.Args[2:])
			return
		case "logs", "nodelogs":
			cli.RunLogsCLI("node", os.Args[2:])
			return
		case "_finish-update":
			if exe, err := os.Executable(); err == nil && strings.HasPrefix(filepath.Base(exe), ".update-monitor-") {
				defer os.Remove(exe)
			}
			if len(os.Args) != 5 {
				os.Exit(2)
			}
			pid, _ := strconv.Atoi(os.Args[4])
			if err := cli.CompleteUpdate(os.Args[2], os.Args[3] == "true", pid); err != nil {
				log.Print(err)
				os.Exit(1)
			}
			return
		case "status":
			if len(os.Args) > 2 && os.Args[2] == "--wait-ready" {
				if err := cli.WaitReady(true, 120*time.Second, 0); err != nil {
					log.Print(err)
					os.Exit(1)
				}
				return
			}
			cli.RunStatusCLI(AppVersion)
			return
		case "restart":
			cli.RunRestartCLI()
			return
		case "update":
			cli.RunUpdateCLI(AppVersion, os.Args[2:])
			return
		case "core":
			cli.RunCoreCLI(os.Args[2:])
			return
		case "version", "-v", "--version":
			fmt.Printf("Remnawave Node (Go) v%s\n", AppVersion)
			return
		case "help", "-h", "--help":
			fmt.Printf("Usage: %s [command]\n\nCommands:\n  status                   Show node status and Xray core info\n  restart                  Restart Remnanode service\n  logs  [-n lines] [-f]    View Node service logs\n  xlogs [-n lines] [-f]    View Xray core logs\n  update [flags]           Update Remnanode binary\n  core install [flags]     Install or update Xray core\n  core update              Update Xray core to latest\n  version                  Show version\n", os.Args[0])
			return
		}
	}

	xrayRingBuffer := logger.NewRingBuffer(5000)
	nodeRingBuffer := logger.NewRingBuffer(5000)

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetOutput(io.MultiWriter(os.Stderr, nodeRingBuffer))
	log.Printf("[INIT] Starting Remnawave Node (Go) v%s...", AppVersion)

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("[FATAL] Failed to load configuration: %v", err)
	}

	if !lock.AcquireInstanceLock() {
		log.Fatal(lock.DuplicateInstanceMessage())
	}
	defer lock.ReleaseInstanceLock()

	processMgr := xray.NewProcessManager(cfg.XrayS6ServiceDir, xrayRingBuffer)
	xrayClient := xray.NewClient(cfg.XtlsApiSocketPath)
	defer xrayClient.Close()

	stateMgr := xray.NewStateManager()

	nftMgr := plugin.NewNftManager(cfg.NftablesLogging, cfg.NftablesAcceptReplyTraffic)
	defer func() {
		if nftMgr.IsAvailable() {
			if err := nftMgr.DeleteTable(); err != nil {
				log.Printf("[PLUGIN] shutdown: %v", err)
			}
		}
	}()
	pluginSvc := plugin.NewService(nftMgr)
	asnPath := os.Getenv("ASN_INDEX_PATH")
	if asnPath == "" {
		asnPath = "/usr/local/share/asn/asn-prefixes.zip"
	}
	pluginSvc.SetASNResolver(plugin.NewASNIndex(asnPath).Resolve)

	netPoller := stats.NewNetworkPoller()
	statsSvc := stats.NewStatsService(xrayClient, netPoller, pluginSvc)

	handlerSvc := handler.NewService(xrayClient, stateMgr, pluginSvc)

	xrayCtrl := xray.NewController(
		cfg,
		stateMgr,
		processMgr,
		xrayClient,
		pluginSvc,
		func() interface{} {
			return stats.GetSystemCombined(netPoller)
		}, AppVersion,
	)

	internalServer := internalapi.NewServer(cfg, stateMgr, pluginSvc, processMgr, xrayRingBuffer, nodeRingBuffer)
	if err := internalServer.Start(); err != nil {
		log.Fatalf("[FATAL] Internal socket server could not bind: %v", err)
	}
	defer internalServer.Stop()

	authMgr, err := auth.NewAuthManager(cfg)
	if err != nil {
		log.Fatalf("[FATAL] Failed to initialize JWT auth: %v", err)
	}

	tlsConfig, err := tlsutil.SetupServerTLS(cfg)
	if err != nil {
		log.Fatalf("[FATAL] Failed to setup mTLS configuration: %v", err)
	}

	r := newRouter(authMgr, xrayCtrl, statsSvc, handlerSvc, pluginSvc, stateMgr, processMgr, internalServer, xrayRingBuffer, nodeRingBuffer)

	addr := fmt.Sprintf(":%d", cfg.NodePort)
	server := &http.Server{
		Addr:              addr,
		Handler:           r,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	sysInfo := stats.GetSystemInfo()
	ver, _ := processMgr.GetCoreVersion()
	box := config.RenderBox(fmt.Sprintf("Remnawave Node v%s (Go)", AppVersion), []string{
		"Docs → https://docs.rw\nCommunity → https://t.me/remnawave",
		fmt.Sprintf("API Port: %d (mTLS TLSv1.3)", cfg.NodePort),
		fmt.Sprintf("Xray Core: v%s\nXray Path: %s", ver, processMgr.GetExecPath()),
		fmt.Sprintf("%dC, %s, %d MB RAM", sysInfo.CPUs, sysInfo.CPUModel, sysInfo.MemoryTotal/(1024*1024)),
		fmt.Sprintf("Kernel: %s %s %s", sysInfo.Release, sysInfo.Type, sysInfo.Platform),
		fmt.Sprintf("Network Interfaces: %v", sysInfo.NetworkInterfaces),
	}, 62, true)
	log.Println("\n" + box)

	go func() {
		log.Printf("[SERVER] Listening with mTLS on %s", addr)
		if err := server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[FATAL] Server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[SHUTDOWN] Shutting down Remnawave Node...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = server.Shutdown(ctx)
	_ = processMgr.Stop()
	log.Println("[SHUTDOWN] Remnawave Node terminated cleanly.")
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func newRouter(authMgr *auth.AuthManager, xrayCtrl *xray.Controller, statsSvc *stats.StatsService, handlerSvc *handler.Service, pluginSvc *plugin.Service, stateMgr *xray.StateManager, processMgr *xray.ProcessManager, internalServer *internalapi.Server, xrayRingBuffer, nodeRingBuffer *logger.RingBuffer) chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(apiutil.Decompress)

	r.Route("/node", func(r chi.Router) {
		r.Use(authMgr.JWTMiddleware)

		r.Get("/logs", func(w http.ResponseWriter, req *http.Request) {
			logType := req.URL.Query().Get("type")
			if logType == "" {
				logType = "xray"
			}
			linesStr := req.URL.Query().Get("lines")
			lines := 500
			if l, err := strconv.Atoi(linesStr); err == nil && l > 0 {
				lines = l
			}

			var logs []string
			if logType == "node" {
				logs = nodeRingBuffer.GetLines(lines)
			} else {
				if xrayRingBuffer.Size() > 0 {
					logs = xrayRingBuffer.GetLines(lines)
				} else {
					logs = processMgr.TailLog(lines)
				}
			}
			if logs == nil {
				logs = []string{}
			}

			writeJSON(w, http.StatusOK, map[string]interface{}{
				"response": map[string]interface{}{
					"logs": logs,
				},
			})
		})

		r.Get("/updates/check", func(w http.ResponseWriter, req *http.Request) {
			repo := req.URL.Query().Get("repo")
			if repo == "" {
				repo = updater.NodeRepository
			}
			releases, err := updater.FetchReleases(repo)
			if err != nil {
				writeJSON(w, http.StatusOK, map[string]interface{}{
					"response": map[string]interface{}{
						"error": err.Error(),
					},
				})
				return
			}
			var latestRelease *updater.ReleaseInfo
			var latestPrerelease *updater.ReleaseInfo
			for _, rel := range releases {
				if rel.Prerelease && latestPrerelease == nil {
					temp := rel
					latestPrerelease = &temp
				} else if !rel.Prerelease && latestRelease == nil {
					temp := rel
					latestRelease = &temp
				}
			}
			coreVer, _ := processMgr.GetCoreVersion()
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"response": updater.CheckResult{
					CurrentVersion:   AppVersion,
					LatestRelease:    latestRelease,
					LatestPrerelease: latestPrerelease,
					AllReleases:      releases,
					Architecture:     updater.GetCurrentArch(),
					OS:               updater.GetCurrentOS(),
					CurrentCoreVer:   coreVer,
				},
			})
		})

		r.Post("/updates/apply", func(w http.ResponseWriter, req *http.Request) {
			stateMgr.Operation.Lock()
			defer stateMgr.Operation.Unlock()
			var body struct {
				Type    string `json:"type"`
				Repo    string `json:"repo"`
				Version string `json:"version"`
			}
			if err := apiutil.Decode(req, &body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid body"})
				return
			}

			if (body.Type != "node" && body.Type != "core") || body.Version == "" {
				writeJSON(w, 400, map[string]string{"error": "type (node/core) and version are required"})
				return
			}
			repo := body.Repo
			targetBinary := "remnanode"
			destPath, _ := os.Executable()
			if destPath == "" {
				destPath = "/usr/local/bin/remnanode"
			}
			destPath, _ = filepath.EvalSymlinks(destPath)

			if body.Type == "core" {
				targetBinary = "rw-core"
				destPath = processMgr.GetExecPath()
				if destPath == "" {
					if exe, err := os.Executable(); err == nil {
						destPath = filepath.Join(filepath.Dir(exe), "rw-core")
					} else {
						destPath = "./rw-core"
					}
				}
				if repo == "" {
					repo = "XTLS/Xray-core"
				}
			} else if repo == "" {
				repo = updater.NodeRepository
			}

			exact, err := updater.FetchRelease(repo, body.Version)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			chosenRelease := &exact

			asset, err := updater.MatchAsset(chosenRelease.Assets, updater.GetCurrentOS(), updater.GetCurrentArch(), targetBinary)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
				return
			}

			tmpPath, err := updater.DownloadReleaseBinary(*chosenRelease, *asset, targetBinary)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
				return
			}

			defer os.Remove(tmpPath)
			if body.Type == "core" {
				if err := updater.ValidateCoreBinary(tmpPath); err != nil {
					_ = os.Remove(tmpPath)
					writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
					return
				}
			}

			if body.Type != "core" {
				if err := updater.ValidateNodeBinary(tmpPath); err != nil {
					os.Remove(tmpPath)
					writeJSON(w, 400, map[string]string{"error": err.Error()})
					return
				}
			}
			if err := req.Context().Err(); err != nil {
				writeJSON(w, 408, map[string]string{"error": err.Error()})
				return
			}
			unlock, err := updater.LockTarget(destPath)
			if err != nil {
				writeJSON(w, 409, map[string]string{"error": err.Error()})
				return
			}
			defer unlock()
			var installErr error
			if body.Type == "core" {
				installErr = updater.InstallStockCore(tmpPath, destPath)
			} else {
				installErr = updater.Backup(destPath, ".previous")
				if installErr == nil {
					installErr = updater.AtomicReplace(tmpPath, destPath)
				}
			}
			if installErr != nil {
				writeJSON(w, 500, map[string]string{"error": installErr.Error()})
				return
			}

			if err := updater.ScheduleRestart(destPath, processMgr.GetStatus().Up); err != nil {
				_ = updater.Rollback(destPath)
				writeJSON(w, 500, map[string]string{"error": err.Error()})
				return
			}

			writeJSON(w, http.StatusOK, map[string]interface{}{
				"response": map[string]interface{}{
					"success": true,
					"version": chosenRelease.TagName,
				},
			})
		})

		r.Route("/xray", func(r chi.Router) {
			r.Post("/start", xrayCtrl.HandleStart)
			r.Get("/stop", xrayCtrl.HandleStop)
			r.Get("/node-health-check", xrayCtrl.HandleHealthCheck)
			r.Get("/healthcheck", xrayCtrl.HandleHealthCheck)
			r.Get("/version", xrayCtrl.HandleVersion)
		})
		r.Get("/version", xrayCtrl.HandleVersion)

		r.Route("/stats", func(r chi.Router) {
			r.Post("/get-user-online-status", statsSvc.HandleGetUserOnlineStatus)
			r.Get("/get-system-stats", statsSvc.HandleGetSystemStats)
			r.Post("/get-users-stats", statsSvc.HandleGetUsersStats)
			r.Post("/get-inbound-stats", statsSvc.HandleGetInboundStats)
			r.Post("/get-outbound-stats", statsSvc.HandleGetOutboundStats)
			r.Post("/get-all-inbounds-stats", statsSvc.HandleGetAllInboundsStats)
			r.Post("/get-all-outbounds-stats", statsSvc.HandleGetAllOutboundsStats)
			r.Post("/get-combined-stats", statsSvc.HandleGetCombinedStats)
			r.Post("/get-user-ip-list", statsSvc.HandleGetUserIPList)
			r.Get("/get-users-ip-list", statsSvc.HandleGetUsersIPList)
			r.Post("/get-geocheck", statsSvc.HandleGetGeocheck)
		})

		r.Route("/handler", func(r chi.Router) {
			r.Post("/add-user", handlerSvc.HandleAddUser)
			r.Post("/remove-user", handlerSvc.HandleRemoveUser)
			r.Post("/add-users", handlerSvc.HandleAddUsers)
			r.Post("/remove-users", handlerSvc.HandleRemoveUsers)
			r.Post("/drop-users-connections", handlerSvc.HandleDropUsersConnections)
			r.Post("/drop-ips", handlerSvc.HandleDropIPs)
		})

		r.Route("/plugin", func(r chi.Router) {
			r.Post("/sync", func(w http.ResponseWriter, req *http.Request) {
				stateMgr.Operation.Lock()
				defer stateMgr.Operation.Unlock()
				_, _, _, beforeRevision := pluginSvc.TorrentConfig()
				var body plugin.SyncRequest
				if err := apiutil.Decode(req, &body); err != nil {
					apiutil.Error(w, req, 400, "E000", err)
					return
				}
				accepted := pluginSvc.Sync(body)
				_, _, _, revision := pluginSvc.TorrentConfig()
				if accepted && revision != beforeRevision {
					xrayCtrl.Invalidate()
				}
				writeJSON(w, 200, map[string]interface{}{"response": map[string]bool{"accepted": accepted}})
			})
			r.Post("/torrent-blocker/collect", pluginSvc.HandleCollectReports)
			r.Post("/nftables/block-ips", pluginSvc.HandleBlockIPs)
			r.Post("/nftables/unblock-ips", pluginSvc.HandleUnblockIPs)
			r.Post("/nftables/recreate-tables", pluginSvc.HandleRecreateTables)
		})
	})

	r.Route("/internal", func(r chi.Router) {
		r.Use(authMgr.InternalTokenMiddleware)
		r.Get("/get-config", func(w http.ResponseWriter, req *http.Request) {
			cfg := stateMgr.GetXrayConfig()
			if cfg == nil {
				cfg = make(map[string]interface{})
			}
			writeJSON(w, http.StatusOK, cfg)
		})
		r.Post("/webhook", internalServer.HandleWebhook)
	})

	return r
}
