package repo

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"mars-rescue-bot/internal/models"
)

const (
	// DefaultCooldownSeconds 为 0，表示每次配对提醒都立即发送。
	DefaultCooldownSeconds = 0
	// DefaultMentionEnabled 默认在回复中 @ 重复发送者。
	DefaultMentionEnabled = true
)

func defaultChatSettings(chatID, updatedBy int64) models.ChatSettings {
	return models.ChatSettings{
		ChatID:          chatID,
		Enabled:         true,
		CooldownSeconds: DefaultCooldownSeconds,
		MentionEnabled:  DefaultMentionEnabled,
		UpdatedBy:       updatedBy,
	}
}

// GetChatSettings 读取群设置；不存在时返回默认设置但不落库。
func (r *Repo) GetChatSettings(chatID int64) (*models.ChatSettings, error) {
	var settings models.ChatSettings
	err := r.db.Where("chat_id = ?", chatID).First(&settings).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		defaults := defaultChatSettings(chatID, 0)
		return &defaults, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get chat settings: %w", err)
	}
	return &settings, nil
}

func (r *Repo) upsertChatSettings(settings *models.ChatSettings, updates map[string]interface{}) error {
	updates["updated_by"] = settings.UpdatedBy
	updates["updated_at"] = time.Now()
	err := r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "chat_id"}},
		DoUpdates: clause.Assignments(updates),
	}).Create(settings).Error
	if err != nil {
		return fmt.Errorf("upsert chat settings: %w", err)
	}
	return nil
}

// SetChatEnabled 开启或关闭本群的记录与回复。
func (r *Repo) SetChatEnabled(chatID int64, enabled bool, updatedBy int64) error {
	settings := defaultChatSettings(chatID, updatedBy)
	settings.Enabled = enabled
	return r.upsertChatSettings(&settings, map[string]interface{}{"enabled": enabled})
}

// SetChatCooldown 设置本群两条回复之间的最小间隔秒数。
func (r *Repo) SetChatCooldown(chatID int64, seconds int, updatedBy int64) error {
	settings := defaultChatSettings(chatID, updatedBy)
	settings.CooldownSeconds = seconds
	return r.upsertChatSettings(&settings, map[string]interface{}{"cooldown_seconds": seconds})
}

// SetChatMention 设置回复中是否 @ 重复发送者。
func (r *Repo) SetChatMention(chatID int64, enabled bool, updatedBy int64) error {
	settings := defaultChatSettings(chatID, updatedBy)
	settings.MentionEnabled = enabled
	return r.upsertChatSettings(&settings, map[string]interface{}{"mention_enabled": enabled})
}

// IsIgnored 报告 userID 是否在本群忽略名单中。
func (r *Repo) IsIgnored(chatID, userID int64) (bool, error) {
	var count int64
	err := r.db.Model(&models.ChatIgnoredUser{}).
		Where("chat_id = ? AND user_id = ?", chatID, userID).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("check ignored user: %w", err)
	}
	return count > 0, nil
}

// AddIgnoredUser 将用户加入本群忽略名单。返回 true 表示新增，false 表示已存在。
func (r *Repo) AddIgnoredUser(chatID, userID, createdBy int64) (bool, error) {
	ignored := models.ChatIgnoredUser{ChatID: chatID, UserID: userID, CreatedBy: createdBy}
	result := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&ignored)
	if result.Error != nil {
		return false, fmt.Errorf("add ignored user: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

// RemoveIgnoredUser 将用户移出本群忽略名单。返回 true 表示确实删除了记录。
func (r *Repo) RemoveIgnoredUser(chatID, userID int64) (bool, error) {
	result := r.db.Where("chat_id = ? AND user_id = ?", chatID, userID).
		Delete(&models.ChatIgnoredUser{})
	if result.Error != nil {
		return false, fmt.Errorf("remove ignored user: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

// ListIgnoredUsers 返回本群忽略名单，按添加时间排序。
func (r *Repo) ListIgnoredUsers(chatID int64) ([]models.ChatIgnoredUser, error) {
	var ignored []models.ChatIgnoredUser
	err := r.db.Where("chat_id = ?", chatID).
		Order("created_at ASC, id ASC").
		Find(&ignored).Error
	if err != nil {
		return nil, fmt.Errorf("list ignored users: %w", err)
	}
	return ignored, nil
}

// CountIgnoredUsers 返回本群忽略名单人数。
func (r *Repo) CountIgnoredUsers(chatID int64) (int64, error) {
	var count int64
	err := r.db.Model(&models.ChatIgnoredUser{}).
		Where("chat_id = ?", chatID).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count ignored users: %w", err)
	}
	return count, nil
}
