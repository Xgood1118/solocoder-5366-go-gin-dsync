package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"configsync/internal/auth"
	"configsync/internal/config"
)

type SubscriptionHandler struct {
	service *config.Service
}

func NewSubscriptionHandler(service *config.Service) *SubscriptionHandler {
	return &SubscriptionHandler{service: service}
}

type CreateSubscriptionRequest struct {
	SubscriberID string `json:"subscriber_id" binding:"required"`
	KeyPattern   string `json:"key_pattern" binding:"required"`
	CallbackURL  string `json:"callback_url" binding:"required,url"`
}

func (h *SubscriptionHandler) Create(c *gin.Context) {
	tenantID := auth.GetTenantID(c)

	var req CreateSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	sub := h.service.CreateSubscription(tenantID, req.SubscriberID, req.KeyPattern, req.CallbackURL)
	c.JSON(http.StatusCreated, sub)
}

func (h *SubscriptionHandler) Delete(c *gin.Context) {
	tenantID := auth.GetTenantID(c)
	id := c.Param("id")

	if !h.service.DeleteSubscription(tenantID, id) {
		c.JSON(http.StatusNotFound, gin.H{"error": "subscription not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *SubscriptionHandler) Get(c *gin.Context) {
	tenantID := auth.GetTenantID(c)
	id := c.Param("id")

	sub := h.service.GetSubscription(tenantID, id)
	if sub == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "subscription not found"})
		return
	}

	c.JSON(http.StatusOK, sub)
}

func (h *SubscriptionHandler) List(c *gin.Context) {
	tenantID := auth.GetTenantID(c)

	subs := h.service.ListSubscriptions(tenantID)
	c.JSON(http.StatusOK, gin.H{"data": subs})
}

func (h *SubscriptionHandler) Recover(c *gin.Context) {
	tenantID := auth.GetTenantID(c)
	id := c.Param("id")

	if !h.service.RecoverSubscription(tenantID, id) {
		c.JSON(http.StatusNotFound, gin.H{"error": "subscription not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "recovered"})
}
