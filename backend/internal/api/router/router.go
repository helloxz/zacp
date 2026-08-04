package router

import (
	"github.com/gin-gonic/gin"

	"github.com/zacp/zacp/internal/api/handlers"
	"github.com/zacp/zacp/internal/ws"
)

// New 构建 Gin 引擎并注册路由。
func New(
	workspaceHandler *handlers.WorkspaceHandler,
	sessionHandler *handlers.SessionHandler,
	chatHandler *handlers.ChatHandler,
	wsHandler *ws.Handler,
	eventBridge *ws.EventBridge,
) *gin.Engine {
	r := gin.Default()
	r.GET("/healthz", chatHandler.Health)

	v1 := r.Group("/api/v1")
	{
		// Agent 管理
		v1.GET("/agents", chatHandler.ListAgents)
		v1.GET("/agents/:agentId/status", chatHandler.GetAgentStatus)

		// 工作目录管理
		v1.GET("/workspaces", workspaceHandler.ListWorkspaces)
		v1.POST("/workspaces", workspaceHandler.CreateWorkspace)
		v1.GET("/workspaces/:id", workspaceHandler.GetWorkspace)
		v1.DELETE("/workspaces/:id", workspaceHandler.DeleteWorkspace)

		// 会话管理
		v1.POST("/sessions", sessionHandler.CreateSession)
		v1.GET("/sessions/:id", sessionHandler.GetSession)
		v1.DELETE("/sessions/:id", sessionHandler.DeleteSession)
		v1.GET("/workspaces/:id/sessions", sessionHandler.ListSessions)

		// 消息管理
		v1.POST("/sessions/:id/messages", sessionHandler.SendMessage)
		v1.GET("/sessions/:id/messages", sessionHandler.GetMessages)

		// Chat（兼容旧 demo）
		v1.POST("/chat", chatHandler.Chat)
		v1.POST("/cancel", chatHandler.Cancel)

		// WebSocket 连接
		v1.GET("/ws", func(c *gin.Context) {
			wsHandler.ServeHTTP(c.Writer, c.Request, eventBridge)
		})
	}
	return r
}
