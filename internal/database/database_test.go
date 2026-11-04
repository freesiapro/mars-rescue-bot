package database

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"mars-rescue-bot/internal/config"
	"mars-rescue-bot/internal/models"
)

func TestInitAppliesSQLiteSettings(t *testing.T) {
	oldPath := config.DBPath
	config.DBPath = filepath.Join(t.TempDir(), "test.db")
	t.Cleanup(func() {
		config.DBPath = oldPath
		if DB != nil {
			if sqlDB, err := DB.DB(); err == nil {
				sqlDB.Close()
			}
		}
	})

	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	sqlDB, err := DB.DB()
	if err != nil {
		t.Fatalf("DB.DB() error = %v", err)
	}

	var journalMode string
	if err := sqlDB.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}

	var busyTimeout int
	if err := sqlDB.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busyTimeout)
	}

	var foreignKeys int
	if err := sqlDB.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}

	if got := sqlDB.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1", got)
	}
}

// TestAutoMigrateKeepsLegacyRowsPendingNull 验证升级前已有的 forward_records 行
// 在 AutoMigrate 新增 pending_pair 列后保持 NULL，从而由仓储层按 ForwardCount
// 奇偶推导待配对状态，无需一次性回填。
func TestAutoMigrateKeepsLegacyRowsPendingNull(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get underlying sql.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	// 模拟升级前的表结构：没有 pending_pair 列。
	if err := db.Exec(`CREATE TABLE forward_records (
		id integer PRIMARY KEY AUTOINCREMENT,
		chat_id integer NOT NULL,
		content_key text NOT NULL,
		forward_count integer DEFAULT 1,
		first_target_msg_id integer,
		created_at datetime,
		updated_at datetime
	)`).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO forward_records (chat_id, content_key, forward_count, first_target_msg_id) VALUES (?, ?, ?, ?)`,
		-100, "legacy", 2, 5,
	).Error; err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	if err := db.AutoMigrate(&models.ForwardRecord{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	var pending sql.NullBool
	if err := db.Raw(`SELECT pending_pair FROM forward_records WHERE content_key = ?`, "legacy").Scan(&pending).Error; err != nil {
		t.Fatalf("read migrated pending_pair: %v", err)
	}
	if pending.Valid {
		t.Errorf("legacy pending_pair = %v, want NULL", pending.Bool)
	}
}
