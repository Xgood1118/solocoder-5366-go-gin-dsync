package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"configsync/internal/models"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "configsync-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	store, err := NewStore(dir)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("new store: %v", err)
	}
	return store, dir
}

func cleanStore(store *Store, dir string) {
	_ = store.Close()
	os.RemoveAll(dir)
}

func TestPutAndGetConfig(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	val := json.RawMessage(`"localhost"`)
	cfg, created, err := store.PutConfig("t1", "db.host", val, "alice", nil)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if !created {
		t.Error("expected created=true")
	}
	if cfg.Version != 1 {
		t.Errorf("version=1 got %d", cfg.Version)
	}
	if cfg.UpdatedBy != "alice" {
		t.Errorf("updatedBy=alice got %s", cfg.UpdatedBy)
	}

	got := store.GetConfig("t1", "db.host")
	if got == nil {
		t.Fatal("get returned nil")
	}
	if string(got.Value) != `"localhost"` {
		t.Errorf("value got %s", got.Value)
	}

	val2 := json.RawMessage(`"remotehost"`)
	cfg2, created2, err := store.PutConfig("t1", "db.host", val2, "bob", nil)
	if err != nil {
		t.Fatalf("put2: %v", err)
	}
	if created2 {
		t.Error("expected created=false")
	}
	if cfg2.Version != 2 {
		t.Errorf("version=2 got %d", cfg2.Version)
	}
}

func TestTenantIsolation(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	val := json.RawMessage(`"v1"`)
	_, _, _ = store.PutConfig("t1", "k1", val, "u1", nil)

	if store.GetConfig("t2", "k1") != nil {
		t.Error("t2 should not see t1's config")
	}

	got := store.GetConfig("t1", "k1")
	if got == nil {
		t.Error("t1 should see its own config")
	}
}

func TestIfMatchConflict(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	val := json.RawMessage(`"v1"`)
	_, _, _ = store.PutConfig("t1", "k1", val, "u1", nil)

	badVer := int64(999)
	cfg, _, err := store.PutConfig("t1", "k1", json.RawMessage(`"v2"`), "u2", &badVer)
	if err == nil {
		t.Fatal("expected version mismatch error")
	}
	if cfg == nil {
		t.Fatal("cfg should not be nil on conflict")
	}
	if cfg.Version != 1 {
		t.Errorf("expected current version 1 got %d", cfg.Version)
	}

	goodVer := int64(1)
	cfg2, _, err := store.PutConfig("t1", "k1", json.RawMessage(`"v2"`), "u2", &goodVer)
	if err != nil {
		t.Fatalf("expected success got %v", err)
	}
	if cfg2.Version != 2 {
		t.Errorf("version=2 got %d", cfg2.Version)
	}
}

func TestListConfigsByPrefix(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	v := json.RawMessage(`"1"`)
	_, _, _ = store.PutConfig("t1", "db.host", v, "u", nil)
	_, _, _ = store.PutConfig("t1", "db.port", v, "u", nil)
	_, _, _ = store.PutConfig("t1", "app.name", v, "u", nil)

	all := store.ListConfigs("t1", "")
	if len(all) != 3 {
		t.Errorf("expected 3 got %d", len(all))
	}

	db := store.ListConfigs("t1", "db.")
	if len(db) != 2 {
		t.Errorf("expected 2 db configs got %d", len(db))
	}
}

func TestGetConfigVersion(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	v1 := json.RawMessage(`"v1"`)
	v2 := json.RawMessage(`"v2"`)
	v3 := json.RawMessage(`"v3"`)
	_, _, _ = store.PutConfig("t1", "k", v1, "u", nil)
	_, _, _ = store.PutConfig("t1", "k", v2, "u", nil)
	_, _, _ = store.PutConfig("t1", "k", v3, "u", nil)

	gotV1 := store.GetConfigVersion("t1", "k", 1)
	if gotV1 == nil || string(gotV1.Value) != `"v1"` {
		t.Errorf("version 1 not found, got %+v", gotV1)
	}
	gotV2 := store.GetConfigVersion("t1", "k", 2)
	if gotV2 == nil || string(gotV2.Value) != `"v2"` {
		t.Errorf("version 2 not found")
	}
	gotV4 := store.GetConfigVersion("t1", "k", 999)
	if gotV4 != nil {
		t.Error("version 999 should be nil")
	}
}

func TestHistory(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	for i := 1; i <= 5; i++ {
		v := json.RawMessage(`"` + "v" + string(rune('0'+i)) + `"`)
		_, _, _ = store.PutConfig("t1", "k", v, "u", nil)
	}

	history := store.GetHistory("t1", "k")
	if len(history) != 5 {
		t.Errorf("expected 5 history entries got %d", len(history))
	}
	if history[0].Version != 5 {
		t.Errorf("history[0] should be latest (v5), got %d", history[0].Version)
	}
	if history[4].Version != 1 {
		t.Errorf("history[4] should be v1 got %d", history[4].Version)
	}
}

func TestRollback(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	v1 := json.RawMessage(`"v1"`)
	v2 := json.RawMessage(`"v2"`)
	_, _, _ = store.PutConfig("t1", "k", v1, "u", nil)
	_, _, _ = store.PutConfig("t1", "k", v2, "u", nil)

	cfg, err := store.Rollback("t1", "k", 1, "admin")
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if cfg.Version != 3 {
		t.Errorf("version after rollback should be 3 got %d", cfg.Version)
	}
	if string(cfg.Value) != `"v1"` {
		t.Errorf("value after rollback should be v1 got %s", cfg.Value)
	}

	if _, err := store.Rollback("t1", "k", 999, "admin"); err == nil {
		t.Error("rollback to nonexistent version should fail")
	}
}

func TestSubscriptionCRUDAndMatching(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	sub := models.NewSubscription("svc1", "db.*", "http://localhost:8080/cb", "t1")
	store.CreateSubscription(sub)

	got := store.GetSubscription("t1", sub.ID)
	if got == nil {
		t.Fatal("get sub failed")
	}
	if got.KeyPattern != "db.*" {
		t.Error("pattern mismatch")
	}

	store2 := store.GetSubscription("t2", sub.ID)
	if store2 != nil {
		t.Error("tenant isolation failed for sub")
	}

	if !store.DeleteSubscription("t1", sub.ID) {
		t.Error("delete should succeed")
	}
	if store.GetSubscription("t1", sub.ID) != nil {
		t.Error("should be deleted")
	}
}

func TestPatternMatching(t *testing.T) {
	tests := []struct {
		pattern string
		key     string
		match   bool
	}{
		{"db.*", "db.host", true},
		{"db.*", "db.port", true},
		{"db.*", "db", false},
		{"db.*", "app.host", false},
		{"db.**", "db.pool.max", true},
		{"db.**", "db.host", true},
		{"**", "anything.at.all", true},
		{"a.b.c", "a.b.c", true},
		{"a.b.c", "a.b.d", false},
	}
	for _, tt := range tests {
		got := matchPattern(tt.pattern, tt.key)
		if got != tt.match {
			t.Errorf("pattern=%s key=%s expect=%v got=%v", tt.pattern, tt.key, tt.match, got)
		}
	}
}

func TestGetMatchingSubscriptions(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	sub1 := models.NewSubscription("s1", "db.*", "http://a", "t1")
	sub2 := models.NewSubscription("s2", "db.**", "http://b", "t1")
	sub3 := models.NewSubscription("s3", "app.*", "http://c", "t1")
	subBad := models.NewSubscription("s4", "db.*", "http://d", "t1")
	subBad.Status = models.SubscriptionStatusUnhealthy
	subInactive := models.NewSubscription("s5", "db.*", "http://e", "t1")
	subInactive.Active = false

	store.CreateSubscription(sub1)
	store.CreateSubscription(sub2)
	store.CreateSubscription(sub3)
	store.CreateSubscription(subBad)
	store.CreateSubscription(subInactive)

	matches := store.GetMatchingSubscriptions("t1", "db.host")
	if len(matches) != 2 {
		t.Errorf("expected 2 matches got %d", len(matches))
		for _, m := range matches {
			t.Logf("matched: %s %s", m.SubscriberID, m.KeyPattern)
		}
	}

	t2matches := store.GetMatchingSubscriptions("t2", "db.host")
	if len(t2matches) != 0 {
		t.Errorf("t2 should have 0 matches got %d", len(t2matches))
	}
}

func TestSubscriptionStatusAndRecover(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	sub := models.NewSubscription("s1", "k.*", "http://a", "t1")
	store.CreateSubscription(sub)

	store.UpdateSubscriptionStatus("t1", sub.ID, models.SubscriptionStatusUnhealthy, "connection refused")
	got := store.GetSubscription("t1", sub.ID)
	if got.Status != models.SubscriptionStatusUnhealthy {
		t.Errorf("expected unhealthy got %s", got.Status)
	}
	if got.LastError != "connection refused" {
		t.Errorf("expected last error got %s", got.LastError)
	}

	if !store.RecoverSubscription("t1", sub.ID) {
		t.Error("recover should succeed")
	}
	got2 := store.GetSubscription("t1", sub.ID)
	if got2.Status != models.SubscriptionStatusHealthy {
		t.Errorf("should be healthy now got %s", got2.Status)
	}
	if got2.LastError != "" {
		t.Error("last error should be cleared")
	}
}

func TestDumpAndLoad(t *testing.T) {
	dir, _ := os.MkdirTemp("", "configsync-dump-*")
	defer os.RemoveAll(dir)

	s1, _ := NewStore(dir)
	v := json.RawMessage(`"x"`)
	_, _, _ = s1.PutConfig("t1", "k1", v, "u", nil)
	sub := models.NewSubscription("s1", "k.*", "http://cb", "t1")
	s1.CreateSubscription(sub)

	if err := s1.Dump(); err != nil {
		t.Fatalf("dump: %v", err)
	}
	_ = s1.Close()

	s2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	defer s2.Close()

	got := s2.GetConfig("t1", "k1")
	if got == nil || string(got.Value) != `"x"` {
		t.Error("config not restored from dump")
	}
	subs := s2.ListSubscriptions("t1")
	if len(subs) != 1 {
		t.Errorf("expected 1 sub got %d", len(subs))
	}
}

func TestAuditLog(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	log1 := &models.AuditLog{
		TenantID: "t1",
		UserID:   "u1",
		Action:   models.AuditActionUpdate,
		KeyPath:  "k1",
	}
	if err := store.WriteAudit(log1); err != nil {
		t.Fatalf("write audit: %v", err)
	}

	logs, err := store.ReadAudit("t1", "k1")
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("expected 1 audit log got %d", len(logs))
	}

	t2logs, _ := store.ReadAudit("t2", "")
	if len(t2logs) != 0 {
		t.Errorf("t2 should see 0 audit logs got %d", len(t2logs))
	}
}

func TestCheckStorage(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	if err := store.CheckStorage(); err != nil {
		t.Errorf("check storage failed: %v", err)
	}
}

func TestLongPoll(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	done := make(chan struct{})
	var result *models.ConfigKey
	go func() {
		result = store.LongPoll("t1", "k1", 2*time.Second)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	v := json.RawMessage(`"hello"`)
	_, _, _ = store.PutConfig("t1", "k1", v, "u", nil)

	<-done
	if result == nil {
		t.Fatal("long poll should return cfg")
	}
	if string(result.Value) != `"hello"` {
		t.Errorf("unexpected value %s", result.Value)
	}
}

func TestLongPollTimeout(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	v := json.RawMessage(`"initial"`)
	_, _, _ = store.PutConfig("t1", "k2", v, "u", nil)

	start := time.Now()
	result := store.LongPoll("t1", "k2", 200*time.Millisecond)
	elapsed := time.Since(start)

	if result == nil {
		t.Error("should return current value on timeout")
	}
	if elapsed < 180*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Errorf("unexpected elapsed time: %v", elapsed)
	}
}

func TestMaxHistory(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)
	store.maxHistory = 5

	for i := 0; i < 10; i++ {
		v := json.RawMessage(`"` + string(rune('a'+i)) + `"`)
		_, _, _ = store.PutConfig("t1", "k", v, "u", nil)
	}

	h := store.GetHistory("t1", "k")
	if len(h) > 5 {
		t.Errorf("history should be capped at 5 got %d", len(h))
	}
}

func TestCompiledPattern(t *testing.T) {
	re := CompilePattern("db.*")
	if !re.MatchString("db.host") {
		t.Error("should match db.host")
	}
	if re.MatchString("db.a.b") {
		t.Error("should not match db.a.b (single star)")
	}

	re2 := CompilePattern("db.**")
	if !re2.MatchString("db.pool.max") {
		t.Error("** should match multi segments")
	}
}

func TestListSubscriptions(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	sub1 := models.NewSubscription("a", "*", "http://a", "t1")
	sub2 := models.NewSubscription("b", "*", "http://b", "t1")
	sub3 := models.NewSubscription("c", "*", "http://c", "t2")
	store.CreateSubscription(sub1)
	store.CreateSubscription(sub2)
	store.CreateSubscription(sub3)

	l1 := store.ListSubscriptions("t1")
	if len(l1) != 2 {
		t.Errorf("t1 should have 2 subs got %d", len(l1))
	}
	l2 := store.ListSubscriptions("t2")
	if len(l2) != 1 {
		t.Errorf("t2 should have 1 sub got %d", len(l2))
	}
}

func TestCounts(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	v := json.RawMessage(`"1"`)
	_, _, _ = store.PutConfig("t1", "a", v, "u", nil)
	_, _, _ = store.PutConfig("t2", "b", v, "u", nil)

	if store.GetConfigCount() != 2 {
		t.Errorf("config count=2 got %d", store.GetConfigCount())
	}

	sub := models.NewSubscription("s", "*", "http://x", "t1")
	store.CreateSubscription(sub)
	if store.GetSubscriptionCount() != 1 {
		t.Errorf("sub count=1 got %d", store.GetSubscriptionCount())
	}
}

func TestGrayConfigAndMetadata(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	v := json.RawMessage(`"base"`)
	_, _, _ = store.PutConfig("t1", "k", v, "u", nil)

	gray := &models.GrayConfig{
		Value: json.RawMessage(`"gray"`),
		GrayRules: models.GrayRules{
			UserIDMod: &models.UserIDModRule{Divisor: 100, LessThan: 10},
		},
		CreatedAt: time.Now().Format(time.RFC3339),
		CreatedBy: "u",
	}
	if err := store.SetGrayConfig("t1", "k", gray); err != nil {
		t.Fatalf("set gray: %v", err)
	}

	got := store.GetConfig("t1", "k")
	if got.GrayConfig == nil {
		t.Fatal("gray config missing")
	}

	md := &models.Metadata{Description: "desc", Tags: []string{"tag1"}}
	if err := store.UpdateMetadata("t1", "k", md); err != nil {
		t.Fatalf("update metadata: %v", err)
	}
	got2 := store.GetConfig("t1", "k")
	if got2.Metadata == nil || got2.Metadata.Description != "desc" {
		t.Error("metadata not saved")
	}
}

func TestDumpPath(t *testing.T) {
	dir, _ := os.MkdirTemp("", "dump-*")
	defer os.RemoveAll(dir)

	store, _ := NewStore(dir)
	v := json.RawMessage(`"1"`)
	_, _, _ = store.PutConfig("t1", "k", v, "u", nil)
	_ = store.Dump()

	dumpPath := filepath.Join(dir, "dump.json")
	data, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read dump: %v", err)
	}
	if len(data) == 0 {
		t.Error("dump file empty")
	}
	_ = store.Close()
}

func TestGetAllConfigsAndSubs(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	v := json.RawMessage(`"v"`)
	_, _, _ = store.PutConfig("t1", "k1", v, "u", nil)
	_, _, _ = store.PutConfig("t2", "k2", v, "u", nil)

	if len(store.GetAllConfigs()) != 2 {
		t.Errorf("all configs=2 got %d", len(store.GetAllConfigs()))
	}

	s1 := models.NewSubscription("a", "*", "http://a", "t1")
	s2 := models.NewSubscription("b", "*", "http://b", "t3")
	store.CreateSubscription(s1)
	store.CreateSubscription(s2)
	if len(store.GetAllSubscriptions()) != 2 {
		t.Errorf("all subs=2 got %d", len(store.GetAllSubscriptions()))
	}
}

func TestDeprecatedConfigBlockWrite(t *testing.T) {
	store, dir := newTestStore(t)
	defer cleanStore(store, dir)

	v := json.RawMessage(`"v1"`)
	cfg, _, _ := store.PutConfig("t1", "k", v, "u", nil)

	md := &models.Metadata{DeprecatedAt: time.Now().Add(-time.Hour)}
	cfg.Metadata = md
	_ = store.UpdateMetadata("t1", "k", md)

	_, _, err := store.PutConfig("t1", "k", json.RawMessage(`"v2"`), "u2", nil)
	if err == nil {
		t.Error("writing to deprecated key should fail")
	}
}
