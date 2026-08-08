<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { NIcon } from 'naive-ui'
import { CaretBackOutline, CaretForwardOutline } from '@vicons/ionicons5'
import { useAgentStore } from '@/stores/agent'
import { useSessionStore } from '@/stores/session'
import { useAppStore } from '@/stores/app'
import WelcomeHero from '@/components/chat/WelcomeHero.vue'
import NewSessionPane from '@/components/chat/NewSessionPane.vue'
import MessageList from '@/components/chat/MessageList.vue'
import PlanDock from '@/components/chat/PlanDock.vue'
import Composer, {
  type ComposerSubmitPayload,
} from '@/components/chat/Composer.vue'

const { t } = useI18n()
const route = useRoute()
const agentStore = useAgentStore()
const sessionStore = useSessionStore()
const appStore = useAppStore()

/** 右侧面板折叠按钮主题：纯灰图标、无 hover 背景（不用 quaternary/primary）；暗色下换亮一档 */
const toggleBtnTheme = computed(() =>
  appStore.isDark
    ? {
        textColor: '#64748b', // slate-500
        textColorHover: '#94a3b8', // slate-400
        textColorPressed: '#94a3b8',
        textColorFocus: '#94a3b8',
      }
    : {
        textColor: '#94a3b8', // slate-400：收起态
        textColorHover: '#475569', // slate-600：hover 仅加深灰色，不出现背景
        textColorPressed: '#475569',
        textColorFocus: '#475569',
      },
)

/** 右侧文件面板折叠状态（状态在 AppShell，这里只展示按钮并转发切换事件） */
defineProps<{ rightOpen: boolean }>()
const emit = defineEmits<{ 'toggle-right-panel': [] }>()

/** 路由态：home（无项目引导）/ new（新建会话空态）/ session（已有会话） */
const routeName = computed(() => route.name as string)

/** 当前路由 sessionId；仅 session 态有效 */
const sessionId = computed(() => {
  const raw = route.params.sessionId
  return raw ? Number(raw) : null
})

/** /new 路由预选的项目 id（?workspaceId=X；未指定时后端回退默认工作区） */
const newWorkspaceId = computed(() => {
  const raw = route.query.workspaceId
  return raw ? Number(raw) : undefined
})

/** 进入 /sessions/:id 时：同步选中 + 按需加载消息历史与配置项 */
watch(
  sessionId,
  (id) => {
    sessionStore.currentId = id
    if (id !== null) {
      void sessionStore.loadMessages(id).catch(() => {
        // 历史加载失败不阻塞界面；后续可加重试
      })
      // 加载会话配置项（模型/思考强度/mode；agent 不支持时为空，前端隐藏）
      void sessionStore.loadConfigOptions(id)
      // 加载可用 / 命令（agent 未通告时为空，前端不显示候选面板）
      void sessionStore.loadSlashCommands(id)
    }
  },
  { immediate: true },
)

/** 当前会话对象；null → 非会话态 */
const current = computed(() => sessionStore.activeSession)

/** 会话头部 Agent 标签文案 */
function agentNameOf(agentId: string): string {
  return agentStore.list.find((a) => a.agentId === agentId)?.name ?? agentId
}

/**
 * 已有会话发送：直接走 store.sendViaWs（WS prompt + 流式事件）。
 * 空态创建由 NewSessionPane 处理（隐式草稿 → 发首条消息即转正）。
 */
async function onSubmit(payload: ComposerSubmitPayload) {
  const text = payload.text.trim()
  // 本会话非空闲时不发送（防止 Composer 停用状态下的竞态双击）
  if (!text || sessionStore.statusOf(current.value?.id) !== 'idle') {
    return
  }

  const session = current.value
  if (!session) {
    return
  }
  try {
    await sessionStore.sendViaWs(session.id, text)
  } catch (e) {
    sessionStore.setSessionStreamError(session.id, e instanceof Error ? e.message : String(e))
  }
}

/** 无项目首屏「新建项目」：打开共享弹窗（AppSidebar 监听同一 flag） */
function onNewProjectFromHero() {
  appStore.newProjectModalOpen = true
}
</script>

<template>
  <!-- 已有会话：对话列表 + bar 输入（agent 已锁定，无切换 tab） -->
  <template v-if="routeName === 'session' && current">
    <div class="flex min-h-0 flex-1 flex-col">
      <!-- 会话头部：Agent 标签（左）+ 标题 + 右侧面板开关（最右） -->
      <div class="flex items-center gap-2 border-b border-divider px-4 py-2.5">
        <span
          class="shrink-0 rounded bg-surface-hover px-1.5 py-0.5 text-xs text-ink-muted"
        >
          {{ agentNameOf(current.agentId) }}
        </span>
        <span class="min-w-0 flex-1 truncate text-sm font-medium text-ink">
          {{ current.title || t('chat.newChatTitle') }}
        </span>
        <!-- 右侧面板（信息|文件|Git）展开/收起：箭头随状态指向收起方向，灰色系无 hover 背景 -->
        <n-button
          text
          circle
          size="small"
          :theme-overrides="toggleBtnTheme"
          title="侧边面板"
          @click="emit('toggle-right-panel')"
        >
          <template #icon>
            <n-icon>
              <CaretForwardOutline v-if="rightOpen" />
              <CaretBackOutline v-else />
            </n-icon>
          </template>
        </n-button>
      </div>

      <div class="relative min-h-0 flex-1">
        <MessageList class="h-full min-h-0" />
        <PlanDock
          :session-id="current.id"
          class="absolute left-2 top-1/2 z-20 -translate-y-1/2"
        />
      </div>

      <!-- 当前会话发送/流式错误条（按 session 隔离） -->
      <div
        v-if="sessionStore.streamErrorOf(current.id)"
        class="mx-4 mb-2 flex items-center justify-between rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 ring-1 ring-inset ring-red-100 dark:bg-red-950/40 dark:text-red-400 dark:ring-red-900/50"
      >
        <span class="truncate">
          {{ t('chat.errorTitle') }}: {{ sessionStore.streamErrorOf(current.id) }}
        </span>
        <button
          class="ml-3 shrink-0 text-red-400 hover:text-red-600"
          aria-label="close"
          @click="sessionStore.clearSessionStreamError(current.id)"
        >
          ✕
        </button>
      </div>

      <!-- 底部输入条：与 AI 内容共用 content-container 宽度（max-w-4xl 居中）；
           左右不加 padding/margin，输入框卡片直接占满容器全宽 -->
      <div class="content-container pb-4 pt-2">
        <Composer
          mode="bar"
          :agent-id="current.agentId"
          :status="sessionStore.statusOf(current.id)"
          @submit="onSubmit"
          @cancel="sessionStore.cancelSend(current.id)"
        />
      </div>
    </div>
  </template>

  <!-- session 态但会话对象尚未解析（转正补列表前的瞬时窗口/会话不存在）：加载占位，避免误入欢迎页 -->
  <div
    v-else-if="routeName === 'session'"
    class="flex min-h-0 flex-1 items-center justify-center text-sm text-ink-muted"
  >
    {{ t('chat.loadingSession') }}
  </div>

  <!-- 新建会话空态：agent tab 选择 + 隐式草稿预览配置项 -->
  <NewSessionPane
    v-else-if="routeName === 'new'"
    :workspace-id="newWorkspaceId"
  />

  <!-- 无项目首屏 / 空态引导 -->
  <WelcomeHero v-else @new-project="onNewProjectFromHero" />
</template>
