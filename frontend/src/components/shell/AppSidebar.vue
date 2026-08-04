<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import {
  AddOutline,
  ChevronBackOutline,
  ChevronForwardOutline,
} from '@vicons/ionicons5'
import { NIcon, useMessage } from 'naive-ui'
import SidebarSessionList from '@/components/shell/SidebarSessionList.vue'
import UserFooter from '@/components/shell/UserFooter.vue'
import { useSessionStore } from '@/stores/session'
import { useAppStore } from '@/stores/app'

defineProps<{ collapsed?: boolean }>()
const emit = defineEmits<{
  (e: 'toggle'): void
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
  <!-- 侧栏折叠：260px ↔ 64px 图标条（PC 界面，不做移动端抽屉） -->
  <aside
    class="flex flex-col border-r border-slate-200 bg-slate-50 transition-[width] duration-200"
    :class="collapsed ? 'w-16' : 'w-[260px]'"
  >
    <!-- 收起态：仅保留「展开」按钮 -->
    <template v-if="collapsed">
      <button
        class="mx-auto mt-3 flex h-9 w-9 items-center justify-center rounded-lg text-slate-500 transition-colors hover:bg-slate-200/60 hover:text-slate-700"
        :aria-label="t('shell.expandSidebar')"
        :title="t('shell.expandSidebar')"
        @click="emit('toggle')"
      >
        <n-icon size="20"><ChevronForwardOutline /></n-icon>
      </button>
    </template>

    <!-- 展开态：新建项目 + 折叠按钮 + 项目会话列表 + 用户区 -->
    <template v-else>
      <div class="flex items-center gap-1 p-3">
        <n-button block secondary class="flex-1" @click="onNewProject">
          <template #icon>
            <n-icon><AddOutline /></n-icon>
          </template>
          {{ t('shell.newProject') }}
        </n-button>
        <n-button
          quaternary
          circle
          size="small"
          :aria-label="t('shell.collapseSidebar')"
          :title="t('shell.collapseSidebar')"
          @click="emit('toggle')"
        >
          <template #icon>
            <n-icon><ChevronBackOutline /></n-icon>
          </template>
        </n-button>
      </div>

      <SidebarSessionList class="min-h-0 flex-1 overflow-y-auto px-3 pb-4" />
      <UserFooter @open-settings="emit('open-settings')" />
    </template>
  </aside>

  <!-- 新建项目弹窗：输入项目路径 -->
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
      <n-input
        v-model:value="projectPath"
        :placeholder="t('shell.newProjectPlaceholder')"
        @keydown.enter="onCreateProject"
      />
      <p class="text-xs text-slate-400">{{ t('shell.newProjectHint') }}</p>
    </div>
  </n-modal>
</template>
