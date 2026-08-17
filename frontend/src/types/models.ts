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
  /** 后端是否登记了该智能体的配置文件路径（前端据此显示「编辑配置」按钮） */
  hasConfigFiles: boolean
}

/**
 * POST /api/v1/agents — 添加自定义智能体的表单数据。
 * args 为原始参数字符串（如 `--model "gpt-4o" --acp`），由后端引号感知切分为参数数组。
 */
export interface AddAgentInput {
  name: string
  id: string
  command: string
  args: string
}

/** POST /api/v1/agents 成功响应的智能体摘要（source 恒为 "config"）。 */
export interface AddedAgent {
  agentId: string
  name: string
  command: string
  enabled: boolean
  source: 'config'
}

/**
 * 智能体配置文件条目（GET /api/v1/agents/:agentId/config-files → `{ files }`）。
 * 后端已按 HOME 展开检查存在性，只返回真实存在的文件；path 为 `~/...` 相对形式。
 */
export interface AgentConfigFile {
  path: string
  name: string
  /** 扩展名（小写，不含点；用于编辑器语言选择） */
  ext: string
}

/** 智能体配置文件内容（GET/PUT /api/v1/agents/:agentId/config-files/content）。 */
export interface AgentConfigContent {
  path: string
  name: string
  content: string
  size: number
  /** 文件 mtime（毫秒），保存时回传做乐观锁比对 */
  mtimeUnixMs: number
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

/** 可由后端启动的本地工具（GET /api/v1/tools）。 */
export interface ExternalTool {
  id: string
  label: string
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
  /** 待推送提交数；null/undefined = 无 upstream 或未知（前端隐藏「推送 (n)」徽标） */
  ahead?: number | null
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

/** POST /api/v1/workspaces/:id/git/commit 请求体：仅提交选中的文件（可选 push） */
export interface GitCommitRequest {
  message: string
  files: string[]
  push: boolean
}

/**
 * POST /api/v1/workspaces/:id/git/commit 结果。
 * committed=true 表示 commit 已成功；push=true 且推送失败时 pushed=false 并带 pushError，
 * 前端展示「已提交但推送失败」并可调用 /git/push 重试。
 */
export interface GitCommitResult {
  committed: boolean
  commitHash?: string
  pushed: boolean
  pushError?: string
}

/** POST /api/v1/workspaces/:id/git/push 结果（重试推送） */
export interface GitPushResult {
  pushed: boolean
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

/** 工具详情映射（messages.toolDetails JSON 解析后；toolId → 最终入参/出参，自后端 v6 起提供） */
export interface ToolDetailsMap {
  [toolId: string]: {
    input?: unknown
    output?: unknown
  }
}

/** 消息（GET /api/v1/sessions/:id/messages → `{ messages, total, limit, offset }`） */
export interface ChatMessage {
  id: number
  sessionId: number
  role: MessageRole
  content: string
  /** 思维/推理文本：
   *  - 流式期间：store 实时追加（来自 ACP agent_thought 事件）；
   *  - turn 结束后：由流式占位消息转移而来（缓存刚展示过的思考过程）；
   *  - 刷新/翻页加载的历史消息一般无此字段：列表接口已把 events 里的
   *    agent_thought text 置空瘦身，展开面板时经 /thoughts 接口按需加载 */
  reasoning?: string
  /** 完整事件 JSON（工具调用等），P0-P1 未消费，P3 渲染工具卡片用 */
  events?: string
  /** 工具详情 JSON（toolId → {input, output}，每工具最终一份）；与 events 互补：
   *  v6 起 events 已剥离 input/output，展开工具卡详情优先读本字段，缺失时回退 events 内嵌值 */
  toolDetails?: string
  /**
   * 前端私有标记：流式占位消息已转正（保留负 id 以稳定 v-for key，避免
   * turn.done 后整列 DOM 重建导致滚动跳动）。DB 消息无此字段。
   */
  streamFinalized?: boolean

  createdAt: string
}

/** 单条消息的思考过程响应（GET /sessions/:id/messages/:messageId/thoughts；
 *  列表接口已置空瘦身，前端展开思考过程面板时按需加载） */
export interface MessageThoughts {
  reasoning: string
}

/** 分页消息响应 */
export interface MessagePage {
  messages: ChatMessage[]
  total: number
  limit: number
  offset: number
}

/** 增量消息响应（afterId 模式；仅包含指定消息之后新增的记录） */
export interface MessageUpdates {
  messages: ChatMessage[]
  afterId: number
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
