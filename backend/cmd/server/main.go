package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/helloxz/zacp/internal/acp/manager"
	"github.com/helloxz/zacp/internal/acp/providers"
	"github.com/helloxz/zacp/internal/api/handlers"
	"github.com/helloxz/zacp/internal/api/router"
	"github.com/helloxz/zacp/internal/config"
	"github.com/helloxz/zacp/internal/service"
	"github.com/helloxz/zacp/internal/store"
	"github.com/helloxz/zacp/internal/version"
	"github.com/helloxz/zacp/internal/ws"
)

func main() {
	// 命令行参数（均可不传，缺省走回退链，命令行优先级最高）：
	//   --addr     监听地址 IP:PORT；回退 ZACP_ADDR 环境变量 → TOML server.addr → :8680
	//   --data-dir ZACP_DATA 状态根目录；回退 ZACP_DATA 环境变量 → ~/.zacp
	//   --config   配置文件路径；回退 ZACP_CONFIG 环境变量 → $ZACP_DATA/config.toml
	addr := flag.String("addr", "", "HTTP listen address (IP:PORT); fallback: ZACP_ADDR env -> config server.addr -> :8680")
	dataDir := flag.String("data-dir", "", "ZACP_DATA state directory; fallback: ZACP_DATA env -> ~/.zacp")
	configPath := flag.String("config", envOr("ZACP_CONFIG", ""), "Config file path (default: $ZACP_DATA/config.toml)")
	showVersion := flag.Bool("version", false, "Print version info and exit")

	// 自定义 --help/-h 输出（Go flag 包内置支持 -h/--help，但缺省输出较简略）：
	// 用英文写明各参数的回退链、优先级与示例，便于命令行直接查看。
	flag.CommandLine.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, `zacp - ACP (Agent Client Protocol) multi-agent web gateway

Usage:
  zacp [flags]

Flags:
  -addr string
        HTTP listen address in IP:PORT form (":8680" binds all interfaces).
        Fallback: --addr > ZACP_ADDR env > config server.addr > :8680
  -config string
        Path to the TOML config file.
        Fallback: --config > ZACP_CONFIG env > $ZACP_DATA/config.toml
  -data-dir string
        ZACP_DATA state root: config.toml and data (database, logs) live here.
        Fallback: --data-dir > ZACP_DATA env > ~/.zacp
  -h, -help
        Show this help and exit.
  -version
        Print version info (version, commit, build time) and exit.

Precedence (high -> low): command-line flags > ZACP_* env vars > TOML config > built-in defaults.

Examples:
  zacp                                     # listen :8680, state in ~/.zacp
  zacp --addr 127.0.0.1:9000               # listen on loopback port 9000
  zacp --data-dir /var/lib/zacp --config /etc/zacp/config.toml

`)
	}
	flag.Parse()

	// --version 仅打印版本信息后退出，不初始化任何资源（不需要 ZACP_DATA/数据库）
	if *showVersion {
		fmt.Println(version.String())
		os.Exit(0)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// 确保 ZACP_DATA 状态根目录存在：--data-dir 显式传入时优先，否则按环境变量/默认解析
	homeDir, err := config.EnsureHomeDir(*dataDir)
	if err != nil {
		log.Error("failed to ensure home dir", "err", err)
		os.Exit(1)
	}
	log.Info("ZACP_DATA", "path", homeDir)

	// 加载配置
	cfg, err := config.Load(homeDir, *configPath)
	if err != nil {
		log.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	log.Info("config loaded", "agents", len(cfg.Agents), "autoApprove", cfg.Session.AutoApprove)

	// 解析配置文件绝对路径（设置页写回 [[agents]] 用）：
	// --config / ZACP_CONFIG 显式指定时以其为准，否则默认 $ZACP_DATA/config.toml
	agentCfgPath := *configPath
	if agentCfgPath == "" {
		agentCfgPath = filepath.Join(homeDir, "config.toml")
	}
	if abs, err := filepath.Abs(agentCfgPath); err == nil {
		agentCfgPath = abs
	}

	// 合成最终监听地址：--addr > ZACP_ADDR 环境变量 > TOML server.addr > :8680
	listenAddr := *addr
	if listenAddr == "" {
		listenAddr = os.Getenv("ZACP_ADDR")
	}
	if listenAddr == "" {
		listenAddr = cfg.Server.Addr
	}
	if listenAddr == "" {
		listenAddr = ":8680"
	}

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

	// 启动清理：物理删除历史软删的空草稿会话（隐式 /new 探测产生的临时会话，
	// 无恢复入口也无保留价值）。清理失败仅告警，不阻塞启动。
	if purged, err := sessionRepo.PurgeSoftDeletedDrafts(); err != nil {
		log.Warn("failed to purge soft-deleted draft sessions", "err", err)
	} else if purged > 0 {
		log.Info("purged soft-deleted draft sessions", "count", purged)
	}

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
	// 注入「prompt 开始执行」钩子：排队门闩获取成功（真正执行）时才注册
	// 该会话的事件回调 + 广播 turn.started，排队期间不注册——
	// 执行中会话的流式事件不串台（同 agent 排队/跨 agent 并行场景，
	// 见 manager.promptGate 与 EventBridge.SetupEventCallback/OnPromptStarted）。
	mgr.SetPromptStartedHook(func(agentID, sessionID string) {
		eventBridge.OnPromptStarted(agentID, sessionID)
	})
	log.Info("event bridge created")

	// 创建 Service
	workspaceSvc := service.NewWorkspaceService(workspaceRepo)
	sessionSvc := service.NewSessionService(workspaceRepo, sessionRepo, messageRepo, mgr, cfg.Session.DefaultCwd)
	// REST 路径（SendMessage/SetConfigOption）重建 ACP 会话后，同样迁移 WS 订阅
	//（与 ws bridge 的 prompt 路径一致：前端订阅旧 id 时，重建后的广播不丢失）。
	sessionSvc.OnSessionRebuilt = func(oldID, newID string) {
		wsHandler.RebindSession(oldID, newID)
	}
	fileSvc := service.NewFileService(workspaceRepo, cfg.Session.DefaultCwd)

	// 创建 Handler
	workspaceHandler := handlers.NewWorkspaceHandler(workspaceSvc)
	sessionHandler := handlers.NewSessionHandler(sessionSvc, eventBridge)
	chatHandler := &handlers.ChatHandler{Mgr: mgr}
	fileHandler := handlers.NewFileHandler(fileSvc)
	agentManageHandler := &handlers.AgentManageHandler{
		Mgr:        mgr,
		ConfigPath: agentCfgPath,
	}

	// gin mode 优先级：GIN_MODE 环境变量（gin 包 init 已读取）> TOML server.mode > 默认 debug。
	// GIN_MODE 显式设置时以环境变量为准（不覆盖）；否则以配置为准。
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(cfg.Server.Mode)
	}

	engine := router.New(workspaceHandler, sessionHandler, chatHandler, fileHandler, agentManageHandler, wsHandler, eventBridge)

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

	log.Info("HTTP listening", "addr", listenAddr)
	log.Info("try: curl -s http://" + normalizeAddr(listenAddr) + "/api/v1/agents")
	log.Info("websocket endpoint: ws://" + normalizeAddr(listenAddr) + "/api/v1/ws")

	// 启动提示：向 stdout 打印用户可直接访问的 Web UI 地址（英文提示，便于复制到浏览器）。
	// 与 slog 日志（默认输出到 stderr）分开：用户重定向日志到文件时，访问地址仍保留在终端。
	// 提示用回环地址（normalizeAddr 已把 0.0.0.0/[::] 等通配监听转成本机可访问地址），
	// 无论配置里写的是 :8680 还是 0.0.0.0:8680，用户都能直接打开。
	fmt.Printf("\nzacp is running at:  http://%s/\n", normalizeAddr(listenAddr))
	fmt.Println("Press Ctrl+C to stop.")
	if err := engine.Run(listenAddr); err != nil {
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

// normalizeAddr 把监听地址转成可拼 URL 的 host:port 形式（仅用于提示，不影响实际监听）：
// ":8680" → "127.0.0.1:8680"；"127.0.0.1:8680" / "192.168.x.x:8680" → 原样；
// "0.0.0.0:8680" → "127.0.0.1:8680"、"[::]:8680" → "[::1]:8680"
// （绑定所有网卡时回环地址本机一定可访问，作为提示 URL 最稳妥）。
func normalizeAddr(addr string) string {
	switch {
	case addr == "":
		return "127.0.0.1:8680"
	case addr[0] == ':':
		return "127.0.0.1" + addr
	case strings.HasPrefix(addr, "0.0.0.0:"):
		return "127.0.0.1" + addr[len("0.0.0.0"):]
	case strings.HasPrefix(addr, "[::]:"):
		return "[::1]" + addr[len("[::]"):]
	default:
		return addr
	}
}
