<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<template>
  <div class="space-y-4">
    <h1 class="text-2xl font-semibold tracking-tight">
      {{ t('sessions.title') }}
    </h1>
    <n-card size="small">
      <n-empty :description="t('sessions.placeholder')" />
    </n-card>
  </div>
</template>
