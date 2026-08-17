<script setup lang="ts">
/**
 * ZliteChannelModal — 设置页「智能体 - 默认渠道设置」弹窗（仅 zlite 使用）。
 *
 * 表单字段（与后端 PUT /api/v1/agents/zlite/default-channel 的 json 对齐）：
 * - type：渠道类型下拉，三个选项的 value 与后端落盘值完全一致
 *   （openai.chat / openai.responses / anthropic），无映射层；
 * - baseUrl：自定义 BaseURL，placeholder 随渠道类型切换（openai 系带 /v1，anthropic 不带）；
 * - apiKey：API 密钥（允许为空 = 本地模型场景；保存后写入 ~/.zlite/.env
 *   的 ZLITE_DEFAULT_API_KEY，config.toml 以 ${ZLITE_DEFAULT_API_KEY} 引用）；
 * - models：可用模型多值输入（n-dynamic-tags：回车添加、× 删除），必填，不允许留空。
 *
 * 打开时 GET 拉取当前值回填；保存时前端 trim 字段、提交 PUT，成功即关闭。
 * 保存成功后后端会自动停止运行中的 zlite ACP 进程（下次对话按新配置自动拉起），
 * 因此无需再提示手动重启。
 */
import { computed, reactive, ref, watch } from 'vue'
import { useMessage, type FormInst, type FormRules } from 'naive-ui'
import { CloseOutline } from '@vicons/ionicons5'
import { useI18n } from 'vue-i18n'
import { fetchZliteDefaultChannel, saveZliteDefaultChannel } from '@/api'
import type { ZliteChannel } from '@/types/models'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ 'update:show': [value: boolean] }>()

const { t } = useI18n()
const message = useMessage()

const formRef = ref<FormInst | null>(null)
const loading = ref(false)
const submitting = ref(false)

/** 渠道类型选项：label 为中文展示名，value 与后端 config.toml 落盘值一致 */
const typeOptions: Array<{ label: string; value: string }> = [
  { label: 'OpenAI 兼容', value: 'openai.chat' },
  { label: 'OpenAI Responses API', value: 'openai.responses' },
  { label: 'Anthropic 兼容', value: 'anthropic' },
]

/** 表单数据（与 ZliteChannel 字段对齐）；打开时由 GET 结果覆盖 */
const form = reactive<ZliteChannel>({
  type: 'openai.chat',
  baseUrl: '',
  apiKey: '',
  models: [],
})

/** BaseURL placeholder 随渠道类型切换：openai 系 → /v1，anthropic → 不带 /v1 */
const baseUrlPlaceholder = computed(() =>
  form.type === 'anthropic'
    ? 'https://api.domain.com'
    : 'https://api.domain.com/v1',
)

const rules: FormRules = {
  baseUrl: [
    {
      required: true,
      message: () => t('settings.agent.zliteBaseUrlRequired'),
      trigger: ['input', 'blur'],
    },
    {
      pattern: /^https?:\/\/\S+$/,
      message: () => t('settings.agent.zliteBaseUrlInvalid'),
      trigger: ['input', 'blur'],
    },
  ],
  // 可用模型为必填：不允许留空（n-dynamic-tags 的 value 是数组，
  // required 规则对空数组不生效，需用 validator 显式判断长度）
  models: {
    required: true,
    type: 'array',
    validator: (_rule, value: unknown) => Array.isArray(value) && value.length > 0,
    message: () => t('settings.agent.zliteModelsRequired'),
    trigger: ['change'],
  },
}

/** 打开弹窗：先清空旧值（避免闪现上一次数据），再拉取当前默认渠道设置回填 */
async function open() {
  // 重置表单为初始默认值：加载中表单被 spin 遮住，避免上一个 agent 的数据闪现
  form.type = 'openai.chat'
  form.baseUrl = ''
  form.apiKey = ''
  form.models = []
  loading.value = true
  try {
    const data = await fetchZliteDefaultChannel()
    form.type = data.type
    form.baseUrl = data.baseUrl
    form.apiKey = data.apiKey
    form.models = [...data.models]
    formRef.value?.restoreValidation()
  } catch (e) {
    message.error(e instanceof Error ? e.message : t('settings.agent.zliteLoadFailed'))
  } finally {
    loading.value = false
  }
}

function onShowChange(v: boolean) {
  // 仅处理关闭：n-modal 的 @update:show 只在组件内部“想关闭”时触发
  // （点遮罩 / 右上角 X / Esc），外部把 :show 置 true 时不会回调。
  // 打开时的初始化走下方 watch(() => props.show) 分支。
  if (!v) emit('update:show', false)
}

/**
 * 打开通道：watch 外部 show 变化（false→true）时拉取默认渠道设置回填。
 * 不能依赖 @update:show——外部改 show 不触发该事件（参照 AgentConfigEditorModal
 * 的成熟模式：打开用 watch、关闭用 update:show）。
 */
watch(
  () => props.show,
  (v) => {
    if (v) void open()
  },
)

/** 提交：前端 trim + 校验后交给后端；后端会再次 trim/去重/校验（以服务端结果为准） */
async function submit() {
  try {
    await formRef.value?.validate()
  } catch {
    return
  }
  submitting.value = true
  try {
    // 保存时移除表单字段前后空白（需求约定）；models 去空、去重
    const seen = new Set<string>()
    const payload: ZliteChannel = {
      type: form.type,
      baseUrl: form.baseUrl.trim(),
      apiKey: form.apiKey.trim(),
      models: form.models
        .map((m) => m.trim())
        .filter((m) => m !== '' && !seen.has(m) && seen.add(m)),
    }
    await saveZliteDefaultChannel(payload)
    message.success(t('settings.agent.zliteSaveSuccess'))
    emit('update:show', false)
  } catch (e) {
    message.error(e instanceof Error ? e.message : t('settings.agent.zliteSaveFailed'))
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <n-modal :show="show" :mask-closable="true" @update:show="onShowChange">
    <!-- 弹窗容器风格与 AddAgentModal / AgentConfigEditorModal 保持一致 -->
    <div
      class="flex max-h-[calc(100vh-2rem)] w-[460px] max-w-[94vw] flex-col overflow-hidden rounded-2xl bg-surface-raised shadow-2xl"
    >
      <header
        class="flex shrink-0 items-center justify-between border-b border-divider-subtle px-6 py-4"
      >
        <h2 class="text-base font-semibold text-ink">
          {{ t('settings.agent.zliteChannelTitle') }}
        </h2>
        <button
          type="button"
          :aria-label="t('settings.close')"
          class="flex h-8 w-8 cursor-pointer items-center justify-center rounded-full text-ink-muted transition-colors hover:bg-surface-hover hover:text-ink-secondary focus:outline-none focus-visible:ring-2 focus-visible:ring-divider"
          @click="onShowChange(false)"
        >
          <n-icon :size="18"><CloseOutline /></n-icon>
        </button>
      </header>

      <div class="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-6">
        <n-spin :show="loading" class="min-h-32">
          <n-form
            v-show="!loading"
            ref="formRef"
            :model="form"
            :rules="rules"
            label-placement="top"
            size="small"
          >
            <n-form-item :label="t('settings.agent.zliteTypeLabel')" path="type">
              <n-select
                v-model:value="form.type"
                :options="typeOptions"
                :placeholder="t('settings.agent.zliteTypeLabel')"
              />
            </n-form-item>

            <n-form-item :label="t('settings.agent.zliteBaseUrlLabel')" path="baseUrl">
              <n-input
                v-model:value="form.baseUrl"
                :placeholder="baseUrlPlaceholder"
                maxlength="500"
              />
            </n-form-item>

            <n-form-item :label="t('settings.agent.zliteApiKeyLabel')" path="apiKey">
              <n-input
                v-model:value="form.apiKey"
                type="password"
                show-password-on="click"
                :placeholder="t('settings.agent.zliteApiKeyPlaceholder')"
                :input-props="{ autocomplete: 'off', spellcheck: false }"
              />
            </n-form-item>

            <n-form-item :label="t('settings.agent.zliteModelsLabel')" path="models">
              <n-dynamic-tags
                v-model:value="form.models"
                :placeholder="t('settings.agent.zliteModelsHint')"
              />
            </n-form-item>
          </n-form>
        </n-spin>

        <div class="flex justify-end gap-3">
          <n-button size="small" tertiary :disabled="submitting" @click="onShowChange(false)">
            {{ t('common.cancel') }}
          </n-button>
          <n-button
            size="small"
            type="primary"
            :loading="submitting"
            :disabled="submitting || loading"
            @click="submit"
          >
            {{ t('common.save') }}
          </n-button>
        </div>
      </div>
    </div>
  </n-modal>
</template>