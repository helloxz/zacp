import { computed, ref } from 'vue'
import type { TtyTabStatus } from '@/types/tty'

export interface TtyTabState {
  id: string
  title: string
  number: number
  status: TtyTabStatus
  terminalId?: string
  exitCode?: number
  error?: string
}

const MAX_TABS = 6

/**
 * 页面级 TTY Tab 管理器。
 * 终端是临时资源，不写入 Pinia 或 localStorage；页面卸载时由各 Terminal 组件关闭 socket。
 */
export function useTtyManager() {
  const tabs = ref<TtyTabState[]>([])
  const activeTabId = ref<string | null>(null)
  let nextNumber = 1

  const activeTab = computed(
    () => tabs.value.find((tab) => tab.id === activeTabId.value) ?? null,
  )
  const canCreate = computed(() => tabs.value.length < MAX_TABS)

  function createTab(): TtyTabState | null {
    if (!canCreate.value) return null
    const number = nextNumber++
    const tab: TtyTabState = {
      id: `tty-${number}-${Date.now()}`,
      title: `终端 ${number}`,
      number,
      status: 'creating',
    }
    tabs.value.push(tab)
    activeTabId.value = tab.id
    return tab
  }

  function selectTab(id: string) {
    if (tabs.value.some((tab) => tab.id === id)) activeTabId.value = id
  }

  function updateTab(id: string, patch: Partial<Omit<TtyTabState, 'id'>>) {
    const tab = tabs.value.find((item) => item.id === id)
    if (tab) Object.assign(tab, patch)
  }

  function removeTab(id: string) {
    const index = tabs.value.findIndex((tab) => tab.id === id)
    if (index < 0) return
    const wasActive = activeTabId.value === id
    tabs.value.splice(index, 1)
    if (!wasActive) return
    const next = tabs.value[index] ?? tabs.value[index - 1] ?? null
    activeTabId.value = next?.id ?? null
  }

  function closeAll() {
    tabs.value.splice(0, tabs.value.length)
    activeTabId.value = null
  }

  return {
    tabs,
    activeTabId,
    activeTab,
    canCreate,
    createTab,
    selectTab,
    updateTab,
    removeTab,
    closeAll,
  }
}
