package circuitbreaker

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker"
)

func NewRedisBreaker() *gobreaker.CircuitBreaker {
	return gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "redis",
		MaxRequests: 3,
		Interval:    10 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 5
		},
	})
}

type RedisHook struct {
	cb *gobreaker.CircuitBreaker
}

func NewRedisHook(cb *gobreaker.CircuitBreaker) *RedisHook {
	return &RedisHook{cb: cb}
}

func (h *RedisHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return next(ctx, network, addr)
	}
}

// redis.Nil é sinal de "nenhum item disponível" (ex: GET em chave inexistente,
// ZPOPMIN em ZSET vazio) - não é falha do Redis e não deve trippar o breaker.
func (h *RedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		_, err := h.cb.Execute(func() (interface{}, error) {
			err := next(ctx, cmd)
			if errors.Is(err, redis.Nil) {
				return nil, nil
			}
			return nil, err
		})
		return err
	}
}

func (h *RedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		_, err := h.cb.Execute(func() (interface{}, error) {
			err := next(ctx, cmds)
			if errors.Is(err, redis.Nil) {
				return nil, nil
			}
			return nil, err
		})
		return err
	}
}
