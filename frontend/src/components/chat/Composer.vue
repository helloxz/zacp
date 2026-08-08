<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from 'vue'
import type { VNodeChild } from 'vue'
import { useI18n } from 'vue-i18n'
import { SendOutline, StopOutline } from '@vicons/ionicons5'
import { NIcon } from 'naive-ui'
import type { InputInst, SelectGroupOption, SelectOption } from 'naive-ui'
import { useSessionStore, type SessionStreamStatus } from '@/stores/session'
import type { ConfigOptionValue } from '@/types/models'

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
    /**
     * 当前会话的发送状态（由父级从 session store 绑定，多会话时各自独立）：
     * idle=可发送 / queued=已发送排队中（可取消）/ streaming=流式进行中（停止按钮）
     */
    status?: SessionStreamStatus
  }>(),
  { mode: 'bar', agentId: undefined, status: 'idle' },
)

const emit = defineEmits<{
  (e: 'submit', payload: ComposerSubmitPayload): void
  (e: 'cancel'): void
}>()

const { t } = useI18n()
const sessionStore = useSessionStore()

/** 会话配置项：select 型（模型/思考强度/mode 等）→ 下拉（仅 bar 模式展示） */
const selectConfigOptions = computed(() =>
  sessionStore.configOptions.filter((o) => o.type === 'select'),
)
/** boolean 型 → 开关（仅 bar 模式展示） */
const booleanConfigOptions = computed(() =>
  sessionStore.configOptions.filter((o) => o.type === 'boolean'),
)

/** 配置项变更失败时的提示（3 秒后自动消失）；成功后清空 */
const configError = ref('')
let configErrorTimer: ReturnType<typeof setTimeout> | undefined
function showConfigError(raw: unknown) {
  const msg = raw instanceof Error ? raw.message : String(raw)
  // 后端 message 形如 `set config option: {"code":-32602,"message":"session/set_config_option: invalid value ..."}`
  // 提取内层 message，用户能直接看懂 agent 拒绝原因（如值已失效/选项未知）
  const inner = msg.match(/"message":"((?:[^"\\]|\\.)*)"/)
  configError.value = inner ? inner[1] : msg
  clearTimeout(configErrorTimer)
  configErrorTimer = setTimeout(() => {
    configError.value = ''
  }, 3000)
}

/** 配置项变更：调后端 set_config_option，成功后本地回写 currentValue */
async function onConfigChange(optionId: string, valueId: string) {
  try {
    await sessionStore.setConfigOption(optionId, valueId)
    configError.value = ''
  } catch (e) {
    // 设置失败：保持原值并提示用户真实原因（agent 拒绝时多为列表已过期）
    showConfigError(e)
  }
}

/**
 * 把 configOption 的选项列表转成 naive-ui select 的选项/分组结构。
 * 模型选项名约定为「渠道/模型名」：提取第一个 / 前的内容作为渠道分组标题（group 不可选），
 * 其下为模型子项，同一渠道聚合为一组；不含 / 的选项保持普通项。
 * 对全部 select 通用：其余选项（思考模式等）无 / 时不受影响。
 *
 * 关键设计（解决「选中 A 却像传了 B」的感知错位）：
 * 子项 label 用完整「渠道/模型」名、value 用 agent 下发的完整 value（两者必须一致，agent 才接受）；
 * 下拉列表内的展示由 renderLabel 剥离渠道前缀只显示模型名（避免组内重复显示渠道），
 * 而选中后的回显（n-select 固定显示 option.label）则是完整「渠道/模型」，与请求体完全一致，
 * 用户能看到自己选的是哪个渠道的哪个模型。
 */
function buildSelectOptions(
  options?: ConfigOptionValue[],
): Array<SelectGroupOption | SelectOption> {
  const result: Array<SelectGroupOption | SelectOption> = []
  // 渠道名 → 该渠道下模型子项；Map 保证组间按渠道首次出现顺序、组内按原顺序
  const groups = new Map<string, SelectOption[]>()
  for (const v of options ?? []) {
    const idx = v.name.indexOf('/')
    if (idx > 0 && idx < v.name.length - 1) {
      // 「渠道/模型」格式：归入对应渠道分组；label 保留完整路径供回显，modelName 供下拉展示
      const channel = v.name.slice(0, idx)
      const modelName = v.name.slice(idx + 1)
      if (!groups.has(channel)) groups.set(channel, [])
      groups.get(channel)!.push({ label: v.name, value: v.value, modelName })
    } else {
      // 无 / 的普通选项（思考模式等）：回显与展示一致
      result.push({ label: v.name, value: v.value, modelName: v.name })
    }
  }
  for (const [label, children] of groups) {
    result.push({ type: 'group', label, key: label, children })
  }
  return result
}

/**
 * naive-ui render-label：只影响下拉列表内的选项渲染，不影响选中回显（回显固定用 option.label）。
 * 分组标题（type=group）无 modelName → 显示渠道名；子项/普通项 → 显示模型名。
 */
function renderConfigOptionLabel(option: SelectOption & { modelName?: string }): VNodeChild {
  return h('span', option.modelName ?? String(option.label))
}

/**
 * 下拉选项的模糊匹配过滤（n-select :filter）。
 * 同时匹配完整「渠道/模型」名（label）与剥离渠道后的模型名（modelName），
 * 例：输入 "deepseek" 命中渠道、输入 "gpt-4o" 命中模型名均能过滤出对应项。
 * 分组（type=group）由 Naive 按 children 过滤结果自动取舍，无需在此处理。
 */
function filterSelectOption(pattern: string, option: SelectOption | SelectGroupOption): boolean {
  const q = pattern.toLowerCase()
  if (String(option.label ?? '').toLowerCase().includes(q)) {
    return true
  }
  const modelName = (option as SelectOption & { modelName?: string }).modelName
  return !!modelName && modelName.toLowerCase().includes(q)
}

const text = ref('')
const selectedAgentId = ref(props.agentId ?? '')
const inputRef = ref<InputInst | null>(null)

// ---------------------------------------------------------------------------
// / 命令候选面板（数据来自 agent 经 ACP available_commands_update 通告的命令列表）
// ---------------------------------------------------------------------------

/** 面板是否被关闭（Esc / 选中命令后）；仅当文本不再以 / 开头时自动重置 */
const slashDismissed = ref(false)
/** 当前高亮命令索引（面板显示时默认选中第一项） */
const slashIndex = ref(0)
/** 候选面板 DOM（高亮项滚动可见用） */
const slashPanelRef = ref<HTMLElement | null>(null)

/** 输入是否处于 / 命令态（第一个非空字符为 /） */
const slashActive = computed(() => text.value.trimStart().startsWith('/'))
/** / 之后的查询串（前缀匹配命令名） */
const slashQuery = computed(() =>
  slashActive.value ? text.value.trimStart().slice(1) : '',
)
/** 过滤后的候选命令（无查询串时显示全部） */
const slashCandidates = computed(() => {
  if (!slashQuery.value) return sessionStore.slashCommands
  const q = slashQuery.value.toLowerCase()
  return sessionStore.slashCommands.filter((c) =>
    c.name.toLowerCase().startsWith(q),
  )
})
/**
 * 面板可见性：以 / 开头 + 未被关闭 + 有候选命令。
 * bar（会话输入条）与 card（新建会话空态）都支持；
 * 候选为空（agent 未通告且无静态兜底）时不显示。
 */
const slashVisible = computed(
  () =>
    slashActive.value &&
    !slashDismissed.value &&
    slashCandidates.value.length > 0,
)

// 查询串变化时高亮回到第一项
watch(slashQuery, () => {
  slashIndex.value = 0
})
// 文本不再以 / 开头时重置 dismissed：删掉 / 或清空后再输入 / 会重新弹出面板
watch(text, (v) => {
  if (!v.trimStart().startsWith('/')) {
    slashDismissed.value = false
  }
})
// 键盘上下移动高亮时，让高亮项滚动到面板可视区内（flush:'post' 确保面板首帧已挂载）
watch(
  slashIndex,
  (i) => {
    const items = slashPanelRef.value?.querySelectorAll('[data-slash-item]')
    ;(items?.[i] as HTMLElement | undefined)?.scrollIntoView({ block: 'nearest' })
  },
  { flush: 'post' },
)

/**
 * 确认选中命令：插入 "/name " 并继续编辑（不发送），光标留在末尾可直接输入参数。
 * 面板随即关闭；输入参数时仍以 / 开头，不会重新弹出。
 *
 * 整体覆盖语义安全性：候选过滤是「查询串前缀匹配命令名」，一旦用户输入了命令名之外的
 * 内容（如参数），查询串含空格即不再匹配任何命令、面板隐藏、Enter 走普通发送，
 * 因此到达此处的文本必然只是 "/" + 命令名前缀，覆盖无数据丢失。
 */
function pickSlashCommand(index?: number) {
  const i = index ?? slashIndex.value
  const cmd = slashCandidates.value[i]
  if (!cmd) return
  text.value = `/${cmd.name} `
  slashDismissed.value = true
  // 光标移到末尾（inputRef 为 naive-ui InputInst：暴露 textareaElRef，
  // 类型未导出故断言；rAF 保证 DOM 已按新 value 更新后再设置光标）
  requestAnimationFrame(() => {
    inputRef.value?.focus()
    const el = (
      inputRef.value as unknown as { textareaElRef?: HTMLTextAreaElement }
    ).textareaElRef
    if (el) el.setSelectionRange(text.value.length, text.value.length)
  })
}

/** 新建会话空态（card）自动聚焦输入框：进入 /new 即可直接打字。
 * rAF 延后到布局稳定后再聚焦，避免被遮罩/过渡干扰。 */
onMounted(() => {
  if (props.mode === 'card') {
    requestAnimationFrame(() => inputRef.value?.focus())
  }
})

/** 聚焦输入框（供父级在草稿创建完成/切 tab 后重新聚焦） */
function focus() {
  inputRef.value?.focus()
}

defineExpose({ focus })

/** bar 模式会话切换时同步外部 agentId */
watch(
  () => props.agentId,
  (v) => {
    if (v) selectedAgentId.value = v
  },
)

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
    text: payload,
  })
  text.value = ''
}

/**
 * 键盘处理优先级：
 * 1. / 命令面板可见时：↑↓ 移动高亮、Enter/Tab 确认选中命令、Esc 关闭面板；
 * 2. 否则 Enter 发送 / Shift+Enter 换行（isComposing 避免中文输入法回车误发送）。
 * 面板不可见时按 Enter 直接发送（如无匹配 /xxx 时按普通消息发送）。
 */
function onKeydown(e: KeyboardEvent) {
  if (e.isComposing) return
  if (slashVisible.value) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      // 阻止默认光标移动，在候选项间循环移动高亮
      e.preventDefault()
      const n = slashCandidates.value.length
      slashIndex.value =
        e.key === 'ArrowDown'
          ? (slashIndex.value + 1) % n
          : (slashIndex.value - 1 + n) % n
      return
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      // 确认选中命令（插入 /name 继续编辑，不发送）
      e.preventDefault()
      pickSlashCommand()
      return
    }
    if (e.key === 'Tab') {
      // Tab 同样确认（阻止默认焦点切换）
      e.preventDefault()
      pickSlashCommand()
      return
    }
    if (e.key === 'Escape') {
      e.preventDefault()
      slashDismissed.value = true
      return
    }
  }
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    onSend()
  }
}
</script>

<template>
  <div
    class="relative w-full rounded-2xl border border-divider bg-surface-raised p-3 shadow-sm transition-shadow focus-within:border-divider focus-within:shadow-md"
  >
    <!-- / 命令候选面板：浮于输入框上方，宽度与输入框一致（容器 relative + 左右对齐） -->
    <div
      v-if="slashVisible"
      ref="slashPanelRef"
      class="absolute bottom-full left-0 right-0 z-20 mb-1.5 overflow-hidden rounded-lg border border-divider bg-surface-raised shadow-lg"
    >
      <div class="max-h-64 overflow-y-auto py-1">
        <button
          v-for="(cmd, i) in slashCandidates"
          :key="cmd.name"
          type="button"
          data-slash-item
          class="flex w-full items-baseline gap-2 px-3 py-1.5 text-left"
          :class="i === slashIndex ? 'bg-surface-hover' : ''"
          @mouseenter="slashIndex = i"
          @click="pickSlashCommand(i)"
        >
          <span class="shrink-0 font-mono text-sm font-medium text-ink">/{{ cmd.name }}</span>
          <span v-if="cmd.description" class="truncate text-xs text-ink-muted">{{ cmd.description }}</span>
          <span v-if="cmd.inputHint" class="ml-auto shrink-0 text-xs text-ink-muted">{{ cmd.inputHint }}</span>
        </button>
      </div>
    </div>

    <n-input
      ref="inputRef"
      v-model:value="text"
      type="textarea"
      class="composer-input"
      :bordered="false"
      :autosize="{ minRows: mode === 'card' ? 3 : 2, maxRows: 8 }"
      :placeholder="t('chat.placeholder')"
      @keydown="onKeydown"
    />

    <!-- 配置项更新失败提示条：显示 agent 真实拒绝原因（如列表过期），3 秒自动消失 -->
    <div
      v-if="configError"
      class="mt-1.5 rounded bg-red-50 px-2 py-1 text-xs leading-relaxed text-red-500 dark:bg-red-950/40 dark:text-red-400"
    >
      {{ t('chat.configUpdateFailed') }}：{{ configError }}
    </div>

    <!-- 底部选项行：左侧配置项（模型/思考强度等），右侧仅图标发送按钮；与输入框同卡片，构成整体 -->
    <!-- flex-1：选项容器占满除发送按钮外的剩余宽度；flex-nowrap：选项强制一行，极端情况才横向滚动兜底 -->
    <div class="mt-2 flex items-center justify-between gap-2">
      <div class="flex min-w-0 flex-1 flex-nowrap items-center gap-2 overflow-x-auto">
        <!-- 配置项（模型/思维强度等）融合进输入卡片；card（新建会话空态）与 bar（会话中）风格一致。
             外层 div 定宽限制下拉宽度（n-select 根样式 width:100% 会撑满父级，直接设 class 不生效）；
             模型下拉内容最长（渠道/模型 完整名），固定更宽；其余选项保持窄宽，避免一行放不下 -->
        <template v-if="sessionStore.configOptions.length">
          <div
            v-for="opt in selectConfigOptions"
            :key="opt.id"
            class="shrink-0"
            :class="opt.id === 'model' ? 'w-44' : 'w-28'"
          >
            <n-select
              :value="String(opt.currentValue)"
              size="tiny"
              class="opt-select"
              :options="buildSelectOptions(opt.options)"
              :render-label="renderConfigOptionLabel"
              :consistent-menu-width="false"
              filterable
              :filter="filterSelectOption"
              @update:value="(v: string) => onConfigChange(opt.id, v)"
            />
          </div>
          <n-switch
            v-for="opt in booleanConfigOptions"
            :key="opt.id"
            :value="Boolean(opt.currentValue)"
            size="small"
            class="shrink-0"
            @update:value="(v: boolean) => onConfigChange(opt.id, v ? 'true' : 'false')"
          />
        </template>
        <span v-else class="text-xs text-ink-muted">{{ t('chat.enterHint') }}</span>
      </div>

      <div class="flex shrink-0 items-center gap-2">
        <!-- 排队中：停止按钮 + 状态文案（可取消排队；A 结束后自动开跑） -->
        <span
          v-if="status === 'queued'"
          class="flex items-center gap-1.5 text-xs text-amber-500 dark:text-amber-400"
        >
          <n-spin :size="13" />
          {{ t('chat.queued') }}
        </span>
        <!-- 停止确认中：文字提示用户正在停止，避免用户以为卡住而重复点击 -->
        <span
          v-else-if="status === 'cancelling'"
          class="flex items-center gap-1.5 text-xs text-ink-muted"
        >
          <n-spin :size="13" />
          {{ t('chat.stopping') }}
        </span>
        <n-button
          v-if="status !== 'idle'"
          type="error"
          size="medium"
          circle
          :disabled="status === 'cancelling'"
          @click="emit('cancel')"
        >
          <template #icon>
            <n-icon :size="18"><StopOutline /></n-icon>
          </template>
        </n-button>
        <n-button
          v-else
          type="primary"
          size="medium"
          circle
          :disabled="!canSend"
          @click="onSend"
        >
          <template #icon>
            <n-icon :size="18"><SendOutline /></n-icon>
          </template>
        </n-button>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 输入框与卡片融合成一个整体：去掉内部边框与聚焦描边/阴影，聚焦背景透明 */
.composer-input :deep(.n-input__border),
.composer-input :deep(.n-input__state-border) {
  display: none;
}
.composer-input :deep(.n-input--focus),
.composer-input :deep(.n-input--hover) {
  background-color: transparent;
  box-shadow: none;
}
/* 下拉选项去边框，与输入卡片融合；hover/聚焦阴影一并隐藏 */
.opt-select :deep(.n-base-selection__border),
.opt-select :deep(.n-base-selection__state-border) {
  display: none;
}
</style>
