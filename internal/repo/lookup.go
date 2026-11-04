package repo

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
	"mars-rescue-bot/internal/models"
)

// FindRecordByKey 只读查询指定群里的内容记录。
// found=false 表示该内容从未记录，与数据库错误区分开。
func (r *Repo) FindRecordByKey(chatID int64, key string) (*models.ForwardRecord, bool, error) {
	var record models.ForwardRecord
	err := r.db.Where("chat_id = ? AND content_key = ?", chatID, key).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("find record by key: %w", err)
	}
	return &record, true, nil
}

// ListRecordLogs 返回一条内容记录的全部出现流水，按写入顺序排列。
// 只读，供 /hyw 推导某条消息是否被记录以及相邻关系。
func (r *Repo) ListRecordLogs(recordID uint64) ([]models.ForwardLog, error) {
	var logs []models.ForwardLog
	if err := r.db.Where("record_id = ?", recordID).Order("id ASC").Find(&logs).Error; err != nil {
		return nil, fmt.Errorf("list record logs: %w", err)
	}
	return logs, nil
}
