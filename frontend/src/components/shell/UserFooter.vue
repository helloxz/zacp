<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { MoonOutline, SettingsOutline, SunnyOutline } from '@vicons/ionicons5'
import { NIcon } from 'naive-ui'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
const appStore = useAppStore()
const emit = defineEmits<{ (e: 'open-settings'): void }>()

/** 头像占位：显示名首字母（无账号体系，见设计文档 §4.3） */
const avatarText = computed(
  () => appStore.displayName.trim().charAt(0).toUpperCase() || '?',
)

/** 主题图标与 tooltip 文案：显示当前模式（浅色=太阳，深色=月亮），点击切换 */
const themeIcon = computed(() => (appStore.isDark ? MoonOutline : SunnyOutline))
const themeTooltip = computed(() =>
  appStore.isDark ? t('settings.switchToLight') : t('settings.switchToDark'),
)
</script>

<template>
  <div
    class="flex shrink-0 items-center gap-2 border-t border-divider px-3 py-2.5"
  >
    <div
      class="flex h-8 w-8 shrink-0 select-none items-center justify-center rounded-full bg-slate-300 text-sm font-semibold text-white dark:bg-slate-600"
    >
      {{ avatarText }}
    </div>
    <div class="min-w-0 flex-1">
      <p class="truncate text-sm font-medium text-ink-secondary">
        {{ appStore.displayName }}
      </p>
      <p class="text-xs text-ink-muted">{{ t('common.appName') }}</p>
    </div>
    <!-- 主题切换：齿轮左侧，图标显示当前模式（太阳=浅色/月亮=深色），点击互切 -->
    <n-tooltip trigger="hover">
      <template #trigger>
        <n-button
          quaternary
          circle
          size="small"
          :aria-label="themeTooltip"
          @click="appStore.toggleTheme()"
        >
          <template #icon>
            <n-icon><component :is="themeIcon" /></n-icon>
          </template>
        </n-button>
      </template>
      {{ themeTooltip }}
    </n-tooltip>
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
