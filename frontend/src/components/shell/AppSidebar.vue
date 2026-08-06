<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { AddOutline } from '@vicons/ionicons5'
import { useMessage } from 'naive-ui'
import SidebarSessionList from '@/components/shell/SidebarSessionList.vue'
import UserFooter from '@/components/shell/UserFooter.vue'
import DirectoryPicker from '@/components/shell/DirectoryPicker.vue'
import { useSessionStore } from '@/stores/session'
import { useAppStore } from '@/stores/app'

const emit = defineEmits<{
  (e: 'open-settings'): void
}>()

const { t } = useI18n()
const router = useRouter()
const sessionStore = useSessionStore()
const appStore = useAppStore()
const message = useMessage()

/** 新建项目弹窗（与 WelcomeHero 共享 appStore.newProjectModalOpen） */
const showProjectModal = ref(false)
const projectPath = ref('')
const projectCreating = ref(false)

// 同步共享 flag → 本地弹窗（WelcomeHero 按钮 / 侧栏按钮都能打开同一弹窗）
watch(
  () => appStore.newProjectModalOpen,
  (open) => {
    if (open) {
      projectPath.value = ''
      showProjectModal.value = true
      appStore.newProjectModalOpen = false
    }
  },
)

/** 打开「新建项目」弹窗 */
function onNewProject() {
  projectPath.value = ''
  showProjectModal.value = true
}

/**
 * 提交项目路径：POST /api/v1/workspaces（后端校验路径存在 + 自动取末尾段为 name）。
 * 创建成功后直接进入该项目的「新建会话」空态（/new?workspaceId=X），少一步点击。
 */
async function onCreateProject() {
  const path = projectPath.value.trim()
  if (!path || projectCreating.value) return
  projectCreating.value = true
  try {
    const ws = await sessionStore.createWorkspace(path)
    showProjectModal.value = false
    // 创建项目成功 → 直接进入该项目的新建会话空态
    void router.push({ name: 'new', query: { workspaceId: String(ws.id) } })
  } catch (e) {
    message.error(e instanceof Error ? e.message : String(e))
  } finally {
    projectCreating.value = false
  }
}
</script>

<template>
  <!-- 固定宽度侧栏（300px，列表常驻展示，不再支持折叠） -->
  <aside class="flex w-[300px] flex-col border-r border-divider bg-surface">
    <div class="flex items-center gap-1 p-3">
      <!-- 新建项目：Tailwind 实现的次级按钮（点击打开与 WelcomeHero 共享的项目弹窗） -->
      <button
        type="button"
        class="flex cursor-pointer flex-1 items-center justify-center gap-1.5 rounded-lg border border-divider bg-surface-raised px-3 py-2 text-sm font-medium text-ink-secondary shadow-sm transition-colors hover:border-divider hover:bg-surface-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-divider"
        @click="onNewProject"
      >
        <AddOutline class="h-4 w-4 shrink-0" />
        {{ t('shell.newProject') }}
      </button>
    </div>

    <SidebarSessionList class="min-h-0 flex-1 overflow-y-auto px-3 pb-4" />
    <UserFooter @open-settings="emit('open-settings')" />
  </aside>

  <!-- 新建项目弹窗：目录选择器（浏览 + 手动输入双通道，路径双向同步） -->
  <n-modal
    v-model:show="showProjectModal"
    preset="dialog"
    :title="t('shell.newProjectTitle')"
    :positive-text="t('common.confirm')"
    :negative-text="t('common.cancel')"
    :loading="projectCreating"
    @positive-click="onCreateProject"
  >
    <div class="space-y-2 py-2">
      <DirectoryPicker v-model="projectPath" @submit="onCreateProject" />
      <p class="text-xs text-ink-muted">{{ t('shell.newProjectHint') }}</p>
    </div>
  </n-modal>
</template>
