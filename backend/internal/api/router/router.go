package router

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/helloxz/zacp/internal/api/handlers"
	"github.com/helloxz/zacp/internal/api/middleware"
	"github.com/helloxz/zacp/internal/auth"
	"github.com/helloxz/zacp/internal/web"
	"github.com/helloxz/zacp/internal/ws"
)

// New 构建 Gin 引擎并注册路由。
func New(
	workspaceHandler *handlers.WorkspaceHandler,
	sessionHandler *handlers.SessionHandler,
	chatHandler *handlers.ChatHandler,
	fileHandler *handlers.FileHandler,
	gitHandler *handlers.GitHandler,
	agentManageHandler *handlers.AgentManageHandler,
	toolHandler *handlers.ToolHandler,
	authHandler *handlers.AuthHandler,
	wsHandler *ws.Handler,
	eventBridge *ws.EventBridge,
	authSvc *auth.Service,
) *gin.Engine {
	r := gin.Default()
	// 跨域访问（前端 dev 直连后端；开发默认允许所有来源，见 middleware.CORS）
	r.Use(middleware.CORS())
	r.GET("/healthz", chatHandler.Health)

	v1 := r.Group("/api/v1")
	{
		// ---- 免认证端点（登录前可达） ----
		v1.POST("/auth/login", authHandler.Login)
		v1.GET("/auth/status", authHandler.Status)

		// 文件直链：双模式校验（Authorization 主 token 或 ?token= 资源 token），
		// 见 middleware.FileRaw；不能放进认证组，因为 <img src> 直链只带资源 token。
		v1.GET("/workspaces/:id/files/raw", middleware.FileRaw(authSvc), fileHandler.RawFile)

		// WebSocket 主通道：token 经 Sec-WebSocket-Protocol 子协议携带
		// （浏览器 WebSocket 无法设置自定义 header），由 wsHandler 内部校验，
		// 故放在认证组之外。
		v1.GET("/ws", func(c *gin.Context) {
			wsHandler.ServeHTTP(c.Writer, c.Request, eventBridge)
		})

		// ---- 需认证端点（认证未启用时中间件直接放行，保持默认无需登录） ----
		authed := v1.Group("", middleware.RequireMain(authSvc))
		{
			// 账号认证：修改用户名/密码（写回 config.toml，热生效）
			authed.PUT("/auth/credentials", authHandler.UpdateCredentials)

			// Agent 管理
			authed.GET("/agents", chatHandler.ListAgents)
			authed.GET("/agents/:agentId/status", chatHandler.GetAgentStatus)
			// 设置页智能体管理（全量目录 + 开关，见 handlers.AgentManageHandler）
			// 注意：/agents/manage 为静态段，与 /agents/:agentId/status 无路由冲突
			authed.GET("/agents/manage", agentManageHandler.ListManageAgents)
			authed.PUT("/agents/:agentId", agentManageHandler.SetAgentEnabled)

			// 本地工具：只返回后端白名单中当前平台已安装的工具。
			authed.GET("/tools", toolHandler.ListTools)

			// 工作目录管理
			authed.GET("/workspaces", workspaceHandler.ListWorkspaces)
			authed.POST("/workspaces", workspaceHandler.CreateWorkspace)
			authed.GET("/workspaces/:id", workspaceHandler.GetWorkspace)
			authed.DELETE("/workspaces/:id", workspaceHandler.DeleteWorkspace)

			// 工作区文件：浏览 / 上传 / 重命名 / 内容读写 / 直链签发
			authed.GET("/workspaces/:id/files", fileHandler.ListFiles)
			authed.POST("/workspaces/:id/files/upload", fileHandler.Upload)
			authed.PATCH("/workspaces/:id/files/rename", fileHandler.RenameFile)
			authed.POST("/workspaces/:id/files/preview-token", fileHandler.PreviewToken)
			authed.GET("/workspaces/:id/files/content", fileHandler.ReadFileContent)
			authed.PUT("/workspaces/:id/files/content", fileHandler.WriteFileContent)
			authed.GET("/workspaces/:id/git/status", gitHandler.Status)

			// 目录浏览（新建项目弹窗用）：列出任意绝对路径下的子文件夹，与 workspace 无关
			authed.GET("/fs/directories", fileHandler.ListDirectories)

			// 会话管理
			authed.POST("/sessions", sessionHandler.CreateSession)
			authed.GET("/sessions", sessionHandler.ListRecentSessions)
			authed.GET("/sessions/:id", sessionHandler.GetSession)
			authed.PATCH("/sessions/:id", sessionHandler.RenameSession)
			authed.DELETE("/sessions/:id", sessionHandler.DeleteSession)
			// 草稿会话释放（切 tab / 离开空态时释放旧隐式草稿）
			authed.DELETE("/sessions/:id/draft", sessionHandler.DeleteDraftSession)
			authed.POST("/sessions/:id/open-tool", toolHandler.OpenSessionTool)
			authed.GET("/workspaces/:id/sessions", sessionHandler.ListSessions)

			// 消息管理
			authed.POST("/sessions/:id/messages", sessionHandler.SendMessage)
			authed.GET("/sessions/:id/messages", middleware.Gzip(), sessionHandler.GetMessages)

			// 会话配置项（模型/思考强度/mode 等，agent 支持才返回非空）
			authed.GET("/sessions/:id/config-options", sessionHandler.GetConfigOptions)
			authed.POST("/sessions/:id/config-options", sessionHandler.SetConfigOption)
			authed.GET("/sessions/:id/slash-commands", sessionHandler.GetSlashCommands)

			// Chat（兼容旧 demo）
			authed.POST("/chat", chatHandler.Chat)
			authed.POST("/cancel", chatHandler.Cancel)

			// 版本信息（构建时注入，前端设置页展示用）
			authed.GET("/version", handlers.GetVersion)
		}
	}

	// 嵌入前端产物时注册静态资源 + SPA fallback（未运行 scripts/build.sh 时跳过，
	// dev 模式由 vite dev server 独立承担，此处保持 gin 默认 404）。
	if web.StaticEnabled() {
		staticFS := web.StaticFS()
		r.NoRoute(middleware.GzipIf(func(c *gin.Context) bool {
			return middleware.StaticAssetPath(c.Request.URL.Path)
		}), func(c *gin.Context) {
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
