/**
 * 前端运行时环境配置（来自 Vite 环境变量）。
 *
 * - 开发：`.env.development`（可被 `.env.development.local` 覆盖）
 * - 生产：`.env.production`
 * - 仅 `VITE_*` 会打进客户端包
 */

/** 去掉尾部 `/`，空串保持为空 */
function normalizeBaseUrl(raw: string | undefined): string {
  const value = (raw ?? '').trim()
  if (!value) {
    return ''
  }
  return value.replace(/\/+$/, '')
}

/** 后端 HTTP 基础 URL；空表示同源（相对路径 `/api/...`） */
export const apiBaseUrl = normalizeBaseUrl(import.meta.env.VITE_API_BASE_URL)

/**
 * 拼接 API 路径。
 * @param path 以 `/` 开头的路径，如 `/api/v1/agents`
 */
export function apiUrl(path: string): string {
  const p = path.startsWith('/') ? path : `/${path}`
  return apiBaseUrl ? `${apiBaseUrl}${p}` : p
}

/**
 * WebSocket 基础 URL（与 HTTP 同源策略一致）。
 * - 配置了 `VITE_API_BASE_URL`：http→ws / https→wss
 * - 未配置：使用当前页面 host，协议随页面 http/https 切换
 */
export function wsBaseUrl(): string {
  if (apiBaseUrl) {
    return apiBaseUrl.replace(/^http/i, 'ws')
  }
  if (typeof window === 'undefined') {
    return ''
  }
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${protocol}//${window.location.host}`
}

/**
 * 拼接 WebSocket 路径，如 `/api/v1/ws`
 */
export function wsUrl(path: string): string {
  const p = path.startsWith('/') ? path : `/${path}`
  return `${wsBaseUrl()}${p}`
}
