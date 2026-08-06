<script setup lang="ts">
import { onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useMessage } from 'naive-ui'
import { CubeOutline } from '@vicons/ionicons5'
import { useAgentManageStore } from '@/stores/agentManage'
import type { ManageAgent } from '@/types/models'

const { t } = useI18n()
const message = useMessage()
const store = useAgentManageStore()

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
</script>

<template>
  <div class="flex flex-col gap-5">
    <div class="flex items-center justify-between gap-3">
      <div class="min-w-0">
        <h3 class="text-base font-semibold text-slate-800">
          {{ t('settings.agent.title') }}
        </h3>
        <p class="mt-1 text-sm text-slate-500">{{ t('settings.agent.desc') }}</p>
      </div>
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

    <!-- 加载失败：显示错误与重试 -->
    <div
      v-if="store.error && !store.loading"
      class="flex flex-col items-center gap-3 rounded-xl border border-dashed border-rose-200 bg-rose-50/60 px-6 py-8 text-center"
    >
      <p class="text-sm text-rose-500">{{ store.error }}</p>
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
            class="flex items-center justify-between gap-3 rounded-xl border border-slate-200 bg-white px-4 py-3 transition-colors hover:border-indigo-200 hover:bg-indigo-50/40"
          >
            <div class="flex min-w-0 items-center gap-3">
              <div
                class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-indigo-50 text-indigo-500"
              >
                <n-icon :size="18"><CubeOutline /></n-icon>
              </div>
              <div class="min-w-0">
                <div class="flex items-center gap-2">
                  <span class="truncate text-sm font-medium text-slate-800">
                    {{ agent.name }}
                  </span>
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
                <p class="mt-0.5 truncate text-xs text-slate-400">
                  {{ agent.command }}
                </p>
              </div>
            </div>

            <div class="flex shrink-0 items-center gap-3">
              <span class="hidden text-xs text-slate-400 sm:inline">
                {{ agent.agentId }}
              </span>
              <!-- 未安装：开关禁用，hover 提示先安装 -->
              <n-tooltip v-if="!agent.installed" trigger="hover">
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
            </div>
          </div>

          <!-- 空态兜底（正常不会出现：内置目录至少 4 项） -->
          <div
            v-if="!store.loading && !store.list.length"
            class="rounded-xl border border-dashed border-slate-200 py-10 text-center text-sm text-slate-400"
          >
            {{ t('settings.agent.empty') }}
          </div>
        </div>
      </n-spin>
    </div>
  </div>
</template>
