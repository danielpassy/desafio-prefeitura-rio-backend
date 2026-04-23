package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/config"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/webhook"
	"github.com/gin-gonic/gin"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	pool, err := storage.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	repo := storage.NewNotificationRepo(pool)
	wh := webhook.NewHandler(repo, webhook.NoOpPublisher{}, cfg.WebhookSecret, cfg.CPFKey)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.POST("/webhook", wh.Handle)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: r,
	}

	go func() {
		slog.Info("server listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
	}
}
