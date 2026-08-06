<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import {
  AddOutline,
  FolderOpenOutline,
  FolderOutline,
  TrashOutline,
} from '@vicons/ionicons5'
import { NIcon, useMessage } from 'naive-ui'
import { useSessionStore } from '@/stores/session'
import { useAppStore } from '@/stores/app'
import type { ChatSession, Workspace } from '@/types/models'
import SessionListItem from '@/components/shell/SessionListItem.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const sessionStore = useSessionStore()
const appStore = useAppStore()
const message = useMessage()

/** tooltip 主题：浅色下用白底浅字（默认深色底，项目行内视觉过重）；暗色下跟随 Naive 主题 */
const tooltipTheme = computed(() =>
  appStore.isDark
    ? {}
    : {
        color: '#ffffff',
        textColor: '#334155',
        boxShadow: '0 2px 10px rgba(15, 23, 42, 0.1)',
      },
)

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

/**
 * 每项目可见会话条数（渲染截断）：默认 20，点「查看更多」+20，最多 40 后按钮消失。
 * 数据仍全量在 store（后端一次返回 ≤1000），只限制 DOM 渲染量，避免长列表压力。
 */
const PAGE_SIZE = 20
const MAX_VISIBLE = PAGE_SIZE * 2
const visibleCount = reactive<Record<number, number>>({})

/** 当前项目实际渲染的会话（后端已按 updatedAt 倒序，取前 N 条即最近使用） */
function visibleSessions(group: { workspace: Workspace; sessions: ChatSession[] }) {
  const n = visibleCount[group.workspace.id] ?? PAGE_SIZE
  return group.sessions.slice(0, n)
}

/** 是否显示「查看更多」：会话数超过当前可见数，且未到上限 40 */
function canLoadMore(group: { workspace: Workspace; sessions: ChatSession[] }) {
  const n = visibleCount[group.workspace.id] ?? PAGE_SIZE
  return group.sessions.length > n && n < MAX_VISIBLE
}

/** 点击「查看更多」：每项目最多加 1 次（20 → 40），计数不随列表刷新重置，保持用户已展开的量 */
function loadMore(wsId: number) {
  visibleCount[wsId] = Math.min((visibleCount[wsId] ?? PAGE_SIZE) + PAGE_SIZE, MAX_VISIBLE)
}

/**
 * 已展开（显示会话列表）的项目 id 集合：独立开关，可同时展开多个项目。
 */
const expandedIds = ref<Set<number>>(new Set())

/**
 * 首次加载完成时默认只展开第一个项目（groups[0]，最近使用的），
 * 之后用户手动展开/折叠的状态不被数据刷新重置。
 */
let expandedInitialized = false
watch(
  groups,
  (gs) => {
    if (!expandedInitialized && gs.length > 0) {
      expandedIds.value = new Set([gs[0].workspace.id])
      expandedInitialized = true
    }
  },
  { immediate: true },
)

/**
 * 当前 URL 会话 id（/sessions/:id）；非会话路由（/、/new）为 null。
 * 刷新后组件重挂载、store 清空，需在数据到达后据此定位父项目。
 */
const currentSessionId = computed(() => {
  const raw = route.params.sessionId
  return raw ? Number(raw) : null
})

/**
 * 确保当前 URL 会话在侧栏可见：
 * 1) 展开其父项目（加入 expandedIds，并集语义，不影响其它已展开项目）；
 * 2) 若会话排在项目可见截断（每项目默认 20 条）之外，提升可见数到包含它，
 *    保证手动刷新 /sessions/:id 后侧栏能看到并高亮当前会话（选中高亮由
 *    SessionListItem 按 route.params.sessionId 驱动，展开后即生效）。
 *
 * 幂等：会话已可见时无副作用；数据未到时直接返回，等数据到达后由 watch 重试。
 * 破例可超 MAX_VISIBLE：活跃会话优先于渲染截断，避免「展开但看不到选中项」的困惑。
 */
function revealCurrentSession() {
  const id = currentSessionId.value
  if (id === null) return
  const s = sessionStore.sessions.find((x) => x.id === id)
  if (!s) return
  // 与 groups 同一套 workspace 解析：软删除 workspace 的会话（id=0 空对象）跳过
  const ws = s.workspace?.id
    ? s.workspace
    : sessionStore.workspaces.find((w) => w.id === s.workspaceId)
  if (!ws) return
  expandedIds.value = new Set(expandedIds.value).add(ws.id)
  // 可见条数：当前会话被截断时提升到包含它（取 max，不回调用户已展开的量）
  const group = groups.value.find((g) => g.workspace.id === ws.id)
  const idx = group?.sessions.findIndex((x) => x.id === id) ?? -1
  if (idx >= 0 && idx >= (visibleCount[ws.id] ?? PAGE_SIZE)) {
    visibleCount[ws.id] = idx + 1
  }
}

// 会话路由下保持当前会话可见：sessionId 变化（immediate 覆盖刷新挂载）
// 与 sessions 数据到达/刷新（首屏迟到、loadSessions 整体替换）都触发重定位；
// 幂等实现保证重复触发无副作用。
watch(
  [currentSessionId, () => sessionStore.sessions],
  () => revealCurrentSession(),
  { immediate: true },
)

/** 切换项目的展开/折叠（点击项目名整行触发） */
function toggleWorkspace(id: number) {
  const next = new Set(expandedIds.value)
  if (next.has(id)) {
    next.delete(id)
  } else {
    next.add(id)
  }
  expandedIds.value = next
}

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
        <!-- 项目头：整行可点击，切换该项目会话列表的展开/折叠 -->
        <div
          class="group/header flex cursor-pointer items-center justify-between rounded px-1 py-1.5 transition-colors hover:bg-surface-hover"
          role="button"
          tabindex="0"
          @click="toggleWorkspace(group.workspace.id)"
          @keydown.enter.self="toggleWorkspace(group.workspace.id)"
        >
          <!-- 文件夹图标（展开时切换为打开状态，兼作展开指示）+ 项目名（淡色，hover 加深） -->
          <span
            class="flex min-w-0 flex-1 items-center gap-1.5"
            :title="group.workspace.path"
          >
            <n-icon :size="15" class="shrink-0 text-ink-muted">
              <FolderOpenOutline v-if="expandedIds.has(group.workspace.id)" />
              <FolderOutline v-else />
            </n-icon>
            <span
              class="min-w-0 truncate text-sm font-semibold text-ink-secondary transition-colors group-hover/header:text-ink"
            >
              {{ projectName(group.workspace) }}
            </span>
          </span>
          <!-- hover 显示的操作区：移除（左）+ 新建会话（右）；n-button text 纯图标按钮，不占宽度；
               @click.stop 防止点击操作按钮误触项目头的展开/折叠 -->
          <div
            class="flex shrink-0 items-center gap-1 opacity-0 transition-opacity focus-within:opacity-100 group-hover/header:opacity-100"
            @click.stop
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
                      class="text-ink-muted hover:text-red-500"
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
                  class="text-ink-muted hover:text-ink-secondary"
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
        <!-- 项目下的会话列表（仅展开时渲染；每项目默认 20 条，超出显示「查看更多」） -->
        <template v-if="expandedIds.has(group.workspace.id)">
          <SessionListItem
            v-for="s in visibleSessions(group)"
            :key="s.id"
            :session="s"
          />
          <!-- 查看更多：+20 条；达到 40 上限后按钮消失（最多加载 1 次） -->
          <n-button
            v-if="canLoadMore(group)"
            text
            size="small"
            class="w-full justify-center text-xs text-ink-muted hover:text-ink-secondary"
            @click="loadMore(group.workspace.id)"
          >
            {{ t('shell.loadMoreSessions') }}
          </n-button>
        </template>
      </div>
    </template>

    <!-- 无任何项目：引导新建项目 -->
    <n-empty v-else size="small" :description="t('shell.noProjectsHint')" />
  </div>
</template>
