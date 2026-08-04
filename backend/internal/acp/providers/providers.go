// Package providers 封装各 Agent 的启动参数与适配逻辑。
// 不同 Agent（reasonix、pi、grok 等）的 CLI 参数差异集中在此处处理。
package providers

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/zacp/zacp/internal/config"
)

// Provider 封装单个 Agent 的启动配置。
type Provider struct {
	// ID 是 Agent 配置 ID（如 "reasonix"）。
	ID string
	// Name 是显示名称。
	Name string
	// Command 是 Agent 二进制路径或命令名。
	Command string
	// Args 是启动参数。
	Args []string
	// Env 是额外环境变量。
	Env []string
	// DefaultCwd 是默认工作目录（空则用 session.default_cwd）。
	DefaultCwd string
}

// NewFromConfig 从配置创建 Provider。
func NewFromConfig(cfg config.AgentConfig) (*Provider, error) {
	if cfg.ID == "" {
		return nil, fmt.Errorf("agent id is required")
	}
	if cfg.Command == "" {
		return nil, fmt.Errorf("agent '%s' command is required", cfg.ID)
	}

	// 解析 command 路径
	command := resolveCommand(cfg.ID, cfg.Command)

	// 默认参数
	args := cfg.Args
	if len(args) == 0 {
		args = []string{"--acp"}
	}

	return &Provider{
		ID:         cfg.ID,
		Name:       cfg.Name,
		Command:    command,
		Args:       args,
		Env:        cfg.Env,
		DefaultCwd: cfg.Cwd,
	}, nil
}

// ResolveCwd 解析实际工作目录。
// 如果 provider.DefaultCwd 为空，使用 sessionDefault。
func (p *Provider) ResolveCwd(sessionDefault string) string {
	cwd := p.DefaultCwd
	if cwd == "" {
		cwd = sessionDefault
	}
	if cwd == "" {
		cwd = "."
	}
	// 转换为绝对路径
	if abs, err := filepath.Abs(cwd); err == nil {
		return abs
	}
	return cwd
}

// resolveCommand 解析 Agent 二进制路径。
// 对于 reasonix 有特殊的路径查找逻辑。
func resolveCommand(agentID, command string) string {
	// 如果是绝对路径或相对路径，直接返回
	if filepath.IsAbs(command) {
		return command
	}
	if filepath.Base(command) != command {
		// 包含路径分隔符，认为是相对路径
		return command
	}

	// 尝试从 PATH 查找
	if p, err := exec.LookPath(command); err == nil {
		return p
	}

	// 特殊处理：reasonix 的常见安装路径
	if agentID == "reasonix" {
		if v := os.Getenv("REASONIX_BIN"); v != "" {
			return v
		}
		candidates := []string{
			"/home/xiaoz/.local/share/pnpm/global/5/.pnpm/@reasonix+cli-linux-x64@1.17.1-rc.1/node_modules/@reasonix/cli-linux-x64/bin/reasonix",
			"/data/apps/znode/bin/reasonix",
			filepath.Join(os.Getenv("HOME"), ".local/share/pnpm/reasonix"),
		}
		for _, c := range candidates {
			if st, err := os.Stat(c); err == nil && !st.IsDir() {
				return c
			}
		}
	}

	// 返回原始命令名，让 exec 去查找
	return command
}

// ProviderRegistry 管理多个 Provider。
type ProviderRegistry struct {
	providers map[string]*Provider
}

// NewRegistry 从配置创建 ProviderRegistry。
func NewRegistry(agents []config.AgentConfig) (*ProviderRegistry, error) {
	registry := &ProviderRegistry{
		providers: make(map[string]*Provider),
	}

	for _, cfg := range agents {
		if !cfg.Enabled {
			continue
		}
		p, err := NewFromConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("create provider '%s': %w", cfg.ID, err)
		}
		registry.providers[cfg.ID] = p
	}

	return registry, nil
}

// Get 返回指定 ID 的 Provider。
func (r *ProviderRegistry) Get(id string) (*Provider, bool) {
	p, ok := r.providers[id]
	return p, ok
}

// List 返回所有已注册的 Provider ID 列表。
func (r *ProviderRegistry) List() []string {
	ids := make([]string, 0, len(r.providers))
	for id := range r.providers {
		ids = append(ids, id)
	}
	return ids
}
