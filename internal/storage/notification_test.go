package storage_test

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

var testDB *pgxpool.Pool

func TestMain(m *testing.M) {
	pool, err := testutil.NewTestDB("../../migrations")
	if err != nil {
		log.Fatalf("storage tests require postgres: %v", err)
	}
	testDB = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

// testRepo opens a transaction and registers a rollback on test cleanup,
// so each test runs in isolation without leaving data behind.
func testRepo(t *testing.T) *storage.NotificationRepo {
	t.Helper()
	tx, err := testDB.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { tx.Rollback(context.Background()) })
	return storage.NewNotificationRepo(tx)
}

func baseParams() storage.InsertParams {
	return storage.InsertParams{
		TicketID:       "CH-2024-001",
		Type:           "status_change",
		CitizenRef:     []byte("citizen-a"),
		PreviousStatus: "open",
		NewStatus:      "in_progress",
		Title:          "Ticket updated",
		EventTimestamp: time.Now().UTC().Truncate(time.Microsecond),
		EventHash:      []byte("hash-unique-1"),
	}
}

func TestInsert_Success(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	n, err := repo.Insert(ctx, baseParams())
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if n == nil {
		t.Fatal("expected non-nil notification on first insert")
	}
}

func TestInsert_DuplicateEventHash(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	p := baseParams()
	if _, err := repo.Insert(ctx, p); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	n, err := repo.Insert(ctx, p)
	if err != nil {
		t.Fatalf("duplicate insert returned error: %v", err)
	}
	if n != nil {
		t.Fatal("expected nil for duplicate event_hash")
	}
}

func TestInsert_DifferentHashSameTransition(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	p1 := baseParams()
	p2 := baseParams()
	p2.EventHash = []byte("hash-unique-2")

	if _, err := repo.Insert(ctx, p1); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	n, err := repo.Insert(ctx, p2)
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if n == nil {
		t.Fatal("expected non-nil notification for different event_hash")
	}
}

func TestFindByID_CitizenIsolation(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	p := baseParams()
	p.CitizenRef = []byte("citizen-a")
	if _, err := repo.Insert(ctx, p); err != nil {
		t.Fatalf("insert: %v", err)
	}

	items, _, err := repo.List(ctx, storage.ListParams{CitizenRef: []byte("citizen-a"), Limit: 10})
	if err != nil || len(items) == 0 {
		t.Fatalf("list: %v / %d items", err, len(items))
	}
	id := items[0].ID

	n, err := repo.FindByID(ctx, id, []byte("citizen-a"))
	if err != nil || n == nil {
		t.Fatalf("expected to find notification: %v", err)
	}

	n, err = repo.FindByID(ctx, id, []byte("citizen-b"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != nil {
		t.Fatal("citizen-b should not see citizen-a's notification")
	}
}

func TestList_PaginationAndOrder(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	citizenRef := []byte("citizen-paginate")
	base := time.Now().UTC().Truncate(time.Microsecond)

	for i := range 5 {
		p := baseParams()
		p.CitizenRef = citizenRef
		p.EventHash = []byte{byte(i)}
		p.EventTimestamp = base.Add(time.Duration(i) * time.Second)
		if _, err := repo.Insert(ctx, p); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	items, total, err := repo.List(ctx, storage.ListParams{CitizenRef: citizenRef, Limit: 3, Offset: 0})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(items) != 3 {
		t.Errorf("len(items) = %d, want 3", len(items))
	}
	if !items[0].EventTimestamp.After(items[1].EventTimestamp) {
		t.Error("expected descending order by event_timestamp")
	}
}

func TestMarkRead_IdempotentAndIsolation(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	p := baseParams()
	p.CitizenRef = []byte("citizen-mark")
	if _, err := repo.Insert(ctx, p); err != nil {
		t.Fatalf("insert: %v", err)
	}
	items, _, _ := repo.List(ctx, storage.ListParams{CitizenRef: []byte("citizen-mark"), Limit: 1})
	id := items[0].ID

	n, err := repo.MarkRead(ctx, id, []byte("citizen-mark"))
	if err != nil || n == nil {
		t.Fatalf("mark read: %v", err)
	}
	if !n.Read {
		t.Error("expected Read=true after MarkRead")
	}

	// idempotent: mark again
	n2, err := repo.MarkRead(ctx, id, []byte("citizen-mark"))
	if err != nil || n2 == nil {
		t.Fatalf("second mark read: %v", err)
	}

	// wrong citizen returns nil
	n3, err := repo.MarkRead(ctx, id, []byte("citizen-other"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n3 != nil {
		t.Fatal("citizen-other should not mark citizen-mark's notification")
	}
}

func TestCountUnread(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()

	citizenRef := []byte("citizen-count")
	for i := range 3 {
		p := baseParams()
		p.CitizenRef = citizenRef
		p.EventHash = []byte{byte(100 + i)}
		if _, err := repo.Insert(ctx, p); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	count, err := repo.CountUnread(ctx, citizenRef)
	if err != nil {
		t.Fatalf("count unread: %v", err)
	}
	if count != 3 {
		t.Errorf("unread count = %d, want 3", count)
	}

	items, _, _ := repo.List(ctx, storage.ListParams{CitizenRef: citizenRef, Limit: 1})
	repo.MarkRead(ctx, items[0].ID, citizenRef)

	count, _ = repo.CountUnread(ctx, citizenRef)
	if count != 2 {
		t.Errorf("unread count after mark = %d, want 2", count)
	}
}
