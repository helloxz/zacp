// Package store 负责数据库初始化、迁移和仓储操作。
// 使用 GORM + SQLite（纯 Go 驱动），启用 WAL 模式。
package store

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/zacp/zacp/internal/model"
)

// Store 封装数据库连接和仓储。
type Store struct {
	DB *gorm.DB
}

// Config 数据库配置。
type Config struct {
	// DBPath 数据库文件绝对路径。
	DBPath string
	// LogMode GORM 日志级别（silent/error/warn/info）。
	LogMode logger.LogLevel
}

// New 打开数据库并执行初始化。
func New(cfg Config) (*Store, error) {
	// 确保目录存在
	dir := filepath.Dir(cfg.DBPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create db dir %s: %w", dir, err)
	}

	// 打开数据库（纯 Go SQLite 驱动，无 CGO）
	// 基础 DSN，后续通过 PRAGMA 设置参数
	dsn := cfg.DBPath

	logLevel := cfg.LogMode
	if logLevel == 0 {
		logLevel = logger.Warn
	}

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", cfg.DBPath, err)
	}

	// 显式设置 PRAGMA 参数（更可靠）
	pragmas := []struct {
		name  string
		value string
	}{
		{"journal_mode", "WAL"},
		{"busy_timeout", "5000"},
		{"foreign_keys", "ON"},
	}

	for _, p := range pragmas {
		if err := db.Exec(fmt.Sprintf("PRAGMA %s=%s", p.name, p.value)).Error; err != nil {
			return nil, fmt.Errorf("set PRAGMA %s: %w", p.name, err)
		}
	}

	// 验证 WAL 是否启用
	var journalMode string
	if err := db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error; err != nil {
		return nil, fmt.Errorf("check journal_mode: %w", err)
	}
	if journalMode != "wal" {
		return nil, fmt.Errorf("WAL mode not enabled, got: %s", journalMode)
	}

	// 执行版本化迁移
	if err := runMigrations(db); err != nil {
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return &Store{DB: db}, nil
}

// Close 关闭数据库连接。
func (s *Store) Close() error {
	if s.DB == nil {
		return nil
	}
	sqlDB, err := s.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// runMigrations 执行版本化数据库迁移。
// 使用自管的 schema_migrations 表记录已执行的版本。
func runMigrations(db *gorm.DB) error {
	// 创建迁移记录表（如果不存在）
	if err := db.AutoMigrate(&model.SchemaMigration{}); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// 获取当前版本
	var currentVersion int
	if err := db.Model(&model.SchemaMigration{}).
		Select("COALESCE(MAX(version), 0)").
		Scan(&currentVersion).Error; err != nil {
		return fmt.Errorf("get current version: %w", err)
	}

	// 定义迁移（按版本号递增）
	migrations := []struct {
		Version int
		Name    string
		Func    func(*gorm.DB) error
	}{
		{
			Version: 1,
			Name:    "create_initial_tables",
			Func:    migrateV1,
		},
		{
			Version: 2,
			Name:    "workspace_archived_and_default",
			Func:    migrateV2,
		},
		{
			Version: 3,
			Name:    "session_config_options",
			Func:    migrateV3,
		},
		{
			Version: 4,
			Name:    "session_is_draft",
			Func:    migrateV4,
		},
	}

	// 执行未应用的迁移
	for _, m := range migrations {
		if m.Version <= currentVersion {
			continue
		}

		// 在事务中执行迁移
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := m.Func(tx); err != nil {
				return err
			}
			// 记录迁移版本
			return tx.Create(&model.SchemaMigration{
				Version:   m.Version,
				AppliedAt: time.Now(),
			}).Error
		})
		if err != nil {
			return fmt.Errorf("migration v%d (%s): %w", m.Version, m.Name, err)
		}
	}

	return nil
}

// migrateV1 创建初始表结构。
func migrateV1(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.Workspace{},
		&model.Session{},
		&model.Message{},
	)
}

// migrateV2 工作区增加归档与默认标记（不删历史会话）。
func migrateV2(db *gorm.DB) error {
	// AutoMigrate 会为已有表添加新列；archived / is_default 默认 false。
	return db.AutoMigrate(&model.Workspace{})
}

// migrateV3 为 sessions 表添加 config_options 列（ACP 会话配置项 JSON）。
func migrateV3(db *gorm.DB) error {
	return db.AutoMigrate(&model.Session{})
}

// migrateV4 为 sessions 表添加 is_draft 列（草稿会话标记）。
// 隐式 session/new 探测创建的会话 is_draft=true，不进侧栏列表；
// 用户发出首条 prompt 后转正（is_draft=false）。默认 false，兼容历史数据。
func migrateV4(db *gorm.DB) error {
	return db.AutoMigrate(&model.Session{})
}
