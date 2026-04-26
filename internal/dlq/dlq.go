package dlq

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
	"github.com/redis/go-redis/v9"
)

const (
	queueKey    = "dlq:webhooks"
	MaxAttempts = 5
)

type Entry struct {
	Event    storage.InsertParams `json:"event"`
	FailedAt time.Time            `json:"failed_at"`
	Attempts int                  `json:"attempts"`
}

type Queue struct {
	rdb *redis.Client
}

func NewQueue(rdb *redis.Client) *Queue {
	return &Queue{rdb: rdb}
}

func (q *Queue) Enqueue(ctx context.Context, e storage.InsertParams) error {
	return q.enqueueEntry(ctx, Entry{Event: e, FailedAt: time.Now(), Attempts: 1})
}

func (q *Queue) enqueueEntry(ctx context.Context, e Entry) error {
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("dlq marshal: %w", err)
	}
	return q.rdb.LPush(ctx, queueKey, data).Err()
}

// dequeue blocks up to timeout waiting for an entry. Returns nil with no error on timeout.
func (q *Queue) dequeue(ctx context.Context, timeout time.Duration) (*Entry, error) {
	result, err := q.rdb.BRPop(ctx, timeout, queueKey).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dlq brpop: %w", err)
	}
	var e Entry
	if err := json.Unmarshal([]byte(result[1]), &e); err != nil {
		return nil, fmt.Errorf("dlq unmarshal: %w", err)
	}
	return &e, nil
}
