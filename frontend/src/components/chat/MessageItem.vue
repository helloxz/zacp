<script setup lang="ts">
import { computed } from 'vue'
import type { ChatMessage } from '@/types/models'
import type { WsEvent } from '@/types/ws'
import type { ToolCard } from '@/stores/session'
import ToolCallCard from '@/components/chat/ToolCallCard.vue'

const props = defineProps<{ message: ChatMessage }>()

/** user 右对齐 / assistant 左对齐（角色用样式区分，不用气泡色做语义） */
const isUser = computed(() => props.message.role === 'user')

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
    <!-- 历史工具调用卡片（assistant 消息上方） -->
    <div v-if="toolCards.length" class="flex w-full max-w-[85%] flex-col gap-2">
      <ToolCallCard v-for="c in toolCards" :key="c.toolId" :card="c" />
    </div>

    <div
      class="max-w-[85%] whitespace-pre-wrap rounded-2xl px-3.5 py-2.5 text-sm leading-relaxed"
      :class="
        isUser
          ? 'rounded-br-md bg-green-50 text-slate-900 ring-1 ring-inset ring-green-100'
          : 'rounded-bl-md bg-slate-100 text-slate-800'
      "
    >
      {{ message.content }}
    </div>
  </div>
</template>
