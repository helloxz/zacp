<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { IncremarkContent } from '@incremark/vue'
import type { ChatMessage } from '@/types/models'
import type { WsEvent } from '@/types/ws'
import type { ToolCard } from '@/stores/session'
import { useSessionStore } from '@/stores/session'
import ToolCallCard from '@/components/chat/ToolCallCard.vue'

const props = defineProps<{ message: ChatMessage }>()

const { t } = useI18n()
const sessionStore = useSessionStore()

/** user 右对齐 / assistant 左对齐（角色用样式区分，不用气泡色做语义） */
const isUser = computed(() => props.message.role === 'user')

/**
 * 流式终态判定：仅当「当前 turn 仍在流式」且「本条就是流式占位消息（列表最后一条）」时
 * 视为未完成；incremark 据此决定块状态（pending/completed）与渐入动画。
 * turn.done / cancelSend 后 streaming=false，消息转终态，再由 store 刷新 DB 最终内容。
 */
const isFinished = computed(
  () =>
    !(
      sessionStore.streaming &&
      sessionStore.activeMessages.at(-1)?.id === props.message.id
    ),
)

/**
 * 历史工具调用卡片：解析 assistant 消息的 events JSON（client.Event 数组），
 * 按 toolId 去重保留最后一次状态。流式期间的实时卡片由 store.activeToolCards 负责。
 */
const toolCards = computed<ToolCard[]>(() => {
  if (props.message.role !== 'assistant' || !props.message.events) {
    return []
  }
  try {
    const events = JSON.parse(props.message.events) as WsEvent[]
    const map = new Map<string, WsEvent>()
    for (const e of events) {
      if (
        (e.type === 'tool_call' || e.type === 'tool_call_update') &&
        e.toolId
      ) {
        map.set(e.toolId, e)
      }
    }
    return [...map.values()].map((e) => ({
      toolId: e.toolId as string,
      title: e.title,
      status: e.status,
    }))
  } catch {
    // events 不是合法 JSON：静默跳过工具卡片，不影响文本渲染
    return []
  }
})
</script>

<template>
  <div class="flex flex-col gap-2" :class="isUser ? 'items-end' : 'items-start'">
    <!-- 思维/推理文本（仅流式期间有值；折叠展示，点击展开查看） -->
    <details
      v-if="!isUser && message.reasoning"
      class="w-full max-w-[85%] rounded-lg bg-amber-50/70 px-3 py-2 text-xs leading-relaxed text-slate-500 ring-1 ring-inset ring-amber-100"
    >
      <summary class="cursor-pointer select-none font-medium text-slate-400">
        {{ t('chat.reasoning') }}
      </summary>
      <div class="mt-1.5 whitespace-pre-wrap">{{ message.reasoning }}</div>
    </details>

    <!-- 历史工具调用卡片（assistant 消息上方，与正文同宽） -->
    <div v-if="toolCards.length" class="flex w-full flex-col gap-2">
      <ToolCallCard v-for="c in toolCards" :key="c.toolId" :card="c" />
    </div>

    <!-- user：右对齐气泡；assistant：markdown 全宽渲染（边框+阴影与气泡区分，样式由 incremark 主题提供） -->
    <div
      v-if="isUser"
      class="max-w-[85%] whitespace-pre-wrap rounded-2xl rounded-br-md bg-green-50 px-3.5 py-2.5 text-sm leading-relaxed text-slate-900 ring-1 ring-inset ring-green-100"
    >
      {{ message.content }}
    </div>
    <IncremarkContent
      v-else
      class="w-full min-w-0 rounded-xl border border-slate-200/80 bg-white px-4 py-3 text-sm leading-relaxed shadow-sm"
      :content="message.content"
      :is-finished="isFinished"
      :incremark-options="{ htmlTree: true }"
    />
  </div>
</template>

<style scoped>
/*
 * Tailwind v4 preflight 全局移除了 ul/ol 的 list-style（list-style: none），
 * 这里只在 AI markdown 内容区域局部恢复列表符号，不动全局。
 * 任务列表（task-list）保持无圆点（checkbox 形态，主题自带处理）。
 */
:deep(ul.incremark-list) {
  list-style: disc;
}
:deep(ol.incremark-list) {
  list-style: decimal;
}
</style>
