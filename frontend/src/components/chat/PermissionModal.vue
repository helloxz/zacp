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
      <!-- 工具调用信息：完整展示详情（长文本换行），内容过多时区域内滚动 -->
      <div
        class="max-h-48 overflow-y-auto rounded-lg bg-surface-hover px-3 py-2.5 text-sm"
      >
        <p class="font-medium break-all whitespace-pre-wrap text-ink">
          {{
            sessionStore.pendingPermission.toolCall?.title ||
            t('permission.unknownTool')
          }}
        </p>
        <p
          v-if="sessionStore.pendingPermission.toolCall?.toolCallId"
          class="mt-0.5 break-all whitespace-pre-wrap text-xs text-ink-muted"
        >
          {{ sessionStore.pendingPermission.toolCall?.toolCallId }}
        </p>
      </div>

      <!-- 选项（后端 Agent 提供 label，无需 i18n）：文本过长时按钮内省略号，悬浮可看全文 -->
      <div class="flex flex-col gap-2">
        <n-button
          v-for="opt in sessionStore.pendingPermission.options"
          :key="opt.optionId"
          block
          type="primary"
          ghost
          :title="opt.name"
          @click="select(opt.optionId)"
        >
          <span class="block w-full truncate">{{ opt.name }}</span>
        </n-button>
      </div>

      <p class="text-xs text-ink-muted">{{ t('permission.hint') }}</p>
    </div>
  </n-modal>
</template>
