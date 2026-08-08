package eventstore

import (
	"encoding/json"
	"testing"

	"github.com/helloxz/zacp/internal/acp/client"
)

// TestSplitToolDetails 覆盖：非工具事件原样保留、工具事件瘦身、
// update 替换语义（最终值覆盖）、input/output 缺失时对应字段不落。
func TestSplitToolDetails(t *testing.T) {
	events := []client.Event{
		{Type: "agent_message", Text: "我来帮你查一下"},
		{Type: "tool_call", ToolID: "t1", Title: "read_file", Status: "running", Input: map[string]any{"path": "a.go"}},
		{Type: "tool_call_update", ToolID: "t1", Status: "completed", Output: "旧内容"},
		{Type: "tool_call_update", ToolID: "t1", Status: "completed", Output: "最终内容"},
		{Type: "tool_call", ToolID: "t2", Title: "bash", Status: "running", Input: "ls -la"},
		{Type: "tool_call_update", ToolID: "t2", Status: "completed", Output: []byte(`{"ok":true}`)},
		{Type: "agent_message", Text: "搞定"},
	}

	slim, details := SplitToolDetails(events)

	// 事件总数不变，非工具事件原样保留
	if len(slim) != len(events) {
		t.Fatalf("slim 长度 = %d, 期望 %d", len(slim), len(events))
	}
	if slim[0].Text != "我来帮你查一下" || slim[6].Text != "搞定" {
		t.Errorf("非工具事件被改动: %+v / %+v", slim[0], slim[6])
	}

	// 工具事件保留 type/toolId/title/status，剥离 input/output
	for _, idx := range []int{1, 2, 3, 4, 5} {
		if slim[idx].Type == "" || slim[idx].ToolID == "" {
			t.Errorf("slim[%d] 丢失标识字段: %+v", idx, slim[idx])
		}
		if slim[idx].Input != nil || slim[idx].Output != nil {
			t.Errorf("slim[%d] 未剥离详情: %+v", idx, slim[idx])
		}
	}
	if slim[2].Status != "completed" || slim[3].Status != "completed" {
		t.Errorf("status 字段丢失: %+v / %+v", slim[2], slim[3])
	}

	// 每工具只留最终一份详情（update 覆盖旧值）
	if len(details) != 2 {
		t.Fatalf("details 数量 = %d, 期望 2", len(details))
	}
	d1 := details["t1"]
	if d1.Input == nil {
		t.Errorf("t1 缺少 Input（首条 tool_call 提供）")
	}
	if got, ok := d1.Output.(string); !ok || got != "最终内容" {
		t.Errorf("t1 Output = %v, 期望覆盖为「最终内容」", d1.Output)
	}
	d2 := details["t2"]
	if got, ok := d2.Input.(string); !ok || got != "ls -la" {
		t.Errorf("t2 Input = %v", d2.Input)
	}
	// Output 是 []byte（json.RawMessage 类型），应保留原始字节内容
	if _, ok := d2.Output.([]byte); !ok {
		t.Errorf("t2 Output 类型 = %T, 期望 []byte", d2.Output)
	}
}

// TestSplitToolDetailsTypedNil 类型化 nil（json.RawMessage(nil)、空字符串）不写入 details，
// 避免覆盖已捕获的入参（真实 SDK 流式事件中常见）。
func TestSplitToolDetailsTypedNil(t *testing.T) {
	events := []client.Event{
		{Type: "tool_call", ToolID: "t1", Input: json.RawMessage(`{"path":"a.go"}`)},
		{Type: "tool_call_update", ToolID: "t1", Input: json.RawMessage(nil)}, // 类型化 nil
		{Type: "tool_call_update", ToolID: "t1", Output: ""},                  // 空字符串视为无值
	}
	slim, details := SplitToolDetails(events)
	d := details["t1"]
	if d.Input == nil {
		t.Fatal("类型化 nil 覆盖了已有 Input")
	}
	if got, ok := d.Output.(string); ok && got != "" {
		t.Errorf("Output = %q, 期望保持空（无值不写入）", got)
	}
	if len(slim) != 3 {
		t.Fatalf("slim 长度 = %d, 期望 3", len(slim))
	}
}

// TestMarshalEmpty 空列表/空 map 序列化为 ""（与既有 events 空串约定一致）。
func TestMarshalEmpty(t *testing.T) {
	if got := Marshal(nil); got != "" {
		t.Errorf("Marshal(nil) = %q, 期望空串", got)
	}
	if got := Marshal([]client.Event{}); got != "" {
		t.Errorf("Marshal(empty) = %q, 期望空串", got)
	}
	if got := MarshalDetails(nil); got != "" {
		t.Errorf("MarshalDetails(nil) = %q, 期望空串", got)
	}
	if got := MarshalDetails(map[string]ToolDetail{}); got != "" {
		t.Errorf("MarshalDetails(empty) = %q, 期望空串", got)
	}
}

// TestMarshalRoundTrip 拆分后可序列化回同一结构（迁移回填链路自洽）。
func TestMarshalRoundTrip(t *testing.T) {
	events := []client.Event{
		{Type: "tool_call", ToolID: "t1", Title: "x", Status: "running", Input: map[string]any{"a": 1}},
		{Type: "tool_call_update", ToolID: "t1", Status: "completed", Output: "ok"},
	}
	slim, details := SplitToolDetails(events)

	var back []client.Event
	if err := json.Unmarshal([]byte(Marshal(slim)), &back); err != nil {
		t.Fatalf("slim 序列化后无法解析: %v", err)
	}
	if len(back) != 2 || back[1].Status != "completed" {
		t.Errorf("round trip 后事件结构异常: %+v", back)
	}

	var det map[string]ToolDetail
	if err := json.Unmarshal([]byte(MarshalDetails(details)), &det); err != nil {
		t.Fatalf("details 序列化后无法解析: %v", err)
	}
	if got := det["t1"].Output; got != "ok" {
		t.Errorf("round trip 后 t1.Output = %v", got)
	}
}
