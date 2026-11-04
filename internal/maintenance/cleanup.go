// Package maintenance 定期清理超过保留期的数据。
package maintenance

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
	"mars-rescue-bot/internal/models"
)

// DefaultRetentionDays 与 DATA_RETENTION_DAYS 的默认值保持一致。
const DefaultRetentionDays = 180

const defaultBatchSize = 500

// Stats 汇总一次清理删除的行数。
type Stats struct {
	Logs      int64
	Records   int64
	UserStats int64
}

// Cleaner 按 updated_at/created_at 删除过期记录。
type Cleaner struct {
	db        *gorm.DB
	retention time.Duration
	batchSize int
	// afterRun 仅用于测试，在每次 RunOnce 结束后调用。
	afterRun func()
}

func New(db *gorm.DB, retentionDays int) *Cleaner {
	if retentionDays <= 0 {
		retentionDays = DefaultRetentionDays
	}
	return &Cleaner{
		db:        db,
		retention: time.Duration(retentionDays) * 24 * time.Hour,
		batchSize: defaultBatchSize,
	}
}

// RunOnce 删除 cutoff 之前的流水，然后删除已经过期的记录，最后清理不再被
// 任何流水引用的 user_stats 行。先删日志再删记录，避免留下孤儿流水。
func (c *Cleaner) RunOnce(now time.Time) (Stats, error) {
	cutoff := now.Add(-c.retention)
	var stats Stats

	// 1. 按 created_at 分批删除过期流水。活跃记录也可能有很旧的流水，
	//    因此这里不依赖记录的 updated_at。
	for {
		result := c.db.Exec(`
			DELETE FROM forward_logs
			WHERE id IN (
				SELECT id FROM forward_logs
				WHERE created_at < ?
				ORDER BY id
				LIMIT ?
			)
		`, cutoff, c.batchSize)
		if result.Error != nil {
			return stats, fmt.Errorf("delete expired logs: %w", result.Error)
		}
		stats.Logs += result.RowsAffected
		if result.RowsAffected == 0 {
			break
		}
	}

	// 2. 分批删除过期记录。删除记录前先删掉它可能残留的流水。
	for {
		var ids []uint64
		if err := c.db.Model(&models.ForwardRecord{}).
			Where("updated_at < ?", cutoff).
			Order("id").
			Limit(c.batchSize).
			Pluck("id", &ids).Error; err != nil {
			return stats, fmt.Errorf("find expired records: %w", err)
		}
		if len(ids) == 0 {
			break
		}

		var deletedLogs, deletedRecords int64
		err := c.db.Transaction(func(tx *gorm.DB) error {
			logs := tx.Where("record_id IN ?", ids).Delete(&models.ForwardLog{})
			if logs.Error != nil {
				return fmt.Errorf("delete logs of expired records: %w", logs.Error)
			}
			records := tx.Where("id IN ?", ids).Delete(&models.ForwardRecord{})
			if records.Error != nil {
				return fmt.Errorf("delete expired records: %w", records.Error)
			}
			deletedLogs = logs.RowsAffected
			deletedRecords = records.RowsAffected
			return nil
		})
		if err != nil {
			return stats, err
		}
		stats.Logs += deletedLogs
		stats.Records += deletedRecords
	}

	// 3. user_stats 只保存用户 ID；不再被保留期内的流水引用时一并删除。
	result := c.db.Exec(`
		DELETE FROM user_stats
		WHERE user_id NOT IN (SELECT DISTINCT sender_id FROM forward_logs)
	`)
	if result.Error != nil {
		return stats, fmt.Errorf("delete orphan user stats: %w", result.Error)
	}
	stats.UserStats = result.RowsAffected

	return stats, nil
}

// Start 立即执行一次清理，然后按 interval 周期执行，直到 ctx 取消。
func (c *Cleaner) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 24 * time.Hour
	}

	c.runAndLog(time.Now())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			c.runAndLog(now)
		}
	}
}

func (c *Cleaner) runAndLog(now time.Time) {
	stats, err := c.RunOnce(now)
	if err != nil {
		slog.Error("retention cleanup failed", "error", err)
	} else {
		slog.Info("retention cleanup finished",
			"logs_deleted", stats.Logs,
			"records_deleted", stats.Records,
			"user_stats_deleted", stats.UserStats,
		)
	}
	if c.afterRun != nil {
		c.afterRun()
	}
}
