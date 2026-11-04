// Package testutil 为依赖数据库的测试提供测试基础设施。
package testutil

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// SetupDB 为单个测试创建一个隔离的临时 SQLite 数据库文件，
// 对给定的模型执行 AutoMigrate，并返回 *gorm.DB 实例。
// 使用 t.TempDir 保证每个测试独立，并在测试结束时关闭连接。
func SetupDB(t *testing.T, models ...interface{}) *gorm.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.db")
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get underlying sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	return db
}
