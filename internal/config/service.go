package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"configsync/internal/models"
	"configsync/internal/notifier"
	"configsync/internal/storage"
)

type Service struct {
	store    *storage.Store
	notifier notifier.Notifier
	mu       sync.Mutex
}

func NewService(store *storage.Store, notifier notifier.Notifier) *Service {
	return &Service{
		store:    store,
		notifier: notifier,
	}
}

type UpdateConfigResult struct {
	Config    *models.ConfigKey
	Created   bool
	Conflict  bool
	CurrentVersion int64
}

func (s *Service) UpdateConfig(tenantID, keyPath string, value json.RawMessage, updatedBy string, ifMatch *int64) (*UpdateConfigResult, error) {
	cfg, created, err := s.store.PutConfig(tenantID, keyPath, value, updatedBy, ifMatch)
	if err != nil {
		if strings.Contains(err.Error(), "version mismatch") {
			var current int64
			if cfg != nil {
				current = cfg.Version
			}
			return &UpdateConfigResult{
				Conflict:       true,
				CurrentVersion: current,
			}, err
		}
		return nil, err
	}

	var oldValue json.RawMessage
	var oldVersion int64
	if !created {
		history := s.store.GetHistory(tenantID, keyPath)
		if len(history) > 1 {
			oldValue = history[1].Value
			oldVersion = history[1].Version
		}
	}

	action := models.AuditActionUpdate
	if created {
		action = models.AuditActionCreate
	}

	_ = s.store.WriteAudit(&models.AuditLog{
		TenantID:   tenantID,
		UserID:     updatedBy,
		Action:     action,
		KeyPath:    keyPath,
		OldVersion: oldVersion,
		NewVersion: cfg.Version,
		OldValue:   oldValue,
		NewValue:   cfg.Value,
	})

	s.notifySubscribers(cfg, models.EventUpdated)

	return &UpdateConfigResult{
		Config:  cfg,
		Created: created,
	}, nil
}

func (s *Service) GetConfig(tenantID, keyPath string, version *int64, grayUserID string) *models.ConfigKey {
	var cfg *models.ConfigKey
	if version != nil {
		cfg = s.store.GetConfigVersion(tenantID, keyPath, *version)
	} else {
		cfg = s.store.GetConfig(tenantID, keyPath)
	}

	if cfg != nil && grayUserID != "" && cfg.GrayConfig != nil && cfg.GrayConfig.GrayRules.Match(grayUserID) {
		return &models.ConfigKey{
			KeyPath:   cfg.KeyPath,
			Value:     cfg.GrayConfig.Value,
			Version:   cfg.Version,
			UpdatedAt: cfg.GrayConfig.CreatedAt,
			UpdatedBy: cfg.GrayConfig.CreatedBy,
			TenantID:  cfg.TenantID,
			Metadata:  cfg.Metadata,
		}
	}

	return cfg
}

func (s *Service) ListConfigs(tenantID, prefix string) []*models.ConfigKey {
	return s.store.ListConfigs(tenantID, prefix)
}

func (s *Service) GetHistory(tenantID, keyPath string) []models.ConfigVersion {
	return s.store.GetHistory(tenantID, keyPath)
}

func (s *Service) Rollback(tenantID, keyPath string, toVersion int64, updatedBy string) (*models.ConfigKey, error) {
	cfg, err := s.store.Rollback(tenantID, keyPath, toVersion, updatedBy)
	if err != nil {
		return nil, err
	}

	_ = s.store.WriteAudit(&models.AuditLog{
		TenantID:   tenantID,
		UserID:     updatedBy,
		Action:     models.AuditActionRollback,
		KeyPath:    keyPath,
		NewVersion: cfg.Version,
		OldVersion: toVersion,
		NewValue:   cfg.Value,
		Details:    fmt.Sprintf("rollback to version %d", toVersion),
	})

	s.notifySubscribers(cfg, models.EventRollback)

	return cfg, nil
}

func (s *Service) Resolve(tenantID, keyPath string, candidateVersion1, candidateVersion2 int64, resolution string, updatedBy string) (*models.ConfigKey, error) {
	var pickVersion int64
	switch resolution {
	case "pick_old":
		pickVersion = candidateVersion1
	case "pick_new":
		pickVersion = candidateVersion2
	default:
		return nil, errors.New("invalid resolution")
	}

	return s.Rollback(tenantID, keyPath, pickVersion, updatedBy)
}

func (s *Service) SetGrayConfig(tenantID, keyPath string, value json.RawMessage, rules models.GrayRules, createdBy string) error {
	cfg := s.store.GetConfig(tenantID, keyPath)
	if cfg == nil {
		return errors.New("config not found")
	}

	grayCfg := &models.GrayConfig{
		Value:     value,
		GrayRules: rules,
		CreatedAt: time.Now().Format(time.RFC3339),
		CreatedBy: createdBy,
	}

	if err := s.store.SetGrayConfig(tenantID, keyPath, grayCfg); err != nil {
		return err
	}

	_ = s.store.WriteAudit(&models.AuditLog{
		TenantID: tenantID,
		UserID:   createdBy,
		Action:   models.AuditActionGray,
		KeyPath:  keyPath,
		NewValue: value,
	})

	return nil
}

func (s *Service) GetGrayConfig(tenantID, keyPath string) *models.GrayConfig {
	cfg := s.store.GetConfig(tenantID, keyPath)
	if cfg == nil {
		return nil
	}
	return cfg.GrayConfig
}

func (s *Service) PromoteGray(tenantID, keyPath string, updatedBy string) (*models.ConfigKey, error) {
	cfg, err := s.store.PromoteGray(tenantID, keyPath, updatedBy)
	if err != nil {
		return nil, err
	}

	_ = s.store.WriteAudit(&models.AuditLog{
		TenantID:   tenantID,
		UserID:     updatedBy,
		Action:     models.AuditActionPromote,
		KeyPath:    keyPath,
		NewVersion: cfg.Version,
		NewValue:   cfg.Value,
	})

	s.notifySubscribers(cfg, models.EventUpdated)

	return cfg, nil
}

func (s *Service) Preview(tenantID string, changes []models.PreviewChange) (*models.PreviewResponse, error) {
	resp := &models.PreviewResponse{}
	keySet := make(map[string]bool)

	for _, change := range changes {
		oldCfg := s.store.GetConfig(tenantID, change.KeyPath)
		var oldValue json.RawMessage
		changed := true
		if oldCfg != nil {
			oldValue = oldCfg.Value
			changed = !bytes.Equal(oldCfg.Value, change.Value)
		}

		resp.Changes = append(resp.Changes, models.PreviewDiff{
			KeyPath:  change.KeyPath,
			OldValue: oldValue,
			NewValue: change.Value,
			Changed:  changed,
		})

		if changed {
			keySet[change.KeyPath] = true
			resp.AffectedKeys = append(resp.AffectedKeys, change.KeyPath)
		}
	}

	subs := s.store.ListSubscriptions(tenantID)
	for _, sub := range subs {
		if !sub.Active || sub.Status != models.SubscriptionStatusHealthy {
			continue
		}

		var matched []string
		for key := range keySet {
			pattern := storage.CompilePattern(sub.KeyPattern)
			if pattern.MatchString(key) {
				matched = append(matched, key)
			}
		}

		if len(matched) > 0 {
			resp.AffectedSubscribers = append(resp.AffectedSubscribers, models.AffectedSubscriber{
				SubscriberID: sub.SubscriberID,
				CallbackURL:  sub.CallbackURL,
				KeyPattern:   sub.KeyPattern,
				MatchedKeys:  matched,
			})
		}
	}

	return resp, nil
}

func (s *Service) BatchUpdate(tenantID string, changes []models.BatchChange, updatedBy string) ([]*models.ConfigKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var results []*models.ConfigKey
	snapshots := make(map[string]*models.ConfigKey)

	for _, change := range changes {
		oldCfg := s.store.GetConfig(tenantID, change.KeyPath)
		if oldCfg != nil {
			snapshots[change.KeyPath] = &models.ConfigKey{
				KeyPath:   oldCfg.KeyPath,
				Value:     oldCfg.Value,
				Version:   oldCfg.Version,
				UpdatedAt: oldCfg.UpdatedAt,
				UpdatedBy: oldCfg.UpdatedBy,
				TenantID:  oldCfg.TenantID,
				Metadata:  oldCfg.Metadata,
			}
		}
	}

	success := true
	for _, change := range changes {
		cfg, _, err := s.store.PutConfig(tenantID, change.KeyPath, change.Value, updatedBy, nil)
		if err != nil {
			success = false
			break
		}
		results = append(results, cfg)
	}

	if !success {
		for key, oldCfg := range snapshots {
			_, _, _ = s.store.PutConfig(tenantID, key, oldCfg.Value, oldCfg.UpdatedBy, nil)
		}
		return nil, errors.New("batch update failed, rolled back")
	}

	allChanges, _ := json.Marshal(changes)
	_ = s.store.WriteAudit(&models.AuditLog{
		TenantID: tenantID,
		UserID:   updatedBy,
		Action:   models.AuditActionBatch,
		KeyPath:  fmt.Sprintf("batch:%d", len(changes)),
		NewValue: allChanges,
		Details:  fmt.Sprintf("batch updated %d configs", len(changes)),
	})

	for _, cfg := range results {
		s.notifySubscribers(cfg, models.EventUpdated)
	}

	return results, nil
}

func (s *Service) LongPoll(tenantID, keyPath string, timeout time.Duration) *models.ConfigKey {
	return s.store.LongPoll(tenantID, keyPath, timeout)
}

func (s *Service) UpdateMetadata(tenantID, keyPath string, metadata *models.Metadata) error {
	return s.store.UpdateMetadata(tenantID, keyPath, metadata)
}

func (s *Service) CreateSubscription(tenantID, subscriberID, keyPattern, callbackURL string) *models.Subscription {
	sub := models.NewSubscription(subscriberID, keyPattern, callbackURL, tenantID)
	s.store.CreateSubscription(sub)
	return sub
}

func (s *Service) DeleteSubscription(tenantID, id string) bool {
	return s.store.DeleteSubscription(tenantID, id)
}

func (s *Service) GetSubscription(tenantID, id string) *models.Subscription {
	return s.store.GetSubscription(tenantID, id)
}

func (s *Service) ListSubscriptions(tenantID string) []*models.Subscription {
	return s.store.ListSubscriptions(tenantID)
}

func (s *Service) RecoverSubscription(tenantID, id string) bool {
	return s.store.RecoverSubscription(tenantID, id)
}

func (s *Service) GetAuditLogs(tenantID, keyPath string) ([]*models.AuditLog, error) {
	return s.store.ReadAudit(tenantID, keyPath)
}

func (s *Service) Export(tenantID string) (*models.ExportData, error) {
	configs := s.store.ListConfigs(tenantID, "")
	subs := s.store.ListSubscriptions(tenantID)

	return &models.ExportData{
		Version:     1,
		ExportedAt:  time.Now().Format(time.RFC3339),
		Configs:     configs,
		Subscriptions: subs,
	}, nil
}

func (s *Service) ExportAll() (*models.ExportData, error) {
	configs := s.store.GetAllConfigs()
	subs := s.store.GetAllSubscriptions()

	return &models.ExportData{
		Version:     1,
		ExportedAt:  time.Now().Format(time.RFC3339),
		Configs:     configs,
		Subscriptions: subs,
	}, nil
}

func (s *Service) Import(tenantID string, configs []models.ConfigKey, strategy string, userID string) (int, int, error) {
	imported := 0
	skipped := 0

	for _, cfg := range configs {
		existing := s.store.GetConfig(tenantID, cfg.KeyPath)
		if existing != nil {
			switch strategy {
			case models.ConflictStrategyOverwrite:
				if _, _, err := s.store.PutConfig(tenantID, cfg.KeyPath, cfg.Value, userID, nil); err != nil {
					return imported, skipped, err
				}
				imported++
			default:
				skipped++
			}
		} else {
			if _, _, err := s.store.PutConfig(tenantID, cfg.KeyPath, cfg.Value, userID, nil); err != nil {
				return imported, skipped, err
			}
			imported++
		}
	}

	_ = s.store.WriteAudit(&models.AuditLog{
		TenantID: tenantID,
		UserID:   userID,
		Action:   models.AuditActionImport,
		KeyPath:  fmt.Sprintf("import:%d", imported),
		Details:  fmt.Sprintf("imported %d, skipped %d, strategy: %s", imported, skipped, strategy),
	})

	return imported, skipped, nil
}

func (s *Service) notifySubscribers(cfg *models.ConfigKey, event string) {
	subs := s.store.GetMatchingSubscriptions(cfg.TenantID, cfg.KeyPath)

	payload := &models.NotificationPayload{
		KeyPath:   cfg.KeyPath,
		Value:     cfg.Value,
		Version:   cfg.Version,
		UpdatedAt: cfg.UpdatedAt,
		TenantID:  cfg.TenantID,
		Event:     event,
	}

	for _, sub := range subs {
		s.notifier.Notify(payload, sub)
	}
}

func (s *Service) GetConfigCount() int {
	return s.store.GetConfigCount()
}

func (s *Service) GetSubscriptionCount() int {
	return s.store.GetSubscriptionCount()
}

func (s *Service) CheckStorage() error {
	return s.store.CheckStorage()
}

func (s *Service) Dump() error {
	return s.store.Dump()
}

func (s *Service) Close() error {
	return s.store.Close()
}
