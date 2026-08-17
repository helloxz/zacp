<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useMessage, type DropdownOption } from 'naive-ui'
import {
  ChevronBackOutline,
  ChevronForwardOutline,
  OpenOutline,
  TerminalOutline,
} from '@vicons/ionicons5'
import HeaderIconButton from '@/components/chat/HeaderIconButton.vue'
import TurnIndicator from '@/components/chat/TurnIndicator.vue'
import { acpSocket } from '@/composables/useAcpSocket'
import { fetchExternalTools, openSessionTool } from '@/api'
import { useAgentStore } from '@/stores/agent'
import { useSessionStore, MAX_TURNS_PER_SESSION } from '@/stores/session'
import { useAppStore } from '@/stores/app'
import type { ExternalTool } from '@/types/models'
import WelcomeHero from '@/components/chat/WelcomeHero.vue'
import NewSessionPane from '@/components/chat/NewSessionPane.vue'
import MessageList from '@/components/chat/MessageList.vue'
import PlanDock from '@/components/chat/PlanDock.vue'
import Composer, {
  type ComposerSubmitPayload,
} from '@/components/chat/Composer.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const agentStore = useAgentStore()
const sessionStore = useSessionStore()
const appStore = useAppStore()

const message = useMessage()
const externalTools = ref<ExternalTool[]>([])
const externalToolsLoaded = ref(false)
const externalToolOpening = ref<string | null>(null)
let externalToolsLoading = false

/** 工具列表是本机全局能力；首次进入真实 Session 时加载一次，避免每次切会话重复探测。 */
async function loadExternalTools() {
	if (externalToolsLoaded.value || externalToolsLoading) return
	externalToolsLoading = true
	try {
		externalTools.value = await fetchExternalTools()
	} catch {
		// 工具探测失败不阻塞对话页面；菜单保持隐藏，点击其它功能仍正常。
		externalTools.value = []
	} finally {
		externalToolsLoaded.value = true
		externalToolsLoading = false
	}
}

const externalToolOptions = computed<DropdownOption[]>(() =>
	externalTools.value.map((tool) => ({
		key: tool.id,
		label: tool.label,
	})),
)

/** 点击菜单后仅提交白名单 ID；目录由后端从当前 Session 解析，前端不传路径。 */
async function onExternalToolSelect(key: string | number) {
	const session = current.value
	const toolID = String(key)
	if (!session || !toolID || externalToolOpening.value !== null) return

	externalToolOpening.value = toolID
	try {
		await openSessionTool(session.id, toolID)
		message.success(t('chat.toolOpened'))
	} catch {
		message.error(t('chat.toolOpenFailed'))
	} finally {
		externalToolOpening.value = null
	}
}

/** 在新浏览器 Tab 打开当前会话工作区的 Web TTY；窗口被拦截时明确提示用户。 */
function openWebTTY() {
  const workspaceId = current.value?.workspaceId
  if (!workspaceId) {
    message.error(t('chat.ttyWorkspaceUnavailable'))
    return
  }
  const target = router.resolve({ name: 'tty', query: { workspaceId: String(workspaceId) } })
  const opened = window.open(target.href, '_blank', 'noopener,noreferrer')
  if (!opened) message.warning(t('chat.ttyPopupBlocked'))
}

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

/** 进入 /sessions/:id 时：同步选中 + 以 GET /sessions/:id 为权威校验会话存在性。
 * 校验成功才加载消息历史/配置项/命令（见 store.resolveSession）；
 * 失败按原因置错误态（后端未启动 → network、id 不存在 → not_found、超时/其它 → error），
 * 避免「后端未启动 / 会话不存在」时界面永远卡在「加载会话中…」。 */
watch(
  sessionId,
  (id) => {
    sessionStore.currentId = id
    if (id !== null) {
      void loadExternalTools()
      void sessionStore.resolveSession(id)
    }
  },
  { immediate: true },
)

/** 解析失败标题（按原因分类：不存在 / 网络 / 其它） */
const sessionResolveTitle = computed(() => {
  switch (sessionStore.sessionResolve.status) {
    case 'not_found':
      return t('chat.sessionNotFound')
    case 'network':
      return t('chat.sessionNetworkError')
    case 'error':
      return t('chat.sessionLoadFailed')
    default:
      return ''
  }
})

/** 解析失败态（非 idle/loading/ready 时非空），驱动错误面板渲染 */
const sessionResolveError = computed(() => {
  const st = sessionStore.sessionResolve.status
  if (st === 'idle' || st === 'loading' || st === 'ready') {
    return null
  }
  return {
    title: sessionResolveTitle.value,
    message: sessionStore.sessionResolve.message,
  }
})

/** 当前会话是否有进行中的 turn（断线提示条件：断线 + 仍在跑才提示） */
const currentTurnActive = computed(() => {
  const id = current.value?.id
  return id !== undefined && ['queued', 'streaming', 'cancelling'].includes(sessionStore.statusOf(id))
})

/**
 * WS 通道断开（未连接/重连退避中/已断开都算；仅作提示，不影响功能）。
 * 注意不能用 status === 'closed' 判断：onclose 会立即进入 connecting 退避重连，
 * 'closed' 只在 token 失效跳登录页等不重连路径常驻。
 */
const wsDisconnected = computed(() => acpSocket.state.status !== 'open')

/** 重试解析：后端恢复 / 网络抖动后重新校验会话 */
function onRetryResolve() {
  const id = sessionId.value
  if (id !== null) {
    void sessionStore.resolveSession(id)
  }
}

/** 错误态「新建会话」入口（不存在的会话无返回价值） */
function onGoNewSession() {
  void router.push({ name: 'new' })
}

/** 当前会话对象；null → 非会话态 */
const current = computed(() => sessionStore.activeSession)

/** 当前会话对话轮次（role=user 消息数；口径见 store.turnCountOf）。0 轮不渲染指示器。 */
const turnCount = computed(() => sessionStore.turnCountOf(current.value?.id))

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
  <!-- session 解析失败（后端未启动 / 会话不存在 / 其它）：错误面板优先于缓存内容，
       避免「列表残留该会话但后端已不可达/已删除」时仍显示可输入的死会话 -->
  <div
    v-if="routeName === 'session' && sessionResolveError"
    class="flex min-h-0 flex-1 flex-col items-center justify-center gap-4 px-6 text-center"
  >
    <div class="text-sm font-medium text-ink">{{ sessionResolveError.title }}</div>
    <div
      v-if="sessionResolveError.message"
      class="max-w-md break-words text-xs text-ink-muted"
    >
      {{ sessionResolveError.message }}
    </div>
    <div class="flex items-center gap-3">
      <n-button size="small" @click="onRetryResolve">
        {{ t('chat.retry') }}
      </n-button>
      <n-button size="small" secondary @click="onGoNewSession">
        {{ t('chat.newChatTitle') }}
      </n-button>
    </div>
  </div>

  <!-- 已有会话：对话列表 + bar 输入（agent 已锁定，无切换 tab） -->
  <template v-else-if="routeName === 'session' && current">
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
        <!-- 对话轮次指示器：圆环展示进度，悬停查看详情；0 轮（草稿/空会话）不显示 -->
        <TurnIndicator v-if="turnCount > 0" :turns="turnCount" />
        <!-- Web TTY：使用当前会话所属工作区，在新浏览器 Tab 打开临时终端。 -->
        <HeaderIconButton :title="t('chat.openWebTTY')" @click="openWebTTY">
          <TerminalOutline />
        </HeaderIconButton>
        <!-- 本地工具：后端仅返回当前平台已安装的白名单工具；悬停展开，点击直接启动。 -->
        <n-dropdown
          v-if="externalToolsLoaded && externalTools.length"
          trigger="hover"
          placement="bottom-end"
          :options="externalToolOptions"
          @select="onExternalToolSelect"
        >
          <HeaderIconButton
            :title="t('chat.openTool')"
            :disabled="externalToolOpening !== null"
          >
            <OpenOutline />
          </HeaderIconButton>
        </n-dropdown>
        <!-- 右侧面板（信息|文件|Git）展开/收起：箭头随状态指向收起方向，图标色随壳 hover 加深 -->
        <HeaderIconButton title="侧边面板" @click="emit('toggle-right-panel')">
          <ChevronForwardOutline v-if="rightOpen" />
          <ChevronBackOutline v-else />
        </HeaderIconButton>
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

      <!-- WS 断线提示：会话仍在本机 running 但实时通道已断；后端任务不受影响，
           重连后由 store 保险丝自动同步收尾（见 session.ts checkStalledTurns） -->
      <div
        v-if="wsDisconnected && currentTurnActive"
        class="mx-4 mb-2 flex items-center justify-between rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-700 ring-1 ring-inset ring-amber-200 dark:bg-amber-950/40 dark:text-amber-400 dark:ring-amber-900/50"
      >
        <span>{{ t('chat.disconnectedBanner') }}</span>
      </div>

      <!-- 底部输入条：与 AI 内容共用 content-container 宽度（max-w-4xl 居中）；
           左右不加 padding/margin，输入框卡片直接占满容器全宽 -->
      <div class="content-container pb-4 pt-2">
        <Composer
          mode="bar"
          :agent-id="current.agentId"
          :status="sessionStore.statusOf(current.id)"
          :turn-limited="turnCount >= MAX_TURNS_PER_SESSION"
          @submit="onSubmit"
          @cancel="sessionStore.cancelSend(current.id)"
        />
      </div>
    </div>
  </template>


  <!-- session 态但会话尚未解析（转正补列表前的瞬时窗口）：加载占位，避免误入欢迎页 -->
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
