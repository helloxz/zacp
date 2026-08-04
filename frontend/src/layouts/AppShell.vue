<script setup lang="ts">
import { onMounted, ref } from 'vue'
import AppSidebar from '@/components/shell/AppSidebar.vue'
import ChatPane from '@/components/chat/ChatPane.vue'
import SettingsDrawer from '@/components/shell/SettingsDrawer.vue'
import { useAgentStore } from '@/stores/agent'
import { useSessionStore } from '@/stores/session'
import { acpSocket } from '@/composables/useAcpSocket'

/** 设置抽屉开关（由 UserFooter 齿轮触发；壳层级状态，覆盖全屏） */
const settingsOpen = ref(false)

function openSettings() {
  settingsOpen.value = true
}

/** 侧栏折叠状态（localStorage 持久化，PC 桌面布局） */
const SIDEBAR_KEY = 'zacp.sidebarCollapsed'
const sidebarCollapsed = ref(
  typeof localStorage !== 'undefined'
    ? localStorage.getItem(SIDEBAR_KEY) === '1'
    : false,
)

function toggleSidebar() {
  sidebarCollapsed.value = !sidebarCollapsed.value
  if (typeof localStorage !== 'undefined') {
    localStorage.setItem(SIDEBAR_KEY, sidebarCollapsed.value ? '1' : '0')
  }
}

/** 首屏：拉取 agent 列表 + 工作区 + 最近会话（并行，失败不阻塞壳层渲染）+ 建立 WS 长连接 */
const agentStore = useAgentStore()
const sessionStore = useSessionStore()
onMounted(() => {
  void agentStore.load().catch(() => {})
  void sessionStore.loadInitial().catch(() => {})
  acpSocket.connect()
})
</script>

<template>
  <!-- 壳层骨架：左固定侧栏 + 右对话主区；整页内部滚动，避免 body 双滚动条（设计文档 §3） -->
  <div class="flex h-screen overflow-hidden bg-white">
    <AppSidebar
      class="shrink-0"
      :collapsed="sidebarCollapsed"
      @toggle="toggleSidebar"
      @open-settings="openSettings"
    />
    <main class="flex min-w-0 flex-1 flex-col">
      <ChatPane />
    </main>
    <SettingsDrawer :show="settingsOpen" @update:show="settingsOpen = $event" />
  </div>
</template>
