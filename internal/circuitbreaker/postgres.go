package circuitbreaker

import (
	"context"
	"errors"
	"time"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sony/gobreaker"
)

func NewPostgresBreaker() *gobreaker.CircuitBreaker {
	return gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "postgres",
		MaxRequests: 3,
		Interval:    10 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 5
		},
	})
}

type cbQuerier struct {
	inner storage.Querier
	cb    *gobreaker.CircuitBreaker
}

func WrapQuerier(inner storage.Querier, cb *gobreaker.CircuitBreaker) storage.Querier {
	return &cbQuerier{inner: inner, cb: cb}
}

func (q *cbQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	out, err := q.cb.Execute(func() (interface{}, error) {
		return q.inner.Exec(ctx, sql, args...)
	})
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	return out.(pgconn.CommandTag), nil
}

func (q *cbQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	out, err := q.cb.Execute(func() (interface{}, error) {
		return q.inner.Query(ctx, sql, args...)
	})
	if err != nil {
		return nil, err
	}
	return out.(pgx.Rows), nil
}

func (q *cbQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if q.cb.State() == gobreaker.StateOpen {
		return &errRow{err: gobreaker.ErrOpenState}
	}
	return &cbRow{inner: q.inner.QueryRow(ctx, sql, args...), cb: q.cb}
}

// cbRow wraps pgx.Row so that Scan counts failures without tripping the CB on ErrNoRows.
type cbRow struct {
	inner pgx.Row
	cb    *gobreaker.CircuitBreaker
}

type scanResult struct{ err error }

func (r *cbRow) Scan(dest ...any) error {
	out, cbErr := r.cb.Execute(func() (interface{}, error) {
		err := r.inner.Scan(dest...)
		if errors.Is(err, pgx.ErrNoRows) {
			return scanResult{err: err}, nil // expected absence, not an infra failure
		}
		return scanResult{err: err}, err
	})
	if cbErr != nil {
		return cbErr
	}
	return out.(scanResult).err
}

type errRow struct{ err error }

func (r *errRow) Scan(_ ...any) error { return r.err }
