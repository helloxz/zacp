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
 * 思维/推理文本：
 * - 流式期间：来自 store 占位消息的 message.reasoning（agent_thought 实时追加）；
 * - 刷新后/历史消息：Message 表未持久化 reasoning 列，但 agent_thought 事件
 *   完整落在 events JSON 里，这里按序拼接恢复（与流式 `+=` 的拼接结果一致）。
 */
const reasoning = computed(() => {
  if (props.message.reasoning) {
    return props.message.reasoning
  }
  if (props.message.role !== 'assistant' || !props.message.events) {
    return ''
  }
  try {
    const events = JSON.parse(props.message.events) as WsEvent[]
    return events
      .filter((e) => e.type === 'agent_thought' && e.text)
      .map((e) => e.text as string)
      .join('')
  } catch {
    // events 不是合法 JSON：静默跳过，不影响文本渲染
    return ''
  }
})

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
 * 是否为流式占位消息（当前 turn 正在流式且本条是列表最后一条）。
 * 流式期间该消息 events 为空，工具卡片由 store.activeToolCards 实时渲染，
 * 与历史 toolCards 区块互斥、位置一致（都在 AI 内容框上方），避免 tool.done
 * 后工具条从消息下方“跳”到上方。
 */
const isStreamingPlaceholder = computed(
  () =>
    sessionStore.streaming &&
    sessionStore.activeMessages.at(-1)?.id === props.message.id,
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
        const existing = map.get(e.toolId)
        if (existing) {
          // 合并语义（与流式 upsertToolCard 一致）：update 事件未携带的字段
          // 保留 tool_call 时的值——title/status 空串、input/output 为 null/undefined
          // 都视为未携带，避免刷新后入参/出参丢失
          map.set(e.toolId, {
            ...existing,
            ...e,
            title: e.title || existing.title,
            status: e.status || existing.status,
            input: e.input ?? existing.input,
            output: e.output ?? existing.output,
          })
        } else {
          map.set(e.toolId, e)
        }
      }
    }
    return [...map.values()].map((e) => ({
      toolId: e.toolId as string,
      title: e.title,
      status: e.status,
      // 入参/出参透传，供 ToolCallCard 展开详情；旧消息无此字段则为 undefined
      input: e.input,
      output: e.output,
    }))
  } catch {
    // events 不是合法 JSON：静默跳过工具卡片，不影响文本渲染
    return []
  }
})
</script>

<template>
  <div class="flex flex-col gap-2" :class="isUser ? 'items-end' : 'items-start'">
    <!-- 思维/推理文本（流式实时 + 历史从 events 恢复；折叠展示，点击展开查看） -->
    <!-- 与 AI 内容一致：固定占用整个可用内容宽度（w-full），无 max 宽度限制 -->
    <details
      v-if="!isUser && reasoning"
      class="w-full rounded-lg bg-amber-50/70 px-3 py-2 text-xs leading-relaxed text-slate-500 ring-1 ring-inset ring-amber-100"
    >
      <summary class="cursor-pointer select-none font-medium text-slate-400">
        {{ t('chat.reasoning') }}
      </summary>
      <div class="mt-1.5 whitespace-pre-wrap">{{ reasoning }}</div>
    </details>

    <!-- 历史工具调用卡片（assistant 消息上方，与正文同宽） -->
    <div v-if="toolCards.length" class="flex w-full flex-col gap-2">
      <ToolCallCard v-for="c in toolCards" :key="c.toolId" :card="c" />
    </div>

    <!-- 实时工具调用卡片（仅流式占位消息渲染；turn.done 后由上方历史卡片接替） -->
    <div
      v-if="isStreamingPlaceholder && sessionStore.activeToolCards.length"
      class="flex w-full flex-col gap-2"
    >
      <ToolCallCard
        v-for="c in sessionStore.activeToolCards"
        :key="c.toolId"
        :card="c"
      />
    </div>

    <!-- user：右对齐气泡；assistant：markdown 全宽渲染（边框+阴影与气泡区分，样式由 incremark 主题提供） -->
    <div
      v-if="isUser"
      class="max-w-[85%] whitespace-pre-wrap rounded-2xl rounded-br-md bg-green-50 px-3.5 py-2.5 text-sm leading-relaxed text-slate-900 ring-1 ring-inset ring-green-100"
    >
      {{ message.content }}
    </div>
    <!-- 流式占位：assistant 首条内容尚未到达（content 为空且 turn 未结束）时
         显示三点加载动画，避免空白 AI 卡片；首个文本块到达后由 incremark 替换 -->
    <div
      v-else-if="!message.content && !isFinished"
      class="flex w-full min-w-0 items-center gap-1.5 rounded-xl border border-slate-200/80 bg-white px-4 py-3.5 shadow-sm"
      aria-label="loading"
    >
      <span v-for="i in 3" :key="i" class="loading-dot" />
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
/* 加载动画：三个圆点依次弹跳（assistant 内容未到达时的占位，见模板 loading-dot） */
.loading-dot {
  width: 6px;
  height: 6px;
  border-radius: 9999px;
  background-color: #94a3b8;
  animation: loading-dot-bounce 1.2s ease-in-out infinite;
}
.loading-dot:nth-child(2) {
  animation-delay: 0.15s;
}
.loading-dot:nth-child(3) {
  animation-delay: 0.3s;
}
@keyframes loading-dot-bounce {
  0%,
  60%,
  100% {
    transform: translateY(0);
    opacity: 0.45;
  }
  30% {
    transform: translateY(-4px);
    opacity: 1;
  }
}
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
