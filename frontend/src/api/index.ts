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
import type {
  Agent,
  AvailableCommand,
  ChatMessage,
  ChatSession,
  ConfigOption,
  DirectoryList,
  FileEntry,
  MessagePage,
  VersionInfo,
  Workspace,
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

/** GET /api/v1/workspaces — 工作区列表（按最近使用排序） */
export async function fetchWorkspaces(): Promise<Workspace[]> {
  const data = await http.get<{ workspaces: Workspace[] }>('/api/v1/workspaces')
  return data.workspaces
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

/** GET /api/v1/workspaces/:id/files?path=... — 列出工作区目录内容（隐藏文件由后端强制过滤） */
export async function fetchFiles(
  workspaceId: number,
  path = '',
): Promise<FileEntry[]> {
  const data = await http.get<{ path: string; entries: FileEntry[] }>(
    `/api/v1/workspaces/${workspaceId}/files`,
    { query: { path: path || undefined } },
  )
  return data.entries
}

/** GET /api/v1/workspaces/:id/files/raw?path=... — 文件原始内容 URL（图片预览 / 下载直链） */
export function fileRawUrl(workspaceId: number, path: string): string {
  return apiUrl(
    `/api/v1/workspaces/${workspaceId}/files/raw?path=${encodeURIComponent(path)}`,
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

/** GET /api/v1/sessions/:id — 会话详情 */
export async function fetchSession(sessionId: number): Promise<ChatSession> {
  const data = await http.get<{ session: ChatSession }>(
    `/api/v1/sessions/${sessionId}`,
  )
  return data.session
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

/** DELETE /api/v1/sessions/:id — 删除会话（物理删除 + 停 agent） */
export async function deleteSession(sessionId: number): Promise<void> {
  await http.delete(`/api/v1/sessions/${sessionId}`)
}

/** PATCH /api/v1/sessions/:id — 重命名会话标题（用户手动重命名） */
export async function renameSession(sessionId: number, title: string): Promise<void> {
  await http.patch(`/api/v1/sessions/${sessionId}`, { body: { title } })
}

/** DELETE /api/v1/sessions/:id/draft — 删除草稿会话（切 tab/离开空态时释放旧隐式草稿） */
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
