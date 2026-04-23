package broadcast

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
	"github.com/redis/go-redis/v9"
)

const channelPrefix = "notifications:"

// Message is the payload published to Redis and forwarded to WebSocket clients.
type Message struct {
	ID             string    `json:"id"`
	TicketID       string    `json:"ticket_id"`
	Type           string    `json:"type"`
	PreviousStatus string    `json:"previous_status"`
	NewStatus      string    `json:"new_status"`
	Title          string    `json:"title"`
	Description    *string   `json:"description,omitempty"`
	EventTimestamp time.Time `json:"event_timestamp"`
	ReceivedAt     time.Time `json:"received_at"`
}

// channelFor builds the Redis channel name for a citizen.
// hex encoding ensures arbitrary bytes are safe as channel names;
// the prefix namespaces these channels from other systems on the same Redis.
func channelFor(citizenRef []byte) string {
	return channelPrefix + hex.EncodeToString(citizenRef)
}

func toMessage(n *storage.Notification) Message {
	return Message{
		ID:             n.ID.String(),
		TicketID:       n.TicketID,
		Type:           n.Type,
		PreviousStatus: n.PreviousStatus,
		NewStatus:      n.NewStatus,
		Title:          n.Title,
		Description:    n.Description,
		EventTimestamp: n.EventTimestamp,
		ReceivedAt:     n.ReceivedAt,
	}
}

type RedisPublisher struct {
	rdb *redis.Client
}

func NewRedisPublisher(rdb *redis.Client) *RedisPublisher {
	return &RedisPublisher{rdb: rdb}
}

func (p *RedisPublisher) Publish(ctx context.Context, n *storage.Notification) error {
	data, err := json.Marshal(toMessage(n))
	if err != nil {
		return err
	}
	return p.rdb.Publish(ctx, channelFor(n.CitizenRef), data).Err()
}
