<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SendOutline, StopOutline } from '@vicons/ionicons5'
import { NIcon } from 'naive-ui'
import type { SelectGroupOption, SelectOption } from 'naive-ui'
import { useSessionStore } from '@/stores/session'
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
    /** 发送中（流式时由父级置 true，显示停止按钮） */
    sending?: boolean
  }>(),
  { mode: 'bar', agentId: undefined, sending: false },
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

/** 配置项变更：调后端 set_config_option，成功后本地回写 currentValue */
async function onConfigChange(optionId: string, valueId: string) {
  try {
    await sessionStore.setConfigOption(optionId, valueId)
  } catch {
    // 设置失败：保持原值（下轮进入会话时会重新加载）
  }
}

/**
 * 把 configOption 的选项列表转成 naive-ui select 的选项/分组结构。
 * 模型选项名约定为「渠道/模型名」：提取第一个 / 前的内容作为渠道分组标题（group 不可选），
 * 其下为模型子项，同一渠道聚合为一组；不含 / 的选项保持普通项。
 * 对全部 select 通用：其余选项（思考模式等）无 / 时不受影响。
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
      // 「渠道/模型」格式：归入对应渠道分组
      const channel = v.name.slice(0, idx)
      const label = v.name.slice(idx + 1)
      if (!groups.has(channel)) groups.set(channel, [])
      groups.get(channel)!.push({ label, value: v.value })
    } else {
      result.push({ label: v.name, value: v.value })
    }
  }
  for (const [label, children] of groups) {
    result.push({ type: 'group', label, key: label, children })
  }
  return result
}

const text = ref('')
const selectedAgentId = ref(props.agentId ?? '')

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
    class="w-full rounded-2xl border border-slate-200 bg-white p-3 shadow-sm transition-shadow focus-within:border-slate-300 focus-within:shadow-md"
  >
    <n-input
      v-model:value="text"
      type="textarea"
      class="composer-input"
      :bordered="false"
      :autosize="{ minRows: mode === 'card' ? 3 : 2, maxRows: 8 }"
      :placeholder="t('chat.placeholder')"
      @keydown="onKeydown"
    />

    <!-- 底部选项行：左侧配置项（模型/思考强度等），右侧仅图标发送按钮；与输入框同卡片，构成整体 -->
    <!-- flex-1：选项容器占满除发送按钮外的剩余宽度；flex-nowrap：选项强制一行，极端情况才横向滚动兜底 -->
    <div class="mt-2 flex items-center justify-between gap-2">
      <div class="flex min-w-0 flex-1 flex-nowrap items-center gap-2 overflow-x-auto">
        <!-- bar 模式：agent 下发 configOptions 才显示，否则整段隐藏 -->
        <template v-if="mode === 'bar' && sessionStore.configOptions.length">
          <!-- 外层 div 定宽限制下拉宽度（n-select 根样式 width:100% 会撑满父级，直接设 class 不生效） -->
          <div
            v-for="opt in selectConfigOptions"
            :key="opt.id"
            class="w-28 shrink-0"
          >
            <n-select
              :value="String(opt.currentValue)"
              size="tiny"
              class="opt-select"
              :options="buildSelectOptions(opt.options)"
              :consistent-menu-width="false"
              filterable
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
        <span v-else class="text-xs text-slate-400">{{ t('chat.enterHint') }}</span>
      </div>

      <div class="flex shrink-0 items-center">
        <n-button
          v-if="sending"
          type="error"
          size="medium"
          circle
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
