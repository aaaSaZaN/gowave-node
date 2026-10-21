package stats

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/aaaSaZaN/gowave-node/internal/apiutil"
	"net/http"
	"time"

	"github.com/aaaSaZaN/gowave-node/internal/plugin"
	"github.com/aaaSaZaN/gowave-node/internal/xray"
)

type StatsService struct {
	xrayClient *xray.Client
	netPoller  *NetworkPoller
	pluginSvc  *plugin.Service
	geocheck   *GeocheckService
}

func NewStatsService(xrayClient *xray.Client, netPoller *NetworkPoller, pluginSvc *plugin.Service) *StatsService {
	return &StatsService{
		xrayClient: xrayClient,
		netPoller:  netPoller,
		pluginSvc:  pluginSvc,
		geocheck:   NewGeocheckService(),
	}
}

// getUserOnlineStatus
type GetUserOnlineStatusRequest struct {
	Username string `json:"username"`
}

func (s *StatsService) HandleGetUserOnlineStatus(w http.ResponseWriter, r *http.Request) {
	var req GetUserOnlineStatusRequest
	if err := apiutil.Decode(r, &req); err != nil {
		apiutil.Error(w, r, 400, "E000", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	online, err := s.xrayClient.GetUserOnlineStatus(ctx, req.Username)
	if err != nil {
		online = false
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"isOnline": online,
		},
	})
}

// getSystemStats
func (s *StatsService) HandleGetSystemStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	sysStats, err := s.xrayClient.GetSysStats(ctx)
	if err != nil {
		apiutil.Error(w, r, 500, "A010", err)
		return
	}
	var xrayInfo interface{} = nil
	if err == nil && sysStats != nil {
		xrayInfo = map[string]interface{}{
			"numGoroutine": sysStats.NumGoroutine,
			"numGC":        sysStats.NumGC,
			"alloc":        sysStats.Alloc,
			"totalAlloc":   sysStats.TotalAlloc,
			"sys":          sysStats.Sys,
			"mallocs":      sysStats.Mallocs,
			"frees":        sysStats.Frees,
			"liveObjects":  sysStats.LiveObjects,
			"pauseTotalNs": sysStats.PauseTotalNs,
			"uptime":       sysStats.Uptime,
		}
	}

	reportsCount := s.pluginSvc.TorrentBlocker.ReportsCount()
	systemCombined := GetSystemCombined(s.netPoller)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"xrayInfo": xrayInfo,
			"plugins": map[string]interface{}{
				"torrentBlocker": map[string]interface{}{
					"reportsCount": reportsCount,
				},
			},
			"system": systemCombined,
		},
	})
}

// getUsersStats
type ResetRequest struct {
	Reset bool `json:"reset"`
}

func (s *StatsService) HandleGetUsersStats(w http.ResponseWriter, r *http.Request) {
	var req ResetRequest
	if err := apiutil.Decode(r, &req); err != nil {
		apiutil.Error(w, r, 400, "E000", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	users, err := s.xrayClient.GetAllUsersStats(ctx, req.Reset)
	if err != nil {
		apiutil.Error(w, r, 500, "A011", err)
		return
	}
	if users == nil {
		users = []xray.UserTraffic{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"users": users,
		},
	})
}

// getInboundStats
type InboundStatsRequest struct {
	Tag   string `json:"tag"`
	Reset bool   `json:"reset"`
}

func (s *StatsService) HandleGetInboundStats(w http.ResponseWriter, r *http.Request) {
	var req InboundStatsRequest
	if err := apiutil.Decode(r, &req); err != nil {
		apiutil.Error(w, r, 400, "E000", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	ib, err := s.xrayClient.GetInboundStats(ctx, req.Tag, req.Reset)
	if err != nil {
		apiutil.Error(w, r, 500, "A012", err)
		return
	}
	if ib == nil {
		ib = &xray.InboundTraffic{Inbound: req.Tag}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"inbound":  ib.Inbound,
			"downlink": ib.Downlink,
			"uplink":   ib.Uplink,
		},
	})
}

// getOutboundStats
type OutboundStatsRequest struct {
	Tag   string `json:"tag"`
	Reset bool   `json:"reset"`
}

func (s *StatsService) HandleGetOutboundStats(w http.ResponseWriter, r *http.Request) {
	var req OutboundStatsRequest
	if err := apiutil.Decode(r, &req); err != nil {
		apiutil.Error(w, r, 400, "E000", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	ob, err := s.xrayClient.GetOutboundStats(ctx, req.Tag, req.Reset)
	if err != nil {
		apiutil.Error(w, r, 500, "A013", err)
		return
	}
	if ob == nil {
		ob = &xray.OutboundTraffic{Outbound: req.Tag}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"outbound": ob.Outbound,
			"downlink": ob.Downlink,
			"uplink":   ob.Uplink,
		},
	})
}

// getAllInboundsStats
func (s *StatsService) HandleGetAllInboundsStats(w http.ResponseWriter, r *http.Request) {
	var req ResetRequest
	if err := apiutil.Decode(r, &req); err != nil {
		apiutil.Error(w, r, 400, "E000", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	inbounds, err := s.xrayClient.GetAllInboundsStats(ctx, req.Reset)
	if err != nil {
		apiutil.Error(w, r, 500, "A015", err)
		return
	}
	if inbounds == nil {
		inbounds = []xray.InboundTraffic{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"inbounds": inbounds,
		},
	})
}

// getAllOutboundsStats
func (s *StatsService) HandleGetAllOutboundsStats(w http.ResponseWriter, r *http.Request) {
	var req ResetRequest
	if err := apiutil.Decode(r, &req); err != nil {
		apiutil.Error(w, r, 400, "E000", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	outbounds, err := s.xrayClient.GetAllOutboundsStats(ctx, req.Reset)
	if err != nil {
		apiutil.Error(w, r, 500, "A016", err)
		return
	}
	if outbounds == nil {
		outbounds = []xray.OutboundTraffic{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"outbounds": outbounds,
		},
	})
}

// getCombinedStats
func (s *StatsService) HandleGetCombinedStats(w http.ResponseWriter, r *http.Request) {
	var req ResetRequest
	if err := apiutil.Decode(r, &req); err != nil {
		apiutil.Error(w, r, 400, "E000", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	inbounds, err := s.xrayClient.GetAllInboundsStats(ctx, req.Reset)
	if err != nil {
		apiutil.Error(w, r, 500, "A017", err)
		return
	}
	outbounds, err := s.xrayClient.GetAllOutboundsStats(ctx, req.Reset)
	if err != nil {
		apiutil.Error(w, r, 500, "A017", err)
		return
	}
	if inbounds == nil {
		inbounds = []xray.InboundTraffic{}
	}
	if outbounds == nil {
		outbounds = []xray.OutboundTraffic{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"inbounds":  inbounds,
			"outbounds": outbounds,
		},
	})
}

// getUserIPList
type UserIPListRequest struct {
	UserID string `json:"userId"`
}

func (s *StatsService) HandleGetUserIPList(w http.ResponseWriter, r *http.Request) {
	var req UserIPListRequest
	if err := apiutil.Decode(r, &req); err != nil {
		apiutil.Error(w, r, 400, "E000", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	ips, err := s.xrayClient.GetStatsOnlineIPList(ctx, req.UserID, true)
	if err != nil {
		apiutil.Error(w, r, 500, "A014", err)
		return
	}
	if ips == nil {
		ips = []xray.UserIPSeen{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"ips": ips,
		},
	})
}

func (s *StatsService) HandleGetUsersIPList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	users, err := s.xrayClient.GetUsersIPList(ctx)
	if err != nil {
		apiutil.Error(w, r, 500, "A014", err)
		return
	}
	writeJSON(w, 200, map[string]interface{}{"response": map[string]interface{}{"users": users}})
}

// getGeocheck
func (s *StatsService) HandleGetGeocheck(w http.ResponseWriter, r *http.Request) {
	var req GeocheckRequest
	if err := apiutil.Decode(r, &req); err != nil {
		apiutil.Error(w, r, 400, "E000", err)
		return
	}

	report, err := s.geocheck.Run(r.Context(), req)
	if err != nil {
		apiutil.Error(w, r, 500, "A018", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": report,
	})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (r *GetUserOnlineStatusRequest) Validate() error {
	if r.Username == "" {
		return errors.New("username is required")
	}
	return nil
}
func (r *InboundStatsRequest) Validate() error {
	if r.Tag == "" {
		return errors.New("tag is required")
	}
	return nil
}
func (r *OutboundStatsRequest) Validate() error {
	if r.Tag == "" {
		return errors.New("tag is required")
	}
	return nil
}
func (r *UserIPListRequest) Validate() error {
	if r.UserID == "" {
		return errors.New("userId is required")
	}
	return nil
}
