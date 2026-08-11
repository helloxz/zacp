import { reactive } from 'vue'
import { wsUrl } from '@/config/env'
import { useAuthStore } from '@/stores/auth'
import { readAuthToken } from '@/utils/authStorage'
import type {
  TtyServerMessage,
  TtySocketHandlers,
  TtySocketStatus,
} from '@/types/tty'

/** WebSocket 子协议前缀，与后端共享认证实现保持一致。 */
const WS_AUTH_PROTOCOL_PREFIX = 'zacp-auth.'

export type { TtySocketStatus }

/**
 * 一个 Tab 对应一个 TTY socket。
 * 不做 ACP composable 的全局重连：临时终端断开后关闭原 PTY，避免恢复语义不清。
 */
export interface TtySocket {
  readonly state: Readonly<{ status: TtySocketStatus; error: string | null }>
  connect(): Promise<boolean>
  sendInput(data: Uint8Array): boolean
  sendResize(cols: number, rows: number): boolean
  close(): void
}

export function useTtySocket(workspaceId: number, handlers: TtySocketHandlers = {}): TtySocket {
  const state = reactive<{ status: TtySocketStatus; error: string | null }>({
    status: 'idle',
    error: null,
  })
  let socket: WebSocket | null = null
  let manuallyClosed = false

  function setError(message: string) {
    state.error = message
    state.status = 'error'
    handlers.onError?.(message)
  }

  function handleServerMessage(raw: string) {
    let message: TtyServerMessage
    try {
      message = JSON.parse(raw) as TtyServerMessage
    } catch {
      setError('终端服务返回了无效消息')
      return
    }
    if (message.type !== 'ready' && message.type !== 'exit' && message.type !== 'error') {
      setError('终端服务返回了未知消息')
      return
    }
    handlers.onMessage?.(message)
  }

  async function connect(): Promise<boolean> {
    if (socket && (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)) {
      return true
    }
    manuallyClosed = false
    state.status = 'connecting'
    state.error = null

    const authStore = useAuthStore()
    try {
      await authStore.ensureStatus()
    } catch (error) {
      setError(error instanceof Error ? error.message : '无法读取认证状态')
      return false
    }
    const token = readAuthToken()
    if (authStore.enabled && !token) {
      setError('请先登录后使用终端')
      return false
    }

    const url = wsUrl(`/api/v1/tty/ws?workspaceId=${encodeURIComponent(String(workspaceId))}`)
    let opened = false
    return await new Promise<boolean>((resolve) => {
      const current = token
        ? new WebSocket(url, [`${WS_AUTH_PROTOCOL_PREFIX}${token}`])
        : new WebSocket(url)
      socket = current
      current.binaryType = 'arraybuffer'

      current.onopen = () => {
        if (socket !== current) return
        opened = true
        state.status = 'open'
        handlers.onStatus?.('open')
        resolve(true)
      }
      current.onmessage = (event) => {
        if (typeof event.data === 'string') {
          handleServerMessage(event.data)
          return
        }
        if (event.data instanceof ArrayBuffer) {
          handlers.onOutput?.(new Uint8Array(event.data))
          return
        }
        if (event.data instanceof Blob) {
          void event.data.arrayBuffer().then((data) => handlers.onOutput?.(new Uint8Array(data)))
        }
      }
      current.onerror = () => {
        if (socket !== current) return
        setError('终端 WebSocket 连接失败')
      }
      current.onclose = () => {
        if (socket !== current) return
        socket = null
        if (!manuallyClosed && !opened) {
          setError('终端 WebSocket 握手失败')
        } else {
          state.status = 'closed'
          handlers.onStatus?.('closed')
        }
        handlers.onClose?.()
        if (!opened) resolve(false)
      }
    })
  }

  function sendInput(data: Uint8Array): boolean {
    if (!socket || socket.readyState !== WebSocket.OPEN || data.byteLength === 0) return false
    const payload = new Uint8Array(data.byteLength)
    payload.set(data)
    socket.send(payload.buffer)
    return true
  }

  function sendResize(cols: number, rows: number): boolean {
    if (!socket || socket.readyState !== WebSocket.OPEN) return false
    socket.send(JSON.stringify({ type: 'resize', cols, rows }))
    return true
  }

  function close(): void {
    manuallyClosed = true
    const current = socket
    socket = null
    if (current && current.readyState !== WebSocket.CLOSED) {
      current.close(1000, 'terminal closed')
    }
    state.status = 'closed'
    handlers.onStatus?.('closed')
  }

  return {
    state,
    connect,
    sendInput,
    sendResize,
    close,
  }
}
