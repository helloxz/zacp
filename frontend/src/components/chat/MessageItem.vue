<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { IncremarkContent } from '@incremark/vue'
import type { ChatMessage, ToolDetailsMap } from '@/types/models'
import type { Plan, WsEvent } from '@/types/ws'
import type { ToolCard } from '@/stores/session'
import { useSessionStore } from '@/stores/session'
import { type MessageBlock } from '@/composables/useMessageBlocks'
import ToolCallCard from '@/components/chat/ToolCallCard.vue'
import PlanCard from '@/components/chat/PlanCard.vue'

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
 * 流式终态判定：仅当「本消息所属会话的 turn 仍在流式」且「本条就是流式占位消息（列表最后一条）」时
 * 视为未完成；incremark 据此决定块状态（pending/completed）与渐入动画。
 * turn.done / cancelSend 后状态回 idle，消息转终态，再由 store 刷新 DB 最终内容。
 * 按会话判断：A 流式时切到 B，A 的消息仍显示流式态。
 */
const isFinished = computed(
  () =>
    !(
      sessionStore.statusOf(props.message.sessionId) === 'streaming' &&
      sessionStore.activeMessages.at(-1)?.id === props.message.id
    ),
)

/**
 * 是否为流式占位消息（本会话 turn 正在流式且本条是列表最后一条）。
 * 流式期间该消息 events 为空，工具卡片由 store.activeToolCardsOf 实时渲染，
 * 与历史 toolCards 区块互斥、位置一致（都在 AI 内容框上方），避免 tool.done
 * 后工具条从消息下方“跳”到上方。
 */
const isStreamingPlaceholder = computed(
  () =>
    sessionStore.statusOf(props.message.sessionId) === 'streaming' &&
    sessionStore.activeMessages.at(-1)?.id === props.message.id,
)

/** 本消息会话的实时执行计划（流式占位消息展示用；null=无计划） */
const activePlan = computed(() => sessionStore.activePlanOf(props.message.sessionId))

/**
 * 消息渲染块：流式期间用 store.streamBlocks（实时维护），
 * 历史消息从 events JSON 按时间线重建。
 * tool block 携带 contentSplit 位置信息，供模板将 message.content
 * 按工具调用点拆分为多段，恢复文本→工具→文本的因果交错。
 */
const blocks = computed<MessageBlock[]>(() => {
  // 流式占位消息：使用本会话的 store.streamBlocks（随事件增量构建）
  if (isStreamingPlaceholder.value) {
    return sessionStore.streamBlocksOf(props.message.sessionId)
  }
  // 历史消息：从 events 重建，tool block 携带 contentSplit 位置
  if (props.message.role !== 'assistant' || !props.message.events) {
    return []
  }
  try {
    const events = JSON.parse(props.message.events) as WsEvent[]
    // 工具详情快照（v6 起与 events 拆分存储）：解析失败则回退 events 内嵌值
    let toolDetails: ToolDetailsMap | undefined
    if (props.message.toolDetails) {
      try {
        toolDetails = JSON.parse(props.message.toolDetails) as ToolDetailsMap
      } catch {
        toolDetails = undefined
      }
    }
    const result: MessageBlock[] = []
    let textBuf = ''
    let cumulativeLen = 0

    const flushText = () => {
      if (textBuf) {
        result.push({ kind: 'text', content: textBuf })
        cumulativeLen += textBuf.length
        textBuf = ''
      }
    }

    const toolIndex = new Map<string, { kind: 'tool'; card: ToolCard; contentSplit: number }>()

    for (const e of events) {
      if (e.type === 'agent_message' || e.type === 'user_message') {
        if (e.text) textBuf += e.text
      } else if (e.type === 'agent_thought') {
        // 思维/推理：由 reasoning 区块单独渲染，不混入 blocks
      } else if (e.type === 'tool_call' || e.type === 'tool_call_update') {
        if (!e.toolId) continue
        // 历史详情快照优先（每工具最终一份）；events 内嵌值为旧数据回退
        const detail = toolDetails?.[e.toolId]
        const existing = toolIndex.get(e.toolId)
        if (existing) {
          // 合并语义：update 未携带的字段保留前值
          if (e.title) existing.card.title = e.title
          if (e.status) existing.card.status = e.status
          if (detail) {
            // 快照缺字段时保留前值（防御：拆分语义变化时不会静默清空已有详情）
            existing.card.input = detail.input ?? existing.card.input
            existing.card.output = detail.output ?? existing.card.output
          } else {
            if (e.input != null) existing.card.input = e.input
            if (e.output != null) existing.card.output = e.output
          }
        } else {
          flushText()
          const block: { kind: 'tool'; card: ToolCard; contentSplit: number } = {
            kind: 'tool',
            card: {
              toolId: e.toolId,
              title: e.title,
              status: e.status ?? 'running',
              input: detail?.input ?? e.input,
              output: detail?.output ?? e.output,
            },
            contentSplit: cumulativeLen,
          }
          result.push(block)
          toolIndex.set(e.toolId, block)
        }
      } else if (e.type === 'plan' && e.plan) {
        flushText()
        result.push({ kind: 'plan' })
      }
    }
    flushText()
    return result
  } catch {
    return []
  }
})

/**
 * 历史执行计划：ACP plan 事件为整体替换语义（每次携带完整条目列表），
 * 取 events 中最后一个带 plan 数据的 plan 事件即可，无需按 toolId 合并。
 * 流式期间的实时计划由 store.activePlan 负责（占位消息 events 为空，互斥）。
 */
const plan = computed<Plan | null>(() => {
  if (props.message.role !== 'assistant' || !props.message.events) {
    return null
  }
  try {
    const events = JSON.parse(props.message.events) as WsEvent[]
    for (let i = events.length - 1; i >= 0; i--) {
      if (events[i].type === 'plan' && events[i].plan) {
        return events[i].plan as Plan
      }
    }
    return null
  } catch {
    // events 不是合法 JSON：静默跳过计划卡片，不影响文本渲染
    return null
  }
})

/**
 * blocks 中 plan 块的展示数据：流式期间取实时 activePlan；
 * turn.done 后 activePlan 被清空，回退到从 events 恢复的历史 plan。
 */
const displayPlan = computed<Plan | null>(() => activePlan.value ?? plan.value)
</script>

<template>
  <div class="flex flex-col gap-2" :class="isUser ? 'items-end' : 'items-start'">
    <!-- 思维/推理文本（流式实时 + 历史从 events 恢复；折叠展示，点击展开查看） -->
    <!-- 与 AI 内容一致：固定占用整个可用内容宽度（w-full），无 max 宽度限制 -->
    <details
      v-if="!isUser && reasoning"
      class="w-full rounded-lg bg-amber-50/70 px-3 py-2 text-xs leading-relaxed text-slate-500 ring-1 ring-inset ring-amber-100 dark:bg-amber-500/10 dark:text-amber-200/80 dark:ring-amber-500/20"
    >
      <summary
        class="cursor-pointer select-none font-medium"
        :class="
          isStreamingPlaceholder
            ? 'text-amber-600 dark:text-amber-400'
            : 'text-ink-muted'
        "
      >
        <!-- 思考中：显示「思考中」+ 弹跳圆点（amber 活跃色）；turn.done 后切回「思考过程」且圆点消失 -->
        {{ isStreamingPlaceholder ? t('chat.reasoningThinking') : t('chat.reasoning') }}
        <span v-if="isStreamingPlaceholder" class="inline-flex items-center gap-1 align-middle">
          <span v-for="i in 3" :key="i" class="loading-dot loading-dot-sm" />
        </span>
      </summary>
      <div class="mt-1.5 whitespace-pre-wrap">{{ reasoning }}</div>
    </details>

    <!-- user：右对齐气泡 -->
    <div
      v-if="isUser"
      class="max-w-[85%] whitespace-pre-wrap rounded-2xl rounded-br-md bg-sky-50 px-3.5 py-2.5 text-sm leading-relaxed text-slate-900 ring-1 ring-inset ring-sky-100 dark:bg-sky-500/15 dark:text-sky-50 dark:ring-sky-500/30"
    >
      {{ message.content }}
    </div>

    <!-- 消息块时间线：按事件顺序交错渲染 AI 文本与工具调用，
         恢复因果关系（每个工具卡紧跟触发它的文字之后） -->
    <template v-for="(block, idx) in blocks" :key="idx">
      <!-- 文本块：从 message.content 按 contentSplit 位置切片，
           流式期间仅最后一个 text block 有内容（其余为空占位） -->
      <div
        v-if="block.kind === 'text'"
        class="w-full min-w-0"
      >
        <!-- 流式占位：content 为空且 turn 未结束时显示加载动画 -->
        <div
          v-if="!block.content && !isFinished"
          class="flex w-full min-w-0 items-center gap-1.5 rounded-xl border border-divider bg-surface-raised px-4 py-3.5 shadow-sm"
          aria-label="loading"
        >
          <span v-for="i in 3" :key="i" class="loading-dot" />
        </div>
        <IncremarkContent
          v-else-if="block.content"
          class="w-full min-w-0 rounded-xl border border-divider bg-surface-raised px-4 py-3 text-sm leading-relaxed shadow-sm"
          :content="block.content"
          :is-finished="isFinished"
          :incremark-options="{ htmlTree: true }"
        />
      </div>
      <!-- 工具调用块 -->
      <ToolCallCard
        v-else-if="block.kind === 'tool'"
        class="w-full"
        :card="block.card"
      />
      <!-- 执行计划块：数据取 displayPlan（流式=activePlan，历史=events 恢复的 plan） -->
      <PlanCard
        v-else-if="block.kind === 'plan' && displayPlan"
        class="w-full"
        :plan="displayPlan"
      />
    </template>

    <!-- 流式初始态：streamBlocks 尚无内容时显示加载动画（首个文本块到达后由 IncremarkContent 接管） -->
    <div
      v-if="isStreamingPlaceholder && !blocks.length"
      class="flex w-full min-w-0 items-center gap-1.5 rounded-xl border border-divider bg-surface-raised px-4 py-3.5 shadow-sm"
      aria-label="loading"
    >
      <span v-for="i in 3" :key="i" class="loading-dot" />
    </div>
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
/* 思考中 summary 内的小号圆点：尺寸更小、amber-600 色，与「思考中」活跃提示一致；
   动画复用上方 loading-dot（弹跳），本类仅覆盖尺寸与颜色（须定义在 .loading-dot 之后） */
.loading-dot-sm {
  width: 4px;
  height: 4px;
  background-color: #d97706; /* amber-600 */
}
/* 暗色下 loading 圆点提亮一档（.dark 由 stores/app applyThemeClass 控制，scoped 内用 html.dark 选择器） */
html.dark .loading-dot {
  background-color: #cbd5e1; /* slate-300 */
}
html.dark .loading-dot-sm {
  background-color: #f59e0b; /* amber-500 */
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
