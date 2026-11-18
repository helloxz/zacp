package manager

import (
	"fmt"
	"strings"
)

// 语言指令文本：按前端显示语言映射。这里用「界面语言」作为回复语言的锚点，
// 目的是消除「界面显示越南语、agent 默认回中文」这类不一致。
// 之所以写成自然语言指令而不是 ACP 配置项，是因为各 CLI Agent 的配置项名不统一，
// 而 prompt 前缀对所有支持 ACP 的 Agent 都生效。
var responseLanguageInstructions = map[string]string{
	"zh-cn": "请始终使用简体中文回复。当前界面语言为简体中文，请保持回复语言与界面一致。",
	"zh":    "请始终使用简体中文回复。当前界面语言为简体中文，请保持回复语言与界面一致。",
	"zh-hans": "请始终使用简体中文回复。当前界面语言为简体中文，请保持回复语言与界面一致。",
	"zh-tw":   "請始終使用繁體中文回覆。目前介面語言為繁體中文，請保持回覆語言與介面一致。",
	"zh-hant": "請始終使用繁體中文回覆。目前介面語言為繁體中文，請保持回覆語言與介面一致。",
	"en-us":   "Always reply in English. The interface language is English; keep your responses in the same language as the interface.",
	"en":      "Always reply in English. The interface language is English; keep your responses in the same language as the interface.",
	"vi-vn":   "Hãy luôn trả lời bằng tiếng Việt. Giao diện hiện đang dùng tiếng Việt, vui lòng giữ câu trả lời cùng ngôn ngữ với giao diện.",
	"vi":      "Hãy luôn trả lời bằng tiếng Việt. Giao diện hiện đang dùng tiếng Việt, vui lòng giữ câu trả lời cùng ngôn ngữ với giao diện.",
	"chinese": "请始终使用简体中文回复。当前界面语言为简体中文，请保持回复语言与界面一致。",
	"english": "Always reply in English. The interface language is English; keep your responses in the same language as the interface.",
	"vietnamese": "Hãy luôn trả lời bằng tiếng Việt. Giao diện hiện đang dùng tiếng Việt, vui lòng giữ câu trả lời cùng ngôn ngữ với giao diện.",
}

// languageDirective 计算要注入到 prompt 的语言指令。
//
// 优先级（高 → 低）：
//  1. 单个 Agent 的 [[agents]].lang（显式值，非 "auto"）
//  2. 全局 [session].lang（显式值，非 "auto"）
//  3. 前端随 prompt 传来的显示语言（lang 参数）
//  4. 空（不注入，保持旧行为）
//
// 返回空字符串表示不注入；这样老调用方（不传 lang）行为完全不变。
func (m *Manager) languageDirective(agentID, clientLang string) string {
	resolved := ""
	if p, ok := m.registry.Get(agentID); ok && isExplicitLang(p.Lang) {
		resolved = p.Lang
	}
	if resolved == "" && isExplicitLang(m.lang) {
		resolved = m.lang
	}
	if resolved == "" {
		resolved = clientLang
	}
	return responseLanguageInstruction(resolved)
}

// isExplicitLang 判断配置项是否为「强制指定语言」："auto"、空串、auto 的各种大小写写法都视为跟随前端。
func isExplicitLang(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v != "" && v != "auto" && v != "follow" && v != "ui"
}

// responseLanguageInstruction 把语言标签/名称转成指令文本。
// 未命中已知标签时，对显式配置值生成通用指令（便于用户填 "Japanese" 等），
// 对前端传来的未知 locale 则不注入，避免把 "fr-FR" 之类的代码原样发给 agent。
func responseLanguageInstruction(lang string) string {
	key := strings.ToLower(strings.TrimSpace(lang))
	key = strings.ReplaceAll(key, "_", "-")
	if key == "" {
		return ""
	}
	if text, ok := responseLanguageInstructions[key]; ok {
		return text
	}
	if isExplicitLang(lang) && !strings.Contains(key, "-") {
		return fmt.Sprintf("Always reply in %s. Keep your responses in the same language as the user interface.", strings.TrimSpace(lang))
	}
	return ""
}
