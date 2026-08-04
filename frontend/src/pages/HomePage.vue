<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

const { t } = useI18n()
const router = useRouter()

function goSessions() {
  void router.push({ name: 'sessions' })
}
</script>

<template>
  <div class="space-y-6">
    <div class="space-y-2">
      <h1 class="text-2xl font-semibold tracking-tight md:text-3xl">
        {{ t('home.title') }}
      </h1>
      <n-text depth="3" class="text-base leading-relaxed">
        {{ t('home.subtitle') }}
      </n-text>
    </div>

    <n-space>
      <n-button type="primary" @click="goSessions">
        {{ t('home.getStarted') }}
      </n-button>
    </n-space>

    <n-card :title="t('home.featuresTitle')" size="small">
      <n-ul>
        <n-li>{{ t('home.featureAgents') }}</n-li>
        <n-li>{{ t('home.featureStream') }}</n-li>
        <n-li>{{ t('home.featurePermission') }}</n-li>
      </n-ul>
    </n-card>
  </div>
</template>
