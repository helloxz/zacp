<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { AddOutline } from '@vicons/ionicons5'
import { NIcon } from 'naive-ui'
import { useSessionStore } from '@/stores/session'
import type { ChatSession, Workspace } from '@/types/models'
import SessionListItem from '@/components/shell/SessionListItem.vue'

const { t } = useI18n()
const router = useRouter()
const sessionStore = useSessionStore()

/**
 * 两级结构：按 workspace 分组（数据来自后端预加载的 session.workspace；
 * 兜底用本地 workspaces 匹配 workspaceId，避免偶发未预加载导致分组丢失）。
 *
 * 分组顺序：有会话的项目在前（按最近使用），无会话项目仍展示但排在后面，
 * 方便用户在任何已有项目下新建会话。
 */
const groups = computed(() => {
  const map = new Map<number, { workspace: Workspace; sessions: ChatSession[] }>()
  // 先收集有会话的项目
  for (const s of sessionStore.sessions) {
    const ws =
      s.workspace ??
      sessionStore.workspaces.find((w) => w.id === s.workspaceId)
    if (!ws) continue
    let group = map.get(ws.id)
    if (!group) {
      group = { workspace: ws, sessions: [] }
      map.set(ws.id, group)
    }
    group.sessions.push(s)
  }
  // 补充无会话的项目（显示项目名 + 「+」，下方无会话列表）
  for (const w of sessionStore.workspaces) {
    if (w.archived) continue
    if (!map.has(w.id)) {
      map.set(w.id, { workspace: w, sessions: [] })
    }
  }
  return [...map.values()]
})

const hasAny = computed(
  () => sessionStore.sessions.length > 0 || sessionStore.workspaces.length > 0,
)

/** 项目显示名：优先用 workspace.name（后端取末尾段填充） */
function projectName(ws: Workspace): string {
  return ws.name || ws.path.split('/').pop() || ws.path
}

/** 在该项目下新建会话：进入 /new?workspaceId=X 空态 */
function onNewSessionInWorkspace(wsId: number) {
  void router.push({ name: 'new', query: { workspaceId: String(wsId) } })
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <!-- 首屏加载态 -->
    <n-spin v-if="sessionStore.loading" size="small" class="w-full py-10" />

    <template v-else-if="hasAny">
      <div
        v-for="group in groups"
        :key="group.workspace.id"
        class="flex flex-col gap-1"
      >
        <div
          class="group/header flex items-center justify-between rounded px-1 pt-2"
        >
          <!-- 项目名：加粗、字体加大（设计约定：只取末尾文件夹名） -->
          <span
            class="truncate text-base font-bold text-slate-800"
            :title="group.workspace.path"
          >
            {{ projectName(group.workspace) }}
          </span>
          <!-- hover 出现「+」新建会话（tooltip 提示） -->
          <n-tooltip trigger="hover" placement="right">
            <template #trigger>
              <button
                type="button"
                class="shrink-0 rounded p-0.5 text-slate-400 opacity-0 transition-opacity hover:bg-slate-200/60 hover:text-slate-700 focus:opacity-100 group-hover/header:opacity-100"
                :aria-label="t('shell.newSession')"
                @click="onNewSessionInWorkspace(group.workspace.id)"
              >
                <n-icon size="18"><AddOutline /></n-icon>
              </button>
            </template>
            {{ t('shell.newSession') }}
          </n-tooltip>
        </div>
        <!-- 项目下的会话列表 -->
        <SessionListItem
          v-for="s in group.sessions"
          :key="s.id"
          :session="s"
        />
      </div>
    </template>

    <!-- 无任何项目：引导新建项目 -->
    <n-empty v-else size="small" :description="t('shell.noProjectsHint')" />
  </div>
</template>
