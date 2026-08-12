<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { RefreshOutline } from '@vicons/ionicons5'
import { commitGitChanges, fetchGitStatus, pushGit } from '@/api'
import type { GitChange, GitCommitResult, GitStatus } from '@/types/models'
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

// ---- 文件选择（提交目标） ----
// reactive Set：Vue 3 原生支持集合型响应式，增删无需整体替换。
const selected = reactive(new Set<string>())
const message = ref('')
const committing = ref(false)
const pushing = ref(false)
const actionError = ref<string | null>(null)
const commitResult = ref<GitCommitResult | null>(null)

/** 可选中的文件（冲突文件禁止 add：git add 会以工作区内容强制标记 resolved）。 */
const selectableFiles = computed(() => status.value?.files.filter((f) => f.status !== 'conflicted') ?? [])

/** 可见可选文件是否已全部选中（决定「全选/取消全选」文案与行为）。 */
const allSelected = computed(
  () => selectableFiles.value.length > 0 && selectableFiles.value.every((f) => selected.has(f.path)),
)

const canCommit = computed(
  () => selected.size > 0 && message.value.trim() !== '' && !committing.value && !pushing.value,
)

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
      // 刷新后丢弃已不在列表中的勾选（如已提交/已删除的文件），避免提交失效路径
      const known = new Set(next.files.map((f) => f.path))
      for (const p of [...selected]) {
        if (!known.has(p)) selected.delete(p)
      }
    }
  } catch (err) {
    if (serial === requestSerial && id === workspaceId.value) {
      error.value = err instanceof Error ? err.message : '读取 Git 状态失败'
    }
  } finally {
    if (serial === requestSerial) loading.value = false
  }
}

function isSelected(path: string): boolean {
  return selected.has(path)
}

function setSelected(path: string, checked: boolean) {
  if (checked) selected.add(path)
  else selected.delete(path)
}

/** 行点击 / checkbox 切换选中；冲突文件不可选。 */
function toggle(path: string) {
  if (status.value?.files.find((f) => f.path === path)?.status === 'conflicted') return
  setSelected(path, !selected.has(path))
}

/** 全选 / 取消全选（仅作用于可见可选文件；truncated 时超出部分无法覆盖）。 */
function toggleAll() {
  if (allSelected.value) {
    selected.clear()
  } else {
    for (const f of selectableFiles.value) selected.add(f.path)
  }
}

/** 提交选中文件；push=true 时提交并推送。 */
async function submit(push: boolean) {
  const id = workspaceId.value
  if (!id || !canCommit.value) return
  committing.value = true
  actionError.value = null
  commitResult.value = null
  try {
    const result = await commitGitChanges(id, {
      message: message.value.trim(),
      files: [...selected],
      push,
    })
    commitResult.value = result
    if (result.committed) {
      // 提交成功：清空信息与选择，刷新状态（已提交文件从列表消失）
      message.value = ''
      selected.clear()
      void loadStatus()
    }
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : '提交失败'
  } finally {
    committing.value = false
  }
}

/** 重试推送：commit 成功但 push 失败（无网络/无 upstream 等）后的独立入口。 */
async function retryPush() {
  const id = workspaceId.value
  if (!id) return
  pushing.value = true
  actionError.value = null
  try {
    await pushGit(id)
    if (commitResult.value) commitResult.value.pushed = true
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : '推送失败'
  } finally {
    pushing.value = false
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
  // 切换项目：清空提交相关的全部本地状态，避免串到下一个仓库
  selected.clear()
  message.value = ''
  commitResult.value = null
  actionError.value = null
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
        <span v-if="status.truncated">变更过多，仅显示部分结果，全选仅覆盖可见文件</span>
      </div>

      <div v-if="!status.files.length" class="flex min-h-0 flex-1 items-center justify-center">
        <n-empty size="small" description="工作区干净" />
      </div>

      <template v-else>
        <!-- 选择操作条：已选数量 + 全选/取消全选 -->
        <div class="flex shrink-0 items-center justify-between px-1">
          <span class="text-xs text-ink-muted">已选 {{ selected.size }} 项</span>
          <n-button size="tiny" quaternary :disabled="!selectableFiles.length" @click="toggleAll">
            {{ allSelected ? '取消全选' : '全选' }}
          </n-button>
        </div>

        <div class="min-h-0 flex-1 overflow-y-auto">
          <div class="space-y-0.5 p-1">
            <div
              v-for="change in status.files"
              :key="`${change.originalPath ?? ''}:${change.path}:${change.indexStatus}:${change.worktreeStatus}`"
              class="flex min-w-0 cursor-pointer items-center gap-1 rounded-md px-2 py-1.5 hover:bg-surface-hover"
              :class="{ 'opacity-60': change.status === 'conflicted' }"
              @click="toggle(change.path)"
            >
              <n-checkbox
                size="small"
                :checked="isSelected(change.path)"
                :disabled="change.status === 'conflicted'"
                :title="change.status === 'conflicted' ? '存在冲突，请先解决' : undefined"
                @click.stop
                @update:checked="(checked: boolean) => setSelected(change.path, checked)"
              />
              <n-tooltip class="min-w-0 flex-1" placement="left" :theme-overrides="tooltipTheme">
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

        <!-- 底部提交区：信息输入 + 提交 / 提交并推送（仅处理选中文件） -->
        <div class="shrink-0 space-y-2 border-t border-surface-hover pt-3">
          <n-alert v-if="actionError" type="error" :show-icon="false" title="操作失败">
            <span class="break-all">{{ actionError }}</span>
          </n-alert>

          <n-alert
            v-else-if="commitResult"
            :type="commitResult.pushed ? 'success' : 'warning'"
            :show-icon="false"
            :title="commitResult.pushed ? '已提交并推送' : '已提交，但推送失败'"
          >
            <div class="flex items-center justify-between gap-2">
              <span class="min-w-0 break-all text-xs">
                {{
                  commitResult.pushed
                    ? commitResult.commitHash
                      ? `提交 ${commitResult.commitHash.slice(0, 7)} 已推送到远程`
                      : '提交已推送到远程'
                    : `提交 ${commitResult.commitHash?.slice(0, 7) ?? ''} 成功，但推送失败：${commitResult.pushError ?? ''}`
                }}
              </span>
              <n-button v-if="!commitResult.pushed" size="tiny" secondary :loading="pushing" @click="retryPush">
                重试推送
              </n-button>
            </div>
          </n-alert>

          <n-input
            v-model:value="message"
            type="textarea"
            size="small"
            :autosize="{ minRows: 1, maxRows: 3 }"
            placeholder="提交信息（必填）"
            :disabled="committing || pushing"
          />
          <div class="flex gap-2">
            <n-button size="small" type="primary" :disabled="!canCommit" :loading="committing" @click="submit(false)">
              提交
            </n-button>
            <n-button size="small" secondary :disabled="!canCommit" :loading="committing" @click="submit(true)">
              提交并推送
            </n-button>
          </div>
        </div>
      </template>
    </template>
  </div>
</template>
