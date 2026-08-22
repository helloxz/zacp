<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { IncremarkContent } from '@incremark/vue'
import { fetchMessageThoughts } from '@/api'
import type { ChatMessage, ToolDetailsMap } from '@/types/models'
import type { WsEvent } from '@/types/ws'
import type { ToolCard } from '@/stores/session'
import { useSessionStore } from '@/stores/session'
import { type MessageBlock } from '@/composables/useMessageBlocks'
import ToolCallCard from '@/components/chat/ToolCallCard.vue'

const props = defineProps<{ message: ChatMessage }>()

const { t } = useI18n()
const sessionStore = useSessionStore()

/** 思考过程滚动容器（展开后的内容区，限高 + 滚动，避免长思考把页面撑开） */
const reasoningBodyRef = ref<HTMLElement | null>(null)

/** 按需加载的思考过程缓存（展开面板时请求 /thoughts，组件实例级；重复展开不重复请求） */
const loadedReasoning = ref('')

/**
 * 思考过程按需加载状态机：idle=未加载 / loading=请求中 / loaded=已加载（含加载结果为空）。
 * 用状态而非布尔标记：加载中折叠再展开不重复请求；加载结果为空串时
 * 「已加载过」与「未加载」可区分，避免每次展开都重发请求。
 */
const reasoningLoadState = ref<'idle' | 'loading' | 'loaded'>('idle')

/** user 右对齐 / assistant 左对齐（角色用样式区分，不用气泡色做语义） */
const isUser = computed(() => props.message.role === 'user')

/**
 * 是否存在思考过程（决定折叠面板是否展示）：
 * - 本地已有内容（流式实时 / 缓存 / 老后端未置空）：message.reasoning 非空；
 * - 历史消息：events 中存在 agent_thought 事件即认为有——列表接口已把 text
 *   置空瘦身，type 字段保留作为「存在思考过程」的标记（用户展开时再按需加载）。
 */
const hasThought = computed(() => {
  if (props.message.reasoning) {
    return true
  }
  if (props.message.role !== 'assistant' || !props.message.events) {
    return false
  }
  try {
    const events = JSON.parse(props.message.events) as WsEvent[]
    return events.some((e) => e.type === 'agent_thought')
  } catch {
    // events 不是合法 JSON：静默跳过，不影响文本渲染
    return false
  }
})

/**
 * 思维/推理文本：
 * - 流式期间：来自 store 占位消息的 message.reasoning（agent_thought 实时追加）；
 * - 展开后：loadedReasoning（按需加载 /thoughts 的结果）；
 * - 刷新后/历史消息：兜底从 events 恢复（兼容老后端未置空的完整数据；
 *   新后端已置空时这里得到空串，由展开加载补全）。
 */
const reasoning = computed(() => {
  if (props.message.reasoning) {
    return props.message.reasoning
  }
  if (loadedReasoning.value) {
    return loadedReasoning.value
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
 * 展开思考面板时按需加载完整思考过程（历史消息列表已瘦身置空）。
 * 本地已有内容（流式实时 / 已加载过）或非 assistant 消息时直接跳过；
 * 加载失败保持 idle 状态，用户再次展开可重试；加载成功（含空结果）标记 loaded，
 * 之后重复展开不再请求。
 */
async function onToggleReasoning(e: Event) {
  const open = (e.target as HTMLDetailsElement).open
  if (!open || reasoning.value || reasoningLoadState.value !== 'idle' || !hasThought.value) {
    return
  }
  if (props.message.id < 0 && !props.message.streamFinalized) {
    return // 未转正的流式占位消息本地必有内容，无需按需加载
  }
  const dbId = sessionStore.persistedIdOf(props.message)
  if (dbId === undefined) {
    return // 兜底：拿不到真实 DB id 时跳过（正常不会走到）
  }
  reasoningLoadState.value = 'loading'
  try {
    const data = await fetchMessageThoughts(props.message.sessionId, dbId)
    loadedReasoning.value = data.reasoning
  } catch {
    loadedReasoning.value = ''
    reasoningLoadState.value = 'idle' // 失败允许重试
    return
  }
  reasoningLoadState.value = 'loaded'
}

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

/**
 * 流式自动滚底：思考中 agent_thought 持续追加 reasoning，
 * 若用户已展开面板，视口停在顶部会只看到旧内容；这里在内容增长后把滚动容器
 * 滚到底部，让用户追着最新思考看。turn.done 后（isStreamingPlaceholder=false）不再滚动。
 * 注意：不用 smooth 行为，token 高频追加时平滑动画会累积排队，反而卡顿。
 * 注意：必须定义在 reasoning / isStreamingPlaceholder 之后——Vue 的 watch 注册时会
 * 立即执行一次 getter 收集依赖，若此时引用尚未初始化的 const 会触发 TDZ 报错。
 */
watch(
  () => reasoning.value.length,
  async () => {
    if (!isStreamingPlaceholder.value) return
    await nextTick()
    reasoningBodyRef.value?.scrollTo({ top: reasoningBodyRef.value.scrollHeight })
  },
)


/**
 * 消息渲染块：流式期间用 store.streamBlocks（实时维护），
 * 历史消息从 events JSON 按时间线重建。
 * tool block 携带 contentSplit 位置信息，供模板将 message.content
 * 按工具调用点拆分为多段，恢复文本→工具→文本的因果交错。
 */
const blocks = computed<MessageBlock[]>(() => {
  // 流式占位消息（含「软收尾后、转正前」窗口：status 已回 idle 但占位尚未
  // 合并进 DB）：使用 store.streamBlocks（随事件增量构建，turn.done 后由
  // finalizeStream 保留至 refreshAfterTurn 转正）。已转正占位（streamFinalized）
  // 与历史消息一样从 events 重建——保持消息高度连续，避免滚动跳动。
  // 注意：必须限定 role === 'assistant'——乐观 user 消息的 id 也是负数
  // （appendLocal 用 -Date.now()），若不加过滤，user 消息会把整个会话的
  // streamBlocks（AI 实时内容）也渲染一份，流式期间出现 2 份重复内容。
  if (
    isStreamingPlaceholder.value ||
    (props.message.id < 0 &&
      props.message.role === 'assistant' &&
      !props.message.streamFinalized)
  ) {
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
      }
    }
    flushText()
    return result
  } catch {
    return []
  }
})

/**
 * 可见消息块：过滤空白纯空行（仅空白/换行的 text 块）。
 * 后端 AI 有时会输出 "\n" / "\n\n" / "   " 等空白，导致 IncremarkContent
 * 以空壳渲染（rounded-xl border 仍在，形成空白卡片）。此处统一用 trim 判空，
 * 工具块不受影响（无 content）。
 */
const visibleBlocks = computed<MessageBlock[]>(() =>
  blocks.value.filter((b) => b.kind !== 'text' || Boolean(b.content && b.content.trim())),
)
</script>

<template>
  <div class="flex flex-col gap-2" :class="isUser ? 'items-end' : 'items-start'">
    <!-- 思维/推理文本（流式实时 + 历史按需加载；折叠展示，点击展开查看） -->
    <!-- 与 AI 内容一致：固定占用整个可用内容宽度（w-full），无 max 宽度限制 -->
    <!-- 展示依据：hasThought（本地内容或 events 存在 agent_thought type）；新后端列表接口
         已把 text 置空瘦身，历史内容在展开时经 /thoughts 接口按需加载 -->
    <details
      v-if="!isUser && hasThought"
      class="w-full rounded-lg bg-amber-50/70 px-3 py-2 text-xs leading-relaxed text-slate-500 ring-1 ring-inset ring-amber-100 dark:bg-amber-500/10 dark:text-amber-200/80 dark:ring-amber-500/20"
      @toggle="onToggleReasoning"
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
      <div
        ref="reasoningBodyRef"
        class="reasoning-scroll mt-1.5 max-h-80 overflow-y-auto overscroll-contain wrap-anywhere break-words whitespace-pre-wrap [overflow-wrap:anywhere] pr-2 [word-break:break-word]"
      >
        {{ reasoningLoadState === 'loading' ? t('chat.reasoningLoading') : reasoning }}
      </div>
    </details>

    <!-- user：右对齐气泡（max-w 限宽 + 强制换行，避免纯英文/长 token 无空格时溢出边框） -->
    <div
      v-if="isUser"
      class="max-w-[85%] min-w-0 wrap-anywhere break-words whitespace-pre-wrap [overflow-wrap:anywhere] rounded-2xl rounded-br-md bg-sky-50 px-3.5 py-2.5 text-sm leading-relaxed text-slate-900 ring-1 ring-inset ring-sky-100 dark:bg-sky-500/15 dark:text-sky-50 dark:ring-sky-500/30"
    >
      {{ message.content }}
    </div>

    <!-- 消息块时间线：按事件顺序交错渲染 AI 文本与工具调用，
         恢复因果关系（每个工具卡紧跟触发它的文字之后） -->
    <template v-for="(block, idx) in visibleBlocks" :key="idx">
      <!-- 文本块：从 message.content 按 contentSplit 位置切片，
           流式期间仅最后一个 text block 有内容（其余为空占位） -->
      <div
        v-if="block.kind === 'text'"
        class="w-full min-w-0 overflow-hidden"
      >
        <!-- 流式占位：content 为空且 turn 未结束时显示加载动画 -->
        <div
          v-if="!block.content.trim() && !isFinished"
          class="flex w-full min-w-0 items-center gap-1.5 rounded-xl border border-divider bg-surface-raised px-4 py-3.5 shadow-sm"
          aria-label="loading"
        >
          <span v-for="i in 3" :key="i" class="loading-dot" />
        </div>
        <IncremarkContent
          v-else-if="block.content.trim()"
          class="w-full min-w-0 wrap-anywhere break-words [overflow-wrap:anywhere] [word-break:break-word] overflow-hidden rounded-xl border border-divider bg-surface-raised px-4 py-3 text-sm leading-relaxed shadow-sm"
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
    </template>

    <!-- 流式初始态：streamBlocks 尚无内容时显示加载动画（首个文本块到达后由 IncremarkContent 接管） -->
    <div
      v-if="isStreamingPlaceholder && !visibleBlocks.length"
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
/* 思考过程滚动区：限高后出现滚动条，这里收敛原生滚动条样式（窄条 + 主题色），
   暗色下浏览器默认滚动条偏亮，覆盖为暗色系（.dark 由 stores/app applyThemeClass 控制） */
.reasoning-scroll {
  scrollbar-width: thin;
  scrollbar-color: rgb(203 213 225 / 0.9) transparent; /* slate-300 */
}
html.dark .reasoning-scroll {
  scrollbar-color: rgb(71 85 105 / 0.9) transparent; /* slate-600 */
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
 * 注意：必须使用 :deep()（Vue 3 规范），:::deep 为非法伪元素，lightningcss 会丢弃整条规则。
 */
:deep(ul.incremark-list) {
  list-style-type: disc;
  list-style-position: outside;
  padding-left: 2em;
}
:deep(ol.incremark-list) {
  list-style-type: decimal;
  list-style-position: outside;
  padding-left: 2em;
}
:deep(ul.incremark-list ul) {
  list-style-type: circle;
}
:deep(ul.incremark-list ul ul) {
  list-style-type: square;
}
:deep(.incremark-list.task-list) {
  list-style: none;
  padding-left: 0;
}
:deep(.incremark-list-item.task-item) {
  list-style: none;
}
/*
 * 表格横向滚动（方案 A）：手机端多列表格不强制换行，通过横向滚动保证可读性
 * - .incremark-table-wrapper 本身已有 overflow-x:auto，需突破父级 IncremarkContent 的 overflow-hidden + px-4
 *   负 margin 抵消内边距，max-width 校正，避免滚动条被裁剪
 * - 表格 width:max-content + min-width:560px（6列×90px）保证在窄视口下触发滚动，而非等分压缩
 * - 单元格默认 nowrap（表头/数字/状态不换行），仅名称/类别列允许换行（避免超长英文撑破）
 * - 保留 table-layout:fixed 避免打字机流式时列宽抖动
 */
:deep(.incremark-table-wrapper) {
  overflow-x: auto;
  -webkit-overflow-scrolling: touch;
  overscroll-behavior-x: contain;
  margin-left: -16px;
  margin-right: -16px;
  padding-left: 16px;
  padding-right: 16px;
  max-width: calc(100% + 32px);
  scrollbar-width: thin;
  scrollbar-color: var(--color-scrollbar-thumb) transparent;
}
:deep(.incremark-table-wrapper)::-webkit-scrollbar {
  height: 6px;
}
:deep(.incremark-table-wrapper)::-webkit-scrollbar-thumb {
  background-color: var(--color-scrollbar-thumb);
  border-radius: 9999px;
  border: 1px solid transparent;
  background-clip: content-box;
}
:deep(.incremark-table-wrapper)::-webkit-scrollbar-thumb:hover {
  background-color: var(--color-scrollbar-thumb-hover);
}
:deep(.incremark-table) {
  width: max-content;
  min-width: 560px;
  table-layout: fixed;
}
:deep(.incremark-table th),
:deep(.incremark-table td) {
  white-space: nowrap;
  min-width: 90px;
}
/* 名称/类别列允许换行，避免长文本单行过宽 */
:deep(.incremark-table td:nth-child(2)),
:deep(.incremark-table td:nth-child(3)) {
  white-space: normal;
  min-width: 140px;
  overflow-wrap: anywhere;
  word-break: break-word;
}
/*
 * 纯英文/长 token 无空格时溢出修复：
 * - user 气泡已用 wrap-anywhere/break-words 兜底；
 * - AI 侧 IncremarkContent 内段落/标题/列表/引用等文本容器同样需强制换行，
 *   否则超长连续英文会撑破卡片边框（flex + w-full 仍会溢出）。
 * - 代码块（.incremark-code / pre / code）保留横向滚动，不强制换行。
 */
:deep(.incremark-paragraph),
:deep(.incremark-heading),
:deep(.incremark-blockquote),
:deep(.incremark-paragraph a) {
  overflow-wrap: anywhere;
  word-break: break-word;
}
/* 列表/段落行高提升至 1.75，配合 inline-code 的垂直边距，避免换行粘连 */
:deep(.incremark-list-item),
:deep(.incremark-paragraph) {
  line-height: 1.75;
}
/* 行内代码：2px 4px + box-decoration-break:clone，解决 li 内大量 `code` 换行背景粘连 */
:deep(.incremark-inline-code) {
  padding: 2px 4px;
  line-height: 1.6;
  box-decoration-break: clone;
  -webkit-box-decoration-break: clone;
  vertical-align: baseline;
  margin: 1px 0;
  overflow-wrap: anywhere;
  word-break: break-word;
}
</style>
