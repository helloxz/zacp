<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import {
  MAX_TURNS_PER_SESSION,
  WARN_TURNS_PER_SESSION,
} from '@/stores/session'

/**
 * 对话轮次指示器（会话头部右侧按钮区）：Naive Progress 圆环 + Tooltip 详情。
 * - percentage = 轮次 / 50（封顶 100），圆环本身即「图标」，不显示百分比文字；
 * - >30 轮进入告警区，圆环与 tooltip 状态文案同步变红（含 30~50 告警与满格上限态）；
 * - 0 轮（草稿/空会话）不渲染，由父组件 v-if 控制，保持头部干净。
 */
const props = defineProps<{ turns: number }>()

const { t } = useI18n()
const appStore = useAppStore()

const percentage = computed(() =>
  Math.min(100, Math.round((props.turns / MAX_TURNS_PER_SESSION) * 100)),
)

/**
 * 填充色：
 * - >30 轮告警档固定红（#d03050，亮/暗均可读）；
 * - 正常档亮色用 sky-600（比主题主色深一度，浅底上更清晰），
 *   暗色用 sky-300（比主题 sky-400 更亮，深底上不融背景）。
 */
const fillColor = computed(() => {
  if (props.turns > WARN_TURNS_PER_SESSION) return '#d03050'
  return appStore.isDark ? '#7dd3fc' : '#0284c7'
})

/** 轨道色：亮色 slate-200 与 sky-600 填充形成对比；暗色 slate-600 比背景高两档，保证圆环轮廓可见。 */
const railColor = computed(() => (appStore.isDark ? '#475569' : '#e2e8f0'))

/**
 * tooltip 主题：Naive 的 n-tooltip 默认恒为黑底（与主题亮暗无关），
 * 亮色模式下改为白底深字贴近页面观感；暗色模式保持默认深色背景。
 */
const tooltipTheme = computed(() =>
  appStore.isDark
    ? undefined
    : { color: '#ffffff', textColor: '#0f172a' },
)

/** tooltip 状态文案三档：正常 / 告警（质量可能下降）/ 已超限（停止发送）。 */
const statusText = computed(() => {
  if (props.turns >= MAX_TURNS_PER_SESSION) {
    return t('chat.turnStatusLimit', { max: MAX_TURNS_PER_SESSION })
  }
  if (props.turns > WARN_TURNS_PER_SESSION) {
    return t('chat.turnStatusWarn')
  }
  return t('chat.turnStatusGood')
})
</script>

<template>
  <n-tooltip
    trigger="hover"
    placement="bottom"
    :theme-overrides="tooltipTheme"
  >
    <template #trigger>
      <div
        class="flex h-6 w-6 shrink-0 cursor-pointer items-center justify-center rounded-md bg-surface-hover"
        role="img"
        :aria-label="
          t('chat.turnIndicatorAria', {
            count: turns,
            max: MAX_TURNS_PER_SESSION,
          })
        "
      >
        <n-progress
          type="circle"
          :percentage="percentage"
          :show-indicator="false"
          :stroke-width="18"
          :color="fillColor"
          :rail-color="railColor"
          :style="{ width: '15px', height: '15px' }"
          class="turn-indicator-progress"
        />
      </div>
    </template>
    <div class="flex flex-col gap-0.5 text-xs">
      <span class="font-medium text-ink">
        {{ t('chat.turnCount', { count: turns, max: MAX_TURNS_PER_SESSION }) }}
      </span>
      <span
        :class="
          turns > WARN_TURNS_PER_SESSION
            ? 'text-red-500 dark:text-red-400'
            : 'text-ink-muted'
        "
      >
        {{ statusText }}
      </span>
    </div>
  </n-tooltip>
</template>

<style scoped>
/* 根尺寸用内联 style 锁死 15px：scoped 的 :deep 选不到 n-progress 根元素
   （根元素自身带父 scopeId，无法用「父级后代」选择器命中），内联最可靠。
   内部按真实渲染层级（content → graph → graph-circle → svg）逐层收拢，
   每层 flex 居中，保证 svg 在 15px 画布内上下左右居中；
   再做一层 svg 兜底尺寸（防止 future 渲染层级变化导致 svg 回退默认尺寸）。 */
.turn-indicator-progress :deep(.n-progress-content),
.turn-indicator-progress :deep(.n-progress-graph),
.turn-indicator-progress :deep(.n-progress-graph-circle) {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 15px;
  height: 15px;
}
.turn-indicator-progress :deep(svg) {
  width: 15px;
  height: 15px;
  display: block;
}
</style>