package auth

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"configsync/internal/models"
)

type Middleware struct {
	adminToken string
}

func NewMiddleware() *Middleware {
	return &Middleware{
		adminToken: os.Getenv("ADMIN_TOKEN"),
	}
}

func (m *Middleware) AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := c.GetHeader("X-Tenant-Id")
		if tenantID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "X-Tenant-Id header required"})
			c.Abort()
			return
		}

		userID := c.GetHeader("X-User-Id")
		if userID == "" {
			userID = "anonymous"
		}

		role := models.Role(c.GetHeader("X-User-Role"))
		if role == "" {
			role = models.RoleViewer
		}

		if role == models.RoleAdmin {
			adminToken := c.GetHeader("X-Admin-Token")
			if m.adminToken != "" && adminToken != m.adminToken {
				c.JSON(http.StatusForbidden, gin.H{"error": "invalid admin token"})
				c.Abort()
				return
			}
		}

		c.Set(string(models.TenantIDKey), tenantID)
		c.Set(string(models.UserIDKey), userID)
		c.Set(string(models.UserRoleKey), role)

		c.Next()
	}
}

func (m *Middleware) RequireRole(roles ...models.Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole, exists := c.Get(string(models.UserRoleKey))
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
			c.Abort()
			return
		}

		role := userRole.(models.Role)
		allowed := false
		for _, r := range roles {
			if r == role {
				allowed = true
				break
			}
		}

		if !allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			c.Abort()
			return
		}

		c.Next()
	}
}

func GetTenantID(c *gin.Context) string {
	v, _ := c.Get(string(models.TenantIDKey))
	return v.(string)
}

func GetUserID(c *gin.Context) string {
	v, _ := c.Get(string(models.UserIDKey))
	return v.(string)
}

func GetUserRole(c *gin.Context) models.Role {
	v, _ := c.Get(string(models.UserRoleKey))
	return v.(models.Role)
}

func GetIfMatch(c *gin.Context) *int64 {
	return ParseIfMatch(c.GetHeader("If-Match"))
}

func ParseIfMatch(header string) *int64 {
	if header == "" {
		return nil
	}

	parts := strings.SplitN(header, "=", 2)
	if len(parts) == 2 && strings.TrimSpace(parts[0]) == "version" {
		v, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err == nil {
			return &v
		}
	}

	return nil
}
