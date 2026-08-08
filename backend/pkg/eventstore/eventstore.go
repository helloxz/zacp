// Package eventstore 提供消息事件列表的落库序列化与工具详情拆分，
// 供 WebSocket（internal/ws/bridge）与 REST（internal/service）两条落库路径
// 以及 v6 迁移（internal/store）共用，保证「events 瘦身 + tool_details 拆分」
// 的格式单一来源，防止两处实现漂移。
package eventstore

import (
	"encoding/json"
	"reflect"

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

// SplitToolDetails 拆分事件列表：
//   - 抽取每个工具最终的 input/output 到 details（update 替换语义：非空才覆盖，
//     与实时工具卡展示一致——这是列表瘦身 ~90% 的主要来源：update 的重复全量
//     快照不再落库，每工具只留最终一份）；
//   - 返回瘦身版事件列表：tool_call 系列事件去掉 Input/Output，其余字段
//     （type/toolId/title/status）原样保留，前端 deriveBlocks 仍能重建工具卡的时间线；
//   - 事件里的 SessionID 只用于实时 WS 路由，历史消息已由 Message.SessionID 绑定，
//     因此持久化副本不重复保存该字段。
func SplitToolDetails(events []client.Event) ([]client.Event, map[string]ToolDetail) {
	slim := make([]client.Event, 0, len(events))
	details := make(map[string]ToolDetail)
	for _, ev := range events {
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
