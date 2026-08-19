<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import LocaleSwitch from '@/components/LocaleSwitch.vue'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
const appStore = useAppStore()

/** 显示名本地草稿：输入过程不 trim，失焦/回车（change）才保存 */
const nameDraft = ref(appStore.displayName)

function onNameChange() {
  appStore.setDisplayName(nameDraft.value)
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <div>
      <h3 class="text-base font-semibold text-ink">
        {{ t('settings.system.title') }}
      </h3>
      <p class="mt-1 text-sm text-ink-muted">{{ t('settings.system.subtitle') }}</p>
    </div>

    <!-- 语言：复用 LocaleSwitch（vue-i18n + Naive locale 联动） -->
    <section class="flex flex-col gap-2">
      <h4 class="text-sm font-medium text-ink-secondary">{{ t('common.language') }}</h4>
      <LocaleSwitch />
      <p class="text-xs text-ink-muted">{{ t('settings.system.languageHint') }}</p>
    </section>

    <!-- 显示名：本地保存，影响左下用户区 -->
    <section class="flex flex-col gap-2">
      <h4 class="text-sm font-medium text-ink-secondary">
        {{ t('settings.system.displayNameLabel') }}
      </h4>
      <n-input
        v-model:value="nameDraft"
        :placeholder="t('settings.system.displayNamePlaceholder')"
        @change="onNameChange"
      />
      <p class="text-xs text-ink-muted">
        {{ t('settings.system.displayNameHint') }}
      </p>
    </section>

    <!-- 右侧边栏自动展开：进入会话页时是否自动展开右侧面板；与标题栏手动切换双向同步 -->
    <section class="flex items-center justify-between gap-4">
      <div class="flex flex-col gap-1">
        <span class="text-sm font-medium text-ink-secondary">
          {{ t('settings.system.rightPanelAutoExpandLabel') }}
        </span>
        <span class="text-xs text-ink-muted">
          {{ t('settings.system.rightPanelAutoExpandHint') }}
        </span>
      </div>
      <n-switch
        :value="appStore.rightPanelAutoExpand"
        @update:value="appStore.setRightPanelAutoExpand"
      />
    </section>
  </div>
</template>
