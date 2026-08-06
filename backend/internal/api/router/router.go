package router

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/zacp/zacp/internal/api/handlers"
	"github.com/zacp/zacp/internal/api/middleware"
	"github.com/zacp/zacp/internal/web"
	"github.com/zacp/zacp/internal/ws"
)

// New 构建 Gin 引擎并注册路由。
func New(
	workspaceHandler *handlers.WorkspaceHandler,
	sessionHandler *handlers.SessionHandler,
	chatHandler *handlers.ChatHandler,
	fileHandler *handlers.FileHandler,
	agentManageHandler *handlers.AgentManageHandler,
	wsHandler *ws.Handler,
	eventBridge *ws.EventBridge,
) *gin.Engine {
	r := gin.Default()
	// 跨域访问（前端 dev 直连后端；开发默认允许所有来源，见 middleware.CORS）
	r.Use(middleware.CORS())
	r.GET("/healthz", chatHandler.Health)

	v1 := r.Group("/api/v1")
	{
		// Agent 管理
		v1.GET("/agents", chatHandler.ListAgents)
		v1.GET("/agents/:agentId/status", chatHandler.GetAgentStatus)
		// 设置页智能体管理（全量目录 + 开关，见 handlers.AgentManageHandler）
		// 注意：/agents/manage 为静态段，与 /agents/:agentId/status 无路由冲突
		v1.GET("/agents/manage", agentManageHandler.ListManageAgents)
		v1.PUT("/agents/:agentId", agentManageHandler.SetAgentEnabled)

		// 工作目录管理
		v1.GET("/workspaces", workspaceHandler.ListWorkspaces)
		v1.POST("/workspaces", workspaceHandler.CreateWorkspace)
		v1.GET("/workspaces/:id", workspaceHandler.GetWorkspace)
		v1.DELETE("/workspaces/:id", workspaceHandler.DeleteWorkspace)

		// 工作区文件：浏览 / 上传 / 原始内容（图片预览、下载）
		v1.GET("/workspaces/:id/files", fileHandler.ListFiles)
		v1.POST("/workspaces/:id/files/upload", fileHandler.Upload)
		v1.GET("/workspaces/:id/files/raw", fileHandler.RawFile)

		// 目录浏览（新建项目弹窗用）：列出任意绝对路径下的子文件夹，与 workspace 无关
		v1.GET("/fs/directories", fileHandler.ListDirectories)

		// 会话管理
		v1.POST("/sessions", sessionHandler.CreateSession)
		v1.GET("/sessions", sessionHandler.ListRecentSessions)
		v1.GET("/sessions/:id", sessionHandler.GetSession)
		v1.PATCH("/sessions/:id", sessionHandler.RenameSession)
		v1.DELETE("/sessions/:id", sessionHandler.DeleteSession)
		// 草稿会话释放（切 tab / 离开空态时释放旧隐式草稿）
		v1.DELETE("/sessions/:id/draft", sessionHandler.DeleteDraftSession)
		v1.GET("/workspaces/:id/sessions", sessionHandler.ListSessions)

		// 消息管理
		v1.POST("/sessions/:id/messages", sessionHandler.SendMessage)
		v1.GET("/sessions/:id/messages", sessionHandler.GetMessages)

		// 会话配置项（模型/思考强度/mode 等，agent 支持才返回非空）
		v1.GET("/sessions/:id/config-options", sessionHandler.GetConfigOptions)
		v1.POST("/sessions/:id/config-options", sessionHandler.SetConfigOption)
		v1.GET("/sessions/:id/slash-commands", sessionHandler.GetSlashCommands)

		// Chat（兼容旧 demo）
		v1.POST("/chat", chatHandler.Chat)
		v1.POST("/cancel", chatHandler.Cancel)

		// WebSocket 连接
		v1.GET("/ws", func(c *gin.Context) {
			wsHandler.ServeHTTP(c.Writer, c.Request, eventBridge)
		})

		// 版本信息（构建时注入，前端设置页展示用）
		v1.GET("/version", handlers.GetVersion)
	}

	// 嵌入前端产物时注册静态资源 + SPA fallback（未运行 scripts/build.sh 时跳过，
	// dev 模式由 vite dev server 独立承担，此处保持 gin 默认 404）。
	if web.StaticEnabled() {
		staticFS := web.StaticFS()
		r.NoRoute(func(c *gin.Context) {
			p := strings.TrimPrefix(c.Request.URL.Path, "/")

			// API 路径未命中：返回 JSON 404，不能回 HTML（前端依赖错误结构统一解析）
			if p == "api" || strings.HasPrefix(p, "api/") {
				c.JSON(http.StatusNotFound, gin.H{
					"error": gin.H{"code": "not_found", "message": "API endpoint not found: " + c.Request.URL.Path},
				})
				return
			}

			// 静态资源（assets/*、favicon 等）存在则直接提供，不走 SPA fallback
			if f, err := staticFS.Open(p); err == nil {
				f.Close()
				http.ServeFileFS(c.Writer, c.Request, staticFS, p)
				return
			}

			// SPA fallback：前端为 history 路由，所有未匹配页面路径一律回 index.html，
			// 由前端 vue-router 自行解析（刷新 /settings 等深链时仍能正确渲染）
			idx, err := web.IndexHTML()
			if err != nil {
				c.JSON(http.StatusNotFound, gin.H{
					"error": gin.H{"code": "not_found", "message": "frontend not embedded, run scripts/build.sh"},
				})
				return
			}
			c.Data(http.StatusOK, "text/html; charset=utf-8", idx)
		})
	}
	return r
}
