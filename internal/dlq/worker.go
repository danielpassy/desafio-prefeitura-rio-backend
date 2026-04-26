package dlq

import (
	"context"
	"log/slog"
	"time"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
)

var queueTimeout = 5 * time.Second

type Publisher interface {
	Publish(ctx context.Context, n *storage.Notification) error
}

type Worker struct {
	queue *Queue
	repo  *storage.NotificationRepo
	pub   Publisher
}

func NewWorker(queue *Queue, repo *storage.NotificationRepo, pub Publisher) *Worker {
	return &Worker{queue: queue, repo: repo, pub: pub}
}

func (w *Worker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		entry, err := w.queue.dequeue(ctx, queueTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.ErrorContext(ctx, "dlq dequeue error", "error", err)
			continue
		}
		if entry == nil {
			continue
		}

		n, err := w.repo.Insert(ctx, entry.Event)
		if err != nil {
			slog.ErrorContext(ctx, "dlq insert failed", "error", err, "attempts", entry.Attempts)
			if entry.Attempts < MaxAttempts {
				entry.Attempts++
				if enqErr := w.queue.enqueueEntry(ctx, *entry); enqErr != nil {
					slog.ErrorContext(ctx, "dlq re-enqueue failed", "error", enqErr)
				}
			} else {
				slog.ErrorContext(ctx, "dlq max attempts reached, discarding",
					"ticket_id", entry.Event.TicketID,
					"failed_at", entry.FailedAt,
				)
			}
			continue
		}

		if n == nil {
			// duplicate — already persisted by a previous attempt or the original request
			continue
		}

		if err := w.pub.Publish(ctx, n); err != nil {
			slog.ErrorContext(ctx, "dlq publish failed", "error", err)
		}
	}
}
