<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ThemeProvider } from '@incremark/vue'
import { ChevronDownOutline, ChevronUpOutline } from '@vicons/ionicons5'
import { useSessionStore } from '@/stores/session'
import { useAppStore } from '@/stores/app'
import { useChatScroll } from '@/composables/useChatScroll'
import MessageItem from '@/components/chat/MessageItem.vue'
import PermissionModal from '@/components/chat/PermissionModal.vue'

const { t } = useI18n()
const sessionStore = useSessionStore()
const appStore = useAppStore()

const scroller = ref<HTMLElement | null>(null)
const { atTop, atBottom, onScroll, scrollToBottom, snapToBottom, followIfAtBottom, scrollUp, scrollDown } =
  useChatScroll(scroller)

/** 消息列表变化信号：长度（追加/刷新）或最后一条内容（流式追加）变化时触发跟随 */
const messageTick = computed(
  () =>
    `${sessionStore.activeMessages.length}:${sessionStore.activeMessages.at(-1)?.content.length ?? 0}:${sessionStore.activeMessages.at(-1)?.reasoning?.length ?? 0}`,
)

/** 当前会话消息历史加载状态：未开始（缓存缺失且请求未发）时按加载中处理，
 * 避免切换会话瞬间把「还没加载」误显成「暂无消息」。 */
const messagesLoadStatus = computed<'loading' | 'ready' | 'error'>(() => {
  const id = sessionStore.currentId
  return id === null ? 'ready' : (sessionStore.messagesStatus[id] ?? 'loading')
})

/** 历史加载失败后手动重试（force 强制重新拉取最新窗口） */
function onRetryMessages() {
  const id = sessionStore.currentId
  if (id !== null) {
    void sessionStore.loadMessages(id, true)
  }
}

/**
 * 切换会话待吸附标记：消息历史是异步加载的（loadMessages 完成后才渲染），
 * currentId 变化时直接滚动往往发生在消息渲染前（空列表），需等列表变化后再贴底。
 */
let pendingSnapToBottom = false

watch(messageTick, () => {
  void nextTick(() => {
    if (pendingSnapToBottom) {
      // 新会话消息渲染完成：无条件贴底，并复位标记（之后恢复「贴底才跟随」策略）
      pendingSnapToBottom = false
      scrollToBottom()
    } else {
      followIfAtBottom()
    }
  })
})

/** 切换会话：先尝试立即贴底（缓存命中时本 tick 已渲染），并标记等待异步消息加载 */
watch(
  () => sessionStore.currentId,
  (id) => {
    if (id === null) {
      pendingSnapToBottom = false
      return
    }
    pendingSnapToBottom = true
    void nextTick(() => scrollToBottom())
  },
)

/**
 * 发送消息后强制贴底：turn 状态进入 queued 只发生在 sendViaWs 里（用户主动发送），
 * 此时上翻历史的「暂停跟随」不再适用——发送是明确的「回到最新」意图，
 * 否则滚动条会停留在上方，用户看不到自己刚发的消息与 AI 开始回复。
 * 等 nextTick 让新消息渲染完再滚，行为与上方 messageTick 的贴底逻辑一致；
 * 贴底后 atBottom=true，后续流式 token 追加由 followIfAtBottom 自然持续跟随。
 * prev !== 'queued' 排除重复触发：排队中再次发送会被 Composer 停用拦截，
 * 不存在 queued→queued 的连续发送路径，此条件仅作防御。
 */
watch(
  () => sessionStore.statusOf(sessionStore.currentId),
  (status, prev) => {
    if (status === 'queued' && prev !== 'queued') {
      void nextTick(() => snapToBottom())
    }
  },
)
</script>

<template>
  <!-- 外层 relative + h-full：为右侧悬浮按钮提供视口锚点，滚动容器在内部 -->
  <div class="relative h-full min-h-0">
    <div ref="scroller" class="h-full overflow-y-auto" @scroll="onScroll">
      <div class="content-container flex flex-col gap-4 px-4 py-6">
        <!-- ThemeProvider 把当前主题注入 incremark 渲染上下文：
             驱动 shiki 代码高亮在 github-light / github-dark 之间切换（CSS 层的
             data-theme 属性只影响代码块背景/容器色，token 颜色必须靠这个上下文）。
             只包消息列表（唯一消费方），wrapper 继承原 flex 布局避免间距丢失。 -->
        <ThemeProvider
          class="flex w-full min-w-0 flex-col gap-4"
          :theme="appStore.isDark ? 'dark' : 'default'"
        >
          <MessageItem
            v-for="m in sessionStore.activeMessages"
            :key="m.id"
            :message="m"
          />

          <!-- 实时工具调用卡片已移入 MessageItem 流式占位消息内部（AI 内容上方），
               与历史工具卡片位置统一，避免 turn 结束后工具条跳动 -->

          <!-- 消息历史三态：加载中 / 加载失败（可重试）/ 确认空。
               空态只在 ready 后才显示，避免请求在途时误显「暂无消息」。 -->
          <n-text
            v-if="messagesLoadStatus === 'loading'"
            depth="3"
            class="self-center py-10 text-sm"
          >
            {{ t('chat.loadingMessages') }}
          </n-text>
          <div
            v-else-if="messagesLoadStatus === 'error'"
            class="flex flex-col items-center gap-2 self-center py-10"
          >
            <n-text depth="3" class="text-sm">
              {{ t('chat.messagesLoadFailed') }}
            </n-text>
            <n-button size="small" secondary @click="onRetryMessages">
              {{ t('chat.retry') }}
            </n-button>
          </div>
          <n-text
            v-else-if="!sessionStore.activeMessages.length"
            depth="3"
            class="self-center py-10 text-sm"
          >
            {{ t('chat.emptyMessages') }}
          </n-text>
        </ThemeProvider>
      </div>

      <!-- 权限请求弹窗（壳层级，挂载在消息列表上即可全局可见） -->
      <PermissionModal />
    </div>

    <!-- 右侧上下滚动按钮（替代原输入框上方回到底部按钮） -->
    <!-- 悬浮于外层视口、垂直居中，不随内部滚动而移动；小尺寸圆形，亮/暗色均用语义 token -->
    <div class="absolute right-2 top-1/2 z-10 flex -translate-y-1/2 flex-col gap-1.5 lg:right-4">
      <button
        class="flex h-7 w-7 cursor-pointer items-center justify-center rounded-full border border-divider bg-surface-raised/90 text-ink-muted shadow-sm backdrop-blur transition-colors hover:bg-surface-hover hover:text-ink disabled:pointer-events-none disabled:opacity-40"
        :aria-label="t('chat.scrollUp')"
        :disabled="atTop"
        @click="scrollUp()"
      >
        <n-icon :size="14"><ChevronUpOutline /></n-icon>
      </button>
      <button
        class="flex h-7 w-7 cursor-pointer items-center justify-center rounded-full border border-divider bg-surface-raised/90 text-ink-muted shadow-sm backdrop-blur transition-colors hover:bg-surface-hover hover:text-ink disabled:pointer-events-none disabled:opacity-40"
        :aria-label="t('chat.scrollDown')"
        :disabled="atBottom"
        @click="scrollDown()"
      >
        <n-icon :size="14"><ChevronDownOutline /></n-icon>
      </button>
    </div>
  </div>
</template>
