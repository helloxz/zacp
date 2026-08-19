<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { FolderOutline, MenuOutline } from '@vicons/ionicons5'
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
 * 右侧面板（信息|文件|Git）折叠状态：默认收起。
 * 展开/收起按钮在会话标题栏最右侧（ChatPane 转发切换事件）。
 *
 * 状态来源：进入会话页时按【设置-系统设置】的「右侧边栏自动展开」偏好恢复
 * （localStorage，由 appStore 管理与持久化）；会话内手动切换经由 toggleRightPanel
 * 写回同一偏好，二者双向同步——即「自动展开 = 记住右侧面板最后的状态」。
 *
 * 关键约束：非会话态（/ 与 /new）一律强制收起——新建对话时不得展示右侧面板，
 * 且 /new 下没有切换按钮，若残留展开态用户将无法关闭。
 * watch 默认 pre 刷新（渲染前执行），路由切换不会出现「先展开后收起」的闪帧。
 * 只收起、不重置：FilePanel 一直挂载在 DOM（w-0 + overflow-hidden），内部状态保留。
 */
const rightPanelOpen = ref(false)

/** 会话标题栏手动切换右侧面板；写回 appStore 偏好，使设置开关与面板状态保持同步 */
function toggleRightPanel() {
  rightPanelOpen.value = !rightPanelOpen.value
  appStore.setRightPanelAutoExpand(rightPanelOpen.value)
}

/**
 * 移动端左侧栏抽屉开关（<lg 生效；lg 及以上侧栏常驻流内，与现状一致）。
 * 默认收起：手机上首屏只显示中间内容区。
 * 打开方式：壳层移动顶栏汉堡按钮；关闭方式：遮罩点击 / 路由切换。
 * 注意：必须声明在下方 watch 之前——watch 的 immediate 回调会同步执行，
 * 若晚声明会在 const 初始化前访问（TDZ 抛错）。
 */
const mobileSidebarOpen = ref(false)

/**
 * 桌面断点（lg=1024px）实时判定，两个用途：
 * 1. 窗口从手机拉宽到桌面时强制收起抽屉——否则遮罩消失但状态残留，再缩回手机抽屉会自己弹出；
 * 2. 桌面下侧栏是流内常驻，不能被 inert 禁用（inert 只在移动端关闭态生效）。
 */
const isDesktop = ref(false)
const mqlDesktop = window.matchMedia('(min-width: 1024px)')
isDesktop.value = mqlDesktop.matches
function onDesktopChange(e: MediaQueryListEvent) {
  isDesktop.value = e.matches
  if (e.matches) mobileSidebarOpen.value = false
}
mqlDesktop.addEventListener('change', onDesktopChange)
// AppShell 是根布局、生命周期等同 app，但按习惯补上清理，避免组件复用时泄漏监听
onUnmounted(() => mqlDesktop.removeEventListener('change', onDesktopChange))

const route = useRoute()
const router = useRouter()
watch(
  () => route.name,
  (name) => {
    if (name === 'session') {
      // 进入会话页：按「右侧边栏自动展开」偏好恢复（已持久化；默认 false 则保持收起）
      rightPanelOpen.value = appStore.rightPanelAutoExpand
    } else {
      rightPanelOpen.value = false
    }
    // 路由切换时自动关闭移动端侧栏抽屉（避免切页后残留遮罩盖住内容）
    mobileSidebarOpen.value = false
  },
  { immediate: true },
)

const { t } = useI18n()
const sessionStore = useSessionStore()

/** 移动顶栏标题：session 态显示会话标题，new 态显示「新建会话」，home 态显示应用名 */
const topBarTitle = computed(() => {
  if (route.name === 'session') {
    return sessionStore.activeSession?.title || t('chat.newChatTitle')
  }
  if (route.name === 'new') return t('shell.newSession')
  return t('common.appName')
})

function openSettings() {
  appStore.settingsOpen = true
}

/** 跳转手机端独立文件页（session 态文件夹按钮）；仅移动端入口（按钮所在顶栏 lg:hidden），PC 无此跳转 */
function openFiles() {
  const id = sessionStore.activeSession?.id
  if (id) {
    void router.push({ name: 'files', params: { sessionId: String(id) } })
  }
}

/** 首屏：拉取 agent 列表 + 工作区 + 最近会话（并行，失败不阻塞壳层渲染）+ 建立 WS 长连接 */
const agentStore = useAgentStore()
onMounted(() => {
  void agentStore.load().catch(() => {})
  void sessionStore.loadInitial().catch(() => {})
  void acpSocket.connect()
})
</script>

<template>
  <!-- 壳层骨架：左固定侧栏 + 中对话主区 + 右文件信息栏；整页内部滚动，避免 body 双滚动条（设计文档 §3）。
       h-screen(100vh) 在 iOS Safari 会随地址栏收起/展开跳动，用 supports 变体在支持 100dvh 的浏览器回退为动态视口高度。 -->
  <div class="flex h-screen supports-[height:100dvh]:h-dvh overflow-hidden bg-surface-raised">
    <!-- 移动端侧栏遮罩：打开抽屉时覆盖主区，点击关闭（lg 及以上不渲染） -->
    <div
      v-if="mobileSidebarOpen"
      class="fixed inset-0 z-40 bg-black/40 lg:hidden"
      @click="mobileSidebarOpen = false"
    />

    <!-- 左侧栏：lg 及以上流内常驻（现状）；lg 以下 fixed overlay 抽屉，位置由 open 控制。
         desktop 传入 isDesktop 供 aside 的 inert 判断（多根组件无法透传 attr，须经 prop）。
         抽屉关闭仅靠遮罩点击 / 路由切换，AppSidebar 内部不提供关闭按钮。 -->
    <AppSidebar
      :open="mobileSidebarOpen"
      :desktop="isDesktop"
      @open-settings="openSettings"
    />

    <main class="flex min-w-0 flex-1 flex-col">
      <!-- 移动端顶栏（lg:hidden）：汉堡按钮 + 当前页标题。
           放在壳层而非 ChatPane：home/new 态没有自己的标题栏，但新建项目/会话入口都在侧栏里，
           必须保证手机用户在三个路由态都能打开侧栏。 -->
      <header
        class="flex min-h-12 shrink-0 items-center gap-2 border-b border-divider bg-surface pt-[env(safe-area-inset-top)] pl-[max(env(safe-area-inset-left),0.75rem)] pr-[max(env(safe-area-inset-right),0.75rem)] lg:hidden"
      >
        <button
          type="button"
          aria-label="打开侧栏"
          class="-ml-1 flex h-9 w-9 cursor-pointer items-center justify-center rounded-lg text-ink-secondary transition-colors hover:bg-surface-hover active:bg-surface-active focus:outline-none focus-visible:ring-2 focus-visible:ring-primary/50"
          @click="mobileSidebarOpen = true"
        >
          <MenuOutline class="h-5 w-5" />
        </button>
        <span class="min-w-0 flex-1 truncate text-sm font-medium text-ink">
          {{ topBarTitle }}
        </span>
        <!-- 文件页入口（仅 session 态；lg:hidden 由顶栏整体控制）：手机端右侧面板已隐藏，
             文件浏览移到独立页 /sessions/:id/files（PC 继续用右侧面板，不走此按钮） -->
        <button
          v-if="route.name === 'session'"
          type="button"
          aria-label="查看文件"
          class="flex h-9 w-9 shrink-0 cursor-pointer items-center justify-center rounded-lg text-ink-secondary transition-colors hover:bg-surface-hover active:bg-surface-active focus:outline-none focus-visible:ring-2 focus-visible:ring-primary/50"
          @click="openFiles"
        >
          <FolderOutline class="h-5 w-5" />
        </button>
      </header>

      <ChatPane
        :right-open="rightPanelOpen"
        @toggle-right-panel="toggleRightPanel"
      />
    </main>

    <!-- 右侧面板：仅 lg 及以上可用（手机端不渲染、无入口）；展开/收起用宽度动画（收起时不占空间） -->
    <div
      class="hidden shrink-0 overflow-hidden transition-[width] duration-200 lg:flex"
      :class="rightPanelOpen ? 'w-80' : 'w-0'"
    >
      <FilePanel class="h-full w-80" />
    </div>
    <SettingsModal :show="appStore.settingsOpen" @update:show="appStore.settingsOpen = $event" />
  </div>
</template>