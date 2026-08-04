<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import LocaleSwitch from '@/components/LocaleSwitch.vue'

/** 版本占位（P0；后续可由构建注入 import.meta.env） */
const APP_VERSION = '0.0.0'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ (e: 'update:show', v: boolean): void }>()

const { t } = useI18n()
const appStore = useAppStore()

/** 显示名本地草稿：输入过程不 trim，失焦/回车（change）才保存 */
const nameDraft = ref(appStore.displayName)

function onNameChange() {
  appStore.setDisplayName(nameDraft.value)
}
</script>

<template>
  <n-drawer
    :show="props.show"
    :width="360"
    placement="right"
    @update:show="emit('update:show', $event)"
  >
    <n-drawer-content :title="t('settings.title')" closable>
      <div class="flex flex-col gap-6">
        <!-- 语言：复用 LocaleSwitch（与顶栏逻辑一致：vue-i18n + Naive locale 联动） -->
        <section class="flex flex-col gap-2">
          <h3 class="text-sm font-medium text-slate-700">
            {{ t('common.language') }}
          </h3>
          <LocaleSwitch />
          <p class="text-xs text-slate-400">{{ t('settings.languageHint') }}</p>
        </section>

        <!-- 显示名：本地保存，影响左下用户区 -->
        <section class="flex flex-col gap-2">
          <h3 class="text-sm font-medium text-slate-700">
            {{ t('settings.displayNameLabel') }}
          </h3>
          <n-input
            v-model:value="nameDraft"
            :placeholder="t('settings.displayNamePlaceholder')"
            @change="onNameChange"
          />
          <p class="text-xs text-slate-400">
            {{ t('settings.displayNameHint') }}
          </p>
        </section>

        <!-- 关于 -->
        <section class="flex flex-col gap-1">
          <h3 class="text-sm font-medium text-slate-700">
            {{ t('shell.about') }}
          </h3>
          <p class="text-sm text-slate-600">
            {{ t('common.appName') }} · {{ t('settings.aboutHint') }}
          </p>
          <p class="text-xs text-slate-400">
            {{ t('shell.version') }} {{ APP_VERSION }}
          </p>
        </section>
      </div>
    </n-drawer-content>
  </n-drawer>
</template>
