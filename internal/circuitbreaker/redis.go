package circuitbreaker

import (
	"context"
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

func (h *RedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		_, err := h.cb.Execute(func() (interface{}, error) {
			return nil, next(ctx, cmd)
		})
		return err
	}
}

func (h *RedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		_, err := h.cb.Execute(func() (interface{}, error) {
			return nil, next(ctx, cmds)
		})
		return err
	}
}
