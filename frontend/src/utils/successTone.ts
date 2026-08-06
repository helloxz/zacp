/**
 * 回复完成提示音（硬编码行为，无设置开关）。
 *
 * - 资源：public/success.mp3（vite 构建时原样复制到站点根路径 /success.mp3）
 * - 复用单个 Audio 实例，避免每次播放都重新创建
 * - 播放被浏览器自动播放策略拒绝时静默忽略（catch），不影响对话功能；
 *   对话场景下 turn.done 必然发生在用户点击发送之后，已满足用户手势授权，
 *   正常不会被拦，此处只是兜底。
 */

/** 提示音资源地址（vite base 未配置，public 下资源即站点根路径） */
const TONE_URL = '/success.mp3'

let audio: HTMLAudioElement | null = null

/** 播放回复完成提示音（一轮对话正常完成时调用） */
export function playSuccessTone(): void {
  // SSR / 非浏览器环境兜底
  if (typeof document === 'undefined') {
    return
  }
  // 页面不可见（用户切走标签页）时不响铃，避免后台突然出声
  if (document.visibilityState !== 'visible') {
    return
  }
  if (!audio) {
    audio = new Audio(TONE_URL)
  }
  // 连续多轮回复时重置到开头再播，避免同一实例停在 ended 状态无法重播
  audio.currentTime = 0
  audio.play().catch(() => {
    // 自动播放策略拒绝等场景：静默忽略，不影响对话
  })
}
