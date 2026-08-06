<script setup lang="ts">
import { onMounted, ref } from 'vue'
import AppSidebar from '@/components/shell/AppSidebar.vue'
import ChatPane from '@/components/chat/ChatPane.vue'
import FilePanel from '@/components/files/FilePanel.vue'
import SettingsModal from '@/components/shell/SettingsModal.vue'
import { useAgentStore } from '@/stores/agent'
import { useSessionStore } from '@/stores/session'
import { acpSocket } from '@/composables/useAcpSocket'

/** 设置弹窗开关（由 UserFooter 齿轮触发；壳层级状态，覆盖全屏） */
const settingsOpen = ref(false)

/**
 * 右侧面板（信息|文件|Git）折叠状态：默认收起（不持久化）。
 * 展开/收起按钮在会话标题栏最右侧（ChatPane 转发切换事件）。
 */
const rightPanelOpen = ref(false)

function openSettings() {
  settingsOpen.value = true
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
  <!-- 壳层骨架：左固定侧栏 + 中对话主区 + 右文件信息栏；整页内部滚动，避免 body 双滚动条（设计文档 §3） -->
  <div class="flex h-screen overflow-hidden bg-surface-raised">
    <AppSidebar class="shrink-0" @open-settings="openSettings" />
    <main class="flex min-w-0 flex-1 flex-col">
      <ChatPane
        :right-open="rightPanelOpen"
        @toggle-right-panel="rightPanelOpen = !rightPanelOpen"
      />
    </main>
    <!-- 右侧面板：默认折叠；展开/收起用宽度动画（收起时不占空间） -->
    <div
      class="shrink-0 overflow-hidden transition-[width] duration-200"
      :class="rightPanelOpen ? 'w-80' : 'w-0'"
    >
      <FilePanel class="h-full w-80" />
    </div>
    <SettingsModal :show="settingsOpen" @update:show="settingsOpen = $event" />
  </div>
</template>
