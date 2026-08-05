package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 写临时 TOML 配置，返回其路径。
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// 默认配置下 idle_timeout 应为 30m（空态无需写配置）。
func TestIdleTimeoutDefault(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Session.IdleTimeout != 30*time.Minute {
		t.Fatalf("default idle_timeout = %v, want 30m", cfg.Session.IdleTimeout)
	}
}

// TOML 中 idle_timeout 可覆盖为任意 duration；"0" 表示禁用回收。
func TestIdleTimeoutFromTOML(t *testing.T) {
	path := writeTempConfig(t, `
[server]
addr = ":0"

[session]
idle_timeout = "1h"
`)
	cfg, err := Load(t.TempDir(), path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Session.IdleTimeout != time.Hour {
		t.Fatalf("idle_timeout = %v, want 1h", cfg.Session.IdleTimeout)
	}

	path = writeTempConfig(t, `
[server]
addr = ":0"

[session]
idle_timeout = "0"
`)
	cfg, err = Load(t.TempDir(), path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Session.IdleTimeout != 0 {
		t.Fatalf("idle_timeout = %v, want 0 (disabled)", cfg.Session.IdleTimeout)
	}
}
