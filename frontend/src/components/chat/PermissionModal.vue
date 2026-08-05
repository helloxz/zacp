<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useSessionStore } from '@/stores/session'

const { t } = useI18n()
const sessionStore = useSessionStore()

/** 用户选择权限选项：回传 permission 帧（由 store 处理），弹窗随即关闭 */
function select(optionId: string) {
  sessionStore.resolvePermission(optionId)
}
</script>

<template>
  <!-- 权限请求弹窗：不可遮罩关闭/不可 Esc（agent 在等待决定，必须选一项或等超时） -->
  <n-modal
    :show="sessionStore.pendingPermission !== null"
    :mask-closable="false"
    :closable="false"
    preset="card"
    :title="t('permission.title')"
    :style="{ width: '420px', maxWidth: 'calc(100vw - 32px)' }"
  >
    <div v-if="sessionStore.pendingPermission" class="flex flex-col gap-4">
      <!-- 工具调用信息 -->
      <div class="rounded-lg bg-slate-50 px-3 py-2.5 text-sm">
        <p class="truncate font-medium text-slate-800">
          {{
            sessionStore.pendingPermission.toolCall?.title ||
            t('permission.unknownTool')
          }}
        </p>
        <p class="mt-0.5 truncate text-xs text-slate-400">
          {{ sessionStore.pendingPermission.toolCall?.toolCallId }}
        </p>
      </div>

      <!-- 选项（后端 Agent 提供 label，无需 i18n） -->
      <div class="flex flex-col gap-2">
        <n-button
          v-for="opt in sessionStore.pendingPermission.options"
          :key="opt.optionId"
          type="primary"
          ghost
          @click="select(opt.optionId)"
        >
          {{ opt.name }}
        </n-button>
      </div>

      <p class="text-xs text-slate-400">{{ t('permission.hint') }}</p>
    </div>
  </n-modal>
</template>
