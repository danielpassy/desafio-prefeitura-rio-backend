package storage

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Notification struct {
	ID             uuid.UUID
	TicketID       string
	Type           string
	CitizenRef     []byte
	PreviousStatus string
	NewStatus      string
	Title          string
	Description    *string
	EventTimestamp time.Time
	ReceivedAt     time.Time
	Read           bool
	ReadAt         *time.Time
	EventHash      []byte
}

type InsertParams struct {
	TicketID       string
	Type           string
	CitizenRef     []byte
	PreviousStatus string
	NewStatus      string
	Title          string
	Description    *string
	EventTimestamp time.Time
	EventHash      []byte
}

type ListParams struct {
	CitizenRef []byte
	Limit      int
	Offset     int
}

type NotificationRepo struct {
	db Querier
}

func NewNotificationRepo(db Querier) *NotificationRepo {
	return &NotificationRepo{db: db}
}

// Insert persists a new notification. Returns nil when event_hash already
// exists (duplicate event — caller should respond 200 without re-publishing).
func (r *NotificationRepo) Insert(ctx context.Context, p InsertParams) (*Notification, error) {
	const q = `
		INSERT INTO notifications
			(ticket_id, type, citizen_ref, previous_status, new_status,
			 title, description, event_timestamp, event_hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (event_hash) DO NOTHING
		RETURNING id, ticket_id, type, citizen_ref, previous_status, new_status,
		          title, description, event_timestamp, received_at, read, read_at, event_hash`

	row := r.db.QueryRow(ctx, q,
		p.TicketID, p.Type, p.CitizenRef, p.PreviousStatus, p.NewStatus,
		p.Title, p.Description, p.EventTimestamp, p.EventHash,
	)
	n, err := scanNotification(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return n, err
}

func (r *NotificationRepo) FindByID(ctx context.Context, id uuid.UUID, citizenRef []byte) (*Notification, error) {
	const q = `
		SELECT id, ticket_id, type, citizen_ref, previous_status, new_status,
		       title, description, event_timestamp, received_at, read, read_at, event_hash
		FROM notifications
		WHERE id = $1 AND citizen_ref = $2`

	row := r.db.QueryRow(ctx, q, id, citizenRef)
	n, err := scanNotification(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return n, err
}

func (r *NotificationRepo) List(ctx context.Context, p ListParams) (items []Notification, total int, err error) {
	const q = `
		SELECT id, ticket_id, type, citizen_ref, previous_status, new_status,
		       title, description, event_timestamp, received_at, read, read_at, event_hash
		FROM notifications
		WHERE citizen_ref = $1
		ORDER BY event_timestamp DESC, id DESC
		LIMIT $2 OFFSET $3`

	rows, err := r.db.Query(ctx, q, p.CitizenRef, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *n)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	total, err = r.CountAll(ctx, p.CitizenRef)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *NotificationRepo) MarkRead(ctx context.Context, id uuid.UUID, citizenRef []byte) (*Notification, error) {
	const q = `
		UPDATE notifications
		SET read = TRUE, read_at = NOW()
		WHERE id = $1 AND citizen_ref = $2
		RETURNING id, ticket_id, type, citizen_ref, previous_status, new_status,
		          title, description, event_timestamp, received_at, read, read_at, event_hash`

	row := r.db.QueryRow(ctx, q, id, citizenRef)
	n, err := scanNotification(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return n, err
}

func (r *NotificationRepo) CountUnread(ctx context.Context, citizenRef []byte) (int, error) {
	var count int
	err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM notifications WHERE citizen_ref = $1 AND read = FALSE`,
		citizenRef,
	).Scan(&count)
	return count, err
}

func (r *NotificationRepo) CountAll(ctx context.Context, citizenRef []byte) (int, error) {
	var count int
	err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM notifications WHERE citizen_ref = $1`,
		citizenRef,
	).Scan(&count)
	return count, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanNotification(s scanner) (*Notification, error) {
	var n Notification
	err := s.Scan(
		&n.ID, &n.TicketID, &n.Type, &n.CitizenRef,
		&n.PreviousStatus, &n.NewStatus, &n.Title, &n.Description,
		&n.EventTimestamp, &n.ReceivedAt, &n.Read, &n.ReadAt, &n.EventHash,
	)
	if err != nil {
		return nil, err
	}
	return &n, nil
}
