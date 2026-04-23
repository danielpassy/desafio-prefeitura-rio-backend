package broadcast

import (
	"context"
	"encoding/json"

	"github.com/redis/go-redis/v9"
)

type RedisSubscriber struct {
	rdb *redis.Client
}

func NewRedisSubscriber(rdb *redis.Client) *RedisSubscriber {
	return &RedisSubscriber{rdb: rdb}
}

// Subscribe returns a channel that delivers messages published for citizenRef.
// The channel is closed when ctx is cancelled or the subscription is dropped.
func (s *RedisSubscriber) Subscribe(ctx context.Context, citizenRef []byte) <-chan Message {
	ch := make(chan Message, 16)
	sub := s.rdb.Subscribe(ctx, channelFor(citizenRef))

	go func() {
		defer sub.Close()
		defer close(ch)

		redisCh := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-redisCh:
				if !ok {
					return
				}
				var m Message
				if err := json.Unmarshal([]byte(msg.Payload), &m); err != nil {
					continue
				}
				select {
				case ch <- m:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return ch
}
