package router

import (
	"github.com/gin-gonic/gin"

	"github.com/zacp/zacp/internal/api/handlers"
)

// New builds the Gin engine with demo routes.
func New(h *handlers.ChatHandler) *gin.Engine {
	r := gin.Default()
	r.GET("/healthz", h.Health)

	v1 := r.Group("/api/v1")
	{
		v1.POST("/chat", h.Chat)
		v1.POST("/cancel", h.Cancel)
	}
	return r
}
