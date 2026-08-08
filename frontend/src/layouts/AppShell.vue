<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import AppSidebar from '@/components/shell/AppSidebar.vue'
import ChatPane from '@/components/chat/ChatPane.vue'
import FilePanel from '@/components/files/FilePanel.vue'
import SettingsModal from '@/components/shell/SettingsModal.vue'
import { useAgentStore } from '@/stores/agent'
import { useAppStore } from '@/stores/app'
import { useSessionStore } from '@/stores/session'
import { acpSocket } from '@/composables/useAcpSocket'

/**
 * 设置弹窗开关：状态放 appStore（与 newProjectModalOpen 同模式），
 * 供 UserFooter 齿轮与各页面「前往设置」入口（如无智能体提示条）共享。
 */
const appStore = useAppStore()

/**
 * 右侧面板（信息|文件|Git）折叠状态：默认收起（不持久化）。
 * 展开/收起按钮在会话标题栏最右侧（ChatPane 转发切换事件）。
 *
 * 关键约束：非会话态（/ 与 /new）一律强制收起——新建对话时不得展示右侧面板，
 * 且 /new 下没有切换按钮，若残留展开态用户将无法关闭。
 * watch 默认 pre 刷新（渲染前执行），路由切换不会出现「先展开后收起」的闪帧。
 * 只收起、不重置：FilePanel 一直挂载在 DOM（w-0 + overflow-hidden），内部状态保留。
 */
const rightPanelOpen = ref(false)
const route = useRoute()
watch(
  () => route.name,
  (name) => {
    if (name !== 'session') {
      rightPanelOpen.value = false
    }
  },
  { immediate: true },
)

function openSettings() {
  appStore.settingsOpen = true
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
    <SettingsModal :show="appStore.settingsOpen" @update:show="appStore.settingsOpen = $event" />
  </div>
</template>
