package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"configsync/internal/auth"
	"configsync/internal/config"
	"configsync/internal/models"
)

type AdminHandler struct {
	service *config.Service
}

func NewAdminHandler(service *config.Service) *AdminHandler {
	return &AdminHandler{service: service}
}

func (h *AdminHandler) Export(c *gin.Context) {
	tenantID := auth.GetTenantID(c)

	data, err := h.service.Export(tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", "attachment; filename=config-export.json")
	c.JSON(http.StatusOK, data)
}

func (h *AdminHandler) ExportAll(c *gin.Context) {
	data, err := h.service.ExportAll()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", "attachment; filename=config-export-all.json")
	c.JSON(http.StatusOK, data)
}

type ImportResponse struct {
	Imported int    `json:"imported"`
	Skipped  int    `json:"skipped"`
	Strategy string `json:"strategy"`
}

func (h *AdminHandler) Import(c *gin.Context) {
	tenantID := auth.GetTenantID(c)
	userID := auth.GetUserID(c)

	var req models.ImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	strategy := req.ConflictStrategy
	if strategy == "" {
		strategy = models.ConflictStrategySkip
	}
	if strategy != models.ConflictStrategySkip && strategy != models.ConflictStrategyOverwrite {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid conflict strategy"})
		return
	}

	imported, skipped, err := h.service.Import(tenantID, req.Configs, strategy, userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, ImportResponse{
		Imported: imported,
		Skipped:  skipped,
		Strategy: strategy,
	})
}
