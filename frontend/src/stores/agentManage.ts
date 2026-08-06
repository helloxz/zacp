import { defineStore } from 'pinia'
import { computed, reactive, ref } from 'vue'
import { fetchManageAgents, setAgentEnabled } from '@/api'
import type { ManageAgent } from '@/types/models'

/**
 * 设置页「智能体」目录 store。
 * 独立于 agentStore（运行时可用 agent 列表），数据源为 GET /api/v1/agents/manage
 * （配置 [[agents]] + 内置目录合并，含已停用与未安装项）。
 */
export const useAgentManageStore = defineStore('agentManage', () => {
  const list = ref<ManageAgent[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  /** 正在切换开关的 agentId 集合（防止重复点击）。
   *  用 reactive 而非 ref：Set 的 add/delete 需要触发 Vue 渲染（n-switch loading 态）。 */
  const toggling = reactive(new Set<string>())

  /** 已启用的智能体数量（设置页展示用） */
  const enabledCount = computed(() => list.value.filter((a) => a.enabled).length)

  /** 拉取目录列表；已加载中则跳过（幂等） */
  async function load() {
    if (loading.value) {
      return
    }
    loading.value = true
    error.value = null
    try {
      list.value = await fetchManageAgents()
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    } finally {
      loading.value = false
    }
  }

  /**
   * 开启/关闭智能体：调用后端（写 config.toml + 热更新）。
   * 失败时抛出异常由调用方提示，列表状态不本地回滚（下次 load 以服务端为准）。
   */
  async function toggle(agent: ManageAgent, enabled: boolean) {
    if (toggling.has(agent.agentId)) {
      return
    }
    toggling.add(agent.agentId)
    try {
      await setAgentEnabled(agent.agentId, enabled)
      agent.enabled = enabled
      return true
    } finally {
      toggling.delete(agent.agentId)
    }
  }

  /** 单个条目是否正在切换（供 n-switch loading） */
  function isToggling(agentId: string) {
    return toggling.has(agentId)
  }

  return { list, loading, error, enabledCount, load, toggle, isToggling }
})
