<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ThemeProvider } from '@incremark/vue'
import { useSessionStore } from '@/stores/session'
import { useAppStore } from '@/stores/app'
import { useChatScroll } from '@/composables/useChatScroll'
import MessageItem from '@/components/chat/MessageItem.vue'
import PermissionModal from '@/components/chat/PermissionModal.vue'

const { t } = useI18n()
const sessionStore = useSessionStore()
const appStore = useAppStore()

const scroller = ref<HTMLElement | null>(null)
const {
  showBackToBottom,
  onScroll,
  scrollToBottom,
  followIfAtBottom,
} = useChatScroll(scroller)

/** 消息列表变化信号：长度（追加/刷新）或最后一条内容（流式追加）变化时触发跟随 */
const messageTick = computed(
  () =>
    `${sessionStore.activeMessages.length}:${sessionStore.activeMessages.at(-1)?.content.length ?? 0}:${sessionStore.activeMessages.at(-1)?.reasoning?.length ?? 0}`,
)

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
</script>

<template>
  <div ref="scroller" class="relative overflow-y-auto" @scroll="onScroll">
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

        <n-text
          v-if="!sessionStore.activeMessages.length"
          depth="3"
          class="self-center py-10 text-sm"
        >
          {{ t('chat.emptyMessages') }}
        </n-text>
      </ThemeProvider>
    </div>

    <!-- 权限请求弹窗（壳层级，挂载在消息列表上即可全局可见） -->
    <PermissionModal />

    <!-- 上滚后出现「回到底部」悬浮按钮（流式场景常用） -->
    <transition name="fade">
      <button
        v-if="showBackToBottom"
        class="absolute bottom-3 left-1/2 flex h-8 w-8 -translate-x-1/2 items-center justify-center rounded-full border border-divider bg-surface-raised text-ink-muted shadow-sm transition-colors hover:text-ink"
        :aria-label="t('chat.backToBottom')"
        @click="scrollToBottom(true)"
      >
        ↓
      </button>
    </transition>
  </div>
</template>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.15s ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
