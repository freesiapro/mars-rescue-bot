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

func TestGetTopRescuersCountsEveryOccurrence(t *testing.T) {
	r := setupRepo(t)
	const chatID = int64(-100)
	now := time.Now()
	since := now.Add(-7 * 24 * time.Hour)

	for _, user := range []struct {
		id   int64
		name string
	}{{1, "alice"}, {2, "bob"}} {
		if err := r.UpsertUserStats(user.id, user.name, user.name); err != nil {
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
	rec2 := createRecord(chatID, "k2", 11)
	rec3 := createRecord(chatID, "k3", 12)
	oldRec := createRecord(chatID, "old", 13)
	otherChatRec := createRecord(-200, "other", 14)

	mustCreateLog := func(recID uint64, senderID int64, at time.Time) {
		t.Helper()
		if err := r.db.Create(&models.ForwardLog{
			RecordID:    recID,
			SenderID:    senderID,
			TargetMsgID: 1,
			CreatedAt:   at,
		}).Error; err != nil {
			t.Fatalf("create log: %v", err)
		}
	}

	// user 1：rec1 出现两次算 2，rec2 再算 1，共 3。
	mustCreateLog(rec1.ID, 1, now.Add(-time.Hour))
	mustCreateLog(rec1.ID, 1, now.Add(-2*time.Hour))
	mustCreateLog(rec2.ID, 1, now.Add(-time.Hour))
	// 周期外的记录不应计入。
	mustCreateLog(oldRec.ID, 1, now.Add(-10*24*time.Hour))
	// user 2：rec1、rec3 各算 1，共 2。
	mustCreateLog(rec1.ID, 2, now.Add(-time.Hour))
	mustCreateLog(rec3.ID, 2, now.Add(-time.Hour))
	// 其他群不应计入。
	mustCreateLog(otherChatRec.ID, 1, now.Add(-time.Hour))

	stats, err := r.GetTopRescuers(chatID, since, 10)
	if err != nil {
		t.Fatalf("GetTopRescuers() error = %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("stats = %+v, want 2 users", stats)
	}
	// 同分时按 user_id 升序。
	if stats[0].UserID != 1 || stats[0].RescueCount != 3 {
		t.Errorf("first = %+v, want user 1 with 3 occurrences", stats[0])
	}
	if stats[1].UserID != 2 || stats[1].RescueCount != 2 {
		t.Errorf("second = %+v, want user 2 with 2 occurrences", stats[1])
	}
}
