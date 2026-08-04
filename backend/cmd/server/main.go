package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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
	})
	defer mgr.Close()

	// 启动所有 enabled 的 agent
	ctx := context.Background()
	for _, agentID := range registry.List() {
		if err := mgr.StartAgent(ctx, agentID); err != nil {
			log.Warn("failed to start agent", "agent", agentID, "err", err)
		} else {
			log.Info("agent started", "agent", agentID)
		}
	}

	// 创建 WebSocket Hub
	wsHub := ws.NewHub(log)
	go wsHub.Run()
	log.Info("websocket hub started")

	// 创建 WebSocket Handler
	wsHandler := ws.NewHandler(wsHub, log)

	// 创建 EventBridge
	eventBridge := ws.NewEventBridge(wsHandler, mgr, log)
	log.Info("event bridge created")

	// 创建 Service
	workspaceSvc := service.NewWorkspaceService(workspaceRepo)
	sessionSvc := service.NewSessionService(workspaceRepo, sessionRepo, messageRepo, mgr)

	// 创建 Handler
	workspaceHandler := handlers.NewWorkspaceHandler(workspaceSvc)
	sessionHandler := handlers.NewSessionHandler(sessionSvc)
	chatHandler := &handlers.ChatHandler{Mgr: mgr}

	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	engine := router.New(workspaceHandler, sessionHandler, chatHandler, wsHandler, eventBridge)

	// Graceful shutdown on signal.
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		sig := <-ch
		log.Info("shutting down", "signal", sig.String())
		wsHandler.CloseAll()
		_ = mgr.Close()
		os.Exit(0)
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
