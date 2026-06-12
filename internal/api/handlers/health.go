package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"configsync/internal/config"
)

type HealthHandler struct {
	service     *config.Service
	version     string
	startTime   time.Time
}

func NewHealthHandler(service *config.Service, version string) *HealthHandler {
	return &HealthHandler{
		service:   service,
		version:   version,
		startTime: time.Now(),
	}
}

type HealthResponse struct {
	Version          string `json:"version"`
	StartTime        string `json:"start_time"`
	UptimeSeconds    int64  `json:"uptime_seconds"`
	ConfigCount      int    `json:"config_count"`
	SubscriptionCount int   `json:"subscription_count"`
	Status           string `json:"status"`
}

func (h *HealthHandler) Healthz(c *gin.Context) {
	uptime := time.Since(h.startTime).Seconds()

	c.JSON(http.StatusOK, HealthResponse{
		Version:          h.version,
		StartTime:        h.startTime.Format(time.RFC3339),
		UptimeSeconds:    int64(uptime),
		ConfigCount:      h.service.GetConfigCount(),
		SubscriptionCount: h.service.GetSubscriptionCount(),
		Status:           "ok",
	})
}

type ReadyResponse struct {
	Status       string `json:"status"`
	StorageReady bool   `json:"storage_ready"`
	StorageError string `json:"storage_error,omitempty"`
}

func (h *HealthHandler) Readyz(c *gin.Context) {
	resp := ReadyResponse{
		Status:       "ok",
		StorageReady: true,
	}

	if err := h.service.CheckStorage(); err != nil {
		resp.Status = "not_ready"
		resp.StorageReady = false
		resp.StorageError = err.Error()
		c.JSON(http.StatusServiceUnavailable, resp)
		return
	}

	c.JSON(http.StatusOK, resp)
}
