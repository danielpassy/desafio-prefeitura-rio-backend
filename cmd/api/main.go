package main

import (
	"log/slog"
	"os"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	slog.Info("starting server", "port", cfg.Port)
}
