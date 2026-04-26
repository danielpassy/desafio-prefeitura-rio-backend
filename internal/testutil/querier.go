package testutil

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrQuerier is a storage.Querier that always returns an error.
// Use it to simulate DB failures without a real database connection.
type ErrQuerier struct{}

func (ErrQuerier) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("injected db error")
}
func (ErrQuerier) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, errors.New("injected db error")
}
func (ErrQuerier) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return errRow{}
}

type errRow struct{}

func (errRow) Scan(_ ...any) error { return errors.New("injected db error") }
