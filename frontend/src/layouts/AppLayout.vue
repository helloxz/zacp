<script setup lang="ts">
import { computed, h } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import type { MenuOption } from 'naive-ui'
// 在 render 函数（h）里使用的组件无法被模板解析器自动注入，需显式按需 import
import { NIcon } from 'naive-ui'
import {
  ChatbubblesOutline,
  HomeOutline,
  SettingsOutline,
} from '@vicons/ionicons5'
import LocaleSwitch from '@/components/LocaleSwitch.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

function renderIcon(icon: typeof HomeOutline) {
  return () => h(NIcon, null, { default: () => h(icon) })
}

const menuOptions = computed<MenuOption[]>(() => [
  {
    label: t('nav.home'),
    key: 'home',
    icon: renderIcon(HomeOutline),
  },
  {
    label: t('nav.sessions'),
    key: 'sessions',
    icon: renderIcon(ChatbubblesOutline),
  },
  {
    label: t('nav.settings'),
    key: 'settings',
    icon: renderIcon(SettingsOutline),
  },
])

const activeKey = computed(() => {
  const name = route.name
  return typeof name === 'string' ? name : 'home'
})

function onMenuUpdate(key: string) {
  if (key === activeKey.value) {
    return
  }
  void router.push({ name: key })
}
</script>

<template>
  <n-layout class="min-h-full">
    <n-layout-header
      bordered
      class="flex h-14 items-center justify-between px-4 md:px-6"
    >
      <n-space align="center" :size="16">
        <router-link to="/" class="inline-flex items-center no-underline">
          <n-text strong class="text-base tracking-tight">
            {{ t('common.appName') }}
          </n-text>
        </router-link>
        <n-menu
          mode="horizontal"
          :value="activeKey"
          :options="menuOptions"
          :responsive="false"
          @update:value="onMenuUpdate"
        />
      </n-space>
      <LocaleSwitch />
    </n-layout-header>

    <n-layout-content class="px-4 py-6 md:px-8">
      <div class="mx-auto w-full max-w-5xl">
        <router-view />
      </div>
    </n-layout-content>
  </n-layout>
</template>
