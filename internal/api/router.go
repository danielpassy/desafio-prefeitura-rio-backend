package api

import (
	"github.com/MicahParks/keyfunc/v3"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/auth"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/notification"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/webhook"
	"github.com/gin-gonic/gin"
)

type RouterParams struct {
	Keyfunc       keyfunc.Keyfunc
	Notifications *storage.NotificationRepo
	Publisher     webhook.Publisher
	WebhookSecret string
	CPFKey        string
}

func NewRouter(p RouterParams) *gin.Engine {
	wh := webhook.NewHandler(p.Notifications, p.Publisher, p.WebhookSecret, p.CPFKey)
	nh := notification.NewHandler(p.Notifications, p.CPFKey)

	r := gin.New()
	r.Use(gin.Recovery())
	r.POST("/webhook", wh.Handle)

	api := r.Group("/", auth.AuthMiddleware(p.Keyfunc))
	api.GET("/notifications", nh.List)
	api.GET("/notifications/unread-count", nh.UnreadCount)
	api.PATCH("/notifications/:id/read", nh.MarkRead)

	return r
}
