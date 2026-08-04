<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SendOutline, StopOutline, AddOutline } from '@vicons/ionicons5'
import { NIcon } from 'naive-ui'
import { useAgentStore } from '@/stores/agent'
import { useSessionStore } from '@/stores/session'

/** Composer 提交载荷（card / bar 共用） */
export interface ComposerSubmitPayload {
  agentId: string
  workspaceId?: number
  text: string
}

const props = withDefaults(
  defineProps<{
    /** card：空态居中卡片；bar：会话中底部输入条 */
    mode?: 'card' | 'bar'
    /** bar 模式当前会话的 Agent（只读标签）；card 模式为下拉默认值 */
    agentId?: string
    /** 发送中（P2 流式时由父级置 true，显示停止按钮） */
    sending?: boolean
  }>(),
  { mode: 'bar', agentId: undefined, sending: false },
)

const emit = defineEmits<{
  (e: 'submit', payload: ComposerSubmitPayload): void
  (e: 'cancel'): void
}>()

const { t } = useI18n()
const agentStore = useAgentStore()
const sessionStore = useSessionStore()

/** 会话配置项：select 型（模型/思考强度/mode 等）→ 下拉 */
const selectConfigOptions = computed(() =>
  sessionStore.configOptions.filter((o) => o.type === 'select'),
)
/** boolean 型 → 开关 */
const booleanConfigOptions = computed(() =>
  sessionStore.configOptions.filter((o) => o.type === 'boolean'),
)

/** 配置项变更：调后端 set_config_option，成功后本地回写 currentValue */
async function onConfigChange(optionId: string, valueId: string) {
  try {
    await sessionStore.setConfigOption(optionId, valueId)
  } catch {
    // 设置失败：保持原值（下轮进入会话时会重新加载）
  }
}

const text = ref('')
const selectedAgentId = ref(props.agentId ?? '')
const selectedWorkspaceId = ref<number | undefined>(undefined)

// ---- 工作区创建（解决「无工作区时下拉为空无法开启」的死循环）----
const wsPath = ref('')
const wsCreating = ref(false)
const showWsInput = ref(false)
const wsError = ref<string | null>(null)

/** 输入路径创建/开启工作区（后端校验路径存在）；成功后选中新工作区 */
async function createWs() {
  const path = wsPath.value.trim()
  if (!path || wsCreating.value) {
    return
  }
  wsCreating.value = true
  wsError.value = null
  try {
    const ws = await sessionStore.createWorkspace(path)
    selectedWorkspaceId.value = ws.id
    wsPath.value = ''
    showWsInput.value = false
  } catch (e) {
    wsError.value = e instanceof Error ? e.message : String(e)
  } finally {
    wsCreating.value = false
  }
}

/** 会话切换（bar 模式）时同步外部 agentId */
watch(
  () => props.agentId,
  (v) => {
    if (v) selectedAgentId.value = v
  },
)

/** card 模式首次进入：默认选中第一个可用 Agent，减少空态操作成本 */
onMounted(() => {
  if (!selectedAgentId.value) {
    const first = agentStore.list.find((a) => a.running) ?? agentStore.list[0]
    if (first) selectedAgentId.value = first.agentId
  }
})

const agentOptions = computed(() =>
  agentStore.list.map((a) => ({
    label: a.name,
    value: a.agentId,
    disabled: !a.running,
  })),
)

const workspaceOptions = computed(() =>
  sessionStore.workspaces.map((w) => ({
    label: w.name || w.path,
    value: w.id,
  })),
)

/** bar 模式只读 Agent 标签文案 */
const agentLabel = computed(() => {
  const a = agentStore.list.find(
    (it) => it.agentId === (props.agentId ?? selectedAgentId.value),
  )
  return a?.name ?? props.agentId ?? t('chat.agent')
})

/** 可发送：bar 模式不要求 Agent（沿用当前会话）；card 模式必须已选 Agent */
const canSend = computed(
  () =>
    text.value.trim().length > 0 &&
    (props.mode === 'bar' || !!selectedAgentId.value),
)

function onSend() {
  const payload = text.value.trim()
  if (!payload || !canSend.value) return
  emit('submit', {
    agentId: selectedAgentId.value,
    workspaceId: selectedWorkspaceId.value || undefined,
    text: payload,
  })
  text.value = ''
}

/** Enter 发送 / Shift+Enter 换行；isComposing 避免中文输入法回车误发送 */
function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
    e.preventDefault()
    onSend()
  }
}
</script>

<template>
  <div
    class="w-full rounded-2xl border border-slate-200 bg-white p-3 shadow-sm"
  >
    <!-- card：Agent / Workspace 可交互下拉；bar：只读 Agent 标签 -->
    <div v-if="mode === 'card'" class="mb-2 flex flex-wrap items-center gap-2">
      <n-select
        v-model:value="selectedAgentId"
        class="w-40"
        size="small"
        :options="agentOptions"
        :placeholder="t('chat.agent')"
        :consistent-menu-width="false"
      />

      <!-- 无任何工作区：不能选，必须手动输入路径「开启」（避免死循环） -->
      <template v-if="!workspaceOptions.length">
        <n-input
          v-model:value="wsPath"
          class="w-52"
          size="small"
          :placeholder="t('chat.workspacePathPlaceholder')"
          @keydown.enter="createWs"
        />
        <n-button
          size="small"
          type="primary"
          ghost
          :loading="wsCreating"
          @click="createWs"
        >
          {{ t('chat.enableWorkspace') }}
        </n-button>
      </template>

      <!-- 已有工作区：下拉选择 + 新建按钮（展开路径输入） -->
      <template v-else>
        <n-select
          v-model:value="selectedWorkspaceId"
          class="w-48"
          size="small"
          :options="workspaceOptions"
          :placeholder="t('chat.selectWorkspace')"
          clearable
          :consistent-menu-width="false"
        />
        <n-button
          quaternary
          circle
          size="small"
          :title="t('chat.newWorkspace')"
          @click="showWsInput = !showWsInput"
        >
          <template #icon>
            <n-icon><AddOutline /></n-icon>
          </template>
        </n-button>
      </template>
    </div>

    <!-- 新建工作区输入（已有工作区时点「+」展开） -->
    <div
      v-if="mode === 'card' && showWsInput && workspaceOptions.length"
      class="mb-2 flex items-center gap-2"
    >
      <n-input
        v-model:value="wsPath"
        class="w-52"
        size="small"
        :placeholder="t('chat.workspacePathPlaceholder')"
        @keydown.enter="createWs"
      />
      <n-button
        size="small"
        type="primary"
        ghost
        :loading="wsCreating"
        @click="createWs"
      >
        {{ t('chat.enableWorkspace') }}
      </n-button>
      <n-button
        size="small"
        quaternary
        @click="showWsInput = false; wsPath = ''; wsError = null"
      >
        {{ t('common.cancel') }}
      </n-button>
    </div>

    <!-- 工作区创建失败提示（如路径不存在） -->
    <p
      v-if="mode === 'card' && wsError"
      class="mb-2 text-xs text-red-500"
    >
      {{ wsError }}
    </p>
    <div v-else class="mb-2 flex flex-wrap items-center gap-2">
      <span
        class="shrink-0 rounded bg-slate-100 px-1.5 py-0.5 text-xs text-slate-500"
      >
        {{ agentLabel }}
      </span>

      <!-- 会话配置项（模型/思考强度/mode 等）：agent 下发 configOptions 才显示，否则整段隐藏 -->
      <template v-if="sessionStore.configOptions.length">
        <n-select
          v-for="opt in selectConfigOptions"
          :key="opt.id"
          :value="String(opt.currentValue)"
          size="tiny"
          class="w-32"
          :options="(opt.options ?? []).map((v) => ({ label: v.name, value: v.value }))"
          :consistent-menu-width="false"
          @update:value="(v: string) => onConfigChange(opt.id, v)"
        />
        <n-switch
          v-for="opt in booleanConfigOptions"
          :key="opt.id"
          :value="Boolean(opt.currentValue)"
          size="small"
          @update:value="(v: boolean) => onConfigChange(opt.id, v ? 'true' : 'false')"
        />
      </template>
    </div>

    <n-input
      v-model:value="text"
      type="textarea"
      :autosize="{ minRows: mode === 'card' ? 3 : 2, maxRows: 8 }"
      :placeholder="t('chat.placeholder')"
      @keydown="onKeydown"
    />

    <div class="mt-2 flex items-center justify-between">
      <span class="text-xs text-slate-400">{{ t('chat.enterHint') }}</span>
      <div class="flex items-center gap-2">
        <n-button
          v-if="sending"
          type="error"
          size="small"
          @click="emit('cancel')"
        >
          <template #icon>
            <n-icon><StopOutline /></n-icon>
          </template>
          {{ t('chat.stop') }}
        </n-button>
        <n-button
          v-else
          type="primary"
          size="small"
          :disabled="!canSend"
          @click="onSend"
        >
          <template #icon>
            <n-icon><SendOutline /></n-icon>
          </template>
          {{ t('chat.send') }}
        </n-button>
      </div>
    </div>
  </div>
</template>
