package xray

import (
	"fmt"
	"net/url"
	"strings"
)

type TorrentBlockerState struct {
	Enabled         bool
	IncludeRuleTags []string
	RulePosition    int
}

type InternalConfig struct {
	SocketPath        string
	Token             string
	XtlsApiSocketPath string
}

func GenerateApiConfig(
	baseConfig map[string]interface{},
	tbState TorrentBlockerState,
	internal InternalConfig,
	capNetAdmin bool,
) map[string]interface{} {
	baseConfig = cloneConfig(baseConfig)
	result := make(map[string]interface{})
	for k, v := range baseConfig {
		result[k] = v
	}

	// stats
	result["stats"] = map[string]interface{}{}

	apiListen := internal.XtlsApiSocketPath
	if apiListen == "" {
		apiListen = "/run/remnanode/xray.sock"
	}
	api := map[string]interface{}{
		"tag":      "REMNAWAVE_API",
		"services": []string{"HandlerService", "StatsService", "RoutingService"},
	}
	unixAPI := !strings.Contains(apiListen, ":") || strings.HasPrefix(apiListen, "unix:")
	if !unixAPI {
		api["listen"] = apiListen
	}
	result["api"] = api

	// policy
	levels := map[string]interface{}{
		"0": map[string]interface{}{
			"statsUserUplink":   true,
			"statsUserDownlink": true,
			"statsUserOnline":   capNetAdmin,
		},
	}
	if existingPolicy, ok := baseConfig["policy"].(map[string]interface{}); ok {
		if existingLevels, ok := existingPolicy["levels"].(map[string]interface{}); ok {
			if l0, ok := existingLevels["0"].(map[string]interface{}); ok {
				for k, v := range l0 {
					levels["0"].(map[string]interface{})[k] = v
				}
				levels["0"].(map[string]interface{})["statsUserUplink"] = true
				levels["0"].(map[string]interface{})["statsUserDownlink"] = true
				levels["0"].(map[string]interface{})["statsUserOnline"] = capNetAdmin
			}
		}
	}
	result["policy"] = map[string]interface{}{
		"levels": levels,
		"system": map[string]interface{}{
			"statsInboundDownlink":  true,
			"statsInboundUplink":    true,
			"statsOutboundDownlink": true,
			"statsOutboundUplink":   true,
		},
	}

	// inbounds
	var inbounds []interface{}
	if unixAPI {
		listen := strings.TrimPrefix(apiListen, "unix:")
		if !strings.HasPrefix(listen, "/") && !strings.HasPrefix(listen, "@") {
			listen = "@" + listen
		}
		// Older stable cores require an explicit UNIX network and nonzero
		// nominal destination port; the API routing rule consumes the connection.
		inbounds = append(inbounds, map[string]interface{}{"tag": "REMNAWAVE_API_INBOUND", "listen": listen, "protocol": "tunnel", "settings": map[string]interface{}{"network": "unix", "address": "127.0.0.1", "port": 1}})
	}
	if existingInbounds, ok := baseConfig["inbounds"].([]interface{}); ok {
		inbounds = append(inbounds, existingInbounds...)
	}
	result["inbounds"] = inbounds

	// outbounds
	var outbounds []interface{}
	if existingOutbounds, ok := baseConfig["outbounds"].([]interface{}); ok {
		outbounds = append(outbounds, existingOutbounds...)
	}

	// routing rules
	var rules []interface{}
	if unixAPI {
		rules = append(rules, map[string]interface{}{"inboundTag": []string{"REMNAWAVE_API_INBOUND"}, "outboundTag": "REMNAWAVE_API"})
	}

	if existingRouting, ok := baseConfig["routing"].(map[string]interface{}); ok {
		if existingRules, ok := existingRouting["rules"].([]interface{}); ok {
			for _, r := range existingRules {
				if rMap, ok := r.(map[string]interface{}); ok {
					if rMap["outboundTag"] != "REMNAWAVE_API" {
						rules = append(rules, rMap)
					}
				}
			}
		}
	}

	// torrent blocker integration
	if tbState.Enabled {
		webhookURL := fmt.Sprintf("@%s:/internal/webhook?token=%s", strings.TrimPrefix(strings.TrimPrefix(internal.SocketPath, "unix:"), "@"), url.QueryEscape(internal.Token))

		outbounds = append(outbounds, map[string]interface{}{
			"tag":      "RW_TB_OUTBOUND_BLOCK",
			"protocol": "blackhole",
		})

		tbRule := map[string]interface{}{
			"protocol":    []string{"bittorrent"},
			"outboundTag": "RW_TB_OUTBOUND_BLOCK",
			"webhook": map[string]interface{}{
				"url":           webhookURL,
				"deduplication": 5,
			},
		}

		// insert at rulePosition
		insertIdx := min(max(tbState.RulePosition, 0)+map[bool]int{true: 1, false: 0}[unixAPI], len(rules))
		if insertIdx >= len(rules) {
			rules = append(rules, tbRule)
		} else {
			rules = append(rules[:insertIdx], append([]interface{}{tbRule}, rules[insertIdx:]...)...)
		}

		if len(tbState.IncludeRuleTags) > 0 {
			tagSet := make(map[string]struct{})
			for _, t := range tbState.IncludeRuleTags {
				tagSet[t] = struct{}{}
			}
			for _, r := range rules {
				if rMap, ok := r.(map[string]interface{}); ok {
					if ruleTag, ok := rMap["ruleTag"].(string); ok {
						if _, exists := tagSet[ruleTag]; exists {
							rMap["webhook"] = map[string]interface{}{
								"url":           webhookURL,
								"deduplication": 5,
							}
						}
					}
				}
			}
		}
	}

	routingMap := make(map[string]interface{})
	if existingRouting, ok := baseConfig["routing"].(map[string]interface{}); ok {
		for k, v := range existingRouting {
			routingMap[k] = v
		}
	}
	routingMap["rules"] = rules
	result["routing"] = routingMap
	result["outbounds"] = outbounds

	return result
}

func resolveRuleIndex(position int, rulesLength int) int {
	userRulesCount := rulesLength - 1
	if position <= 0 {
		return 1
	}
	idx := 1 + position
	if idx > 1+userRulesCount {
		idx = 1 + userRulesCount
	}
	return idx
}
