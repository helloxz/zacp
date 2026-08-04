<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { TrashOutline } from '@vicons/ionicons5'
import { NIcon } from 'naive-ui'
import { useAgentStore } from '@/stores/agent'
import { useAppStore } from '@/stores/app'
import { useSessionStore } from '@/stores/session'
import type { ChatSession } from '@/types/models'
import { formatRelativeTime } from '@/utils/relativeTime'

const props = defineProps<{ session: ChatSession }>()

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const agentStore = useAgentStore()
const appStore = useAppStore()
const sessionStore = useSessionStore()

/** 当前路由是否正展示该会话（驱动高亮） */
const isActive = computed(
  () => Number(route.params.sessionId) === props.session.id,
)

/** Agent 副文案：按 agentId 从 agent store 查名（后端 Session 不冗余 agentName） */
const agentName = computed(() => {
  const agent = agentStore.list.find((a) => a.agentId === props.session.agentId)
  return agent?.name ?? props.session.agentId
})

const title = computed(() => props.session.title || t('chat.newChatTitle'))

const relativeTime = computed(() =>
  formatRelativeTime(props.session.updatedAt, appStore.locale),
)

function onSelect() {
  if (isActive.value) return
  sessionStore.currentId = props.session.id
  void router.push({
    name: 'session',
    params: { sessionId: String(props.session.id) },
  })
}

/** 删除会话；若正展示该会话则回空态 */
async function onDelete() {
  try {
    await sessionStore.removeSession(props.session.id)
    if (isActive.value) {
      void router.push({ name: 'home' })
    }
  } catch {
    // 删除失败由 n-popconfirm 内部吞掉？不——上抛给全局处理（P1 简化：静默 + 控制台）
  }
}
</script>

<template>
  <div
    class="group flex w-full cursor-pointer items-center gap-1 rounded-lg px-2.5 py-2 transition-colors"
    :class="isActive ? 'bg-slate-200/70' : 'hover:bg-slate-200/50'"
    role="button"
    tabindex="0"
    @click="onSelect"
    @keydown.enter="onSelect"
  >
    <div class="flex min-w-0 flex-1 flex-col gap-0.5">
      <span
        class="truncate text-sm"
        :class="isActive ? 'font-medium text-slate-900' : 'text-slate-700'"
      >
        {{ title }}
      </span>
      <span class="flex items-center gap-1.5 text-xs text-slate-400">
        <span class="truncate">{{ agentName }}</span>
        <span aria-hidden="true">·</span>
        <span class="shrink-0">{{ relativeTime }}</span>
      </span>
    </div>

    <!-- hover 出现删除（确认后删除） -->
    <n-popconfirm :positive-text="t('common.confirm')" :negative-text="t('common.cancel')" @positive-click="onDelete">
      <template #trigger>
        <n-button
          quaternary
          circle
          size="tiny"
          class="shrink-0 opacity-0 transition-opacity group-hover:opacity-100 focus:opacity-100"
          @click.stop
        >
          <template #icon>
            <n-icon><TrashOutline /></n-icon>
          </template>
        </n-button>
      </template>
      {{ t('shell.confirmDelete') }}
    </n-popconfirm>
  </div>
</template>
