<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useSessionStore } from '@/stores/session'
import { useChatScroll } from '@/composables/useChatScroll'
import MessageItem from '@/components/chat/MessageItem.vue'
import ToolCallCard from '@/components/chat/ToolCallCard.vue'
import PermissionModal from '@/components/chat/PermissionModal.vue'

const { t } = useI18n()
const sessionStore = useSessionStore()

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

watch(messageTick, () => {
  void nextTick(followIfAtBottom)
})

/** 切换会话时直接回到底部 */
watch(
  () => sessionStore.currentId,
  () => {
    void nextTick(() => scrollToBottom())
  },
)
</script>

<template>
  <div ref="scroller" class="relative overflow-y-auto" @scroll="onScroll">
    <div class="content-container flex flex-col gap-4 px-4 py-6">
      <MessageItem
        v-for="m in sessionStore.activeMessages"
        :key="m.id"
        :message="m"
      />

      <!-- 实时工具调用卡片（流式 turn 中；turn.done 后由历史消息 events 渲染） -->
      <div
        v-if="sessionStore.activeToolCards.length"
        class="flex flex-col gap-2"
      >
        <ToolCallCard
          v-for="c in sessionStore.activeToolCards"
          :key="c.toolId"
          :card="c"
        />
      </div>

      <n-text
        v-if="!sessionStore.activeMessages.length"
        depth="3"
        class="self-center py-10 text-sm"
      >
        {{ t('chat.emptyMessages') }}
      </n-text>
    </div>

    <!-- 权限请求弹窗（壳层级，挂载在消息列表上即可全局可见） -->
    <PermissionModal />

    <!-- 上滚后出现「回到底部」悬浮按钮（流式场景常用） -->
    <transition name="fade">
      <button
        v-if="showBackToBottom"
        class="absolute bottom-3 left-1/2 flex h-8 w-8 -translate-x-1/2 items-center justify-center rounded-full border border-slate-200 bg-white text-slate-500 shadow-sm transition-colors hover:text-slate-800"
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
