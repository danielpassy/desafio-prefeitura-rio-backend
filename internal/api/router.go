package api

import (
	"github.com/MicahParks/keyfunc/v3"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/auth"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/notification"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/webhook"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/ws"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

type RouterParams struct {
	Keyfunc       keyfunc.Keyfunc
	Notifications *storage.NotificationRepo
	Publisher     webhook.Publisher
	DLQ           webhook.DeadLetterQueue
	Subscriber    ws.Subscriber
	WebhookSecret string
	CPFKey        string
}

func NewRouter(p RouterParams) *gin.Engine {
	wh := webhook.NewHandler(p.Notifications, p.Publisher, p.DLQ, p.WebhookSecret, p.CPFKey)
	nh := notification.NewHandler(p.Notifications)
	wsh := ws.NewHandler(p.Subscriber)

	r := gin.New()
	r.Use(otelgin.Middleware("notifications-api"), gin.Logger(), gin.Recovery())
	r.POST("/webhook", wh.Handle)

	api := r.Group("/", auth.AuthMiddleware(p.Keyfunc, []byte(p.CPFKey)))
	api.GET("/notifications", nh.List)
	api.GET("/notifications/unread-count", nh.UnreadCount)
	api.PATCH("/notifications/:id/read", nh.MarkRead)
	api.GET("/ws", wsh.Handle)

	return r
}
