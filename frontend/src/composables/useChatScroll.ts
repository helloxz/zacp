import { ref, type Ref } from 'vue'

/**
 * 聊天滚动控制：自动贴底 + 用户上滚暂停跟随 + 「回到底部」按钮状态。
 *
 * 流式场景约束（设计文档 §6.3）：token 持续追加时，若用户正在上翻历史，
 * 不应强行拉回底部；仅当用户位于底部（或未滚动）时才自动跟随。
 *
 * @param scroller 可滚动容器元素（v-for 消息列表的父级）
 */
export function useChatScroll(scroller: Ref<HTMLElement | null>) {
  /** 是否贴底（距底部 < 40px 视为贴底） */
  const atBottom = ref(true)
  /** 是否显示「回到底部」悬浮按钮 */
  const showBackToBottom = ref(false)

  /** 滚动事件：更新 atBottom / showBackToBottom */
  function onScroll() {
    const el = scroller.value
    if (!el) {
      return
    }
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight
    atBottom.value = distance < 40
    showBackToBottom.value = !atBottom.value
  }

  /** 滚动到底部 */
  function scrollToBottom(smooth = false) {
    const el = scroller.value
    if (!el) {
      return
    }
    el.scrollTo({ top: el.scrollHeight, behavior: smooth ? 'smooth' : 'auto' })
  }

  /**
   * 无条件贴底并复位跟随状态（发送新消息/切换会话用）。
   * 与「上翻暂停跟随」策略相反：发送是用户明确的「回到最新」主动意图，
   * 不延续历史阅读位置；贴底后 atBottom=true，后续流式追加由 followIfAtBottom 自然持续跟随。
   */
  function snapToBottom() {
    atBottom.value = true
    showBackToBottom.value = false
    scrollToBottom()
  }

  /** 内容变化后调用：贴底则跟随，否则保持用户位置 */
  function followIfAtBottom() {
    if (atBottom.value) {
      scrollToBottom()
    }
  }

  return {
    atBottom,
    showBackToBottom,
    onScroll,
    scrollToBottom,
    snapToBottom,
    followIfAtBottom,
  }
}
