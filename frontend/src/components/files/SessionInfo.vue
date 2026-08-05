<script setup lang="ts">
/**
 * SessionInfo — 「信息」Tab：当前会话的概要信息卡片。
 *
 * 数据源为 useSessionStore().activeSession（后端 GET /api/v1/sessions/:id 的
 * model.Session，字段与后端 json tag 对齐）。仅展示有区分度的信息：
 * Agent、工作区、状态、创建/更新时间；会话 ID / ACP 会话 ID / 标题在侧栏
 * 列表已可见，此处不再重复。
 *
 * 边界说明：Token 用量 / context 占用等 ACP usage 信息（usage_update 通知）
 * 后端暂未接入（agent 也未必上报），故本组件不包含用量区块——见 AGENTS.md
 * 选型讨论「信息 Tab 分三层渐进」的规划。
 */
import { computed, onMounted } from 'vue'
import { FolderOutline, TimeOutline } from '@vicons/ionicons5'
import { useAgentStore } from '@/stores/agent'
import { useSessionStore } from '@/stores/session'
import type { SessionStatus } from '@/types/models'

/** 头像底色色板（Tailwind 完整类名，禁止动态拼接） */
const AVATAR_COLORS = [
  'bg-blue-500',
  'bg-emerald-500',
  'bg-violet-500',
  'bg-amber-500',
  'bg-rose-500',
  'bg-cyan-600',
] as const

/** 会话状态 → 展示文案 + n-tag 颜色（后端 model.SessionStatus） */
const statusMap: Record<SessionStatus, { text: string; type: 'success' | 'info' | 'error' }> = {
  active: { text: '活动', type: 'success' },
  closed: { text: '已关闭', type: 'info' },
  error: { text: '错误', type: 'error' },
}

const sessionStore = useSessionStore()
const agentStore = useAgentStore()

/** 当前会话；null 对应无会话空态 */
const session = computed(() => sessionStore.activeSession)

/** Agent 显示名：列表中有 name 则用 name，否则退化为 agentId */
const agentName = computed(() => {
  const s = session.value
  if (!s) return ''
  return agentStore.list.find((a) => a.agentId === s.agentId)?.name ?? s.agentId
})

/** 头像文字：Agent 名首字符（中文名取前两个字符） */
const avatarText = computed(() => {
  const name = agentName.value.trim()
  if (!name) return '?'
  return [...name].slice(0, 2).join('').toUpperCase()
})

/** 头像底色：按 agentId 哈希从色板取色，同一 Agent 恒定同色 */
const avatarClass = computed(() => {
  const s = session.value
  if (!s) return 'bg-gray-400'
  let h = 0
  for (let i = 0; i < s.agentId.length; i++) {
    h = (h * 31 + s.agentId.charCodeAt(i)) >>> 0
  }
  return AVATAR_COLORS[h % AVATAR_COLORS.length]
})

/** 工作区显示：优先 name，其次 path */
const workspaceLabel = computed(() => {
  const ws = session.value?.workspace
  if (!ws) return ''
  return ws.name || ws.path
})

/** 绝对时间格式化（本地时区 YYYY-MM-DD HH:mm，精确到分钟）；后端 time.Time 序列化为 ISO 8601 */
function formatDateTime(iso?: string): string {
  if (!iso) return '-'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '-'
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

// Agent 列表可能尚未加载（页面不经过侧栏加载时），按需拉一次以便显示名称
onMounted(() => {
  if (!agentStore.list.length) {
    agentStore.load()
  }
})
</script>

<template>
  <div class="px-3 py-3">
    <n-empty v-if="!session" size="small" description="暂无会话" />
    <div v-else class="space-y-3">
      <!-- 头部：Agent 头像 + 名称 + 状态 -->
      <div class="flex items-center gap-2.5">
        <div
          class="flex h-10 w-10 shrink-0 items-center justify-center rounded-full text-sm font-semibold text-white shadow-sm"
          :class="avatarClass"
        >
          {{ avatarText }}
        </div>
        <div class="min-w-0 flex-1">
          <div class="truncate text-sm font-medium text-gray-800">{{ agentName }}</div>
          <div class="truncate text-xs text-gray-400">{{ session.agentId }}</div>
        </div>
        <n-tag size="small" :bordered="false" :type="statusMap[session.status].type">
          {{ statusMap[session.status].text }}
        </n-tag>
      </div>

      <!-- 工作区卡片：路径可能很长，允许换行 -->
      <div class="flex items-start gap-2 rounded-lg bg-gray-50 px-2.5 py-2">
        <n-icon :component="FolderOutline" class="mt-0.5 shrink-0 text-base text-gray-400" />
        <div class="min-w-0">
          <div class="text-xs text-gray-400">工作区</div>
          <div class="break-all text-xs leading-5 text-gray-700">{{ workspaceLabel || '-' }}</div>
        </div>
      </div>

      <!-- 时间区：完整时间（年月日 + 分钟），不再用相对时间 -->
      <div class="divide-y divide-gray-100 rounded-lg border border-gray-100">
        <div class="flex items-center gap-2 px-2.5 py-2">
          <n-icon :component="TimeOutline" class="shrink-0 text-base text-gray-400" />
          <span class="w-10 shrink-0 text-xs text-gray-400">创建</span>
          <span class="truncate text-xs text-gray-600">
            {{ formatDateTime(session.createdAt) }}
          </span>
        </div>
        <div class="flex items-center gap-2 px-2.5 py-2">
          <n-icon :component="TimeOutline" class="shrink-0 text-base text-gray-400" />
          <span class="w-10 shrink-0 text-xs text-gray-400">更新</span>
          <span class="truncate text-xs text-gray-600">
            {{ formatDateTime(session.updatedAt) }}
          </span>
        </div>
      </div>
    </div>
  </div>
</template>
