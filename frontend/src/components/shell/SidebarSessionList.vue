<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useSessionStore } from '@/stores/session'
import type { ChatSession, Workspace } from '@/types/models'
import SessionListItem from '@/components/shell/SessionListItem.vue'

const { t } = useI18n()
const sessionStore = useSessionStore()

/**
 * 两级结构：按 workspace 分组（数据来自后端预加载的 session.workspace；
 * 兜底用本地 workspaces 匹配 workspaceId，避免偶发未预加载导致分组丢失）。
 */
const groups = computed(() => {
  const map = new Map<number, { workspace: Workspace; sessions: ChatSession[] }>()
  for (const s of sessionStore.sessions) {
    const ws =
      s.workspace ??
      sessionStore.workspaces.find((w) => w.id === s.workspaceId)
    if (!ws) {
      continue
    }
    let group = map.get(ws.id)
    if (!group) {
      group = { workspace: ws, sessions: [] }
      map.set(ws.id, group)
    }
    group.sessions.push(s)
  }
  return [...map.values()]
})

const hasAny = computed(() => sessionStore.sessions.length > 0)
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
        <template v-if="group.sessions.length">
          <div class="flex items-center justify-between px-1 pt-2">
            <span class="text-xs font-medium text-slate-400">
              {{ group.workspace.name || group.workspace.path }}
            </span>
          </div>
          <SessionListItem
            v-for="s in group.sessions"
            :key="s.id"
            :session="s"
          />
        </template>
      </div>
    </template>

    <n-empty v-else size="small" :description="t('shell.noSessions')" />
  </div>
</template>
