<script setup lang="ts">
/**
 * 新建会话空态（路由 /new）：
 *
 * - 顶部 agent tab（单选）：选中哪个 agent 就用它创建会话
 * - 隐式草稿：进入空态即为选中 agent 隐式创建草稿会话（isDraft=true），
 *   拿到 agent 下发的 configOptions（模型/思维程度）直接展示
 * - 切 tab：释放旧隐式草稿（DELETE /sessions/:id/draft）→ 为新 agent 创建隐式草稿
 * - 发首条消息：当前隐式草稿「转正」（后端 HandlePrompt 置 isDraft=false），
 *   并跳转到 /sessions/:id 进入已有会话态，顶部 tab 隐藏、agent 锁定
 *
 * 设计约束：侧栏只展示转正后的会话；草稿不进列表。
 * A 方案代价：每次切 tab = 真实 session/new（启动 agent 子进程），有启动延迟。
 */
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { NIcon } from 'naive-ui'
import { WarningOutline } from '@vicons/ionicons5'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAgentStore } from '@/stores/agent'
import { useAppStore } from '@/stores/app'
import { useSessionStore } from '@/stores/session'
import type { ChatSession, ConfigOption } from '@/types/models'
import Composer, {
  type ComposerSubmitPayload,
} from '@/components/chat/Composer.vue'

const props = defineProps<{ workspaceId?: number }>()

const { t } = useI18n()
const router = useRouter()
const agentStore = useAgentStore()
const appStore = useAppStore()
const sessionStore = useSessionStore()

/** 可用 agent 列表（未运行项可点击，点击时按需启动对应 agent） */
const agents = computed(() => agentStore.list)

/** 一个智能体都没有：输入框不可用，需提示先安装 Agent 并到「设置 - 智能体」启用 */
const noAgent = computed(() => agents.value.length === 0)

/** 打开设置弹窗（默认定位「智能体」目录，SettingsModal activeKey 初始为 agent） */
function goSettings() {
  appStore.settingsOpen = true
}

/** 当前工作区路径（空态提示用）：优先 ?workspaceId 指定的工作区，缺省回退默认工作区 */
const workspacePath = computed(() => {
  const ws =
    props.workspaceId != null
      ? sessionStore.workspaces.find((w) => w.id === props.workspaceId)
      : undefined
  return (ws ?? sessionStore.defaultWorkspace())?.path ?? ''
})

/** 当前选中的 agent id（默认第一个可用 running agent） */
const selectedAgentId = ref('')

/** 当前隐式草稿会话（未转正前保留在本地，不进侧栏） */
const draftSession = ref<ChatSession | null>(null)
/** 当前草稿的配置项（模型/思维程度；来自 createSession 响应） */
const draftConfigOptions = ref<ConfigOption[]>([])
/** 草稿创建中（驱动 tab 切换时的加载态） */
const draftCreating = ref(false)

/** 组件存活标记：卸载后到达的异步回调不得再写 currentId（同 releaseDraft 的竞态） */
let alive = true

/**
 * 创建请求代际号：每次发起 createDraftFor 自增。
 * 异步返回时若 gen 已不等于最新 draftGen，说明期间用户又切了 agent
 * （或组件已卸载），本次请求作废：只做清理（释放已创建的草稿），
 * 不写任何状态、不弹错误。解决「切 tab 时旧请求仍挂起 → 新 agent
 * 一直被 draftCreating 拦截、界面永远转圈」的问题。
 */
let draftGen = 0

/** 当前选中 agent 的显示名（加载遮罩文案用，避免只转圈不知在启动哪个） */
const selectedAgentName = computed(
  () => agents.value.find((a) => a.agentId === selectedAgentId.value)?.name ?? '',
)

/** 当前选中 agent 是否已在运行：遮罩据此区分「正在启动」与「正在创建会话」两种文案 */
const selectedAgentRunning = computed(
  () => agents.value.find((a) => a.agentId === selectedAgentId.value)?.running ?? false,
)

/**
 * 为指定 agent 创建隐式草稿会话，并加载其 configOptions。
 * 切 tab 前先释放旧草稿，避免堆积空 ACP session。
 * 注意：draftCreating 必须先置 true（在释放旧草稿之前），
 * 让遮罩全程覆盖输入卡片，避免「释放→创建」间隙内容区空窗导致的跳动闪烁。
 * draftCreating 只表示「有请求在途」，不再拦截新请求——切换时旧请求
 * 通过代际号作废，新请求立即放行（旧请求返回后自行清理）。
 */
async function createDraftFor(agentId: string) {
  if (!agentId) return
  // 新代际开始：此前未完成的创建请求全部作废
  const gen = ++draftGen
  // 切换即清除上一次的报错横幅，避免旧 agent 的错误串到新 agent 界面
  sessionStore.clearStreamError()
  draftCreating.value = true
  try {
    // 释放上一个草稿（如有）
    if (draftSession.value) {
      await releaseDraft()
    }
    // 释放期间用户可能又切了 tab：本次请求作废，不继续创建
    if (!alive || gen !== draftGen) return

    const { session, configOptions } = await sessionStore.createSession(
      agentId,
      props.workspaceId,
      true, // isDraft
    )
    if (!alive || gen !== draftGen) {
      // 用户在草稿创建期间已切走（卸载或切到其它 agent）：立即释放刚创建的草稿，
      // 避免泄露 ACP session；且不得再写 currentId 覆盖新会话的选中态
      void sessionStore.removeDraftSession(session.id).catch(() => {})
      return
    }
    draftSession.value = session
    draftConfigOptions.value = configOptions
    // 刷新 agent 列表：StartAgent 后 running 变 true，tab 圆点同步变绿
    // （load 幂等；失败仅置 error，不影响草稿流程）
    void agentStore.load()
    // 同步到 sessionStore.configOptions，使 Composer 融合输入框的下拉可复用
    sessionStore.configOptions = configOptions
    // 草稿视为当前会话：Composer 内置的 setConfigOption 用 currentId 定位后端目标会话
    sessionStore.currentId = session.id
    // 加载草稿的可用 / 命令（含静态兜底，如 grok；失败视为空数组，不阻塞草稿流程）。
    // 无此步时 /new 空态输入框拿不到候选，/ 提示不出现。
    await sessionStore.loadSlashCommands(session.id)
  } catch (e) {
    // 过期请求的错误丢弃：用户已切到其它 agent，别把旧 agent 的失败弹到新界面上
    if (gen !== draftGen) return
    sessionStore.streamError = e instanceof Error ? e.message : String(e)
  } finally {
    // 仅最新代际负责清遮罩：旧请求完成时若有更新代际在途，遮罩归新代际管
    if (gen === draftGen) {
      draftCreating.value = false
    }
  }
}

/** 释放当前草稿（切 tab / 离开空态时调用） */
async function releaseDraft() {
  if (!draftSession.value) return
  const draftId = draftSession.value.id
  try {
    await sessionStore.removeDraftSession(draftId)
  } catch {
    // 释放失败不阻塞（后端可能已不存在）
  }
  draftSession.value = null
  draftConfigOptions.value = []
  sessionStore.configOptions = []
  // 守卫式清空：本函数在 await DELETE 后才走到这里，若用户已切到其它会话
  // （ChatPane watch 已把 currentId 设为新会话 id），无条件置 null 会覆盖它，
  // 导致 activeSession 解析失败、界面卡在「加载会话中…」。仅当 currentId
  // 仍是本次释放的草稿时才清空。
  if (sessionStore.currentId === draftId) {
    sessionStore.currentId = null
    // 同守卫清空 / 命令：避免旧草稿的命令残留串到下一个会话
    // （残留会被下一次 loadSlashCommands 覆盖，此处仅保证释放语义干净）。
    sessionStore.slashCommands = []
  }
}

/** 切 tab：切换 agent → 释放旧草稿 → 创建新草稿（未运行的 agent 在此按需启动） */
async function onSwitchAgent(agentId: string) {
  // 已选中且草稿存在则跳过；草稿为空（首次进入/启动失败/空闲回收后）时
  // 点击重试，重新触发 createDraftFor → 后端幂等 StartAgent
  if (agentId === selectedAgentId.value && draftSession.value) return
  selectedAgentId.value = agentId
  await createDraftFor(agentId)
}

/**
 * 发首条消息：通过当前隐式草稿的 WS prompt 发送。
 * 后端 HandlePrompt 收到首条 prompt 即转正草稿（isDraft=false）。
 * 发送后跳转 /sessions/:id，进入已有会话态（顶部 tab 隐藏、agent 锁定）。
 */
async function onSubmit(payload: ComposerSubmitPayload) {
  const text = payload.text.trim()
  // 本草稿非空闲时不发送（排队中/流式中由停止按钮接管）。
  // 项目切换守卫：切项目时草稿重建有窗口期（旧草稿未释放、新草稿未创建完），
  // 期间 draftCreating 遮罩不夺焦点，按 Enter 仍会走到这里——若草稿所属项目
  // 已与当前路由 ?workspaceId 不符，直接拦截，避免把消息发到上一个项目的草稿
  // （否则会话转正后工作路径错挂在旧项目）。仅当路由显式指定项目时校验，
  // 无 ?workspaceId（后端回退默认工作区）时不拦截，保持原有行为。
  if (
    !text ||
    sessionStore.statusOf(draftSession.value?.id) !== 'idle' ||
    !draftSession.value ||
    (props.workspaceId != null &&
      draftSession.value.workspaceId !== props.workspaceId)
  ) {
    return
  }
  const draft = draftSession.value
  try {
    // 草稿不在 sessions 列表，显式传入 session 对象发送（否则 sendViaWs 查不到抛错）
    await sessionStore.sendViaWs(draft.id, text, draft)
    // 后端收到首条 prompt 已转正（isDraft=false）：补进侧栏列表，
    // 跳转 /sessions/:id 后 activeSession 才能解析到该会话
    sessionStore.promoteDraftSession(draft)
    draftSession.value = null // 置空，避免 onUnmounted 重复释放
    await router.push({ name: 'session', params: { sessionId: String(draft.id) } })
  } catch (e) {
    sessionStore.streamError = e instanceof Error ? e.message : String(e)
  }
}

/** 输入卡片引用（草稿创建完成/切 tab 后重新聚焦输入框） */
const composerRef = ref<InstanceType<typeof Composer> | null>(null)

/**
 * agent 列表就绪后默认选中第一个可用 agent 并创建草稿。
 * 列表由 AppShell 异步加载（GET /api/v1/agents），onMounted 时可能仍为空，
 * 因此用 watch 兜底：列表到达即补默认选中（immediate 处理已就绪的情况）。
 */
watch(
  () => agents.value,
  (list) => {
    if (selectedAgentId.value || list.length === 0) {
      return
    }
    const first = list.find((a) => a.running) ?? list[0]
    if (first) {
      selectedAgentId.value = first.agentId
      void createDraftFor(first.agentId)
    }
  },
  { immediate: true },
)

/**
 * 项目切换（?workspaceId 变化，如侧栏在另一项目下点「新建对话」）：
 * 同 /new 路由间跳转时组件实例被 Vue Router 复用，草稿不会自动重建——
 * 若不处理，发送的会话会沿用旧项目草稿，工作路径错挂在上一项目。
 * 复用 createDraftFor（内部先释放旧草稿 + draftGen 代际号作废在途请求），
 * 连续快速切换项目也不会串项目。selectedAgentId 为空（agent 列表未就绪）
 * 时跳过，由上面的 agents watch 在列表到达后用最新 props.workspaceId 兜底创建。
 */
watch(
  () => props.workspaceId,
  () => {
    if (!selectedAgentId.value) return
    void createDraftFor(selectedAgentId.value)
  },
)

/** 草稿创建完成（遮罩消失）后重新聚焦输入框：切 tab 后可直接打字 */
watch(draftCreating, (creating) => {
  if (!creating) {
    void nextTick(() => composerRef.value?.focus())
  }
})

/** 离开空态时释放未转正的草稿（组件卸载） */
onUnmounted(() => {
  alive = false
  // 未转正的草稿在离开空态时释放（不 await，避免阻塞导航）
  if (draftSession.value) {
    void releaseDraft()
  }
})
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <!-- 空态内容区：居中欢迎语 + Agent tab + 融合输入卡片 -->
    <div
      class="relative flex min-h-0 flex-1 flex-col items-center justify-center overflow-hidden px-6"
    >
      <div class="flex w-full max-w-[680px] flex-col items-center gap-6">
        <div class="space-y-4 text-center">
          <h1
            class="bg-gradient-to-b from-slate-900 to-slate-500 bg-clip-text text-3xl font-semibold tracking-tight text-transparent dark:from-slate-100 dark:to-slate-400"
          >
            {{ t('chat.welcomeSubtitle') }}
          </h1>
          <!-- 灰色工作区提示：告知用户会话将创建在哪个目录下 -->
          <p v-if="workspacePath" class="text-sm text-ink-muted">
            {{ t('chat.newSessionPathHint', { path: workspacePath }) }}
          </p>
        </div>

        <!-- Agent 选择 tab：居中显示在欢迎语下方（三个 agent）；
             未运行 agent 也可点击，点击时按需启动（创建草稿会触发后端 StartAgent），
             绿色圆点=运行中，灰色圆点=未运行；对话后路由切到 session 态，本组件卸载、tab 自然隐藏 -->
        <div class="flex items-center justify-center gap-1">
          <button
            v-for="a in agents"
            :key="a.agentId"
            type="button"
            :title="a.running ? a.name : t('chat.agentNotRunning')"
            :class="[
              'shrink-0 cursor-pointer rounded-t-md border-b-2 px-4 py-1.5 text-sm transition-colors',
              a.agentId === selectedAgentId
                ? 'border-ink font-medium text-ink'
                : 'border-transparent text-ink-muted hover:text-ink-secondary',
            ]"
            @click="onSwitchAgent(a.agentId)"
          >
            <span class="flex items-center gap-1.5">
              <!-- 运行状态圆点：绿=运行中，灰=未运行（点击后自动启动） -->
              <span
                class="inline-block h-1.5 w-1.5 rounded-full"
                :class="a.running ? 'bg-emerald-500' : 'bg-slate-300 dark:bg-slate-600'"
              ></span>
              {{ a.name }}
            </span>
          </button>
        </div>

        <!-- 加载态：切换 agent 时以半透明遮罩覆盖输入卡片（内容保留原位，避免整块替换造成的跳动闪烁）；
             草稿创建完成（session/new 启动 agent）后遮罩消失，配置项随之更新 -->
        <div v-if="sessionStore.streamError" class="flex w-full items-center justify-between rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 ring-1 ring-inset ring-red-100 dark:bg-red-950/40 dark:text-red-400 dark:ring-red-900/50">
          <span class="truncate">
            {{ t('chat.errorTitle') }}: {{ sessionStore.streamError }}
          </span>
          <button
            class="ml-3 shrink-0 text-red-400 hover:text-red-600"
            aria-label="close"
            @click="sessionStore.clearStreamError()"
          >
            ✕
          </button>
        </div>

        <!-- 无可用智能体：输入框上方醒目的引导提示（amber 警示色，亮暗双主题兼容）。
             此时无 agent 可选，Composer 发送按钮天然禁用（card 模式要求已选 agent），
             提示条引导用户先安装 Agent 再到「设置 - 智能体」启用 -->
        <div
          v-if="noAgent"
          class="flex w-full items-center justify-between gap-3 rounded-xl bg-amber-50 px-4 py-3 ring-1 ring-inset ring-amber-300/60 dark:bg-amber-950/30 dark:ring-amber-700/50"
        >
          <span class="flex items-center gap-2 text-sm text-amber-700 dark:text-amber-300">
            <n-icon :size="18"><WarningOutline /></n-icon>
            <span>{{ t('chat.noAgentHint') }}</span>
          </span>
          <button
            type="button"
            class="shrink-0 cursor-pointer rounded-lg bg-amber-500 px-3 py-1.5 text-sm font-medium text-white shadow-sm transition-colors hover:bg-amber-600 focus:outline-none focus-visible:ring-2 focus-visible:ring-amber-400/60 dark:bg-amber-600 dark:hover:bg-amber-500"
            @click="goSettings"
          >
            {{ t('chat.goSettings') }}
          </button>
        </div>

        <!-- 融合输入框容器：配置项（模型/工作模式/思维强度）与输入框一体化，风格与 /sessions/:id 一致 -->
        <div class="relative w-full">
          <div
            v-if="draftCreating"
            class="absolute inset-0 z-10 flex items-center justify-center rounded-2xl bg-surface-raised/70 backdrop-blur-[1px]"
          >
            <span class="flex items-center gap-2 text-sm text-ink-muted">
              <span class="inline-block h-4 w-4 animate-spin rounded-full border-2 border-slate-300 border-t-slate-600 dark:border-slate-600 dark:border-t-slate-300"></span>
              {{ selectedAgentRunning
                ? t('chat.creatingSession') + (selectedAgentName ? ' ' + selectedAgentName : '')
                : t('chat.loadingAgent') + (selectedAgentName ? ' ' + selectedAgentName : '') }}
            </span>
          </div>
          <Composer
            ref="composerRef"
            mode="card"
            :agent-id="selectedAgentId"
            :status="sessionStore.statusOf(draftSession?.id)"
            :workspace-id="props.workspaceId"
            @submit="onSubmit"
            @cancel="sessionStore.cancelSend(draftSession?.id)"
          />
        </div>
      </div>
    </div>
  </div>
</template>
