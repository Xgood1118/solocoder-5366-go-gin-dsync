package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ConfigKey struct {
	KeyPath     string          `json:"key_path"`
	Value       json.RawMessage `json:"value"`
	Version     int64           `json:"version"`
	UpdatedAt   string          `json:"updated_at"`
	UpdatedBy   string          `json:"updated_by"`
	TenantID    string          `json:"tenant_id"`
	Metadata    *Metadata       `json:"metadata,omitempty"`
	GrayConfig  *GrayConfig     `json:"gray_config,omitempty"`
	History     []ConfigVersion `json:"-"`
}

type ConfigVersion struct {
	Version   int64           `json:"version"`
	Value     json.RawMessage `json:"value"`
	UpdatedAt string          `json:"updated_at"`
	UpdatedBy string          `json:"updated_by"`
}

type Metadata struct {
	Description string    `json:"description,omitempty"`
	Owner       string    `json:"owner,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	DeprecatedAt time.Time `json:"deprecated_at,omitempty"`
	Replacement string    `json:"replacement,omitempty"`
}

func (m *Metadata) IsDeprecated() bool {
	return m != nil && !m.DeprecatedAt.IsZero() && time.Now().After(m.DeprecatedAt)
}

type GrayConfig struct {
	Value     json.RawMessage `json:"value"`
	GrayRules GrayRules       `json:"gray_rules"`
	CreatedAt string          `json:"created_at"`
	CreatedBy string          `json:"created_by"`
}

type GrayRules struct {
	UserIDMod *UserIDModRule `json:"user_id_mod,omitempty"`
	UserIDs   []string       `json:"user_ids,omitempty"`
}

type UserIDModRule struct {
	Divisor int64 `json:"divisor"`
	LessThan int64 `json:"less_than"`
}

func (r *GrayRules) Match(grayUserID string) bool {
	if r == nil {
		return false
	}
	if r.UserIDMod != nil && grayUserID != "" {
		var userIDNum int64
		if err := json.Unmarshal([]byte(grayUserID), &userIDNum); err != nil {
			for _, c := range grayUserID {
				userIDNum += int64(c)
			}
		}
		if userIDNum%r.UserIDMod.Divisor < r.UserIDMod.LessThan {
			return true
		}
	}
	if len(r.UserIDs) > 0 {
		for _, id := range r.UserIDs {
			if id == grayUserID {
				return true
			}
		}
	}
	return false
}

type Subscription struct {
	ID           string    `json:"id"`
	SubscriberID string    `json:"subscriber_id"`
	KeyPattern   string    `json:"key_pattern"`
	CallbackURL  string    `json:"callback_url"`
	CreatedAt    string    `json:"created_at"`
	TenantID     string    `json:"tenant_id"`
	Active       bool      `json:"active"`
	Status       string    `json:"status"`
	LastNotifyAt string    `json:"last_notify_at,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
}

const (
	SubscriptionStatusHealthy   = "healthy"
	SubscriptionStatusUnhealthy = "unhealthy"
)

func NewSubscription(subscriberID, keyPattern, callbackURL, tenantID string) *Subscription {
	return &Subscription{
		ID:           uuid.New().String(),
		SubscriberID: subscriberID,
		KeyPattern:   keyPattern,
		CallbackURL:  callbackURL,
		CreatedAt:    time.Now().Format(time.RFC3339),
		TenantID:     tenantID,
		Active:       true,
		Status:       SubscriptionStatusHealthy,
	}
}

type NotificationPayload struct {
	KeyPath   string          `json:"key_path"`
	Value     json.RawMessage `json:"value"`
	Version   int64           `json:"version"`
	UpdatedAt string          `json:"updated_at"`
	TenantID  string          `json:"tenant_id"`
	Event     string          `json:"event"`
}

const (
	EventUpdated = "updated"
	EventRollback = "rollback"
)

type AuditLog struct {
	Timestamp   string          `json:"timestamp"`
	TenantID    string          `json:"tenant_id"`
	UserID      string          `json:"user_id"`
	Action      string          `json:"action"`
	KeyPath     string          `json:"key_path"`
	OldVersion  int64           `json:"old_version,omitempty"`
	NewVersion  int64           `json:"new_version,omitempty"`
	OldValue    json.RawMessage `json:"old_value,omitempty"`
	NewValue    json.RawMessage `json:"new_value,omitempty"`
	Details     string          `json:"details,omitempty"`
}

const (
	AuditActionCreate   = "create"
	AuditActionUpdate   = "update"
	AuditActionRollback = "rollback"
	AuditActionDelete   = "delete"
	AuditActionGray     = "gray"
	AuditActionPromote  = "promote"
	AuditActionBatch    = "batch"
	AuditActionImport   = "import"
)

type PreviewRequest struct {
	Changes []PreviewChange `json:"changes"`
}

type PreviewChange struct {
	KeyPath string          `json:"key_path"`
	Value   json.RawMessage `json:"value"`
}

type PreviewResponse struct {
	AffectedKeys       []string           `json:"affected_keys"`
	AffectedSubscribers []AffectedSubscriber `json:"affected_subscribers"`
	Changes            []PreviewDiff      `json:"changes"`
}

type PreviewDiff struct {
	KeyPath     string          `json:"key_path"`
	OldValue    json.RawMessage `json:"old_value,omitempty"`
	NewValue    json.RawMessage `json:"new_value"`
	Changed     bool            `json:"changed"`
}

type AffectedSubscriber struct {
	SubscriberID string   `json:"subscriber_id"`
	CallbackURL  string   `json:"callback_url"`
	KeyPattern   string   `json:"key_pattern"`
	MatchedKeys  []string `json:"matched_keys"`
}

type BatchRequest struct {
	Changes []BatchChange `json:"changes"`
}

type BatchChange struct {
	KeyPath string          `json:"key_path"`
	Value   json.RawMessage `json:"value"`
}

type ExportData struct {
	Version       int64             `json:"export_version"`
	ExportedAt    string            `json:"exported_at"`
	Configs       []*ConfigKey      `json:"configs"`
	Subscriptions []*Subscription   `json:"subscriptions,omitempty"`
}

type ImportRequest struct {
	Configs     []ConfigKey `json:"configs"`
	ConflictStrategy string  `json:"conflict_strategy"`
}

const (
	ConflictStrategySkip      = "skip"
	ConflictStrategyOverwrite = "overwrite"
)

type Role string

const (
	RoleViewer Role = "viewer"
	RoleEditor Role = "editor"
	RoleAdmin  Role = "admin"
)

type ContextKey string

const (
	TenantIDKey  ContextKey = "tenant_id"
	UserIDKey    ContextKey = "user_id"
	UserRoleKey  ContextKey = "user_role"
)
