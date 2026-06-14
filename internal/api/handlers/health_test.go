package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"configsync/internal/config"
	"configsync/internal/notifier"
	"configsync/internal/storage"
)

func setupTestService(t *testing.T) *config.Service {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Load(); err != nil {
		t.Fatalf("store load: %v", err)
	}
	n := notifier.NewLocalNotifier(store, 2)
	svc := config.NewService(store, n)
	t.Cleanup(func() {
		if err := svc.Close(); err != nil {
			t.Logf("close err: %v", err)
		}
		_ = os.RemoveAll(filepath.Join(dir, "audit.jsonl"))
	})
	return svc
}

func TestHealthHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = os.Setenv("GIN_MODE", "test")
	svc := setupTestService(t)

	_, err := svc.UpdateConfig("t1", "k1", json.RawMessage(`"v1"`), "u1", nil)
	if err != nil {
		t.Fatalf("update err: %v", err)
	}

	h := NewHealthHandler(svc, "9.9.9-test")
	r := gin.New()
	r.GET("/healthz", h.Healthz)
	r.GET("/readyz", h.Readyz)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/healthz", nil)
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("healthz expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["version"] != "9.9.9-test" {
		t.Errorf("version mismatch: %v", out["version"])
	}
	cfgCount := int(out["config_count"].(float64))
	if cfgCount < 1 {
		t.Errorf("expected at least 1 config, got %d", cfgCount)
	}
	if out["status"] != "ok" {
		t.Errorf("status should be ok")
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/readyz", nil)
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("readyz expected 200, got %d: %s", w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out["storage_ready"] != true {
		t.Error("storage should be ready")
	}
}
