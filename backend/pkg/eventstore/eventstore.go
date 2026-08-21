// Package eventstore 提供消息事件列表的落库序列化、文本事件合并与工具详情拆分，
// 供 WebSocket（internal/ws/bridge）与 REST（internal/service）两条落库路径
// 以及 v6 迁移（internal/store）共用，保证「events 瘦身 + tool_details 拆分」
// 的格式单一来源，防止两处实现漂移。
package eventstore

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/helloxz/zacp/internal/acp/client"
)

// ToolDetail 单个工具调用的最终入参/出参（历史消息展开工具卡用）。
type ToolDetail struct {
	Input  any `json:"input,omitempty"`
	Output any `json:"output,omitempty"`
}

// IsEmpty 严格判空：nil、nil 指针/切片/map/channel/func、空字符串都视为「无值」。
// SDK 的 RawInput/RawOutput 可能是类型化 nil（如 json.RawMessage），
// 直接 != nil 判断不准确，会把 nil 写入 tool_details 造成前端覆盖已有入参。
func IsEmpty(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return rv.IsNil()
	case reflect.String:
		return rv.Len() == 0
	}
	return false
}

// toolEventTypes 携带工具详情的事件类型（落库时抽离 input/output）。
var toolEventTypes = map[string]bool{
	"tool_call":        true,
	"tool_call_update": true,
}

// compactTextEvents 合并同一消息中相邻的纯文本事件。
// Agent 流式输出会把一段 thought/message 切成很多小块；历史前端本来就按顺序
// 拼接这些文本，因此合并不会改变展示内容，只减少重复的 JSON 元数据。工具调用、
// plan、带额外元数据的文本事件以及跨工具边界的文本事件都不会被合并。
// 优化：同一合并组内复用 strings.Builder，避免 `+=` 导致的 O(n²) 重复拷贝与多次分配。
func compactTextEvents(events []client.Event) []client.Event {
	compacted := make([]client.Event, 0, len(events))
	var builder strings.Builder
	builderActive := false
	for _, event := range events {
		canMerge := false
		if len(compacted) > 0 {
			if builderActive {
				// Builder 持有当前合并组的文本，compacted[last].Text 已清空为 ""，
				// 需按组语义判断：同类型+同会话且当前块为纯文本即可续接
				last := compacted[len(compacted)-1]
				if last.Type == event.Type && last.SessionID == event.SessionID && isPlainTextEvent(event) {
					canMerge = true
				}
			} else {
				canMerge = canMergeTextEvents(compacted[len(compacted)-1], event)
			}
		}
		if canMerge {
			if !builderActive {
				// 首个合并：用 Builder 承载 prev.Text + cur.Text，后续追加仅 WriteString
				builder.Reset()
				prev := compacted[len(compacted)-1].Text
				builder.Grow(len(prev) + len(event.Text))
				builder.WriteString(prev)
				builder.WriteString(event.Text)
				builderActive = true
				compacted[len(compacted)-1].Text = ""
			} else {
				builder.WriteString(event.Text)
			}
			continue
		}
		// 遇到不可合并块，先刷出之前 Builder 累积的文本
		if builderActive {
			compacted[len(compacted)-1].Text = builder.String()
			builder.Reset()
			builderActive = false
		}
		compacted = append(compacted, event)
	}
	if builderActive {
		compacted[len(compacted)-1].Text = builder.String()
	}
	return compacted
}

func canMergeTextEvents(previous, current client.Event) bool {
	if previous.Type != current.Type || previous.SessionID != current.SessionID {
		return false
	}
	if current.Type != "agent_thought" && current.Type != "agent_message" {
		return false
	}
	return isPlainTextEvent(previous) && isPlainTextEvent(current)
}

func isPlainTextEvent(event client.Event) bool {
	return event.Text != "" &&
		event.Title == "" &&
		event.Status == "" &&
		event.ToolID == "" &&
		event.RawKind == "" &&
		IsEmpty(event.Input) &&
		IsEmpty(event.Output) &&
		event.Plan == nil
}

// SplitToolDetails 拆分事件列表：
//   - 抽取每个工具最终的 input/output 到 details（update 替换语义：非空才覆盖，
//     与实时工具卡展示一致——这是列表瘦身 ~90% 的主要来源：update 的重复全量
//     快照不再落库，每工具只留最终一份）；
//   - 返回瘦身版事件列表：tool_call 系列事件去掉 Input/Output，其余字段
//     （type/toolId/title/status）原样保留，前端 deriveBlocks 仍能重建工具卡的时间线；
//   - 事件里的 SessionID 只用于实时 WS 路由，历史消息已由 Message.SessionID 绑定，
//     因此持久化副本不重复保存该字段；相邻纯文本事件在此之前已完成合并。
func SplitToolDetails(events []client.Event) ([]client.Event, map[string]ToolDetail) {
	compacted := compactTextEvents(events)
	slim := make([]client.Event, 0, len(compacted))
	// 预分配：工具事件数通常 < 总事件数/2，预分配减少 map 扩容
	details := make(map[string]ToolDetail, len(compacted)/2+1)
	for _, ev := range compacted {
		// SessionID 必须只在持久化副本中清空：实时路由在调用本函数前已经
		// 使用原始 event.SessionID 完成广播；range 变量为值副本，不会改动原切片。
		ev.SessionID = ""
		if toolEventTypes[ev.Type] && ev.ToolID != "" {
			d := details[ev.ToolID]
			if !IsEmpty(ev.Input) {
				d.Input = ev.Input
			}
			if !IsEmpty(ev.Output) {
				d.Output = ev.Output
			}
			// 全空（如 update 只报 status）不建 key，避免 tool_details 出现 {"t1":{}} 噪音
			if !IsEmpty(d.Input) || !IsEmpty(d.Output) {
				details[ev.ToolID] = d
			}
			// 瘦身：去掉详情字段（Event 为值类型，改副本不影响原切片）
			ev.Input = nil
			ev.Output = nil
		}
		slim = append(slim, ev)
	}
	return slim, details
}

// Marshal 将事件列表序列化为 JSON（空列表返回 ""，与既有 events 空串约定一致）。
func Marshal(events []client.Event) string {
	if len(events) == 0 {
		return ""
	}
	data, err := json.Marshal(events)
	if err != nil {
		return ""
	}
	return string(data)
}

// MarshalDetails 将工具详情 map 序列化为 JSON（空 map 返回 ""）。
func MarshalDetails(details map[string]ToolDetail) string {
	if len(details) == 0 {
		return ""
	}
	data, err := json.Marshal(details)
	if err != nil {
		return ""
	}
	return string(data)
}

// ContainsThought 廉价预筛：事件 JSON 中是否存在 agent_thought 事件。
// 落库序列化格式固定为紧凑 JSON 且 "type" 是事件首字段，子串匹配可靠；
// 列表瘦身热路径用它跳过无思考过程的消息，避免无谓的 JSON 解析。
func ContainsThought(eventsJSON string) bool {
	return strings.Contains(eventsJSON, `"type":"agent_thought"`)
}

// StripThoughtText 返回思考过程文本被置空后的事件 JSON：
// agent_thought 事件的 text 置空、type 字段保留——前端仍能据此判断
// 「存在思考过程」并展示折叠面板，内容改为展开时经 /thoughts 接口按需加载。
// 无 agent_thought 事件或解析失败时原样返回输入（不阻塞列表返回）。
func StripThoughtText(eventsJSON string) string {
	if eventsJSON == "" || !ContainsThought(eventsJSON) {
		return eventsJSON
	}
	var events []client.Event
	if err := json.Unmarshal([]byte(eventsJSON), &events); err != nil {
		return eventsJSON
	}
	changed := false
	for i := range events {
		if events[i].Type == "agent_thought" && events[i].Text != "" {
			events[i].Text = ""
			changed = true
		}
	}
	if !changed {
		return eventsJSON
	}
	return Marshal(events)
}

// ExtractThoughtText 从事件 JSON 中按序拼接全部 agent_thought 文本。
// 用于「思考过程按需加载」接口：DB 落库的 events 保留完整思考文本
// （列表接口已置空瘦身），这里从原始数据恢复拼接结果。
// 无思考过程或解析失败时返回空串。
func ExtractThoughtText(eventsJSON string) string {
	if eventsJSON == "" || !ContainsThought(eventsJSON) {
		return ""
	}
	var events []client.Event
	if err := json.Unmarshal([]byte(eventsJSON), &events); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, ev := range events {
		if ev.Type == "agent_thought" && ev.Text != "" {
			sb.WriteString(ev.Text)
		}
	}
	return sb.String()
}
