import { apiUrl } from '@/config/env'
import { clearAuthToken, readAuthToken } from '@/utils/authStorage'
import { ApiError, type HttpMethod, type RequestOptions } from './types'

/**
 * 将 query 对象编码为 `?a=1&b=2`；无有效参数时返回空串。
 */
function buildQuery(query?: RequestOptions['query']): string {
  if (!query) {
    return ''
  }
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null) {
      continue
    }
    params.set(key, String(value))
  }
  const s = params.toString()
  return s ? `?${s}` : ''
}

/**
 * 解析后端错误 JSON；非约定格式时回退到通用 code。
 */
async function parseError(res: Response): Promise<ApiError> {
  let body: unknown
  let code = `http_${res.status}`
  let message = res.statusText || `HTTP ${res.status}`

  const contentType = res.headers.get('content-type') ?? ''
  if (contentType.includes('application/json')) {
    try {
      body = await res.json()
      const err = (body as { error?: { code?: string; message?: string } })?.error
      if (err?.code) {
        code = err.code
      }
      if (err?.message) {
        message = err.message
      }
    } catch {
      // 保持默认 message
    }
  } else {
    try {
      const text = await res.text()
      if (text) {
        message = text.slice(0, 500)
        body = text
      }
    } catch {
      // ignore
    }
  }

  return new ApiError({ status: res.status, code, message, body })
}

/**
 * 认证免跳转路径：这些端点的 401 属于正常业务返回（登录失败 / 状态查询），
 * 不应触发「清 token 跳登录」的全局拦截。
 */
const AUTH_FREE_PATHS = ['/api/v1/auth/login', '/api/v1/auth/status']

/**
 * 401 全局拦截：清空本地 token 并整页跳转登录页（带回跳地址）。
 * 用 window.location 而非 vue-router：http.ts 在请求层，避免与 router 循环依赖；
 * 整页刷新也能让所有已挂载组件以干净状态重建。
 */
function handleUnauthorized(): void {
  clearAuthToken()
  // 动态 import 打破与 stores/auth（auth store → api → http）的静态循环依赖
  void import('@/stores/auth').then(({ useAuthStore }) => {
    useAuthStore().forceLogout()
  })
  if (window.location.pathname.startsWith('/login')) {
    return // 已在登录页：仅清 token，不重复跳转
  }
  const redirect = encodeURIComponent(window.location.pathname + window.location.search)
  window.location.assign(`/login?redirect=${redirect}`)
}

/**
 * 底层请求：自动拼接 `VITE_API_BASE_URL` + 路径。
 *
 * @param path 仅写后端路径即可，例如 `/api/v1/agents`（可省略前导 `/`）
 *
 * @example
 * ```ts
 * const data = await request<{ agents: Agent[] }>('GET', '/api/v1/agents')
 * await request('POST', '/api/v1/sessions', { body: { agentId: 'reasonix' } })
 * ```
 */
export async function request<T = unknown>(
  method: HttpMethod,
  path: string,
  options: RequestOptions = {},
): Promise<T> {
  const { query, body, headers = {}, signal, json = true } = options

  const url = apiUrl(path) + buildQuery(query)

  const init: RequestInit = {
    method,
    signal,
    headers: { ...headers },
  }

  // 登录 token：存在则统一带 Authorization Bearer（认证未启用时后端直接忽略）
  const token = readAuthToken()
  if (token) {
    ;(init.headers as Record<string, string>)['Authorization'] = `Bearer ${token}`
  }

  if (body !== undefined && body !== null && method !== 'GET') {
    if (body instanceof FormData) {
      // 让浏览器自动带 multipart boundary，不要设 Content-Type
      init.body = body
    } else {
      ;(init.headers as Record<string, string>)['Content-Type'] =
        'application/json'
      init.body = JSON.stringify(body)
    }
  }

  // 期望 JSON 时显式 Accept，便于后端与中间层识别
  if (json && !(init.headers as Record<string, string>)['Accept']) {
    ;(init.headers as Record<string, string>)['Accept'] = 'application/json'
  }

  let res: Response
  try {
    res = await fetch(url, init)
  } catch (err) {
    // 网络错误 / 被 abort
    if (err instanceof DOMException && err.name === 'AbortError') {
      throw err
    }
    throw new ApiError({
      status: 0,
      code: 'network_error',
      message: err instanceof Error ? err.message : 'Network request failed',
      body: err,
    })
  }

  if (!res.ok) {
    // 认证失败：清 token 并跳登录（登录/状态接口除外）
    if (res.status === 401 && !AUTH_FREE_PATHS.some((p) => path.startsWith(p))) {
      handleUnauthorized()
    }
    throw await parseError(res)
  }

  // 204 / 空体
  if (res.status === 204 || res.headers.get('content-length') === '0') {
    return undefined as T
  }

  if (!json) {
    return res as unknown as T
  }

  // 部分 DELETE 成功无 body
  const text = await res.text()
  if (!text) {
    return undefined as T
  }

  try {
    return JSON.parse(text) as T
  } catch {
    throw new ApiError({
      status: res.status,
      code: 'invalid_json',
      message: 'Response is not valid JSON',
      body: text,
    })
  }
}

/** 面向业务的快捷方法：路径只写 `/api/v1/...` */
export const http = {
  get<T = unknown>(path: string, options?: Omit<RequestOptions, 'body'>) {
    return request<T>('GET', path, options)
  },

  post<T = unknown>(path: string, options?: RequestOptions) {
    return request<T>('POST', path, options)
  },

  put<T = unknown>(path: string, options?: RequestOptions) {
    return request<T>('PUT', path, options)
  },

  patch<T = unknown>(path: string, options?: RequestOptions) {
    return request<T>('PATCH', path, options)
  },

  delete<T = unknown>(path: string, options?: RequestOptions) {
    return request<T>('DELETE', path, options)
  },
}

export default http
