<script setup lang="ts">
import { themeOverrides } from '@/config/theme'
import { useAppStore } from '@/stores/app'

const appStore = useAppStore()
// NConfigProvider / Message / Dialog / Notification 由 unplugin-vue-components 按需解析
</script>

<template>
  <!-- locale / date-locale 与 vue-i18n 同步，见 stores/app + useLocaleSwitch -->
  <!-- theme-overrides：全局主色换为 sky 蓝，定义见 config/theme.ts -->
  <n-config-provider
    :locale="appStore.naiveLocale"
    :date-locale="appStore.naiveDateLocale"
    :theme-overrides="themeOverrides"
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
