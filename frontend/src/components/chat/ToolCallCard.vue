<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { HammerOutline } from '@vicons/ionicons5'
import { NIcon } from 'naive-ui'
import type { ToolCard } from '@/stores/session'

const props = defineProps<{ card: ToolCard }>()

const { t } = useI18n()

/** 状态展示文案（running/completed/error 之外显示原文） */
const statusText = computed(() => {
  switch (props.card.status) {
    case 'running':
      return t('tool.running')
    case 'completed':
      return t('tool.completed')
    case 'error':
      return t('tool.error')
    default:
      return props.card.status ?? t('tool.running')
  }
})

/** 状态点颜色：进行中蓝色呼吸 / 完成绿色 / 出错红色 */
const statusDotClass = computed(() => {
  switch (props.card.status) {
    case 'completed':
      return 'bg-green-500'
    case 'error':
      return 'bg-red-500'
    default:
      return 'bg-blue-500'
  }
})
</script>

<template>
  <div
    class="flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm shadow-sm"
  >
    <span
      class="relative flex h-2 w-2 shrink-0"
      :class="{ 'animate-pulse': card.status !== 'completed' && card.status !== 'error' }"
    >
      <span class="absolute inline-flex h-full w-full rounded-full opacity-75" :class="statusDotClass" />
      <span class="relative inline-flex h-2 w-2 rounded-full" :class="statusDotClass" />
    </span>
    <n-icon class="shrink-0 text-slate-400"><HammerOutline /></n-icon>
    <span class="min-w-0 flex-1 truncate font-medium text-slate-700">
      {{ card.title || t('tool.unknown') }}
    </span>
    <span class="shrink-0 text-xs text-slate-400">{{ statusText }}</span>
  </div>
</template>
