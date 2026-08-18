<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from 'vue'
import type { VNodeChild } from 'vue'
import { useI18n } from 'vue-i18n'
import { SendOutline, StopOutline } from '@vicons/ionicons5'
import { NIcon, useMessage } from 'naive-ui'
import type { InputInst, SelectGroupOption, SelectOption } from 'naive-ui'
import { useSessionStore, MAX_TURNS_PER_SESSION, type SessionStreamStatus } from '@/stores/session'
import { uploadFiles } from '@/api'
import { extractPastedFiles, prepareFile } from '@/utils/fileUpload'
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
    /** 会话轮次达到上限（MAX_TURNS_PER_SESSION）：输入框与发送按钮一并禁用，显示提示条。 */
    turnLimited?: boolean
  }>(),
  { mode: 'bar', agentId: undefined, status: 'idle', turnLimited: false },
)

const emit = defineEmits<{
  (e: 'submit', payload: ComposerSubmitPayload): void
  (e: 'cancel'): void
}>()

const { t } = useI18n()
const sessionStore = useSessionStore()
const message = useMessage()

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
// 粘贴上传文件（仅 bar 模式启用）：Ctrl/Cmd+V 粘贴图片或其它文件 →
// 图片经 prepareFile 转 webp、其它文件原样直传，上传到当前会话工作区根目录 →
// 在文本最前面插入 @文件名 引用 → 通知文件列表刷新。
// 与「文件」面板共用 utils/fileUpload 的提取/压缩逻辑，行为保持一致。
// ---------------------------------------------------------------------------

/** 输入框中已引用文件的数量上限（图片与其它文件统一计数；已有引用 + 本次 1 个 > 上限则拒绝） */
const MAX_FILE_REFS = 3
/**
 * 统计文本中 @引用文件 的数量。粘贴上传统一插入 @文件名 引用，
 * 正则按通用文件名匹配（字母数字/点/连字符，可带或不带扩展名）；
 * 会误计邮件地址等含 @ 的文本，但仅影响上限拦截，可接受。
 */
const FILE_REF_RE = /@[\w.-]+/g
function countRefs(t: string): number {
  return (t.match(FILE_REF_RE) ?? []).length
}

/** 非图片文件上传大小上限（与后端 service.MaxOtherSizeBytes 一致；图片由 prepareFile 压缩，不受此限） */
const MAX_OTHER_FILE_BYTES = 10 * 1024 * 1024

/** 上传进行中：禁止发送（避免引用还没插入文本就被发走） */
const fileUploading = ref(false)

/**
 * 当前会话所属工作区（与 FileExplorer activeWs 同规则：会话项目 → 默认项目）。
 * bar 模式必在会话内，activeSession 可解析。
 */
const composerWorkspaceId = computed(() => {
  const sid = sessionStore.activeSession?.workspace?.id
  if (sid) return sid
  return sessionStore.defaultWorkspace()?.id ?? 0
})

/**
 * 输入框粘贴（仅 bar 模式）：
 * - 剪贴板含文件（图片或其它）且 无纯文本 → 上传第一个文件并插入 @引用；
 * - 含文件 且有纯文本（Word/网页复制文字+图）→ 放行默认粘贴：只留文字、丢弃文件；
 * - 纯文本/无文件 → 放行默认粘贴。
 * 图片与其它文件走同一上传链路（同一批内图片优先；单张限制，多文件只取第一个）。
 */
function onPaste(e: ClipboardEvent) {
  if (props.mode !== 'bar') return
  const files = extractPastedFiles(e)
  if (!files.length) return
  // 富文本粘贴（文字+图/文件）：丢弃文件、保留文字，交给浏览器默认行为插入纯文本
  const hasPlainText =
    (e.clipboardData?.getData('text/plain') ?? '').trim().length > 0
  if (hasPlainText) return
  const target = files.find((f) => f.type.startsWith('image/')) ?? files[0]
  e.preventDefault()
  if (fileUploading.value) {
    // 上一个还在传：吞掉本次并明确提示，避免用户误以为粘贴失败
    message.info('正在上传文件，请稍候')
    return
  }
  void pasteUpload(target) // 单张限制：多文件只取第一个，其余忽略
}

/**
 * 粘贴上传（图片或其它文件，单张）：图片经 prepareFile 压缩转 webp、
 * 其它文件原样直传 → 上传到工作区根目录（dir=''）→
 * 成功后在文本最前面插入引用并通知文件列表刷新。
 */
async function pasteUpload(file: File) {
  const wsId = composerWorkspaceId.value
  if (!wsId) {
    // 会话无工作区（如默认项目缺失）时上传无处可去，明确提示而非静默丢弃
    message.warning('请先添加项目，再粘贴上传文件')
    return
  }
  // 引用数上限：已有引用 + 本次 1 个 > 3 → 提示并跳过上传（图片与文件统一计数）
  if (countRefs(text.value) + 1 > MAX_FILE_REFS) {
    message.warning(`最多引用 ${MAX_FILE_REFS} 个文件`)
    return
  }
  // 大小预检：图片由 prepareFile 压缩（不在此限）；其它文件原样直传，
  // 受后端 10MB 上限约束，超限提前拒绝，避免上传到一半才被 413
  const isImage = file.type.startsWith('image/')
  if (!isImage && file.size > MAX_OTHER_FILE_BYTES) {
    message.error('文件超过 10MB 上限，无法上传')
    return
  }
  // 上传前捕获上下文：上传期间用户可能切换会话/项目，完成时需双重校验
  // （同会话 + 同工作区）才把引用插回输入框，避免污染新会话文本
  const sid = sessionStore.activeSession?.id ?? null
  fileUploading.value = true
  try {
    const prepared = await prepareFile(file)
    const uploaded = await uploadFiles(wsId, '', [prepared])
    const name = uploaded[0]?.name
    if (name) {
      // 文件已上传成功：无论是否插入引用都刷新列表
      sessionStore.bumpFileList()
      // 上下文已变化 → 不插引用（文件仍在工作区，可手动 @ 引用）
      const sameContext =
        sessionStore.activeSession?.id === sid &&
        composerWorkspaceId.value === wsId
      // 插入前重新计数：上传耗时期间用户可能已手动输入 @引用，超限则不再自动插入
      const withinLimit = countRefs(text.value) + 1 <= MAX_FILE_REFS
      if (sameContext && withinLimit) {
        insertRefs([name])
      } else if (sameContext && !withinLimit) {
        message.info('文件已上传，但引用数超过上限，未自动插入')
      }
    }
  } catch (err) {
    message.error(`文件上传失败：${err instanceof Error ? err.message : '未知错误'}`)
  } finally {
    fileUploading.value = false
  }
}

/** 把引用（@文件名）插入到文本最前面，多个用空格分隔；末尾带尾随空格，用户可直接继续输入 */
function insertRefs(names: string[]) {
  const refs = names.map((n) => `@${n}`).join(' ') + ' '
  text.value = text.value ? `${refs}${text.value}` : refs
  // rAF 保证 DOM 已按新 value 更新后再设置光标（与 pickSlashCommand 同模式）
  requestAnimationFrame(() => {
    inputRef.value?.focus()
    const el = (
      inputRef.value as unknown as { textareaElRef?: HTMLTextAreaElement }
    ).textareaElRef
    if (el) el.setSelectionRange(text.value.length, text.value.length)
  })
}

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

/** 可发送：bar 模式不要求 Agent（沿用当前会话）；card 模式必须已选 Agent；上传文件期间禁止发送；轮次达上限禁止发送 */
const canSend = computed(
  () =>
    !props.turnLimited &&
    !fileUploading.value &&
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
      :disabled="turnLimited"
      @keydown="onKeydown"
      @paste="onPaste"
    />

    <!-- 轮次达上限提示条：禁用输入与发送，引导新建会话（与下方错误条同位，避免布局跳动） -->
    <div
      v-if="turnLimited"
      class="mt-1.5 rounded bg-red-50 px-2 py-1 text-xs leading-relaxed text-red-500 dark:bg-red-950/40 dark:text-red-400"
    >
      {{ t('chat.turnLimitBanner', { max: MAX_TURNS_PER_SESSION }) }}
    </div>

    <!-- 配置项更新失败提示条：显示 agent 真实拒绝原因（如列表过期），3 秒自动消失 -->
    <div
      v-if="configError"
      class="mt-1.5 rounded bg-red-50 px-2 py-1 text-xs leading-relaxed text-red-500 dark:bg-red-950/40 dark:text-red-400"
    >
      {{ t('chat.configUpdateFailed') }}：{{ configError }}
    </div>

    <!-- 文件上传中提示条：粘贴上传进行时显示；与错误条同位避免卡片高度跳动 -->
    <div
      v-if="fileUploading"
      class="mt-1.5 flex items-center gap-2 rounded bg-blue-50 px-2 py-1 text-xs leading-relaxed text-blue-500 dark:bg-blue-950/40 dark:text-blue-400"
    >
      <n-spin :size="13" />
      正在上传文件…
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
        <!-- 处理中（排队/流式/停止确认）：红色停止按钮外层套「彗星」光弧——
             一条头部亮、尾部渐隐的弧，缓慢绕按钮旋转扫过（3s 一圈），
             比满圈虚线更优雅、更低调；pointer-events-none 不挡按钮点击。
             动画仅 bar（会话输入条）模式显示；card（/new 新建会话）只要改小按钮、
             不要动画效果（见 v-if="mode === 'bar'"） -->
        <span v-if="status !== 'idle'" class="relative inline-flex">
          <span v-if="mode === 'bar'" class="comet-ring"></span>
          <n-button
            type="error"
            size="small"
            circle
            :disabled="status === 'cancelling'"
            @click="emit('cancel')"
          >
            <template #icon>
              <n-icon :size="16"><StopOutline /></n-icon>
            </template>
          </n-button>
        </span>
        <n-button
          v-else
          type="primary"
          size="small"
          circle
          :loading="fileUploading"
          :disabled="!canSend"
          @click="onSend"
        >
          <template #icon>
            <n-icon :size="16"><SendOutline /></n-icon>
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
/* 处理中红色按钮的「彗星」光弧：
 * - conic-gradient 只点亮约 110° 的弧段，且由尾部（约 250° 起，几乎透明）
 *   到头部（360°，最亮）渐强，形成「彗尾拖长渐隐、头部聚集」的扫掠感；
 * - mask 径向镂空中心，只留约 1px 宽的细圆环（1px 的过渡沿让边缘柔和）；
 * - 3s 一圈匀速旋转，缓慢优雅；.dark 下换浅红系保持暗色可见。
 */
.comet-ring {
  position: absolute;
  inset: -6px;
  border-radius: 9999px;
  pointer-events: none;
  background: conic-gradient(
    from 0deg,
    transparent 0deg,
    transparent 250deg,
    rgba(239, 68, 68, 0.08) 280deg,
    rgba(239, 68, 68, 0.4) 320deg,
    rgba(239, 68, 68, 0.85) 350deg,
    rgba(239, 68, 68, 1) 360deg
  );
  -webkit-mask: radial-gradient(
    farthest-side,
    transparent calc(100% - 3px),
    #000 calc(100% - 2px)
  );
  mask: radial-gradient(
    farthest-side,
    transparent calc(100% - 3px),
    #000 calc(100% - 2px)
  );
  animation: comet-spin 3s linear infinite;
}
.dark .comet-ring {
  background: conic-gradient(
    from 0deg,
    transparent 0deg,
    transparent 250deg,
    rgba(248, 113, 113, 0.08) 280deg,
    rgba(248, 113, 113, 0.4) 320deg,
    rgba(248, 113, 113, 0.85) 350deg,
    rgba(248, 113, 113, 1) 360deg
  );
}
@keyframes comet-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
