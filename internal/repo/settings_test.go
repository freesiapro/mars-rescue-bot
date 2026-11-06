package repo

import (
	"testing"
	"time"

	"mars-rescue-bot/internal/models"
)

func TestGetChatSettingsDefaults(t *testing.T) {
	r := setupRepo(t)

	settings, err := r.GetChatSettings(-100)
	if err != nil {
		t.Fatalf("GetChatSettings() error = %v", err)
	}
	if !settings.Enabled {
		t.Error("Enabled = false, want true by default")
	}
	if settings.CooldownSeconds != DefaultCooldownSeconds {
		t.Errorf("CooldownSeconds = %d, want %d", settings.CooldownSeconds, DefaultCooldownSeconds)
	}
	if !settings.MentionEnabled {
		t.Error("MentionEnabled = false, want true by default")
	}

	var count int64
	if err := r.db.Model(&models.ChatSettings{}).Count(&count).Error; err != nil {
		t.Fatalf("count settings: %v", err)
	}
	if count != 0 {
		t.Errorf("settings rows = %d, want 0 (defaults must not be persisted)", count)
	}
}

func TestChatSettingsUpdates(t *testing.T) {
	r := setupRepo(t)
	const chatID = int64(-100)

	if err := r.SetChatCooldown(chatID, 30, 7); err != nil {
		t.Fatalf("SetChatCooldown() error = %v", err)
	}
	settings, err := r.GetChatSettings(chatID)
	if err != nil {
		t.Fatalf("GetChatSettings() error = %v", err)
	}
	if settings.CooldownSeconds != 30 || !settings.Enabled || !settings.MentionEnabled {
		t.Errorf("after cooldown update: %+v", settings)
	}
	if settings.UpdatedBy != 7 {
		t.Errorf("UpdatedBy = %d, want 7", settings.UpdatedBy)
	}

	if err := r.SetChatMention(chatID, false, 8); err != nil {
		t.Fatalf("SetChatMention() error = %v", err)
	}
	settings, _ = r.GetChatSettings(chatID)
	if settings.MentionEnabled {
		t.Error("MentionEnabled = true, want false")
	}
	if settings.CooldownSeconds != 30 {
		t.Errorf("CooldownSeconds = %d, want preserved 30", settings.CooldownSeconds)
	}
	if settings.UpdatedBy != 8 {
		t.Errorf("UpdatedBy = %d, want 8", settings.UpdatedBy)
	}

	if err := r.SetChatEnabled(chatID, false, 9); err != nil {
		t.Fatalf("SetChatEnabled() error = %v", err)
	}
	settings, _ = r.GetChatSettings(chatID)
	if settings.Enabled {
		t.Error("Enabled = true, want false")
	}
	if settings.CooldownSeconds != 30 || settings.MentionEnabled {
		t.Errorf("other settings not preserved: %+v", settings)
	}
}

func TestChatSettingsFalseValuesOnFirstWrite(t *testing.T) {
	r := setupRepo(t)

	if err := r.SetChatEnabled(-101, false, 1); err != nil {
		t.Fatalf("SetChatEnabled() error = %v", err)
	}
	settings, _ := r.GetChatSettings(-101)
	if settings.Enabled {
		t.Error("Enabled = true on first write, want false")
	}
	if !settings.MentionEnabled {
		t.Error("MentionEnabled should default to true")
	}

	if err := r.SetChatMention(-102, false, 1); err != nil {
		t.Fatalf("SetChatMention() error = %v", err)
	}
	settings, _ = r.GetChatSettings(-102)
	if settings.MentionEnabled {
		t.Error("MentionEnabled = true on first write, want false")
	}
	if !settings.Enabled {
		t.Error("Enabled should default to true")
	}
}

func TestChatIgnoredUsers(t *testing.T) {
	r := setupRepo(t)
	const chatID = int64(-100)

	ignored, err := r.IsIgnored(chatID, 7)
	if err != nil {
		t.Fatalf("IsIgnored() error = %v", err)
	}
	if ignored {
		t.Error("IsIgnored() = true for empty list")
	}

	added, err := r.AddIgnoredUser(chatID, 7, 99)
	if err != nil {
		t.Fatalf("AddIgnoredUser() error = %v", err)
	}
	if !added {
		t.Error("AddIgnoredUser() added = false, want true")
	}

	added, err = r.AddIgnoredUser(chatID, 7, 99)
	if err != nil {
		t.Fatalf("duplicate AddIgnoredUser() error = %v", err)
	}
	if added {
		t.Error("duplicate AddIgnoredUser() added = true, want false")
	}

	if _, err := r.AddIgnoredUser(chatID, 8, 99); err != nil {
		t.Fatalf("AddIgnoredUser(8) error = %v", err)
	}
	// 其他群不受影响。
	if _, err := r.AddIgnoredUser(-200, 7, 99); err != nil {
		t.Fatalf("AddIgnoredUser other chat error = %v", err)
	}

	ignored, err = r.IsIgnored(chatID, 7)
	if err != nil || !ignored {
		t.Fatalf("IsIgnored() = %v, %v, want true/nil", ignored, err)
	}
	ignored, err = r.IsIgnored(chatID, 9)
	if err != nil || ignored {
		t.Fatalf("IsIgnored(other user) = %v, %v, want false/nil", ignored, err)
	}

	count, err := r.CountIgnoredUsers(chatID)
	if err != nil {
		t.Fatalf("CountIgnoredUsers() error = %v", err)
	}
	if count != 2 {
		t.Errorf("CountIgnoredUsers() = %d, want 2", count)
	}

	list, err := r.ListIgnoredUsers(chatID)
	if err != nil {
		t.Fatalf("ListIgnoredUsers() error = %v", err)
	}
	if len(list) != 2 || list[0].UserID != 7 || list[1].UserID != 8 {
		t.Errorf("ListIgnoredUsers() = %+v, want [7 8]", list)
	}

	removed, err := r.RemoveIgnoredUser(chatID, 7)
	if err != nil {
		t.Fatalf("RemoveIgnoredUser() error = %v", err)
	}
	if !removed {
		t.Error("RemoveIgnoredUser() removed = false, want true")
	}
	removed, err = r.RemoveIgnoredUser(chatID, 7)
	if err != nil {
		t.Fatalf("second RemoveIgnoredUser() error = %v", err)
	}
	if removed {
		t.Error("second RemoveIgnoredUser() removed = true, want false")
	}

	count, _ = r.CountIgnoredUsers(chatID)
	if count != 1 {
		t.Errorf("CountIgnoredUsers() after remove = %d, want 1", count)
	}
}

func TestGetTopRescuersCountsRepeatOccurrences(t *testing.T) {
	r := setupRepo(t)
	const chatID = int64(-100)
	now := time.Now()
	since := now.Add(-7 * 24 * time.Hour)

	for _, userID := range []int64{1, 2} {
		if err := r.UpsertUserStats(userID); err != nil {
			t.Fatalf("UpsertUserStats() error = %v", err)
		}
	}

	createRecord := func(chat int64, key string, msgID int) *models.ForwardRecord {
		t.Helper()
		rec := &models.ForwardRecord{ChatID: chat, ContentKey: key, FirstTargetMsgID: msgID}
		if err := r.db.Create(rec).Error; err != nil {
			t.Fatalf("create record %q: %v", key, err)
		}
		return rec
	}

	rec1 := createRecord(chatID, "k1", 10)
	rec2 := createRecord(chatID, "k2", 20)
	rec3 := createRecord(chatID, "k3", 30)
	oldRec := createRecord(chatID, "old", 40)
	otherChatRec := createRecord(-200, "other", 50)

	mustCreateLog := func(recID uint64, senderID int64, targetMsgID int, at time.Time) {
		t.Helper()
		if err := r.db.Create(&models.ForwardLog{
			RecordID:    recID,
			SenderID:    senderID,
			TargetMsgID: targetMsgID,
			CreatedAt:   at,
		}).Error; err != nil {
			t.Fatalf("create log: %v", err)
		}
	}

	// user 1：rec1 首次出现不计，之后重复 2 次；rec2 首次出现不计，之后重复 1 次。
	mustCreateLog(rec1.ID, 1, 10, now.Add(-time.Hour))
	mustCreateLog(rec1.ID, 1, 11, now.Add(-time.Hour))
	mustCreateLog(rec1.ID, 1, 12, now.Add(-2*time.Hour))
	mustCreateLog(rec2.ID, 1, 20, now.Add(-time.Hour))
	mustCreateLog(rec2.ID, 1, 21, now.Add(-time.Hour))
	// 周期外的重复不应计入本周。
	mustCreateLog(oldRec.ID, 1, 40, now.Add(-10*24*time.Hour))
	mustCreateLog(oldRec.ID, 1, 41, now.Add(-10*24*time.Hour))
	// user 2：rec2 的重复 1 次。
	mustCreateLog(rec2.ID, 2, 22, now.Add(-time.Hour))
	// user 3 只有首次出现：不应上榜，也不需要 user_stats 行。
	mustCreateLog(rec3.ID, 3, 30, now.Add(-time.Hour))
	// 其他群的重复不应计入。
	mustCreateLog(otherChatRec.ID, 1, 50, now.Add(-time.Hour))
	mustCreateLog(otherChatRec.ID, 1, 51, now.Add(-time.Hour))

	stats, err := r.GetTopRescuers(chatID, since, 10)
	if err != nil {
		t.Fatalf("GetTopRescuers() error = %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("stats = %+v, want 2 users", stats)
	}
	if stats[0].UserID != 1 || stats[0].RescueCount != 3 {
		t.Errorf("first = %+v, want user 1 with 3 repeat occurrences", stats[0])
	}
	if stats[1].UserID != 2 || stats[1].RescueCount != 1 {
		t.Errorf("second = %+v, want user 2 with 1 repeat occurrence", stats[1])
	}
	for _, s := range stats {
		if s.Username != "" || s.FirstName != "" {
			t.Errorf("stats row %+v contains names; leaderboard must be ID-only", s)
		}
	}

	allStats, err := r.GetTopRescuers(chatID, time.Time{}, 10)
	if err != nil {
		t.Fatalf("GetTopRescuers(all) error = %v", err)
	}
	if len(allStats) != 2 || allStats[0].UserID != 1 || allStats[0].RescueCount != 4 {
		t.Errorf("all-time stats = %+v, want user 1 with 4 repeat occurrences", allStats)
	}
}

func TestCountUserOccurrencesAndLastOccurrence(t *testing.T) {
	r := setupRepo(t)
	const chatID = int64(-100)
	now := time.Now()

	rec := &models.ForwardRecord{ChatID: chatID, ContentKey: "k", FirstTargetMsgID: 1}
	if err := r.db.Create(rec).Error; err != nil {
		t.Fatalf("create record: %v", err)
	}
	otherChatRec := &models.ForwardRecord{ChatID: -200, ContentKey: "k", FirstTargetMsgID: 2}
	if err := r.db.Create(otherChatRec).Error; err != nil {
		t.Fatalf("create other chat record: %v", err)
	}

	for _, log := range []models.ForwardLog{
		// 首次出现：即使仍在统计窗口内也不计入火星次数。
		{RecordID: rec.ID, SenderID: 7, TargetMsgID: 1, CreatedAt: now.Add(-10 * 24 * time.Hour)},
		{RecordID: rec.ID, SenderID: 7, TargetMsgID: 2, CreatedAt: now.Add(-2 * time.Hour)},
		{RecordID: rec.ID, SenderID: 7, TargetMsgID: 3, CreatedAt: now.Add(-30 * time.Minute)},
		// 其他发送者的重复不计入 user 7。
		{RecordID: rec.ID, SenderID: 8, TargetMsgID: 4, CreatedAt: now.Add(-time.Hour)},
		// 其他群的记录不计入。
		{RecordID: otherChatRec.ID, SenderID: 7, TargetMsgID: 5, CreatedAt: now.Add(-time.Hour)},
	} {
		if err := r.db.Create(&log).Error; err != nil {
			t.Fatalf("create log: %v", err)
		}
	}

	weekCount, err := r.CountUserOccurrences(chatID, 7, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("CountUserOccurrences(week) error = %v", err)
	}
	if weekCount != 2 {
		t.Errorf("week count = %d, want 2", weekCount)
	}

	monthCount, err := r.CountUserOccurrences(chatID, 7, now.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatalf("CountUserOccurrences(month) error = %v", err)
	}
	if monthCount != 2 {
		t.Errorf("month count = %d, want 2 (first occurrence excluded)", monthCount)
	}

	totalCount, err := r.CountUserOccurrences(chatID, 7, time.Time{})
	if err != nil {
		t.Fatalf("CountUserOccurrences(all) error = %v", err)
	}
	if totalCount != 2 {
		t.Errorf("total count = %d, want 2 (first occurrence excluded)", totalCount)
	}

	last, has, err := r.LastUserOccurrence(chatID, 7)
	if err != nil {
		t.Fatalf("LastUserOccurrence() error = %v", err)
	}
	if !has {
		t.Fatal("LastUserOccurrence() has = false, want true")
	}
	if diff := now.Sub(last); diff < 29*time.Minute || diff > 31*time.Minute {
		t.Errorf("last = %v, want about 30 minutes ago", last)
	}

	noneCount, err := r.CountUserOccurrences(chatID, 9, time.Time{})
	if err != nil {
		t.Fatalf("CountUserOccurrences(none) error = %v", err)
	}
	if noneCount != 0 {
		t.Errorf("unknown user count = %d, want 0", noneCount)
	}
	_, has, err = r.LastUserOccurrence(chatID, 9)
	if err != nil {
		t.Fatalf("LastUserOccurrence(none) error = %v", err)
	}
	if has {
		t.Error("LastUserOccurrence(none) has = true, want false")
	}
}

func TestRankTimeWindowUsesAbsoluteTime(t *testing.T) {
	r := setupRepo(t)
	const chatID = int64(-100)
	const userID = int64(7)

	// STATS_TZ=Asia/Shanghai 的周一 00:00 等于 UTC 周日 16:00。
	loc := time.FixedZone("CST", 8*60*60)
	since := time.Date(2026, 11, 2, 0, 0, 0, 0, loc)

	rec := &models.ForwardRecord{ChatID: chatID, ContentKey: "tz", FirstTargetMsgID: 1}
	if err := r.db.Create(rec).Error; err != nil {
		t.Fatalf("create record: %v", err)
	}
	for _, log := range []models.ForwardLog{
		// 首次出现，不计入。
		{RecordID: rec.ID, SenderID: userID, TargetMsgID: 1, CreatedAt: time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC)},
		// 本地周一 00:30（UTC 周日 16:30）：在窗口内。
		{RecordID: rec.ID, SenderID: userID, TargetMsgID: 2, CreatedAt: time.Date(2026, 11, 1, 16, 30, 0, 0, time.UTC)},
		// 本地周日 23:30（UTC 周日 15:30）：在窗口外。
		{RecordID: rec.ID, SenderID: userID, TargetMsgID: 3, CreatedAt: time.Date(2026, 11, 1, 15, 30, 0, 0, time.UTC)},
	} {
		if err := r.db.Create(&log).Error; err != nil {
			t.Fatalf("create log: %v", err)
		}
	}

	count, err := r.CountUserOccurrences(chatID, userID, since)
	if err != nil {
		t.Fatalf("CountUserOccurrences() error = %v", err)
	}
	if count != 1 {
		t.Errorf("window count = %d, want 1", count)
	}

	stats, err := r.GetTopRescuers(chatID, since, 10)
	if err != nil {
		t.Fatalf("GetTopRescuers() error = %v", err)
	}
	if len(stats) != 1 || stats[0].UserID != userID || stats[0].RescueCount != 1 {
		t.Errorf("leaderboard = %+v, want user 7 with 1 repeat occurrence", stats)
	}
}
