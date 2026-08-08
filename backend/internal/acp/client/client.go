// Package client implements an ACP Client suitable for Web/API demos.
package client

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"
)

// PlanStep 表示执行计划中的一个任务条目（对应 ACP PlanEntry）。
type PlanStep struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status"`
}

// Plan 是 agent 的执行计划（TODO 列表）。
// ACP 语义为整体替换：每次 plan 事件携带完整条目列表，前端直接覆盖展示。
type Plan struct {
	Entries []PlanStep `json:"entries"`
}

// Event is a simplified session update for API/CLI consumers.
type Event struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	Title   string `json:"title,omitempty"`
	Status  string `json:"status,omitempty"`
	ToolID  string `json:"toolId,omitempty"`
	RawKind string `json:"rawKind,omitempty"`
	// SessionID 为事件所属的 ACP 会话 id（来自 SDK session/update 通知的 SessionId，
	// v1/v2 通知均自带）。用于 WS 多会话场景：同一 agent 排队、跨 agent 并行时，
	// 事件按会话路由，避免「执行中会话 A 的事件串到已排队的会话 B」。
	SessionID string `json:"sessionId,omitempty"`
	// Input/Output 为工具调用的入参/出参（对应 ACP 的 RawInput/RawOutput），
	// 供前端在消息里展开查看详情；nil 时省略不序列化。
	Input  any `json:"input,omitempty"`
	Output any `json:"output,omitempty"`
	// Plan 为 agent 执行计划（仅 plan 事件携带；整体替换语义，见 Plan 注释）
	Plan *Plan `json:"plan,omitempty"`
}

// Bridge is an ACP Client that buffers session updates and forwards live events.
// 事件缓存按 ACP session id 隔离：同一 Agent 连接上的多个 prompt 并发执行时，
// 一个 session 的回复和工具事件不能覆盖另一个 session。
type Bridge struct {
	log         *slog.Logger
	autoApprove bool

	mu              sync.Mutex
	eventsBySession map[string][]Event
	// onEvent is optional live callback (e.g. print to stdout).
	onEvent func(Event)
	// configOptionsHandler 接收 agent 经 session/update 通知下发的 configOptions
	//（模型/思考强度/mode 等，可能不在 session/new 响应里，而在后续通知中）。
	// 第一个参数为 SDK 通知里的 ACP session id。
	configOptionsHandler func(sessionID string, opts []acp.SessionConfigOption)
	// availableCommandsHandler 接收 agent 经 session/update 通知下发的可用 / 命令。
	availableCommandsHandler func(sessionID string, cmds []acp.AvailableCommand)
	// sessionInfoHandler 接收 agent 经 session/update 通知下发的会话信息。
	sessionInfoHandler func(sessionID string, info acp.SessionSessionInfoUpdate)
	// permissionHandler 将权限请求转发给外部（如 WebSocket 前端交互式选择）。
	permissionHandler func(acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
}

// Ensure Bridge implements acp.Client.
var _ acp.Client = (*Bridge)(nil)

// New creates a demo ACP client bridge.
func New(log *slog.Logger, autoApprove bool) *Bridge {
	if log == nil {
		log = slog.Default()
	}
	return &Bridge{
		log:             log,
		autoApprove:     autoApprove,
		eventsBySession: make(map[string][]Event),
	}
}

// SetOnEvent sets a live event sink (optional).
func (b *Bridge) SetOnEvent(fn func(Event)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onEvent = fn
}

// SetPermissionHandler sets an interactive permission handler (optional).
// 当非 autoApprove 时，RequestPermission 会把请求交给该处理器（如转发 WebSocket 前端），
// 由用户选择后回传 outcome；未设置时维持默认行为（见 RequestPermission）。
func (b *Bridge) SetPermissionHandler(fn func(acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.permissionHandler = fn
}

// SetConfigOptionsHandler sets a live config-options sink (optional).
// 接收 agent 经 session/update 通知下发的配置项（可能不在 session/new 响应中）；
// 回调第一个参数为通知自带的 ACP session id。
func (b *Bridge) SetConfigOptionsHandler(fn func(sessionID string, opts []acp.SessionConfigOption)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.configOptionsHandler = fn
}

// SetAvailableCommandsHandler sets a live slash-commands sink (optional).
// 接收 agent 经 session/update 的 available_commands_update 通知下发的可用 / 命令列表；
// 回调第一个参数为通知自带的 ACP session id。
func (b *Bridge) SetAvailableCommandsHandler(fn func(sessionID string, cmds []acp.AvailableCommand)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.availableCommandsHandler = fn
}

// SetSessionInfoHandler sets a live session-info sink (optional).
// 接收 agent 经 session/update 的 session_info_update 通知下发的会话信息
// （AI 总结标题、最近活动时间等）；回调第一个参数为通知自带的 ACP session id。
func (b *Bridge) SetSessionInfoHandler(fn func(sessionID string, info acp.SessionSessionInfoUpdate)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessionInfoHandler = fn
}

// ResetSession clears buffered events for one ACP session.
func (b *Bridge) ResetSession(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.eventsBySession, sessionID)
}

// Events returns a copy of buffered events for one ACP session.
func (b *Bridge) Events(sessionID string) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	events := b.eventsBySession[sessionID]
	out := make([]Event, len(events))
	copy(out, events)
	return out
}

// AgentText joins all agent message text chunks for one ACP session.
func (b *Bridge) AgentText(sessionID string) string {
	var sb strings.Builder
	for _, e := range b.Events(sessionID) {
		if e.Type == "agent_message" {
			sb.WriteString(e.Text)
		}
	}
	return sb.String()
}

func (b *Bridge) push(e Event) {
	b.mu.Lock()
	b.eventsBySession[e.SessionID] = append(b.eventsBySession[e.SessionID], e)
	fn := b.onEvent
	b.mu.Unlock()
	if fn != nil {
		fn(e)
	}
}

// RequestPermission implements acp.Client.
func (b *Bridge) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	b.mu.Lock()
	autoApprove := b.autoApprove
	handler := b.permissionHandler
	b.mu.Unlock()

	// 交互式权限：非自动批准且设置了处理器时，转发给外部（WebSocket 前端弹窗），
	// 由用户选择后回传 outcome；否则走默认（autoApprove 放行 / 无交互则取消）。
	if handler != nil && !autoApprove {
		return handler(params)
	}

	title := ""
	if params.ToolCall.Title != nil {
		title = *params.ToolCall.Title
	}
	b.log.Info("permission requested", "title", title, "options", len(params.Options), "autoApprove", autoApprove)

	if autoApprove {
		for _, o := range params.Options {
			if o.Kind == acp.PermissionOptionKindAllowOnce || o.Kind == acp.PermissionOptionKindAllowAlways {
				return acp.RequestPermissionResponse{
					Outcome: acp.RequestPermissionOutcome{
						Selected: &acp.RequestPermissionOutcomeSelected{OptionId: o.OptionId},
					},
				}, nil
			}
		}
		if len(params.Options) > 0 {
			return acp.RequestPermissionResponse{
				Outcome: acp.RequestPermissionOutcome{
					Selected: &acp.RequestPermissionOutcomeSelected{OptionId: params.Options[0].OptionId},
				},
			}, nil
		}
		return acp.RequestPermissionResponse{
			Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
		}, nil
	}

	// Non-auto: cancel (no interactive handler configured).
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
	}, nil
}

// SessionUpdate implements acp.Client.
func (b *Bridge) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	u := params.Update
	// 通知自带的 ACP session id：事件流按会话归属，WS 多会话（排队/并行）时路由不串台
	sid := string(params.SessionId)
	switch {
	case u.AgentMessageChunk != nil:
		if u.AgentMessageChunk.Content.Text != nil {
			b.push(Event{Type: "agent_message", Text: u.AgentMessageChunk.Content.Text.Text, SessionID: sid})
		}
	case u.AgentThoughtChunk != nil:
		if u.AgentThoughtChunk.Content.Text != nil {
			b.push(Event{Type: "agent_thought", Text: u.AgentThoughtChunk.Content.Text.Text, SessionID: sid})
		}
	case u.UserMessageChunk != nil:
		if u.UserMessageChunk.Content.Text != nil {
			b.push(Event{Type: "user_message", Text: u.UserMessageChunk.Content.Text.Text, SessionID: sid})
		}
	case u.ToolCall != nil:
		b.push(Event{
			Type:      "tool_call",
			Title:     u.ToolCall.Title,
			Status:    string(u.ToolCall.Status),
			ToolID:    string(u.ToolCall.ToolCallId),
			SessionID: sid,
			// 入参/出参透传给前端（可能是大 JSON，展示时由前端截断/滚动）
			Input:  u.ToolCall.RawInput,
			Output: u.ToolCall.RawOutput,
		})
	case u.ToolCallUpdate != nil:
		status := ""
		if u.ToolCallUpdate.Status != nil {
			status = string(*u.ToolCallUpdate.Status)
		}
		title := ""
		if u.ToolCallUpdate.Title != nil {
			title = *u.ToolCallUpdate.Title
		}
		b.push(Event{
			Type:      "tool_call_update",
			Title:     title,
			Status:    status,
			ToolID:    string(u.ToolCallUpdate.ToolCallId),
			SessionID: sid,
			// update 通知语义为替换：非 nil 才覆盖，保持与流式工具卡一致
			Input:  u.ToolCallUpdate.RawInput,
			Output: u.ToolCallUpdate.RawOutput,
		})
	case u.Plan != nil:
		// 执行计划通知：ACP 语义为整体替换（每次携带完整条目列表，见 SDK SessionUpdatePlan 注释）。
		// 转为独立 plan 事件透传：实时卡片直接覆盖，历史消息取最后一个 plan 事件即可。
		plan := &Plan{}
		for _, e := range u.Plan.Entries {
			plan.Entries = append(plan.Entries, PlanStep{
				Content:  e.Content,
				Priority: string(e.Priority),
				Status:   string(e.Status),
			})
		}
		b.push(Event{Type: "plan", Plan: plan, SessionID: sid})
	case u.ConfigOptionUpdate != nil:
		// 会话配置项通知（模型/思考强度/mode 等）：走独立处理器（不混入消息事件流）。
		// 通知自带 sessionId（v1/v2 均有），按会话分发，避免多会话并发时串到其它会话。
		b.mu.Lock()
		fn := b.configOptionsHandler
		b.mu.Unlock()
		if fn != nil {
			fn(string(params.SessionId), u.ConfigOptionUpdate.ConfigOptions)
		}
	case u.AvailableCommandsUpdate != nil:
		// 可用 / 命令通知（ACP available_commands_update）：走独立处理器，不混入消息事件流。
		// 与 configOptions 同理：命令列表可能在会话任意时刻（重新）下发，需落库 + 广播；
		// 同样使用通知自带的 sessionId 分发，保证会话归属正确。
		b.mu.Lock()
		fn := b.availableCommandsHandler
		b.mu.Unlock()
		if fn != nil {
			fn(string(params.SessionId), u.AvailableCommandsUpdate.AvailableCommands)
		}
	case u.SessionInfoUpdate != nil:
		// 会话信息通知（ACP session_info_update）：AI 总结标题等，走独立处理器，不混入消息事件流。
		// 与 configOptions 同理：通知自带 sessionId，按会话分发，保证会话归属正确。
		b.mu.Lock()
		fn := b.sessionInfoHandler
		b.mu.Unlock()
		if fn != nil {
			fn(string(params.SessionId), *u.SessionInfoUpdate)
		}
	default:
		b.push(Event{Type: "other", RawKind: "unknown", SessionID: sid})
	}
	return nil
}

// WriteTextFile implements acp.Client.
func (b *Bridge) WriteTextFile(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	if !filepath.IsAbs(params.Path) {
		return acp.WriteTextFileResponse{}, fmt.Errorf("path must be absolute: %s", params.Path)
	}
	if dir := filepath.Dir(params.Path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return acp.WriteTextFileResponse{}, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(params.Path, []byte(params.Content), 0o644); err != nil {
		return acp.WriteTextFileResponse{}, fmt.Errorf("write %s: %w", params.Path, err)
	}
	b.log.Info("wrote file", "path", params.Path, "bytes", len(params.Content))
	return acp.WriteTextFileResponse{}, nil
}

// ReadTextFile implements acp.Client.
func (b *Bridge) ReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	if !filepath.IsAbs(params.Path) {
		return acp.ReadTextFileResponse{}, fmt.Errorf("path must be absolute: %s", params.Path)
	}
	raw, err := os.ReadFile(params.Path)
	if err != nil {
		return acp.ReadTextFileResponse{}, fmt.Errorf("read %s: %w", params.Path, err)
	}
	content := string(raw)
	if params.Line != nil || params.Limit != nil {
		lines := strings.Split(content, "\n")
		start := 0
		if params.Line != nil && *params.Line > 0 {
			start = *params.Line - 1
			if start < 0 {
				start = 0
			}
			if start > len(lines) {
				start = len(lines)
			}
		}
		end := len(lines)
		if params.Limit != nil && *params.Limit > 0 && start+*params.Limit < end {
			end = start + *params.Limit
		}
		content = strings.Join(lines[start:end], "\n")
	}
	return acp.ReadTextFileResponse{Content: content}, nil
}

// CreateTerminal implements acp.Client (stub for demo).
func (b *Bridge) CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{TerminalId: "term-demo-1"}, nil
}

// KillTerminal implements acp.Client (stub).
func (b *Bridge) KillTerminal(ctx context.Context, params acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, nil
}

// TerminalOutput implements acp.Client (stub).
func (b *Bridge) TerminalOutput(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{Output: "", Truncated: false}, nil
}

// ReleaseTerminal implements acp.Client (stub).
func (b *Bridge) ReleaseTerminal(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, nil
}

// WaitForTerminalExit implements acp.Client (stub).
func (b *Bridge) WaitForTerminalExit(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, nil
}
