<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ChevronDownOutline, HammerOutline } from '@vicons/ionicons5'
import { NIcon } from 'naive-ui'
import type { ToolCard } from '@/stores/session'

const props = defineProps<{ card: ToolCard }>()

const { t } = useI18n()

/** 详情展开/折叠状态（默认收起；无详情时按钮禁用，仅展示标题行） */
const expanded = ref(false)

/** 状态展示文案（ACP 状态词汇：pending/in_progress/completed/failed；
 * running/error 兼容旧数据；未知值原样展示） */
const statusText = computed(() => {
  switch (props.card.status) {
    case 'pending':
      return t('tool.pending')
    case 'in_progress':
    case 'running':
      return t('tool.inProgress')
    case 'completed':
      return t('tool.completed')
    case 'failed':
      return t('tool.failed')
    case 'error':
      return t('tool.error')
    default:
      return props.card.status ?? t('tool.running')
  }
})

/** 是否终态（completed/failed/error）：终态停止呼吸动画 */
const isTerminal = computed(() =>
  ['completed', 'failed', 'error'].includes(props.card.status ?? ''),
)

/** 状态点颜色：进行中蓝色呼吸 / 完成绿色 / 失败黄色 / 出错红色 */
const statusDotClass = computed(() => {
  switch (props.card.status) {
    case 'completed':
      return 'bg-green-500'
    case 'failed':
      return 'bg-amber-500'
    case 'error':
      return 'bg-red-500'
    default:
      return 'bg-blue-500'
  }
})

/** 详情文本最大长度（字符），超出截断防止超大输出撑爆渲染 */
const MAX_DETAIL_CHARS = 50000

/**
 * 详情展示文本：对象/数组格式化 JSON，字符串原样展示；
 * 空值（undefined/null）返回空串表示无此内容；超长截断。
 */
function formatDetail(value: unknown): string {
  if (value === undefined || value === null) return ''
  let text: string
  if (typeof value === 'string') {
    text = value
  } else {
    try {
      text = JSON.stringify(value, null, 2) ?? ''
    } catch {
      // 极端情况（循环引用等）：退化为 String 展示，避免渲染崩溃
      text = String(value)
    }
  }
  if (text.length > MAX_DETAIL_CHARS) {
    text = text.slice(0, MAX_DETAIL_CHARS) + t('tool.truncated')
  }
  return text
}

/** 原始值是否非空（标题行箭头/禁用判断用，不做序列化） */
function hasValue(value: unknown): boolean {
  if (value === undefined || value === null) return false
  return typeof value !== 'string' || value.length > 0
}

/**
 * 详情文本：惰性 computed——折叠时返回 null 不执行 JSON 序列化
 * （避免大输出在折叠态也占用主线程）；展开期间随 card 更新实时重算
 * （流式 tool_call_update 可刷新展示）。
 */
const details = computed(() => {
  if (!expanded.value) return null
  return {
    input: formatDetail(props.card.input),
    output: formatDetail(props.card.output),
  }
})

/** 是否有详情可展开（旧消息无 input/output 时为 false，保持纯标题行） */
const hasDetail = computed(() => hasValue(props.card.input) || hasValue(props.card.output))
</script>

<template>
  <div class="overflow-hidden rounded-lg border border-divider bg-surface-raised shadow-sm">
    <!-- 标题行：整行可点击，切换详情展开/折叠（无详情时禁用点击） -->
    <button
      type="button"
      class="flex w-full cursor-pointer items-center gap-2 px-3 py-2 text-left text-sm transition-colors hover:bg-surface-hover disabled:cursor-default disabled:hover:bg-transparent"
      :disabled="!hasDetail"
      @click="expanded = !expanded"
    >
      <span
        class="relative flex h-2 w-2 shrink-0"
        :class="{ 'animate-pulse': !isTerminal }"
      >
        <span class="absolute inline-flex h-full w-full rounded-full opacity-75" :class="statusDotClass" />
        <span class="relative inline-flex h-2 w-2 rounded-full" :class="statusDotClass" />
      </span>
      <n-icon class="shrink-0 text-ink-muted"><HammerOutline /></n-icon>
      <span class="min-w-0 flex-1 truncate font-medium text-ink-secondary">
        {{ card.title || t('tool.unknown') }}
      </span>
      <span class="shrink-0 text-xs text-ink-muted">{{ statusText }}</span>
      <!-- 展开指示箭头：展开时旋转 180° -->
      <n-icon
        v-if="hasDetail"
        class="shrink-0 text-ink-muted transition-transform duration-200"
        :class="expanded ? 'rotate-180' : ''"
      >
        <ChevronDownOutline />
      </n-icon>
    </button>

    <!-- 详情区：参数 + 结果（JSON 格式化，max-h 内滚动防大输出撑爆布局） -->
    <div v-show="expanded" class="border-t border-divider-subtle px-3 py-2.5">
      <div v-if="details?.input" class="mb-2.5">
        <div class="mb-1 text-xs font-medium text-ink-muted">{{ t('tool.input') }}</div>
        <pre
          class="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-md bg-surface-hover p-2.5 font-mono text-xs leading-relaxed text-ink-secondary"
        >{{ details.input }}</pre>
      </div>
      <div v-if="details?.output">
        <div class="mb-1 text-xs font-medium text-ink-muted">{{ t('tool.output') }}</div>
        <pre
          class="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-md bg-surface-hover p-2.5 font-mono text-xs leading-relaxed text-ink-secondary"
        >{{ details.output }}</pre>
      </div>
    </div>
  </div>
</template>
