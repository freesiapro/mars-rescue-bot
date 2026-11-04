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

func (r *Repo) FindOrCreateRecord(chatID int64, key string, targetMsgID int) (*models.ForwardRecord, bool, error) {
	pending := true
	rec := models.ForwardRecord{
		ChatID:           chatID,
		ContentKey:       key,
		FirstTargetMsgID: targetMsgID,
		PendingPair:      &pending,
	}

	// INSERT ... ON CONFLICT DO NOTHING 依赖 (chat_id, content_key) 唯一索引，
	// 把“查不到再创建”合并为单条原子语句，避免并发下重复插入。
	result := r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "chat_id"}, {Name: "content_key"}},
		DoNothing: true,
	}).Create(&rec)
	if result.Error != nil {
		return nil, false, fmt.Errorf("create record: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		return &rec, true, nil
	}

	if err := r.db.Where("chat_id = ? AND content_key = ?", chatID, key).First(&rec).Error; err != nil {
		return nil, false, fmt.Errorf("find record: %w", err)
	}
	return &rec, false, nil
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
	// Seq 是本次出现之后的 forward_count，用于发送失败/被冷却抑制时的条件回滚。
	Seq int64
}

// DecideAndLog 原子地完成三件事：翻转 (chat, key) 的待配对状态、自增计数、写入本次出现的日志。
//
// 配对规则：第 1 次出现只记录并置为待配对；第 2 次与其配对并发提醒；
// 第 3 次重新开启新的一对但不提醒；第 4 次再配对提醒，依此类推。
// 无论是否发送提醒，每次出现都会自增 ForwardCount 并写入 ForwardLog，
// 因此排行榜统计与取消配对无关。
//
// 并发安全：分支 1 的 WHERE 条件只匹配“仍有待配对出现”的行，并用 RowsAffected
// 判断本次调用是否抢到了这一对；抢不到的并发调用会落到分支 2，成为新的待配对出现。
// 同一对因此最多只有一个调用者会去发送提醒。
//
// 历史数据兼容：升级前的行没有 pending_pair 列（AutoMigrate 后为 NULL）。
// 旧逻辑下第 N 次出现对应 ForwardCount=N，所以用计数奇偶推导待配对状态：
// 奇数表示还有一次未配对的出现，偶数表示上一次已经配过对。这样无需回填数据，
// 旧记录也能从正确的配对位置继续。
func (r *Repo) DecideAndLog(rec *models.ForwardRecord, targetMsgID int, senderID int64) (*PairDecision, error) {
	decision := &PairDecision{}

	err := r.db.Transaction(func(tx *gorm.DB) error {
		// 分支 1：当前存在待配对出现 -> 与本次出现配对，把 pending 翻转为 false。
		claim := tx.Model(&models.ForwardRecord{}).
			Where("id = ? AND (pending_pair = ? OR (pending_pair IS NULL AND forward_count % 2 = 1))", rec.ID, true).
			Updates(map[string]interface{}{
				"pending_pair":  false,
				"forward_count": gorm.Expr("forward_count + 1"),
			})
		if claim.Error != nil {
			return fmt.Errorf("claim pair: %w", claim.Error)
		}

		if claim.RowsAffected == 0 {
			// 分支 2：上一条出现已经配对过 -> 本次出现开启新的一对，仅记录不提醒。
			silent := tx.Model(&models.ForwardRecord{}).
				Where("id = ? AND (pending_pair = ? OR (pending_pair IS NULL AND forward_count % 2 = 0))", rec.ID, false).
				Updates(map[string]interface{}{
					"pending_pair":  true,
					"forward_count": gorm.Expr("forward_count + 1"),
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

		log := models.ForwardLog{RecordID: rec.ID, SenderID: senderID, TargetMsgID: targetMsgID}
		if err := tx.Create(&log).Error; err != nil {
			return fmt.Errorf("create log: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	rec.ForwardCount = int(decision.Seq)
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

func (r *Repo) UpsertUserStats(userID int64, username, firstName string) error {
	stats := models.UserStats{UserID: userID, Username: username, FirstName: firstName}

	updates := make(map[string]interface{})
	if username != "" {
		updates["username"] = username
	}
	if firstName != "" {
		updates["first_name"] = firstName
	}

	onConflict := clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
	}
	if len(updates) > 0 {
		updates["updated_at"] = time.Now()
		onConflict.DoUpdates = clause.Assignments(updates)
	} else {
		onConflict.DoNothing = true
	}

	if err := r.db.Clauses(onConflict).Create(&stats).Error; err != nil {
		return fmt.Errorf("upsert user stats: %w", err)
	}
	return nil
}

func (r *Repo) IncrementUserCount(userID int64) error {
	return r.db.Model(&models.UserStats{}).
		Where("user_id = ?", userID).
		UpdateColumn("rescue_count", gorm.Expr("rescue_count + 1")).Error
}

// GetTopRescuers 返回 since 之后、按“不同内容条数”统计的群排行榜。
// 同一用户重复发送同一内容只计一次，避免刷同一句话霸榜。
func (r *Repo) GetTopRescuers(chatID int64, since time.Time, limit int) ([]models.UserStats, error) {
	var stats []models.UserStats
	err := r.db.Raw(`
		SELECT u.user_id, u.username, u.first_name, COUNT(DISTINCT l.record_id) as rescue_count
		FROM forward_logs l
		JOIN forward_records r ON l.record_id = r.id
		JOIN user_stats u ON l.sender_id = u.user_id
		WHERE r.chat_id = ? AND l.created_at >= ?
		GROUP BY l.sender_id
		ORDER BY rescue_count DESC, l.sender_id ASC
		LIMIT ?
	`, chatID, since, limit).Scan(&stats).Error
	if err != nil {
		slog.Error("get top rescuers failed", "chat_id", chatID, "error", err)
		return nil, err
	}
	return stats, nil
}
