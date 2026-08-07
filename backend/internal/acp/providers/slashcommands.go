package providers

import (
	"strings"

	"github.com/zacp/zacp/internal/model"
)

// DefaultSlashCommands 返回按 agent id 内置的静态 / 命令列表。
//
// 背景：ACP 协议中可用 / 命令由 agent 经 available_commands_update 主动通告
// （协议约定为 MAY，agent 不通告完全合规）。部分 agent（如 grok）不通告命令，
// 导致前端候选面板为空。这里为已知 agent 内置一份「展示层兜底」命令集：
// 选中后命令会作为普通用户消息经 session/prompt 发送给 agent（ACP 协议约定），
// 命令语义仍由 agent 侧解析，zacp 只负责展示与转发。
//
// 返回 nil 表示该 agent 无内置命令，保持「仅展示 agent 通告」的现状。
// 命令列表可按 agent 实际 CLI 语义调整；description 使用英文展示，
// 与前端候选面板的展示语言保持一致。
func DefaultSlashCommands(agentID string) []model.AvailableCommandDTO {
	switch strings.ToLower(strings.TrimSpace(agentID)) {
	case "grok":
		return []model.AvailableCommandDTO{
			{Name: "init", Description: "Initialize the project / session", InputHint: "<optional task>"},
			{Name: "model", Description: "Switch model", InputHint: "<model name>"},
			{Name: "effort", Description: "Set reasoning effort", InputHint: "<min|low|medium|high|max>"},
			{Name: "auto", Description: "Enable auto mode (autonomous execution without asking)"},
			{Name: "always-approve", Description: "Always approve permission requests"},
			{Name: "skills", Description: "View / manage skills"},
			{Name: "session-info", Description: "Show current session info"},
			{Name: "plan", Description: "Create an implementation plan", InputHint: "<requirement>"},
			{Name: "view-plan", Description: "View the current plan"},
			{Name: "usage", Description: "View usage / quota"},
		}
	// case "opencode":
	// 	return []model.AvailableCommandDTO{
	// 		{Name: "help", Description: "Show help / available commands"},
	// 		{Name: "compact", Description: "Compact conversation history"},
	// 		{Name: "init", Description: "Initialize project (create AGENTS.md)", InputHint: "<optional task>"},
	// 		{Name: "models", Description: "Switch model", InputHint: "<model name>"},
	// 		{Name: "thinking", Description: "Toggle / set reasoning effort", InputHint: "<mode>"},
	// 	}
	default:
		return nil
	}
}

// MergeSlashCommands 合并 agent 通告的命令与 zacp 静态兜底命令。
// 规则：
//   - 同名命令保留「agent 通告」项（agent 自己声明的更权威，静态命令不覆盖）；
//   - 通告命令在前、静态命令追加在后；
//   - 保证 agent 不通告命令（如 grok）时前端仍能展示静态命令。
//
// 返回结果统一为**非 nil 空切片**（JSON 序列化为 `[]` 而非 `null`）：
// 前端 Composer 对 slashCommands 直接做 .filter()，null 会抛 TypeError。
// 供 REST（GetSlashCommands）与 WS 广播（applyAvailableCommands）复用，两侧结果一致。
func MergeSlashCommands(advertised, statics []model.AvailableCommandDTO) []model.AvailableCommandDTO {
	if len(statics) == 0 {
		// 无静态命令（非 grok 等其它 agent 现状）：原样返回通告；
		// 通告也为空时归一为空切片，避免 REST 序列化为 null。
		if advertised == nil {
			return []model.AvailableCommandDTO{}
		}
		return advertised
	}
	merged := make([]model.AvailableCommandDTO, 0, len(advertised)+len(statics))
	seen := make(map[string]struct{}, len(advertised)+len(statics))
	for _, c := range advertised {
		name := strings.ToLower(c.Name)
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		merged = append(merged, c)
	}
	for _, c := range statics {
		name := strings.ToLower(c.Name)
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		merged = append(merged, c)
	}
	return merged
}
