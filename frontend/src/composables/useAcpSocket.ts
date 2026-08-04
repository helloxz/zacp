import { reactive } from 'vue'
import { wsUrl } from '@/config/env'
import type { WsClientMessage, WsServerMessage } from '@/types/ws'

export type SocketStatus = 'idle' | 'connecting' | 'open' | 'closed'

/**
 * 应用级 WebSocket 单例（浏览器一个 Tab 一条连接）。
 *
 * 模型：后端 `GET /api/v1/ws` 为无绑定连接，客户端在 prompt/cancel 消息里带
 * sessionId(ACP session id)+agentId，服务端动态绑定后把事件/turn.done 广播回
 * 本连接（见 hub.go handleMessage 与 BroadcastToSession）。因此本连接天然
 * 绑定「最近一次 prompt 的会话」，符合 AGENTS.md「一条 WS 连接绑定一个后端
 * ACP session」的约定。
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

/** 消息订阅者集合（session store 注册） */
const listeners = new Set<(msg: WsServerMessage) => void>()

const MAX_RECONNECT_MS = 30_000
const HEARTBEAT_MS = 30_000

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
  state.status = 'connecting'
  state.error = null
  let socket: WebSocket
  try {
    socket = new WebSocket(wsUrl('/api/v1/ws'))
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
