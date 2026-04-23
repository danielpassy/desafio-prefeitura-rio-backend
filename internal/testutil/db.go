package testutil

import (
	"context"
	"fmt"
	"os"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewTestDB connects to Postgres and runs migrations.
// Returns an error if Postgres is unreachable (caller should skip) or if
// migrations fail (caller should fatal).
func NewTestDB(migrationsDir string) (*pgxpool.Pool, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://app:app@localhost:5432/notifications"
	}
	pool, err := storage.NewPool(context.Background(), dsn)
	if err != nil {
		return nil, err
	}
	if err := storage.RunMigrations(context.Background(), pool, migrationsDir); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrations: %w", err)
	}
	return pool, nil
}
