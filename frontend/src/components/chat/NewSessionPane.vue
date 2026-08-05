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
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAgentStore } from '@/stores/agent'
import { useSessionStore } from '@/stores/session'
import type { ChatSession, ConfigOption } from '@/types/models'
import Composer, {
  type ComposerSubmitPayload,
} from '@/components/chat/Composer.vue'

const props = defineProps<{ workspaceId?: number }>()

const { t } = useI18n()
const router = useRouter()
const agentStore = useAgentStore()
const sessionStore = useSessionStore()

/** 可用 agent 列表（未运行项可点击，点击时按需启动对应 agent） */
const agents = computed(() => agentStore.list)

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
 * 为指定 agent 创建隐式草稿会话，并加载其 configOptions。
 * 切 tab 前先释放旧草稿，避免堆积空 ACP session。
 * 注意：draftCreating 必须先置 true（在释放旧草稿之前），
 * 让遮罩全程覆盖输入卡片，避免「释放→创建」间隙内容区空窗导致的跳动闪烁。
 */
async function createDraftFor(agentId: string) {
  if (!agentId || draftCreating.value) return
  draftCreating.value = true
  try {
    // 释放上一个草稿（如有）
    if (draftSession.value) {
      await releaseDraft()
    }

    const { session, configOptions } = await sessionStore.createSession(
      agentId,
      props.workspaceId,
      true, // isDraft
    )
    if (!alive) {
      // 用户在草稿创建期间已切走（组件卸载）：立即释放刚创建的草稿，
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
  } catch (e) {
    sessionStore.streamError = e instanceof Error ? e.message : String(e)
  } finally {
    draftCreating.value = false
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
  if (!text || sessionStore.streaming || !draftSession.value) {
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
            class="bg-gradient-to-b from-slate-900 to-slate-500 bg-clip-text text-3xl font-semibold tracking-tight text-transparent"
          >
            {{ t('chat.welcomeSubtitle') }}
          </h1>
          <!-- 灰色工作区提示：告知用户会话将创建在哪个目录下 -->
          <p v-if="workspacePath" class="text-sm text-slate-400">
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
                ? 'border-slate-800 font-medium text-slate-900'
                : 'border-transparent text-slate-500 hover:text-slate-700',
            ]"
            @click="onSwitchAgent(a.agentId)"
          >
            <span class="flex items-center gap-1.5">
              <!-- 运行状态圆点：绿=运行中，灰=未运行（点击后自动启动） -->
              <span
                class="inline-block h-1.5 w-1.5 rounded-full"
                :class="a.running ? 'bg-emerald-500' : 'bg-slate-300'"
              ></span>
              {{ a.name }}
            </span>
          </button>
        </div>

        <!-- 加载态：切换 agent 时以半透明遮罩覆盖输入卡片（内容保留原位，避免整块替换造成的跳动闪烁）；
             草稿创建完成（session/new 启动 agent）后遮罩消失，配置项随之更新 -->
        <div v-if="sessionStore.streamError" class="flex w-full items-center justify-between rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 ring-1 ring-inset ring-red-100">
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

        <!-- 融合输入框容器：配置项（模型/工作模式/思维强度）与输入框一体化，风格与 /sessions/:id 一致 -->
        <div class="relative w-full">
          <div
            v-if="draftCreating"
            class="absolute inset-0 z-10 flex items-center justify-center rounded-2xl bg-white/70 backdrop-blur-[1px]"
          >
            <span class="flex items-center gap-2 text-sm text-slate-400">
              <span class="inline-block h-4 w-4 animate-spin rounded-full border-2 border-slate-300 border-t-slate-600"></span>
              {{ t('chat.loadingAgent') }}
            </span>
          </div>
          <Composer
            ref="composerRef"
            mode="card"
            :agent-id="selectedAgentId"
            :sending="sessionStore.streaming"
            @submit="onSubmit"
            @cancel="sessionStore.cancelSend()"
          />
        </div>
      </div>
    </div>
  </div>
</template>
