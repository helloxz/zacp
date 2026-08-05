// Command chat 是一个最小化的终端 REPL，用于测试 ACP agent。
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/zacp/zacp/internal/acp/client"
	"github.com/zacp/zacp/internal/acp/manager"
	"github.com/zacp/zacp/internal/acp/providers"
	"github.com/zacp/zacp/internal/config"
)

func main() {
	agentID := flag.String("agent", "reasonix", "Agent ID to use")
	cwd := flag.String("cwd", "", "Agent working directory")
	autoApprove := flag.Bool("yolo", true, "Auto-approve permission requests")
	configPath := flag.String("config", "", "Config file path")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// 加载配置
	homeDir, err := config.EnsureHomeDir("")
	if err != nil {
		log.Error("failed to ensure home dir", "err", err)
		os.Exit(1)
	}

	cfg, err := config.Load(homeDir, *configPath)
	if err != nil {
		log.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	// 创建 Provider Registry
	registry, err := providers.NewRegistry(cfg.Agents)
	if err != nil {
		log.Error("failed to create provider registry", "err", err)
		os.Exit(1)
	}

	// 创建 Manager
	mgr := manager.New(log, manager.Config{
		Registry:    registry,
		AutoApprove: *autoApprove,
		DefaultCwd:  *cwd,
	})
	defer mgr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
		fmt.Fprintln(os.Stderr, "\n(interrupted, exiting…)")
		_ = mgr.Close()
		cancel()
		os.Exit(130)
	}()

	// 启动指定 agent
	startCtx, startCancel := context.WithTimeout(ctx, 60*time.Second)
	if err := mgr.StartAgent(startCtx, *agentID); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start agent '%s': %v\n", *agentID, err)
		os.Exit(1)
	}
	startCancel()

	// 获取 bridge 用于流式输出
	bridge, err := mgr.GetBridge(*agentID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get bridge: %v\n", err)
		os.Exit(1)
	}

	// 设置事件回调，实时输出 agent 响应
	bridge.SetOnEvent(func(e client.Event) {
		switch e.Type {
		case "agent_message":
			fmt.Fprint(os.Stdout, e.Text)
		case "tool_call":
			fmt.Fprintf(os.Stdout, "\n🔧 %s (%s)\n", e.Title, e.Status)
		case "tool_call_update":
			fmt.Fprintf(os.Stdout, "🔧 %s -> %s\n", e.ToolID, e.Status)
		}
	})

	// 创建 session
	sessionID, _, err := mgr.CreateSession(ctx, *agentID, *cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create session: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("connected to agent '%s' session=%s\n", *agentID, sessionID)
	fmt.Println("type a message and Enter to send.  commands: :exit  :cancel")
	fmt.Println()

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for {
		fmt.Print("> ")
		if !sc.Scan() {
			break
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		switch line {
		case ":exit", ":quit", "exit", "quit":
			return
		case ":cancel":
			if err := mgr.Cancel(ctx, *agentID, sessionID); err != nil {
				fmt.Fprintf(os.Stderr, "cancel error: %v\n", err)
			}
			continue
		}

		turnCtx, turnCancel := context.WithTimeout(ctx, 10*time.Minute)
		res, err := mgr.Prompt(turnCtx, *agentID, sessionID, line)
		turnCancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
			continue
		}
		if strings.TrimSpace(res.Reply) == "" {
			fmt.Print("\n(empty reply)")
		}
		fmt.Printf("\n— stop=%s  %dms\n\n", res.StopReason, res.DurationMs)
	}
}
