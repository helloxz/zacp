import { reactive } from 'vue'
import { wsUrl } from '@/config/env'
import type { WsClientMessage, WsServerMessage } from '@/types/ws'
import { readAuthToken } from '@/utils/authStorage'

/** WebSocket 子协议前缀（与后端 ws/handler.go 的 wsAuthProtocolPrefix 保持一致） */
const WS_AUTH_PROTOCOL_PREFIX = 'zacp-auth.'

export type SocketStatus = 'idle' | 'connecting' | 'open' | 'closed'

/**
 * 应用级 WebSocket 单例（浏览器一个 Tab 一条连接）。
 *
 * 模型：后端 `GET /api/v1/ws` 为无绑定连接，客户端在 prompt/cancel 消息里带
 * sessionId(ACP session id)+agentId，服务端把该会话加入本连接的**订阅集合**
 * （见 hub.go SubscribeSession 与 BroadcastToSession），事件/turn.done 广播按
 * 订阅匹配回送。因此一条连接可同时跟踪多个会话：全局三槽位 FIFO 排队时，
 * 各会话的广播都带 sessionId，由 session store 按会话路由。
 *
 * 关键逻辑：
 * - 心跳：每 30s 发应用层 ping，服务端回 pong；任意消息都视为连接健康
 * - 断线重连：onclose 后指数退避（1s→2s→…→30s 封顶），手动 disconnect 后不再重连
 * - 分发：所有消息广播给订阅者（session store 注册自己的处理）
 */
const state = reactive({
  status: 'idle' as SocketStatus,
  error: null as string | null,
})

let ws: WebSocket | null = null
let reconnectTimer: number | undefined
let reconnectAttempts = 0
let heartbeatTimer: number | undefined
let manuallyClosed = false
/** 连续握手失败计数（从未 open 就被关闭；open 时清零，见 onclose 兜底逻辑） */
let handshakeFailures = 0

/** 消息订阅者集合（session store 注册） */
const listeners = new Set<(msg: WsServerMessage) => void>()

const MAX_RECONNECT_MS = 30_000
const HEARTBEAT_MS = 30_000
/** 连续握手失败上限：超过即视为登录 token 已失效，清登录态跳转登录页 */
const MAX_HANDSHAKE_FAILURES = 3

/** 指数退避重连（不打断已在排队的重连） */
function scheduleReconnect() {
  if (manuallyClosed || reconnectTimer !== undefined) {
    return
  }
  const delay = Math.min(1000 * 2 ** reconnectAttempts, MAX_RECONNECT_MS)
  reconnectAttempts += 1
  state.status = 'connecting'
  reconnectTimer = window.setTimeout(() => {
    reconnectTimer = undefined
    connect()
  }, delay)
}

function handleMessage(raw: string) {
  let msg: WsServerMessage
  try {
    msg = JSON.parse(raw) as WsServerMessage
  } catch {
    return
  }
  // 任何合法消息都视为连接健康，重置退避计数
  reconnectAttempts = 0
  for (const fn of listeners) {
    fn(msg)
  }
}

/** 建立连接（幂等：已连接/连接中则跳过） */
export function connect() {
  if (manuallyClosed || ws) {
    return
  }
  // 认证启用但本地无 token（未登录/已被登出）：不建连，等登录成功后由页面手动 connect。
  // 注意：这里只按「有无 token」判断；token 是否有效由后端握手校验（401 时 onclose 处理）。
  const token = readAuthToken()
  if (!token) {
    return
  }
  state.status = 'connecting'
  state.error = null
  let socket: WebSocket
  try {
    // 登录 token 经 WebSocket 子协议携带（浏览器 WS 无法设自定义 header，
    // 放 URL query 会进访问日志）；后端校验通过后回显该子协议完成握手。
    socket = new WebSocket(wsUrl('/api/v1/ws'), [`${WS_AUTH_PROTOCOL_PREFIX}${token}`])
  } catch (e) {
    state.error = e instanceof Error ? e.message : String(e)
    scheduleReconnect()
    return
  }
  ws = socket

  socket.onopen = () => {
    if (ws !== socket) return // 已被新连接替换
    state.status = 'open'
    reconnectAttempts = 0
    handshakeFailures = 0
    heartbeatTimer = window.setInterval(() => {
      send({ type: 'ping' })
    }, HEARTBEAT_MS)
  }

  socket.onmessage = (e) => {
    handleMessage(String(e.data))
  }

  socket.onerror = () => {
    state.error = 'websocket error'
  }

  socket.onclose = () => {
    if (ws !== socket) return
    ws = null
    state.status = 'closed'
    if (heartbeatTimer !== undefined) {
      window.clearInterval(heartbeatTimer)
      heartbeatTimer = undefined
    }
    // 认证启用后本地 token 已无（401 拦截登出 / 凭证变更清 token）：
    // 不再重连，避免无效握手无限循环；重新登录后由页面手动 connect。
    if (!readAuthToken()) {
      return
    }
    // 连续握手失败兜底：服务重启后内存 token 全部失效（localStorage 里仍是旧 token），
    // 每次握手都会 401——浏览器 WS 无法拿到 HTTP 状态码，只能凭「从未 open 过就关闭」
    // 累计判断。连续 N 次失败视为 token 已失效：清登录态，由 HTTP 层 401 拦截跳转登录页，
    // 避免每 30s 一次的无效握手无限刷后端日志。
    handshakeFailures += 1
    if (handshakeFailures >= MAX_HANDSHAKE_FAILURES) {
      handshakeFailures = 0
      void import('@/stores/auth').then(({ useAuthStore }) => {
        useAuthStore().forceLogout()
        const redirect = encodeURIComponent(window.location.pathname + window.location.search)
        window.location.assign(`/login?redirect=${redirect}`)
      })
      return
    }
    scheduleReconnect()
  }
}

/** 主动断开（页面卸载时调用；断开后不再自动重连） */
export function disconnect() {
  manuallyClosed = true
  if (reconnectTimer !== undefined) {
    window.clearTimeout(reconnectTimer)
    reconnectTimer = undefined
  }
  if (heartbeatTimer !== undefined) {
    window.clearInterval(heartbeatTimer)
    heartbeatTimer = undefined
  }
  ws?.close()
  ws = null
  state.status = 'closed'
}

/** 发送客户端消息；连接未就绪时返回 false */
export function send(msg: WsClientMessage): boolean {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify(msg))
    return true
  }
  return false
}

/** 订阅服务端消息；返回取消订阅函数 */
export function onMessage(fn: (msg: WsServerMessage) => void): () => void {
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
  }
}

/** 只读状态快照（供 UI 显示连接状态） */
export function socketState() {
  return state
}

/** 工具：单个文件里的单例 API（保持命名风格与 store 一致） */
export const acpSocket = {
  connect,
  disconnect,
  send,
  onMessage,
  state,
}
