<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SettingsOutline } from '@vicons/ionicons5'
import { NIcon } from 'naive-ui'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
const appStore = useAppStore()
const emit = defineEmits<{ (e: 'open-settings'): void }>()

/** 头像占位：显示名首字母（无账号体系，见设计文档 §4.3） */
const avatarText = computed(
  () => appStore.displayName.trim().charAt(0).toUpperCase() || '?',
)
</script>

<template>
  <div
    class="flex shrink-0 items-center gap-2 border-t border-slate-200 px-3 py-2.5"
  >
    <div
      class="flex h-8 w-8 shrink-0 select-none items-center justify-center rounded-full bg-slate-300 text-sm font-semibold text-white"
    >
      {{ avatarText }}
    </div>
    <div class="min-w-0 flex-1">
      <p class="truncate text-sm font-medium text-slate-700">
        {{ appStore.displayName }}
      </p>
      <p class="text-xs text-slate-400">{{ t('common.appName') }}</p>
    </div>
    <n-tooltip trigger="hover">
      <template #trigger>
        <n-button
          quaternary
          circle
          size="small"
          @click="emit('open-settings')"
        >
          <template #icon>
            <n-icon><SettingsOutline /></n-icon>
          </template>
        </n-button>
      </template>
      {{ t('shell.settings') }}
    </n-tooltip>
  </div>
</template>
