<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { ServerOutline } from '@vicons/ionicons5'

const { t } = useI18n()
</script>

<template>
  <div class="flex flex-col gap-5">
    <div>
      <h3 class="text-base font-semibold text-slate-800">
        {{ t('settings.agent.title') }}
      </h3>
      <p class="mt-1 text-sm text-slate-500">{{ t('settings.agent.desc') }}</p>
    </div>

    <!-- 占位空态：后续接入 Agent 管理（启停、启动参数等）时替换 -->
    <div
      class="flex flex-col items-center gap-3 rounded-xl border border-dashed border-slate-200 bg-slate-50/60 px-6 py-12 text-center"
    >
      <div
        class="flex h-12 w-12 items-center justify-center rounded-full bg-indigo-50 text-indigo-500"
      >
        <n-icon :size="24"><ServerOutline /></n-icon>
      </div>
      <p class="text-sm font-medium text-slate-600">
        {{ t('settings.agent.comingSoon') }}
      </p>
      <p class="max-w-sm text-xs leading-relaxed text-slate-400">
        {{ t('settings.agent.placeholder') }}
      </p>
    </div>
  </div>
</template>
