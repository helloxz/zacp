/**
 * 消息块（blocks）派生逻辑：将 assistant 消息的事件时间线拆分为
 * text / tool / plan 三种块，按时间顺序排列，恢复 AI 文本与工具调用的因果关系。
 *
 * 两条渲染路径：
 *  - 流式：store.streamBlocks 随事件增量构建（store 维护，tool card 引用共享）
 *  - 历史：从 message.events JSON 按序重建（本模块提供 deriveBlocks）
 */
import type { WsEvent } from '@/types/ws'
import type { ToolDetailsMap } from '@/types/models'
import type { ToolCard } from '@/stores/session'

/** 消息渲染块（按时间线排列，取代原先「工具卡统一在上方」的固定布局） */
export type MessageBlock =
  | { kind: 'text'; content: string }
  | { kind: 'tool'; card: ToolCard }
  | { kind: 'plan' }

/**
 * 从历史消息的 events JSON 派生渲染块（按事件时间线交错排列）。
 *
 * 算法：顺序遍历 events，连续 agent_message 文本合并为一个 text block；
 * 遇到 tool_call 时先刷出累积文本，再插入 tool block；
 * 同一 toolId 的多次事件（tool_call + tool_call_update）合并为同一个 tool block（原地更新）。
 * plan 事件作为独立 plan block 插入。
 *
 * 工具卡详情来源（v6 拆分后）：
 *  1. 优先取 toolDetails 快照（toolId → 最终 {input, output}，每工具一份）；
 *  2. 缺失时回退 events 内嵌值——兼容「新前端 + 旧后端」等组合（旧后端 events 未瘦身）。
 *
 * 语义与流式期间 store.streamBlocks 的增量构建一致：
 * turn.done → loadMessages(force) 切换到历史路径时无视觉跳动。
 */
export function deriveBlocks(events: WsEvent[], toolDetails?: ToolDetailsMap): MessageBlock[] {
  const blocks: MessageBlock[] = []
  let textBuf = ''

  /** 把累积的文本刷为一个 text block（空串时跳过） */
  const flushText = () => {
    if (textBuf) {
      blocks.push({ kind: 'text', content: textBuf })
      textBuf = ''
    }
  }

  // toolId → blocks 中对应 tool block 的引用（用于 update 时原地更新 card 属性）
  const toolIndex = new Map<string, { kind: 'tool'; card: ToolCard }>()

  for (const e of events) {
    if (e.type === 'agent_message' || e.type === 'user_message') {
      if (e.text) textBuf += e.text
    } else if (e.type === 'agent_thought') {
      // 思维/推理：由 MessageItem 的 reasoning 区块单独渲染，不混入 blocks
    } else if (e.type === 'tool_call' || e.type === 'tool_call_update') {
      if (!e.toolId) continue
      // 历史详情快照优先；update 事件里若还带内嵌值（旧数据），以快照为准
      const detail = toolDetails?.[e.toolId]
      const existing = toolIndex.get(e.toolId)
      if (existing) {
        // 合并语义：update 未携带的字段保留前值（与流式 upsertToolCard 一致）
        if (e.title) existing.card.title = e.title
        if (e.status) existing.card.status = e.status
        if (detail) {
          // 快照缺字段时保留前值（防御：拆分语义变化时不会静默清空已有详情）
          existing.card.input = detail.input ?? existing.card.input
          existing.card.output = detail.output ?? existing.card.output
        } else {
          if (e.input != null) existing.card.input = e.input
          if (e.output != null) existing.card.output = e.output
        }
      } else {
        // 首次出现：先刷出累积文本，再插入 tool block（保持时间线交错）
        flushText()
        const block: { kind: 'tool'; card: ToolCard } = {
          kind: 'tool',
          card: {
            toolId: e.toolId,
            title: e.title,
            status: e.status ?? 'running',
            input: detail?.input ?? e.input,
            output: detail?.output ?? e.output,
          },
        }
        blocks.push(block)
        toolIndex.set(e.toolId, block)
      }
    } else if (e.type === 'plan' && e.plan) {
      flushText()
      blocks.push({ kind: 'plan' })
    }
  }

  // 刷出剩余文本（工具调用之后的 AI 继续输出）
  flushText()

  return blocks
}
