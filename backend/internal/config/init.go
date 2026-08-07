// Package config 提供配置加载和初始化功能。
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/helloxz/zacp/internal/web"
)

// EnsureHomeDir 确保 $ZACP_DATA 目录存在，返回绝对路径。
// homeDir 非空时使用调用方传入的目录（如命令行 --data-dir，优先级最高）；
// 为空则按 ZACP_DATA 环境变量 → ~/.zacp 自动解析。
// 如果目录不存在则创建，权限为 0700。
// 如果 config.toml 不存在，则从内嵌的示例配置创建。
func EnsureHomeDir(homeDir string) (string, error) {
	if homeDir == "" {
		var err error
		homeDir, err = HomeDir()
		if err != nil {
			return "", fmt.Errorf("get home directory: %w", err)
		}
	}

	// 转换为绝对路径
	absPath, err := filepath.Abs(homeDir)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}

	// 检查目录是否存在
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		// 创建目录，权限 0700（仅所有者可读写执行）
		if err := os.MkdirAll(absPath, 0700); err != nil {
			return "", fmt.Errorf("create home directory %s: %w", absPath, err)
		}
	} else if err != nil {
		return "", fmt.Errorf("check home directory %s: %w", absPath, err)
	}

	// 如果 config.toml 不存在，从示例配置创建
	configPath := filepath.Join(absPath, "config.toml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := createDefaultConfig(configPath); err != nil {
			// 创建配置文件失败不阻止启动，只打印警告
			fmt.Fprintf(os.Stderr, "warning: failed to create default config: %v\n", err)
		}
	}

	return absPath, nil
}

// createDefaultConfig 从内嵌的示例配置（backend/internal/web/config.example.toml，
// 由 scripts/build.sh 从 backend/configs/config.example.toml 同步）创建默认配置文件。
func createDefaultConfig(configPath string) error {
	content, err := web.ExampleConfig()
	if err != nil {
		return fmt.Errorf("read embedded example config: %w", err)
	}
	return os.WriteFile(configPath, content, 0600)
}

// EnsureDataDir 确保数据目录存在，返回绝对路径。
// 默认路径为 $ZACP_DATA/data，权限为 0700。
func EnsureDataDir() (string, error) {
	homeDir, err := EnsureHomeDir("")
	if err != nil {
		return "", err
	}

	dataDir := filepath.Join(homeDir, "data")

	// 检查目录是否存在
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		// 创建目录，权限 0700
		if err := os.MkdirAll(dataDir, 0700); err != nil {
			return "", fmt.Errorf("create data directory %s: %w", dataDir, err)
		}
	} else if err != nil {
		return "", fmt.Errorf("check data directory %s: %w", dataDir, err)
	}

	return dataDir, nil
}
