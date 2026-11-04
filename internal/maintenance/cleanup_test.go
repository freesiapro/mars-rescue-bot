package maintenance

import (
	"context"
	"fmt"
	"testing"
	"time"

	"mars-rescue-bot/internal/models"
	"mars-rescue-bot/internal/testutil"
)

func TestRunOnceDeletesExpiredData(t *testing.T) {
	db := testutil.SetupDB(t,
		&models.ForwardRecord{},
		&models.ForwardLog{},
		&models.UserStats{},
	)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	oldRecord := now.Add(-200 * 24 * time.Hour)
	activeRecord := now.Add(-10 * 24 * time.Hour)
	oldLog := now.Add(-190 * 24 * time.Hour)
	recentLog := now.Add(-24 * time.Hour)

	c := New(db, 180)
	c.batchSize = 2

	createRecord := func(key string, updatedAt time.Time) *models.ForwardRecord {
		t.Helper()
		rec := &models.ForwardRecord{ChatID: -100, ContentKey: key, FirstTargetMsgID: 1}
		if err := db.Create(rec).Error; err != nil {
			t.Fatalf("create record %q: %v", key, err)
		}
		if err := db.Model(&models.ForwardRecord{}).Where("id = ?", rec.ID).
			UpdateColumn("updated_at", updatedAt).Error; err != nil {
			t.Fatalf("set updated_at for %q: %v", key, err)
		}
		return rec
	}
	createLog := func(recID uint64, senderID int64, createdAt time.Time) {
		t.Helper()
		log := &models.ForwardLog{RecordID: recID, SenderID: senderID, TargetMsgID: 1}
		if err := db.Create(log).Error; err != nil {
			t.Fatalf("create log: %v", err)
		}
		if err := db.Model(&models.ForwardLog{}).Where("id = ?", log.ID).
			UpdateColumn("created_at", createdAt).Error; err != nil {
			t.Fatalf("set log created_at: %v", err)
		}
	}

	// 5 条过期记录，每条一个过期流水；batchSize=2 会走多轮。
	for i := 0; i < 5; i++ {
		rec := createRecord(fmt.Sprintf("old-%d", i), oldRecord)
		createLog(rec.ID, int64(100+i), oldLog)
		if err := db.Create(&models.UserStats{UserID: int64(100 + i)}).Error; err != nil {
			t.Fatalf("create user stats: %v", err)
		}
	}

	// 活跃记录：更新时间在保留期内，因此记录保留；但其中的旧流水仍需删除。
	active := createRecord("active", activeRecord)
	createLog(active.ID, 200, oldLog)
	createLog(active.ID, 200, recentLog)
	if err := db.Create(&models.UserStats{UserID: 200}).Error; err != nil {
		t.Fatalf("create active user stats: %v", err)
	}

	// 从未产生流水、也不会被任何流水引用的 user_stats 行。
	if err := db.Create(&models.UserStats{UserID: 999}).Error; err != nil {
		t.Fatalf("create orphan user stats: %v", err)
	}

	stats, err := c.RunOnce(now)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if stats.Logs != 6 {
		t.Errorf("deleted logs = %d, want 6", stats.Logs)
	}
	if stats.Records != 5 {
		t.Errorf("deleted records = %d, want 5", stats.Records)
	}
	if stats.UserStats != 6 {
		t.Errorf("deleted user stats = %d, want 6", stats.UserStats)
	}

	var records, logs, userStats int64
	db.Model(&models.ForwardRecord{}).Count(&records)
	db.Model(&models.ForwardLog{}).Count(&logs)
	db.Model(&models.UserStats{}).Count(&userStats)
	if records != 1 || logs != 1 || userStats != 1 {
		t.Errorf("remaining rows = %d records / %d logs / %d stats, want 1/1/1", records, logs, userStats)
	}

	var remaining models.ForwardLog
	if err := db.First(&remaining).Error; err != nil {
		t.Fatalf("load remaining log: %v", err)
	}
	if remaining.RecordID != active.ID || remaining.SenderID != 200 {
		t.Errorf("remaining log = %+v, want active record log", remaining)
	}

	// 第二遍应无事可做。
	stats, err = c.RunOnce(now)
	if err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}
	if stats != (Stats{}) {
		t.Errorf("second run stats = %+v, want zero", stats)
	}
}

func TestRunOnceKeepsRowsInsideRetention(t *testing.T) {
	db := testutil.SetupDB(t, &models.ForwardRecord{}, &models.ForwardLog{}, &models.UserStats{})
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	rec := &models.ForwardRecord{ChatID: -100, ContentKey: "recent", FirstTargetMsgID: 1}
	if err := db.Create(rec).Error; err != nil {
		t.Fatalf("create record: %v", err)
	}
	if err := db.Model(&models.ForwardRecord{}).Where("id = ?", rec.ID).
		UpdateColumn("updated_at", now.Add(-179*24*time.Hour)).Error; err != nil {
		t.Fatalf("set updated_at: %v", err)
	}
	log := &models.ForwardLog{RecordID: rec.ID, SenderID: 7, TargetMsgID: 1}
	if err := db.Create(log).Error; err != nil {
		t.Fatalf("create log: %v", err)
	}
	if err := db.Model(&models.ForwardLog{}).Where("id = ?", log.ID).
		UpdateColumn("created_at", now.Add(-179*24*time.Hour)).Error; err != nil {
		t.Fatalf("set created_at: %v", err)
	}
	if err := db.Create(&models.UserStats{UserID: 7}).Error; err != nil {
		t.Fatalf("create user stats: %v", err)
	}

	stats, err := New(db, 180).RunOnce(now)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if stats != (Stats{}) {
		t.Errorf("stats = %+v, want zero", stats)
	}

	var records, logs, userStats int64
	db.Model(&models.ForwardRecord{}).Count(&records)
	db.Model(&models.ForwardLog{}).Count(&logs)
	db.Model(&models.UserStats{}).Count(&userStats)
	if records != 1 || logs != 1 || userStats != 1 {
		t.Errorf("remaining rows = %d/%d/%d, want 1/1/1", records, logs, userStats)
	}
}

func TestStartRunsImmediatelyAndStops(t *testing.T) {
	db := testutil.SetupDB(t, &models.ForwardRecord{}, &models.ForwardLog{}, &models.UserStats{})
	c := New(db, 180)

	ran := make(chan struct{}, 1)
	c.afterRun = func() {
		select {
		case ran <- struct{}{}:
		default:
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Start(ctx, time.Hour)
	}()

	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("Start() did not run cleanup immediately")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start() did not stop after context cancellation")
	}
}
