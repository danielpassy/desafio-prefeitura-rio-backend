package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/config"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := storage.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := storage.RunMigrations(ctx, pool, "migrations"); err != nil {
		slog.Error("migrations failed", "error", err)
		os.Exit(1)
	}

	slog.Info("migrations applied successfully")
}
