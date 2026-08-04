/**
 * 与后端契约对齐的业务模型（字段 = backend/internal/model 的 json tag，camelCase）。
 * 后端变更时同步此处，避免静默字段漂移。
 */

/** Agent 状态（GET /api/v1/agents → `{ agents: AgentStatus[] }`） */
export interface Agent {
  agentId: string
  name: string
  running: boolean
}

/** 工作目录（GET /api/v1/workspaces → `{ workspaces: Workspace[] }`） */
export interface Workspace {
  id: number
  path: string
  name?: string
  /** 是否为 config session.default_cwd 对应默认工作区 */
  isDefault: boolean
  /** 归档后侧栏隐藏，数据保留 */
  archived: boolean
  lastUsed: string
  createdAt: string
  updatedAt: string
}

/** 会话状态（后端 model.SessionStatus） */
export type SessionStatus = 'active' | 'closed' | 'error'

/** 会话（GET /api/v1/sessions → `{ sessions: ChatSession[] }`；workspace 由后端预加载） */
export interface ChatSession {
  id: number
  workspaceId: number
  agentId: string
  acpSessionId?: string
  title: string
  status: SessionStatus
  createdAt: string
  updatedAt: string
  workspace?: Workspace
}

/** 消息角色（后端 model.Message.Role） */
export type MessageRole = 'user' | 'assistant' | 'system'

/** 消息（GET /api/v1/sessions/:id/messages → `{ messages, total, limit, offset }`） */
export interface ChatMessage {
  id: number
  sessionId: number
  role: MessageRole
  content: string
  /** 完整事件 JSON（工具调用等），P0-P1 未消费，P3 渲染工具卡片用 */
  events?: string
  createdAt: string
}

/** 分页消息响应 */
export interface MessagePage {
  messages: ChatMessage[]
  total: number
  limit: number
  offset: number
}

/** 会话配置项可选值（下拉项，对齐后端 model.ConfigOptionValueDTO） */
export interface ConfigOptionValue {
  value: string
  name: string
  description?: string
}

/**
 * 会话配置项（模型 / 思考强度 / mode 等，agent 下发的 configOptions）。
 * 对齐后端 model.ConfigOptionDTO；select 型可下拉选择，boolean 型为开关。
 */
export interface ConfigOption {
  id: string
  name: string
  description?: string
  /** 语义分类：model | mode | thought_level | ... */
  category?: string
  /** select | boolean */
  type: string
  currentValue: string | boolean
  options?: ConfigOptionValue[]
}
