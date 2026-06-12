package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"configsync/internal/models"
)

type Store struct {
	mu              sync.RWMutex
	configs         map[string]map[string]*models.ConfigKey
	subscriptions   map[string]map[string]*models.Subscription
	dumpPath        string
	auditPath       string
	auditFile       *os.File
	longPollChans   map[string][]chan *models.ConfigKey
	maxHistory      int
}

func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	dumpPath := filepath.Join(dataDir, "dump.json")
	auditPath := filepath.Join(dataDir, "audit.jsonl")

	auditFile, err := os.OpenFile(auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open audit file: %w", err)
	}

	s := &Store{
		configs:       make(map[string]map[string]*models.ConfigKey),
		subscriptions: make(map[string]map[string]*models.Subscription),
		dumpPath:      dumpPath,
		auditPath:     auditPath,
		auditFile:     auditFile,
		longPollChans: make(map[string][]chan *models.ConfigKey),
		maxHistory:    20,
	}

	if err := s.Load(); err != nil {
		return nil, fmt.Errorf("load dump: %w", err)
	}

	return s, nil
}

func (s *Store) Load() error {
	data, err := os.ReadFile(s.dumpPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}

	type dumpData struct {
		Configs       []*models.ConfigKey      `json:"configs"`
		Subscriptions []*models.Subscription   `json:"subscriptions"`
	}

	var dump dumpData
	if err := json.Unmarshal(data, &dump); err != nil {
		return err
	}

	for _, cfg := range dump.Configs {
		if _, ok := s.configs[cfg.TenantID]; !ok {
			s.configs[cfg.TenantID] = make(map[string]*models.ConfigKey)
		}
		cfg.History = []models.ConfigVersion{
			{
				Version:   cfg.Version,
				Value:     cfg.Value,
				UpdatedAt: cfg.UpdatedAt,
				UpdatedBy: cfg.UpdatedBy,
			},
		}
		s.configs[cfg.TenantID][cfg.KeyPath] = cfg
	}

	for _, sub := range dump.Subscriptions {
		if _, ok := s.subscriptions[sub.TenantID]; !ok {
			s.subscriptions[sub.TenantID] = make(map[string]*models.Subscription)
		}
		s.subscriptions[sub.TenantID][sub.ID] = sub
	}

	return nil
}

func (s *Store) Dump() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type dumpData struct {
		Configs       []*models.ConfigKey      `json:"configs"`
		Subscriptions []*models.Subscription   `json:"subscriptions"`
	}

	dump := dumpData{}
	for _, tenantConfigs := range s.configs {
		for _, cfg := range tenantConfigs {
			dump.Configs = append(dump.Configs, cfg)
		}
	}
	for _, tenantSubs := range s.subscriptions {
		for _, sub := range tenantSubs {
			dump.Subscriptions = append(dump.Subscriptions, sub)
		}
	}

	data, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := s.dumpPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmpPath, s.dumpPath)
}

func (s *Store) Close() error {
	if s.auditFile != nil {
		s.auditFile.Close()
	}
	return s.Dump()
}

func (s *Store) CheckStorage() error {
	if _, err := os.Stat(filepath.Dir(s.dumpPath)); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(s.dumpPath), ".check")
	if err := os.WriteFile(tmp, []byte("check"), 0644); err != nil {
		return err
	}
	return os.Remove(tmp)
}

func (s *Store) GetConfig(tenantID, keyPath string) *models.ConfigKey {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if tenant, ok := s.configs[tenantID]; ok {
		if cfg, ok := tenant[keyPath]; ok {
			return cfg
		}
	}
	return nil
}

func (s *Store) GetConfigVersion(tenantID, keyPath string, version int64) *models.ConfigKey {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if tenant, ok := s.configs[tenantID]; ok {
		if cfg, ok := tenant[keyPath]; ok {
			if cfg.Version == version {
				return cfg
			}
			for _, h := range cfg.History {
				if h.Version == version {
					return &models.ConfigKey{
						KeyPath:   cfg.KeyPath,
						Value:     h.Value,
						Version:   h.Version,
						UpdatedAt: h.UpdatedAt,
						UpdatedBy: h.UpdatedBy,
						TenantID:  cfg.TenantID,
					}
				}
			}
		}
	}
	return nil
}

func (s *Store) ListConfigs(tenantID, prefix string) []*models.ConfigKey {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*models.ConfigKey
	if tenant, ok := s.configs[tenantID]; ok {
		for key, cfg := range tenant {
			if prefix == "" || strings.HasPrefix(key, prefix) {
				result = append(result, cfg)
			}
		}
	}
	return result
}

func (s *Store) GetHistory(tenantID, keyPath string) []models.ConfigVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if tenant, ok := s.configs[tenantID]; ok {
		if cfg, ok := tenant[keyPath]; ok {
			history := make([]models.ConfigVersion, len(cfg.History))
			copy(history, cfg.History)
			for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
				history[i], history[j] = history[j], history[i]
			}
			return history
		}
	}
	return nil
}

func (s *Store) PutConfig(tenantID, keyPath string, value json.RawMessage, updatedBy string, ifMatch *int64) (*models.ConfigKey, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.configs[tenantID]; !ok {
		s.configs[tenantID] = make(map[string]*models.ConfigKey)
	}

	now := time.Now().Format(time.RFC3339)
	cfg, exists := s.configs[tenantID][keyPath]

	if ifMatch != nil && exists && cfg.Version != *ifMatch {
		return cfg, false, fmt.Errorf("version mismatch: current=%d, expected=%d", cfg.Version, *ifMatch)
	}

	var newVersion int64
	if exists {
		newVersion = cfg.Version + 1
	} else {
		newVersion = 1
	}

	if exists && cfg.Metadata != nil && cfg.Metadata.IsDeprecated() {
		return nil, false, fmt.Errorf("config key is deprecated")
	}

	newCfg := &models.ConfigKey{
		KeyPath:   keyPath,
		Value:     value,
		Version:   newVersion,
		UpdatedAt: now,
		UpdatedBy: updatedBy,
		TenantID:  tenantID,
	}

	if exists {
		newCfg.Metadata = cfg.Metadata
		newCfg.GrayConfig = cfg.GrayConfig
		newCfg.History = append(cfg.History, models.ConfigVersion{
			Version:   cfg.Version,
			Value:     cfg.Value,
			UpdatedAt: cfg.UpdatedAt,
			UpdatedBy: cfg.UpdatedBy,
		})
		if len(newCfg.History) > s.maxHistory {
			newCfg.History = newCfg.History[len(newCfg.History)-s.maxHistory:]
		}
	} else {
		newCfg.History = []models.ConfigVersion{
			{
				Version:   newVersion,
				Value:     value,
				UpdatedAt: now,
				UpdatedBy: updatedBy,
			},
		}
		newCfg.Metadata = &models.Metadata{
			Owner: updatedBy,
		}
	}

	s.configs[tenantID][keyPath] = newCfg

	s.notifyLongPoll(tenantID, keyPath, newCfg)

	return newCfg, !exists, nil
}

func (s *Store) Rollback(tenantID, keyPath string, toVersion int64, updatedBy string) (*models.ConfigKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tenant, ok := s.configs[tenantID]
	if !ok {
		return nil, fmt.Errorf("tenant not found")
	}

	cfg, ok := tenant[keyPath]
	if !ok {
		return nil, fmt.Errorf("config not found")
	}

	var targetValue json.RawMessage
	found := false
	for _, h := range cfg.History {
		if h.Version == toVersion {
			targetValue = h.Value
			found = true
			break
		}
	}

	if !found && cfg.Version == toVersion {
		targetValue = cfg.Value
		found = true
	}

	if !found {
		return nil, fmt.Errorf("version %d not found", toVersion)
	}

	now := time.Now().Format(time.RFC3339)
	newVersion := cfg.Version + 1

	newCfg := &models.ConfigKey{
		KeyPath:   keyPath,
		Value:     targetValue,
		Version:   newVersion,
		UpdatedAt: now,
		UpdatedBy: updatedBy,
		TenantID:  tenantID,
		Metadata:  cfg.Metadata,
		GrayConfig: cfg.GrayConfig,
		History: append(cfg.History, models.ConfigVersion{
			Version:   cfg.Version,
			Value:     cfg.Value,
			UpdatedAt: cfg.UpdatedAt,
			UpdatedBy: cfg.UpdatedBy,
		}),
	}

	if len(newCfg.History) > s.maxHistory {
		newCfg.History = newCfg.History[len(newCfg.History)-s.maxHistory:]
	}

	s.configs[tenantID][keyPath] = newCfg
	s.notifyLongPoll(tenantID, keyPath, newCfg)

	return newCfg, nil
}

func (s *Store) UpdateMetadata(tenantID, keyPath string, metadata *models.Metadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tenant, ok := s.configs[tenantID]
	if !ok {
		return fmt.Errorf("tenant not found")
	}

	cfg, ok := tenant[keyPath]
	if !ok {
		return fmt.Errorf("config not found")
	}

	cfg.Metadata = metadata
	return nil
}

func (s *Store) SetGrayConfig(tenantID, keyPath string, grayCfg *models.GrayConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tenant, ok := s.configs[tenantID]
	if !ok {
		return fmt.Errorf("tenant not found")
	}

	cfg, ok := tenant[keyPath]
	if !ok {
		return fmt.Errorf("config not found")
	}

	cfg.GrayConfig = grayCfg
	return nil
}

func (s *Store) PromoteGray(tenantID, keyPath string, updatedBy string) (*models.ConfigKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tenant, ok := s.configs[tenantID]
	if !ok {
		return nil, fmt.Errorf("tenant not found")
	}

	cfg, ok := tenant[keyPath]
	if !ok {
		return nil, fmt.Errorf("config not found")
	}

	if cfg.GrayConfig == nil {
		return nil, fmt.Errorf("no gray config")
	}

	return s.PutConfigLocked(tenantID, keyPath, cfg.GrayConfig.Value, updatedBy, nil)
}

func (s *Store) PutConfigLocked(tenantID, keyPath string, value json.RawMessage, updatedBy string, ifMatch *int64) (*models.ConfigKey, error) {
	if _, ok := s.configs[tenantID]; !ok {
		s.configs[tenantID] = make(map[string]*models.ConfigKey)
	}

	now := time.Now().Format(time.RFC3339)
	cfg, exists := s.configs[tenantID][keyPath]

	if ifMatch != nil && exists && cfg.Version != *ifMatch {
		return nil, fmt.Errorf("version mismatch: current=%d, expected=%d", cfg.Version, *ifMatch)
	}

	var newVersion int64
	if exists {
		newVersion = cfg.Version + 1
	} else {
		newVersion = 1
	}

	newCfg := &models.ConfigKey{
		KeyPath:   keyPath,
		Value:     value,
		Version:   newVersion,
		UpdatedAt: now,
		UpdatedBy: updatedBy,
		TenantID:  tenantID,
	}

	if exists {
		newCfg.Metadata = cfg.Metadata
		newCfg.GrayConfig = cfg.GrayConfig
		newCfg.History = append(cfg.History, models.ConfigVersion{
			Version:   cfg.Version,
			Value:     cfg.Value,
			UpdatedAt: cfg.UpdatedAt,
			UpdatedBy: cfg.UpdatedBy,
		})
		if len(newCfg.History) > s.maxHistory {
			newCfg.History = newCfg.History[len(newCfg.History)-s.maxHistory:]
		}
	} else {
		newCfg.History = []models.ConfigVersion{
			{
				Version:   newVersion,
				Value:     value,
				UpdatedAt: now,
				UpdatedBy: updatedBy,
			},
		}
		newCfg.Metadata = &models.Metadata{
			Owner: updatedBy,
		}
	}

	s.configs[tenantID][keyPath] = newCfg
	s.notifyLongPoll(tenantID, keyPath, newCfg)

	return newCfg, nil
}

func (s *Store) GetMatchingSubscriptions(tenantID, keyPath string) []*models.Subscription {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*models.Subscription
	if tenant, ok := s.subscriptions[tenantID]; ok {
		for _, sub := range tenant {
			if sub.Active && sub.Status == models.SubscriptionStatusHealthy && matchPattern(sub.KeyPattern, keyPath) {
				result = append(result, sub)
			}
		}
	}
	return result
}

func matchPattern(pattern, key string) bool {
	patternParts := strings.Split(pattern, ".")
	keyParts := strings.Split(key, ".")

	pi := 0
	ki := 0

	for pi < len(patternParts) && ki < len(keyParts) {
		if patternParts[pi] == "**" {
			if pi == len(patternParts)-1 {
				return true
			}
			for k := ki; k <= len(keyParts); k++ {
				if matchPattern(strings.Join(patternParts[pi+1:], "."), strings.Join(keyParts[k:], ".")) {
					return true
				}
			}
			return false
		}

		if patternParts[pi] == "*" {
			pi++
			ki++
			continue
		}

		if patternParts[pi] != keyParts[ki] {
			return false
		}

		pi++
		ki++
	}

	return pi == len(patternParts) && ki == len(keyParts)
}

func (s *Store) CreateSubscription(sub *models.Subscription) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.subscriptions[sub.TenantID]; !ok {
		s.subscriptions[sub.TenantID] = make(map[string]*models.Subscription)
	}
	s.subscriptions[sub.TenantID][sub.ID] = sub
}

func (s *Store) DeleteSubscription(tenantID, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tenant, ok := s.subscriptions[tenantID]; ok {
		if _, ok := tenant[id]; ok {
			delete(tenant, id)
			return true
		}
	}
	return false
}

func (s *Store) GetSubscription(tenantID, id string) *models.Subscription {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if tenant, ok := s.subscriptions[tenantID]; ok {
		return tenant[id]
	}
	return nil
}

func (s *Store) ListSubscriptions(tenantID string) []*models.Subscription {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*models.Subscription
	if tenant, ok := s.subscriptions[tenantID]; ok {
		for _, sub := range tenant {
			result = append(result, sub)
		}
	}
	return result
}

func (s *Store) UpdateSubscriptionStatus(tenantID, id, status, lastError string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tenant, ok := s.subscriptions[tenantID]; ok {
		if sub, ok := tenant[id]; ok {
			sub.Status = status
			if status == models.SubscriptionStatusHealthy {
				sub.LastError = ""
			} else {
				sub.LastError = lastError
			}
			sub.LastNotifyAt = time.Now().Format(time.RFC3339)
		}
	}
}

func (s *Store) RecoverSubscription(tenantID, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tenant, ok := s.subscriptions[tenantID]; ok {
		if sub, ok := tenant[id]; ok {
			sub.Status = models.SubscriptionStatusHealthy
			sub.LastError = ""
			return true
		}
	}
	return false
}

func (s *Store) WriteAudit(log *models.AuditLog) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	log.Timestamp = time.Now().Format(time.RFC3339)
	data, err := json.Marshal(log)
	if err != nil {
		return err
	}
	_, err = s.auditFile.Write(append(data, '\n'))
	if err != nil {
		return err
	}
	return s.auditFile.Sync()
}

func (s *Store) ReadAudit(tenantID, keyPath string) ([]*models.AuditLog, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.auditPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []*models.AuditLog
	for _, line := range strings.Split(string(data), "\n") {
		if len(line) == 0 {
			continue
		}
		var log models.AuditLog
		if err := json.Unmarshal([]byte(line), &log); err != nil {
			continue
		}
		if log.TenantID == tenantID && (keyPath == "" || log.KeyPath == keyPath) {
			result = append(result, &log)
		}
	}

	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return result, nil
}

func (s *Store) GetConfigCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, tenant := range s.configs {
		count += len(tenant)
	}
	return count
}

func (s *Store) GetSubscriptionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, tenant := range s.subscriptions {
		count += len(tenant)
	}
	return count
}

func (s *Store) GetAllConfigs() []*models.ConfigKey {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*models.ConfigKey
	for _, tenant := range s.configs {
		for _, cfg := range tenant {
			result = append(result, cfg)
		}
	}
	return result
}

func (s *Store) GetAllSubscriptions() []*models.Subscription {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*models.Subscription
	for _, tenant := range s.subscriptions {
		for _, sub := range tenant {
			result = append(result, sub)
		}
	}
	return result
}

func (s *Store) LongPoll(tenantID, keyPath string, timeout time.Duration) *models.ConfigKey {
	ch := make(chan *models.ConfigKey, 1)

	s.mu.Lock()
	key := tenantID + ":" + keyPath
	s.longPollChans[key] = append(s.longPollChans[key], ch)
	cfg := s.configs[tenantID][keyPath]
	s.mu.Unlock()

	if cfg != nil {
		select {
		case newCfg := <-ch:
			return newCfg
		case <-time.After(timeout):
			s.removeLongPollChan(key, ch)
			return cfg
		}
	}

	select {
	case newCfg := <-ch:
		return newCfg
	case <-time.After(timeout):
		s.removeLongPollChan(key, ch)
		return nil
	}
}

func (s *Store) notifyLongPoll(tenantID, keyPath string, cfg *models.ConfigKey) {
	key := tenantID + ":" + keyPath
	if chans, ok := s.longPollChans[key]; ok {
		for _, ch := range chans {
			select {
			case ch <- cfg:
			default:
			}
		}
		delete(s.longPollChans, key)
	}
}

func (s *Store) removeLongPollChan(key string, ch chan *models.ConfigKey) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if chans, ok := s.longPollChans[key]; ok {
		for i, c := range chans {
			if c == ch {
				s.longPollChans[key] = append(chans[:i], chans[i+1:]...)
				break
			}
		}
		if len(s.longPollChans[key]) == 0 {
			delete(s.longPollChans, key)
		}
	}
}

func CompilePattern(pattern string) *regexp.Regexp {
	regexPattern := "^" + strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(pattern, ".", "\\."), "**", ".*"), "*", "[^.]*") + "$"
	return regexp.MustCompile(regexPattern)
}
