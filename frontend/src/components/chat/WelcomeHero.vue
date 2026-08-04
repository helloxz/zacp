<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

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
        class="select-none text-[9rem] font-bold tracking-tighter text-slate-100"
      >
        {{ t('common.appName') }}
      </span>
    </div>

    <div class="relative flex w-full max-w-[680px] flex-col items-center gap-6">
      <div class="space-y-2 text-center">
        <h1 class="text-3xl font-semibold tracking-tight text-slate-900">
          {{ greeting }}
        </h1>
        <p class="text-base text-slate-500">{{ t('shell.noProjectsHint') }}</p>
      </div>
      <!-- 无项目首屏：引导新建项目（侧栏左上角入口） -->
      <n-button
        size="large"
        type="primary"
        @click="emit('new-project')"
      >
        {{ t('shell.newProject') }}
      </n-button>
    </div>
  </div>
</template>
