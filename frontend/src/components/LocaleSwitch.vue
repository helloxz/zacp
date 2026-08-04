<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AppLocale } from '@/types/locale'
import { useLocaleSwitch } from '@/composables/useLocaleSwitch'

const { t } = useI18n()
const { locale, setLocale } = useLocaleSwitch()

const options = computed(() => [
  { label: t('locale.zhCN'), value: 'zh-CN' satisfies AppLocale },
  { label: t('locale.enUS'), value: 'en-US' satisfies AppLocale },
])

function onUpdate(value: string) {
  setLocale(value as AppLocale)
}
</script>

<template>
  <!-- n-select 由 NaiveUiResolver 按需引入，无需 script 中 import -->
  <n-select
    class="w-36"
    size="small"
    :value="locale"
    :options="options"
    :consistent-menu-width="false"
    @update:value="onUpdate"
  />
</template>
