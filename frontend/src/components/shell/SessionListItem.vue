<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { EllipsisHorizontalOutline } from '@vicons/ionicons5'
import { useMessage, type DropdownOption } from 'naive-ui'
import { useAgentStore } from '@/stores/agent'
import { useAppStore } from '@/stores/app'
import { useSessionStore } from '@/stores/session'
import type { ChatSession } from '@/types/models'
import { formatRelativeTime } from '@/utils/relativeTime'

const props = defineProps<{ session: ChatSession }>()

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const agentStore = useAgentStore()
const appStore = useAppStore()
const sessionStore = useSessionStore()
const message = useMessage()

/** 当前路由是否正展示该会话（驱动高亮） */
const isActive = computed(
  () => Number(route.params.sessionId) === props.session.id,
)

/** Agent 副文案：按 agentId 从 agent store 查名（后端 Session 不冗余 agentName） */
const agentName = computed(() => {
  const agent = agentStore.list.find((a) => a.agentId === props.session.agentId)
  return agent?.name ?? props.session.agentId
})

const title = computed(() => props.session.title || t('chat.newChatTitle'))

/** 该会话是否正在等待 Agent 回复（驱动列表项前「任务进行中」呼吸圆点） */
const isRunning = computed(() => sessionStore.runningSessionId === props.session.id)

const relativeTime = computed(() =>
  formatRelativeTime(props.session.updatedAt, appStore.locale),
)

function onSelect() {
  if (isActive.value) return
  sessionStore.currentId = props.session.id
  void router.push({
    name: 'session',
    params: { sessionId: String(props.session.id) },
  })
}

// ---------- 操作菜单（hover 显示 ... 按钮，点击展开：重命名 / 删除） ----------

/** 操作菜单选项：label 用函数保持 i18n 响应式 */
const menuOptions: DropdownOption[] = [
  { label: () => t('shell.rename'), key: 'rename' },
  { label: () => t('shell.delete'), key: 'delete' },
]

const renameModalVisible = ref(false)
const deleteModalVisible = ref(false)
const renameValue = ref('')
const renaming = ref(false)

/** 操作菜单展开状态：展开期间保持 ... 按钮可见（避免移开鼠标后按钮消失） */
const actionsVisible = ref(false)

/** 操作菜单选择分发：重命名开输入弹窗，删除开确认弹窗 */
function onMenuSelect(key: string | number) {
  if (key === 'rename') {
    renameValue.value = props.session.title || ''
    renameModalVisible.value = true
  } else if (key === 'delete') {
    deleteModalVisible.value = true
  }
}

/** 确认重命名：调后端 PATCH /sessions/:id，成功后 store 更新本地列表 */
async function onRenameConfirm() {
  const nextTitle = renameValue.value.trim()
  if (!nextTitle) {
    message.warning(t('shell.renameEmptyHint'))
    return
  }
  if ([...nextTitle].length > 200) {
    // 按码点计数，与后端 len([]rune) 一致（emoji 等代理对字符不会误报）
    message.warning(t('shell.renameTooLongHint'))
    return
  }
  renaming.value = true
  try {
    await sessionStore.renameSession(props.session.id, nextTitle)
    renameModalVisible.value = false
    message.success(t('shell.renameSuccess'))
  } catch {
    message.error(t('shell.renameFailed'))
  } finally {
    renaming.value = false
  }
}

/** 删除会话（复用 store.removeSession）；若正展示该会话则回空态 */
async function onDelete() {
  try {
    await sessionStore.removeSession(props.session.id)
    if (isActive.value) {
      void router.push({ name: 'home' })
    }
  } catch {
    // 删除失败静默 + 控制台（与原有行为一致，P1 简化）
  }
}
</script>

<template>
  <div
    class="group flex w-full cursor-pointer items-center gap-1 rounded-lg px-2.5 py-2 transition-colors"
    :class="isActive ? 'bg-slate-200/70' : 'hover:bg-slate-200/50'"
    role="button"
    tabindex="0"
    @click="onSelect"
    @keydown.enter="onSelect"
  >
    <div class="flex min-w-0 flex-1 flex-col gap-0.5">
      <div class="flex min-w-0 items-center gap-1.5">
        <!-- 任务进行中：蓝色轻呼吸圆点（仅进行中的会话显示，完成后消失） -->
        <span
          v-if="isRunning"
          class="running-dot shrink-0"
          :title="t('shell.runningHint')"
          aria-hidden="true"
        />
        <span
          class="truncate text-sm"
          :class="isActive ? 'font-medium text-slate-900' : 'text-slate-700'"
        >
          {{ title }}
        </span>
      </div>
      <span class="flex items-center gap-1.5 text-xs text-slate-400">
        <span class="truncate">{{ agentName }}</span>
        <span aria-hidden="true">·</span>
        <span class="shrink-0">{{ relativeTime }}</span>
      </span>
    </div>
    <!-- hover 显示 ... 按钮：点击展开操作菜单（重命名/删除）；stop 阻止冒泡切换会话 -->
    <n-dropdown
      trigger="click"
      :options="menuOptions"
      placement="bottom-end"
      @select="onMenuSelect"
      @update:show="(v) => (actionsVisible = v)"
    >
      <n-button
        quaternary
        size="tiny"
        circle
        class="shrink-0 transition-opacity"
        :class="actionsVisible ? 'opacity-100' : 'opacity-0 group-hover:opacity-100'"
        aria-label="session actions"
        @click.stop
      >
        <template #icon>
          <n-icon :size="16"><EllipsisHorizontalOutline /></n-icon>
        </template>
      </n-button>
    </n-dropdown>
  </div>

  <!-- 重命名弹窗：预填当前标题，回车或确认按钮提交 -->
  <n-modal
    v-model:show="renameModalVisible"
    preset="card"
    :title="t('shell.renameTitle')"
    style="width: 420px"
  >
    <n-input
      v-model:value="renameValue"
      :placeholder="t('shell.renamePlaceholder')"
      maxlength="200"
      clearable
      @keydown.enter.prevent="onRenameConfirm"
    />
    <template #footer>
      <n-space justify="end">
        <n-button quaternary @click="renameModalVisible = false">
          {{ t('common.cancel') }}
        </n-button>
        <n-button type="primary" :loading="renaming" @click="onRenameConfirm">
          {{ t('common.confirm') }}
        </n-button>
      </n-space>
    </template>
  </n-modal>

  <!-- 删除确认弹窗（positive-click 返回 promise 时自动 loading 并等待） -->
  <n-modal
    v-model:show="deleteModalVisible"
    preset="dialog"
    type="warning"
    :title="t('shell.deleteTitle')"
    :content="t('shell.confirmDelete')"
    :positive-text="t('common.confirm')"
    :negative-text="t('common.cancel')"
    @positive-click="onDelete"
  />
</template>

<style scoped>
/* 任务进行中呼吸圆点：蓝色轻闪烁（opacity 0.35↔1，周期 2.4s），比 Tailwind
   animate-pulse 的强脉冲更柔和；仅装饰，无信息承载（配合 aria-hidden） */
.running-dot {
  width: 8px;
  height: 8px;
  border-radius: 9999px;
  background: #3b82f6; /* blue-500 */
  animation: running-dot-breathe 2.4s ease-in-out infinite;
}

@keyframes running-dot-breathe {
  0%,
  100% {
    opacity: 0.35;
  }
  50% {
    opacity: 1;
  }
}

/* 动画偏好减弱：降级为静态蓝点，避免闪烁干扰 */
@media (prefers-reduced-motion: reduce) {
  .running-dot {
    animation: none;
    opacity: 1;
  }
}
</style>
