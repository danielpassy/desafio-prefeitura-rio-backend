package dlq

import (
	"context"
	"log/slog"
	"time"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
)

var queueTimeout = 5 * time.Second

const (
	backoffInitial = 2 * time.Second
	backoffMax     = 30 * time.Second
)

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
	backoff := backoffInitial
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
			slog.ErrorContext(ctx, "dlq dequeue error", "error", err, "backoff", backoff.String())
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, backoffMax)
			continue
		}
		// reset backoff on any healthy dequeue (incl. timeout returning nil)
		backoff = backoffInitial

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
				continue
			}
			if mvErr := w.queue.MoveToDead(ctx, *entry); mvErr != nil {
				slog.ErrorContext(ctx, "dlq move-to-dead failed",
					"error", mvErr,
					"ticket_id", entry.Event.TicketID,
				)
			} else {
				slog.WarnContext(ctx, "dlq entry moved to dead queue",
					"ticket_id", entry.Event.TicketID,
					"failed_at", entry.FailedAt,
					"attempts", entry.Attempts,
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

