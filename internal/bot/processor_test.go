package bot

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/telebot.v3"
	"gorm.io/gorm"
	"mars-rescue-bot/internal/models"
	"mars-rescue-bot/internal/names"
	"mars-rescue-bot/internal/repo"
	"mars-rescue-bot/internal/testutil"
	"mars-rescue-bot/internal/textnorm"
)

const testChatID = int64(-1001234567890)

func setupProcessor(t *testing.T) (*Processor, *repo.Repo, *gorm.DB) {
	t.Helper()
	db := testutil.SetupDB(t,
		&models.ForwardRecord{},
		&models.ForwardLog{},
		&models.UserStats{},
		&models.ChatSettings{},
		&models.ChatIgnoredUser{},
	)
	r := repo.New(db)
	p := NewProcessor(r, "test-secret", names.New(nil), time.UTC)
	p.sleep = func(time.Duration) {}
	return p, r, db
}

func textMessage(chatID, userID int64, firstName, text string, msgID int) *telebot.Message {
	return &telebot.Message{
		ID:     msgID,
		Text:   text,
		Sender: &telebot.User{ID: userID, Username: "user" + text, FirstName: firstName},
		Chat:   &telebot.Chat{ID: chatID, Type: telebot.ChatSuperGroup},
	}
}

// textKeyFor 按处理器相同的步骤生成内容键，供构造历史数据的测试使用。
func textKeyFor(secret, text string) string {
	return textContentKey(secret, textnorm.Normalize(text))
}

type replyRecorder struct {
	mu       sync.Mutex
	messages []string
}

func (r *replyRecorder) reply(text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, text)
	return nil
}

func (r *replyRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.messages...)
}

func TestTextKeyUsesHMACAndNoPlaintext(t *testing.T) {
	key := textContentKey("secret", "hello mars")
	if !strings.HasPrefix(key, "text:v2:") {
		t.Errorf("key = %q, want text:v2: prefix", key)
	}
	if strings.Contains(key, "hello") || strings.Contains(key, "mars") {
		t.Errorf("key = %q, must not contain plaintext", key)
	}

	other := textContentKey("other-secret", "hello mars")
	if key == other {
		t.Error("different secrets should produce different keys")
	}

	again := textContentKey("secret", "hello mars")
	if key != again {
		t.Error("same secret and text should be deterministic")
	}
}

func TestMessageLink(t *testing.T) {
	tests := []struct {
		name      string
		chat      *telebot.Chat
		messageID int
		want      string
		wantOK    bool
	}{
		{
			name:      "public supergroup",
			chat:      &telebot.Chat{ID: -1001234567890, Type: telebot.ChatSuperGroup, Username: "public_group"},
			messageID: 42,
			want:      "https://t.me/public_group/42",
			wantOK:    true,
		},
		{
			name:      "private supergroup",
			chat:      &telebot.Chat{ID: -1001234567890, Type: telebot.ChatSuperGroup},
			messageID: 42,
			want:      "https://t.me/c/1234567890/42",
			wantOK:    true,
		},
		{
			name:      "basic group has no public link",
			chat:      &telebot.Chat{ID: -123456, Type: telebot.ChatGroup},
			messageID: 42,
			wantOK:    false,
		},
		{
			name:      "invalid message id",
			chat:      &telebot.Chat{ID: -1001234567890, Type: telebot.ChatSuperGroup},
			messageID: 0,
			wantOK:    false,
		},
		{
			name:      "nil chat",
			messageID: 42,
			wantOK:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := messageLink(tt.chat, tt.messageID)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("messageLink() = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestProcessorTextReplyUsesPublicGroupLink(t *testing.T) {
	p, _, _ := setupProcessor(t)
	rec := &replyRecorder{}
	chat := &telebot.Chat{ID: testChatID, Type: telebot.ChatSuperGroup, Username: "public_group"}

	if err := p.ProcessMessage(&telebot.Message{
		ID: 10, Text: "hello mars", Sender: &telebot.User{ID: 1, FirstName: "A"}, Chat: chat,
	}, "trace", rec.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}
	if err := p.ProcessMessage(&telebot.Message{
		ID: 11, Text: "hello mars", Sender: &telebot.User{ID: 2, FirstName: "B"}, Chat: chat,
	}, "trace", rec.reply); err != nil {
		t.Fatalf("second ProcessMessage() error = %v", err)
	}

	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1", rec.messages)
	}
	if !strings.Contains(rec.messages[0], "https://t.me/public_group/10") {
		t.Errorf("reply = %q, want public group link", rec.messages[0])
	}
}

func TestProcessorReplyIncludesFirstSender(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	if err := p.ProcessMessage(textMessage(testChatID, 7, "Alice", "hello mars", 10), "trace", rec.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}
	if err := p.ProcessMessage(textMessage(testChatID, 8, "Bob", "hello mars", 11), "trace", rec.reply); err != nil {
		t.Fatalf("second ProcessMessage() error = %v", err)
	}

	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1", rec.messages)
	}
	reply := rec.messages[0]
	for _, want := range []string{"最早由 Alice 于", "这是第 2 次出现", "回到首发现场"} {
		if !strings.Contains(reply, want) {
			t.Errorf("reply = %q, want containing %q", reply, want)
		}
	}

	var record models.ForwardRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if record.FirstSenderID != 7 {
		t.Errorf("FirstSenderID = %d, want 7", record.FirstSenderID)
	}
}

func TestProcessorLegacyRecordShowsUnknownFirstSender(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	// 旧记录：没有 first_sender_id，计数为 1（奇数）表示还有一次未配对的出现。
	if err := db.Create(&models.ForwardRecord{
		ChatID:           testChatID,
		ContentKey:       textKeyFor("test-secret", "legacy sender"),
		ForwardCount:     1,
		FirstTargetMsgID: 5,
	}).Error; err != nil {
		t.Fatalf("create legacy record: %v", err)
	}

	if err := p.ProcessMessage(textMessage(testChatID, 8, "Bob", "legacy sender", 11), "trace", rec.reply); err != nil {
		t.Fatalf("ProcessMessage() error = %v", err)
	}
	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1", rec.messages)
	}
	if !strings.Contains(rec.messages[0], "首发者未知") {
		t.Errorf("reply = %q, want unknown first sender", rec.messages[0])
	}
}

func TestProcessorFirstMessageNoReply(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	if err := p.ProcessMessage(textMessage(testChatID, 1, "A", "hello mars", 10), "trace", rec.reply); err != nil {
		t.Fatalf("ProcessMessage() error = %v", err)
	}
	if len(rec.messages) != 0 {
		t.Fatalf("replies = %#v, want none for first occurrence", rec.messages)
	}

	var count int64
	if err := db.Model(&models.ForwardRecord{}).Count(&count).Error; err != nil {
		t.Fatalf("count records: %v", err)
	}
	if count != 1 {
		t.Errorf("record count = %d, want 1", count)
	}

	var logs int64
	if err := db.Model(&models.ForwardLog{}).Count(&logs).Error; err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if logs != 1 {
		t.Errorf("log count = %d, want 1 for the first occurrence", logs)
	}
}

// 决策 1：自己重复发同一内容也算火星并提醒。
func TestProcessorSelfRepeatRepliesWithMention(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}
	msg1 := textMessage(testChatID, 7, "<A&B>", "hello mars", 10)
	msg2 := textMessage(testChatID, 7, "<A&B>", "hello mars", 11)

	if err := p.ProcessMessage(msg1, "trace", rec.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}
	if err := p.ProcessMessage(msg2, "trace", rec.reply); err != nil {
		t.Fatalf("second ProcessMessage() error = %v", err)
	}

	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1", rec.messages)
	}
	reply := rec.messages[0]
	for _, want := range []string{"火星救援", "tg://user?id=7", "&lt;A&amp;B&gt;", "这是第 2 次出现", "最早由"} {
		if !strings.Contains(reply, want) {
			t.Errorf("reply = %q, want containing %q", reply, want)
		}
	}
	if strings.Contains(reply, "<A&B>") {
		t.Errorf("reply contains unescaped name: %q", reply)
	}

	var record models.ForwardRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if !strings.HasPrefix(record.ContentKey, "text:v2:") {
		t.Errorf("ContentKey = %q, want text:v2: prefix", record.ContentKey)
	}
	if strings.Contains(record.ContentKey, "hello mars") {
		t.Errorf("ContentKey = %q, must not contain plaintext", record.ContentKey)
	}
	if record.ForwardCount != 2 {
		t.Errorf("ForwardCount = %d, want 2", record.ForwardCount)
	}

	var logs int64
	if err := db.Model(&models.ForwardLog{}).Count(&logs).Error; err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if logs != 2 {
		t.Errorf("log count = %d, want 2 (first occurrence + duplicate)", logs)
	}
}

func TestProcessorDisabledChatDoesNotRecordOrReply(t *testing.T) {
	p, r, db := setupProcessor(t)
	rec := &replyRecorder{}
	if err := r.SetChatEnabled(testChatID, false, 1); err != nil {
		t.Fatalf("SetChatEnabled() error = %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := p.ProcessMessage(textMessage(testChatID, 1, "A", "hello mars", 10+i), "trace", rec.reply); err != nil {
			t.Fatalf("ProcessMessage() error = %v", err)
		}
	}

	if len(rec.messages) != 0 {
		t.Errorf("replies = %#v, want none", rec.messages)
	}
	var records, logs int64
	db.Model(&models.ForwardRecord{}).Count(&records)
	db.Model(&models.ForwardLog{}).Count(&logs)
	if records != 0 || logs != 0 {
		t.Errorf("records=%d logs=%d, want 0/0", records, logs)
	}
}

func TestProcessorIgnoredUserDoesNotRecordOrReply(t *testing.T) {
	p, r, db := setupProcessor(t)
	rec := &replyRecorder{}
	if _, err := r.AddIgnoredUser(testChatID, 1, 99); err != nil {
		t.Fatalf("AddIgnoredUser() error = %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := p.ProcessMessage(textMessage(testChatID, 1, "A", "hello mars", 10+i), "trace", rec.reply); err != nil {
			t.Fatalf("ProcessMessage() error = %v", err)
		}
	}

	if len(rec.messages) != 0 {
		t.Errorf("replies = %#v, want none", rec.messages)
	}
	var records, logs int64
	db.Model(&models.ForwardRecord{}).Count(&records)
	db.Model(&models.ForwardLog{}).Count(&logs)
	if records != 0 || logs != 0 {
		t.Errorf("records=%d logs=%d, want 0/0", records, logs)
	}
}

func TestProcessorCooldownSuppressesReplyButStillRecords(t *testing.T) {
	p, r, db := setupProcessor(t)
	rec := &replyRecorder{}
	if err := r.SetChatCooldown(testChatID, 60, 1); err != nil {
		t.Fatalf("SetChatCooldown() error = %v", err)
	}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	p.cooldown.now = func() time.Time { return now }

	messages := []*telebot.Message{
		textMessage(testChatID, 1, "A", "hello mars", 10), // first: record only
		textMessage(testChatID, 2, "B", "hello mars", 11), // duplicate: reply
		textMessage(testChatID, 3, "C", "hello mars", 12), // duplicate within cooldown: no reply
	}
	for _, msg := range messages {
		if err := p.ProcessMessage(msg, "trace", rec.reply); err != nil {
			t.Fatalf("ProcessMessage() error = %v", err)
		}
	}
	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1 before cooldown expires", rec.messages)
	}

	now = now.Add(61 * time.Second)
	if err := p.ProcessMessage(textMessage(testChatID, 4, "D", "hello mars", 13), "trace", rec.reply); err != nil {
		t.Fatalf("ProcessMessage() error = %v", err)
	}
	if len(rec.messages) != 2 {
		t.Fatalf("replies = %#v, want 2 after cooldown expires", rec.messages)
	}

	var logs int64
	if err := db.Model(&models.ForwardLog{}).Count(&logs).Error; err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if logs != 4 {
		t.Errorf("log count = %d, want 4 (every occurrence is recorded)", logs)
	}
}

func TestProcessorMentionOff(t *testing.T) {
	p, r, _ := setupProcessor(t)
	rec := &replyRecorder{}
	if err := r.SetChatMention(testChatID, false, 1); err != nil {
		t.Fatalf("SetChatMention() error = %v", err)
	}

	msg1 := textMessage(testChatID, 7, "<A>", "hello mars", 10)
	msg2 := textMessage(testChatID, 7, "<A>", "hello mars", 11)
	if err := p.ProcessMessage(msg1, "trace", rec.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}
	if err := p.ProcessMessage(msg2, "trace", rec.reply); err != nil {
		t.Fatalf("second ProcessMessage() error = %v", err)
	}

	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1", rec.messages)
	}
	reply := rec.messages[0]
	if strings.Contains(reply, "tg://user") {
		t.Errorf("reply should not mention when disabled: %q", reply)
	}
	if !strings.Contains(reply, "&lt;A&gt;") {
		t.Errorf("reply should contain escaped display name: %q", reply)
	}
}

func TestProcessorSkipsCommands(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	for i := 0; i < 2; i++ {
		if err := p.ProcessMessage(textMessage(testChatID, 1, "A", "/mars_status", 10+i), "trace", rec.reply); err != nil {
			t.Fatalf("ProcessMessage() error = %v", err)
		}
	}
	var records int64
	db.Model(&models.ForwardRecord{}).Count(&records)
	if records != 0 {
		t.Errorf("records = %d, want 0 for command text", records)
	}
	if len(rec.messages) != 0 {
		t.Errorf("replies = %#v, want none", rec.messages)
	}
}

func TestProcessorChannelForwardDuplicate(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	newForward := func(msgID int) *telebot.Message {
		return &telebot.Message{
			ID:                msgID,
			Sender:            &telebot.User{ID: 7, FirstName: "A"},
			Chat:              &telebot.Chat{ID: testChatID, Type: telebot.ChatSuperGroup},
			OriginalChat:      &telebot.Chat{ID: -100999, Type: telebot.ChatChannel},
			OriginalMessageID: 42,
		}
	}

	if err := p.ProcessMessage(newForward(10), "trace", rec.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}
	if err := p.ProcessMessage(newForward(11), "trace", rec.reply); err != nil {
		t.Fatalf("second ProcessMessage() error = %v", err)
	}

	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1", rec.messages)
	}
	if !strings.Contains(rec.messages[0], "这条消息") {
		t.Errorf("reply = %q, want channel-forward wording", rec.messages[0])
	}

	var record models.ForwardRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if record.ContentKey != "channel:-100999:42" {
		t.Errorf("ContentKey = %q, want channel:-100999:42", record.ContentKey)
	}
}

func TestProcessorKeepsChatsSeparate(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	if err := p.ProcessMessage(textMessage(-1001, 1, "A", "same text", 10), "trace", rec.reply); err != nil {
		t.Fatalf("chat 1 ProcessMessage() error = %v", err)
	}
	if err := p.ProcessMessage(textMessage(-1002, 1, "A", "same text", 11), "trace", rec.reply); err != nil {
		t.Fatalf("chat 2 ProcessMessage() error = %v", err)
	}
	if len(rec.messages) != 0 {
		t.Errorf("replies = %#v, want none (first occurrence per chat)", rec.messages)
	}

	var records int64
	db.Model(&models.ForwardRecord{}).Count(&records)
	if records != 2 {
		t.Errorf("records = %d, want 2 (one per chat)", records)
	}
}

// 配对规则：第 2、4、6… 次出现发送提醒，同时所有出现都照常计数和记录。
func TestProcessorPairingAlertsEverySecondOccurrence(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	for i := 0; i < 5; i++ {
		msg := textMessage(testChatID, int64(i+1), "A", "pair me", 10+i)
		if err := p.ProcessMessage(msg, "trace", rec.reply); err != nil {
			t.Fatalf("occurrence %d ProcessMessage() error = %v", i+1, err)
		}

		wantAlerts := (i + 1) / 2 // 第 2、4 次提醒
		if len(rec.messages) != wantAlerts {
			t.Fatalf("after occurrence %d replies = %#v, want %d", i+1, rec.messages, wantAlerts)
		}

		var record models.ForwardRecord
		if err := db.First(&record).Error; err != nil {
			t.Fatalf("occurrence %d load record: %v", i+1, err)
		}
		if record.ForwardCount != i+1 {
			t.Errorf("after occurrence %d ForwardCount = %d, want %d", i+1, record.ForwardCount, i+1)
		}

		var logs int64
		if err := db.Model(&models.ForwardLog{}).Count(&logs).Error; err != nil {
			t.Fatalf("occurrence %d count logs: %v", i+1, err)
		}
		// 每次出现都写日志：第 1 次出现也计入排行榜。
		if logs != int64(i+1) {
			t.Errorf("after occurrence %d logs = %d, want %d", i+1, logs, i+1)
		}
	}

	if !strings.Contains(rec.messages[0], "这是第 2 次出现") {
		t.Errorf("first alert = %q, want count 2", rec.messages[0])
	}
	if !strings.Contains(rec.messages[1], "这是第 4 次出现") {
		t.Errorf("second alert = %q, want count 4", rec.messages[1])
	}
}

func TestProcessorPairingIndependentPerContentAndChat(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	msgs := []*telebot.Message{
		textMessage(-1001, 1, "A", "alpha", 10), // chat1 alpha: 第 1 次，静默
		textMessage(-1002, 1, "A", "alpha", 11), // chat2 alpha: 第 1 次，静默
		textMessage(-1001, 1, "A", "beta", 12),  // chat1 beta:  第 1 次，静默
		textMessage(-1001, 1, "A", "alpha", 13), // chat1 alpha: 第 2 次，提醒
		textMessage(-1002, 1, "A", "alpha", 14), // chat2 alpha: 第 2 次，提醒
		textMessage(-1001, 1, "A", "beta", 15),  // chat1 beta:  第 2 次，提醒
	}
	for i, msg := range msgs {
		if err := p.ProcessMessage(msg, "trace", rec.reply); err != nil {
			t.Fatalf("message %d ProcessMessage() error = %v", i, err)
		}
	}

	if len(rec.messages) != 3 {
		t.Errorf("replies = %#v, want 3 (one per content/chat pair)", rec.messages)
	}

	var records int64
	if err := db.Model(&models.ForwardRecord{}).Count(&records).Error; err != nil {
		t.Fatalf("count records: %v", err)
	}
	if records != 3 {
		t.Errorf("records = %d, want 3", records)
	}
}

func TestProcessorPairingSurvivesRestart(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	if err := p.ProcessMessage(textMessage(testChatID, 1, "A", "persist", 10), "trace", rec.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}

	// 用同一数据库重新构造 repo/processor，模拟进程重启。
	restarted := func() *Processor {
		np := NewProcessor(repo.New(db), "test-secret", names.New(nil), time.UTC)
		np.sleep = func(time.Duration) {}
		return np
	}

	p2 := restarted()
	if err := p2.ProcessMessage(textMessage(testChatID, 2, "B", "persist", 11), "trace", rec.reply); err != nil {
		t.Fatalf("second ProcessMessage() after restart error = %v", err)
	}
	if len(rec.messages) != 1 {
		t.Fatalf("replies after restart = %#v, want 1 (second occurrence pairs)", rec.messages)
	}

	p3 := restarted()
	if err := p3.ProcessMessage(textMessage(testChatID, 3, "C", "persist", 12), "trace", rec.reply); err != nil {
		t.Fatalf("third ProcessMessage() after restart error = %v", err)
	}
	if len(rec.messages) != 1 {
		t.Fatalf("replies after third occurrence = %#v, want still 1", rec.messages)
	}

	p4 := restarted()
	if err := p4.ProcessMessage(textMessage(testChatID, 4, "D", "persist", 13), "trace", rec.reply); err != nil {
		t.Fatalf("fourth ProcessMessage() after restart error = %v", err)
	}
	if len(rec.messages) != 2 {
		t.Fatalf("replies after fourth occurrence = %#v, want 2", rec.messages)
	}
}

func TestProcessorSendFailureDoesNotCancelPair(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	if err := p.ProcessMessage(textMessage(testChatID, 1, "A", "retry me", 10), "trace", rec.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}

	sendErr := errors.New("telegram unavailable")
	failingReply := func(string) error { return sendErr }
	if err := p.ProcessMessage(textMessage(testChatID, 2, "B", "retry me", 11), "trace", failingReply); !errors.Is(err, sendErr) {
		t.Fatalf("second ProcessMessage() error = %v, want %v", err, sendErr)
	}

	// 提醒发送失败，配对没有真正取消：下一次出现应再次尝试提醒。
	if err := p.ProcessMessage(textMessage(testChatID, 3, "C", "retry me", 12), "trace", rec.reply); err != nil {
		t.Fatalf("third ProcessMessage() error = %v", err)
	}
	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1 successful retry", rec.messages)
	}

	// 第 4 次出现开启新的一对，仍然静默。
	if err := p.ProcessMessage(textMessage(testChatID, 4, "D", "retry me", 13), "trace", rec.reply); err != nil {
		t.Fatalf("fourth ProcessMessage() error = %v", err)
	}
	if len(rec.messages) != 1 {
		t.Errorf("replies = %#v, want still 1", rec.messages)
	}

	var record models.ForwardRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if record.ForwardCount != 4 {
		t.Errorf("ForwardCount = %d, want 4", record.ForwardCount)
	}
	var logs int64
	if err := db.Model(&models.ForwardLog{}).Count(&logs).Error; err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if logs != 4 {
		t.Errorf("logs = %d, want 4", logs)
	}
}

func TestProcessorCooldownSuppressionDoesNotCancelPair(t *testing.T) {
	p, r, db := setupProcessor(t)
	rec := &replyRecorder{}
	if err := r.SetChatCooldown(testChatID, 60, 1); err != nil {
		t.Fatalf("SetChatCooldown() error = %v", err)
	}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	p.cooldown.now = func() time.Time { return now }

	messages := []*telebot.Message{
		textMessage(testChatID, 1, "A", "cool down", 10), // 第 1 次：静默
		textMessage(testChatID, 2, "B", "cool down", 11), // 第 2 次：提醒
		textMessage(testChatID, 3, "C", "cool down", 12), // 第 3 次：静默
		textMessage(testChatID, 4, "D", "cool down", 13), // 第 4 次：提醒但被冷却抑制
	}
	for i, msg := range messages {
		if err := p.ProcessMessage(msg, "trace", rec.reply); err != nil {
			t.Fatalf("message %d ProcessMessage() error = %v", i, err)
		}
	}
	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1 before cooldown expires", rec.messages)
	}

	// 被冷却抑制的一对不能算作已取消，冷却结束后下一次出现仍可提醒。
	now = now.Add(61 * time.Second)
	if err := p.ProcessMessage(textMessage(testChatID, 5, "E", "cool down", 14), "trace", rec.reply); err != nil {
		t.Fatalf("fifth ProcessMessage() error = %v", err)
	}
	if len(rec.messages) != 2 {
		t.Fatalf("replies = %#v, want 2 after cooldown expires", rec.messages)
	}

	var logs int64
	if err := db.Model(&models.ForwardLog{}).Count(&logs).Error; err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if logs != 5 {
		t.Errorf("logs = %d, want 5 (every occurrence is logged)", logs)
	}
}

func TestProcessorConcurrentOccurrencesAlertOncePerPair(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	if err := p.ProcessMessage(textMessage(testChatID, 1, "A", "race", 10), "trace", rec.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}

	const concurrent = 8
	var wg sync.WaitGroup
	errs := make(chan error, concurrent)
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			msg := textMessage(testChatID, int64(100+i), "A", "race", 100+i)
			errs <- p.ProcessMessage(msg, "trace", rec.reply)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent ProcessMessage() error = %v", err)
		}
	}

	// 初始 1 次 + 8 次并发 = 9 次出现，应正好组成 4 对，每对只提醒一次。
	messages := rec.snapshot()
	if len(messages) != concurrent/2 {
		t.Errorf("replies = %d, want %d", len(messages), concurrent/2)
	}

	var record models.ForwardRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if record.ForwardCount != concurrent+1 {
		t.Errorf("ForwardCount = %d, want %d", record.ForwardCount, concurrent+1)
	}
	var logs int64
	if err := db.Model(&models.ForwardLog{}).Count(&logs).Error; err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if logs != concurrent+1 {
		t.Errorf("logs = %d, want %d (all occurrences)", logs, concurrent+1)
	}
}

// 升级前的行没有 pending_pair（NULL），按 ForwardCount 奇偶推导待配对状态。
func TestProcessorLegacyRecordsDerivePendingFromParity(t *testing.T) {
	p, _, db := setupProcessor(t)

	// 旧数据：ForwardCount=2 表示两次出现已经配过对，下一次应开启新的一对（静默）。
	if err := db.Create(&models.ForwardRecord{
		ChatID:           testChatID,
		ContentKey:       textKeyFor("test-secret", "legacy-even"),
		ForwardCount:     2,
		FirstTargetMsgID: 5,
	}).Error; err != nil {
		t.Fatalf("create legacy even record: %v", err)
	}
	// 旧数据：ForwardCount=1 表示还有一次未配对的出现，下一次应提醒。
	if err := db.Create(&models.ForwardRecord{
		ChatID:           testChatID,
		ContentKey:       textKeyFor("test-secret", "legacy-odd"),
		ForwardCount:     1,
		FirstTargetMsgID: 6,
	}).Error; err != nil {
		t.Fatalf("create legacy odd record: %v", err)
	}

	var legacy models.ForwardRecord
	if err := db.Where("content_key = ?", textKeyFor("test-secret", "legacy-even")).First(&legacy).Error; err != nil {
		t.Fatalf("load legacy record: %v", err)
	}
	if legacy.PendingPair != nil {
		t.Fatalf("legacy PendingPair = %v, want NULL before first use", *legacy.PendingPair)
	}

	rec := &replyRecorder{}
	// 偶数旧记录的第 3 次出现：静默；第 4 次：提醒。
	if err := p.ProcessMessage(textMessage(testChatID, 1, "A", "legacy-even", 10), "trace", rec.reply); err != nil {
		t.Fatalf("legacy-even third ProcessMessage() error = %v", err)
	}
	if len(rec.messages) != 0 {
		t.Fatalf("replies after legacy-even third = %#v, want none", rec.messages)
	}
	if err := p.ProcessMessage(textMessage(testChatID, 2, "B", "legacy-even", 11), "trace", rec.reply); err != nil {
		t.Fatalf("legacy-even fourth ProcessMessage() error = %v", err)
	}
	if len(rec.messages) != 1 {
		t.Fatalf("replies after legacy-even fourth = %#v, want 1", rec.messages)
	}

	// 奇数旧记录的第 2 次出现：提醒。
	if err := p.ProcessMessage(textMessage(testChatID, 3, "C", "legacy-odd", 12), "trace", rec.reply); err != nil {
		t.Fatalf("legacy-odd second ProcessMessage() error = %v", err)
	}
	if len(rec.messages) != 2 {
		t.Errorf("replies after legacy-odd second = %#v, want 2", rec.messages)
	}
}

func TestProcessorNormalizesTextBeforeMatching(t *testing.T) {
	tests := []struct {
		name  string
		first string
		again string
	}{
		{
			name:  "width case whitespace and punctuation",
			first: "Ｈｅｌｌｏ，   ＭＡＲＳ！",
			again: "hello mars",
		},
		{
			name:  "url tracking params",
			first: "请看这个 https://example.com/post?utm_source=a",
			again: "请看这个 https://example.com/post?utm_source=b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _, db := setupProcessor(t)
			rec := &replyRecorder{}

			if err := p.ProcessMessage(textMessage(testChatID, 1, "A", tt.first, 10), "trace", rec.reply); err != nil {
				t.Fatalf("first ProcessMessage() error = %v", err)
			}
			if err := p.ProcessMessage(textMessage(testChatID, 2, "B", tt.again, 11), "trace", rec.reply); err != nil {
				t.Fatalf("second ProcessMessage() error = %v", err)
			}
			if len(rec.messages) != 1 {
				t.Fatalf("replies = %#v, want 1 for normalized duplicate", rec.messages)
			}

			var records int64
			if err := db.Model(&models.ForwardRecord{}).Count(&records).Error; err != nil {
				t.Fatalf("count records: %v", err)
			}
			if records != 1 {
				t.Errorf("records = %d, want 1", records)
			}
		})
	}
}

func TestProcessorCollapsesButDoesNotRemoveSpaces(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	if err := p.ProcessMessage(textMessage(testChatID, 1, "A", "foo bar", 10), "trace", rec.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}
	if err := p.ProcessMessage(textMessage(testChatID, 2, "B", "foobar", 11), "trace", rec.reply); err != nil {
		t.Fatalf("second ProcessMessage() error = %v", err)
	}
	if err := p.ProcessMessage(textMessage(testChatID, 3, "C", "foo   bar", 12), "trace", rec.reply); err != nil {
		t.Fatalf("third ProcessMessage() error = %v", err)
	}

	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1", rec.messages)
	}

	var records int64
	if err := db.Model(&models.ForwardRecord{}).Count(&records).Error; err != nil {
		t.Fatalf("count records: %v", err)
	}
	if records != 2 {
		t.Errorf("records = %d, want 2 (space kept, runs collapsed)", records)
	}
}

func TestProcessorSkipsTextBelowMinimumContent(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"too short", "hi"},
		{"pure url", "https://example.com/a/b"},
		{"url with too little text", "看 https://example.com/a/b"},
		{"emoji only", "👍👍👍👍"},
		{"punctuation only", "！！！！"},
		{"command with bot suffix", "/mars_rank@some_bot"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _, db := setupProcessor(t)
			rec := &replyRecorder{}

			for i := 0; i < 2; i++ {
				if err := p.ProcessMessage(textMessage(testChatID, 1, "A", tt.text, 10+i), "trace", rec.reply); err != nil {
					t.Fatalf("ProcessMessage() error = %v", err)
				}
			}
			if len(rec.messages) != 0 {
				t.Errorf("replies = %#v, want none", rec.messages)
			}

			var records, logs, stats int64
			db.Model(&models.ForwardRecord{}).Count(&records)
			db.Model(&models.ForwardLog{}).Count(&logs)
			db.Model(&models.UserStats{}).Count(&stats)
			if records != 0 || logs != 0 || stats != 0 {
				t.Errorf("records=%d logs=%d stats=%d, want 0/0/0", records, logs, stats)
			}
		})
	}
}

func TestProcessorIgnoresBotMessages(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	newBotMessage := func(id int) *telebot.Message {
		msg := textMessage(testChatID, 99, "Bot", "hello mars", id)
		msg.Sender.IsBot = true
		return msg
	}
	if err := p.ProcessMessage(newBotMessage(10), "trace", rec.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}
	if err := p.ProcessMessage(newBotMessage(11), "trace", rec.reply); err != nil {
		t.Fatalf("second ProcessMessage() error = %v", err)
	}

	if len(rec.messages) != 0 {
		t.Errorf("replies = %#v, want none", rec.messages)
	}
	var records int64
	if err := db.Model(&models.ForwardRecord{}).Count(&records).Error; err != nil {
		t.Fatalf("count records: %v", err)
	}
	if records != 0 {
		t.Errorf("records = %d, want 0", records)
	}
}

func TestProcessorMediaDuplicateByFileUniqueID(t *testing.T) {
	tests := []struct {
		name  string
		build func(msgID int, userID int64, uniqueID string) *telebot.Message
	}{
		{
			name: "photo",
			build: func(msgID int, userID int64, uniqueID string) *telebot.Message {
				msg := textMessage(testChatID, userID, "A", "", msgID)
				msg.Photo = &telebot.Photo{File: telebot.File{UniqueID: uniqueID}}
				return msg
			},
		},
		{
			name: "video",
			build: func(msgID int, userID int64, uniqueID string) *telebot.Message {
				msg := textMessage(testChatID, userID, "A", "", msgID)
				msg.Video = &telebot.Video{File: telebot.File{UniqueID: uniqueID}}
				return msg
			},
		},
		{
			name: "animation",
			build: func(msgID int, userID int64, uniqueID string) *telebot.Message {
				msg := textMessage(testChatID, userID, "A", "", msgID)
				msg.Animation = &telebot.Animation{File: telebot.File{UniqueID: uniqueID}}
				return msg
			},
		},
		{
			name: "document",
			build: func(msgID int, userID int64, uniqueID string) *telebot.Message {
				msg := textMessage(testChatID, userID, "A", "", msgID)
				msg.Document = &telebot.Document{File: telebot.File{UniqueID: uniqueID}}
				return msg
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _, db := setupProcessor(t)
			rec := &replyRecorder{}

			if err := p.ProcessMessage(tt.build(10, 1, "same-unique-id"), "trace", rec.reply); err != nil {
				t.Fatalf("first ProcessMessage() error = %v", err)
			}
			if err := p.ProcessMessage(tt.build(11, 2, "same-unique-id"), "trace", rec.reply); err != nil {
				t.Fatalf("second ProcessMessage() error = %v", err)
			}
			if len(rec.messages) != 1 {
				t.Fatalf("replies = %#v, want 1", rec.messages)
			}
			if !strings.Contains(rec.messages[0], "这条内容") {
				t.Errorf("reply = %q, want media wording", rec.messages[0])
			}

			var record models.ForwardRecord
			if err := db.First(&record).Error; err != nil {
				t.Fatalf("load record: %v", err)
			}
			wantKey := "media:" + tt.name + ":same-unique-id"
			if record.ContentKey != wantKey {
				t.Errorf("ContentKey = %q, want %q", record.ContentKey, wantKey)
			}
			if record.ForwardCount != 2 {
				t.Errorf("ForwardCount = %d, want 2", record.ForwardCount)
			}
		})
	}
}

func TestProcessorDifferentMediaUniqueIDIsNotDuplicate(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	for i, uniqueID := range []string{"photo-a", "photo-b"} {
		msg := textMessage(testChatID, 1, "A", "", 10+i)
		msg.Photo = &telebot.Photo{File: telebot.File{UniqueID: uniqueID}}
		if err := p.ProcessMessage(msg, "trace", rec.reply); err != nil {
			t.Fatalf("ProcessMessage() error = %v", err)
		}
	}

	if len(rec.messages) != 0 {
		t.Errorf("replies = %#v, want none", rec.messages)
	}
	var records int64
	if err := db.Model(&models.ForwardRecord{}).Count(&records).Error; err != nil {
		t.Fatalf("count records: %v", err)
	}
	if records != 2 {
		t.Errorf("records = %d, want 2", records)
	}
}

func TestProcessorMediaSkipsForwardedAndMissingUniqueID(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	forwarded := textMessage(testChatID, 1, "A", "", 10)
	forwarded.Photo = &telebot.Photo{File: telebot.File{UniqueID: "forwarded-photo"}}
	forwarded.OriginalSender = &telebot.User{ID: 77}

	missingID := textMessage(testChatID, 1, "A", "", 11)
	missingID.Photo = &telebot.Photo{}

	for _, msg := range []*telebot.Message{forwarded, forwarded, missingID, missingID} {
		if err := p.ProcessMessage(msg, "trace", rec.reply); err != nil {
			t.Fatalf("ProcessMessage() error = %v", err)
		}
	}

	if len(rec.messages) != 0 {
		t.Errorf("replies = %#v, want none", rec.messages)
	}
	var records int64
	if err := db.Model(&models.ForwardRecord{}).Count(&records).Error; err != nil {
		t.Fatalf("count records: %v", err)
	}
	if records != 0 {
		t.Errorf("records = %d, want 0", records)
	}
}
