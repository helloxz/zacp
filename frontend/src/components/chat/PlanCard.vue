<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { CheckmarkOutline, ListOutline } from '@vicons/ionicons5'
import { NIcon } from 'naive-ui'
import type { Plan, PlanStep } from '@/types/ws'

defineProps<{ plan: Plan }>()

const { t } = useI18n()

/** 步骤状态标识样式：completed 绿色对勾底 / in_progress 蓝色呼吸点 / pending 灰色圆点 */
function statusDotClass(status: string): string {
  switch (status) {
    case 'completed':
      return 'bg-green-500'
    case 'in_progress':
      return 'bg-blue-500 animate-pulse'
    default:
      return 'bg-divider'
  }
}

/** 步骤是否已完成（completed 时文本置灰加删除线） */
function isCompleted(step: PlanStep): boolean {
  return step.status === 'completed'
}
</script>

<template>
  <div class="overflow-hidden rounded-lg border border-divider bg-surface-raised shadow-sm">
    <!-- 标题行：执行计划（TODO 列表） -->
    <div class="flex items-center gap-2 border-b border-divider-subtle px-3 py-2">
      <n-icon class="shrink-0 text-ink-muted"><ListOutline /></n-icon>
      <span class="text-sm font-medium text-ink-secondary">{{ t('plan.title') }}</span>
    </div>
    <!-- 条目列表：按 agent 下发顺序渲染；ACP 整体替换语义，此处直接消费完整列表 -->
    <ol v-if="plan.entries.length" class="px-3 py-2">
      <li
        v-for="(step, idx) in plan.entries"
        :key="idx"
        class="flex items-start gap-2.5 py-1 text-sm"
      >
        <!-- 状态标识：completed 对勾 / in_progress 呼吸圆点 / pending 灰色圆点 -->
        <span
          class="relative mt-1 flex h-4 w-4 shrink-0 items-center justify-center rounded-full"
          :class="statusDotClass(step.status)"
        >
          <n-icon v-if="isCompleted(step)" class="text-[10px] text-white">
            <CheckmarkOutline />
          </n-icon>
        </span>
        <span
          class="min-w-0 flex-1 leading-relaxed text-ink-secondary"
          :class="{ 'text-ink-muted line-through': isCompleted(step) }"
        >{{ step.content }}</span>
        <span
          v-if="step.status === 'in_progress'"
          class="shrink-0 text-xs text-blue-500 dark:text-blue-400"
        >{{ t('plan.inProgress') }}</span>
      </li>
    </ol>
    <!-- 空计划兜底：agent 发过 plan 事件但无条目时避免空白卡片 -->
    <div v-else class="px-3 py-2 text-sm text-ink-muted">{{ t('plan.empty') }}</div>
  </div>
</template>
