package testutil

import (
	"context"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
)

// NewTestRedis connects to Redis and skips the test if unavailable.
func NewTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	// DB 15 isola os testes do app em execução: sem isso, testes e app
	// disputam as mesmas chaves no Redis e contaminam o estado um do outro.
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("redis unavailable at %s: %v", addr, err)
	}
	if err := rdb.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("flushdb: %v", err)
	}
	t.Cleanup(func() { rdb.Close() })
	return rdb
}
