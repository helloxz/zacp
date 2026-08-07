<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RefreshOutline } from '@vicons/ionicons5'
import { fetchGitStatus } from '@/api'
import type { GitChange, GitStatus } from '@/types/models'
import { useSessionStore } from '@/stores/session'
import { useAppStore } from '@/stores/app'

const sessionStore = useSessionStore()
const appStore = useAppStore()

/** 当前 workspace：与文件树保持一致，优先当前会话所属项目，其次默认项目。 */
const activeWorkspace = computed(() => {
  const workspaceId = sessionStore.activeSession?.workspace?.id
  if (workspaceId) {
    return sessionStore.workspaces.find((workspace) => workspace.id === workspaceId)
  }
  return sessionStore.defaultWorkspace()
})
const workspaceId = computed(() => activeWorkspace.value?.id ?? 0)

const status = ref<GitStatus | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
let requestSerial = 0

const statusLabels: Record<GitChange['status'], string> = {
  modified: '修改',
  added: '新增',
  deleted: '删除',
  renamed: '重命名',
  copied: '复制',
  untracked: '未跟踪',
  conflicted: '冲突',
  changed: '变更',
}

const statusClasses: Record<GitChange['status'], string> = {
  modified: 'bg-amber-100 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300',
  added: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300',
  deleted: 'bg-red-100 text-red-700 dark:bg-red-500/15 dark:text-red-300',
  renamed: 'bg-violet-100 text-violet-700 dark:bg-violet-500/15 dark:text-violet-300',
  copied: 'bg-violet-100 text-violet-700 dark:bg-violet-500/15 dark:text-violet-300',
  untracked: 'bg-sky-100 text-sky-700 dark:bg-sky-500/15 dark:text-sky-300',
  conflicted: 'bg-red-100 text-red-700 dark:bg-red-500/15 dark:text-red-300',
  changed: 'bg-surface-hover text-ink-secondary',
}

async function loadStatus() {
  const id = workspaceId.value
  const serial = ++requestSerial
  status.value = null
  error.value = null
  if (!id) return

  loading.value = true
  try {
    const next = await fetchGitStatus(id)
    if (serial === requestSerial && id === workspaceId.value) {
      status.value = next
    }
  } catch (err) {
    if (serial === requestSerial && id === workspaceId.value) {
      error.value = err instanceof Error ? err.message : '读取 Git 状态失败'
    }
  } finally {
    if (serial === requestSerial) loading.value = false
  }
}

function changeLabel(change: GitChange): string {
  return statusLabels[change.status] ?? '变更'
}

function changeClass(change: GitChange): string {
  return statusClasses[change.status] ?? statusClasses.changed
}
function changeName(change: GitChange): string {
  const normalized = change.path.replaceAll('\\', '/')
  const separator = normalized.lastIndexOf('/')
  return separator >= 0 ? normalized.slice(separator + 1) : normalized
}

function changeTooltip(change: GitChange): string {
  return change.originalPath ? `${change.originalPath} → ${change.path}` : change.path
}

/** 亮色模式使用白底 tooltip，与文件树的完整路径提示保持一致。 */
const tooltipTheme = computed(() =>
  appStore.isDark
    ? {}
    : {
        color: '#fff',
        textColor: '#333',
        boxShadow: '0 2px 8px rgba(0, 0, 0, 0.12)',
      },
)

watch(workspaceId, () => {
  void loadStatus()
})

// GitPanel 仅在 Git Tab 激活时挂载；首次加载不会发生在整个页面首屏渲染阶段。
onMounted(() => {
  void loadStatus()
})
</script>

<template>
  <div class="flex h-full min-h-0 flex-col gap-3 px-3 py-3">
    <div class="flex shrink-0 items-center justify-between">
      <div class="min-w-0">
        <div class="text-sm font-medium text-ink">Git 状态</div>
        <div class="truncate text-xs text-ink-muted" :title="activeWorkspace?.path">
          {{ activeWorkspace?.name || '当前项目' }}
        </div>
      </div>
      <n-button
        quaternary
        circle
        size="tiny"
        title="刷新 Git 状态"
        :loading="loading"
        :disabled="!workspaceId"
        @click="loadStatus"
      >
        <template #icon>
          <n-icon><RefreshOutline /></n-icon>
        </template>
      </n-button>
    </div>

    <div v-if="!workspaceId" class="flex min-h-0 flex-1 items-center justify-center">
      <n-empty size="small" description="请先在左侧添加项目" />
    </div>

    <div v-else-if="loading && !status" class="flex min-h-0 flex-1 items-center justify-center">
      <n-spin size="small" description="正在读取 Git 状态" />
    </div>

    <n-alert v-else-if="error" type="error" :show-icon="false" title="Git 状态读取失败">
      <div class="flex items-center justify-between gap-2">
        <span class="min-w-0 break-all">{{ error }}</span>
        <n-button size="tiny" secondary @click="loadStatus">重试</n-button>
      </div>
    </n-alert>

    <div v-else-if="status && !status.gitInstalled" class="flex min-h-0 flex-1 items-center justify-center">
      <n-empty size="small" description="未检测到 Git" />
    </div>

    <div v-else-if="status && !status.isRepository" class="flex min-h-0 flex-1 items-center justify-center">
      <n-empty size="small" description="当前项目不是 Git 项目" />
    </div>

    <template v-else-if="status">
      <div class="grid shrink-0 grid-cols-2 gap-2 text-xs">
        <div class="rounded-lg bg-surface-hover px-2.5 py-2">
          <div class="text-ink-muted">变更</div>
          <div class="mt-0.5 text-base font-semibold text-ink">{{ status.summary.changed }}</div>
        </div>
        <div class="rounded-lg bg-surface-hover px-2.5 py-2">
          <div class="text-ink-muted">未跟踪</div>
          <div class="mt-0.5 text-base font-semibold text-sky-600 dark:text-sky-400">
            {{ status.summary.untracked }}
          </div>
        </div>
        <div class="rounded-lg bg-surface-hover px-2.5 py-2">
          <div class="text-ink-muted">已暂存</div>
          <div class="mt-0.5 text-base font-semibold text-emerald-600 dark:text-emerald-400">
            {{ status.summary.staged }}
          </div>
        </div>
        <div class="rounded-lg bg-surface-hover px-2.5 py-2">
          <div class="text-ink-muted">未暂存</div>
          <div class="mt-0.5 text-base font-semibold text-amber-600 dark:text-amber-400">
            {{ status.summary.unstaged }}
          </div>
        </div>
      </div>

      <div
        v-if="status.hiddenCount || status.truncated"
        class="shrink-0 rounded-md bg-surface-hover px-2.5 py-2 text-xs text-ink-muted"
      >
        <span v-if="status.hiddenCount">已隐藏 {{ status.hiddenCount }} 个路径</span>
        <span v-if="status.hiddenCount && status.truncated">；</span>
        <span v-if="status.truncated">变更过多，仅显示部分结果</span>
      </div>

      <div v-if="!status.files.length" class="flex min-h-0 flex-1 items-center justify-center">
        <n-empty size="small" description="工作区干净" />
      </div>

      <div v-else class="min-h-0 flex-1 overflow-y-auto">
        <div class="space-y-0.5 p-1">
          <div
            v-for="change in status.files"
            :key="`${change.originalPath ?? ''}:${change.path}:${change.indexStatus}:${change.worktreeStatus}`"
            class="flex min-w-0 items-center gap-2 rounded-md px-2.5 py-2 hover:bg-surface-hover"
          >
            <n-tooltip
              class="min-w-0 flex-1"
              placement="left"
              :theme-overrides="tooltipTheme"
            >
              <template #trigger>
                <div class="flex w-full min-w-0 items-center gap-2">
                  <span class="min-w-0 flex-1 truncate text-xs leading-4 text-ink">
                    {{ changeName(change) }}
                  </span>
                  <span
                    class="inline-flex shrink-0 items-center rounded px-1.5 py-0.5 text-[10px] font-medium leading-4"
                    :class="changeClass(change)"
                  >
                    {{ changeLabel(change) }}
                  </span>
                </div>
              </template>
              <span class="break-all">{{ changeTooltip(change) }}</span>
            </n-tooltip>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>
