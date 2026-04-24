package ws

import (
	"context"
	"net/http"
	"time"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/auth"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/broadcast"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	pingInterval = 30 * time.Second
	pongTimeout  = 60 * time.Second
	writeTimeout = 10 * time.Second
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Subscriber interface {
	Subscribe(ctx context.Context, citizenRef []byte) <-chan broadcast.Message
}

// NoOpSubscriber never delivers any messages. Useful in tests.
type NoOpSubscriber struct{}

func (NoOpSubscriber) Subscribe(_ context.Context, _ []byte) <-chan broadcast.Message {
	return make(chan broadcast.Message)
}

type Handler struct {
	sub Subscriber
}

func NewHandler(sub Subscriber) *Handler {
	return &Handler{sub: sub}
}

func (h *Handler) Handle(c *gin.Context) {
	citizenRef, ok := auth.CitizenRefFromContext(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	conn.SetReadDeadline(time.Now().Add(pongTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongTimeout))
		return nil
	})

	msgs := h.sub.Subscribe(ctx, citizenRef)

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	// drain reads to process pong frames and detect client disconnect
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				cancel()
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-msgs:
			if !ok {
				return
			}
			conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := conn.WriteJSON(msg); err != nil {
				return
			}
		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
