package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"configsync/internal/models"
)

func newTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	mw := NewMiddleware()
	r := gin.New()
	r.Use(mw.AuthRequired())
	r.GET("/api/configs/:key", mw.RequireRole(models.RoleViewer, models.RoleEditor, models.RoleAdmin), func(c *gin.Context) {
		c.JSON(200, gin.H{"tenant": GetTenantID(c), "user": GetUserID(c), "role": GetUserRole(c)})
	})
	r.PUT("/api/configs/:key", mw.RequireRole(models.RoleEditor, models.RoleAdmin), func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})
	r.POST("/api/admin/import", mw.RequireRole(models.RoleAdmin), func(c *gin.Context) {
		c.JSON(200, gin.H{"admin": true})
	})
	return r
}

func TestRoleConstants(t *testing.T) {
	if string(models.RoleViewer) == "" || string(models.RoleEditor) == "" || string(models.RoleAdmin) == "" {
		t.Fatal("role constants should not be empty")
	}
}

func TestContextKeys(t *testing.T) {
	if string(models.TenantIDKey) != "tenant_id" {
		t.Errorf("TenantIDKey unexpected: %q", models.TenantIDKey)
	}
	if string(models.UserIDKey) != "user_id" {
		t.Errorf("UserIDKey unexpected: %q", models.UserIDKey)
	}
}

func TestAuthRequiredMissingTenant(t *testing.T) {
	r := newTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/configs/k1", nil)
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("expected 401 without X-Tenant-Id, got %d", w.Code)
	}
}

func TestAuthRequiredDefaultRoleViewer(t *testing.T) {
	r := newTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/configs/k1", nil)
	req.Header.Set("X-Tenant-Id", "t1")
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200 with viewer, got %d: %s", w.Code, w.Body.String())
	}
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	if out["tenant"] != "t1" {
		t.Errorf("tenant_id injected wrong: %v", out["tenant"])
	}
	if out["user"] != "anonymous" {
		t.Errorf("default user should be anonymous, got %v", out["user"])
	}
	if out["role"] != string(models.RoleViewer) {
		t.Errorf("default role should be viewer, got %v", out["role"])
	}
}

func TestAuthRoleCustomUser(t *testing.T) {
	r := newTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/configs/k1", nil)
	req.Header.Set("X-Tenant-Id", "t9")
	req.Header.Set("X-User-Id", "bob")
	req.Header.Set("X-User-Role", "editor")
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200: %d %s", w.Code, w.Body.String())
	}
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	if out["user"] != "bob" {
		t.Errorf("user wrong: %v", out["user"])
	}
	if out["role"] != "editor" {
		t.Errorf("role wrong: %v", out["role"])
	}
}

func TestRequireRoleDeniedForViewerOnEditorRoute(t *testing.T) {
	r := newTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/configs/k1", nil)
	req.Header.Set("X-Tenant-Id", "t1")
	r.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Errorf("viewer on editor route expected 403, got %d", w.Code)
	}
}

func TestRequireRoleAllowedForEditor(t *testing.T) {
	r := newTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/configs/k1", nil)
	req.Header.Set("X-Tenant-Id", "t1")
	req.Header.Set("X-User-Role", "editor")
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("editor expected 200, got %d", w.Code)
	}
}

func TestAdminRoleRequiresToken(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "secret-123")
	r := newTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/admin/import", nil)
	req.Header.Set("X-Tenant-Id", "t1")
	req.Header.Set("X-User-Role", "admin")
	r.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Errorf("admin without X-Admin-Token expected 403, got %d", w.Code)
	}
}

func TestAdminRoleWithToken(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "secret-123")
	r := newTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/admin/import", nil)
	req.Header.Set("X-Tenant-Id", "t1")
	req.Header.Set("X-User-Role", "admin")
	req.Header.Set("X-Admin-Token", "secret-123")
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("admin with token expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminRoleWrongToken(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "secret-123")
	r := newTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/admin/import", nil)
	req.Header.Set("X-Tenant-Id", "t1")
	req.Header.Set("X-User-Role", "admin")
	req.Header.Set("X-Admin-Token", "wrong")
	r.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Errorf("admin with wrong token expected 403, got %d", w.Code)
	}
}

func TestAdminNoEnvTokenSkipsCheck(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "")
	r := newTestRouter()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/admin/import", nil)
	req.Header.Set("X-Tenant-Id", "t1")
	req.Header.Set("X-User-Role", "admin")
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("no env admin token: admin without token expected 200 (skip check), got %d", w.Code)
	}
}

func TestGetIfMatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/t", func(c *gin.Context) {
		v := GetIfMatch(c)
		if v == nil {
			c.JSON(200, gin.H{"ok": false})
			return
		}
		c.JSON(200, gin.H{"ok": true, "value": *v})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/t", nil)
	r.ServeHTTP(w, req)
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	if out["ok"] != false {
		t.Errorf("no If-Match header should be ok=false, got %v", out["ok"])
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/t", nil)
	req.Header.Set("If-Match", "version=42")
	r.ServeHTTP(w, req)
	json.Unmarshal(w.Body.Bytes(), &out)
	if out["ok"] != true {
		t.Errorf("If-Match=version=42 should be ok=true, got %v", out["ok"])
	}
	val := int64(out["value"].(float64))
	if val != 42 {
		t.Errorf("expected 42, got %d", val)
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/t", nil)
	req.Header.Set("If-Match", "garbage")
	r.ServeHTTP(w, req)
	json.Unmarshal(w.Body.Bytes(), &out)
	if out["ok"] != false {
		t.Errorf("bad If-Match should be ok=false, got %v", out["ok"])
	}
}

func TestParseIfMatchEdgeCases(t *testing.T) {
	cases := []struct {
		h    string
		want *int64
	}{
		{"", nil},
		{"version=0", ptr(0)},
		{"version=999", ptr(999)},
		{"version=", nil},
		{"VERSION=5", nil},
		{"version=-1", ptr(-1)},
	}
	for i, c := range cases {
		got := ParseIfMatch(c.h)
		if c.want == nil {
			if got != nil {
				t.Errorf("case %d: want nil, got %d", i, *got)
			}
			continue
		}
		if got == nil || *got != *c.want {
			g := int64(-1)
			if got != nil {
				g = *got
			}
			t.Errorf("case %d: want %d, got %d", i, *c.want, g)
		}
	}
}

func ptr(n int64) *int64 { return &n }
