<script setup lang="ts">
/**
 * FilesPage — 手机端独立文件浏览页（/sessions/:id/files）。
 *
 * PC 的文件组件是右侧面板内嵌展示（FilePanel → FileExplorer）；
 * 手机端因右侧面板被整体隐藏，通过移动顶栏的文件夹按钮进入本页，
 * 复用同一个 FileExplorer 组件、同一会话（workspace）上下文。
 *
 * 布局：顶部条（返回 + 标题）+ FileExplorer 主体，独立于壳层（无汉堡/侧栏）。
 * 标题取 workspace 路径最后一段（项目名），见上方 FileExplorer 的 wsBaseName 逻辑。
 */
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ArrowBackOutline } from '@vicons/ionicons5'
import FileExplorer from '@/components/files/FileExplorer.vue'
import { useSessionStore } from '@/stores/session'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const sessionStore = useSessionStore()

const sessionId = computed(() => {
  const raw = route.params.sessionId
  return raw ? Number(raw) : null
})

/** 是否已就绪（会话解析成功）——FileExplorer 挂载时会读取 sessionStore.activeSession，
 *  必须在它挂载前把 currentId 设好且会话已并入 sessions 列表（resolveSession 完成），
 *  否则会回退到默认 workspace 显示错误项目。故用 v-if 延迟渲染。 */
const ready = ref(false)
const errorMsg = ref<string | null>(null)

onMounted(async () => {
  const id = sessionId.value
  if (id === null) {
    errorMsg.value = 'invalid session'
    return
  }
  sessionStore.currentId = id
  try {
    await sessionStore.resolveSession(id)
    ready.value = true
  } catch (e) {
    errorMsg.value = e instanceof Error ? e.message : String(e)
  }
})

/** 标题：workspace 绝对路径最后一段（项目名），未就绪时回退通用文案 */
const pageTitle = computed(() => {
  const p = sessionStore.activeSession?.workspace?.path ?? ''
  const base = p.replace(/\/+$/, '').split('/').pop()
  return base || t('chat.filesTitle')
})

/** 返回聊天页：固定跳回 /sessions/:id（不用 router.back，深链进入时历史栈可能为空） */
function goBack() {
  const id = sessionId.value
  void router.push({ name: 'session', params: { sessionId: String(id) } })
}
</script>

<template>
  <div class="flex h-screen supports-[height:100dvh]:h-dvh flex-col bg-surface-raised">
    <!-- 顶部条：返回 + 标题；样式与 AppShell 移动顶栏对齐（刘海/横屏安全区一致） -->
    <header
      class="flex min-h-12 shrink-0 items-center gap-2 border-b border-divider bg-surface pt-[env(safe-area-inset-top)] pl-[max(env(safe-area-inset-left),0.75rem)] pr-[max(env(safe-area-inset-right),0.75rem)]"
    >
      <button
        type="button"
        aria-label="返回聊天"
        class="-ml-1 flex h-9 w-9 shrink-0 cursor-pointer items-center justify-center rounded-lg text-ink-secondary transition-colors hover:bg-surface-hover active:bg-surface-active focus:outline-none focus-visible:ring-2 focus-visible:ring-primary/50"
        @click="goBack"
      >
        <ArrowBackOutline class="h-5 w-5" />
      </button>
      <span class="min-w-0 flex-1 truncate text-sm font-medium text-ink">
        {{ pageTitle }}
      </span>
    </header>

    <!-- 主体：会话解析成功后渲染 FileExplorer（见 ready 注释）；失败/未就绪给占位与错误提示 -->
    <main class="min-h-0 flex-1 overflow-hidden">
      <div
        v-if="ready"
        class="h-full"
      >
        <FileExplorer />
      </div>
      <div
        v-else
        class="flex h-full flex-col items-center justify-center gap-3 px-6 text-center"
      >
        <template v-if="errorMsg">
          <p class="text-sm font-medium text-ink">{{ t('chat.sessionLoadFailed') }}</p>
          <p class="max-w-md break-words text-xs text-ink-muted">{{ errorMsg }}</p>
          <n-button size="small" secondary @click="goBack">
            {{ t('common.cancel') }}
          </n-button>
        </template>
        <span v-else class="text-sm text-ink-muted">{{ t('common.loading') }}</span>
      </div>
    </main>
  </div>
</template>