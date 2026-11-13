package bot

import (
	"testing"
	"time"

	"mars-rescue-bot/internal/models"
)

func processAdjacentText(t *testing.T, p *Processor, rec *replyRecorder, text string, msgID int, userID int64) {
	t.Helper()
	if err := p.ProcessMessage(textMessage(testChatID, userID, "A", text, msgID), "trace", rec.reply); err != nil {
		t.Fatalf("ProcessMessage(msg %d) error = %v", msgID, err)
	}
}

func TestAdjacentRepeatGapBoundary(t *testing.T) {
	t.Run("gap 5 is exempt", func(t *testing.T) {
		p, _, db := setupProcessor(t)
		p.adjacentRepeatGap = AdjacentRepeatGap
		rec := &replyRecorder{}

		processAdjacentText(t, p, rec, "adjacent text still counts now", 10, 1)
		processAdjacentText(t, p, rec, "adjacent text still counts now", 15, 2)

		if len(rec.messages) != 0 {
			t.Errorf("replies = %#v, want none for gap 5", rec.messages)
		}
		var record models.ForwardRecord
		if err := db.First(&record).Error; err != nil {
			t.Fatalf("load record: %v", err)
		}
		if record.ForwardCount != 2 {
			t.Errorf("ForwardCount = %d, want 2", record.ForwardCount)
		}
		if record.LastOccurrenceMsgID == nil || *record.LastOccurrenceMsgID != 15 {
			t.Errorf("LastOccurrenceMsgID = %v, want 15", record.LastOccurrenceMsgID)
		}
		if record.PendingPair == nil || !*record.PendingPair {
			t.Errorf("PendingPair = %v, want true (adjacent repeat must not flip pairing)", record.PendingPair)
		}
	})

	t.Run("gap 6 is normal pairing", func(t *testing.T) {
		p, _, db := setupProcessor(t)
		p.adjacentRepeatGap = AdjacentRepeatGap
		rec := &replyRecorder{}

		processAdjacentText(t, p, rec, "adjacent text still counts now", 10, 1)
		processAdjacentText(t, p, rec, "adjacent text still counts now", 16, 2)

		if len(rec.messages) != 1 {
			t.Fatalf("replies = %#v, want 1 for gap 6", rec.messages)
		}
		var record models.ForwardRecord
		if err := db.First(&record).Error; err != nil {
			t.Fatalf("load record: %v", err)
		}
		if record.ForwardCount != 2 {
			t.Errorf("ForwardCount = %d, want 2", record.ForwardCount)
		}
		if record.LastOccurrenceMsgID == nil || *record.LastOccurrenceMsgID != 16 {
			t.Errorf("LastOccurrenceMsgID = %v, want 16", record.LastOccurrenceMsgID)
		}
		if record.PendingPair == nil || *record.PendingPair {
			t.Errorf("PendingPair = %v, want false after alert", record.PendingPair)
		}
	})
}

func TestAdjacentRepeatLegacyNullLastMessageIDIsNotExempt(t *testing.T) {
	p, _, db := setupProcessor(t)
	p.adjacentRepeatGap = AdjacentRepeatGap
	rec := &replyRecorder{}

	key := textKeyFor(p.dedupSecret, "legacy adjacent message content here")
	legacy := models.ForwardRecord{
		ChatID:           testChatID,
		ContentKey:       key,
		FirstTargetMsgID: 10,
		FirstSenderID:    1,
		ForwardCount:     1,
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("create legacy record: %v", err)
	}
	processAdjacentText(t, p, rec, "legacy adjacent message content here", 11, 2)

	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1 (NULL last_message_id is not exempt)", rec.messages)
	}
	var record models.ForwardRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if record.LastOccurrenceMsgID == nil || *record.LastOccurrenceMsgID != 11 {
		t.Errorf("LastOccurrenceMsgID = %v, want 11 after normal occurrence", record.LastOccurrenceMsgID)
	}
	if record.PendingPair == nil || *record.PendingPair {
		t.Errorf("PendingPair = %v, want false after legacy parity alert", record.PendingPair)
	}
}

func TestAdjacentRepeatChainCountsForLeaderboardWithoutAlerts(t *testing.T) {
	p, r, db := setupProcessor(t)
	p.adjacentRepeatGap = AdjacentRepeatGap
	rec := &replyRecorder{}

	for i := 0; i < 10; i++ {
		processAdjacentText(t, p, rec, "chain text go on and on", 10+i, 1)
	}

	if len(rec.messages) != 0 {
		t.Errorf("replies = %#v, want none for a 10-message chain", rec.messages)
	}
	var record models.ForwardRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if record.ForwardCount != 10 {
		t.Errorf("ForwardCount = %d, want 10", record.ForwardCount)
	}
	if record.LastOccurrenceMsgID == nil || *record.LastOccurrenceMsgID != 19 {
		t.Errorf("LastOccurrenceMsgID = %v, want 19", record.LastOccurrenceMsgID)
	}
	if record.PendingPair == nil || !*record.PendingPair {
		t.Errorf("PendingPair = %v, want true", record.PendingPair)
	}

	stats, err := r.GetTopRescuers(testChatID, time.Time{}, 10)
	if err != nil {
		t.Fatalf("GetTopRescuers() error = %v", err)
	}
	// 10 次出现里第 1 次是首次记录，排行榜只计后续 9 次重复出现。
	if len(stats) != 1 || stats[0].UserID != 1 || stats[0].RescueCount != 9 {
		t.Errorf("leaderboard = %#v, want user 1 with 9 repeat occurrences", stats)
	}
}

func TestAdjacentRepeatChainDoesNotFlipPairing(t *testing.T) {
	p, _, db := setupProcessor(t)
	p.adjacentRepeatGap = AdjacentRepeatGap
	rec := &replyRecorder{}

	for i := 0; i < 10; i++ {
		processAdjacentText(t, p, rec, "chain parity content stays here", 10+i, 1)
	}
	processAdjacentText(t, p, rec, "chain parity content stays here", 100, 2)
	processAdjacentText(t, p, rec, "chain parity content stays here", 200, 2)
	processAdjacentText(t, p, rec, "chain parity content stays here", 300, 2)

	if len(rec.messages) != 2 {
		t.Fatalf("replies = %#v, want alerts after the chain at msg 100 and 300", rec.messages)
	}
	var record models.ForwardRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if record.ForwardCount != 13 {
		t.Errorf("ForwardCount = %d, want 13", record.ForwardCount)
	}
	if record.LastOccurrenceMsgID == nil || *record.LastOccurrenceMsgID != 300 {
		t.Errorf("LastOccurrenceMsgID = %v, want 300", record.LastOccurrenceMsgID)
	}
	if record.PendingPair == nil || *record.PendingPair {
		t.Errorf("PendingPair = %v, want false after the second post-chain alert", record.PendingPair)
	}
}
