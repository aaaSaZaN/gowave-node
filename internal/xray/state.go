package xray

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/remnawave/node-go/internal/hashedset"
)

type InboundHashInfo struct {
	UsersCount int    `json:"usersCount"`
	Hash       string `json:"hash"`
	Tag        string `json:"tag"`
}

type HashesInfo struct {
	EmptyConfig string            `json:"emptyConfig"`
	Inbounds    []InboundHashInfo `json:"inbounds"`
}

type StateManager struct {
	Operation          sync.RWMutex
	mu                 sync.RWMutex
	xrayConfig         map[string]interface{}
	emptyConfigHash    string
	inboundsHashMap    map[string]*hashedset.HashedSet
	xtlsConfigInbounds map[string]struct{}
}

func NewStateManager() *StateManager {
	return &StateManager{
		inboundsHashMap:    make(map[string]*hashedset.HashedSet),
		xtlsConfigInbounds: make(map[string]struct{}),
	}
}

func (s *StateManager) GetXrayConfig() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneConfig(s.xrayConfig)
}

func (s *StateManager) SetXrayConfig(cfg map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.xrayConfig = cloneConfig(cfg)
}

func (s *StateManager) ExtractUsersFromConfig(hashes HashesInfo, newConfig map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked()
	s.emptyConfigHash = hashes.EmptyConfig
	s.xrayConfig = cloneConfig(newConfig)

	validTags := make(map[string]struct{})
	remoteHashByTag := make(map[string]string)
	for _, item := range hashes.Inbounds {
		validTags[item.Tag] = struct{}{}
		remoteHashByTag[item.Tag] = item.Hash
	}

	if inbounds, ok := newConfig["inbounds"].([]interface{}); ok {
		for _, ib := range inbounds {
			ibMap, ok := ib.(map[string]interface{})
			if !ok {
				continue
			}
			tag, _ := ibMap["tag"].(string)
			if tag == "" {
				continue
			}
			if _, isValid := validTags[tag]; !isValid {
				continue
			}

			usersSet := hashedset.New()
			if settings, ok := ibMap["settings"].(map[string]interface{}); ok {
				if clients, ok := settings["clients"].([]interface{}); ok {
					for _, c := range clients {
						if cMap, ok := c.(map[string]interface{}); ok {
							if id, ok := cMap["id"].(string); ok && id != "" {
								usersSet.Add(id)
							}
						}
					}
				}
			}

			s.inboundsHashMap[tag] = usersSet
			s.xtlsConfigInbounds[tag] = struct{}{}
			log.Printf("[STATE] ▸ %s · %d users · %s (expected %s)", tag, usersSet.Size(), usersSet.Hash64String(), remoteHashByTag[tag])
		}
	}
}

func (s *StateManager) IsNeedRestartCore(incomingHashes HashesInfo) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.emptyConfigHash == "" {
		return true
	}
	if incomingHashes.EmptyConfig != s.emptyConfigHash {
		log.Printf("[STATE] Detected changes in Xray Core base configuration: %s vs %s", incomingHashes.EmptyConfig, s.emptyConfigHash)
		return true
	}
	if len(incomingHashes.Inbounds) != len(s.inboundsHashMap) {
		log.Printf("[STATE] Number of Xray Core inbounds has changed: %d vs %d", len(incomingHashes.Inbounds), len(s.inboundsHashMap))
		return true
	}

	for _, incoming := range incomingHashes.Inbounds {
		usersSet, exists := s.inboundsHashMap[incoming.Tag]
		if !exists {
			log.Printf("[STATE] Inbound %s no longer exists in local state", incoming.Tag)
			return true
		}
		if usersSet.Hash64String() != incoming.Hash {
			log.Printf("[STATE] Inbound %s hash mismatch: local %s vs incoming %s", incoming.Tag, usersSet.Hash64String(), incoming.Hash)
			return true
		}
	}

	return false
}

func (s *StateManager) AddUserToInbound(inboundTag, userUUID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	usersSet, exists := s.inboundsHashMap[inboundTag]
	if !exists {
		usersSet = hashedset.New(userUUID)
		s.inboundsHashMap[inboundTag] = usersSet
		s.xtlsConfigInbounds[inboundTag] = struct{}{}
		return
	}
	usersSet.Add(userUUID)
	s.xtlsConfigInbounds[inboundTag] = struct{}{}
}

func (s *StateManager) RemoveUserFromInbound(inboundTag, userUUID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	usersSet, exists := s.inboundsHashMap[inboundTag]
	if !exists {
		return
	}
	usersSet.Delete(userUUID)

}

func (s *StateManager) GetXtlsConfigInbounds() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]string, 0, len(s.xtlsConfigInbounds))
	for tag := range s.xtlsConfigInbounds {
		res = append(res, tag)
	}
	return res
}

func (s *StateManager) AddXtlsConfigInbound(inboundTag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.xtlsConfigInbounds[inboundTag] = struct{}{}
}

func (s *StateManager) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
}

func (s *StateManager) cleanupLocked() {
	s.inboundsHashMap = make(map[string]*hashedset.HashedSet)
	s.xtlsConfigInbounds = make(map[string]struct{})
	s.xrayConfig = nil
	s.emptyConfigHash = ""
}

func cloneConfig(in map[string]interface{}) map[string]interface{} {
	if in == nil {
		return nil
	}
	b, _ := json.Marshal(in)
	var out map[string]interface{}
	_ = json.Unmarshal(b, &out)
	return out
}
func (s *StateManager) Invalidate() { s.mu.Lock(); defer s.mu.Unlock(); s.emptyConfigHash = "" }

func (s *StateManager) HasConfig() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.xrayConfig != nil
}
