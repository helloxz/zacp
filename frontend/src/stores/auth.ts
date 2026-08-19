import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { fetchAuthStatus, login as apiLogin } from '@/api'
import { clearAuthToken, readAuthToken, writeAuthToken } from '@/utils/authStorage'

/**
 * 认证状态 store：登录 token + 后端认证启用状态。
 *
 * 关键语义（与后端一致）：
 * - `enabled`（后端 [auth].password_hash 非空）= 需要登录；
 * - 未启用时所有请求免认证，本 store 保持「未登录但可用」；
 * - token 存 localStorage（刷新不丢），由后端内存校验（服务重启后失效，需重新登录）。
 */
export const useAuthStore = defineStore('auth', () => {
  const token = ref(readAuthToken())
  const username = ref('')
  /** 后端认证是否启用（来自 GET /api/v1/auth/status，免认证） */
  const enabled = ref(false)
  /** 是否已从后端拉取过启用状态（防止每次路由切换都重复请求） */
  const statusLoaded = ref(false)

  /** 并发去重：ensureStatus 的进行中 promise */
  let statusPromise: Promise<void> | null = null

  /** 是否有可用登录 token */
  const hasToken = computed(() => token.value !== '')

  /**
   * 用后端状态更新本地（login / ensureStatus 共用）。
   * username 仅供已登录上下文（login 响应）回填，供设置页回显当前用户名；
   * 免认证的 /auth/status 不再返回 username，故 ensureStatus 走这里时不会写入。
   */
  function applyStatus(s: { enabled: boolean; username?: string }) {
    enabled.value = s.enabled
    if (s.username) {
      username.value = s.username
    }
    statusLoaded.value = true
  }

  /**
   * 确保已拉取认证启用状态（幂等、并发去重）。
   * 网络失败时不阻塞路由（保持本地认知），由请求层的 401 拦截兜底。
   */
  async function ensureStatus(): Promise<void> {
    if (statusLoaded.value) {
      return
    }
    if (!statusPromise) {
      statusPromise = fetchAuthStatus()
        .then((s) => applyStatus(s))
        .catch(() => {
          // 后端不可达时静默：不把 enabled 置为 false 而阻塞页面
        })
        .finally(() => {
          statusPromise = null
        })
    }
    return statusPromise
  }

  /** 登录：成功后写入 token 并同步启用状态 */
  async function login(usernameInput: string, password: string): Promise<void> {
    const res = await apiLogin(usernameInput, password)
    token.value = res.token
    writeAuthToken(res.token)
    applyStatus({ enabled: true, username: res.username })
  }

  /**
   * 清除本地登录态（登出 / 401 拦截 / 凭证变更后由调用方触发）。
   * 只清本地，不通知后端（token 存内存，服务端无吊销接口）。
   */
  function forceLogout() {
    token.value = ''
    clearAuthToken()
  }

  return {
    token,
    username,
    enabled,
    hasToken,
    statusLoaded,
    ensureStatus,
    login,
    forceLogout,
    applyStatus,
  }
})
