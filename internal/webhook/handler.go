package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var cpfRe = regexp.MustCompile(`^\d{11}$`)

var validStatuses = map[string]bool{
	"open":           true,
	"under_analysis": true,
	"in_progress":    true,
	"completed":      true,
}

// Publisher broadcasts a persisted notification to connected WebSocket clients.
// Failures are best-effort and must not invalidate an already-committed event.
type Publisher interface {
	Publish(ctx context.Context, n *storage.Notification) error
}

// NoOpPublisher is used before the real Redis publisher is wired in.
type NoOpPublisher struct{}

func (NoOpPublisher) Publish(_ context.Context, _ *storage.Notification) error { return nil }

// DeadLetterQueue enqueues payloads that could not be persisted for later retry.
type DeadLetterQueue interface {
	Enqueue(ctx context.Context, e storage.InsertParams) error
}

type payload struct {
	TicketID       string  `json:"ticket_id"`
	Type           string  `json:"type"`
	CPF            string  `json:"cpf"`
	PreviousStatus string  `json:"previous_status"`
	NewStatus      string  `json:"new_status"`
	Title          string  `json:"title"`
	Description    *string `json:"description"`
	Timestamp      string  `json:"timestamp"`
}

type Handler struct {
	repo          *storage.NotificationRepo
	pub           Publisher
	dlq           DeadLetterQueue
	webhookSecret []byte
	cpfKey        []byte
}

func NewHandler(repo *storage.NotificationRepo, pub Publisher, dlq DeadLetterQueue, webhookSecret, cpfKey string) *Handler {
	return &Handler{
		repo:          repo,
		pub:           pub,
		dlq:           dlq,
		webhookSecret: []byte(webhookSecret),
		cpfKey:        []byte(cpfKey),
	}
}

func (h *Handler) Handle(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}

	if !h.validSignature(c.GetHeader("X-Signature-256"), body) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}

	if err := validatePayload(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ts, err := time.Parse(time.RFC3339, p.Timestamp)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid timestamp: must be RFC3339"})
		return
	}

	citizenRef := computeHMAC([]byte(p.CPF), h.cpfKey)
	hashArr := sha256.Sum256(body)
	eventHash := hashArr[:]

	span := trace.SpanFromContext(c.Request.Context())
	span.SetAttributes(
		attribute.String("webhook.ticket_id", p.TicketID),
		attribute.String("webhook.type", p.Type),
		attribute.String("webhook.new_status", p.NewStatus),
	)

	event := storage.InsertParams{
		TicketID:       p.TicketID,
		Type:           p.Type,
		CitizenRef:     citizenRef,
		PreviousStatus: p.PreviousStatus,
		NewStatus:      p.NewStatus,
		Title:          p.Title,
		Description:    p.Description,
		EventTimestamp: ts,
		EventHash:      eventHash,
	}

	n, err := h.repo.Insert(c.Request.Context(), event)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "webhook insert failed", "error", err)
		if dlqErr := h.dlq.Enqueue(c.Request.Context(), event); dlqErr != nil {
			slog.ErrorContext(c.Request.Context(), "webhook dlq enqueue failed", "error", dlqErr)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if n == nil {
		// duplicate — idempotent 200 with no re-publish (R-WH-15)
		span.SetAttributes(attribute.Bool("webhook.duplicate", true))
		c.Status(http.StatusOK)
		return
	}

	if err := h.pub.Publish(c.Request.Context(), n); err != nil {
		// broadcast failure must not invalidate a committed event (R-WH-14)
		slog.ErrorContext(c.Request.Context(), "webhook broadcast failed", "error", err)
	}

	c.Status(http.StatusOK)
}

func (h *Handler) validSignature(header string, body []byte) bool {
	sig, ok := strings.CutPrefix(header, "sha256=")
	if !ok || sig == "" {
		return false
	}
	actual, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	expected := computeHMAC(body, h.webhookSecret)
	return hmac.Equal(expected, actual)
}

func computeHMAC(data, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func validatePayload(p *payload) error {
	if p.TicketID == "" {
		return errBadRequest("ticket_id is required")
	}
	if p.Type != "status_change" {
		return errBadRequest("type must be status_change")
	}
	if !cpfRe.MatchString(p.CPF) {
		return errBadRequest("cpf must be 11 numeric digits")
	}
	if !validStatuses[p.PreviousStatus] {
		return errBadRequest("invalid previous_status")
	}
	if !validStatuses[p.NewStatus] {
		return errBadRequest("invalid new_status")
	}
	if p.Title == "" {
		return errBadRequest("title is required")
	}
	if p.Timestamp == "" {
		return errBadRequest("timestamp is required")
	}
	return nil
}

type validationError struct{ msg string }

func (e validationError) Error() string { return e.msg }

func errBadRequest(msg string) error { return validationError{msg} }
