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
	"github.com/zacp/zacp/internal/api/handlers"
	"github.com/zacp/zacp/internal/api/router"
)

func main() {
	addr := flag.String("addr", envOr("ZACP_ADDR", ":8080"), "HTTP listen address")
	command := flag.String("command", envOr("REASONIX_BIN", ""), "Agent binary (default: auto-detect reasonix)")
	cwd := flag.String("cwd", envOr("ZACP_CWD", ""), "Agent working directory (default: process cwd)")
	autoApprove := flag.Bool("yolo", true, "Auto-approve agent permission requests")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := manager.Config{
		Command:     *command,
		Args:        []string{"--acp"},
		Cwd:         *cwd,
		AutoApprove: *autoApprove,
	}
	mgr := manager.New(log, cfg)
	defer mgr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	if err := mgr.Start(ctx); err != nil {
		log.Error("failed to start reasonix ACP", "err", err)
		os.Exit(1)
	}
	cancel()
	log.Info("reasonix ACP ready", "sessionId", mgr.SessionID())

	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	h := &handlers.ChatHandler{Mgr: mgr}
	engine := router.New(h)

	// Graceful shutdown on signal.
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		sig := <-ch
		log.Info("shutting down", "signal", sig.String())
		_ = mgr.Close()
		os.Exit(0)
	}()

	log.Info("HTTP listening", "addr", *addr)
	log.Info("try: curl -s http://127.0.0.1" + normalizeAddr(*addr) + `/api/v1/chat -H 'Content-Type: application/json' -d '{"message":"你好，用一句话介绍你自己"}'`)
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
