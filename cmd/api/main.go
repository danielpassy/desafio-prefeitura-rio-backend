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

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/api"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/auth"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/broadcast"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/circuitbreaker"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/config"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/dlq"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/telemetry"
	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
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

	shutdownTracer, err := telemetry.Setup(ctx, "notifications-api", cfg.OTELEndpoint)
	if err != nil {
		slog.Error("otel setup failed", "error", err)
		os.Exit(1)
	}
	defer shutdownTracer(context.Background()) //nolint:errcheck

	pool, err := storage.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer rdb.Close()
	if err := redisotel.InstrumentTracing(rdb); err != nil {
		slog.Error("redis otel hook failed", "error", err)
		os.Exit(1)
	}
	rdb.AddHook(circuitbreaker.NewRedisHook(circuitbreaker.NewRedisBreaker()))
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("redis connection failed", "error", err)
		os.Exit(1)
	}

	kf, err := auth.NewJWKSKeyfunc(ctx, cfg.JWTJWKSURL)
	if err != nil {
		slog.Error("jwks init failed", "error", err)
		os.Exit(1)
	}

	repo := storage.NewNotificationRepo(circuitbreaker.WrapQuerier(pool, circuitbreaker.NewPostgresBreaker()))
	pub := broadcast.NewRedisPublisher(rdb)
	sub := broadcast.NewRedisSubscriber(rdb)

	dlqQueue := dlq.NewQueue(rdb)
	dlqWorker := dlq.NewWorker(dlqQueue, repo, pub)
	go dlqWorker.Run(ctx)

	r := api.NewRouter(api.RouterParams{
		Keyfunc:       kf,
		Notifications: repo,
		Publisher:     pub,
		DLQ:           dlqQueue,
		Subscriber:    sub,
		WebhookSecret: cfg.WebhookSecret,
		CPFKey:        cfg.CPFKey,
	})

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
