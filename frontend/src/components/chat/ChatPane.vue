<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useAgentStore } from '@/stores/agent'
import { useSessionStore } from '@/stores/session'
import WelcomeHero from '@/components/chat/WelcomeHero.vue'
import MessageList from '@/components/chat/MessageList.vue'
import Composer, {
  type ComposerSubmitPayload,
} from '@/components/chat/Composer.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const agentStore = useAgentStore()
const sessionStore = useSessionStore()

/** 当前路由 sessionId；null 表示空态 */
const sessionId = computed(() => {
  const raw = route.params.sessionId
  return raw ? Number(raw) : null
})

/** 进入 /sessions/:id 时：同步选中 + 按需加载消息历史（覆盖刷新 / 直达链接） */
watch(
  sessionId,
  (id) => {
    sessionStore.currentId = id
    if (id !== null) {
      void sessionStore.loadMessages(id).catch(() => {
        // 历史加载失败不阻塞界面；P2 简化静默，后续可加重试
      })
      // 加载会话配置项（模型/思考强度/mode；agent 不支持时为空，前端隐藏）
      void sessionStore.loadConfigOptions(id)
    }
  },
  { immediate: true },
)

/** 当前会话对象；null → 空态 */
const current = computed(() => sessionStore.activeSession)

/** 会话头部 Agent 标签文案 */
function agentNameOf(agentId: string): string {
  return agentStore.list.find((a) => a.agentId === agentId)?.name ?? agentId
}

/**
 * 发送：空态先创建会话（首次发送才创建，POST /api/v1/sessions），
 * 再走 store.sendViaWs（WS prompt + 流式事件；P2）。
 */
async function onSubmit(payload: ComposerSubmitPayload) {
  const text = payload.text.trim()
  if (!text || sessionStore.streaming) {
    return
  }

  let session = current.value
  try {
    if (!session) {
      session = await sessionStore.createSession(
        payload.agentId,
        payload.workspaceId,
      )
      if (sessionId.value !== session.id) {
        await router.push({
          name: 'session',
          params: { sessionId: String(session.id) },
        })
      }
    }
    await sessionStore.sendViaWs(session.id, text)
  } catch (e) {
    sessionStore.streamError = e instanceof Error ? e.message : String(e)
  }
}
</script>

<template>
  <!-- 空态 / 会话中切换 -->
  <template v-if="current">
    <div class="flex min-h-0 flex-1 flex-col">
      <!-- （可选）SessionHeader：标题 + Agent 标签 -->
      <div class="flex items-center gap-2 border-b border-slate-200 px-4 py-2.5">
        <span class="truncate text-sm font-medium text-slate-800">
          {{ current.title || t('chat.newChatTitle') }}
        </span>
        <span
          class="shrink-0 rounded bg-slate-100 px-1.5 py-0.5 text-xs text-slate-500"
        >
          {{ agentNameOf(current.agentId) }}
        </span>
      </div>

      <MessageList class="min-h-0 flex-1" />

      <!-- 发送/流式错误条（可关闭） -->
      <div
        v-if="sessionStore.streamError"
        class="mx-4 mb-2 flex items-center justify-between rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 ring-1 ring-inset ring-red-100"
      >
        <span class="truncate">
          {{ t('chat.errorTitle') }}: {{ sessionStore.streamError }}
        </span>
        <button
          class="ml-3 shrink-0 text-red-400 hover:text-red-600"
          aria-label="close"
          @click="sessionStore.clearStreamError()"
        >
          ✕
        </button>
      </div>

      <div class="px-4 pb-4 pt-2">
        <Composer
          mode="bar"
          :agent-id="current.agentId"
          :sending="sessionStore.streaming"
          @submit="onSubmit"
          @cancel="sessionStore.cancelSend()"
        />
      </div>
    </div>
  </template>
  <WelcomeHero v-else @submit="onSubmit" />
</template>
