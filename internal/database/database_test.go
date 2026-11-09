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

	var firstSenderID int64
	if err := db.Raw(`SELECT first_sender_id FROM forward_records WHERE content_key = ?`, "legacy").Scan(&firstSenderID).Error; err != nil {
		t.Fatalf("read migrated first_sender_id: %v", err)
	}
	if firstSenderID != 0 {
		t.Errorf("legacy first_sender_id = %d, want 0", firstSenderID)
	}

	var lastOccurrenceMsgID sql.NullInt64
	if err := db.Raw(`SELECT last_occurrence_msg_id FROM forward_records WHERE content_key = ?`, "legacy").Scan(&lastOccurrenceMsgID).Error; err != nil {
		t.Fatalf("read migrated last_occurrence_msg_id: %v", err)
	}
	if lastOccurrenceMsgID.Valid {
		t.Errorf("legacy last_occurrence_msg_id = %d, want NULL", lastOccurrenceMsgID.Int64)
	}
}

// TestClearLegacyUserNames 用旧表结构建库并写入姓名，模拟升级流程：
// AutoMigrate 保留列，迁移逻辑清空历史 username/first_name，但保留用户行。
func TestClearLegacyUserNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-names.db")
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

	if err := db.Exec(`CREATE TABLE user_stats (
		user_id integer PRIMARY KEY,
		username text,
		first_name text,
		rescue_count integer DEFAULT 0,
		updated_at datetime
	)`).Error; err != nil {
		t.Fatalf("create legacy user_stats: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO user_stats (user_id, username, first_name, rescue_count) VALUES (?, ?, ?, ?)`,
		7, "alice", "Alice", 3,
	).Error; err != nil {
		t.Fatalf("insert legacy user_stats: %v", err)
	}

	if err := db.AutoMigrate(&models.UserStats{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	if err := clearLegacyUserNames(db); err != nil {
		t.Fatalf("clearLegacyUserNames() error = %v", err)
	}

	var row struct {
		UserID      int64
		Username    string
		FirstName   string
		RescueCount int
	}
	if err := db.Raw(`SELECT user_id, username, first_name, rescue_count FROM user_stats WHERE user_id = ?`, 7).Scan(&row).Error; err != nil {
		t.Fatalf("read migrated user_stats: %v", err)
	}
	if row.UserID != 7 || row.RescueCount != 3 {
		t.Errorf("migrated row = %+v, want user 7 with count 3", row)
	}
	if row.Username != "" || row.FirstName != "" {
		t.Errorf("names after migration = %q/%q, want empty", row.Username, row.FirstName)
	}
	for _, column := range []string{"username", "first_name"} {
		if !db.Migrator().HasColumn(&models.UserStats{}, column) {
			t.Errorf("column %q was dropped, want retained for compatibility", column)
		}
	}
}

// TestInitMigratesLegacyMessageUnix 用升级前的库启动，确认 message_unix 被补成可空列，
// 旧流水仍在且时间为空，进程可以照常打开数据库。
func TestInitMigratesLegacyMessageUnix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-message-unix.db")
	raw, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	rawSQL, err := raw.DB()
	if err != nil {
		t.Fatalf("get underlying sql.DB: %v", err)
	}
	if err := raw.Exec(`CREATE TABLE forward_logs (
		id integer PRIMARY KEY AUTOINCREMENT,
		record_id integer NOT NULL,
		sender_id integer NOT NULL,
		target_msg_id integer NOT NULL,
		created_at datetime
	)`).Error; err != nil {
		t.Fatalf("create legacy forward_logs: %v", err)
	}
	if err := raw.Exec(
		`INSERT INTO forward_logs (record_id, sender_id, target_msg_id) VALUES (?, ?, ?)`,
		1, 42, 7,
	).Error; err != nil {
		t.Fatalf("insert legacy log: %v", err)
	}
	if err := rawSQL.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	oldPath := config.DBPath
	oldDB := DB
	config.DBPath = path
	t.Cleanup(func() {
		config.DBPath = oldPath
		if DB != nil && DB != oldDB {
			if sqlDB, err := DB.DB(); err == nil {
				sqlDB.Close()
			}
		}
		DB = oldDB
	})

	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	var unix sql.NullInt64
	if err := DB.Raw(`SELECT message_unix FROM forward_logs WHERE sender_id = ?`, 42).Scan(&unix).Error; err != nil {
		t.Fatalf("read migrated message_unix: %v", err)
	}
	if unix.Valid {
		t.Errorf("legacy message_unix = %d, want NULL", unix.Int64)
	}

	stamp := int64(1_700_000_000)
	if err := DB.Create(&models.ForwardLog{
		RecordID:    1,
		SenderID:    43,
		TargetMsgID: 8,
		MessageUnix: &stamp,
	}).Error; err != nil {
		t.Fatalf("insert timed log: %v", err)
	}
	var count int64
	if err := DB.Model(&models.ForwardLog{}).Count(&count).Error; err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if count != 2 {
		t.Errorf("logs = %d, want 2", count)
	}
}

// TestAutoMigrateAddsTimeIndexes 验证旧库升级后会补上排行榜和清理任务所需的
// forward_logs.created_at、forward_records.updated_at 索引。
func TestAutoMigrateAddsTimeIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-indexes.db")
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

	if err := db.Exec(`CREATE TABLE forward_records (
		id integer PRIMARY KEY AUTOINCREMENT,
		chat_id integer NOT NULL,
		content_key text NOT NULL,
		forward_count integer DEFAULT 1,
		first_target_msg_id integer,
		created_at datetime,
		updated_at datetime
	)`).Error; err != nil {
		t.Fatalf("create legacy forward_records: %v", err)
	}
	if err := db.Exec(`CREATE TABLE forward_logs (
		id integer PRIMARY KEY AUTOINCREMENT,
		record_id integer NOT NULL,
		sender_id integer NOT NULL,
		target_msg_id integer NOT NULL,
		created_at datetime
	)`).Error; err != nil {
		t.Fatalf("create legacy forward_logs: %v", err)
	}

	if err := db.AutoMigrate(&models.ForwardRecord{}, &models.ForwardLog{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	for _, tc := range []struct {
		model interface{}
		index string
	}{
		{&models.ForwardRecord{}, "idx_forward_records_updated_at"},
		{&models.ForwardLog{}, "idx_forward_logs_created_at"},
	} {
		if !db.Migrator().HasIndex(tc.model, tc.index) {
			t.Errorf("index %q was not created", tc.index)
		}
	}
}
