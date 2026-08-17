<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDialog, useMessage } from 'naive-ui'
import { AddOutline, CreateOutline, CubeOutline, TrashOutline } from '@vicons/ionicons5'
import { useAgentManageStore } from '@/stores/agentManage'
import AgentConfigEditorModal from '@/components/shell/AgentConfigEditorModal.vue'
import AddAgentModal from '@/components/shell/AddAgentModal.vue'
import type { ManageAgent } from '@/types/models'

const { t } = useI18n()
const message = useMessage()
const dialog = useDialog()
const store = useAgentManageStore()

// 「编辑配置」弹窗：目标智能体 + 开关
const editTarget = ref<ManageAgent | null>(null)
const editorShow = ref(false)

// 「添加智能体」弹窗开关
const addShow = ref(false)

// 正在删除的 agentId（防重复点击；删除成功后列表刷新，条目消失）
const deletingId = ref<string | null>(null)

/**
 * 白色背景 tooltip 样式：覆盖 Naive UI 主题 CSS 变量（默认 tooltip 为深色底）。
 * --n-color 同时控制气泡与箭头背景色（见 popover 样式实现），
 * --n-text-color 控制提示文字颜色。设置页内容区 tooltip 统一用白底。
 */
const whiteTooltipStyle = {
  '--n-color': '#ffffff',
  '--n-text-color': '#3f3f46',
  '--n-box-shadow': '0 2px 8px rgba(0, 0, 0, 0.12)',
}

/** 打开某智能体的配置编辑弹窗 */
function onEditConfig(agent: ManageAgent) {
  editTarget.value = agent
  editorShow.value = true
}

// 每次进入「智能体」页重新拉取目录，保证开关状态与服务端一致
onMounted(() => {
  store.load()
})

/**
 * 开关切换：成功后在本地乐观更新；失败时提示并重载列表（以服务端为准，
 * 不手动回滚，避免与服务端状态漂移）。
 */
async function onToggle(agent: ManageAgent, enabled: boolean) {
  try {
    await store.toggle(agent, enabled)
    message.success(
      enabled
        ? t('settings.agent.enabledToast', { name: agent.name })
        : t('settings.agent.disabledToast', { name: agent.name }),
    )
  } catch (e) {
    message.error(e instanceof Error ? e.message : t('settings.agent.toggleFailed'))
    store.load()
  }
}

function retry() {
  store.error = null
  store.load()
}

/**
 * 删除智能体（仅 source=config 可删）：先弹确认框（内容带 agent 名称，
 * 并提示运行中的会话将中断），确认后调用后端移除配置并热更新停用。
 * 失败时保持弹窗打开让用户可重试（onPositiveClick 返回 false 阻止关闭）。
 */
function onDelete(agent: ManageAgent) {
  dialog.warning({
    title: t('settings.agent.deleteTitle'),
    content: t('settings.agent.deleteContent', { name: agent.name }),
    positiveText: t('settings.agent.delete'),
    negativeText: t('common.cancel'),
    onPositiveClick: () =>
      new Promise<boolean>(async (resolve) => {
        if (deletingId.value) {
          resolve(false)
          return
        }
        deletingId.value = agent.agentId
        try {
          await store.remove(agent)
          message.success(t('settings.agent.deleteSuccess', { name: agent.name }))
          resolve(true)
        } catch (e) {
          message.error(e instanceof Error ? e.message : t('settings.agent.deleteFailed'))
          resolve(false) // 删除失败：弹窗保持打开，便于重试
        } finally {
          deletingId.value = null
        }
      }),
  })
}
</script>

<template>
  <div class="flex flex-col gap-5">
    <div class="flex items-center justify-between gap-3">
      <div class="min-w-0">
        <h3 class="text-base font-semibold text-ink">
          {{ t('settings.agent.title') }}
        </h3>
        <p class="mt-1 text-sm text-ink-muted">{{ t('settings.agent.desc') }}</p>
      </div>
      <div class="flex shrink-0 items-center gap-2">
        <!-- 添加自定义智能体（写配置 + 默认启用）。
             size=tiny 高度 22px 与旁边「已启用」n-tag(small) 对齐；tertiary 无边框
             浅色填充 + 胶囊圆角（见 .add-agent-btn 样式），与 tag 风格一致 -->
        <n-button
          size="tiny"
          type="primary"
          tertiary
          class="add-agent-btn"
          @click="addShow = true"
        >
          <template #icon>
            <n-icon :size="12"><AddOutline /></n-icon>
          </template>
          {{ t('settings.agent.add') }}
        </n-button>
        <n-tag
          v-if="!store.loading && store.list.length"
          size="small"
          round
          :bordered="false"
          type="info"
        >
          {{ t('settings.agent.enabledCount', { count: store.enabledCount }) }}
        </n-tag>
      </div>
    </div>

    <!-- 加载失败：显示错误与重试 -->
    <div
      v-if="store.error && !store.loading"
      class="flex flex-col items-center gap-3 rounded-xl border border-dashed border-rose-200 bg-rose-50/60 px-6 py-8 text-center dark:border-rose-900/50 dark:bg-rose-950/30"
    >
      <p class="text-sm text-rose-500 dark:text-rose-400">{{ store.error }}</p>
      <n-button size="small" type="primary" tertiary @click="retry">
        {{ t('dirPicker.retry') }}
      </n-button>
    </div>

    <!-- 智能体列表 -->
    <div v-else class="flex flex-col gap-2">
      <n-spin :show="store.loading">
        <div class="flex flex-col gap-2">
          <div
            v-for="agent in store.list"
            :key="agent.agentId"
            class="flex items-center justify-between gap-3 rounded-xl border border-divider bg-surface-raised px-4 py-3 transition-colors hover:border-indigo-300 hover:bg-indigo-50/40 dark:hover:border-indigo-500/50 dark:hover:bg-indigo-500/10"
          >
            <div class="flex min-w-0 items-center gap-3">
              <div
                class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-indigo-50 text-indigo-500 dark:bg-indigo-500/15 dark:text-indigo-400"
              >
                <n-icon :size="18"><CubeOutline /></n-icon>
              </div>
              <div class="min-w-0">
                <div class="flex items-center gap-2">
                  <span class="truncate text-sm font-medium text-ink">
                    {{ agent.name }}
                  </span>
                  <n-tag
                    v-if="agent.agentId === 'zlite'"
                    size="tiny"
                    :bordered="false"
                    type="info"
                  >
                    {{ t('settings.agent.official') }}
                  </n-tag>
                  <n-tag
                    v-if="agent.source === 'builtin'"
                    size="tiny"
                    :bordered="false"
                    type="default"
                  >
                    {{ t('settings.agent.builtin') }}
                  </n-tag>
                  <n-tag
                    :type="agent.installed ? 'success' : 'warning'"
                    size="tiny"
                    :bordered="false"
                  >
                    {{
                      agent.installed
                        ? t('settings.agent.installed')
                        : t('settings.agent.notInstalled')
                    }}
                  </n-tag>
                </div>
                <p class="mt-0.5 truncate text-xs text-ink-muted">
                  {{ agent.command }}
                </p>
              </div>
            </div>

            <div class="flex shrink-0 items-center gap-3">
              <!-- 已安装且后端登记了配置文件路径 → 提供「编辑配置」入口。
                   用纯文字按钮（text 类型）：quaternary 按钮点击后有 hover 背景残留，
                   与未点击项不协调；文字按钮 hover 仅变色、无背景块 -->
              <n-button
                v-if="agent.installed && agent.hasConfigFiles"
                size="small"
                text
                type="primary"
                @click="onEditConfig(agent)"
              >
                <template #icon>
                  <n-icon :size="14"><CreateOutline /></n-icon>
                </template>
                {{ t('settings.agent.editConfig') }}
              </n-button>
              <!-- 未安装：开关禁用，左置白底 hover 提示先安装 -->
              <n-tooltip
                v-if="!agent.installed"
                placement="left"
                trigger="hover"
                :style="whiteTooltipStyle"
              >
                <template #trigger>
                  <n-switch :value="agent.enabled" disabled />
                </template>
                {{ t('settings.agent.notInstalledHint') }}
              </n-tooltip>
              <n-switch
                v-else
                :value="agent.enabled"
                :loading="store.isToggling(agent.agentId)"
                @update:value="(v: boolean) => onToggle(agent, v)"
              />
              <!-- 删除：仅配置来源（source=config）可删；内置项无配置块，灰置禁用 +
                   hover 提示（左置白底）。放在 switch 右侧，图标按钮即可 -->
              <n-tooltip
                placement="left"
                trigger="hover"
                :disabled="agent.source === 'config'"
                :style="whiteTooltipStyle"
              >
                <template #trigger>
                  <span class="inline-flex">
                    <n-button
                      size="small"
                      quaternary
                      circle
                      type="error"
                      :disabled="agent.source !== 'config' || deletingId !== null"
                      :loading="deletingId === agent.agentId"
                      :aria-label="t('settings.agent.delete')"
                      @click="onDelete(agent)"
                    >
                      <template #icon>
                        <n-icon :size="14"><TrashOutline /></n-icon>
                      </template>
                    </n-button>
                  </span>
                </template>
                {{ t('settings.agent.deleteBuiltinHint') }}
              </n-tooltip>
            </div>
          </div>

          <!-- 空态兜底（正常不会出现：内置目录至少 4 项） -->
          <div
            v-if="!store.loading && !store.list.length"
            class="rounded-xl border border-dashed border-divider py-10 text-center text-sm text-ink-muted"
          >
            {{ t('settings.agent.empty') }}
          </div>
        </div>
      </n-spin>
    </div>

    <!-- 编辑配置弹窗 -->
    <AgentConfigEditorModal
      :show="editorShow"
      :agent="editTarget"
      @update:show="editorShow = $event"
    />

    <!-- 添加智能体弹窗 -->
    <AddAgentModal :show="addShow" @update:show="addShow = $event" />
  </div>
</template>

<style scoped>
/* 「添加」按钮与旁边「已启用 n 个」n-tag（small + round）视觉统一：
   tiny 高度同为 22px，这里把按钮圆角覆盖为胶囊形（--n-border-radius
   是 Naive UI 按钮圆角的 CSS 变量，组件根上覆盖即对内部生效） */
.add-agent-btn {
  --n-border-radius: 9999px;
}
</style>
