package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"configsync/internal/auth"
	"configsync/internal/config"
	"configsync/internal/models"
)

type ConfigHandler struct {
	service *config.Service
}

func NewConfigHandler(service *config.Service) *ConfigHandler {
	return &ConfigHandler{service: service}
}

type UpdateConfigRequest struct {
	Value    json.RawMessage  `json:"value" binding:"required"`
	Metadata *models.Metadata `json:"metadata,omitempty"`
}

func (h *ConfigHandler) UpdateConfig(c *gin.Context) {
	tenantID := auth.GetTenantID(c)
	userID := auth.GetUserID(c)
	keyPath := c.Param("key_path")
	ifMatch := auth.GetIfMatch(c)

	var req UpdateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.service.UpdateConfig(tenantID, keyPath, req.Value, userID, ifMatch)
	if err != nil {
		if result != nil && result.Conflict {
			c.JSON(http.StatusPreconditionFailed, gin.H{
				"error":           err.Error(),
				"current_version": result.CurrentVersion,
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Metadata != nil {
		_ = h.service.UpdateMetadata(tenantID, keyPath, req.Metadata)
	}

	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	c.JSON(status, result.Config)
}

func (h *ConfigHandler) GetConfig(c *gin.Context) {
	tenantID := auth.GetTenantID(c)
	keyPath := c.Param("key_path")
	grayUserID := c.GetHeader("X-Gray-User-Id")

	longPoll := c.Query("long_poll") == "true"
	timeoutStr := c.DefaultQuery("timeout", "30")
	versionStr := c.Query("version")

	var version *int64
	if versionStr != "" {
		v, err := strconv.ParseInt(versionStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid version"})
			return
		}
		version = &v
	}

	if longPoll {
		timeout, err := strconv.Atoi(timeoutStr)
		if err != nil || timeout <= 0 || timeout > 60 {
			timeout = 30
		}
		cfg := h.service.LongPoll(tenantID, keyPath, time.Duration(timeout)*time.Second)
		if cfg == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "config not found"})
			return
		}
		h.writeConfigResponse(c, cfg)
		return
	}

	cfg := h.service.GetConfig(tenantID, keyPath, version, grayUserID)
	if cfg == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "config not found"})
		return
	}

	h.writeConfigResponse(c, cfg)
}

func (h *ConfigHandler) writeConfigResponse(c *gin.Context, cfg *models.ConfigKey) {
	if cfg.Metadata != nil && cfg.Metadata.IsDeprecated() {
		c.Header("Deprecation", "@"+cfg.Metadata.DeprecatedAt.Format(time.RFC3339))
		if cfg.Metadata.DeprecatedAt.AddDate(0, 6, 0).After(time.Now()) {
			c.Header("Sunset", cfg.Metadata.DeprecatedAt.AddDate(0, 6, 0).Format(time.RFC3339))
		} else {
			c.Header("Sunset", cfg.Metadata.DeprecatedAt.Format(time.RFC3339))
		}
		if cfg.Metadata.Replacement != "" {
			c.Header("Link", "<"+cfg.Metadata.Replacement+">; rel=\"alternate\"")
		}
	}

	c.Header("ETag", "version="+strconv.FormatInt(cfg.Version, 10))
	c.JSON(http.StatusOK, cfg)
}

func (h *ConfigHandler) ListConfigs(c *gin.Context) {
	tenantID := auth.GetTenantID(c)
	prefix := c.Query("prefix")

	configs := h.service.ListConfigs(tenantID, prefix)
	c.JSON(http.StatusOK, gin.H{"data": configs})
}

func (h *ConfigHandler) GetHistory(c *gin.Context) {
	tenantID := auth.GetTenantID(c)
	keyPath := c.Param("key_path")

	history := h.service.GetHistory(tenantID, keyPath)
	if history == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "config not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": history})
}

type RollbackRequest struct {
	ToVersion int64 `json:"to" binding:"required"`
}

func (h *ConfigHandler) Rollback(c *gin.Context) {
	tenantID := auth.GetTenantID(c)
	userID := auth.GetUserID(c)
	keyPath := c.Param("key_path")

	var req RollbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cfg, err := h.service.Rollback(tenantID, keyPath, req.ToVersion, userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, cfg)
}

type ResolveRequest struct {
	CandidateVersion1 int64  `json:"candidate_version_1" binding:"required"`
	CandidateVersion2 int64  `json:"candidate_version_2" binding:"required"`
	Resolution        string `json:"resolution" binding:"required,oneof=pick_old pick_new"`
}

func (h *ConfigHandler) Resolve(c *gin.Context) {
	tenantID := auth.GetTenantID(c)
	userID := auth.GetUserID(c)
	keyPath := c.Param("key_path")

	var req ResolveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cfg, err := h.service.Resolve(tenantID, keyPath, req.CandidateVersion1, req.CandidateVersion2, req.Resolution, userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, cfg)
}

type GrayConfigRequest struct {
	Value     json.RawMessage    `json:"value" binding:"required"`
	GrayRules models.GrayRules   `json:"gray_rules" binding:"required"`
}

func (h *ConfigHandler) CreateGrayConfig(c *gin.Context) {
	tenantID := auth.GetTenantID(c)
	userID := auth.GetUserID(c)
	keyPath := c.Param("key_path")

	var req GrayConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.service.SetGrayConfig(tenantID, keyPath, req.Value, req.GrayRules, userID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *ConfigHandler) GetGrayConfig(c *gin.Context) {
	tenantID := auth.GetTenantID(c)
	keyPath := c.Param("key_path")

	grayCfg := h.service.GetGrayConfig(tenantID, keyPath)
	if grayCfg == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "gray config not found"})
		return
	}

	c.JSON(http.StatusOK, grayCfg)
}

func (h *ConfigHandler) PromoteGray(c *gin.Context) {
	tenantID := auth.GetTenantID(c)
	userID := auth.GetUserID(c)
	keyPath := c.Param("key_path")

	cfg, err := h.service.PromoteGray(tenantID, keyPath, userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, cfg)
}

func (h *ConfigHandler) Preview(c *gin.Context) {
	tenantID := auth.GetTenantID(c)

	var req models.PreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.service.Preview(tenantID, req.Changes)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

func (h *ConfigHandler) BatchUpdate(c *gin.Context) {
	tenantID := auth.GetTenantID(c)
	userID := auth.GetUserID(c)

	var req models.BatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.Changes) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no changes provided"})
		return
	}

	results, err := h.service.BatchUpdate(tenantID, req.Changes, userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": results})
}

func (h *ConfigHandler) GetAudit(c *gin.Context) {
	tenantID := auth.GetTenantID(c)
	keyPath := c.Query("key_path")

	logs, err := h.service.GetAuditLogs(tenantID, keyPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": logs})
}

func (h *ConfigHandler) UpdateMetadata(c *gin.Context) {
	tenantID := auth.GetTenantID(c)
	keyPath := c.Param("key_path")

	var req models.Metadata
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.service.UpdateMetadata(tenantID, keyPath, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
