package dlq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
	"github.com/redis/go-redis/v9"
)

const (
	retryKey    = "dlq:webhooks:retry"
	deadKey     = "dlq:webhooks:dead"
	MaxAttempts = 5
)

type Entry struct {
	Event    storage.InsertParams `json:"event"`
	FailedAt time.Time            `json:"failed_at"`
	Attempts int                  `json:"attempts"`
}

// Queue stores retries in a ZSET with score = ready_at_unix_ms.
// Entries with score <= now are eligible for processing; the rest
// sleep in Redis without blocking the worker.
// This allows non-blocking backoff and acts as a circuit breaker that handles both
// transient failures (e.g. temporarily unavailable database) and
// bad data (e.g. payload that fails database validation).
type Queue struct {
	rdb *redis.Client
}

func NewQueue(rdb *redis.Client) *Queue {
	return &Queue{rdb: rdb}
}

// Enqueue inserts a new event, available immediately.
func (q *Queue) Enqueue(ctx context.Context, e storage.InsertParams) error {
	return q.enqueueAt(ctx, Entry{Event: e, FailedAt: time.Now(), Attempts: 1}, time.Now())
}

// enqueueAt schedules an entry to become available at readyAt.
func (q *Queue) enqueueAt(ctx context.Context, e Entry, readyAt time.Time) error {
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("dlq marshal: %w", err)
	}
	return q.rdb.ZAdd(ctx, retryKey, redis.Z{
		Score:  float64(readyAt.UnixMilli()),
		Member: data,
	}).Err()
}

// popReadyScript atomically removes the oldest eligible entry (score <= ARGV[1]).
// Returns the raw JSON or false if nothing is ready.
var popReadyScript = redis.NewScript(`
local entries = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1], 'LIMIT', 0, 1)
if #entries == 0 then return false end
redis.call('ZREM', KEYS[1], entries[1])
return entries[1]
`)

// dequeueReady removes and returns a ready entry, or nil if none is eligible right now.
func (q *Queue) dequeueReady(ctx context.Context) (*Entry, error) {
	now := time.Now().UnixMilli()
	res, err := popReadyScript.Run(ctx, q.rdb, []string{retryKey}, now).Result()
	// The circuit breaker hook (internal/circuitbreaker/redis.go) handles redis.Nil
	// only to avoid counting it as a breaker failure — the error is still propagated
	// by cmd. The check here is therefore necessary, not redundant.
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dlq pop-ready: %w", err)
	}
	var e Entry
	if err := json.Unmarshal([]byte(res.(string)), &e); err != nil {
		return nil, fmt.Errorf("dlq unmarshal: %w", err)
	}
	return &e, nil
}

// MoveToDead pushes the entry to the terminal queue. There is no automatic processing
// from here — requires manual inspection/replay.
func (q *Queue) MoveToDead(ctx context.Context, e Entry) error {
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("dlq marshal: %w", err)
	}
	if err := q.rdb.LPush(ctx, deadKey, data).Err(); err != nil {
		return fmt.Errorf("dlq lpush dead: %w", err)
	}
	return nil
}
