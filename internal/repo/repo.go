package repo

import (
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"mars-rescue-bot/internal/models"
)

type Repo struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Repo {
	return &Repo{db: db}
}

// messageUnixPtr 把 Telegram date 收成可空列。0 和负数表示未知，写成 NULL，
// 这样升级前的调用和新消息缺少 date 时都不会被当成 1970 年。
func messageUnixPtr(unix int64) *int64 {
	if unix <= 0 {
		return nil
	}
	v := unix
	return &v
}

// FindOrCreateRecord 返回 (chat, key) 对应的记录。
//
// 首次出现时会在同一事务内创建记录并写入第 1 条 ForwardLog。排行榜只统计
// 重复出现，查询时会排除这条首次流水；后续出现由 DecideAndLog 负责计数和写日志。
func (r *Repo) FindOrCreateRecord(chatID int64, key string, targetMsgID int, senderID, messageUnix int64) (*models.ForwardRecord, bool, error) {
	pending := true
	lastMsgID := targetMsgID
	rec := models.ForwardRecord{
		ChatID:              chatID,
		ContentKey:          key,
		FirstTargetMsgID:    targetMsgID,
		FirstSenderID:       senderID,
		PendingPair:         &pending,
		LastOccurrenceMsgID: &lastMsgID,
	}
	isNew := false

	err := r.db.Transaction(func(tx *gorm.DB) error {
		// INSERT ... ON CONFLICT DO NOTHING 依赖 (chat_id, content_key) 唯一索引，
		// 把“查不到再创建”合并为单条原子语句，避免并发下重复插入。
		result := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "chat_id"}, {Name: "content_key"}},
			DoNothing: true,
		}).Create(&rec)
		if result.Error != nil {
			return fmt.Errorf("create record: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			if err := tx.Where("chat_id = ? AND content_key = ?", chatID, key).First(&rec).Error; err != nil {
				return fmt.Errorf("find record: %w", err)
			}
			return nil
		}
		isNew = true

		log := models.ForwardLog{RecordID: rec.ID, SenderID: senderID, TargetMsgID: targetMsgID, MessageUnix: messageUnixPtr(messageUnix)}
		if err := tx.Create(&log).Error; err != nil {
			return fmt.Errorf("create first log: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}

	return &rec, isNew, nil
}

func (r *Repo) IncrementAndLog(rec *models.ForwardRecord, targetMsgID int, senderID int64) error {
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// 用数据库表达式自增，避免读-改-写丢失更新。
		result := tx.Model(&models.ForwardRecord{}).
			Where("id = ?", rec.ID).
			Update("forward_count", gorm.Expr("forward_count + 1"))
		if result.Error != nil {
			return fmt.Errorf("increment record: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("forward record %d not found", rec.ID)
		}

		log := models.ForwardLog{RecordID: rec.ID, SenderID: senderID, TargetMsgID: targetMsgID}
		if err := tx.Create(&log).Error; err != nil {
			return fmt.Errorf("create log: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	rec.ForwardCount++
	return nil
}

// PairDecision 是 DecideAndLog 对一次重复出现的配对判定。
type PairDecision struct {
	// Alert 为 true 表示本次出现与此前待配对的出现组成一对，应发送火星提醒。
	Alert bool
	// Adjacent 为 true 表示本次是同一波连续刷屏，只计数，不参与配对。
	Adjacent bool
	// Seq 是本次出现之后的 forward_count，用于发送失败/被冷却抑制时的条件回滚。
	Seq int64
}

// DecideAndLog 原子地完成三件事：更新配对状态、自增计数、写入本次出现的日志。
//
// 配对规则：第 1 次出现只记录并置为待配对；第 2 次与其配对并发提醒；
// 第 3 次重新开启新的一对但不提醒；第 4 次再配对提醒，依此类推。
// 无论是否发送提醒，每次出现都会自增 ForwardCount 并写入 ForwardLog；
// 排行榜只统计除首次之外的流水，因此与取消配对无关。
//
// 并发安全：分支 1 的 WHERE 条件只匹配“仍有待配对出现”的行，并用 RowsAffected
// 判断本次调用是否抢到了这一对；抢不到的并发调用会落到分支 2，成为新的待配对出现。
// 同一对因此最多只有一个调用者会去发送提醒。
//
// 历史数据兼容：升级前的行没有 pending_pair 列（AutoMigrate 后为 NULL）。
// 旧逻辑下第 N 次出现对应 ForwardCount=N，所以用计数奇偶推导待配对状态：
// 奇数表示还有一次未配对的出现，偶数表示上一次已经配过对。这样无需回填数据，
// 旧记录也能从正确的配对位置继续。
//
// 相邻刷屏：本次消息 ID 与上一次出现的消息 ID 相差不超过 adjacentGap 时，
// 视为同一波连续刷屏。此时只自增计数、写日志、更新 last_occurrence_msg_id，
// 不翻转 pending_pair，也不提醒，因此连续刷屏不会消耗配对位置。
// 旧行新增的 last_occurrence_msg_id 为 NULL，第一次出现一定走普通分支并显式
// 写回 pending_pair；相邻分支遇到 pending_pair 仍为 NULL 的异常行时，也只按
// ForwardCount 奇偶把它显式化，不改变原语义。
func (r *Repo) DecideAndLog(rec *models.ForwardRecord, targetMsgID int, senderID int64, adjacentGap int, messageUnix int64) (*PairDecision, error) {
	decision := &PairDecision{}

	err := r.db.Transaction(func(tx *gorm.DB) error {
		var current models.ForwardRecord
		if err := tx.Select("id", "forward_count", "pending_pair", "last_occurrence_msg_id").
			First(&current, rec.ID).Error; err != nil {
			return fmt.Errorf("read record: %w", err)
		}

		if adjacentGap > 0 && current.LastOccurrenceMsgID != nil {
			delta := targetMsgID - *current.LastOccurrenceMsgID
			if delta >= 0 && delta <= adjacentGap {
				updates := map[string]interface{}{
					"forward_count":          gorm.Expr("forward_count + 1"),
					"last_occurrence_msg_id": targetMsgID,
				}
				if current.PendingPair == nil {
					// 只把隐式状态显式化，不改变奇偶推导出的语义。
					pending := current.ForwardCount%2 == 1
					updates["pending_pair"] = pending
					rec.PendingPair = &pending
				} else {
					rec.PendingPair = current.PendingPair
				}

				result := tx.Model(&models.ForwardRecord{}).
					Where("id = ? AND last_occurrence_msg_id = ?", rec.ID, *current.LastOccurrenceMsgID).
					Updates(updates)
				if result.Error != nil {
					return fmt.Errorf("record adjacent occurrence: %w", result.Error)
				}
				if result.RowsAffected == 0 {
					return fmt.Errorf("adjacent state for record %d changed unexpectedly", rec.ID)
				}

				var row struct {
					ForwardCount int
				}
				if err := tx.Model(&models.ForwardRecord{}).
					Select("forward_count").
					Where("id = ?", rec.ID).
					Scan(&row).Error; err != nil {
					return fmt.Errorf("read forward count: %w", err)
				}
				decision.Seq = int64(row.ForwardCount)
				decision.Adjacent = true

				log := models.ForwardLog{RecordID: rec.ID, SenderID: senderID, TargetMsgID: targetMsgID, MessageUnix: messageUnixPtr(messageUnix)}
				if err := tx.Create(&log).Error; err != nil {
					return fmt.Errorf("create log: %w", err)
				}
				return nil
			}
		}

		// 分支 1：当前存在待配对出现 -> 与本次出现配对，把 pending 翻转为 false。
		claim := tx.Model(&models.ForwardRecord{}).
			Where("id = ? AND (pending_pair = ? OR (pending_pair IS NULL AND forward_count % 2 = 1))", rec.ID, true).
			Updates(map[string]interface{}{
				"pending_pair":           false,
				"forward_count":          gorm.Expr("forward_count + 1"),
				"last_occurrence_msg_id": targetMsgID,
			})
		if claim.Error != nil {
			return fmt.Errorf("claim pair: %w", claim.Error)
		}

		if claim.RowsAffected == 0 {
			// 分支 2：上一条出现已经配对过 -> 本次出现开启新的一对，仅记录不提醒。
			silent := tx.Model(&models.ForwardRecord{}).
				Where("id = ? AND (pending_pair = ? OR (pending_pair IS NULL AND forward_count % 2 = 0))", rec.ID, false).
				Updates(map[string]interface{}{
					"pending_pair":           true,
					"forward_count":          gorm.Expr("forward_count + 1"),
					"last_occurrence_msg_id": targetMsgID,
				})
			if silent.Error != nil {
				return fmt.Errorf("start new pair: %w", silent.Error)
			}
			if silent.RowsAffected == 0 {
				return fmt.Errorf("pair state for record %d changed unexpectedly", rec.ID)
			}
		} else {
			decision.Alert = true
		}

		var row struct {
			ForwardCount int
		}
		if err := tx.Model(&models.ForwardRecord{}).
			Select("forward_count").
			Where("id = ?", rec.ID).
			Scan(&row).Error; err != nil {
			return fmt.Errorf("read forward count: %w", err)
		}
		decision.Seq = int64(row.ForwardCount)

		log := models.ForwardLog{RecordID: rec.ID, SenderID: senderID, TargetMsgID: targetMsgID, MessageUnix: messageUnixPtr(messageUnix)}
		if err := tx.Create(&log).Error; err != nil {
			return fmt.Errorf("create log: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	rec.ForwardCount = int(decision.Seq)
	rec.LastOccurrenceMsgID = &targetMsgID
	if decision.Adjacent {
		return decision, nil
	}
	if decision.Alert {
		pending := false
		rec.PendingPair = &pending
	} else {
		pending := true
		rec.PendingPair = &pending
	}
	return decision, nil
}

// RevertPairClaim 在配对后的提醒没有真正发出（发送失败或被冷却抑制）时，
// 把该对的 pending 状态恢复为 true，使后续出现仍能重新触发提醒。
//
// 回滚是条件更新：只有本次出现的 forward_count 仍是最新值、且 pending 仍为 false
// 时才恢复。若期间已有后续出现推进了状态（例如它开启或完成了新的一对），
// 则不做任何修改，避免覆盖它的配对结果。
func (r *Repo) RevertPairClaim(rec *models.ForwardRecord, seq int64) error {
	result := r.db.Model(&models.ForwardRecord{}).
		Where("id = ? AND pending_pair = ? AND forward_count = ?", rec.ID, false, seq).
		Update("pending_pair", true)
	if result.Error != nil {
		return fmt.Errorf("revert pair claim: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		pending := true
		rec.PendingPair = &pending
	}
	return nil
}

func (r *Repo) CreateLogOnly(rec *models.ForwardRecord, targetMsgID int, senderID int64) error {
	if err := r.db.Create(&models.ForwardLog{RecordID: rec.ID, SenderID: senderID, TargetMsgID: targetMsgID}).Error; err != nil {
		return fmt.Errorf("create log: %w", err)
	}
	return nil
}

// HasSenderForwarded 报告 senderID 是否已经就 recordID 转发/发送过。
// 调用方据此判断是否为首次发送者。
func (r *Repo) HasSenderForwarded(recordID uint64, senderID int64) (bool, error) {
	var count int64
	err := r.db.Model(&models.ForwardLog{}).
		Where("record_id = ? AND sender_id = ?", recordID, senderID).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("check sender: %w", err)
	}
	return count > 0, nil
}

// UpsertUserStats 确保 user_stats 中存在该用户的 ID 行。
// 不写入 username/first_name：姓名只在进程内缓存，展示时实时解析。
func (r *Repo) UpsertUserStats(userID int64) error {
	stats := models.UserStats{UserID: userID}
	if err := r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoNothing: true,
	}).Create(&stats).Error; err != nil {
		return fmt.Errorf("upsert user stats: %w", err)
	}
	return nil
}

func (r *Repo) IncrementUserCount(userID int64) error {
	return r.db.Model(&models.UserStats{}).
		Where("user_id = ?", userID).
		UpdateColumn("rescue_count", gorm.Expr("rescue_count + 1")).Error
}

// GetTopRescuers 返回 since 之后、按“重复出现次数”统计的群排行榜。
// since 为零值时统计全部历史。同一条内容在同一群的第一次出现只记录、不计入；
// 之后每次出现都会写入 forward_logs，因此排除首次流水后 COUNT(*) 即为重复次数。
// 时间窗口用 unixepoch 比较绝对时间，避免把 STATS_TZ 的本地时间与数据库中的
// UTC 时间戳按字符串比较，在日界处错开若干小时。
// 不 JOIN user_stats：排行榜只依赖 sender_id，姓名由调用方在展示时解析。
func (r *Repo) GetTopRescuers(chatID int64, since time.Time, limit int) ([]models.UserStats, error) {
	var stats []models.UserStats
	query := `
		SELECT l.sender_id AS user_id, COUNT(*) as rescue_count
		FROM forward_logs l
		JOIN forward_records r ON l.record_id = r.id
		WHERE r.chat_id = ?
		  AND l.target_msg_id <> r.first_target_msg_id`
	args := []interface{}{chatID}
	if !since.IsZero() {
		query += " AND unixepoch(l.created_at) >= ?"
		args = append(args, since.Unix())
	}
	query += `
		GROUP BY l.sender_id
		ORDER BY rescue_count DESC, l.sender_id ASC
		LIMIT ?`
	args = append(args, limit)

	err := r.db.Raw(query, args...).Scan(&stats).Error
	if err != nil {
		slog.Error("get top rescuers failed", "chat_id", chatID, "error", err)
		return nil, err
	}
	return stats, nil
}

// CountUserOccurrences 统计某用户自 since 起在本群的重复出现次数；
// 同一条内容的第一次出现不计入。since 为零值时统计全部。
func (r *Repo) CountUserOccurrences(chatID, userID int64, since time.Time) (int64, error) {
	query := r.db.Table("forward_logs AS l").
		Joins("JOIN forward_records AS r ON l.record_id = r.id").
		Where("r.chat_id = ? AND l.sender_id = ? AND l.target_msg_id <> r.first_target_msg_id", chatID, userID)
	if !since.IsZero() {
		query = query.Where("unixepoch(l.created_at) >= ?", since.Unix())
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count user occurrences: %w", err)
	}
	return count, nil
}

// LastUserOccurrence 返回某用户在本群最近一次出现的时间。has=false 表示没有记录。
func (r *Repo) LastUserOccurrence(chatID, userID int64) (last time.Time, has bool, err error) {
	var row struct {
		CreatedAt time.Time
	}
	err = r.db.Table("forward_logs AS l").
		Select("l.created_at").
		Joins("JOIN forward_records AS r ON l.record_id = r.id").
		Where("r.chat_id = ? AND l.sender_id = ?", chatID, userID).
		Order("unixepoch(l.created_at) DESC, l.id DESC").
		Limit(1).
		Scan(&row).Error
	if err != nil {
		return time.Time{}, false, fmt.Errorf("last user occurrence: %w", err)
	}
	if row.CreatedAt.IsZero() {
		return time.Time{}, false, nil
	}
	return row.CreatedAt, true, nil
}
