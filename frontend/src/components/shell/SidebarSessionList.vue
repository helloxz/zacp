<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { AddOutline, TrashOutline } from '@vicons/ionicons5'
import { NIcon, useMessage } from 'naive-ui'
import { useSessionStore } from '@/stores/session'
import type { ChatSession, Workspace } from '@/types/models'
import SessionListItem from '@/components/shell/SessionListItem.vue'

const { t } = useI18n()
const router = useRouter()
const sessionStore = useSessionStore()
const message = useMessage()

/** tooltip 白底浅字主题（默认深色底，项目行内视觉过重） */
const tooltipTheme = {
  color: '#ffffff',
  textColor: '#334155',
  boxShadow: '0 2px 10px rgba(15, 23, 42, 0.1)',
}

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
    // 注意：后端 Preload 对已软删除（被移除）的 workspace 会序列化为空对象（id=0），
    // 不能用 truthy 判断——必须校验 id 有效，否则被移除项目的会话会落到「无名分组」继续显示。
    const ws =
      s.workspace?.id
        ? s.workspace
        : sessionStore.workspaces.find((w) => w.id === s.workspaceId)
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

/** 移除项目（软删除）：项目从侧栏隐藏，同路径再次添加时整体恢复（含会话/消息） */
async function onRemoveWorkspace(ws: Workspace) {
  try {
    await sessionStore.removeWorkspace(ws.id)
  } catch (e) {
    message.error(e instanceof Error ? e.message : String(e))
  }
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
          <!-- hover 显示的操作区：移除（左）+ 新建会话（右）；n-button text 纯图标按钮，不占宽度 -->
          <div
            class="flex shrink-0 items-center gap-1 opacity-0 transition-opacity focus-within:opacity-100 group-hover/header:opacity-100"
          >
            <!-- 移除项目：图标按钮 + tooltip（顶部弹出，白底浅字，避免遮挡右侧的新建会话图标）；
                 点击弹 popconfirm 确认（软删除，可同路径恢复） -->
            <n-tooltip
              trigger="hover"
              placement="top"
              :theme-overrides="tooltipTheme"
            >
              <template #trigger>
                <n-popconfirm
                  :positive-text="t('common.confirm')"
                  :negative-text="t('common.cancel')"
                  @positive-click="onRemoveWorkspace(group.workspace)"
                >
                  <template #trigger>
                    <n-button
                      text
                      size="small"
                      class="text-slate-400 hover:text-red-500"
                      :aria-label="t('shell.removeProject')"
                    >
                      <template #icon>
                        <n-icon :size="18"><TrashOutline /></n-icon>
                      </template>
                    </n-button>
                  </template>
                  {{ t('shell.removeProjectConfirm', { name: projectName(group.workspace) }) }}
                </n-popconfirm>
              </template>
              {{ t('shell.removeProject') }}
            </n-tooltip>

            <!-- 新建会话：图标按钮 + tooltip（顶部弹出，白底浅字）；进入该项目的 /new 空态 -->
            <n-tooltip
              trigger="hover"
              placement="top"
              :theme-overrides="tooltipTheme"
            >
              <template #trigger>
                <n-button
                  text
                  size="small"
                  class="text-slate-400 hover:text-slate-700"
                  :aria-label="t('shell.newSession')"
                  @click="onNewSessionInWorkspace(group.workspace.id)"
                >
                  <template #icon>
                    <n-icon :size="18"><AddOutline /></n-icon>
                  </template>
                </n-button>
              </template>
              {{ t('shell.newSession') }}
            </n-tooltip>
          </div>
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
