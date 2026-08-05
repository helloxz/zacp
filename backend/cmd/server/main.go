package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/zacp/zacp/internal/acp/manager"
	"github.com/zacp/zacp/internal/acp/providers"
	"github.com/zacp/zacp/internal/api/handlers"
	"github.com/zacp/zacp/internal/api/router"
	"github.com/zacp/zacp/internal/config"
	"github.com/zacp/zacp/internal/service"
	"github.com/zacp/zacp/internal/store"
	"github.com/zacp/zacp/internal/ws"
)

func main() {
	addr := flag.String("addr", envOr("ZACP_ADDR", ":8680"), "HTTP listen address")
	configPath := flag.String("config", envOr("ZACP_CONFIG", ""), "Config file path (default: $ZACP_HOME/config.toml)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// 确保 ZACP_HOME 存在
	homeDir, err := config.EnsureHomeDir()
	if err != nil {
		log.Error("failed to ensure home dir", "err", err)
		os.Exit(1)
	}
	log.Info("ZACP_HOME", "path", homeDir)

	// 加载配置
	cfg, err := config.Load(homeDir, *configPath)
	if err != nil {
		log.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	log.Info("config loaded", "agents", len(cfg.Agents), "autoApprove", cfg.Session.AutoApprove)

	// 初始化数据库
	dbPath := cfg.ResolveDBPath(homeDir)
	st, err := store.New(store.Config{DBPath: dbPath})
	if err != nil {
		log.Error("failed to open database", "err", err, "path", dbPath)
		os.Exit(1)
	}
	defer st.Close()
	log.Info("database ready", "path", dbPath)

	// 创建 Repository
	workspaceRepo := store.NewWorkspaceRepository(st.DB)
	sessionRepo := store.NewSessionRepository(st.DB)
	messageRepo := store.NewMessageRepository(st.DB)

	// 创建 Provider Registry
	registry, err := providers.NewRegistry(cfg.Agents)
	if err != nil {
		log.Error("failed to create provider registry", "err", err)
		os.Exit(1)
	}
	log.Info("provider registry ready", "agents", registry.List())

	// 创建 Manager
	mgr := manager.New(log, manager.Config{
		Registry:    registry,
		AutoApprove: cfg.Session.AutoApprove,
		DefaultCwd:  cfg.Session.DefaultCwd,
		IdleTimeout: cfg.Session.IdleTimeout,
	})
	defer mgr.Close()

	// 仅预启动配置中第一个（最顶部）enabled 的 agent，保证空态有可用 agent；
	// 其余 agent 按需启动：前端切换 agent 建会话时经 service 自动拉起。
	ctx := context.Background()
	if ids := registry.List(); len(ids) > 0 {
		preloadID := ids[0]
		if err := mgr.StartAgent(ctx, preloadID); err != nil {
			log.Warn("failed to preload agent", "agent", preloadID, "err", err)
		} else {
			log.Info("agent preloaded", "agent", preloadID)
		}
	}
	if cfg.Session.IdleTimeout > 0 {
		log.Info("other agents start on demand", "idleTimeout", cfg.Session.IdleTimeout.String())
	} else {
		log.Info("other agents start on demand", "idleRecycle", "disabled")
	}

	// 创建 WebSocket Hub
	wsHub := ws.NewHub(log)
	go wsHub.Run()
	log.Info("websocket hub started")

	// 创建 WebSocket Handler
	wsHandler := ws.NewHandler(wsHub, log)

	// 创建 EventBridge
	eventBridge := ws.NewEventBridge(wsHandler, mgr, sessionRepo, messageRepo, log)
	log.Info("event bridge created")

	// 创建 Service
	workspaceSvc := service.NewWorkspaceService(workspaceRepo)
	sessionSvc := service.NewSessionService(workspaceRepo, sessionRepo, messageRepo, mgr, cfg.Session.DefaultCwd)
	fileSvc := service.NewFileService(workspaceRepo)

	// 创建 Handler
	workspaceHandler := handlers.NewWorkspaceHandler(workspaceSvc)
	sessionHandler := handlers.NewSessionHandler(sessionSvc, eventBridge)
	chatHandler := &handlers.ChatHandler{Mgr: mgr}
	fileHandler := handlers.NewFileHandler(fileSvc)

	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	engine := router.New(workspaceHandler, sessionHandler, chatHandler, fileHandler, wsHandler, eventBridge)

	// Graceful shutdown on signal.
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		sig := <-ch
		log.Info("shutting down", "signal", sig.String())
		done := make(chan struct{})
		go func() {
			wsHandler.CloseAll()
			_ = mgr.Close()
			close(done)
		}()
		// 优雅关闭兜底：agent 子进程/WS 清理异常时强制退出，避免 Ctrl+C 卡死
		select {
		case <-done:
			os.Exit(0)
		case <-time.After(5 * time.Second):
			log.Error("graceful shutdown timed out, forcing exit")
			os.Exit(1)
		}
	}()

	log.Info("HTTP listening", "addr", *addr)
	log.Info("try: curl -s http://127.0.0.1" + normalizeAddr(*addr) + "/api/v1/agents")
	log.Info("websocket endpoint: ws://127.0.0.1" + normalizeAddr(*addr) + "/api/v1/ws")
	if err := engine.Run(*addr); err != nil {
		log.Error("http server failed", "err", err)
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func normalizeAddr(addr string) string {
	if len(addr) > 0 && addr[0] == ':' {
		return addr
	}
	return ":" + addr
}
