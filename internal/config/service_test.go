package config

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"configsync/internal/models"
	"configsync/internal/notifier"
	"configsync/internal/storage"
)

type MockNotifier struct {
	mu       sync.Mutex
	notifies []mockNotifyCall
	stopped  int32
	pending  int64
}

type mockNotifyCall struct {
	payload      *models.NotificationPayload
	subscription *models.Subscription
}

func NewMockNotifier() *MockNotifier {
	return &MockNotifier{}
}

func (m *MockNotifier) Notify(payload *models.NotificationPayload, sub *models.Subscription) {
	if atomic.LoadInt32(&m.stopped) == 1 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifies = append(m.notifies, mockNotifyCall{payload, sub})
}

func (m *MockNotifier) Start(_ context.Context) {}
func (m *MockNotifier) Stop() error {
	atomic.StoreInt32(&m.stopped, 1)
	return nil
}
func (m *MockNotifier) Wait(_ time.Duration) bool { return true }
func (m *MockNotifier) PendingCount() int64       { return atomic.LoadInt64(&m.pending) }

func (m *MockNotifier) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.notifies)
}

func newTestService(t *testing.T) (*Service, *MockNotifier, *storage.Store, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "configsync-svc-test-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	store, err := storage.NewStore(dir)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("store: %v", err)
	}
	mn := NewMockNotifier()
	svc := NewService(store, mn)
	return svc, mn, store, dir
}

func cleanSvc(store *storage.Store, dir string) {
	_ = store.Close()
	os.RemoveAll(dir)
}

func TestUpdateConfigCreateAndNotify(t *testing.T) {
	svc, mn, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	sub := models.NewSubscription("svc-a", "db.*", "http://cb", "t1")
	store.CreateSubscription(sub)

	v := json.RawMessage(`"localhost"`)
	res, err := svc.UpdateConfig("t1", "db.host", v, "alice", nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !res.Created {
		t.Error("should be created")
	}
	if res.Config.Version != 1 {
		t.Errorf("v=1 got %d", res.Config.Version)
	}

	time.Sleep(10 * time.Millisecond)
	if mn.CallCount() < 1 {
		t.Errorf("expected at least 1 notification got %d", mn.CallCount())
	}

	v2 := json.RawMessage(`"remote"`)
	res2, err := svc.UpdateConfig("t1", "db.host", v2, "bob", nil)
	if err != nil {
		t.Fatalf("update2: %v", err)
	}
	if res2.Created {
		t.Error("should not be created")
	}
	if res2.Config.Version != 2 {
		t.Errorf("v=2 got %d", res2.Config.Version)
	}
}

func TestUpdateConfigIfMatch412(t *testing.T) {
	svc, _, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	v := json.RawMessage(`"v1"`)
	_, _ = svc.UpdateConfig("t1", "k", v, "u", nil)

	bad := int64(999)
	res, err := svc.UpdateConfig("t1", "k", json.RawMessage(`"v2"`), "u", &bad)
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if !res.Conflict {
		t.Error("Conflict should be true")
	}
	if res.CurrentVersion != 1 {
		t.Errorf("CurrentVersion=1 got %d", res.CurrentVersion)
	}

	good := int64(1)
	res2, err := svc.UpdateConfig("t1", "k", json.RawMessage(`"v2"`), "u", &good)
	if err != nil {
		t.Fatalf("expected ok got %v", err)
	}
	if res2.Config.Version != 2 {
		t.Errorf("v=2 got %d", res2.Config.Version)
	}
}

func TestGetConfigWithGray(t *testing.T) {
	svc, _, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	v := json.RawMessage(`"base"`)
	_, _ = svc.UpdateConfig("t1", "k", v, "u", nil)

	grayRules := models.GrayRules{
		UserIDMod: &models.UserIDModRule{Divisor: 100, LessThan: 50},
	}
	_ = svc.SetGrayConfig("t1", "k", json.RawMessage(`"gray"`), grayRules, "admin")

	noGray := svc.GetConfig("t1", "k", nil, "")
	if string(noGray.Value) != `"base"` {
		t.Errorf("base got %s", noGray.Value)
	}

	inGray := svc.GetConfig("t1", "k", nil, "5")
	if string(inGray.Value) != `"gray"` {
		t.Errorf("gray user 5 should get gray value, got %s", inGray.Value)
	}

	outGray := svc.GetConfig("t1", "k", nil, "99")
	if string(outGray.Value) != `"base"` {
		t.Errorf("user 99 should get base got %s", outGray.Value)
	}
}

func TestGetConfigByVersion(t *testing.T) {
	svc, _, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	_, _ = svc.UpdateConfig("t1", "k", json.RawMessage(`"v1"`), "u", nil)
	_, _ = svc.UpdateConfig("t1", "k", json.RawMessage(`"v2"`), "u", nil)

	v1 := int64(1)
	cfg := svc.GetConfig("t1", "k", &v1, "")
	if cfg == nil || string(cfg.Value) != `"v1"` {
		t.Errorf("v1 not found, got %+v", cfg)
	}
}

func TestRollback(t *testing.T) {
	svc, _, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	_, _ = svc.UpdateConfig("t1", "k", json.RawMessage(`"v1"`), "u", nil)
	_, _ = svc.UpdateConfig("t1", "k", json.RawMessage(`"v2"`), "u", nil)

	cfg, err := svc.Rollback("t1", "k", 1, "admin")
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if cfg.Version != 3 {
		t.Errorf("v=3 got %d", cfg.Version)
	}
	if string(cfg.Value) != `"v1"` {
		t.Errorf("should be v1 got %s", cfg.Value)
	}
}

func TestResolve(t *testing.T) {
	svc, _, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	_, _ = svc.UpdateConfig("t1", "k", json.RawMessage(`"old"`), "u", nil)
	_, _ = svc.UpdateConfig("t1", "k", json.RawMessage(`"new"`), "u", nil)

	cfg, err := svc.Resolve("t1", "k", 1, 2, "pick_old", "admin")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if string(cfg.Value) != `"old"` {
		t.Errorf("pick_old should return v1 got %s", cfg.Value)
	}

	if _, err := svc.Resolve("t1", "k", 1, 2, "invalid", "admin"); err == nil {
		t.Error("invalid resolution should fail")
	}
}

func TestListConfigs(t *testing.T) {
	svc, _, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	_, _ = svc.UpdateConfig("t1", "a.b", json.RawMessage(`1`), "u", nil)
	_, _ = svc.UpdateConfig("t1", "a.c", json.RawMessage(`2`), "u", nil)
	_, _ = svc.UpdateConfig("t1", "b", json.RawMessage(`3`), "u", nil)

	all := svc.ListConfigs("t1", "")
	if len(all) != 3 {
		t.Errorf("all=3 got %d", len(all))
	}
	prefixA := svc.ListConfigs("t1", "a.")
	if len(prefixA) != 2 {
		t.Errorf("prefix a. = 2 got %d", len(prefixA))
	}
}

func TestHistory(t *testing.T) {
	svc, _, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	for i := 0; i < 4; i++ {
		_, _ = svc.UpdateConfig("t1", "k", json.RawMessage(`"`+string(rune('a'+i))+`"`), "u", nil)
	}

	h := svc.GetHistory("t1", "k")
	if len(h) != 4 {
		t.Errorf("history len=4 got %d", len(h))
	}
	if h[0].Version != 4 {
		t.Errorf("first history entry should be v4 got %d", h[0].Version)
	}
}

func TestPreview(t *testing.T) {
	svc, _, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	_, _ = svc.UpdateConfig("t1", "db.host", json.RawMessage(`"old"`), "u", nil)
	sub := models.NewSubscription("s1", "db.*", "http://cb", "t1")
	store.CreateSubscription(sub)

	changes := []models.PreviewChange{
		{KeyPath: "db.host", Value: json.RawMessage(`"new"`)},
		{KeyPath: "db.port", Value: json.RawMessage(`5432`)},
		{KeyPath: "db.host", Value: json.RawMessage(`"old"`)},
	}

	resp, err := svc.Preview("t1", changes)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(resp.AffectedKeys) != 2 {
		t.Errorf("affected keys=2 got %d: %v", len(resp.AffectedKeys), resp.AffectedKeys)
	}
	if len(resp.AffectedSubscribers) != 1 {
		t.Errorf("affected subs=1 got %d", len(resp.AffectedSubscribers))
	}
}

func TestBatchUpdate(t *testing.T) {
	svc, mn, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	sub := models.NewSubscription("s", "k.*", "http://cb", "t1")
	store.CreateSubscription(sub)

	changes := []models.BatchChange{
		{KeyPath: "k.a", Value: json.RawMessage(`1`)},
		{KeyPath: "k.b", Value: json.RawMessage(`2`)},
	}
	results, err := svc.BatchUpdate("t1", changes, "u")
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("2 results got %d", len(results))
	}

	time.Sleep(10 * time.Millisecond)
	if mn.CallCount() < 2 {
		t.Errorf("2 notifications got %d", mn.CallCount())
	}
}

func TestGrayPromote(t *testing.T) {
	svc, _, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	_, _ = svc.UpdateConfig("t1", "k", json.RawMessage(`"base"`), "u", nil)
	rules := models.GrayRules{UserIDs: []string{"u1"}}
	_ = svc.SetGrayConfig("t1", "k", json.RawMessage(`"gray"`), rules, "admin")

	cfg, err := svc.PromoteGray("t1", "k", "admin")
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if string(cfg.Value) != `"gray"` {
		t.Errorf("promoted value should be gray got %s", cfg.Value)
	}
	if cfg.Version != 2 {
		t.Errorf("v=2 got %d", cfg.Version)
	}
}

func TestSubscriptionOps(t *testing.T) {
	svc, _, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	sub := svc.CreateSubscription("t1", "s1", "k.*", "http://cb")
	if sub == nil || sub.ID == "" {
		t.Fatal("create sub failed")
	}

	got := svc.GetSubscription("t1", sub.ID)
	if got == nil {
		t.Fatal("get sub failed")
	}
	if got.SubscriberID != "s1" {
		t.Errorf("subscriber id s1 got %s", got.SubscriberID)
	}

	list := svc.ListSubscriptions("t1")
	if len(list) != 1 {
		t.Errorf("list len 1 got %d", len(list))
	}

	if !svc.DeleteSubscription("t1", sub.ID) {
		t.Error("delete should succeed")
	}
	if svc.GetSubscription("t1", sub.ID) != nil {
		t.Error("should be deleted")
	}

	sub2 := svc.CreateSubscription("t1", "s2", "*", "http://bad")
	store.UpdateSubscriptionStatus("t1", sub2.ID, models.SubscriptionStatusUnhealthy, "err")
	if !svc.RecoverSubscription("t1", sub2.ID) {
		t.Error("recover should succeed")
	}
}

func TestAuditLogs(t *testing.T) {
	svc, _, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	_, _ = svc.UpdateConfig("t1", "k1", json.RawMessage(`"v1"`), "alice", nil)
	_, _ = svc.UpdateConfig("t1", "k1", json.RawMessage(`"v2"`), "bob", nil)

	logs, err := svc.GetAuditLogs("t1", "k1")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(logs) < 2 {
		t.Errorf("expected >=2 audit logs got %d", len(logs))
	}
}

func TestExportAndImport(t *testing.T) {
	svc, _, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	_, _ = svc.UpdateConfig("t1", "k1", json.RawMessage(`"x"`), "u", nil)
	_, _ = svc.UpdateConfig("t1", "k2", json.RawMessage(`"y"`), "u", nil)

	data, err := svc.Export("t1")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(data.Configs) != 2 {
		t.Errorf("export 2 configs got %d", len(data.Configs))
	}

	dataAll, err := svc.ExportAll()
	if err != nil {
		t.Fatalf("export all: %v", err)
	}
	if len(dataAll.Configs) != 2 {
		t.Errorf("export all 2 got %d", len(dataAll.Configs))
	}

	svc2, _, store2, dir2 := newTestService(t)
	defer cleanSvc(store2, dir2)

	cfgs := make([]models.ConfigKey, 0, len(data.Configs))
	for _, c := range data.Configs {
		cfgs = append(cfgs, *c)
	}
	imported, skipped, err := svc2.Import("t1", cfgs, models.ConflictStrategySkip, "admin")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imported != 2 {
		t.Errorf("imported 2 got %d", imported)
	}
	if skipped != 0 {
		t.Errorf("skipped 0 got %d", skipped)
	}

	_, _, err = svc2.Import("t1", cfgs, models.ConflictStrategySkip, "admin")
	if err != nil {
		t.Fatalf("import 2: %v", err)
	}

	_, _, err = svc2.Import("t1", cfgs, models.ConflictStrategyOverwrite, "admin")
	if err != nil {
		t.Fatalf("import overwrite: %v", err)
	}
}

func TestLongPoll(t *testing.T) {
	svc, _, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	done := make(chan struct{})
	var result *models.ConfigKey
	go func() {
		result = svc.LongPoll("t1", "lp", 2*time.Second)
		close(done)
	}()

	time.Sleep(30 * time.Millisecond)
	_, _ = svc.UpdateConfig("t1", "lp", json.RawMessage(`"updated"`), "u", nil)

	<-done
	if result == nil {
		t.Fatal("long poll result nil")
	}
	if string(result.Value) != `"updated"` {
		t.Errorf("value mismatch %s", result.Value)
	}
}

func TestMetadata(t *testing.T) {
	svc, _, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	_, _ = svc.UpdateConfig("t1", "k", json.RawMessage(`1`), "u", nil)

	md := &models.Metadata{
		Description: "test key",
		Tags:        []string{"db", "prod"},
		Owner:       "team-a",
	}
	if err := svc.UpdateMetadata("t1", "k", md); err != nil {
		t.Fatalf("update metadata: %v", err)
	}

	cfg := svc.GetConfig("t1", "k", nil, "")
	if cfg.Metadata == nil || cfg.Metadata.Description != "test key" {
		t.Error("metadata not saved")
	}
}

func TestDeprecatedKeyBlockUpdate(t *testing.T) {
	svc, _, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	_, _ = svc.UpdateConfig("t1", "k", json.RawMessage(`"v1"`), "u", nil)

	md := &models.Metadata{DeprecatedAt: time.Now().Add(-time.Hour)}
	_ = svc.UpdateMetadata("t1", "k", md)

	_, err := svc.UpdateConfig("t1", "k", json.RawMessage(`"v2"`), "u", nil)
	if err == nil {
		t.Error("deprecated key should fail update")
	}
}

func TestNotifierInterfaceCompliance(t *testing.T) {
	var _ notifier.Notifier = NewMockNotifier()
	var _ notifier.Notifier = (*MockNotifier)(nil)
}

func TestCounts(t *testing.T) {
	svc, _, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	if svc.GetConfigCount() != 0 {
		t.Errorf("empty count=0 got %d", svc.GetConfigCount())
	}
	_, _ = svc.UpdateConfig("t1", "a", json.RawMessage(`1`), "u", nil)
	_, _ = svc.UpdateConfig("t2", "b", json.RawMessage(`2`), "u", nil)
	if svc.GetConfigCount() != 2 {
		t.Errorf("count=2 got %d", svc.GetConfigCount())
	}

	_ = svc.CreateSubscription("t1", "s1", "*", "http://a")
	if svc.GetSubscriptionCount() != 1 {
		t.Errorf("sub count=1 got %d", svc.GetSubscriptionCount())
	}
}

func TestGrayConfigGet(t *testing.T) {
	svc, _, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	_, _ = svc.UpdateConfig("t1", "k", json.RawMessage(`"b"`), "u", nil)
	if svc.GetGrayConfig("t1", "k") != nil {
		t.Error("no gray yet")
	}

	rules := models.GrayRules{UserIDs: []string{"u1"}}
	_ = svc.SetGrayConfig("t1", "k", json.RawMessage(`"g"`), rules, "admin")
	gc := svc.GetGrayConfig("t1", "k")
	if gc == nil {
		t.Fatal("gray config should exist")
	}
	if string(gc.Value) != `"g"` {
		t.Errorf("gray value got %s", gc.Value)
	}
}

func TestStorageCheckAndDump(t *testing.T) {
	svc, _, store, dir := newTestService(t)
	defer cleanSvc(store, dir)

	if err := svc.CheckStorage(); err != nil {
		t.Errorf("check storage: %v", err)
	}
	if err := svc.Dump(); err != nil {
		t.Errorf("dump: %v", err)
	}
}
