package dlq

import (
	"context"
	"log/slog"
	"time"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
)

var (
	pollInterval   = 200 * time.Millisecond
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

// Run processa a fila de retry sem bloquear em entradas que ainda estão
// em backoff: elas ficam no ZSET com score futuro e são puladas pelo
// dequeueReady, deixando o worker livre pra atender as que já estão prontas.
func (w *Worker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		entry, err := w.queue.dequeueReady(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.ErrorContext(ctx, "dlq dequeue error", "error", err)
			if !sleep(ctx, pollInterval) {
				return
			}
			continue
		}
		if entry == nil {
			if !sleep(ctx, pollInterval) {
				return
			}
			continue
		}

		w.process(ctx, entry)
	}
}

func (w *Worker) process(ctx context.Context, entry *Entry) {
	n, err := w.repo.Insert(ctx, entry.Event)
	if err != nil {
		// ou banco fora, ou dado ruim, provavelmente banco, dado que nós inserimos os dados
		slog.ErrorContext(ctx, "dlq insert failed", "error", err, "attempts", entry.Attempts)
		if entry.Attempts < MaxAttempts {
			entry.Attempts++
			delay := min(backoffInitial<<(entry.Attempts-2), backoffMax)
			if enqErr := w.queue.enqueueAt(ctx, *entry, time.Now().Add(delay)); enqErr != nil {
				slog.ErrorContext(ctx, "dlq re-enqueue failed", "error", enqErr)
			}
			return
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
		return
	}

	if n == nil {
		// duplicate — already persisted by a previous attempt or the original request
		return
	}

	if err := w.pub.Publish(ctx, n); err != nil {
		slog.ErrorContext(ctx, "dlq publish failed", "error", err)
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
