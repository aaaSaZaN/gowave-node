package xray

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/aaaSaZaN/gowave-node/internal/apiutil"
	"github.com/aaaSaZaN/gowave-node/internal/updater"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aaaSaZaN/gowave-node/internal/config"
	"github.com/aaaSaZaN/gowave-node/internal/integration"
)

type PluginInterface interface {
	RunPreStart() error
	Reset() error
	TorrentConfig() (bool, []string, int, string)
	RunPostStart(map[string]interface{})
}
type Controller struct {
	integrations            *integration.Manager
	mu                      sync.Mutex
	cfg                     *config.Config
	state                   *StateManager
	process                 *ProcessManager
	client                  *Client
	plugin                  PluginInterface
	systemStatsGetter       func() interface{}
	isXrayOnline            bool
	isXrayStartedProcessing bool
	xrayVersion             *string
	nodeVersion             string
	pluginRevision          string
}

func NewController(cfg *config.Config, state *StateManager, process *ProcessManager, client *Client, plugin PluginInterface, systemStatsGetter func() interface{}, nodeVersion string) *Controller {
	c := &Controller{integrations: integration.New(), cfg: cfg, state: state, process: process, client: client, plugin: plugin, systemStatsGetter: systemStatsGetter, nodeVersion: nodeVersion}
	if version, err := process.GetCoreVersion(); err == nil {
		c.xrayVersion = &version
	}
	return c
}

type StartXrayRequest struct {
	Internals struct {
		ForceRestart bool                   `json:"forceRestart"`
		Hashes       HashesInfo             `json:"hashes"`
		Metadata     map[string]interface{} `json:"metadata"`
		Integrations map[string]interface{} `json:"integrations"`
	} `json:"internals"`
	XrayConfig map[string]interface{} `json:"xrayConfig"`
}

func (req *StartXrayRequest) Validate() error {
	if req.XrayConfig == nil || req.Internals.Hashes.EmptyConfig == "" || req.Internals.Hashes.Inbounds == nil {
		return errors.New("xrayConfig and internals.hashes are required")
	}
	tags := map[string]bool{}
	for _, inbound := range req.Internals.Hashes.Inbounds {
		if inbound.Tag == "" || inbound.Hash == "" || inbound.UsersCount < 0 || tags[inbound.Tag] {
			return errors.New("invalid inbound hash")
		}
		tags[inbound.Tag] = true
	}
	return nil
}
func (c *Controller) startResponse(w http.ResponseWriter, started bool, err error) {
	c.mu.Lock()
	version := c.xrayVersion
	c.mu.Unlock()
	var message interface{}
	if err != nil {
		message = err.Error()
	}
	writeJSON(w, 200, map[string]interface{}{"response": map[string]interface{}{"isStarted": started, "version": version, "error": message, "nodeInformation": map[string]string{"version": c.nodeVersion}, "system": c.systemStatsGetter()}})
}
func (c *Controller) HandleStart(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	if c.isXrayStartedProcessing {
		c.mu.Unlock()
		c.startResponse(w, false, errors.New("Request already in progress"))
		return
	}
	c.isXrayStartedProcessing = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.isXrayStartedProcessing = false; c.mu.Unlock() }()
	var req StartXrayRequest
	if err := apiutil.Decode(r, &req); err != nil {
		apiutil.Error(w, r, 400, "E000", err)
		return
	}
	c.state.Operation.Lock()
	defer c.state.Operation.Unlock()
	if err := r.Context().Err(); err != nil {
		apiutil.Error(w, r, 408, "E000", err)
		return
	}
	if err := c.integrations.Sync(r.Context(), req.Internals.Integrations, req.Internals.Metadata); err != nil {
		c.startResponse(w, false, err)
		return
	}
	enabled, tags, position, revision := c.plugin.TorrentConfig()
	c.mu.Lock()
	online := c.isXrayOnline
	oldRevision := c.pluginRevision
	c.mu.Unlock()
	if online && !c.cfg.DisableHashedSetCheck && !req.Internals.ForceRestart && oldRevision == revision {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		_, err := c.client.GetSysStats(ctx)
		cancel()
		if err == nil && !c.state.IsNeedRestartCore(req.Internals.Hashes) {
			c.startResponse(w, true, nil)
			return
		}
	}
	if err := c.process.Prepare(r.Context(), req.XrayConfig["geodata"]); err != nil {
		c.startResponse(w, false, err)
		return
	}
	full := GenerateApiConfig(req.XrayConfig, TorrentBlockerState{Enabled: enabled, IncludeRuleTags: tags, RulePosition: position}, InternalConfig{SocketPath: c.cfg.InternalSocketPath, Token: c.cfg.InternalRestToken, XtlsApiSocketPath: c.cfg.XtlsApiSocketPath}, updater.HasNetAdmin())
	if err := c.process.ValidateConfig(r.Context(), full); err != nil {
		log.Printf("[XRAY] new config rejected, the running core keeps the previous one: %v", err)
		c.startResponse(w, false, err)
		return
	}
	if err := c.process.Stop(); err != nil {
		c.startResponse(w, false, err)
		return
	}
	c.mu.Lock()
	c.isXrayOnline = false
	c.mu.Unlock()
	c.client.Close()
	if err := c.plugin.RunPreStart(); err != nil {
		c.startResponse(w, false, err)
		return
	}
	if err := cleanupInboundSockets(full); err != nil {
		c.startResponse(w, false, err)
		return
	}
	c.state.ExtractUsersFromConfig(req.Internals.Hashes, full)
	if err := c.process.Start(full); err != nil {
		if stopErr := c.process.Stop(); stopErr != nil {
			c.startResponse(w, false, errors.Join(err, stopErr))
			return
		}
		c.state.Cleanup()
		if rollbackErr := c.process.RollbackActivation(); rollbackErr != nil {
			log.Printf("[XRAY] rollback: %v", rollbackErr)
		}
		c.startResponse(w, false, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		call, cancelCall := context.WithTimeout(ctx, time.Second)
		_, err := c.client.GetSysStats(call)
		cancelCall()
		if err == nil {
			break
		}
		if !c.process.GetStatus().Up {
			_ = c.process.Stop()
			c.state.Invalidate()
			_ = c.process.RollbackActivation()
			c.startResponse(w, false, errors.New("Xray Core exited before becoming ready"))
			return
		}
		select {
		case <-ctx.Done():
			_ = c.process.Stop()
			c.state.Invalidate()
			_ = c.process.RollbackActivation()
			c.startResponse(w, false, ctx.Err())
			return
		case <-ticker.C:
		}
	}
	version, err := c.process.GetCoreVersion()
	c.mu.Lock()
	c.isXrayOnline = true
	c.pluginRevision = revision
	if err == nil {
		c.xrayVersion = &version
	}
	c.mu.Unlock()
	c.process.CommitActivation()
	c.plugin.RunPostStart(req.Internals.Metadata)
	c.startResponse(w, true, nil)
}
func (c *Controller) HandleStop(w http.ResponseWriter, r *http.Request) {
	c.state.Operation.Lock()
	defer c.state.Operation.Unlock()
	if err := r.Context().Err(); err != nil {
		apiutil.Error(w, r, 408, "E000", err)
		return
	}
	err := c.process.Stop()
	if err == nil {
		err = c.integrations.Stop(r.Context())
	}
	if err == nil {
		err = c.plugin.Reset()
	}
	if err == nil {
		c.client.Close()
		c.state.Cleanup()
		c.mu.Lock()
		c.isXrayOnline = false
		c.mu.Unlock()
	}
	writeJSON(w, 200, map[string]interface{}{"response": map[string]bool{"isStopped": err == nil}})
}
func (c *Controller) Invalidate() {
	// Called while the shared operation gate is held by plugin synchronization.
	if err := c.process.Stop(); err != nil {
		log.Printf("[XRAY] plugin restart: %v", err)
	}
	c.client.Close()
	c.state.Invalidate()
	c.mu.Lock()
	c.isXrayOnline = false
	c.mu.Unlock()
}

func (c *Controller) isCustomCore() bool { return c.process.IsCustom() }

func (c *Controller) HandleHealthCheck(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"isAlive":                  true,
			"xrayInternalStatusCached": c.isXrayOnline && c.process.GetStatus().Up,
			"xrayVersion":              c.xrayVersion,
			"nodeVersion":              c.nodeVersion,
			"nodeType":                 "go",
			"isCustomCore":             c.isCustomCore(),
		},
	})
}

func (c *Controller) HandleVersion(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"nodeVersion":  c.nodeVersion,
			"nodeType":     "go",
			"xrayVersion":  c.xrayVersion,
			"isCustomCore": c.isCustomCore(),
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func cleanupInboundSockets(cfg map[string]interface{}) error {
	inbounds, _ := cfg["inbounds"].([]interface{})
	for _, raw := range inbounds {
		inbound, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		paths := []string{}
		if listen, ok := inbound["listen"].(string); ok && strings.HasPrefix(listen, "/") {
			paths = append(paths, listen)
		}
		if stream, ok := inbound["streamSettings"].(map[string]interface{}); ok {
			if ds, ok := stream["dsSettings"].(map[string]interface{}); ok {
				if path, ok := ds["path"].(string); ok {
					paths = append(paths, path)
				}
			}
		}
		for _, path := range paths {
			info, err := os.Lstat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSocket == 0 {
				continue
			}
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	return nil
}
