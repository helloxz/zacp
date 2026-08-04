// Package config 负责从 $ZACP_HOME/config.toml 加载运行时配置，
// 并对外暴露强类型 Config 结构。业务层只依赖 Config，不直接使用 Viper。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Config 是 zacp 后端的全局运行时配置。
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Session  SessionConfig  `mapstructure:"session"`
	Database DatabaseConfig `mapstructure:"database"`
	Agents   []AgentConfig  `mapstructure:"agents"`
}

// ServerConfig HTTP 服务配置。
type ServerConfig struct {
	Addr string `mapstructure:"addr"`
	Mode string `mapstructure:"mode"` // debug | release
}

// SessionConfig 会话默认参数。
type SessionConfig struct {
	// DefaultCwd 是 Agent 默认工作目录（不是 $ZACP_HOME）。
	DefaultCwd string `mapstructure:"default_cwd"`
	// AutoApprove 是否自动批准 Agent 权限请求（开发模式用）。
	AutoApprove bool `mapstructure:"auto_approve"`
}

// DatabaseConfig 数据库配置。
type DatabaseConfig struct {
	// Path 相对于 $ZACP_HOME，默认 data/zacp.db。
	Path string `mapstructure:"path"`
}

// AgentConfig 单个 Agent 的启动配置。
type AgentConfig struct {
	ID        string   `mapstructure:"id"`
	Name      string   `mapstructure:"name"`
	Enabled   bool     `mapstructure:"enabled"`
	Transport string   `mapstructure:"transport"` // 当前仅 stdio
	Command   string   `mapstructure:"command"`
	Args      []string `mapstructure:"args"`
	Cwd       string   `mapstructure:"cwd"` // 空则用 session.default_cwd
	// Env 额外环境变量（key=value 格式），不在此处写密钥。
	Env []string `mapstructure:"env"`
}

// DefaultConfig 返回默认配置。
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Addr: ":8080",
			Mode: "debug",
		},
		Session: SessionConfig{
			DefaultCwd:  ".",
			AutoApprove: false,
		},
		Database: DatabaseConfig{
			Path: "data/zacp.db",
		},
		Agents: []AgentConfig{},
	}
}

// Load 从 $ZACP_HOME/config.toml 加载配置。
// 优先级：默认值 < TOML 文件 < 环境变量（ZACP_ 前缀）< 显式覆盖。
// homeDir 是 $ZACP_HOME 的绝对路径，configPath 非空时直接使用该文件。
func Load(homeDir, configPath string) (*Config, error) {
	v := viper.New()
	v.SetConfigType("toml")

	// 设置默认值
	setDefaults(v)

	// 确定配置文件路径
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		if homeDir == "" {
			var err error
			homeDir, err = defaultHomeDir()
			if err != nil {
				return nil, fmt.Errorf("resolve ZACP_HOME: %w", err)
			}
		}
		v.AddConfigPath(homeDir)
		v.SetConfigName("config")
	}

	// 环境变量覆盖，前缀 ZACP_
	v.SetEnvPrefix("ZACP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// 读取配置文件（不存在则用默认值，不报错）
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			// 文件存在但解析失败，需要报错
			if configPath != "" || fileExists(v.ConfigFileUsed()) {
				return nil, fmt.Errorf("read config: %w", err)
			}
		}
	}

	cfg := DefaultConfig()
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	// 校验
	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

// ResolveDBPath 返回数据库文件的绝对路径。
func (c *Config) ResolveDBPath(homeDir string) string {
	if filepath.IsAbs(c.Database.Path) {
		return c.Database.Path
	}
	if homeDir == "" {
		homeDir, _ = defaultHomeDir()
	}
	return filepath.Join(homeDir, c.Database.Path)
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.addr", ":8680")
	v.SetDefault("server.mode", "debug")
	v.SetDefault("session.default_cwd", ".")
	v.SetDefault("session.auto_approve", false)
	v.SetDefault("database.path", "data/zacp.db")
}

func validate(cfg *Config) error {
	// server.mode 校验
	if cfg.Server.Mode != "debug" && cfg.Server.Mode != "release" {
		return fmt.Errorf("server.mode must be 'debug' or 'release', got '%s'", cfg.Server.Mode)
	}

	// agents 校验
	seen := make(map[string]bool)
	for i, a := range cfg.Agents {
		if a.ID == "" {
			return fmt.Errorf("agents[%d].id is required", i)
		}
		if seen[a.ID] {
			return fmt.Errorf("agents[%d].id '%s' is duplicated", i, a.ID)
		}
		seen[a.ID] = true
		if a.Enabled && a.Command == "" {
			return fmt.Errorf("agents[%d] '%s' is enabled but command is empty", i, a.ID)
		}
	}
	return nil
}

// defaultHomeDir 返回默认 $ZACP_HOME 路径。
func defaultHomeDir() (string, error) {
	if v := os.Getenv("ZACP_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".zacp"), nil
}

// HomeDir 返回当前生效的 $ZACP_HOME 路径。
func HomeDir() (string, error) {
	return defaultHomeDir()
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
