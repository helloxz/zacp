package ws

import (
	"context"
	"log/slog"

	"github.com/zacp/zacp/internal/acp/client"
	"github.com/zacp/zacp/internal/acp/manager"
)

// EventBridge 将 ACP 事件桥接到 WebSocket
type EventBridge struct {
	handler *Handler
	manager *manager.Manager
	log     *slog.Logger
}

// NewEventBridge 创建事件桥接器
func NewEventBridge(handler *Handler, mgr *manager.Manager, log *slog.Logger) *EventBridge {
	return &EventBridge{
		handler: handler,
		manager: mgr,
		log:     log,
	}
}

// SetupEventCallback 为 Agent 连接设置事件回调
func (b *EventBridge) SetupEventCallback(agentID, sessionID string) error {
	// 获取 Agent 的 Bridge
	bridge, err := b.manager.GetBridge(agentID)
	if err != nil {
		return err
	}

	// 设置事件回调，将 ACP 事件转发到 WebSocket
	bridge.SetOnEvent(func(event client.Event) {
		b.handleEvent(sessionID, event)
	})

	b.log.Info("event callback setup for agent", "agentID", agentID, "sessionID", sessionID)
	return nil
}

// handleEvent 处理 ACP 事件并广播到 WebSocket
func (b *EventBridge) handleEvent(sessionID string, event client.Event) {
	// 将 ACP 事件转换为 WebSocket 事件
	wsEvent := map[string]interface{}{
		"type":    event.Type,
		"text":    event.Text,
		"title":   event.Title,
		"status":  event.Status,
		"toolId":  event.ToolID,
	}

	// 广播事件到该会话的所有连接
	b.handler.BroadcastEvent(sessionID, wsEvent)
}

// HandlePrompt 处理 WebSocket 的 prompt 消息
func (b *EventBridge) HandlePrompt(ctx context.Context, sessionID, agentID, message string) error {
	// 调用 manager 发送 prompt
	result, err := b.manager.Prompt(ctx, agentID, sessionID, message)
	if err != nil {
		return err
	}

	// 广播轮次完成消息
	b.handler.BroadcastTurnDone(sessionID, result.Reply, result.StopReason)

	return nil
}

// HandleCancel 处理 WebSocket 的 cancel 消息
func (b *EventBridge) HandleCancel(ctx context.Context, sessionID, agentID string) error {
	return b.manager.Cancel(ctx, agentID, sessionID)
}
