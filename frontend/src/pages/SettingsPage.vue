<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import LocaleSwitch from '@/components/LocaleSwitch.vue'

const { t } = useI18n()
</script>

<template>
  <div class="space-y-4">
    <h1 class="text-2xl font-semibold tracking-tight">
      {{ t('settings.title') }}
    </h1>

    <n-card size="small">
      <n-form label-placement="left" label-width="auto">
        <n-form-item :label="t('common.language')">
          <div class="flex flex-col gap-2">
            <LocaleSwitch />
            <n-text depth="3" class="text-sm">
              {{ t('settings.languageHint') }}
            </n-text>
          </div>
        </n-form-item>
      </n-form>
    </n-card>
  </div>
</template>
