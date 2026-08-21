/**
 * HTTP / API 入口。
 *
 * 用法：
 * ```ts
 * import { http, ApiError, fetchAgents, fetchRecentSessions } from '@/api'
 * ```
 *
 * 基础域名由 `VITE_API_BASE_URL` 自动拼接（见 `@/config/env`），
 * 业务侧只需传 `/api/v1/...` 路径。
 */
export { http, request } from './http'
export { ApiError, type ApiErrorBody, type RequestOptions, type HttpMethod } from './types'

// ---------------------------------------------------------------------------
// 业务 API（P1 起使用；每个函数对应一个后端端点，返回解包后的数据）
// ---------------------------------------------------------------------------

import { http } from './http'
import { ApiError, type ApiErrorBody } from './types'
import { apiUrl } from '@/config/env'
import { readAuthToken } from '@/utils/authStorage'
import type {
  AddAgentInput,
  AddedAgent,
  Agent,
  AvailableCommand,
  ChatMessage,
  ChatSession,
  ConfigOption,
  DirectoryList,
  ExternalTool,
  FileContent,
  FileEntry,
  AgentConfigContent,
  AgentConfigFile,
  GitCommitRequest,
  GitCommitResult,
  GitPushResult,
  GitStatus,
  ManageAgent,
  MessagePage,
  MessageThoughts,
  MessageUpdates,
  VersionInfo,
  Workspace,
  ZliteChannel,
} from '@/types/models'

/** GET /api/v1/version — 服务端构建版本信息（设置页展示） */
export async function fetchVersion(): Promise<VersionInfo> {
  return http.get<VersionInfo>('/api/v1/version')
}

/** GET /api/v1/agents — Agent 状态列表 */
export async function fetchAgents(): Promise<Agent[]> {
  const data = await http.get<{ agents: Agent[] }>('/api/v1/agents')
  return data.agents
}

/**
 * GET /api/v1/agents/manage — 设置页智能体目录（配置 + 内置合并，含未安装项）。
 * 与 fetchAgents（运行时可用列表）语义不同，勿混用。
 */
export async function fetchManageAgents(): Promise<ManageAgent[]> {
  const data = await http.get<{ agents: ManageAgent[] }>('/api/v1/agents/manage')
  return data.agents
}

/** GET /api/v1/tools — 当前平台已安装且在白名单中的本地工具。 */
export async function fetchExternalTools(): Promise<ExternalTool[]> {
	const data = await http.get<{ tools: ExternalTool[] }>('/api/v1/tools')
	return data.tools
}

/**
 * PUT /api/v1/agents/:agentId — 设置页开关智能体（写 config.toml + 运行时热更新）。
 * 开启未安装的智能体会被后端拒绝（code: agent_not_installed）。
 */
export async function setAgentEnabled(
  agentId: string,
  enabled: boolean,
): Promise<void> {
  await http.put(`/api/v1/agents/${encodeURIComponent(agentId)}`, {
    body: { enabled },
  })
}

/**
 * POST /api/v1/agents — 添加自定义智能体（写 config.toml + 运行时热更新，默认启用）。
 * 后端校验：id 全局唯一（与配置及内置目录均大小写不敏感去重）、command 真实存在；
 * args 为原始参数字符串（如 `--model "gpt-4o"`），由后端按引号感知规则切分，
 * 前端不重复实现解析逻辑。
 * 失败错误码：agent_id_invalid / agent_id_exists / agent_command_not_found 等。
 */
export async function addAgent(input: AddAgentInput): Promise<AddedAgent> {
  const data = await http.post<{ agent: AddedAgent }>('/api/v1/agents', {
    body: input,
  })
  return data.agent
}

/**
 * DELETE /api/v1/agents/:agentId — 删除自定义智能体（从 config.toml 移除块 + 热更新停用）。
 * 仅配置来源（source=config）可删；内置智能体不在配置中，后端返回 400
 * （agent_builtin_not_deletable）；完全不存在返回 404（agent_not_found）。
 * 删除不可恢复；历史会话数据保留在数据库，不受影响。
 */
export async function deleteAgent(agentId: string): Promise<void> {
  await http.delete(`/api/v1/agents/${encodeURIComponent(agentId)}`)
}

/**
 * GET /api/v1/agents/:agentId/config-files — 该智能体真实存在的配置文件列表
 * （后端按 HOME 展开 + 存在性过滤；用于设置页「编辑配置」弹窗）。
 * 路径为 `~/...` 相对形式；列表为空说明文件暂不存在。
 */
export async function fetchAgentConfigFiles(
  agentId: string,
): Promise<AgentConfigFile[]> {
  const data = await http.get<{ files: AgentConfigFile[] }>(
    `/api/v1/agents/${encodeURIComponent(agentId)}/config-files`,
  )
  return data.files
}

/** GET /api/v1/agents/:agentId/config-files/content?path=... — 读取单个配置文件内容。 */
export async function fetchAgentConfigContent(
  agentId: string,
  path: string,
): Promise<AgentConfigContent> {
  return http.get<AgentConfigContent>(
    `/api/v1/agents/${encodeURIComponent(agentId)}/config-files/content?path=${encodeURIComponent(path)}`,
  )
}

/**
 * PUT /api/v1/agents/:agentId/config-files/content — 保存配置文件内容。
 * 携带 expectedMtime 做乐观锁：文件已被他处修改时返回 409（file_modified）；
 * 格式语法错误返回 400（invalid_syntax，message 带解析详情）。
 */
export async function saveAgentConfigContent(
  agentId: string,
  path: string,
  content: string,
  expectedMtime?: number,
): Promise<AgentConfigContent> {
  return http.put<AgentConfigContent>(
    `/api/v1/agents/${encodeURIComponent(agentId)}/config-files/content`,
    { body: { path, content, expectedMtime } },
  )
}

/**
 * GET /api/v1/agents/zlite/default-channel — 读取 zlite 默认渠道设置。
 * 文件不存在/未配置时返回默认值（type=openai.chat，其余为空），可直接回填表单。
 */
export async function fetchZliteDefaultChannel(): Promise<ZliteChannel> {
  return http.get<ZliteChannel>('/api/v1/agents/zlite/default-channel')
}

/**
 * PUT /api/v1/agents/zlite/default-channel — 保存 zlite 默认渠道设置。
 * 写回 ~/.zlite/config.toml 的 name='default' [[providers]] 块（api_key 固定引用
 * ${ZLITE_DEFAULT_API_KEY}）与 ~/.zlite/.env（ZLITE_DEFAULT_API_KEY，为空则删除）。
 * 失败错误码：bad_request / invalid_zlite_channel / write_zlite_channel。
 */
export async function saveZliteDefaultChannel(
  channel: ZliteChannel,
): Promise<void> {
  await http.put('/api/v1/agents/zlite/default-channel', { body: channel })
}

/**
 * POST /api/v1/agents/zlite/install — 安装 zlite 官方智能体（远程脚本，最长 5 分钟）。
 * 调用前前端必须经确认弹窗（后端不做二次确认，只做幂等/并发/超时防护）。
 * 失败错误码：agent_already_installed / unsupported_platform / installing_in_progress /
 * zlite_install_timeout / zlite_install_failed（message 带脚本输出尾部）。
 */
export async function installZlite(): Promise<void> {
  await http.post('/api/v1/agents/zlite/install')
}

// ---------------------------------------------------------------------------
// 通用上游 Provider 探活（兼容 openai/anthropic，去掉 zlite 前缀供后续复用）
// ---------------------------------------------------------------------------

export interface ProviderChannelInput {
  type: 'openai.chat' | 'openai.responses' | 'anthropic'
  baseUrl: string
  apiKey: string
}

/** POST /api/v1/providers/models — 获取上游可用模型列表（8s 超时） */
export async function fetchProviderModels(
  input: ProviderChannelInput,
): Promise<{ models: string[] }> {
  return http.post<{ models: string[] }>('/api/v1/providers/models', {
    body: input,
  })
}

/** POST /api/v1/providers/models/test — 试探指定模型是否可响应（15s 超时，hi/5 token） */
export async function testProviderModel(
  input: ProviderChannelInput & { model: string },
): Promise<{ ok: boolean }> {
  return http.post<{ ok: boolean }>('/api/v1/providers/models/test', {
    body: input,
  })
}

/** GET /api/v1/workspaces — 工作区列表（按最近使用排序） */
export async function fetchWorkspaces(): Promise<Workspace[]> {
  const data = await http.get<{ workspaces: Workspace[] }>('/api/v1/workspaces')
  return data.workspaces
}

/** GET /api/v1/workspaces/:id — 获取 TTY 页面使用的单个工作区。 */
export async function fetchWorkspace(workspaceId: number): Promise<Workspace> {
  const data = await http.get<{ workspace: Workspace }>(
    `/api/v1/workspaces/${encodeURIComponent(String(workspaceId))}`,
  )
  return data.workspace
}

/** POST /api/v1/workspaces — 创建工作区（校验路径存在；同路径曾被移除时整体恢复） */
export async function createWorkspace(path: string): Promise<Workspace> {
  const data = await http.post<{ workspace: Workspace }>('/api/v1/workspaces', {
    body: { path },
  })
  return data.workspace
}

/** DELETE /api/v1/workspaces/:id — 移除项目（后端软删除；同路径再次添加时整体恢复） */
export async function removeWorkspace(workspaceId: number): Promise<void> {
  await http.delete(`/api/v1/workspaces/${workspaceId}`)
}

/**
 * GET /api/v1/workspaces/:id/files?path=... — 列出工作区目录内容。
 *
 * 返回完整 `{ path, entries }`：path 是后端 Clean/越界校验后的规范化相对路径
 * （用户输入脏路径时用它回写输入框，保证展示与后端一致）。
 * 隐藏文件（.gitignore、.env 等）由后端强制显示，仅 node_modules、.git 等
 * 大目录被过滤；path 是后端 Clean/越界校验后的规范化相对路径。
 */
export async function fetchFiles(
  workspaceId: number,
  path = '',
): Promise<{ path: string; entries: FileEntry[] }> {
  return http.get<{ path: string; entries: FileEntry[] }>(
    `/api/v1/workspaces/${workspaceId}/files`,
    { query: { path: path || undefined } },
  )
}

/** GET /api/v1/workspaces/:id/git/status — 按需读取当前 workspace 的 Git 状态 */
export async function fetchGitStatus(workspaceId: number): Promise<GitStatus> {
  return http.get<GitStatus>(`/api/v1/workspaces/${workspaceId}/git/status`)
}

/**
 * POST /api/v1/workspaces/:id/git/commit — 提交选中的文件（可选 push）。
 * push 失败时后端返回 200 + { committed, pushError }，不抛错，前端据此展示
 * 「已提交但推送失败」并提供重试按钮。
 */
export async function commitGitChanges(
  workspaceId: number,
  payload: GitCommitRequest,
): Promise<GitCommitResult> {
  return http.post<GitCommitResult>(
    `/api/v1/workspaces/${workspaceId}/git/commit`,
    { body: payload },
  )
}

/** POST /api/v1/workspaces/:id/git/push — 重试推送当前分支（commit 成功但 push 失败后） */
export async function pushGit(workspaceId: number): Promise<GitPushResult> {
  return http.post<GitPushResult>(`/api/v1/workspaces/${workspaceId}/git/push`)
}

/** PATCH /api/v1/workspaces/:id/files/rename — 在原目录内重命名文件或目录 */
export async function renameFile(
  workspaceId: number,
  path: string,
  name: string,
): Promise<FileEntry> {
  const data = await http.patch<{ file: FileEntry }>(
    `/api/v1/workspaces/${workspaceId}/files/rename`,
    { body: { path, name } },
  )
  return data.file
}

/**
 * DELETE /api/v1/workspaces/:id/files — 删除文件或目录（目录递归删除）。
 *
 * 仅登录认证启用时后端放行（否则 403）；路径含 `.`/`..` 段、根路径、
 * .git / node_modules 等受保护目录由后端拒绝。
 */
export async function deleteFile(workspaceId: number, path: string): Promise<void> {
  await http.delete(`/api/v1/workspaces/${workspaceId}/files`, { body: { path } })
}

/** GET /api/v1/workspaces/:id/files/raw?path=... — 文件原始内容 URL（图片预览 / 下载直链） */
export function fileRawUrl(workspaceId: number, path: string): string {
  return apiUrl(
    `/api/v1/workspaces/${workspaceId}/files/raw?path=${encodeURIComponent(path)}`,
  )
}

/**
 * GET /api/v1/workspaces/:id/files/content?path=... — 读取文本文件内容（编辑器打开）。
 *
 * 目录 / 超过 2MB / 二进制 / 非 UTF-8 文件由后端拒绝并抛对应 ApiError，前端据此提示。
 */
export async function fetchFileContent(
  workspaceId: number,
  path: string,
): Promise<FileContent> {
  return http.get<FileContent>(
    `/api/v1/workspaces/${workspaceId}/files/content`,
    { query: { path } },
  )
}

/**
 * PUT /api/v1/workspaces/:id/files/content — 保存文本文件内容。
 *
 * 携带 expectedMtime（打开时记录的 mtime）做乐观锁：文件已被他处修改时
 * 后端返回 409（code: file_modified），前端据此提示重新加载。
 */
export async function saveFileContent(
  workspaceId: number,
  path: string,
  content: string,
  expectedMtime: number,
): Promise<FileContent> {
  return http.put<FileContent>(
    `/api/v1/workspaces/${workspaceId}/files/content`,
    { body: { path, content, expectedMtime } },
  )
}

/**
 * GET /api/v1/fs/directories?path=<绝对路径> — 目录浏览（新建项目弹窗用）。
 *
 * 列出 path 下的子文件夹（仅文件夹，隐藏目录 / node_modules 等由后端过滤）；
 * path 省略时后端返回 session.default_cwd 解析后的绝对路径作为初始目录。
 */
export async function fetchDirectories(path?: string): Promise<DirectoryList> {
  return http.get<DirectoryList>('/api/v1/fs/directories', {
    query: { path: path || undefined },
  })
}

/**
 * POST /api/v1/workspaces/:id/files/upload — 上传文件到指定目录。
 *
 * 用 XHR 实现以便上报上传进度；失败时解析统一错误体抛 ApiError。
 * @param dir 目标相对目录（空 = 工作区根）
 * @param onProgress 上传进度回调（0~1）
 */
export async function uploadFiles(
  workspaceId: number,
  dir: string,
  files: File[],
  onProgress?: (ratio: number) => void,
): Promise<FileEntry[]> {
  const form = new FormData()
  if (dir) form.append('dir', dir)
  for (const f of files) form.append('files', f, f.name)

  return new Promise<FileEntry[]>((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', apiUrl(`/api/v1/workspaces/${workspaceId}/files/upload`))
    // 上传走 XHR，无法复用 http.ts 的 fetch 封装：这里手动带登录 token
    const token = readAuthToken()
    if (token) {
      xhr.setRequestHeader('Authorization', `Bearer ${token}`)
    }
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && onProgress) {
        onProgress(e.loaded / e.total)
      }
    }
    xhr.onload = () => {
      try {
        const body = JSON.parse(xhr.responseText || '{}') as {
          files?: FileEntry[]
          error?: ApiErrorBody
        }
        if (xhr.status >= 200 && xhr.status < 300 && body.files) {
          resolve(body.files)
        } else {
          reject(
            new ApiError({
              status: xhr.status,
              code: body.error?.code ?? 'upload_failed',
              message: body.error?.message ?? `上传失败（HTTP ${xhr.status}）`,
              body,
            }),
          )
        }
      } catch {
        reject(
          new ApiError({
            status: xhr.status,
            code: 'upload_failed',
            message: '上传失败：响应解析错误',
          }),
        )
      }
    }
    xhr.onerror = () =>
      reject(new ApiError({ status: 0, code: 'network_error', message: '网络错误，上传失败' }))
    xhr.send(form)
  })
}

/**
 * POST /api/v1/files/upload-temp — 聊天输入框快捷键（Ctrl/Cmd+V）粘贴上传。
 * 文件写入后端系统临时目录 /tmp/{yyyyMMddHH}/（目录由后端生成、同名覆盖），
 * 返回 FileEntry[]，其中 Path 为绝对路径（如 /tmp/2026081913/123.webp），
 * 前端直接填充 @/tmp/... 引用。与 uploadFiles（工作区上传）不同：不依赖
 * workspace、允许同名覆盖、返回绝对路径。
 */
export async function uploadTempFiles(files: File[]): Promise<FileEntry[]> {
  const form = new FormData()
  for (const f of files) form.append('files', f, f.name)

  return new Promise<FileEntry[]>((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', apiUrl('/api/v1/files/upload-temp'))
    // 上传走 XHR，无法复用 http.ts 的 fetch 封装：这里手动带登录 token
    const token = readAuthToken()
    if (token) {
      xhr.setRequestHeader('Authorization', `Bearer ${token}`)
    }
    xhr.onload = () => {
      try {
        const body = JSON.parse(xhr.responseText || '{}') as {
          files?: FileEntry[]
          error?: ApiErrorBody
        }
        if (xhr.status >= 200 && xhr.status < 300 && body.files) {
          resolve(body.files)
        } else {
          reject(
            new ApiError({
              status: xhr.status,
              code: body.error?.code ?? 'upload_failed',
              message: body.error?.message ?? `上传失败（HTTP ${xhr.status}）`,
              body,
            }),
          )
        }
      } catch {
        reject(
          new ApiError({
            status: xhr.status,
            code: 'upload_failed',
            message: '上传失败：响应解析错误',
          }),
        )
      }
    }
    xhr.onerror = () =>
      reject(new ApiError({ status: 0, code: 'network_error', message: '网络错误，上传失败' }))
    xhr.send(form)
  })
}

/** GET /api/v1/sessions — 最近活跃会话（侧栏数据源；后端上限 1000，渲染截断在侧栏组件层） */
export async function fetchRecentSessions(limit = 1000): Promise<ChatSession[]> {
  const data = await http.get<{ sessions: ChatSession[] }>('/api/v1/sessions', {
    query: { limit },
  })
  return data.sessions
}

/** GET /api/v1/workspaces/:id/sessions — 按工作区列会话 */
export async function fetchSessionsByWorkspace(
  workspaceId: number,
): Promise<ChatSession[]> {
  const data = await http.get<{ sessions: ChatSession[] }>(
    `/api/v1/workspaces/${workspaceId}/sessions`,
  )
  return data.sessions
}

/** GET /api/v1/sessions/:id — 会话详情；signal 可用于超时/离开页面时中止请求 */
export async function fetchSession(
  sessionId: number,
  signal?: AbortSignal,
): Promise<ChatSession> {
  const data = await http.get<{ session: ChatSession }>(
    `/api/v1/sessions/${sessionId}`,
    { signal },
  )
  return data.session
}

/** POST /api/v1/sessions/:id/open-tool — 在当前会话工作区启动本地工具。 */
export async function openSessionTool(sessionId: number, tool: string): Promise<void> {
	await http.post(`/api/v1/sessions/${sessionId}/open-tool`, {
		body: { tool },
	})
}

/** POST /api/v1/sessions — 创建会话；workspaceId 缺省时后端回退默认工作区 */
export interface CreateSessionInput {
  agentId: string
  workspaceId?: number
  /** 草稿标记：true 表示隐式草稿会话（预览配置项，不进侧栏），默认 false */
  isDraft?: boolean
}

/** 创建会话响应（携带 session 与 agent 下发的 configOptions） */
export interface CreateSessionResult {
  session: ChatSession
  configOptions: ConfigOption[]
}

export async function createSession(
  input: CreateSessionInput,
): Promise<CreateSessionResult> {
  const data = await http.post<{ session: ChatSession; configOptions: ConfigOption[] }>(
    '/api/v1/sessions',
    {
      body: {
        agentId: input.agentId,
        workspaceId: input.workspaceId ?? 0,
        isDraft: input.isDraft ?? false,
      },
    },
  )
  return { session: data.session, configOptions: data.configOptions ?? [] }
}

/** DELETE /api/v1/sessions/:id — 删除会话（物理删除 DB + 异步清理 agent 侧会话：session/delete → close → 有条件停进程） */
export async function deleteSession(sessionId: number): Promise<void> {
  await http.delete(`/api/v1/sessions/${sessionId}`)
}

/** PATCH /api/v1/sessions/:id — 重命名会话标题（用户手动重命名） */
export async function renameSession(sessionId: number, title: string): Promise<void> {
  await http.patch(`/api/v1/sessions/${sessionId}`, { body: { title } })
}

/** DELETE /api/v1/sessions/:id/draft — 删除草稿会话（删 DB + 异步协议层清理 delete→close，不停 agent；切 tab/离开空态时释放旧隐式草稿） */
export async function deleteDraftSession(sessionId: number): Promise<void> {
  await http.delete(`/api/v1/sessions/${sessionId}/draft`)
}

/** GET /api/v1/sessions/:id/messages — 消息历史（分页） */
export async function fetchMessages(
  sessionId: number,
  limit = 50,
  offset = 0,
): Promise<MessagePage> {
  return http.get<MessagePage>(
    `/api/v1/sessions/${sessionId}/messages`,
    { query: { limit, offset } },
  )
}

/** GET /api/v1/sessions/:id/messages?afterId=N — 获取指定消息之后新增的消息 */
export async function fetchMessageUpdates(
  sessionId: number,
  afterId: number,
): Promise<MessageUpdates> {
  return http.get<MessageUpdates>(
    `/api/v1/sessions/${sessionId}/messages`,
    { query: { afterId } },
  )
}

/** GET /api/v1/sessions/:id/messages/:messageId/thoughts — 单条消息的思考过程
 *  （列表接口已把 agent_thought text 置空瘦身，展开思考面板时按需加载） */
export async function fetchMessageThoughts(
  sessionId: number,
  messageId: number,
): Promise<MessageThoughts> {
  return http.get<MessageThoughts>(
    `/api/v1/sessions/${sessionId}/messages/${messageId}/thoughts`,
  )
}

/** POST /api/v1/sessions/:id/messages — 同步发送并等待回复（P1 用；P2 换 WebSocket 流式） */
export async function sendMessage(
  sessionId: number,
  content: string,
  signal?: AbortSignal,
): Promise<ChatMessage> {
  const data = await http.post<{ message: ChatMessage }>(
    `/api/v1/sessions/${sessionId}/messages`,
    { body: { content }, signal },
  )
  return data.message
}

/** GET /api/v1/sessions/:id/config-options — 会话配置项（模型/思考强度/mode；agent 不支持时为空数组） */
export async function fetchConfigOptions(
  sessionId: number,
): Promise<ConfigOption[]> {
  const data = await http.get<{ configOptions: ConfigOption[] }>(
    `/api/v1/sessions/${sessionId}/config-options`,
  )
  return data.configOptions
}

/** GET /api/v1/sessions/:id/slash-commands — 可用 / 命令（agent 未通告时为空数组） */
export async function fetchSlashCommands(
  sessionId: number,
): Promise<AvailableCommand[]> {
  const data = await http.get<{ slashCommands: AvailableCommand[] }>(
    `/api/v1/sessions/${sessionId}/slash-commands`,
  )
  return data.slashCommands
}

/** POST /api/v1/sessions/:id/config-options — 设置会话配置项（如切换模型） */
export async function setConfigOption(
  sessionId: number,
  optionId: string,
  valueId: string,
): Promise<void> {
  await http.post(`/api/v1/sessions/${sessionId}/config-options`, {
    body: { optionId, valueId },
  })
}

// ---------------------------------------------------------------------------
// 账号认证（单用户登录保护，可选）
// ---------------------------------------------------------------------------

/** POST /api/v1/auth/login — 登录，成功返回主 token（7 天，后端内存存储） */
export interface LoginResult {
  token: string
  tokenType: string
  expiresIn: number
  username: string
}

export async function login(
  username: string,
  password: string,
  captchaId?: string,
  captcha?: string,
): Promise<LoginResult> {
  return http.post<LoginResult>('/api/v1/auth/login', {
    body: { username, password, captchaId, captcha },
  })
}

/** GET /api/v1/auth/captcha — 图形验证码（免认证，5 分钟过期单次有效） */
export interface CaptchaResult {
  id: string
  image: string
}

export async function fetchCaptcha(): Promise<CaptchaResult> {
  return http.get<CaptchaResult>('/api/v1/auth/captcha')
}

/** GET /api/v1/auth/status — 认证启用状态（免认证；前端守卫据此决定是否拦截） */
export interface AuthStatus {
  enabled: boolean
}

export async function fetchAuthStatus(): Promise<AuthStatus> {
  return http.get<AuthStatus>('/api/v1/auth/status')
}

/** PUT /api/v1/auth/credentials — 修改用户名/密码；password 为空 = 关闭登录保护 */
export async function updateCredentials(
  username: string,
  password: string,
): Promise<AuthStatus> {
  return http.put<AuthStatus>('/api/v1/auth/credentials', {
    body: { username, password },
  })
}

/**
 * POST /api/v1/workspaces/:id/files/preview-token — 换取文件预览直链。
 *
 * 返回完整 URL（含 12 小时资源 token，绑定 workspace+path），供 <img src> 等
 * 无法携带自定义 header 的场景使用；资源 token 与登录 token 分离，
 * 即使出现在访问日志中也不会泄露登录态。
 */
export async function fetchPreviewUrl(workspaceId: number, path: string): Promise<string> {
  const data = await http.post<{ url: string }>(
    `/api/v1/workspaces/${workspaceId}/files/preview-token`,
    { body: { path } },
  )
  return apiUrl(data.url)
}
