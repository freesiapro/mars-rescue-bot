package repo

import (
	"testing"

	"mars-rescue-bot/internal/models"
	"mars-rescue-bot/internal/testutil"
)

func setupRepo(t *testing.T) *Repo {
	t.Helper()
	db := testutil.SetupDB(t,
		&models.ForwardRecord{},
		&models.ForwardLog{},
		&models.UserStats{},
		&models.ChatSettings{},
		&models.ChatIgnoredUser{},
	)
	return New(db)
}

func TestFindOrCreateRecord(t *testing.T) {
	r := setupRepo(t)

	rec, isNew, err := r.FindOrCreateRecord(-100, "key", 10, 42, 0)
	if err != nil {
		t.Fatalf("first FindOrCreateRecord: %v", err)
	}
	if !isNew {
		t.Error("first call: isNew = false, want true")
	}
	if rec.ID == 0 {
		t.Error("first call: record ID not populated")
	}
	if rec.FirstSenderID != 42 {
		t.Errorf("first call: FirstSenderID = %d, want 42", rec.FirstSenderID)
	}

	var firstLogs int64
	if err := r.db.Model(&models.ForwardLog{}).Where("record_id = ?", rec.ID).Count(&firstLogs).Error; err != nil {
		t.Fatalf("count first logs: %v", err)
	}
	if firstLogs != 1 {
		t.Errorf("first logs = %d, want 1", firstLogs)
	}
	var firstLog models.ForwardLog
	if err := r.db.Where("record_id = ?", rec.ID).First(&firstLog).Error; err != nil {
		t.Fatalf("load first log: %v", err)
	}
	if firstLog.SenderID != 42 || firstLog.TargetMsgID != 10 {
		t.Errorf("first log = %+v, want sender 42 target 10", firstLog)
	}

	again, isNew, err := r.FindOrCreateRecord(-100, "key", 99, 7, 0)
	if err != nil {
		t.Fatalf("second FindOrCreateRecord: %v", err)
	}
	if isNew {
		t.Error("second call: isNew = true, want false")
	}
	if again.ID != rec.ID {
		t.Errorf("second call: ID = %d, want %d", again.ID, rec.ID)
	}
	if again.FirstTargetMsgID != 10 {
		t.Errorf("second call: FirstTargetMsgID = %d, want original 10", again.FirstTargetMsgID)
	}
	if again.FirstSenderID != 42 {
		t.Errorf("second call: FirstSenderID = %d, want original 42", again.FirstSenderID)
	}

	var logs int64
	if err := r.db.Model(&models.ForwardLog{}).Where("record_id = ?", rec.ID).Count(&logs).Error; err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if logs != 1 {
		t.Errorf("logs = %d, want 1 (second call must not log)", logs)
	}
}

func TestHasSenderForwardedAndIncrementAndLog(t *testing.T) {
	r := setupRepo(t)

	rec, _, err := r.FindOrCreateRecord(-100, "key", 10, 99, 0)
	if err != nil {
		t.Fatalf("FindOrCreateRecord: %v", err)
	}
	var before models.ForwardRecord
	if err := r.db.First(&before, rec.ID).Error; err != nil {
		t.Fatalf("load initial record: %v", err)
	}
	rec.ForwardCount = before.ForwardCount

	forwarded, err := r.HasSenderForwarded(rec.ID, 42)
	if err != nil {
		t.Fatalf("HasSenderForwarded before log: %v", err)
	}
	if forwarded {
		t.Error("HasSenderForwarded = true before any log, want false")
	}

	if err := r.IncrementAndLog(rec, 11, 42); err != nil {
		t.Fatalf("IncrementAndLog first: %v", err)
	}
	if rec.ForwardCount != before.ForwardCount+1 {
		t.Errorf("record.ForwardCount = %d after first increment, want %d", rec.ForwardCount, before.ForwardCount+1)
	}

	forwarded, err = r.HasSenderForwarded(rec.ID, 42)
	if err != nil {
		t.Fatalf("HasSenderForwarded after log: %v", err)
	}
	if !forwarded {
		t.Error("HasSenderForwarded = false after log, want true")
	}

	if err := r.IncrementAndLog(rec, 12, 42); err != nil {
		t.Fatalf("IncrementAndLog second: %v", err)
	}
	if rec.ForwardCount != before.ForwardCount+2 {
		t.Errorf("record.ForwardCount = %d after second increment, want %d", rec.ForwardCount, before.ForwardCount+2)
	}

	var stored models.ForwardRecord
	if err := r.db.First(&stored, rec.ID).Error; err != nil {
		t.Fatalf("reload record: %v", err)
	}
	if stored.ForwardCount != before.ForwardCount+2 {
		t.Errorf("stored ForwardCount = %d, want %d", stored.ForwardCount, before.ForwardCount+2)
	}

	var logCount int64
	if err := r.db.Model(&models.ForwardLog{}).Where("record_id = ? AND sender_id = ?", rec.ID, 42).Count(&logCount).Error; err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if logCount != 2 {
		t.Errorf("sender 42 log count = %d, want 2", logCount)
	}

	var totalLogs int64
	if err := r.db.Model(&models.ForwardLog{}).Where("record_id = ?", rec.ID).Count(&totalLogs).Error; err != nil {
		t.Fatalf("count total logs: %v", err)
	}
	if totalLogs != 3 {
		t.Errorf("total log count = %d, want 3 (first occurrence + two increments)", totalLogs)
	}
}

func TestUpsertUserStatsStoresOnlyUserID(t *testing.T) {
	r := setupRepo(t)

	if err := r.UpsertUserStats(7); err != nil {
		t.Fatalf("insert user stats: %v", err)
	}
	if err := r.UpsertUserStats(7); err != nil {
		t.Fatalf("duplicate user stats: %v", err)
	}

	var stats models.UserStats
	if err := r.db.First(&stats, "user_id = ?", 7).Error; err != nil {
		t.Fatalf("load inserted stats: %v", err)
	}
	if stats.UserID != 7 {
		t.Errorf("UserID = %d, want 7", stats.UserID)
	}
	if stats.Username != "" || stats.FirstName != "" {
		t.Errorf("stored names = %q/%q, want empty", stats.Username, stats.FirstName)
	}

	var count int64
	if err := r.db.Model(&models.UserStats{}).Count(&count).Error; err != nil {
		t.Fatalf("count user stats: %v", err)
	}
	if count != 1 {
		t.Errorf("user_stats rows = %d, want 1", count)
	}
}
