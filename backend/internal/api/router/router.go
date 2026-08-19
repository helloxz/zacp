package router

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/helloxz/zacp/internal/api/handlers"
	"github.com/helloxz/zacp/internal/api/middleware"
	"github.com/helloxz/zacp/internal/auth"
	"github.com/helloxz/zacp/internal/tty"
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
	ttyHandler *tty.Handler,
	authSvc *auth.Service,
) *gin.Engine {
	r := gin.New()
	// release 模式输出安静日志：不注册访问日志中间件（[GIN] 请求路径不再打印），
	// 与 main 的 slog 级别联动（release 下仅 warn+，见 newSlogLogger）。
	// gin.Mode() 已在 main 按 GIN_MODE 环境变量 > config server.mode 设置，这里直接跟随。
	if gin.Mode() != gin.ReleaseMode {
		r.Use(gin.Logger())
	}
	r.Use(gin.Recovery())
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

		// TTY 通道使用与 ACP 相同的 WebSocket 子协议认证，但数据协议和生命周期完全独立。
		v1.GET("/tty/ws", func(c *gin.Context) {
			ttyHandler.ServeHTTP(c.Writer, c.Request)
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
			// 添加自定义智能体（写 config.toml + 热更新，默认启用）
			authed.POST("/agents", agentManageHandler.AddAgent)
			authed.PUT("/agents/:agentId", agentManageHandler.SetAgentEnabled)
			// 删除自定义智能体（移除 config.toml 块 + 热更新停用）
			authed.DELETE("/agents/:agentId", agentManageHandler.DeleteAgent)
			// 智能体配置文件读写（设置页「编辑配置」弹窗；路径白名单在后端校验）
			authed.GET("/agents/:agentId/config-files", agentManageHandler.ListConfigFiles)
			authed.GET("/agents/:agentId/config-files/content", agentManageHandler.ReadConfigFileContent)
			authed.PUT("/agents/:agentId/config-files/content", agentManageHandler.WriteConfigFileContent)
			// zlite 官方智能体「默认渠道设置」结构化读写（固定路径 ~/.zlite，仅 zlite 使用；
			// 静态段与 /agents/:agentId 动态段不冲突，manage 段已有先例）
			authed.GET("/agents/zlite/default-channel", agentManageHandler.GetZliteDefaultChannel)
			authed.PUT("/agents/zlite/default-channel", agentManageHandler.SaveZliteDefaultChannel)
			// 安装 zlite（未安装时设置页显示安装按钮；5 分钟超时的远程脚本安装）
			authed.POST("/agents/zlite/install", agentManageHandler.InstallZlite)

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
			authed.DELETE("/workspaces/:id/files", fileHandler.Delete)
			authed.POST("/workspaces/:id/files/preview-token", fileHandler.PreviewToken)
			authed.GET("/workspaces/:id/files/content", fileHandler.ReadFileContent)
			authed.PUT("/workspaces/:id/files/content", fileHandler.WriteFileContent)
			authed.GET("/workspaces/:id/git/status", gitHandler.Status)
			// 提交选中文件（可选 push）；push 失败返回 200 + pushError，前端可经 /git/push 重试
			authed.POST("/workspaces/:id/git/commit", gitHandler.Commit)
			authed.POST("/workspaces/:id/git/push", gitHandler.Push)

			// 目录浏览（新建项目弹窗用）：列出任意绝对路径下的子文件夹，与 workspace 无关
			authed.GET("/fs/directories", fileHandler.ListDirectories)

			// 临时目录上传（聊天输入框快捷键粘贴上传专用；不依赖 workspace）
			authed.POST("/files/upload-temp", fileHandler.UploadTemp)

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
			// 单条消息的思考过程（列表接口已置空瘦身，前端展开时按需加载）
			authed.GET("/sessions/:id/messages/:messageId/thoughts", middleware.Gzip(), sessionHandler.GetMessageThoughts)

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
