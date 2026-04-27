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

// Queue armazena retries num ZSET com score = ready_at_unix_ms.
// Entries com score <= now estão elegíveis pra processamento; o resto
// fica "dormindo" no Redis sem bloquear o worker.
// Isso permite um backoff que não bloqueia e implementa um circuit breaker que lida tanto
// com falhas transitórias (ex: banco temporariamente indisponível) quanto com
// dados problemáticos (ex: payload que não passa na validação do banco).
type Queue struct {
	rdb *redis.Client
}

func NewQueue(rdb *redis.Client) *Queue {
	return &Queue{rdb: rdb}
}

// Enqueue insere um evento novo, disponível imediatamente.
func (q *Queue) Enqueue(ctx context.Context, e storage.InsertParams) error {
	return q.enqueueAt(ctx, Entry{Event: e, FailedAt: time.Now(), Attempts: 1}, time.Now())
}

// enqueueAt agenda uma entry pra ficar disponível em readyAt.
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

// popReadyScript remove atomicamente a entry mais antiga elegível (score <= ARGV[1]).
// Retorna o JSON cru ou false se não há nada pronto.
var popReadyScript = redis.NewScript(`
local entries = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1], 'LIMIT', 0, 1)
if #entries == 0 then return false end
redis.call('ZREM', KEYS[1], entries[1])
return entries[1]
`)

// dequeueReady remove e retorna uma entry pronta, ou nil se nenhuma está elegível agora.
func (q *Queue) dequeueReady(ctx context.Context) (*Entry, error) {
	now := time.Now().UnixMilli()
	res, err := popReadyScript.Run(ctx, q.rdb, []string{retryKey}, now).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dlq pop-ready: %w", err)
	}
	s, ok := res.(string)
	if !ok || s == "" {
		return nil, nil
	}
	var e Entry
	if err := json.Unmarshal([]byte(s), &e); err != nil {
		return nil, fmt.Errorf("dlq unmarshal: %w", err)
	}
	return &e, nil
}

// MoveToDead empurra a entry pra fila terminal. Não há processamento automático
// a partir daqui — requer inspeção/replay manual.
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
