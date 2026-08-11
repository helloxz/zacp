<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useDialog, useMessage } from 'naive-ui'
import { fetchWorkspace } from '@/api'
import { useAppStore } from '@/stores/app'
import TtyTabs from '@/components/tty/TtyTabs.vue'
import TtyTerminal from '@/components/tty/TtyTerminal.vue'
import { useTtyManager, type TtyTabState } from '@/composables/useTtyManager'
import type { Workspace } from '@/types/models'
import type { TtyTabStatus } from '@/types/tty'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const dialog = useDialog()
const message = useMessage()
const appStore = useAppStore()
const {
  tabs,
  activeTabId,
  canCreate,
  createTab: createManagedTab,
  selectTab,
  updateTab,
  removeTab,
  closeAll,
} = useTtyManager()

const workspace = ref<Workspace | null>(null)
const loading = ref(true)
const pageError = ref<string | null>(null)
let loadTicket = 0

const workspaceId = computed(() => {
  const raw = route.query.workspaceId
  if (typeof raw !== 'string' || !/^\d+$/.test(raw)) return null
  const parsed = Number(raw)
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : null
})

async function loadWorkspace() {
  const ticket = ++loadTicket
  loading.value = true
  pageError.value = null
  workspace.value = null
  closeAll()

  if (workspaceId.value === null) {
    pageError.value = t('tty.workspaceMissing')
    loading.value = false
    return
  }

  try {
    workspace.value = await fetchWorkspace(workspaceId.value)
    if (ticket !== loadTicket) return
    createManagedTab()
  } catch (error) {
    if (ticket !== loadTicket) return
    pageError.value = error instanceof Error ? error.message : t('tty.workspaceNotFound')
  } finally {
    if (ticket === loadTicket) loading.value = false
  }
}

function goBack() {
  void router.push({ name: 'home' })
}

function createTab() {
  if (!createManagedTab()) message.warning(t('tty.limit'))
}

function updateStatus(tabId: string, status: TtyTabStatus) {
  updateTab(tabId, { status })
}

function handleReady(tabId: string, terminalId: string) {
  updateTab(tabId, { status: 'connected', terminalId })
}

function handleExit(tabId: string, code: number) {
  updateTab(tabId, { status: 'exited', exitCode: code })
}

function handleError(tabId: string, error: string) {
  updateTab(tabId, { status: 'error', error })
  message.error(error)
}

function closeTab(tab: TtyTabState) {
  const needsConfirm = tab.status === 'creating' || tab.status === 'connecting' || tab.status === 'connected'
  if (!needsConfirm) {
    removeTab(tab.id)
    return
  }
  dialog.warning({
    title: t('tty.closeConfirmTitle'),
    content: t('tty.closeConfirm'),
    positiveText: t('common.confirm'),
    negativeText: t('common.cancel'),
    onPositiveClick: () => {
      removeTab(tab.id)
    },
  })
}

onMounted(() => {
  void loadWorkspace()
})

onBeforeRouteLeave(() => {
  closeAll()
})

onBeforeUnmount(() => {
  closeAll()
})
</script>

<template>
  <div class="tty-page flex h-screen min-h-0 flex-col overflow-hidden bg-surface text-ink" :class="{ dark: appStore.isDark }">
    <TtyTabs
      :tabs="tabs"
      :active-tab-id="activeTabId"
      :can-create="canCreate"
      @select="selectTab"
      @create="createTab"
      @close="closeTab"
    />

    <main class="min-h-0 flex-1 bg-surface p-3">
      <div v-if="loading" class="flex h-full items-center justify-center">
        <n-spin size="large" :description="t('tty.loadingWorkspace')" />
      </div>
      <n-result
        v-else-if="pageError"
        status="error"
        :title="t('tty.error')"
        :description="pageError"
        class="flex h-full items-center justify-center"
      >
        <template #footer>
          <n-button type="primary" @click="goBack">{{ t('tty.back') }}</n-button>
        </template>
      </n-result>
      <div v-else-if="tabs.length === 0" class="flex h-full flex-col items-center justify-center gap-2 text-ink-muted">
        <div class="text-base font-medium text-ink">{{ t('tty.empty') }}</div>
        <div class="text-sm">{{ t('tty.emptyHint') }}</div>
        <n-button type="primary" class="mt-2" @click="createTab">{{ t('tty.add') }}</n-button>
      </div>
      <div v-else class="relative h-full min-h-0 overflow-hidden">
        <TtyTerminal
          v-for="tab in tabs"
          :key="tab.id"
          v-show="tab.id === activeTabId"
          class="h-full"
          :workspace-id="workspaceId!"
          :tab-id="tab.id"
          :active="tab.id === activeTabId"
          @status="updateStatus(tab.id, $event)"
          @ready="handleReady(tab.id, $event)"
          @exit="handleExit(tab.id, $event)"
          @error="handleError(tab.id, $event)"
        />
      </div>
    </main>
  </div>
</template>
