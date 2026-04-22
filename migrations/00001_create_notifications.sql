-- +goose Up
CREATE TABLE notifications (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id        TEXT        NOT NULL,
    type             TEXT        NOT NULL,
    citizen_ref      BYTEA       NOT NULL,
    previous_status  TEXT        NOT NULL,
    new_status       TEXT        NOT NULL,
    title            TEXT        NOT NULL,
    description      TEXT,
    event_timestamp  TIMESTAMPTZ NOT NULL,
    received_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    read             BOOLEAN     NOT NULL DEFAULT FALSE,
    read_at          TIMESTAMPTZ,
    event_hash       BYTEA       NOT NULL UNIQUE
);

CREATE INDEX idx_notif_citizen_ts ON notifications (citizen_ref, event_timestamp DESC, id DESC);
CREATE INDEX idx_notif_unread     ON notifications (citizen_ref) WHERE read = FALSE;

-- +goose Down
DROP TABLE IF EXISTS notifications;
