package notifier

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"configsync/internal/models"
	"configsync/internal/storage"
)

func newTestStore(t *testing.T) *storage.Store {
	dir := t.TempDir()
	s, err := storage.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	_ = os.RemoveAll(filepath.Join(dir, "audit.jsonl"))
	return s
}

func TestLocalNotifierLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := newTestStore(t)
	n := NewLocalNotifier(store, 2)
	n.Start(ctx)

	if n.PendingCount() != 0 {
		t.Error("initial pending should be 0")
	}

	p := &models.NotificationPayload{
		KeyPath: "k", Value: json.RawMessage("null"), Version: 1,
		UpdatedAt: time.Now().Format(time.RFC3339), TenantID: "t1", Event: "updated",
	}
	sub := &models.Subscription{
		ID:            "sub1",
		SubscriberID:  "svc1",
		KeyPattern:    "k",
		CallbackURL:   "http://127.0.0.1:1/cb",
		TenantID:      "t1",
		Active:        true,
		Status:        models.SubscriptionStatusHealthy,
	}
	n.Notify(p, sub)

	if ok := n.Wait(5 * time.Second); !ok {
		t.Log("wait timed out (expected, callback is unreachable)")
	}

	cancel()
	if err := n.Stop(); err != nil {
		t.Errorf("stop failed: %v", err)
	}
}

func TestLocalNotifierPendingCount(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := newTestStore(t)
	n := NewLocalNotifier(store, 1)
	n.Start(ctx)

	if n.PendingCount() < 0 {
		t.Error("pending should be >= 0")
	}
	if err := n.Stop(); err != nil {
		t.Errorf("stop: %v", err)
	}
}

func TestRemoteNotifierStubs(t *testing.T) {
	rn := &RemoteNotifier{}
	rn.Start(context.Background())
	rn.Notify(nil, nil)
	if rn.PendingCount() != 0 {
		t.Error("pending should be 0")
	}
	if !rn.Wait(0) {
		t.Error("wait should return true")
	}
	if err := rn.Stop(); err != nil {
		t.Errorf("stop: %v", err)
	}
}

func TestRetryDelays(t *testing.T) {
	store := newTestStore(t)
	n := NewLocalNotifier(store, 2)
	if len(n.retryDelays) != 3 {
		t.Fatalf("expected 3 retry delays, got %d", len(n.retryDelays))
	}
	if n.retryDelays[0] != 1*time.Second {
		t.Errorf("retryDelays[0] should be 1s, got %v", n.retryDelays[0])
	}
	if n.retryDelays[1] != 5*time.Second {
		t.Errorf("retryDelays[1] should be 5s, got %v", n.retryDelays[1])
	}
	if n.retryDelays[2] != 30*time.Second {
		t.Errorf("retryDelays[2] should be 30s, got %v", n.retryDelays[2])
	}
}
