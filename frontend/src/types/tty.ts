/** 前端单个 TTY Tab 的生命周期状态。 */
export type TtyTabStatus =
  | 'creating'
  | 'connecting'
  | 'connected'
  | 'exited'
  | 'closing'
  | 'closed'
  | 'error'

/** TTY WebSocket 的连接状态。 */
export type TtySocketStatus = 'idle' | 'connecting' | 'open' | 'closed' | 'error'

/** 客户端发送的 TTY Text 控制帧。 */
export interface TtyClientResizeMessage {
  type: 'resize'
  cols: number
  rows: number
}

/** 服务端发送的 TTY Text 控制帧。 */
export type TtyServerMessage =
  | { type: 'ready'; terminalId: string }
  | { type: 'exit'; code: number }
  | { type: 'error'; code: string; message: string }

/** TTY socket 回调；Binary 输出已经转换为 Uint8Array。 */
export interface TtySocketHandlers {
  onStatus?: (status: TtySocketStatus) => void
  onOutput?: (data: Uint8Array) => void
  onMessage?: (message: TtyServerMessage) => void
  onError?: (message: string) => void
  onClose?: () => void
}
