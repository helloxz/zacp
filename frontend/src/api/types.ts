/**
 * 与后端约定对齐的错误体：
 * `{ "error": { "code": "...", "message": "..." } }`
 * @see AGENTS.md §7 API
 */
export interface ApiErrorBody {
  code: string
  message: string
}

/** 可抛出的统一 API 错误（含 HTTP 状态与业务 code） */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly body: unknown

  constructor(options: {
    status: number
    code: string
    message: string
    body?: unknown
  }) {
    super(options.message)
    this.name = 'ApiError'
    this.status = options.status
    this.code = options.code
    this.body = options.body
  }
}

export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

export interface RequestOptions {
  /** 查询参数；值为 null/undefined 的键会被忽略 */
  query?: Record<string, string | number | boolean | null | undefined>
  /** JSON 请求体（对象会自动 JSON.stringify） */
  body?: unknown
  /** 额外请求头 */
  headers?: Record<string, string>
  /** 取消信号（会话切换 / 组件卸载时传入） */
  signal?: AbortSignal
  /**
   * 是否按 JSON 解析响应。默认 true。
   * 设为 false 时返回 Response 原始对象（少见）。
   */
  json?: boolean
}
