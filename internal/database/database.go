package database

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"mars-rescue-bot/internal/config"
	"mars-rescue-bot/internal/models"
)

var DB *gorm.DB

type gormLogger struct {
	level logger.LogLevel
}

func (g *gormLogger) LogMode(level logger.LogLevel) logger.Interface {
	return &gormLogger{level: level}
}

func (g *gormLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	slog.Info(fmt.Sprintf(msg, data...), "source", "gorm")
}

func (g *gormLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	slog.Warn(fmt.Sprintf(msg, data...), "source", "gorm")
}

func (g *gormLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	slog.Error(fmt.Sprintf(msg, data...), "source", "gorm")
}

func (g *gormLogger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	if g.level <= logger.Silent {
		return
	}

	elapsed := time.Since(begin)
	sql, rows := fc()

	switch {
	case err != nil && err != gorm.ErrRecordNotFound && g.level >= logger.Error:
		slog.Error("SQL 执行失败",
			"source", "gorm",
			"elapsed_ms", elapsed.Milliseconds(),
			"rows", rows,
			"sql", sql,
			"error", err.Error(),
		)
	case elapsed > 200*time.Millisecond && g.level >= logger.Warn:
		slog.Warn("SQL 慢查询",
			"source", "gorm",
			"elapsed_ms", elapsed.Milliseconds(),
			"rows", rows,
			"sql", sql,
		)
	case g.level >= logger.Info:
		slog.Debug("SQL 执行",
			"source", "gorm",
			"elapsed_ms", elapsed.Milliseconds(),
			"rows", rows,
			"sql", sql,
		)
	}
}

// dsnWithPragmas 在数据库路径上附加驱动支持的 PRAGMA 参数。
// glebarez/sqlite（modernc.org/sqlite）通过重复的 _pragma 查询参数
// 在每条新连接上执行对应的 PRAGMA。
func dsnWithPragmas(path string) string {
	pragmas := "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	if strings.Contains(path, "?") {
		return path + "&" + pragmas
	}
	return path + "?" + pragmas
}

func Init() error {
	var err error
	DB, err = gorm.Open(sqlite.Open(dsnWithPragmas(config.DBPath)), &gorm.Config{
		Logger: (&gormLogger{}).LogMode(logger.Info),
	})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	sqlDB, err := DB.DB()
	if err != nil {
		return fmt.Errorf("get underlying sql.DB: %w", err)
	}
	// SQLite 写入串行化，避免 database is locked 以及单文件多连接带来的问题。
	sqlDB.SetMaxOpenConns(1)

	return autoMigrate()
}

func autoMigrate() error {
	return DB.AutoMigrate(
		&models.ForwardRecord{},
		&models.ForwardLog{},
		&models.UserStats{},
		&models.ChatSettings{},
		&models.ChatIgnoredUser{},
	)
}
