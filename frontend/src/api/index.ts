/**
 * HTTP / API 入口。
 *
 * 用法：
 * ```ts
 * import { http, ApiError } from '@/api'
 *
 * const { agents } = await http.get<{ agents: Agent[] }>('/api/v1/agents')
 * await http.post('/api/v1/sessions', { body: { agentId: 'reasonix' } })
 * ```
 *
 * 基础域名由 `VITE_API_BASE_URL` 自动拼接（见 `@/config/env`），
 * 业务侧只需传 `/api/v1/...` 路径。
 */
export { http, request } from './http'
export { ApiError, type ApiErrorBody, type RequestOptions, type HttpMethod } from './types'
