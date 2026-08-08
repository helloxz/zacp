/**
 * WebSocket 协议类型（对齐 backend/internal/ws/protocol.go 与 bridge.go）。
 * 注意：prompt/cancel 的 sessionId 是 **ACP session id**（session.acpSessionId），
 * 不是 DB 的 uint id；agentId 用于无绑定连接动态绑定。
 */
import type { AvailableCommand, ConfigOption } from '@/types/models'

/** 客户端 → 服务端消息 */
export type WsClientMessage =
  | { type: 'prompt'; sessionId: string; agentId: string; message: string }
  | { type: 'cancel'; sessionId: string; agentId: string }
  | { type: 'permission'; permissionId: string; optionId: string }
  | { type: 'ping' }

/** 计划任务步骤（ACP plan 事件 entries 项；对齐 client.PlanStep） */
export interface PlanStep {
  content: string
  priority?: string
  status: string
}

/** 执行计划（TODO 列表；对齐 client.Plan；ACP 整体替换语义，每次携带完整条目） */
export interface Plan {
  entries: PlanStep[]
}

/** ACP 事件（对齐 ws/bridge.go handleEvent 的 wsEvent 字段） */
export interface WsEvent {
  /** agent_message | agent_thought | user_message | tool_call | tool_call_update | plan | other */
  type: string
  text?: string
  title?: string
  status?: string
  toolId?: string
  /** 工具调用入参（tool_call / tool_call_update 事件携带，可能是大 JSON） */
  input?: unknown
  /** 工具调用出参（tool_call / tool_call_update 事件携带，可能是大 JSON） */
  output?: unknown
  /** 执行计划（plan 事件携带；整体替换语义） */
  plan?: Plan
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

/** 服务端 → 客户端消息。
 * 除 session.ready/pong 外，广播类消息均携带 sessionId（**ACP session id**）：
 * 同一 WS 连接可同时订阅多个会话；全局三槽位排队/并行时，前端按 sessionId
 * 把事件路由到对应 DB 会话的流式槽位，避免串台。 */
export type WsServerMessage =
  | { type: 'session.ready'; sessionId?: string; agentId?: string }
  | { type: 'event'; sessionId?: string; event: WsEvent }
  | { type: 'turn.started'; sessionId?: string }
  | { type: 'turn.done'; sessionId?: string; reply?: string; stopReason?: string }
  | {
      type: 'permission.request'
      sessionId?: string
      permissionId?: string
      toolCall?: PermissionToolCall
      options?: PermissionOption[]
    }
  | { type: 'configOptions'; sessionId?: string; configOptions?: ConfigOption[] }
  | { type: 'slashCommands'; sessionId?: string; slashCommands?: AvailableCommand[] }
  | { type: 'sessionInfo'; sessionId?: string; sessionInfo?: { title?: string } }
  | {
      type: 'session.recovered'
      oldSessionId?: string
      newSessionId?: string
    }
  | { type: 'error'; sessionId?: string; code?: string; message?: string }
  | { type: 'pong' }
