/**
 * WebSocket 协议类型（对齐 backend/internal/ws/protocol.go 与 bridge.go）。
 * 注意：prompt/cancel 的 sessionId 是 **ACP session id**（session.acpSessionId），
 * 不是 DB 的 uint id；agentId 用于无绑定连接动态绑定。
 */

/** 客户端 → 服务端消息 */
export type WsClientMessage =
  | { type: 'prompt'; sessionId: string; agentId: string; message: string }
  | { type: 'cancel'; sessionId: string; agentId: string }
  | { type: 'permission'; permissionId: string; optionId: string }
  | { type: 'ping' }

/** ACP 事件（对齐 ws/bridge.go handleEvent 的 wsEvent 字段） */
export interface WsEvent {
  /** agent_message | agent_thought | user_message | tool_call | tool_call_update | plan | other */
  type: string
  text?: string
  title?: string
  status?: string
  toolId?: string
}

/** 权限选项（后端广播 permission.request 的 options 结构） */
export interface PermissionOption {
  optionId: string
  name: string
  kind?: string
}

/** 权限请求中的工具调用信息（后端显式挑字段序列化） */
export interface PermissionToolCall {
  toolCallId: string
  title?: string
  status?: string
  rawInput?: unknown
}

/** 服务端 → 客户端消息 */
export type WsServerMessage =
  | { type: 'session.ready'; sessionId?: string; agentId?: string }
  | { type: 'event'; event: WsEvent }
  | { type: 'turn.done'; reply?: string; stopReason?: string }
  | {
      type: 'permission.request'
      permissionId?: string
      toolCall?: PermissionToolCall
      options?: PermissionOption[]
    }
  | { type: 'error'; code?: string; message?: string }
  | { type: 'pong' }
