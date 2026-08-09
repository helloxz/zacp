/**
 * 登录 token 的本地存储工具（无依赖小模块）。
 *
 * 独立成模块是为了让 `api/http.ts`（请求层）与 `stores/auth.ts`（状态层）
 * 共享同一份存取逻辑，避免二者循环依赖（auth store → api → http → auth store）。
 */

const TOKEN_KEY = 'zacp.auth.token'

/** 读取本地登录 token；无则返回空串（未登录） */
export function readAuthToken(): string {
  if (typeof localStorage === 'undefined') {
    return ''
  }
  return localStorage.getItem(TOKEN_KEY) ?? ''
}

/** 写入登录 token（登录成功时调用） */
export function writeAuthToken(token: string): void {
  if (typeof localStorage !== 'undefined') {
    localStorage.setItem(TOKEN_KEY, token)
  }
}

/** 清除登录 token（登出 / 401 / 凭证变更时调用） */
export function clearAuthToken(): void {
  if (typeof localStorage !== 'undefined') {
    localStorage.removeItem(TOKEN_KEY)
  }
}
