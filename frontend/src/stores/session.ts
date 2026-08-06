import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import {
  createSession as apiCreateSession,
  createWorkspace as apiCreateWorkspace,
  deleteSession as apiDeleteSession,
  deleteDraftSession as apiDeleteDraftSession,
  renameSession as apiRenameSession,
  removeWorkspace as apiRemoveWorkspace,
  fetchConfigOptions,
  fetchMessages,
  fetchRecentSessions,
  fetchSlashCommands,
  fetchWorkspaces,
  fetchSession as apiFetchSession,
  setConfigOption as apiSetConfigOption,
} from '@/api'
import { acpSocket } from '@/composables/useAcpSocket'
import { playSuccessTone } from '@/utils/successTone'
import type { MessageBlock } from '@/composables/useMessageBlocks'
import type {
  AvailableCommand,
  ChatMessage,
  ChatSession,
  ConfigOption,
  Workspace,
} from '@/types/models'
import type {
  PermissionOption,
  PermissionToolCall,
  Plan,
  WsEvent,
  WsServerMessage,
} from '@/types/ws'

/** localStorage 键：用户手动重命名的会话 id 集合 */
const MANUALLY_RENAMED_KEY = 'zacp.manuallyRenamedSessions'

/** 实时工具调用卡片（流式 turn 中显示，turn.done 后随历史 events 持久化渲染） */
export interface ToolCard {
  toolId: string
  title?: string
  status?: string
  /** 工具调用入参（后端透传 RawInput，可能很大，展示时截断/滚动） */
  input?: unknown
  /** 工具调用出参（后端透传 RawOutput，可能很大，展示时截断/滚动） */
  output?: unknown
}

/** 待处理的权限请求（permission.request 后、用户选择前） */
export interface PendingPermission {
  permissionId: string
  toolCall: PermissionToolCall | null
  options: PermissionOption[]
}

/** 会话 turn 状态：idle=可发送 / queued=已发送排队中（可取消）/ streaming=流式进行中 */
export type SessionStreamStatus = 'idle' | 'queued' | 'streaming'

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

  /**
   * 用户手动重命名过的会话 id 集合（localStorage 持久化，刷新后仍生效）。
   * 这些会话不再接受 agent 推送的 AI 总结标题覆盖（见 sessionInfo 分支），
   * 保证用户改的名字不会被后续 session_info_update 冲掉。
   */
  const manuallyRenamedIds = ref<Set<number>>(new Set(loadManuallyRenamedIds()))

  const loading = ref(false)
  const loadingError = ref<string | null>(null)

  // ---------------------------------------------------------------------------
  // 流式状态（方案 B「展示并发、执行串行」）：所有 turn 状态按会话隔离。
  //
  // 后端允许：同 agent 串行排队（后续 prompt 进入 FIFO）、跨 agent 天然并行。
  // 因此同一时刻可能有「A streaming、B queued」或「A/B 都 streaming」，
  // 全局单值无法表达——一律以 DB session id 为 key 的 Map 存储，
  // 组件经 statusOf / streamBlocksOf 等取当前会话的值。
  // ---------------------------------------------------------------------------

  /** 会话 turn 状态：idle=可发送 / queued=已发送排队中（可取消）/ streaming=流式进行中 */
  type SessionStreamStatus = 'idle' | 'queued' | 'streaming'

  /** 各会话 turn 状态（key: DB session id；缺失视为 idle） */
  const statusBySession = ref<Record<number, SessionStreamStatus>>({})
  /**
   * 各会话「任务进行中」集合（侧栏呼吸圆点数据源）。
   * 与状态机同步：queued/streaming 都算进行中；turn.done/error/取消后移除。
   */
  const runningSessionIds = ref<Set<number>>(new Set())
  /** 流式发送错误（ChatPane 错误条展示；按会话覆盖，展示时取当前会话） */
  const streamError = ref<string | null>(null)
  /** 待处理的权限请求（非空时 PermissionModal 显示） */
  const pendingPermission = ref<PendingPermission | null>(null)
  /** 各会话当前 turn 的实时工具调用卡片（流式期间展示；turn.done 清空，历史由消息 events 渲染） */
  const activeToolCardsBySession = ref<Record<number, ToolCard[]>>({})
  /** 各会话当前 turn 的实时执行计划（plan 事件整体替换；turn.done 清空，历史由消息 events 渲染） */
  const activePlanBySession = ref<Record<number, Plan | null>>({})
  /**
   * 各会话当前 turn 的消息块时间线（text/tool/plan 按事件到达顺序交错排列）。
   * 流式期间由 appendStreamChunk / upsertToolCard 增量构建；
   * turn.done 后清空，消息切换到历史路径（deriveBlocks 从 events 重建）。
   */
  const streamBlocksBySession = ref<Record<number, MessageBlock[]>>({})
  /** 各会话当前流式 assistant 占位消息 id（-1 表示无占位） */
  const streamMsgIdBySession = ref<Record<number, number>>({})
  /**
   * 当前会话的配置项（模型/思考强度/mode 等，来自 GET config-options）。
   * agent 不支持时为空数组 → 前端隐藏配置 UI（用户约定「ACP 不支持才隐藏」）。
   */
  const configOptions = ref<ConfigOption[]>([])

  /**
   * 当前会话的可用 / 命令（来自 GET slash-commands 与 WS slashCommands 广播）。
   * agent 未通告时为空数组 → 前端不显示候选面板（不做本地兜底）。
   */
  const slashCommands = ref<AvailableCommand[]>([])

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

  // ---------------------------------------------------------------------------
  // ACP session id → DB session id 反向索引 + 「发送过 prompt 的会话」快照。
  //
  // 后端广播（event/turn.done/权限等）携带的是 ACP session id（UUID 字符串），
  // 而本 store 的状态键是 DB session id（number）。WS 事件到达时先反查索引，
  // 查不到（刷新/重连后的历史事件）则丢弃——不做全局回退，避免串台。
  // ---------------------------------------------------------------------------

  /** ACP session id → DB session id（sessions 加载/创建/发送刷新时同步） */
  const dbIdByAcpSession = new Map<string, number>()
  /** 发送过 prompt 的会话快照（cancel 帧需要 acpSessionId；草稿不在 sessions 列表，只能查这里） */
  const sentSessions = new Map<number, ChatSession>()

  function indexAcpSession(session: ChatSession | null | undefined) {
    if (!session?.acpSessionId) return
    dbIdByAcpSession.set(session.acpSessionId, session.id)
  }

  function dropSessionIndexes(sessionId: number) {
    for (const [acpId, dbId] of dbIdByAcpSession) {
      if (dbId === sessionId) dbIdByAcpSession.delete(acpId)
    }
    sentSessions.delete(sessionId)
    delete statusBySession.value[sessionId]
    runningSessionIds.value.delete(sessionId)
    delete streamBlocksBySession.value[sessionId]
    delete activeToolCardsBySession.value[sessionId]
    delete activePlanBySession.value[sessionId]
    delete streamMsgIdBySession.value[sessionId]
  }

  /** 取会话 turn 状态（缺失视为 idle） */
  function statusOf(sessionId: number | null | undefined): SessionStreamStatus {
    if (sessionId === null || sessionId === undefined) return 'idle'
    return statusBySession.value[sessionId] ?? 'idle'
  }

  /** 取会话的实时消息块时间线（空数组兜底） */
  function streamBlocksOf(sessionId: number | null | undefined): MessageBlock[] {
    if (sessionId === null || sessionId === undefined) return []
    return streamBlocksBySession.value[sessionId] ?? []
  }

  /** 取会话的实时工具卡片 */
  function activeToolCardsOf(sessionId: number | null | undefined): ToolCard[] {
    if (sessionId === null || sessionId === undefined) return []
    return activeToolCardsBySession.value[sessionId] ?? []
  }

  /** 取会话的实时执行计划 */
  function activePlanOf(sessionId: number | null | undefined): Plan | null {
    if (sessionId === null || sessionId === undefined) return null
    return activePlanBySession.value[sessionId] ?? null
  }

  /** 消息是否处于「流式占位」态（MessageItem 渲染流式内容/打字指示器用） */
  function isStreamingMessage(message: ChatMessage): boolean {
    return (
      message.id === (streamMsgIdBySession.value[message.sessionId] ?? -1) &&
      statusOf(message.sessionId) === 'streaming'
    )
  }

  /** 当前会话 turn 状态（驱动 Composer：idle=发送按钮 / 非 idle=停止按钮+状态文案） */
  const currentStatus = computed<SessionStreamStatus>(() => statusOf(currentId.value))

  /** 兼容导出：当前会话是否正在流式输出 */
  const streaming = computed<boolean>(() => currentStatus.value === 'streaming')

  /** 默认工作区：isDefault 优先，否则最近使用（侧栏分组兜底） */
  function defaultWorkspace(): Workspace | undefined {
    return workspaces.value.find((w) => w.isDefault) ?? workspaces.value[0]
  }

  /**
   * 侧栏展示的「第一个项目」：与 SidebarSessionList 分组顺序保持一致。
   * 分组顺序 = sessions 按 updatedAt 倒序遍历，首个能解析出有效 workspace
   * 的会话所属项目（最新会话所在项目排最前）；全部项目都无会话时回退
   * workspaces 列表首个（最近使用）。
   *
   * 首页守卫跳转必须用它而不是 workspaces[0]：workspace.last_used 只在
   * 创建/恢复项目时更新（聊天不 touch），workspaces[0] 是「最近添加的项目」，
   * 与侧栏第一个分组（最新活跃项目）可能不一致，会导致守卫跳到侧栏后面的项目。
   */
  function firstWorkspace(): Workspace | undefined {
    for (const s of sessions.value) {
      // 与侧栏分组同一套 workspace 解析：软删除 workspace 的会话
      // （workspace 为空对象/id=0）跳过，不参与「第一个项目」判定
      const ws = s.workspace?.id
        ? s.workspace
        : workspaces.value.find((w) => w.id === s.workspaceId)
      if (ws) {
        return ws
      }
    }
    return workspaces.value[0]
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
    // 不传 limit：用 API 默认值 1000（后端上限 1000），避免会话多时被截断；
    // 渲染截断在侧栏组件层（每项目 30/60 条）。
    const list = await fetchRecentSessions()
    // 保护本地草稿：后端列表按约定过滤 is_draft=true，但草稿可能正被
    // NewSessionPane 使用（loadInitial 与草稿创建/转正存在竞态窗口）。
    // 仅当后端列表没有该 id 时才补回，避免与转正后的正式记录重复。
    const drafts = sessions.value.filter((s) => s.isDraft)
    sessions.value = [
      ...list,
      ...drafts.filter((d) => !list.some((s) => s.id === d.id)),
    ]
    // 重建 ACP session 反向索引（WS 事件按 ACP id 路由到 DB id）
    dbIdByAcpSession.clear()
    for (const s of sessions.value) {
      indexAcpSession(s)
    }
  }

  /**
   * 首屏初始化：工作区 + 会话并行拉取。
   * 幂等：进行中的加载复用同一 promise（路由守卫与 AppShell 可能并发触发），
   * 完成后保留 resolved promise，后续调用直接使用内存中最新数据。
   */
  let initialPromise: Promise<void> | null = null
  function loadInitial(): Promise<void> {
    if (!initialPromise) {
      loading.value = true
      loadingError.value = null
      const pending = Promise.all([loadWorkspaces(), loadSessions()])
        .then(() => {})
        .catch((e) => {
          loadingError.value = e instanceof Error ? e.message : String(e)
        })
        .finally(() => {
          loading.value = false
        })
      initialPromise = pending
    }
    // 首次调用必经过上面的赋值分支；TS 不对跨闭包引用的 let 收窄，此处显式断言非空
    return initialPromise as Promise<void>
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
    indexAcpSession(session)
    messagesById.value[session.id] = []
    return { session, configOptions }
  }

  /** 从 localStorage 读取手动改名会话 id（数据损坏/不可用时返回空集） */
  function loadManuallyRenamedIds(): number[] {
    try {
      const raw = localStorage.getItem(MANUALLY_RENAMED_KEY)
      return raw ? (JSON.parse(raw) as number[]) : []
    } catch {
      return []
    }
  }

  /** 持久化手动改名会话 id 集合（localStorage 不可用时仅内存生效） */
  function persistManuallyRenamed() {
    try {
      localStorage.setItem(MANUALLY_RENAMED_KEY, JSON.stringify([...manuallyRenamedIds.value]))
    } catch {
      // 忽略：降级为仅内存记录
    }
  }

  /** 删除会话（当前会话被删时清空选中） */
  async function removeSession(sessionId: number) {
    await apiDeleteSession(sessionId)
    sessions.value = sessions.value.filter((s) => s.id !== sessionId)
    delete messagesById.value[sessionId]
    // 清理 ACP 反向索引与流式状态槽位（会话已删除，迟到广播直接丢弃）
    dropSessionIndexes(sessionId)
    // 顺手清理手动改名标记，避免 localStorage 无限累积死 id（会话已物理删除）
    manuallyRenamedIds.value.delete(sessionId)
    persistManuallyRenamed()
    if (currentId.value === sessionId) {
      currentId.value = null
    }
  }

  /**
   * 重命名会话标题（PATCH /sessions/:id）。
   * 成功后更新本地列表 title，并把该会话记入手动改名集合——
   * 此后 agent 推送的 AI 总结标题不再覆盖（见 sessionInfo 分支）。
   */
  async function renameSession(sessionId: number, title: string) {
    await apiRenameSession(sessionId, title)
    const s = sessions.value.find((x) => x.id === sessionId)
    if (s) s.title = title
    manuallyRenamedIds.value.add(sessionId)
    persistManuallyRenamed()
  }

  /**
   * 删除草稿会话（切 tab / 离开空态时释放旧隐式草稿）。
   * 调后端 DELETE /sessions/:id/draft（关闭 ACP session + 删 DB 记录，不停 agent）。
   */
  async function removeDraftSession(sessionId: number) {
    await apiDeleteDraftSession(sessionId)
    delete messagesById.value[sessionId]
    dropSessionIndexes(sessionId)
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
    indexAcpSession(full)
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

  let wsRegistered = false

  /**
   * 追加流式文本到占位消息（热路径约束：改最后一条，不重建列表）。
   * 同时维护 streamBlocks：追加到末尾 text block 或创建新的 text block，
   * 保持文本与工具调用的时间线交错顺序。会话隔离：只动指定会话的槽位。
   */
  function appendStreamChunk(sessionId: number, text: string) {
    if (streamMsgIdBySession.value[sessionId] === undefined) {
      return
    }
    const list = messagesById.value[sessionId]
    const last = list?.[list.length - 1]
    if (last && last.id === streamMsgIdBySession.value[sessionId]) {
      last.content += text
    }
    // 维护 streamBlocks：追加到末尾 text block 或创建新 text block
    const blocks = streamBlocksBySession.value[sessionId] ?? (streamBlocksBySession.value[sessionId] = [])
    const lastBlock = blocks[blocks.length - 1]
    if (lastBlock?.kind === 'text') {
      lastBlock.content += text
    } else {
      blocks.push({ kind: 'text', content: text })
    }
  }

  /** 追加思维/推理流式文本到占位消息的 reasoning 字段（与正文分离展示） */
  function appendThoughtChunk(sessionId: number, text: string) {
    const msgId = streamMsgIdBySession.value[sessionId]
    if (msgId === undefined) {
      return
    }
    const list = messagesById.value[sessionId]
    const last = list?.[list.length - 1]
    if (last && last.id === msgId) {
      last.reasoning = (last.reasoning ?? '') + text
    }
  }

  /**
   * 工具调用事件 → 实时卡片 upsert + streamBlocks 维护。
   * 同 toolId 更新状态（原地修改 card 属性保持引用稳定）；
   * 首次出现时追加 tool block 到 streamBlocks（与文本交错）。
   * 会话隔离：只动指定会话的卡片列表。
   */
  function upsertToolCard(sessionId: number, event: WsEvent) {
    const toolId = event.toolId
    if (!toolId) {
      return
    }
    const cards = activeToolCardsBySession.value[sessionId] ?? (activeToolCardsBySession.value[sessionId] = [])
    const blocks = streamBlocksBySession.value[sessionId] ?? (streamBlocksBySession.value[sessionId] = [])
    const existing = cards.find((c) => c.toolId === toolId)
    if (existing) {
      // title/status 用 truthy 判断：后端把 nil 归一为空串，空串不应覆盖已有标题
      if (event.title) existing.title = event.title
      if (event.status) existing.status = event.status
      // 入参/出参用 != null 判断（null 与 undefined 都视为未携带）：
      // update 事件通常不携带 input/output，避免把 tool_call 阶段的入参清掉
      if (event.input != null) existing.input = event.input
      if (event.output != null) existing.output = event.output
      // 同步更新 streamBlocks 中对应 tool block 的 card 引用（原地修改）
      for (const b of blocks) {
        if (b.kind === 'tool' && b.card.toolId === toolId) {
          if (event.title) b.card.title = event.title
          if (event.status) b.card.status = event.status
          if (event.input != null) b.card.input = event.input
          if (event.output != null) b.card.output = event.output
          break
        }
      }
    } else {
      const card: ToolCard = {
        toolId,
        title: event.title,
        status: event.status ?? 'running',
        input: event.input,
        output: event.output,
      }
      cards.push(card)
      // 首次出现：追加 tool block 到 streamBlocks（保持时间线交错）
      blocks.push({ kind: 'tool', card })
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

  /** 加载当前会话可用 / 命令（进入会话时调用；agent 未通告时为空数组） */
  async function loadSlashCommands(sessionId: number) {
    try {
      slashCommands.value = await fetchSlashCommands(sessionId)
    } catch {
      // 获取失败不阻塞聊天（视为无 / 命令，不显示候选面板）
      slashCommands.value = []
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

  /** turn 收尾：复位指定会话的流式状态（幂等；排队取消/正常结束/出错共用） */
  function endStreamTurn(sessionId: number) {
    statusBySession.value[sessionId] = 'idle'
    delete streamMsgIdBySession.value[sessionId]
    streamBlocksBySession.value[sessionId] = []
    activeToolCardsBySession.value[sessionId] = []
    activePlanBySession.value[sessionId] = null
    runningSessionIds.value.delete(sessionId)
  }

  /** turn.done 收尾：结束流式状态，随后强制刷新为 DB 版本（覆盖本地临时消息） */
  async function finalizeStream(sessionId: number) {
    endStreamTurn(sessionId)
    void refreshAfterTurn(sessionId)
  }

  /** 流式结束后的数据对齐：强制拉 DB 版本（服务端已落库 user + assistant）+ 刷新配置项 */
  async function refreshAfterTurn(sessionId: number) {
    try {
      await loadMessages(sessionId, true)
      // agent 可能在 turn 中经 update 通知更新配置项，刷新以同步最新 currentValue
      await loadConfigOptions(sessionId)
      // 同样刷新 / 命令：agent 可能在 turn 中通告新命令（如 init 后出现新命令）
      await loadSlashCommands(sessionId)
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
      // 广播按 ACP session id 路由到 DB 会话：
      // - 属于已索引会话的事件 → 更新该会话的流式槽位（A 在跑时切到 B，A 的事件仍归 A）
      // - 未知会话（刷新/重连后迟到的历史广播）→ 丢弃，不做全局回退，避免串台
      // - sessionInfo/configOptions/slashCommands 等「当前会话视图」状态：仅当前会话的广播生效
      // - pong/session.ready 不带 sessionId（'sessionId' in msg 为 false），回退当前会话（无 case 消费，无害）
      const sid = 'sessionId' in msg
        ? (msg.sessionId ? (dbIdByAcpSession.get(msg.sessionId) ?? null) : currentId.value)
        : currentId.value
      switch (msg.type) {
        case 'event': {
          const e = msg.event
          if (!e || sid === null) {
            break
          }
          // 排队 → 流式兜底：正常由 turn.started 切换；此处兜底旧后端或
          // 广播丢失场景——收到本会话事件说明 agent 已开始处理
          if (statusOf(sid) === 'queued') {
            statusBySession.value[sid] = 'streaming'
          }
          if (e.type === 'agent_message' && e.text) {
            // 流式文本事件：追加到占位消息
            appendStreamChunk(sid, e.text)
          } else if (e.type === 'agent_thought' && e.text) {
            // 思维/推理流式事件：追加到占位消息的 reasoning（折叠展示）
            appendThoughtChunk(sid, e.text)
          } else if (e.type === 'tool_call' || e.type === 'tool_call_update') {
            // 工具调用：实时卡片（title/status 随 update 演进）
            upsertToolCard(sid, e)
          } else if (e.type === 'plan' && e.plan) {
            // 执行计划：整体替换语义，直接覆盖实时卡片（不按 toolId 合并）
            activePlanBySession.value[sid] = e.plan
          }
          break
        }
        case 'configOptions': {
          // agent 推送的配置项更新（如切换模型后下发思维强度等新选项）：
          // 仅当前会话的广播生效，避免 A 的更新串到 B 的视图
          if (msg.configOptions && sid !== null && sid === currentId.value) {
            configOptions.value = msg.configOptions
          }
          break
        }
        case 'slashCommands': {
          // agent 推送的可用 / 命令更新（available_commands_update）：
          // 仅当前会话的广播生效（同上）
          if (msg.slashCommands && sid !== null && sid === currentId.value) {
            slashCommands.value = msg.slashCommands
          }
          break
        }
        case 'sessionInfo': {
          // agent 推送的会话信息更新（session_info_update）：AI 总结标题优先于
          // zacp 本地的首条消息截取标题，实时刷新侧栏与信息面板（activeSession
          // 由 sessions 派生，更新列表项即可，无需额外状态）。
          // 例外：用户手动重命名过的会话（manuallyRenamedIds 含该 id）不被 AI
          // 标题覆盖，尊重用户命名。仅当前会话的广播生效（sid 过滤）。
          const title = msg.sessionInfo?.title
          if (
            title &&
            sid !== null &&
            sid === currentId.value &&
            !manuallyRenamedIds.value.has(sid)
          ) {
            const s = sessions.value.find((x) => x.id === sid)
            if (s) {
              s.title = title
            }
          }
          break
        }
        case 'turn.started': {
          // 后端排队门闩获取成功、agent 已开始处理本会话 prompt：
          // queued → streaming。立即执行的会话几乎瞬间收到（排队中一闪而过），
          // 真正排队的会话在轮到自己时才收到（排队中文案持续到此刻）。
          if (sid !== null && statusOf(sid) === 'queued') {
            statusBySession.value[sid] = 'streaming'
          }
          break
        }
        case 'turn.done': {
          // 收尾目标：优先按广播 sessionId 路由；解析失败（如执行中 agent 重启、
          // recoverSession 换了新 ACP id，索引尚未更新）时回退「唯一运行中会话」——
          // 多会话并行时无法区分则丢弃，避免误复位
          const target =
            sid ?? (runningSessionIds.value.size === 1 ? [...runningSessionIds.value][0] : null)
          if (target === null) {
            break
          }
          // 回复完成提示音：仅当本轮确在流式（过滤排队取消、历史迟到 turn.done 等场景）
          if (statusOf(target) === 'streaming') {
            playSuccessTone()
          }
          void finalizeStream(target)
          break
        }
        case 'permission.request': {
          // 权限请求：弹出 Modal 等待用户选择。
          // 若本会话仍在排队（后端不可能发出，属防御）→ 视为已开始执行
          if (sid !== null && statusOf(sid) === 'queued') {
            statusBySession.value[sid] = 'streaming'
          }
          pendingPermission.value = {
            permissionId: msg.permissionId ?? '',
            toolCall: msg.toolCall ?? null,
            options: msg.options ?? [],
          }
          break
        }
        case 'error': {
          // 出错同样结束本轮：清除侧栏「任务进行中」圆点 + 复位状态。
          // sid 解析失败时同 turn.done 回退唯一运行中会话（recover 换 id 的自愈链）
          const target =
            sid ?? (runningSessionIds.value.size === 1 ? [...runningSessionIds.value][0] : null)
          if (target !== null) {
            endStreamTurn(target)
          }
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
    const placeholderId = -(Date.now() + 1)
    streamMsgIdBySession.value[sessionId] = placeholderId
    const placeholder: ChatMessage = {
      id: placeholderId,
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

    // 状态机：发送后先置 queued（「排队中」+ 停止按钮），后端排队门闩获取成功
    // 即广播 turn.started（立即执行的会话毫秒级收到，排队中一闪而过；真正排队的
    // 会话保持到轮到自己）→ 转 streaming。event/permission.request 兜底切换
    // （旧后端/广播丢失场景），保证状态机不会卡在 queued。
    statusBySession.value[sessionId] = 'queued'
    streamError.value = null
    streamBlocksBySession.value[sessionId] = []
    activeToolCardsBySession.value[sessionId] = []
    activePlanBySession.value[sessionId] = null
    // 快照发送用会话：cancel 帧需要 acpSessionId（草稿不在 sessions 列表）
    sentSessions.set(sessionId, session)
    indexAcpSession(session)

    const sent = acpSocket.send({
      type: 'prompt',
      sessionId: session.acpSessionId,
      agentId: session.agentId,
      message: content,
    })
    if (!sent) {
      // 连接未就绪：提示并回退？P2 简化：置错并结束流式
      endStreamTurn(sessionId)
      streamError.value = 'websocket not connected'
    } else {
      // 发送成功即视为「任务进行中」，点亮侧栏圆点
      runningSessionIds.value.add(sessionId)
    }
  }

  /**
   * 取消回复（发送 cancel 帧）。
   * 按会话语义：排队中的 prompt 被后端撤销排队（广播 turn.done(cancelled)），
   * 正在执行的 prompt 被 ACP cancel 中断——两者都不影响其它会话的 turn。
   * 本地立即复位（幂等；后端 turn.done 到达时再走一遍收尾）。
   */
  function cancelSend(sessionId?: number) {
    const sid = sessionId ?? currentId.value
    if (sid === null) {
      return
    }
    // 优先用发送时快照（草稿不在 sessions 列表），其次当前列表
    const session = sentSessions.get(sid) ?? sessions.value.find((s) => s.id === sid)
    if (session?.acpSessionId) {
      acpSocket.send({
        type: 'cancel',
        sessionId: session.acpSessionId,
        agentId: session.agentId,
      })
    }
    endStreamTurn(sid)
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
    // 流式状态（方案 B）：按会话隔离的存取函数 + 当前会话便捷视图
    streaming,
    currentStatus,
    statusOf,
    streamBlocksOf,
    activeToolCardsOf,
    activePlanOf,
    isStreamingMessage,
    runningSessionIds,
    streamError,
    pendingPermission,
    configOptions,
    slashCommands,
    activeSession,
    activeMessages,
    defaultWorkspace,
    firstWorkspace,
    loadInitial,
    loadWorkspaces,
    createWorkspace,
    removeWorkspace,
    loadMessages,
    loadConfigOptions,
    loadSlashCommands,
    setConfigOption,
    createSession,
    removeSession,
    renameSession,
    removeDraftSession,
    promoteDraftSession,
    sendViaWs,
    cancelSend,
    clearStreamError,
    resolvePermission,
  }
})
