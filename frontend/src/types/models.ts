/**
 * 与后端契约对齐的业务模型（字段 = backend/internal/model 的 json tag，camelCase）。
 * 后端变更时同步此处，避免静默字段漂移。
 */

/** 服务端版本信息（GET /api/v1/version；由构建时 -ldflags 注入，来源 frontend/package.json） */
export interface VersionInfo {
  version: string
  commit: string
  buildTime: string
}

/** Agent 状态（GET /api/v1/agents → `{ agents: AgentStatus[] }`） */
export interface Agent {
  agentId: string
  name: string
  running: boolean
}

/**
 * 设置页智能体目录条目（GET /api/v1/agents/manage → `{ agents: ManageAgent[] }`）。
 * 来源为配置 [[agents]] + 后端内置目录合并（配置优先），含已停用与未安装项。
 */
export interface ManageAgent {
  agentId: string
  name: string
  /** 启动命令（如 reasonix / qoderclicn / grok） */
  command: string
  /** 配置中是否启用 */
  enabled: boolean
  /** 本机是否已安装（后端 which/文件存在性检测，尽力而为） */
  installed: boolean
  /** "config" = 来自用户配置；"builtin" = 后端内置模板（未写入配置） */
  source: 'config' | 'builtin'
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

/**
 * 文件树条目（GET /api/v1/workspaces/:id/files → `{ path, entries: FileEntry[] }`）
 * path 为相对工作区根的路径（`/` 分隔）。
 */
export interface FileEntry {
  name: string
  path: string
  isDir: boolean
  size?: number
  mimeType?: string
}

/**
 * 文本文件内容（GET/PUT /api/v1/workspaces/:id/files/content，编辑器用）。
 * mtimeUnixMs 为打开时记录的 mtime（毫秒），保存时回传做乐观锁比对。
 */
export interface FileContent {
  path: string
  content: string
  size: number
  mtimeUnixMs: number
}

/** GET /api/v1/workspaces/:id/git/status 返回的 Git 状态摘要 */
export interface GitStatus {
  gitInstalled: boolean
  isRepository: boolean
  summary: GitSummary
  files: GitChange[]
  truncated: boolean
  hiddenCount: number
}

export interface GitSummary {
  changed: number
  staged: number
  unstaged: number
  untracked: number
  conflicted: number
}

export interface GitChange {
  path: string
  originalPath?: string
  status: 'modified' | 'added' | 'deleted' | 'renamed' | 'copied' | 'untracked' | 'conflicted' | 'changed'
  indexStatus: string
  worktreeStatus: string
}

/**
 * 目录浏览条目（GET /api/v1/fs/directories → `{ path, parent, entries }`，仅文件夹）。
 * path 为绝对路径，可直接作为「创建项目」路径或继续浏览的入参。
 */
export interface DirectoryEntry {
  name: string
  path: string
}

/** 目录浏览结果（新建项目弹窗数据源） */
export interface DirectoryList {
  /** 当前目录绝对路径 */
  path: string
  /** 上级目录绝对路径；根目录时为 ""（据此禁用「返回上级」） */
  parent: string
  entries: DirectoryEntry[]
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
  /** 草稿标记：隐式 session/new 探测创建的会话为 true，不进侧栏；发首条 prompt 转正后置 false */
  isDraft: boolean
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
  /** 思维/推理文本（仅流式本地消息；来自 ACP agent_thought 事件，DB 不持久化） */
  reasoning?: string
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

/**
 * 可用 / 命令（agent 经 ACP available_commands_update 通告，如 init/plan）。
 * 对齐后端 model.AvailableCommandDTO；输入框以 "/" 开头时展示候选面板。
 */
export interface AvailableCommand {
  /** 命令名（不含斜杠，如 "init"） */
  name: string
  description?: string
  /** 参数提示（如 "<task>"），供前端展示 */
  inputHint?: string
}
