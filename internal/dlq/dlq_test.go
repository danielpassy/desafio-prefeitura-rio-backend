package dlq

import (
	"context"
	"errors"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/testutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

var testDB *pgxpool.Pool

func TestMain(m *testing.M) {
	pool, err := testutil.NewTestDB("../../migrations")
	if err != nil {
		log.Fatalf("dlq tests require postgres: %v", err)
	}
	testDB = pool

	queueTimeout = 100 * time.Millisecond

	code := m.Run()
	pool.Close()
	os.Exit(code)
}

func newTestQueue(t *testing.T) (*Queue, *redis.Client) {
	t.Helper()
	rdb := testutil.NewTestRedis(t)
	rdb.Del(context.Background(), queueKey)
	t.Cleanup(func() { rdb.Del(context.Background(), queueKey) })
	return NewQueue(rdb), rdb
}

// -- fakes --

type errRow struct{}

func (errRow) Scan(_ ...any) error { return errors.New("injected db error") }

// failOnceQuerier fails the first QueryRow call, then delegates to real.
type failOnceQuerier struct {
	mu   sync.Mutex
	done bool
	real storage.Querier
}

func (q *failOnceQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return q.real.Exec(ctx, sql, args...)
}
func (q *failOnceQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return q.real.Query(ctx, sql, args...)
}
func (q *failOnceQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.done {
		q.done = true
		return errRow{}
	}
	return q.real.QueryRow(ctx, sql, args...)
}

// spyPublisher records calls and optionally returns an error.
type spyPublisher struct {
	ch  chan *storage.Notification
	err error
}

func newSpyPublisher() *spyPublisher {
	return &spyPublisher{ch: make(chan *storage.Notification, 8)}
}

func (s *spyPublisher) Publish(_ context.Context, n *storage.Notification) error {
	s.ch <- n
	return s.err
}

// -- helpers --

func makeEvent(ticketID, hash string) storage.InsertParams {
	return storage.InsertParams{
		TicketID:       ticketID,
		Type:           "status_change",
		CitizenRef:     []byte("citizen-ref"),
		PreviousStatus: "open",
		NewStatus:      "in_progress",
		Title:          "Test notification",
		EventTimestamp: time.Now(),
		EventHash:      []byte(hash),
	}
}

func newTestRepo(t *testing.T) *storage.NotificationRepo {
	t.Helper()
	tx, err := testDB.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { tx.Rollback(context.Background()) })
	return storage.NewNotificationRepo(tx)
}

// runWorker starts w.Run in a goroutine and returns cancel + a channel that
// closes when Run exits.
func runWorker(ctx context.Context, w *Worker) (cancel context.CancelFunc, done <-chan struct{}) {
	ctx, cancel = context.WithCancel(ctx)
	ch := make(chan struct{})
	go func() { w.Run(ctx); close(ch) }()
	return cancel, ch
}

// -- Queue tests --

func TestQueue_EnqueueDequeue(t *testing.T) {
	q, _ := newTestQueue(t)

	p := makeEvent("Q-TEST-001", "queue-enqueue-dequeue-hash")

	if err := q.Enqueue(context.Background(), p); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	entry, err := q.dequeue(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry, got nil")
	}
	if entry.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", entry.Attempts)
	}
	if entry.Event.TicketID != p.TicketID {
		t.Errorf("TicketID = %q, want %q", entry.Event.TicketID, p.TicketID)
	}
}

func TestQueue_DequeueEmpty(t *testing.T) {
	q, _ := newTestQueue(t)

	entry, err := q.dequeue(context.Background(), 100*time.Millisecond)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil on empty queue, got %+v", entry)
	}
}

// -- Worker tests --

func TestWorker_ContextCancel(t *testing.T) {
	q, _ := newTestQueue(t)
	w := NewWorker(q, newTestRepo(t), newSpyPublisher())

	cancel, done := runWorker(context.Background(), w)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after context cancel")
	}
}

func TestWorker_Successful_Publishes(t *testing.T) {
	q, _ := newTestQueue(t)
	const ticketID = "WORKER-SUCCESS"
	repo := newTestRepo(t)
	spy := newSpyPublisher()
	w := NewWorker(q, repo, spy)

	p := makeEvent(ticketID, "worker-success-hash")
	if err := q.Enqueue(context.Background(), p); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	cancel, done := runWorker(context.Background(), w)
	defer func() { cancel(); <-done }()

	select {
	case n := <-spy.ch:
		if n.TicketID != ticketID {
			t.Errorf("published TicketID = %q, want %q", n.TicketID, ticketID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not publish notification")
	}
}

func TestWorker_Duplicate_SkipsPublish(t *testing.T) {
	q, _ := newTestQueue(t)
	const ticketID = "WORKER-DUP"
	repo := newTestRepo(t)

	// Pre-insert entry A so that when the worker processes it, Insert returns nil (duplicate).
	dupParams := makeEvent(ticketID, "worker-dup-hash-A")
	if _, err := repo.Insert(context.Background(), dupParams); err != nil {
		t.Fatalf("pre-insert: %v", err)
	}
	// Enqueue duplicate A then new entry B; FIFO → A processed first.
	if err := q.Enqueue(context.Background(), dupParams); err != nil {
		t.Fatalf("Enqueue A: %v", err)
	}
	newParams := makeEvent(ticketID, "worker-dup-hash-B")
	if err := q.Enqueue(context.Background(), newParams); err != nil {
		t.Fatalf("Enqueue B: %v", err)
	}

	spy := newSpyPublisher()
	w := NewWorker(q, repo, spy)

	cancel, done := runWorker(context.Background(), w)
	defer func() { cancel(); <-done }()

	// B must be published (proves worker continued past the duplicate).
	select {
	case n := <-spy.ch:
		if string(n.EventHash) != "worker-dup-hash-B" {
			t.Errorf("unexpected first publish: hash=%q", n.EventHash)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not publish entry B")
	}

	// No second publish expected (A was skipped, B was the only one).
	select {
	case extra := <-spy.ch:
		t.Errorf("unexpected extra publish: %+v", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestWorker_RetryAfterFailure(t *testing.T) {
	q, _ := newTestQueue(t)
	const ticketID = "WORKER-RETRY"

	tx, err := testDB.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { tx.Rollback(context.Background()) })

	// failOnceQuerier fails the first Insert, then delegates to the real tx.
	fq := &failOnceQuerier{real: tx}
	repo := storage.NewNotificationRepo(fq)
	spy := newSpyPublisher()
	w := NewWorker(q, repo, spy)

	p := makeEvent(ticketID, "worker-retry-hash")
	if err := q.Enqueue(context.Background(), p); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	cancel, done := runWorker(context.Background(), w)
	defer func() { cancel(); <-done }()

	// First attempt fails → re-enqueue with Attempts=2 → second attempt succeeds → publish.
	select {
	case n := <-spy.ch:
		if n.TicketID != ticketID {
			t.Errorf("published TicketID = %q, want %q", n.TicketID, ticketID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not publish after retry")
	}
}

func TestWorker_MaxAttemptsDiscard(t *testing.T) {
	q, rdb := newTestQueue(t)
	repo := storage.NewNotificationRepo(testutil.ErrQuerier{})
	spy := newSpyPublisher()
	w := NewWorker(q, repo, spy)

	entry := Entry{
		Event:    makeEvent("WORKER-MAXATTEMPTS", "worker-maxattempts-hash"),
		FailedAt: time.Now(),
		Attempts: MaxAttempts, // already at the limit → must discard, not re-enqueue
	}
	if err := q.enqueueEntry(context.Background(), entry); err != nil {
		t.Fatalf("enqueueEntry: %v", err)
	}

	cancel, done := runWorker(context.Background(), w)
	defer func() { cancel(); <-done }()

	// Poll until the entry is dequeued (discarded), then assert.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		n, _ := rdb.LLen(context.Background(), queueKey).Result()
		if n == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	n, err := rdb.LLen(context.Background(), queueKey).Result()
	if err != nil {
		t.Fatalf("LLEN: %v", err)
	}
	if n != 0 {
		t.Errorf("queue length = %d after discard, want 0", n)
	}
	select {
	case got := <-spy.ch:
		t.Errorf("publisher should not have been called, got %+v", got)
	default:
	}
}

func TestWorker_PublishError_DoesNotHaltWorker(t *testing.T) {
	q, _ := newTestQueue(t)
	const ticketID = "WORKER-PUBERR"
	repo := newTestRepo(t)
	spy := newSpyPublisher()
	spy.err = errors.New("publish unavailable")
	w := NewWorker(q, repo, spy)

	p1 := makeEvent(ticketID, "worker-puberr-hash-1")
	p2 := makeEvent(ticketID, "worker-puberr-hash-2")
	if err := q.Enqueue(context.Background(), p1); err != nil {
		t.Fatalf("Enqueue 1: %v", err)
	}
	if err := q.Enqueue(context.Background(), p2); err != nil {
		t.Fatalf("Enqueue 2: %v", err)
	}

	cancel, done := runWorker(context.Background(), w)
	defer func() { cancel(); <-done }()

	// Worker must call Publish twice — once per entry — despite errors.
	for i := range 2 {
		select {
		case <-spy.ch:
		case <-time.After(3 * time.Second):
			t.Fatalf("worker stopped after publish error (received only %d of 2 calls)", i)
		}
	}
}
