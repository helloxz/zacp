import { defineStore } from 'pinia'
import { ref } from 'vue'
import { fetchAgents } from '@/api'
import type { Agent } from '@/types/models'

export const useAgentStore = defineStore('agent', () => {
  /** Agent 列表（来自 GET /api/v1/agents） */
  const list = ref<Agent[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  /** 拉取 agent 列表；已在加载中则跳过（幂等） */
  async function load() {
    if (loading.value) {
      return
    }
    loading.value = true
    error.value = null
    try {
      list.value = await fetchAgents()
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    } finally {
      loading.value = false
    }
  }

  return { list, loading, error, load }
})
