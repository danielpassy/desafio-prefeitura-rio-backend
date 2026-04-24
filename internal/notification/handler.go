package notification

import (
	"net/http"
	"time"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/auth"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/httputil"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type notificationResponse struct {
	ID             uuid.UUID  `json:"id"`
	TicketID       string     `json:"ticket_id"`
	Type           string     `json:"type"`
	PreviousStatus string     `json:"previous_status"`
	NewStatus      string     `json:"new_status"`
	Title          string     `json:"title"`
	Description    *string    `json:"description"`
	EventTimestamp time.Time  `json:"event_timestamp"`
	ReceivedAt     time.Time  `json:"received_at"`
	Read           bool       `json:"read"`
	ReadAt         *time.Time `json:"read_at"`
}

type listResponse struct {
	Items  []notificationResponse `json:"items"`
	Total  int                    `json:"total"`
	Limit  int                    `json:"limit"`
	Offset int                    `json:"offset"`
}

type Handler struct {
	repo *storage.NotificationRepo
}

func NewHandler(repo *storage.NotificationRepo) *Handler {
	return &Handler{repo: repo}
}

func (h *Handler) List(c *gin.Context) {
	citizenRef, ok := auth.CitizenRefFromContext(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	limit, offset, ok := httputil.ParsePagination(c)
	if !ok {
		return
	}

	items, total, err := h.repo.List(c.Request.Context(), storage.ListParams{
		CitizenRef: citizenRef,
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	resp := listResponse{
		Items:  make([]notificationResponse, 0, len(items)),
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}
	for _, n := range items {
		resp.Items = append(resp.Items, toResponse(n))
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) MarkRead(c *gin.Context) {
	citizenRef, ok := auth.CitizenRefFromContext(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}

	n, err := h.repo.MarkRead(c.Request.Context(), id, citizenRef)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if n == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, toResponse(*n))
}

func (h *Handler) UnreadCount(c *gin.Context) {
	citizenRef, ok := auth.CitizenRefFromContext(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	count, err := h.repo.CountUnread(c.Request.Context(), citizenRef)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"unread_count": count})
}

func toResponse(n storage.Notification) notificationResponse {
	return notificationResponse{
		ID:             n.ID,
		TicketID:       n.TicketID,
		Type:           n.Type,
		PreviousStatus: n.PreviousStatus,
		NewStatus:      n.NewStatus,
		Title:          n.Title,
		Description:    n.Description,
		EventTimestamp: n.EventTimestamp,
		ReceivedAt:     n.ReceivedAt,
		Read:           n.Read,
		ReadAt:         n.ReadAt,
	}
}
