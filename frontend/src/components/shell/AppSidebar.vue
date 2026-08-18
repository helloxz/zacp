<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { AddOutline } from '@vicons/ionicons5'
import { useMessage } from 'naive-ui'
import SidebarSessionList from '@/components/shell/SidebarSessionList.vue'
import UserFooter from '@/components/shell/UserFooter.vue'
import DirectoryPicker from '@/components/shell/DirectoryPicker.vue'
import { useSessionStore } from '@/stores/session'
import { useAppStore } from '@/stores/app'

/** open：移动端抽屉开合（lg 及以上忽略，侧栏常驻流内）；desktop：桌面断点判定（inert 只在移动端关闭态启用）。desktop 必填——漏传且窗口 ≥1024 时流内侧栏会被整体 inert */
defineProps<{ open: boolean; desktop: boolean }>()
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

/**
 * 根目录禁止创建为项目（agent 的 cwd 会是文件系统根，读写范围覆盖整个磁盘，风险过高）。
 * 输入为 / 时确认按钮置灰；后端 CreateWorkspace 同样拒绝（最终防线）。
 */
const isRootPath = computed(() => projectPath.value.trim() === '/')

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
  // 空路径或根目录（/）拒绝：确认按钮已置灰，这里双保险防其它入口触发
  if (!path || path === '/' || projectCreating.value) return
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
  <!-- 侧栏：lg 及以上流内常驻 300px（PC 现状不变）；lg 以下 fixed overlay 抽屉（280px），
       开合由 open + translate-x 控制，transition 实现平滑滑出。
       fixed 抽离流内后主区自动占满全宽；static 时恢复 flex 布局占位。
       顶部/底部加 env() 安全区：刘海屏竖屏时抽屉首尾的按钮不被刘海与底部横条遮挡
       （PC 无安全区时 env() 为 0，行为不变）。 -->
  <aside
    class="flex w-[280px] shrink-0 flex-col border-r border-divider bg-surface transition-transform duration-300 ease-out fixed inset-y-0 left-0 z-50 lg:static lg:z-auto lg:w-[300px] lg:translate-x-0"
    :class="open ? 'translate-x-0' : '-translate-x-full'"
    style="padding-bottom: env(safe-area-inset-bottom)"
    :inert="!open && !desktop"
  >
    <div class="flex items-center gap-1 pt-[max(env(safe-area-inset-top),0.75rem)] pl-[max(env(safe-area-inset-left),0.75rem)] pr-3 pb-3">
      <!-- 新建项目：Tailwind 实现的次级按钮（点击打开与 WelcomeHero 共享的项目弹窗）。
           注意：不做单独的抽屉关闭按钮——点遮罩区域即可关闭（移动端交互更轻） -->
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
    :positive-button-props="{ disabled: isRootPath }"
    :loading="projectCreating"
    @positive-click="onCreateProject"
  >
    <div class="space-y-2 py-2">
      <DirectoryPicker v-model="projectPath" />
      <p class="text-xs text-ink-muted">{{ t('shell.newProjectHint') }}</p>
    </div>
  </n-modal>
</template>
