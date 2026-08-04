<script setup lang="ts">
import { useAppStore } from '@/stores/app'

const appStore = useAppStore()
// NConfigProvider / Message / Dialog / Notification 由 unplugin-vue-components 按需解析
</script>

<template>
  <!-- locale / date-locale 与 vue-i18n 同步，见 stores/app + useLocaleSwitch -->
  <n-config-provider
    :locale="appStore.naiveLocale"
    :date-locale="appStore.naiveDateLocale"
    class="h-full"
  >
    <n-message-provider>
      <n-dialog-provider>
        <n-notification-provider>
          <router-view />
        </n-notification-provider>
      </n-dialog-provider>
    </n-message-provider>
  </n-config-provider>
</template>
