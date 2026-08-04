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
import { computed, onMounted, onUnmounted, ref } from 'vue'
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

/** 可用 agent 列表（含未运行项，供展示但禁用） */
const agents = computed(() => agentStore.list)

/** 当前选中的 agent id（默认第一个可用 running agent） */
const selectedAgentId = ref('')

/** 当前隐式草稿会话（未转正前保留在本地，不进侧栏） */
const draftSession = ref<ChatSession | null>(null)
/** 当前草稿的配置项（模型/思维程度；来自 createSession 响应） */
const draftConfigOptions = ref<ConfigOption[]>([])
/** 草稿创建中（驱动 tab 切换时的加载态） */
const draftCreating = ref(false)

/** select 型配置项 → 下拉（模型/思考强度/mode） */
const selectConfigOptions = computed(() =>
  draftConfigOptions.value.filter((o) => o.type === 'select'),
)

/**
 * 为指定 agent 创建隐式草稿会话，并加载其 configOptions。
 * 切 tab 前先释放旧草稿，避免堆积空 ACP session。
 */
async function createDraftFor(agentId: string) {
  if (!agentId || draftCreating.value) return
  // 释放上一个草稿（如有）
  if (draftSession.value) {
    await releaseDraft()
  }

  draftCreating.value = true
  try {
    const { session, configOptions } = await sessionStore.createSession(
      agentId,
      props.workspaceId,
      true, // isDraft
    )
    draftSession.value = session
    draftConfigOptions.value = configOptions
    // 同步到 sessionStore.configOptions，使 Composer 下拉可复用
    sessionStore.configOptions = configOptions
  } catch (e) {
    sessionStore.streamError = e instanceof Error ? e.message : String(e)
  } finally {
    draftCreating.value = false
  }
}

/** 释放当前草稿（切 tab / 离开空态时调用） */
async function releaseDraft() {
  if (!draftSession.value) return
  try {
    await sessionStore.removeDraftSession(draftSession.value.id)
  } catch {
    // 释放失败不阻塞（后端可能已不存在）
  }
  draftSession.value = null
  draftConfigOptions.value = []
  sessionStore.configOptions = []
}

/** 切 tab：切换 agent → 释放旧草稿 → 创建新草稿 */
async function onSwitchAgent(agentId: string) {
  if (agentId === selectedAgentId.value) return
  selectedAgentId.value = agentId
  await createDraftFor(agentId)
}

/** 配置项变更：调后端 set_config_option，成功后本地回写 */
async function onConfigChange(optionId: string, valueId: string) {
  if (!draftSession.value) return
  try {
    await sessionStore.setConfigOption(optionId, valueId)
  } catch {
    // 设置失败：保持原值
  }
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

/** 首次进入：默认选中第一个可用 agent 并创建草稿 */
onMounted(async () => {
  if (!selectedAgentId.value) {
    const first = agents.value.find((a) => a.running) ?? agents.value[0]
    if (first) selectedAgentId.value = first.agentId
  }
  if (selectedAgentId.value) {
    await createDraftFor(selectedAgentId.value)
  }
})

/** 离开空态时释放未转正的草稿（组件卸载） */
onUnmounted(() => {
  // 未转正的草稿在离开空态时释放（不 await，避免阻塞导航）
  if (draftSession.value) {
    void releaseDraft()
  }
})
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <!-- 顶部 agent tab（单选；对话后由父级路由切到 session 态时此组件卸载，tab 自然消失） -->
    <div class="border-b border-slate-200 px-4 pt-3">
      <div class="flex items-center gap-1">
        <button
          v-for="a in agents"
          :key="a.agentId"
          type="button"
          :disabled="!a.running"
          :class="[
            'shrink-0 rounded-t-md border-b-2 px-4 py-2 text-sm transition-colors',
            a.agentId === selectedAgentId
              ? 'border-slate-800 font-medium text-slate-900'
              : 'border-transparent text-slate-500 hover:text-slate-700',
            !a.running ? 'cursor-not-allowed opacity-40' : 'cursor-pointer',
          ]"
          @click="onSwitchAgent(a.agentId)"
        >
          {{ a.name }}
        </button>
      </div>
    </div>

    <!-- 空态内容区：居中输入卡片 -->
    <div
      class="relative flex min-h-0 flex-1 flex-col items-center justify-center overflow-hidden px-6"
    >
      <div class="flex w-full max-w-[680px] flex-col items-center gap-6">
        <div class="space-y-2 text-center">
          <h1 class="text-3xl font-semibold tracking-tight text-slate-900">
            {{ t('chat.welcomeSubtitle') }}
          </h1>
        </div>

        <!-- 加载态：草稿创建中（session/new 启动 agent） -->
        <div
          v-if="draftCreating"
          class="flex items-center gap-2 text-sm text-slate-400"
        >
          <span class="inline-block h-4 w-4 animate-spin rounded-full border-2 border-slate-300 border-t-slate-600"></span>
          {{ t('chat.loadingAgent') }}
        </div>

        <template v-else>
          <!-- 发送/草稿创建错误条（可关闭；防止「点击发送无反应」的静默失败） -->
          <div
            v-if="sessionStore.streamError"
            class="flex w-full items-center justify-between rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 ring-1 ring-inset ring-red-100"
          >
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

          <!-- 配置项预览（模型/思维程度；agent 不支持时为空，隐藏） -->
          <div
            v-if="selectConfigOptions.length > 0"
            class="flex w-full flex-wrap items-center justify-center gap-3"
          >
            <div
              v-for="opt in selectConfigOptions"
              :key="opt.id"
              class="flex items-center gap-2"
            >
              <span class="text-xs text-slate-500">{{ opt.name }}</span>
              <select
                class="rounded-md border border-slate-200 bg-white px-2 py-1 text-sm"
                :value="String(opt.currentValue)"
                @change="onConfigChange(opt.id, ($event.target as HTMLSelectElement).value)"
              >
                <option
                  v-for="o in opt.options"
                  :key="o.value"
                  :value="o.value"
                >
                  {{ o.name }}
                </option>
              </select>
            </div>
          </div>

          <Composer
            mode="card"
            :agent-id="selectedAgentId"
            :sending="sessionStore.streaming"
            @submit="onSubmit"
            @cancel="sessionStore.cancelSend()"
          />
        </template>
      </div>
    </div>
  </div>
</template>
