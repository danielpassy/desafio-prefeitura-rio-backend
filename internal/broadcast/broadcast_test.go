package broadcast_test

import (
	"context"
	"testing"
	"time"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/broadcast"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/testutil"
	"github.com/google/uuid"
)

func TestPublishSubscribe(t *testing.T) {
	rdb := testutil.NewTestRedis(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	citizenRef := []byte("test-citizen-broadcast")
	desc := "some description"
	n := &storage.Notification{
		ID:             uuid.New(),
		TicketID:       "CH-2024-001",
		Type:           "status_change",
		CitizenRef:     citizenRef,
		PreviousStatus: "open",
		NewStatus:      "in_progress",
		Title:          "Ticket updated",
		Description:    &desc,
		EventTimestamp: time.Now().UTC().Truncate(time.Millisecond),
		ReceivedAt:     time.Now().UTC().Truncate(time.Millisecond),
	}

	sub := broadcast.NewRedisSubscriber(rdb)
	ch := sub.Subscribe(ctx, citizenRef)

	// give the subscription time to be established before publishing
	time.Sleep(50 * time.Millisecond)

	pub := broadcast.NewRedisPublisher(rdb)
	if err := pub.Publish(ctx, n); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before message arrived")
		}
		if msg.ID != n.ID.String() {
			t.Errorf("id = %q, want %q", msg.ID, n.ID.String())
		}
		if msg.TicketID != n.TicketID {
			t.Errorf("ticket_id = %q, want %q", msg.TicketID, n.TicketID)
		}
		if msg.NewStatus != n.NewStatus {
			t.Errorf("new_status = %q, want %q", msg.NewStatus, n.NewStatus)
		}
		if msg.Description == nil || *msg.Description != desc {
			t.Errorf("description = %v, want %q", msg.Description, desc)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for message")
	}
}

func TestSubscribe_ContextCancel_ClosesChannel(t *testing.T) {
	rdb := testutil.NewTestRedis(t)
	ctx, cancel := context.WithCancel(context.Background())

	sub := broadcast.NewRedisSubscriber(rdb)
	ch := sub.Subscribe(ctx, []byte("some-citizen"))

	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed after context cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel not closed within 2s after cancel")
	}
}

func TestPublish_IsolatedByChannel(t *testing.T) {
	rdb := testutil.NewTestRedis(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	citizenA := []byte("citizen-a-isolation")
	citizenB := []byte("citizen-b-isolation")

	sub := broadcast.NewRedisSubscriber(rdb)
	chA := sub.Subscribe(ctx, citizenA)

	time.Sleep(50 * time.Millisecond)

	n := &storage.Notification{
		ID:             uuid.New(),
		TicketID:       "CH-999",
		Type:           "status_change",
		CitizenRef:     citizenB, // published to B's channel
		PreviousStatus: "open",
		NewStatus:      "completed",
		Title:          "Done",
		EventTimestamp: time.Now().UTC(),
		ReceivedAt:     time.Now().UTC(),
	}

	pub := broadcast.NewRedisPublisher(rdb)
	if err := pub.Publish(ctx, n); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case msg := <-chA:
		t.Fatalf("citizen-a should not receive citizen-b's message, got: %+v", msg)
	case <-time.After(200 * time.Millisecond):
		// correct: no message received on citizen-a's channel
	}
}
