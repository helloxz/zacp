// Command chat is a minimal terminal REPL against reasonix --acp.
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
)

func main() {
	command := flag.String("command", envOr("REASONIX_BIN", ""), "Agent binary (default: auto-detect reasonix)")
	cwd := flag.String("cwd", envOr("ZACP_CWD", ""), "Agent working directory")
	autoApprove := flag.Bool("yolo", true, "Auto-approve permission requests")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	mgr := manager.New(log, manager.Config{
		Command:     *command,
		Args:        []string{"--acp"},
		Cwd:         *cwd,
		AutoApprove: *autoApprove,
	})
	defer mgr.Close()

	// Live stream agent text to stdout while Prompt blocks.
	mgr.Bridge().SetOnEvent(func(e client.Event) {
		switch e.Type {
		case "agent_message":
			fmt.Fprint(os.Stdout, e.Text)
		case "agent_thought":
			// Keep quieter by default; uncomment to see thoughts:
			// fmt.Fprintf(os.Stdout, "\n[thought] %s", e.Text)
		case "tool_call":
			fmt.Fprintf(os.Stdout, "\n🔧 %s (%s)\n", e.Title, e.Status)
		case "tool_call_update":
			fmt.Fprintf(os.Stdout, "🔧 %s -> %s\n", e.ToolID, e.Status)
		}
	})

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

	startCtx, startCancel := context.WithTimeout(ctx, 60*time.Second)
	if err := mgr.Start(startCtx); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start reasonix: %v\n", err)
		fmt.Fprintf(os.Stderr, "hint: install reasonix and ensure PATH, or set REASONIX_BIN=/path/to/reasonix\n")
		os.Exit(1)
	}
	startCancel()

	fmt.Printf("connected to reasonix ACP  session=%s\n", mgr.SessionID())
	fmt.Println("type a message and Enter to send.  commands: :exit  :cancel")
	fmt.Println()

	sc := bufio.NewScanner(os.Stdin)
	// Allow long pastes.
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
			if err := mgr.Cancel(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "cancel error: %v\n", err)
			}
			continue
		}

		turnCtx, turnCancel := context.WithTimeout(ctx, 10*time.Minute)
		res, err := mgr.Chat(turnCtx, line)
		turnCancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
			continue
		}
		// If stream already printed text, just print footer.
		if strings.TrimSpace(res.Reply) == "" {
			fmt.Print("\n(empty reply)")
		}
		fmt.Printf("\n— stop=%s  %dms\n\n", res.StopReason, res.DurationMs)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
