<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { AddOutline } from '@vicons/ionicons5'

const { t } = useI18n()
const emit = defineEmits<{
  (e: 'new-project'): void
}>()

/** 按时段问候：6-12 早上 / 12-18 下午 / 其余 晚上（文案不 i18n 细化，仅切换问候语） */
const greeting = computed(() => {
  const hour = new Date().getHours()
  if (hour >= 6 && hour < 12) return t('chat.welcomeMorning')
  if (hour >= 12 && hour < 18) return t('chat.welcomeAfternoon')
  return t('chat.welcomeEvening')
})
</script>

<template>
  <div
    class="relative flex min-h-0 flex-1 flex-col items-center justify-center overflow-hidden px-6"
  >
    <!-- 极淡品牌水印（背景装饰，不参与交互） -->
    <div
      class="pointer-events-none absolute inset-0 flex items-center justify-center"
    >
      <span
        class="select-none text-[9rem] font-bold tracking-tighter text-slate-100 dark:text-slate-800"
      >
        {{ t('common.appName') }}
      </span>
    </div>

    <div class="relative flex w-full max-w-[680px] flex-col items-center gap-6">
      <div class="space-y-2 text-center">
        <h1 class="text-3xl font-semibold tracking-tight text-ink">
          {{ greeting }}
        </h1>
        <p class="text-base text-ink-muted">{{ t('shell.noProjectsHint') }}</p>
      </div>
      <!-- 无项目首屏：引导新建项目（Tailwind 主按钮，点击与侧栏共享同一弹窗） -->
      <button
        type="button"
        class="inline-flex cursor-pointer items-center justify-center gap-2 rounded-xl bg-slate-900 px-8 py-3 text-sm font-medium text-white shadow-sm transition-colors hover:bg-slate-700 focus:outline-none focus-visible:ring-2 focus-visible:ring-slate-400 focus-visible:ring-offset-2 dark:bg-slate-100 dark:text-slate-900 dark:hover:bg-slate-300 dark:focus-visible:ring-slate-500 dark:focus-visible:ring-offset-slate-900"
        @click="emit('new-project')"
      >
        <AddOutline class="h-5 w-5 shrink-0" />
        {{ t('shell.newProject') }}
      </button>
    </div>
  </div>
</template>
