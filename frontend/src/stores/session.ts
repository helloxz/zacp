import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import {
  createSession as apiCreateSession,
  createWorkspace as apiCreateWorkspace,
  deleteSession as apiDeleteSession,
  deleteDraftSession as apiDeleteDraftSession,
  removeWorkspace as apiRemoveWorkspace,
  fetchConfigOptions,
  fetchMessages,
  fetchRecentSessions,
  fetchWorkspaces,
  fetchSession as apiFetchSession,
  setConfigOption as apiSetConfigOption,
} from '@/api'
import { acpSocket } from '@/composables/useAcpSocket'
import type {
  ChatMessage,
  ChatSession,
  ConfigOption,
  Workspace,
} from '@/types/models'
import type {
  PermissionOption,
  PermissionToolCall,
  WsEvent,
  WsServerMessage,
} from '@/types/ws'

/** 实时工具调用卡片（流式 turn 中显示，turn.done 后随历史 events 持久化渲染） */
export interface ToolCard {
  toolId: string
  title?: string
  status?: string
}

/** 待处理的权限请求（permission.request 后、用户选择前） */
export interface PendingPermission {
  permissionId: string
  toolCall: PermissionToolCall | null
  options: PermissionOption[]
}

/**
 * 会话工作台状态（P2：REST 数据 + WebSocket 流式发送）。
 * 组件只依赖本 store 暴露的 ref / 方法，不感知传输细节。
 */
export const useSessionStore = defineStore('session', () => {
  /** 工作区列表（Composer 下拉 / 侧栏分组兜底） */
  const workspaces = ref<Workspace[]>([])
  /** 侧栏会话列表（GET /api/v1/sessions，后端按 updatedAt 倒序） */
  const sessions = ref<ChatSession[]>([])
  /** 各会话消息缓存（进入会话时按需加载） */
  const messagesById = ref<Record<number, ChatMessage[]>>({})

  /** 当前选中会话 id（进入 /sessions/:id 时由 ChatPane 同步） */
  const currentId = ref<number | null>(null)

  const loading = ref(false)
  const loadingError = ref<string | null>(null)
  /** 是否正在等待 Agent 回复（驱动 Composer 停止按钮 / 消息流式态） */
  const streaming = ref(false)
  /** 流式发送错误（ChatPane 错误条展示） */
  const streamError = ref<string | null>(null)
  /** 待处理的权限请求（非空时 PermissionModal 显示） */
  const pendingPermission = ref<PendingPermission | null>(null)
  /** 当前 turn 的实时工具调用卡片（流式期间展示；turn.done 清空，历史由消息 events 渲染） */
  const activeToolCards = ref<ToolCard[]>([])
  /**
   * 当前会话的配置项（模型/思考强度/mode 等，来自 GET config-options）。
   * agent 不支持时为空数组 → 前端隐藏配置 UI（用户约定「ACP 不支持才隐藏」）。
   */
  const configOptions = ref<ConfigOption[]>([])

  /** 当前会话对象；null 对应空态 */
  const activeSession = computed<ChatSession | null>(() => {
    if (currentId.value === null) {
      return null
    }
    return sessions.value.find((s) => s.id === currentId.value) ?? null
  })

  /** 当前会话的消息列表（流式追加的载体） */
  const activeMessages = computed<ChatMessage[]>(() =>
    currentId.value === null ? [] : (messagesById.value[currentId.value] ?? []),
  )

  /** 默认工作区：isDefault 优先，否则最近使用（侧栏分组兜底） */
  function defaultWorkspace(): Workspace | undefined {
    return workspaces.value.find((w) => w.isDefault) ?? workspaces.value[0]
  }

  async function loadWorkspaces() {
    workspaces.value = await fetchWorkspaces()
  }

  /**
   * 创建工作区（POST /api/v1/workspaces，后端校验路径存在），成功后刷新列表。
   * 解决「无工作区时下拉为空无法开启」的死循环：由 Composer 提供路径输入入口。
   */
  async function createWorkspace(path: string): Promise<Workspace> {
    const ws = await apiCreateWorkspace(path)
    await loadWorkspaces()
    return ws
  }

  /**
   * 移除项目（DELETE /api/v1/workspaces/:id，后端软删除）：
   * 项目从侧栏隐藏（其下会话与消息保留），同路径再次添加时整体恢复。
   */
  async function removeWorkspace(workspaceId: number) {
    await apiRemoveWorkspace(workspaceId)
    await Promise.all([loadWorkspaces(), loadSessions()])
  }

  async function loadSessions() {
    const list = await fetchRecentSessions(50)
    // 保护本地草稿：后端列表按约定过滤 is_draft=true，但草稿可能正被
    // NewSessionPane 使用（loadInitial 与草稿创建/转正存在竞态窗口）。
    // 仅当后端列表没有该 id 时才补回，避免与转正后的正式记录重复。
    const drafts = sessions.value.filter((s) => s.isDraft)
    sessions.value = [
      ...list,
      ...drafts.filter((d) => !list.some((s) => s.id === d.id)),
    ]
  }

  /** 首屏初始化：工作区 + 会话并行拉取 */
  async function loadInitial() {
    if (loading.value) {
      return
    }
    loading.value = true
    loadingError.value = null
    try {
      await Promise.all([loadWorkspaces(), loadSessions()])
    } catch (e) {
      loadingError.value = e instanceof Error ? e.message : String(e)
    } finally {
      loading.value = false
    }
  }

  /**
   * 加载某会话消息历史。
   * force=true 时无视缓存强制拉取（turn.done 后刷新为 DB 版本，替换本地临时消息）。
   */
  async function loadMessages(sessionId: number, force = false) {
    if (!force && messagesById.value[sessionId] !== undefined) {
      return
    }
    const page = await fetchMessages(sessionId, 100, 0)
    messagesById.value[sessionId] = page.messages
  }

  /**
   * 创建会话（POST /api/v1/sessions）。
   * workspaceId 缺省时后端回退默认工作区（config session.default_cwd）。
   *
   * isDraft=true 时创建隐式草稿会话（预览配置项，不进侧栏列表）；
   * 返回 { session, configOptions }，调用方用 configOptions 直接展示模型/思维程度下拉。
   * 草稿会话在发出首条 prompt 后由后端转正，下次 ListRecent 会包含它。
   */
  async function createSession(
    agentId: string,
    workspaceId?: number,
    isDraft = false,
  ): Promise<{ session: ChatSession; configOptions: ConfigOption[] }> {
    const result = await apiCreateSession({ agentId, workspaceId, isDraft })
    const { session, configOptions } = result
    // 草稿不进侧栏列表；非草稿（兼容旧路径）进列表
    if (!isDraft) {
      sessions.value = [
        session,
        ...sessions.value.filter((s) => s.id !== session.id),
      ]
    }
    messagesById.value[session.id] = []
    return { session, configOptions }
  }

  /** 删除会话（当前会话被删时清空选中） */
  async function removeSession(sessionId: number) {
    await apiDeleteSession(sessionId)
    sessions.value = sessions.value.filter((s) => s.id !== sessionId)
    delete messagesById.value[sessionId]
    if (currentId.value === sessionId) {
      currentId.value = null
    }
  }

  /**
   * 删除草稿会话（切 tab / 离开空态时释放旧隐式草稿）。
   * 调后端 DELETE /sessions/:id/draft（关闭 ACP session + 删 DB 记录，不停 agent）。
   */
  async function removeDraftSession(sessionId: number) {
    await apiDeleteDraftSession(sessionId)
    delete messagesById.value[sessionId]
  }

  /**
   * 草稿转正后接入侧栏列表（NewSessionPane 发首条消息成功后调用）。
   * 草稿创建时未进列表（见 createSession 的 isDraft 分支），转正后补进列表头，
   * 使跳转 /sessions/:id 后 activeSession 可解析、标题刷新与 touch 排序生效。
   */
  function promoteDraftSession(session: ChatSession) {
    // 补 workspace 关联：createSession 响应不带 workspace（后端未预加载），
    // 兜底从本地 workspaces 匹配，保证转正后侧栏立即按父项目分组显示，
    // 不依赖「AI 响应后 loadSessions 从 DB 拉回完整数据」才正确归属。
    // 注意：workspace 可能是空对象（id=0，软删除残留），须按 id 有效性判断。
    const ws =
      session.workspace?.id
        ? session.workspace
        : workspaces.value.find((w) => w.id === session.workspaceId)
    const full = ws ? { ...session, workspace: ws } : session
    sessions.value = [
      full,
      ...sessions.value.filter((s) => s.id !== session.id),
    ]
  }

  /** 本地乐观追加消息（用户消息无后端 id，用负时间戳占位） */
  function appendLocal(
    sessionId: number,
    role: ChatMessage['role'],
    content: string,
  ) {
    const msg: ChatMessage = {
      id: -Date.now(),
      sessionId,
      role,
      content,
      createdAt: new Date().toISOString(),
    }
    messagesById.value[sessionId] = [
      ...(messagesById.value[sessionId] ?? []),
      msg,
    ]
    touch(sessionId, msg.createdAt)
    return msg
  }

  /** 更新会话最近活跃时间并置顶（本会话移到列表头） */
  function touch(sessionId: number, updatedAt: string) {
    const s = sessions.value.find((it) => it.id === sessionId)
    if (!s) {
      return
    }
    s.updatedAt = updatedAt
    sessions.value = [s, ...sessions.value.filter((it) => it.id !== sessionId)]
  }

  // ---------------------------------------------------------------------------
  // WebSocket 流式发送（P2）
  // ---------------------------------------------------------------------------

  /** 当前流式 assistant 占位消息 id（-1 表示无占位） */
  let streamMsgId = -1
  let wsRegistered = false

  /** 追加流式文本到占位消息（热路径约束：改最后一条，不重建列表） */
  function appendStreamChunk(text: string) {
    const sessionId = currentId.value
    if (sessionId === null || streamMsgId === -1) {
      return
    }
    const list = messagesById.value[sessionId]
    const last = list?.[list.length - 1]
    if (last && last.id === streamMsgId) {
      last.content += text
    }
  }

  /** 追加思维/推理流式文本到占位消息的 reasoning 字段（与正文分离展示） */
  function appendThoughtChunk(text: string) {
    const sessionId = currentId.value
    if (sessionId === null || streamMsgId === -1) {
      return
    }
    const list = messagesById.value[sessionId]
    const last = list?.[list.length - 1]
    if (last && last.id === streamMsgId) {
      last.reasoning = (last.reasoning ?? '') + text
    }
  }

  /** 工具调用事件 → 实时卡片 upsert（同 toolId 更新状态，否则追加） */
  function upsertToolCard(event: WsEvent) {
    const toolId = event.toolId
    if (!toolId) {
      return
    }
    const existing = activeToolCards.value.find((c) => c.toolId === toolId)
    if (existing) {
      if (event.title !== undefined) existing.title = event.title
      if (event.status !== undefined) existing.status = event.status
    } else {
      activeToolCards.value = [
        ...activeToolCards.value,
        { toolId, title: event.title, status: event.status ?? 'running' },
      ]
    }
  }

  /** 用户选择权限选项：回传 permission 帧并关闭弹窗 */
  function resolvePermission(optionId: string) {
    const pending = pendingPermission.value
    if (!pending) {
      return
    }
    acpSocket.send({
      type: 'permission',
      permissionId: pending.permissionId,
      optionId,
    })
    pendingPermission.value = null
  }

  /** 加载当前会话配置项（进入会话时调用；agent 不支持时为空数组） */
  async function loadConfigOptions(sessionId: number) {
    try {
      configOptions.value = await fetchConfigOptions(sessionId)
    } catch {
      // 配置项获取失败不阻塞聊天（视为不支持，隐藏配置 UI）
      configOptions.value = []
    }
  }

  /** 设置会话配置项（select 型：切换模型/思考强度/mode），成功后更新本地 currentValue */
  async function setConfigOption(optionId: string, valueId: string) {
    const sessionId = currentId.value
    if (sessionId === null) {
      return
    }
    await apiSetConfigOption(sessionId, optionId, valueId)
    // 本地先回写 currentValue（下拉即时反馈）
    const opt = configOptions.value.find((o) => o.id === optionId)
    if (opt) {
      opt.currentValue = valueId
    }
    // 稍等 agent 处理完成后重新拉取完整配置项：
    // 模型切换可能改变可选配置（如切到 deepseek 官方模型后出现思维强度选项），
    // 即使 agent 不推送 config_option_update，主动刷新也能拿到新列表。
    setTimeout(() => {
      void loadConfigOptions(sessionId)
    }, 300)
  }

  /** turn.done 收尾：结束流式状态，随后强制刷新为 DB 版本（覆盖本地临时消息） */
  async function finalizeStream() {
    const sessionId = currentId.value
    streaming.value = false
    streamMsgId = -1
    if (sessionId === null) {
      return
    }
    void refreshAfterTurn(sessionId)
  }

  /** 流式结束后的数据对齐：强制拉 DB 版本（服务端已落库 user + assistant）+ 刷新配置项 */
  async function refreshAfterTurn(sessionId: number) {
    try {
      await loadMessages(sessionId, true)
      // agent 可能在 turn 中经 update 通知更新配置项，刷新以同步最新 currentValue
      await loadConfigOptions(sessionId)
      // 同步会话标题（服务端首条消息生成）与列表排序
      const fresh = await apiFetchSession(sessionId).catch(() => null)
      if (fresh) {
        const idx = sessions.value.findIndex((s) => s.id === sessionId)
        if (idx >= 0) {
          sessions.value[idx] = { ...sessions.value[idx], ...fresh }
          touch(sessionId, fresh.updatedAt)
        }
      }
    } catch {
      // 刷新失败不影响已展示内容（本地消息仍可见）
    }
  }

  /** 注册 WS 消息处理（store 首次实例化时执行一次） */
  function ensureWsListener() {
    if (wsRegistered) {
      return
    }
    wsRegistered = true
    acpSocket.onMessage((msg: WsServerMessage) => {
      switch (msg.type) {
        case 'event': {
          const e = msg.event
          if (!e) {
            break
          }
          if (e.type === 'agent_message' && e.text) {
            // 流式文本事件：追加到占位消息
            appendStreamChunk(e.text)
          } else if (e.type === 'agent_thought' && e.text) {
            // 思维/推理流式事件：追加到占位消息的 reasoning（折叠展示）
            appendThoughtChunk(e.text)
          } else if (e.type === 'tool_call' || e.type === 'tool_call_update') {
            // 工具调用：实时卡片（title/status 随 update 演进）
            upsertToolCard(e)
          }
          break
        }
        case 'configOptions': {
          // agent 推送的配置项更新（如切换模型后下发思维强度等新选项）：
          // 实时刷新下拉，无需重新进入会话
          if (msg.configOptions) {
            configOptions.value = msg.configOptions
          }
          break
        }
        case 'turn.done': {
          // 流式结束：实时工具卡片清空（历史由 assistant 消息 events 渲染）
          activeToolCards.value = []
          void finalizeStream()
          break
        }
        case 'permission.request': {
          // 权限请求：弹出 Modal 等待用户选择
          pendingPermission.value = {
            permissionId: msg.permissionId ?? '',
            toolCall: msg.toolCall ?? null,
            options: msg.options ?? [],
          }
          break
        }
        case 'error': {
          streaming.value = false
          streamMsgId = -1
          streamError.value = msg.message ?? msg.code ?? 'unknown error'
          break
        }
        default:
          break
      }
    })
  }

  /**
   * 发送消息（WS prompt）：乐观追加用户消息 + 空 assistant 占位 →
   * socket 发送 prompt（sessionId 为 ACP session id）→ 事件流式追加 → turn.done 收尾。
   *
   * sessionOverride：草稿会话（isDraft）创建时未进 sessions 列表，
   * 发送时由调用方（NewSessionPane）显式传入 session 对象，避免列表查找失败。
   */
  async function sendViaWs(
    sessionId: number,
    content: string,
    sessionOverride?: ChatSession,
  ) {
    let session =
      sessionOverride ?? sessions.value.find((s) => s.id === sessionId)
    if (!session) {
      throw new Error('session not found')
    }
    // 发送前刷新会话：服务端重启后 ACP session 可能已被后端重建（acpSessionId 变化），
    // 用 DB 最新值发送可避免「unknown session」报错（后端恢复逻辑见 ws/bridge.go）
    try {
      const fresh = await apiFetchSession(sessionId)
      if (sessionOverride) {
        // 草稿：不在 sessions 列表，直接合并最新字段（acpSessionId 等）
        session = { ...sessionOverride, ...fresh }
      } else {
        const idx = sessions.value.findIndex((s) => s.id === sessionId)
        if (idx >= 0) {
          sessions.value[idx] = { ...sessions.value[idx], ...fresh }
          session = sessions.value[idx]
        }
      }
    } catch {
      // 刷新失败：沿用本地缓存值
    }
    if (!session.acpSessionId) {
      throw new Error('session has no acp session id')
    }

    // 乐观展示用户消息 + 空占位（流式追加目标）
    appendLocal(sessionId, 'user', content)
    // 占位 id 必须与 user 消息不同（appendLocal 用 -Date.now()，同一毫秒会撞 key）
    streamMsgId = -(Date.now() + 1)
    const placeholder: ChatMessage = {
      id: streamMsgId,
      sessionId,
      role: 'assistant',
      content: '',
      reasoning: '',
      createdAt: new Date().toISOString(),
    }
    messagesById.value[sessionId] = [
      ...(messagesById.value[sessionId] ?? []),
      placeholder,
    ]
    touch(sessionId, placeholder.createdAt)

    streaming.value = true
    streamError.value = null

    const sent = acpSocket.send({
      type: 'prompt',
      sessionId: session.acpSessionId,
      agentId: session.agentId,
      message: content,
    })
    if (!sent) {
      // 连接未就绪：提示并回退？P2 简化：置错并结束流式
      streaming.value = false
      streamMsgId = -1
      streamError.value = 'websocket not connected'
    }
  }

  /** 取消当前回复（发送 cancel 帧；agent 停止后事件流随之结束） */
  function cancelSend() {
    const session = activeSession.value
    if (session?.acpSessionId) {
      acpSocket.send({
        type: 'cancel',
        sessionId: session.acpSessionId,
        agentId: session.agentId,
      })
    }
    streaming.value = false
    streamMsgId = -1
  }

  function clearStreamError() {
    streamError.value = null
  }

  // 首次实例化时注册 WS 消息订阅
  ensureWsListener()

  return {
    workspaces,
    sessions,
    messagesById,
    currentId,
    loading,
    loadingError,
    streaming,
    streamError,
    pendingPermission,
    activeToolCards,
    configOptions,
    activeSession,
    activeMessages,
    defaultWorkspace,
    loadInitial,
    loadWorkspaces,
    createWorkspace,
    removeWorkspace,
    loadMessages,
    loadConfigOptions,
    setConfigOption,
    createSession,
    removeSession,
    removeDraftSession,
    promoteDraftSession,
    sendViaWs,
    cancelSend,
    clearStreamError,
    resolvePermission,
  }
})
