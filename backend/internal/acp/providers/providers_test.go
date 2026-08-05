package providers

import (
	"reflect"
	"testing"

	"github.com/zacp/zacp/internal/config"
)

// List() 必须按 config.toml 中 [[agents]] 的书写顺序返回（且过滤 enabled=false），
// 这样「第一个 agent」= 配置中最顶部那个，前端展示顺序也稳定。
func TestRegistryListKeepsConfigOrder(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "first", Name: "First", Enabled: true, Command: "echo"},
		{ID: "disabled", Name: "Disabled", Enabled: false, Command: "echo"},
		{ID: "second", Name: "Second", Enabled: true, Command: "echo"},
		{ID: "third", Name: "Third", Enabled: true, Command: "echo"},
	}
	registry, err := NewRegistry(agents)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	got := registry.List()
	want := []string{"first", "second", "third"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %v, want %v (配置书写顺序, 过滤 disabled)", got, want)
	}

	// 多次调用必须稳定（不能是 map 随机序）
	again := registry.List()
	if !reflect.DeepEqual(got, again) {
		t.Fatalf("List() 不稳定: first=%v second=%v", got, again)
	}
}

// 没有 enabled 的 agent 时返回空列表，调用方（main）不应 panic。
func TestRegistryListEmpty(t *testing.T) {
	registry, err := NewRegistry(nil)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if got := registry.List(); len(got) != 0 {
		t.Fatalf("List() = %v, want empty", got)
	}
}
