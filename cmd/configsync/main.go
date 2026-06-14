package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"configsync/internal/api/handlers"
	"configsync/internal/auth"
	"configsync/internal/config"
	"configsync/internal/models"
	"configsync/internal/notifier"
	"configsync/internal/storage"
)

const version = "1.0.0"

func main() {
	port := flag.Int("port", 9000, "server port")
	dataDir := flag.String("data-dir", "./data", "data directory")
	workerCount := flag.Int("worker-count", 16, "notifier worker count")
	flag.Parse()

	store, err := storage.NewStore(*dataDir)
	if err != nil {
		log.Fatalf("failed to init store: %v", err)
	}

	localNotifier := notifier.NewLocalNotifier(store, *workerCount)
	svc := config.NewService(store, localNotifier)

	authMiddleware := auth.NewMiddleware()

	configHandler := handlers.NewConfigHandler(svc)
	subHandler := handlers.NewSubscriptionHandler(svc)
	adminHandler := handlers.NewAdminHandler(svc)
	healthHandler := handlers.NewHealthHandler(svc, version)

	ctx, cancel := context.WithCancel(context.Background())
	localNotifier.Start(ctx)

	dumpTicker := time.NewTicker(30 * time.Second)
	go func() {
		for range dumpTicker.C {
			if err := svc.Dump(); err != nil {
				log.Printf("periodic dump failed: %v", err)
			}
		}
	}()

	r := gin.Default()

	r.GET("/healthz", healthHandler.Healthz)
	r.GET("/readyz", healthHandler.Readyz)

	api := r.Group("/api")
	api.Use(authMiddleware.AuthRequired())
	{
		viewer := api.Group("")
		viewer.Use(authMiddleware.RequireRole(models.RoleViewer, models.RoleEditor, models.RoleAdmin))
		{
			viewer.GET("/configs", configHandler.ListConfigs)
			viewer.GET("/configs/:key_path", configHandler.GetConfig)
			viewer.GET("/configs/:key_path/history", configHandler.GetHistory)
			viewer.GET("/configs/:key_path/gray", configHandler.GetGrayConfig)
			viewer.GET("/audit", configHandler.GetAudit)
		}

		editor := api.Group("")
		editor.Use(authMiddleware.RequireRole(models.RoleEditor, models.RoleAdmin))
		{
			editor.PUT("/configs/:key_path", configHandler.UpdateConfig)
			editor.POST("/configs/:key_path/rollback", configHandler.Rollback)
			editor.POST("/configs/:key_path/resolve", configHandler.Resolve)
			editor.POST("/configs/:key_path/gray", configHandler.CreateGrayConfig)
			editor.POST("/configs/:key_path/promote", configHandler.PromoteGray)
			editor.POST("/configs/:key_path/metadata", configHandler.UpdateMetadata)
			editor.POST("/configs/preview", configHandler.Preview)
			editor.POST("/configs/batch", configHandler.BatchUpdate)
		}

		admin := api.Group("")
		admin.Use(authMiddleware.RequireRole(models.RoleAdmin))
		{
			admin.POST("/subscriptions", subHandler.Create)
			admin.DELETE("/subscriptions/:id", subHandler.Delete)
			admin.GET("/subscriptions/:id", subHandler.Get)
			admin.GET("/subscriptions", subHandler.List)
			admin.POST("/subscriptions/:id/recover", subHandler.Recover)

			admin.GET("/admin/export", adminHandler.Export)
			admin.GET("/admin/export/all", adminHandler.ExportAll)
			admin.POST("/admin/import", adminHandler.Import)
		}
	}

	addr := fmt.Sprintf(":%d", *port)
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	go func() {
		log.Printf("configsync v%s starting on %s", version, addr)
		log.Printf("loaded %d configs, %d subscriptions", svc.GetConfigCount(), svc.GetSubscriptionCount())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutdown signal received")

	cancel()
	dumpTicker.Stop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}

	log.Println("waiting for pending notifications...")
	if ok := localNotifier.Wait(30 * time.Second); !ok {
		log.Println("warning: timed out waiting for notifications")
	}
	_ = localNotifier.Stop()

	log.Println("flushing data to disk...")
	if err := svc.Close(); err != nil {
		log.Printf("close store error: %v", err)
	}

	log.Println("shutdown complete")
}
