package providers

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/helloxz/zacp/internal/config"
)

// BuiltinAgents 是后端内置的固定智能体目录（用户设置页展示的兜底列表）。
// 设置页「智能体」菜单的数据 = 配置中的 [[agents]] + 本目录，按 id 合并去重
// （配置优先，见 BuildCatalog）。这些模板同时用于「开启」时写入 config.toml
// 的默认 command/args。
//
// 各 agent 的 ACP 启动参数并不统一，已按实际 CLI 校准：
//   - reasonix:  --acp
//   - omp:       acp（无 -- 前缀）
//   - qodercn:   二进制名是 qoderclicn，id 与显示名是 QoderCN
//   - grok:      agent stdio
//
// Enabled 统一为 false：表示「未写入用户配置 = 默认未启用」；
// catalog 中内置项也按 false 展示，用户在设置页开启后才写配置。
var BuiltinAgents = []config.AgentConfig{
	{ID: "reasonix", Name: "Reasonix", Command: "reasonix", Args: []string{"--acp"}},
	{ID: "omp", Name: "Omp", Command: "omp", Args: []string{"acp"}},
	{ID: "qodercn", Name: "QoderCN", Command: "qoderclicn", Args: []string{"--acp"}},
	{ID: "qoder", Name: "Qoder", Command: "qodercli", Args: []string{"--acp"}},
	{ID: "grok", Name: "Grok", Command: "grok", Args: []string{"agent", "stdio"}},
	{ID: "opencode", Name: "OpenCode", Command: "opencode", Args: []string{"acp"}},
	{ID: "codex-acp", Name: "Codex", Command: "codex-acp", Args: []string{}},
	{ID: "pi-acp", Name: "Pi", Command: "pi-acp", Args: []string{}},
}

// BuiltinTemplate 按 id（大小写不敏感）返回内置模板；未命中返回 false。
func BuiltinTemplate(id string) (config.AgentConfig, bool) {
	for _, b := range BuiltinAgents {
		if strings.EqualFold(b.ID, id) {
			return b, true
		}
	}
	return config.AgentConfig{}, false
}

// CatalogItem 设置页「智能体」列表的单条数据。
type CatalogItem struct {
	AgentID   string `json:"agentId"`
	Name      string `json:"name"`
	Command   string `json:"command"`
	Enabled   bool   `json:"enabled"`   // 配置中是否启用（内置且未写入配置时为 false）
	Installed bool   `json:"installed"` // 本机是否已安装（which 检测）
	Source    string `json:"source"`    // "config" | "builtin"
}

// BuildCatalog 合并配置与内置智能体，返回设置页展示列表：
//  1. 配置中的 [[agents]] 按原书写顺序在前（含 disabled，因为设置页要展示开关状态）
//  2. 内置智能体按固定顺序追加在后
//  3. 按 id 去重（小写归一化比较），去重时优先保留配置中的条目
func BuildCatalog(configured []config.AgentConfig) []CatalogItem {
	seen := make(map[string]bool, len(configured)+len(BuiltinAgents))
	items := make([]CatalogItem, 0, len(configured)+len(BuiltinAgents))

	// 配置优先
	for _, a := range configured {
		key := strings.ToLower(a.ID)
		if seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, CatalogItem{
			AgentID:   a.ID,
			Name:      a.Name,
			Command:   a.Command,
			Enabled:   a.Enabled,
			Installed: IsInstalled(a.Command),
			Source:    "config",
		})
	}

	// 内置追加（去重）
	for _, b := range BuiltinAgents {
		key := strings.ToLower(b.ID)
		if seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, CatalogItem{
			AgentID:   b.ID,
			Name:      b.Name,
			Command:   b.Command,
			Enabled:   false, // 未写入配置 = 默认未启用
			Installed: IsInstalled(b.Command),
			Source:    "builtin",
		})
	}

	return items
}

// execExts 返回 Windows 下可执行文件扩展名（PATHEXT），非 Windows 返回 nil。
func execExts() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	if v := os.Getenv("PATHEXT"); v != "" {
		return strings.Split(v, ";")
	}
	return []string{".COM", ".EXE", ".BAT", ".CMD"}
}

// statFile 判断路径是否为已存在的普通文件（非目录）。
func statFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// IsInstalled 判断命令是否已安装（跨平台、尽力而为，不追求 100% 准确）：
//   - 含路径分隔符（绝对/相对路径）：直接检查文件存在性；Windows 下额外尝试 PATHEXT 扩展名
//   - 纯命令名：exec.LookPath——Linux/macOS 查 PATH 且要求可执行位，
//     Windows 查 PATH + PATHEXT（自动补 .exe/.cmd 等）
//
// 已知边界（接受约 80% 准确度）：shell alias/函数、未激活的 conda/venv 环境、
// PATH 中存在同名但功能不同的程序等情况可能误判。
func IsInstalled(command string) bool {
	if command == "" {
		return false
	}
	if strings.ContainsAny(command, `/\\`) {
		// 绝对路径或相对路径：按文件存在性判断
		if statFile(command) {
			return true
		}
		for _, ext := range execExts() {
			if statFile(command + ext) {
				return true
			}
		}
		return false
	}
	_, err := exec.LookPath(command)
	return err == nil
}
