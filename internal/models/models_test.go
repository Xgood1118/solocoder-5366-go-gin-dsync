package models

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMetadataIsDeprecated(t *testing.T) {
	m := Metadata{}
	if m.IsDeprecated() {
		t.Error("empty metadata should not be deprecated")
	}
	m.DeprecatedAt = time.Now().Add(-time.Hour)
	if !m.IsDeprecated() {
		t.Error("past deprecated_at should be deprecated")
	}
}

func TestGrayRulesMatchEmpty(t *testing.T) {
	rules := GrayRules{}
	if rules.Match("anyuser") {
		t.Error("empty rules should never match")
	}
}

func TestGrayRulesMatchUserIDs(t *testing.T) {
	rules := GrayRules{UserIDs: []string{"u1", "u2"}}
	if !rules.Match("u1") {
		t.Error("u1 should match")
	}
	if rules.Match("u99") {
		t.Error("u99 should not match")
	}
}

func TestGrayRulesMatchUserIDMod(t *testing.T) {
	rules := GrayRules{UserIDMod: &UserIDModRule{Divisor: 1000, LessThan: 500}}
	if !rules.Match("user3") {
		t.Error("user3 should match mod rule (ascii sum 498 mod 1000 = 498 < 500)")
	}
	if rules.Match("user50") {
		t.Error("user50 should not match (ascii sum 548 mod 1000 = 548 >= 500)")
	}
}

func TestGrayRulesMatchNumericUserID(t *testing.T) {
	rules := GrayRules{UserIDMod: &UserIDModRule{Divisor: 100, LessThan: 10}}
	if !rules.Match("5") {
		t.Error("5 should match numeric mod rule")
	}
	if rules.Match("50") {
		t.Error("50 should not match")
	}
}

func TestNewSubscription(t *testing.T) {
	s := NewSubscription("sid", "db.*", "http://cb", "tid")
	if s.Status != SubscriptionStatusHealthy || !s.Active {
		t.Error("new subscription should be active/healthy")
	}
	if s.TenantID != "tid" || s.SubscriberID != "sid" {
		t.Error("field mismatch")
	}
	if len(s.ID) < 5 {
		t.Error("id should be auto-generated")
	}
}

func TestRoleValues(t *testing.T) {
	if string(RoleViewer) != "viewer" {
		t.Error("RoleViewer broken")
	}
	bs, err := json.Marshal(RoleAdmin)
	if err != nil || string(bs) != `"admin"` {
		t.Errorf("role marshal broken: %v %s", err, string(bs))
	}
	var r Role
	if err := json.Unmarshal([]byte(`"editor"`), &r); err != nil || r != RoleEditor {
		t.Errorf("role unmarshal broken: %v %v", err, r)
	}
}

func TestNotificationPayloadJSON(t *testing.T) {
	np := NotificationPayload{
		KeyPath:   "a.b",
		Value:     json.RawMessage(`123`),
		Version:   3,
		UpdatedAt: time.Now().Format(time.RFC3339),
		TenantID:  "t1",
		Event:     EventUpdated,
	}
	b, err := json.Marshal(np)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var back NotificationPayload
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if back.KeyPath != "a.b" || back.Version != 3 || back.Event != EventUpdated {
		t.Error("value mismatch")
	}
}

func TestContextKey(t *testing.T) {
	if string(TenantIDKey) == "" || string(UserIDKey) == "" {
		t.Error("context keys should not be empty")
	}
	if string(UserRoleKey) == "" {
		t.Error("user role key empty")
	}
}

func TestConflictStrategyConsts(t *testing.T) {
	if ConflictStrategySkip != "skip" || ConflictStrategyOverwrite != "overwrite" {
		t.Error("conflict strategy constants broken")
	}
}

func TestSubscriptionStatusConsts(t *testing.T) {
	if SubscriptionStatusHealthy != "healthy" || SubscriptionStatusUnhealthy != "unhealthy" {
		t.Error("subscription status constants broken")
	}
}
