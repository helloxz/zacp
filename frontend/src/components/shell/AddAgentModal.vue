<script setup lang="ts">
/**
 * AddAgentModal — 设置页「智能体 - 添加」弹窗。
 *
 * 表单收集：显示名称 / 智能体 ID / 二进制路径 / 启动参数（原始字符串）。
 * 提交走 POST /api/v1/agents：后端校验 id 全局唯一与 command 真实存在、
 * 引号感知切分参数、写 config.toml 并热更新（默认启用）。
 * 前端只做必填与格式的前置校验，唯一性/存在性以后端返回为准（错误信息透传展示）。
 */
import { reactive, ref } from 'vue'
import { useMessage, type FormInst, type FormRules } from 'naive-ui'
import { CloseOutline } from '@vicons/ionicons5'
import { useI18n } from 'vue-i18n'
import { useAgentManageStore } from '@/stores/agentManage'

const { t } = useI18n()
const message = useMessage()
const store = useAgentManageStore()

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ 'update:show': [value: boolean] }>()

const formRef = ref<FormInst | null>(null)
const submitting = ref(false)

/** 表单数据。id 与后端 addAgentRequest 的 json 字段对齐（camelCase）。 */
const form = reactive({
  name: '',
  id: '',
  command: '',
  args: '',
})

const rules: FormRules = {
  name: {
    required: true,
    message: () => t('settings.agent.nameRequired'),
    trigger: ['input', 'blur'],
  },
  id: [
    {
      required: true,
      message: () => t('settings.agent.idRequired'),
      trigger: ['input', 'blur'],
    },
    {
      // 与后端 agentIDRe 保持一致：字母/数字开头，后续字母/数字/下划线/连字符
      pattern: /^[A-Za-z0-9][A-Za-z0-9_-]*$/,
      message: () => t('settings.agent.idInvalid'),
      trigger: ['input', 'blur'],
    },
  ],
  command: {
    required: true,
    message: () => t('settings.agent.commandRequired'),
    trigger: ['input', 'blur'],
  },
}

/** 每次打开弹窗重置表单（上次输入不残留） */
function resetForm() {
  form.name = ''
  form.id = ''
  form.command = ''
  form.args = ''
  formRef.value?.restoreValidation()
}

function onShowChange(v: boolean) {
  if (!v) emit('update:show', false)
  else resetForm()
}

/** 提交：前端校验通过后交给后端；失败错误（id 冲突/命令不存在等）透传后端 message */
async function submit() {
  try {
    await formRef.value?.validate()
  } catch {
    return
  }
  submitting.value = true
  try {
    const added = await store.add({
      name: form.name.trim(),
      id: form.id.trim(),
      command: form.command.trim(),
      args: form.args.trim(),
    })
    message.success(t('settings.agent.addSuccess', { name: added.name }))
    emit('update:show', false)
  } catch (e) {
    message.error(e instanceof Error ? e.message : t('settings.agent.addFailed'))
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <n-modal :show="show" :mask-closable="true" @update:show="onShowChange">
    <!-- 弹窗容器风格与 AgentConfigEditorModal 保持一致 -->
    <div
      class="flex max-h-[calc(100vh-2rem)] w-[460px] max-w-[94vw] flex-col overflow-hidden rounded-2xl bg-surface-raised shadow-2xl"
    >
      <header
        class="flex shrink-0 items-center justify-between border-b border-divider-subtle px-6 py-4"
      >
        <h2 class="text-base font-semibold text-ink">
          {{ t('settings.agent.addModalTitle') }}
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
        <n-form ref="formRef" :model="form" :rules="rules" label-placement="top" size="small">
          <n-form-item :label="t('settings.agent.nameLabel')" path="name">
            <n-input
              v-model:value="form.name"
              :placeholder="t('settings.agent.namePlaceholder')"
              maxlength="50"
            />
          </n-form-item>

          <n-form-item :label="t('settings.agent.idLabel')" path="id">
            <n-input
              v-model:value="form.id"
              :placeholder="t('settings.agent.idPlaceholder')"
              maxlength="50"
            />
            <template #feedback>
              <span class="text-xs text-ink-muted">{{ t('settings.agent.idHint') }}</span>
            </template>
          </n-form-item>

          <n-form-item :label="t('settings.agent.commandLabel')" path="command">
            <n-input
              v-model:value="form.command"
              :placeholder="t('settings.agent.commandPlaceholder')"
            />
          </n-form-item>

          <n-form-item :label="t('settings.agent.argsLabel')" path="args">
            <n-input
              v-model:value="form.args"
              :placeholder="t('settings.agent.argsPlaceholder')"
            />
          </n-form-item>
        </n-form>

        <div class="flex justify-end gap-3">
          <n-button size="small" tertiary @click="onShowChange(false)">
            {{ t('common.cancel') }}
          </n-button>
          <n-button
            size="small"
            type="primary"
            :loading="submitting"
            :disabled="submitting"
            @click="submit"
          >
            {{ t('settings.agent.add') }}
          </n-button>
        </div>
      </div>
    </div>
  </n-modal>
</template>