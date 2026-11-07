package bot

import (
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/telebot.v4"
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
	// 多数测试验证普通配对；相邻刷屏豁免由专门的测试显式开启。
	p.adjacentRepeatGap = 0
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

var occurrenceCountPattern = regexp.MustCompile(`第\s*[0-9]+\s*次|[0-9]+\s*次`)

// assertAlertCopyOmissions 确认提醒文案里没有出现次数，也没有旧版的配对说明。
func assertAlertCopyOmissions(t *testing.T, copy string) {
	t.Helper()
	if match := occurrenceCountPattern.FindString(copy); match != "" {
		t.Errorf("alert copy contains occurrence count %q: %q", match, copy)
	}
	for _, forbidden := range []string{"配对", "抵消"} {
		if strings.Contains(copy, forbidden) {
			t.Errorf("alert copy contains %q: %q", forbidden, copy)
		}
	}
}

func TestTextKeyUsesHMACAndNoPlaintext(t *testing.T) {
	key := textContentKey("secret", "hello mars bot")
	if !strings.HasPrefix(key, "text:v2:") {
		t.Errorf("key = %q, want text:v2: prefix", key)
	}
	if strings.Contains(key, "hello") || strings.Contains(key, "mars") {
		t.Errorf("key = %q, must not contain plaintext", key)
	}

	other := textContentKey("other-secret", "hello mars bot")
	if key == other {
		t.Error("different secrets should produce different keys")
	}

	again := textContentKey("secret", "hello mars bot")
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
		ID: 10, Text: "hello mars bot", Sender: &telebot.User{ID: 1, FirstName: "A"}, Chat: chat,
	}, "trace", rec.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}
	if err := p.ProcessMessage(&telebot.Message{
		ID: 11, Text: "hello mars bot", Sender: &telebot.User{ID: 2, FirstName: "B"}, Chat: chat,
	}, "trace", rec.reply); err != nil {
		t.Fatalf("second ProcessMessage() error = %v", err)
	}

	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1", rec.messages)
	}
	if !strings.Contains(rec.messages[0], "https://t.me/public_group/10") {
		t.Errorf("reply = %q, want public group link", rec.messages[0])
	}
	assertAlertCopyOmissions(t, rec.messages[0])
}

func TestProcessorReplyIncludesFirstSender(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	if err := p.ProcessMessage(textMessage(testChatID, 7, "Alice", "hello mars bot", 10), "trace", rec.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}
	if err := p.ProcessMessage(textMessage(testChatID, 8, "Bob", "hello mars bot", 11), "trace", rec.reply); err != nil {
		t.Fatalf("second ProcessMessage() error = %v", err)
	}

	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1", rec.messages)
	}
	reply := rec.messages[0]
	for _, want := range []string{
		`火星救援，<a href="tg://user?id=8">Bob</a>：这条 Alice `,
		` 就发过了 <a href="https://t.me/c/1234567890/10">看最早那条</a>`,
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("reply = %q, want containing %q", reply, want)
		}
	}
	assertAlertCopyOmissions(t, reply)

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
	want := `火星救援，<a href="tg://user?id=8">Bob</a>：这条之前就有人发过了`
	if rec.messages[0] != want {
		t.Errorf("reply = %q, want %q", rec.messages[0], want)
	}
	assertAlertCopyOmissions(t, rec.messages[0])
}

func TestProcessorBasicGroupReplyHasNoLink(t *testing.T) {
	p, _, _ := setupProcessor(t)
	rec := &replyRecorder{}
	chat := &telebot.Chat{ID: -123456, Type: telebot.ChatGroup}

	first := textMessage(chat.ID, 1, "Alice", "hello mars bot", 10)
	first.Chat = chat
	second := textMessage(chat.ID, 2, "Bob", "hello mars bot", 11)
	second.Chat = chat

	if err := p.ProcessMessage(first, "trace", rec.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}
	if err := p.ProcessMessage(second, "trace", rec.reply); err != nil {
		t.Fatalf("second ProcessMessage() error = %v", err)
	}
	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1", rec.messages)
	}

	reply := rec.messages[0]
	for _, want := range []string{
		`火星救援，<a href="tg://user?id=2">Bob</a>：这条 Alice `,
		" 就发过了",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("reply = %q, want containing %q", reply, want)
		}
	}
	assertAlertCopyOmissions(t, reply)
	if strings.Contains(reply, "看最早那条") {
		t.Errorf("reply = %q, want no link text in a basic group", reply)
	}
	if strings.Contains(reply, "https://t.me") {
		t.Errorf("reply = %q, want no message link in a basic group", reply)
	}
}

func TestFormatDuplicateCopyBranches(t *testing.T) {
	p, _, _ := setupProcessor(t)
	p.names.Observe(&telebot.User{ID: 1, FirstName: "Alice"})
	p.names.Observe(&telebot.User{ID: 2, FirstName: "Bob"})
	p.names.Observe(&telebot.User{ID: 3, FirstName: "<Eve&Co>"})

	fixedNow := time.Date(2026, 11, 4, 18, 0, 0, 0, time.UTC)
	created := time.Date(2026, 11, 4, 12, 30, 0, 0, time.UTC)
	previousDay := time.Date(2026, 11, 3, 12, 30, 0, 0, time.UTC)
	publicChat := &telebot.Chat{ID: testChatID, Type: telebot.ChatSuperGroup, Username: "public_group"}
	basicChat := &telebot.Chat{ID: -123456, Type: telebot.ChatGroup}
	link := `<a href="https://t.me/public_group/10">看最早那条</a>`
	mentionBob := `<a href="tg://user?id=2">Bob</a>`

	tests := []struct {
		name    string
		chat    *telebot.Chat
		event   *duplicateEvent
		mention bool
		now     time.Time
		want    string
	}{
		{
			name: "text known sender with public link today",
			chat: publicChat,
			event: &duplicateEvent{
				record: &models.ForwardRecord{
					FirstSenderID: 1, FirstTargetMsgID: 10, ForwardCount: 4, CreatedAt: created,
				},
				kind:   "text",
				sender: &telebot.User{ID: 2, FirstName: "Bob"},
			},
			mention: true,
			want:    "火星救援，" + mentionBob + "：这条 Alice 12:30 就发过了 " + link,
		},
		{
			name: "text known sender with public link another day",
			chat: publicChat,
			event: &duplicateEvent{
				record: &models.ForwardRecord{
					FirstSenderID: 1, FirstTargetMsgID: 10, CreatedAt: previousDay,
				},
				kind:   "text",
				sender: &telebot.User{ID: 2, FirstName: "Bob"},
			},
			want: "火星救援，Bob：这条 Alice 11-03 12:30 就发过了 " + link,
		},
		{
			name: "text known sender without link",
			chat: basicChat,
			event: &duplicateEvent{
				record: &models.ForwardRecord{
					FirstSenderID: 1, FirstTargetMsgID: 10, CreatedAt: created,
				},
				kind:   "text",
				sender: &telebot.User{ID: 2, FirstName: "Bob"},
			},
			want: "火星救援，Bob：这条 Alice 12:30 就发过了",
		},
		{
			name: "unknown first sender ignores time and link",
			chat: publicChat,
			event: &duplicateEvent{
				record: &models.ForwardRecord{FirstTargetMsgID: 10, ForwardCount: 4, CreatedAt: created},
				kind:   "text",
				sender: &telebot.User{ID: 2, FirstName: "Bob"},
			},
			want: "火星救援，Bob：这条之前就有人发过了",
		},
		{
			name: "unknown first sender uses media noun",
			chat: publicChat,
			event: &duplicateEvent{
				record:       &models.ForwardRecord{FirstTargetMsgID: 10, CreatedAt: created},
				kind:         "media",
				mediaSubject: "这张图",
				sender:       &telebot.User{ID: 2, FirstName: "Bob"},
			},
			want: "火星救援，Bob：这张图之前就有人发过了",
		},
		{
			name: "self repeat text with link",
			chat: publicChat,
			event: &duplicateEvent{
				record: &models.ForwardRecord{
					FirstSenderID: 2, FirstTargetMsgID: 10, CreatedAt: created,
				},
				kind:   "text",
				sender: &telebot.User{ID: 2, FirstName: "Bob"},
			},
			mention: true,
			want:    "火星救援，" + mentionBob + "：这条你自己 12:30 就发过了 " + link,
		},
		{
			name: "self repeat text without link",
			chat: basicChat,
			event: &duplicateEvent{
				record: &models.ForwardRecord{
					FirstSenderID: 2, FirstTargetMsgID: 10, CreatedAt: created,
				},
				kind:   "text",
				sender: &telebot.User{ID: 2, FirstName: "Bob"},
			},
			want: "火星救援，Bob：这条你自己 12:30 就发过了",
		},
		{
			name: "self repeat uses media noun",
			chat: publicChat,
			event: &duplicateEvent{
				record: &models.ForwardRecord{
					FirstSenderID: 2, FirstTargetMsgID: 10, CreatedAt: created,
				},
				kind:         "media",
				mediaSubject: "这段视频",
				sender:       &telebot.User{ID: 2, FirstName: "Bob"},
			},
			want: "火星救援，Bob：这段视频你自己 12:30 就发过了 " + link,
		},
		{
			name: "channel forward",
			chat: publicChat,
			event: &duplicateEvent{
				record: &models.ForwardRecord{
					FirstSenderID: 1, FirstTargetMsgID: 10, CreatedAt: created,
				},
				kind:   "channel",
				sender: &telebot.User{ID: 2, FirstName: "Bob"},
			},
			want: "火星救援，Bob：这条转发 Alice 12:30 就发过了 " + link,
		},
		{
			name: "photo noun",
			chat: publicChat,
			event: &duplicateEvent{
				record: &models.ForwardRecord{
					FirstSenderID: 1, FirstTargetMsgID: 10, CreatedAt: created,
				},
				kind:         "media",
				mediaSubject: "这张图",
				sender:       &telebot.User{ID: 2, FirstName: "Bob"},
			},
			want: "火星救援，Bob：这张图 Alice 12:30 就发过了 " + link,
		},
		{
			name: "video noun",
			chat: publicChat,
			event: &duplicateEvent{
				record: &models.ForwardRecord{
					FirstSenderID: 1, FirstTargetMsgID: 10, CreatedAt: created,
				},
				kind:         "media",
				mediaSubject: "这段视频",
				sender:       &telebot.User{ID: 2, FirstName: "Bob"},
			},
			want: "火星救援，Bob：这段视频 Alice 12:30 就发过了 " + link,
		},
		{
			name: "animation noun",
			chat: publicChat,
			event: &duplicateEvent{
				record: &models.ForwardRecord{
					FirstSenderID: 1, FirstTargetMsgID: 10, CreatedAt: created,
				},
				kind:         "media",
				mediaSubject: "这个动图",
				sender:       &telebot.User{ID: 2, FirstName: "Bob"},
			},
			want: "火星救援，Bob：这个动图 Alice 12:30 就发过了 " + link,
		},
		{
			name: "document noun",
			chat: publicChat,
			event: &duplicateEvent{
				record: &models.ForwardRecord{
					FirstSenderID: 1, FirstTargetMsgID: 10, CreatedAt: created,
				},
				kind:         "media",
				mediaSubject: "这个文件",
				sender:       &telebot.User{ID: 2, FirstName: "Bob"},
			},
			want: "火星救援，Bob：这个文件 Alice 12:30 就发过了 " + link,
		},
		{
			name: "mention disabled uses display name",
			chat: publicChat,
			event: &duplicateEvent{
				record: &models.ForwardRecord{
					FirstSenderID: 1, FirstTargetMsgID: 10, CreatedAt: created,
				},
				kind:   "text",
				sender: &telebot.User{ID: 2, FirstName: "Bob"},
			},
			want: "火星救援，Bob：这条 Alice 12:30 就发过了 " + link,
		},
		{
			name: "unresolved first sender falls back to id",
			chat: publicChat,
			event: &duplicateEvent{
				record: &models.ForwardRecord{
					FirstSenderID: 9, FirstTargetMsgID: 10, CreatedAt: created,
				},
				kind:   "text",
				sender: &telebot.User{ID: 2, FirstName: "Bob"},
			},
			want: "火星救援，Bob：这条 群友 9 12:30 就发过了 " + link,
		},
		{
			name: "escapes first sender name",
			chat: publicChat,
			event: &duplicateEvent{
				record: &models.ForwardRecord{
					FirstSenderID: 3, FirstTargetMsgID: 10, CreatedAt: created,
				},
				kind:   "text",
				sender: &telebot.User{ID: 2, FirstName: "Bob"},
			},
			want: "火星救援，Bob：这条 &lt;Eve&amp;Co&gt; 12:30 就发过了 " + link,
		},
		{
			name: "escapes alerted person name",
			chat: publicChat,
			event: &duplicateEvent{
				record: &models.ForwardRecord{
					FirstSenderID: 1, FirstTargetMsgID: 10, CreatedAt: created,
				},
				kind:   "text",
				sender: &telebot.User{ID: 2, FirstName: "<Bob&Co>"},
			},
			want: "火星救援，&lt;Bob&amp;Co&gt;：这条 Alice 12:30 就发过了 " + link,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := tt.now
			if now.IsZero() {
				now = fixedNow
			}
			p.now = func() time.Time { return now }
			out := p.formatDuplicate(tt.chat, tt.event, tt.mention)
			t.Logf("rendered alert: %s", out)
			if out != tt.want {
				t.Errorf("formatDuplicate() = %q, want %q", out, tt.want)
			}
			assertAlertCopyOmissions(t, out)
		})
	}
}

func TestProcessorFirstMessageNoReply(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	if err := p.ProcessMessage(textMessage(testChatID, 1, "A", "hello mars bot", 10), "trace", rec.reply); err != nil {
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
	msg1 := textMessage(testChatID, 7, "<A&B>", "hello mars bot", 10)
	msg2 := textMessage(testChatID, 7, "<A&B>", "hello mars bot", 11)

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
	for _, want := range []string{
		`火星救援，<a href="tg://user?id=7">&lt;A&amp;B&gt;</a>：这条你自己 `,
		` 就发过了 <a href="https://t.me/c/1234567890/10">看最早那条</a>`,
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("reply = %q, want containing %q", reply, want)
		}
	}
	if strings.Contains(reply, "<A&B>") {
		t.Errorf("reply contains unescaped name: %q", reply)
	}
	assertAlertCopyOmissions(t, reply)

	var record models.ForwardRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if !strings.HasPrefix(record.ContentKey, "text:v2:") {
		t.Errorf("ContentKey = %q, want text:v2: prefix", record.ContentKey)
	}
	if strings.Contains(record.ContentKey, "hello mars bot") {
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
		if err := p.ProcessMessage(textMessage(testChatID, 1, "A", "hello mars bot", 10+i), "trace", rec.reply); err != nil {
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
		if err := p.ProcessMessage(textMessage(testChatID, 1, "A", "hello mars bot", 10+i), "trace", rec.reply); err != nil {
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
		textMessage(testChatID, 1, "A", "hello mars bot", 10), // first: record only
		textMessage(testChatID, 2, "B", "hello mars bot", 11), // duplicate: reply
		textMessage(testChatID, 3, "C", "hello mars bot", 12), // duplicate within cooldown: no reply
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
	if err := p.ProcessMessage(textMessage(testChatID, 4, "D", "hello mars bot", 13), "trace", rec.reply); err != nil {
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

	msg1 := textMessage(testChatID, 7, "<A>", "hello mars bot", 10)
	msg2 := textMessage(testChatID, 7, "<A>", "hello mars bot", 11)
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
	assertAlertCopyOmissions(t, reply)
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
	second := newForward(11)
	second.Sender = &telebot.User{ID: 8, FirstName: "B"}
	if err := p.ProcessMessage(second, "trace", rec.reply); err != nil {
		t.Fatalf("second ProcessMessage() error = %v", err)
	}

	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1", rec.messages)
	}
	for _, want := range []string{
		`火星救援，<a href="tg://user?id=8">B</a>：这条转发 A `,
		` 就发过了 <a href="https://t.me/c/1234567890/10">看最早那条</a>`,
	} {
		if !strings.Contains(rec.messages[0], want) {
			t.Errorf("reply = %q, want containing %q", rec.messages[0], want)
		}
	}
	assertAlertCopyOmissions(t, rec.messages[0])

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

	if err := p.ProcessMessage(textMessage(-1001, 1, "A", "same text here", 10), "trace", rec.reply); err != nil {
		t.Fatalf("chat 1 ProcessMessage() error = %v", err)
	}
	if err := p.ProcessMessage(textMessage(-1002, 1, "A", "same text here", 11), "trace", rec.reply); err != nil {
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
		msg := textMessage(testChatID, int64(i+1), "A", "pair me please", 10+i)
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
		// 每次出现都写日志；排行榜统计时会排除第 1 次出现的流水。
		if logs != int64(i+1) {
			t.Errorf("after occurrence %d logs = %d, want %d", i+1, logs, i+1)
		}
	}

	for i, alert := range rec.messages {
		if !strings.Contains(alert, "火星救援，") || !strings.Contains(alert, " 就发过了") {
			t.Errorf("alert %d = %q, want casual duplicate copy", i, alert)
		}
		assertAlertCopyOmissions(t, alert)
	}
}

func TestAlertCountExceedsTwo(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	var alertCounts []int64
	var alertTexts []string
	for i := 0; i < 6; i++ {
		before := len(rec.snapshot())
		msg := textMessage(testChatID, int64(i+1), "A", "count me again", 10+i)
		if err := p.ProcessMessage(msg, "trace", rec.reply); err != nil {
			t.Fatalf("occurrence %d ProcessMessage() error = %v", i+1, err)
		}
		replies := rec.snapshot()
		if len(replies) == before {
			continue
		}
		alertTexts = append(alertTexts, replies[len(replies)-1])

		// a8d7fab 的 formatDuplicate 直接把 ForwardCount 渲染成“第 N 次”，
		// 所以这里取到的就是用户当时看到的 N；修复后文案不再渲染它。
		var record models.ForwardRecord
		if err := db.First(&record).Error; err != nil {
			t.Fatalf("occurrence %d load record: %v", i+1, err)
		}
		alertCounts = append(alertCounts, int64(record.ForwardCount))
	}

	want := []int64{2, 4, 6}
	if len(alertCounts) != len(want) {
		t.Fatalf("alert counts = %v, want %v", alertCounts, want)
	}
	for i := range want {
		if alertCounts[i] != want[i] {
			t.Fatalf("alert counts = %v, want %v", alertCounts, want)
		}
	}
	if alertCounts[len(alertCounts)-1] <= 2 {
		t.Fatalf("last alert count = %d, want > 2", alertCounts[len(alertCounts)-1])
	}
	for i, alert := range alertTexts {
		assertAlertCopyOmissions(t, alert)
		if !strings.Contains(alert, "火星救援，") || !strings.Contains(alert, " 就发过了") {
			t.Errorf("alert %d = %q, want casual duplicate copy", i, alert)
		}
	}

	t.Logf("alerts fired at cumulative ForwardCount=%v; rendered copies contain no count", alertCounts)
}

func TestProcessorPairingIndependentPerContentAndChat(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	msgs := []*telebot.Message{
		textMessage(-1001, 1, "A", "alpha content", 10), // chat1 alpha: 第 1 次，静默
		textMessage(-1002, 1, "A", "alpha content", 11), // chat2 alpha: 第 1 次，静默
		textMessage(-1001, 1, "A", "beta content", 12),  // chat1 beta:  第 1 次，静默
		textMessage(-1001, 1, "A", "alpha content", 13), // chat1 alpha: 第 2 次，提醒
		textMessage(-1002, 1, "A", "alpha content", 14), // chat2 alpha: 第 2 次，提醒
		textMessage(-1001, 1, "A", "beta content", 15),  // chat1 beta:  第 2 次，提醒
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

	if err := p.ProcessMessage(textMessage(testChatID, 1, "A", "persist content", 10), "trace", rec.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}

	// 用同一数据库重新构造 repo/processor，模拟进程重启。
	restarted := func() *Processor {
		np := NewProcessor(repo.New(db), "test-secret", names.New(nil), time.UTC)
		np.sleep = func(time.Duration) {}
		np.adjacentRepeatGap = 0
		return np
	}

	p2 := restarted()
	if err := p2.ProcessMessage(textMessage(testChatID, 2, "B", "persist content", 11), "trace", rec.reply); err != nil {
		t.Fatalf("second ProcessMessage() after restart error = %v", err)
	}
	if len(rec.messages) != 1 {
		t.Fatalf("replies after restart = %#v, want 1 (second occurrence pairs)", rec.messages)
	}

	p3 := restarted()
	if err := p3.ProcessMessage(textMessage(testChatID, 3, "C", "persist content", 12), "trace", rec.reply); err != nil {
		t.Fatalf("third ProcessMessage() after restart error = %v", err)
	}
	if len(rec.messages) != 1 {
		t.Fatalf("replies after third occurrence = %#v, want still 1", rec.messages)
	}

	p4 := restarted()
	if err := p4.ProcessMessage(textMessage(testChatID, 4, "D", "persist content", 13), "trace", rec.reply); err != nil {
		t.Fatalf("fourth ProcessMessage() after restart error = %v", err)
	}
	if len(rec.messages) != 2 {
		t.Fatalf("replies after fourth occurrence = %#v, want 2", rec.messages)
	}
}

func TestProcessorSendFailureDoesNotCancelPair(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	if err := p.ProcessMessage(textMessage(testChatID, 1, "A", "retry me again", 10), "trace", rec.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}

	sendErr := errors.New("telegram unavailable")
	failingReply := func(string) error { return sendErr }
	if err := p.ProcessMessage(textMessage(testChatID, 2, "B", "retry me again", 11), "trace", failingReply); !errors.Is(err, sendErr) {
		t.Fatalf("second ProcessMessage() error = %v, want %v", err, sendErr)
	}

	// 提醒发送失败，配对没有真正取消：下一次出现应再次尝试提醒。
	if err := p.ProcessMessage(textMessage(testChatID, 3, "C", "retry me again", 12), "trace", rec.reply); err != nil {
		t.Fatalf("third ProcessMessage() error = %v", err)
	}
	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1 successful retry", rec.messages)
	}

	// 第 4 次出现开启新的一对，仍然静默。
	if err := p.ProcessMessage(textMessage(testChatID, 4, "D", "retry me again", 13), "trace", rec.reply); err != nil {
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
		textMessage(testChatID, 1, "A", "cool down now", 10), // 第 1 次：静默
		textMessage(testChatID, 2, "B", "cool down now", 11), // 第 2 次：提醒
		textMessage(testChatID, 3, "C", "cool down now", 12), // 第 3 次：静默
		textMessage(testChatID, 4, "D", "cool down now", 13), // 第 4 次：提醒但被冷却抑制
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
	if err := p.ProcessMessage(textMessage(testChatID, 5, "E", "cool down now", 14), "trace", rec.reply); err != nil {
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

func TestProcessorCooldownRevertAlertsOnNextOccurrence(t *testing.T) {
	p, r, db := setupProcessor(t)
	rec := &replyRecorder{}
	if err := r.SetChatCooldown(testChatID, 60, 1); err != nil {
		t.Fatalf("SetChatCooldown() error = %v", err)
	}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	p.cooldown.now = func() time.Time { return now }

	for i, msg := range []*telebot.Message{
		textMessage(testChatID, 1, "A", "cooldown revert", 10), // 1：静默
		textMessage(testChatID, 2, "B", "cooldown revert", 11), // 2：提醒
		textMessage(testChatID, 3, "C", "cooldown revert", 12), // 3：静默
		textMessage(testChatID, 4, "D", "cooldown revert", 13), // 4：提醒被冷却抑制
	} {
		if err := p.ProcessMessage(msg, "trace", rec.reply); err != nil {
			t.Fatalf("message %d ProcessMessage() error = %v", i+1, err)
		}
	}
	if len(rec.messages) != 1 {
		t.Fatalf("replies after suppressed pair = %#v, want only the first alert", rec.messages)
	}

	// 第 4 次提醒被冷却抑制后配对状态回滚：计数仍是 4，但这一对仍在等待提醒。
	var record models.ForwardRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load record after revert: %v", err)
	}
	if record.ForwardCount != 4 {
		t.Fatalf("ForwardCount after revert = %d, want 4", record.ForwardCount)
	}
	if record.PendingPair == nil || !*record.PendingPair {
		t.Fatalf("PendingPair after revert = %v, want true", record.PendingPair)
	}

	// 冷却结束后，下一次出现重新 claim 这一对并发送提醒。
	now = now.Add(61 * time.Second)
	if err := p.ProcessMessage(textMessage(testChatID, 5, "E", "cooldown revert", 14), "trace", rec.reply); err != nil {
		t.Fatalf("next occurrence ProcessMessage() error = %v", err)
	}
	if len(rec.messages) != 2 {
		t.Fatalf("replies after next occurrence = %#v, want 2", rec.messages)
	}
	if !strings.Contains(rec.messages[1], "火星救援，") || !strings.Contains(rec.messages[1], " 就发过了") {
		t.Errorf("next alert = %q, want casual duplicate copy", rec.messages[1])
	}
	assertAlertCopyOmissions(t, rec.messages[1])

	if err := db.First(&record).Error; err != nil {
		t.Fatalf("reload record after next occurrence: %v", err)
	}
	if record.ForwardCount != 5 {
		t.Errorf("ForwardCount after next occurrence = %d, want 5", record.ForwardCount)
	}
	if record.PendingPair == nil || *record.PendingPair {
		t.Errorf("PendingPair after next occurrence = %v, want false", record.PendingPair)
	}
}

func TestProcessorConcurrentOccurrencesAlertOncePerPair(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	if err := p.ProcessMessage(textMessage(testChatID, 1, "A", "race content", 10), "trace", rec.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}

	const concurrent = 8
	var wg sync.WaitGroup
	errs := make(chan error, concurrent)
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			msg := textMessage(testChatID, int64(100+i), "A", "race content", 100+i)
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
		ContentKey:       textKeyFor("test-secret", "legacy-odd-one"),
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
	if err := p.ProcessMessage(textMessage(testChatID, 3, "C", "legacy-odd-one", 12), "trace", rec.reply); err != nil {
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
			first: "Ｈｅｌｌｏ，   ＭＡＲＳ ＢＯＴ！",
			again: "hello mars bot",
		},
		{
			name:  "url tracking params",
			first: "请看这个内容真的很有意思 https://example.com/post?utm_source=a",
			again: "请看这个内容真的很有意思 https://example.com/post?utm_source=b",
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

	if err := p.ProcessMessage(textMessage(testChatID, 1, "A", "foo bar baz qux", 10), "trace", rec.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}
	if err := p.ProcessMessage(textMessage(testChatID, 2, "B", "foobarbazqux", 11), "trace", rec.reply); err != nil {
		t.Fatalf("second ProcessMessage() error = %v", err)
	}
	if err := p.ProcessMessage(textMessage(testChatID, 3, "C", "foo   bar baz qux", 12), "trace", rec.reply); err != nil {
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

func TestProcessorSkipsRepeatedSingleRuneText(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"chinese catchphrase", "草草草草草草草草草草"},
		{"digits", "6666666666"},
		{"latin", "aaaaaaaaaa"},
		{"longer catchphrase", "哈哈哈哈哈哈哈哈哈哈"},
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

func TestProcessorKeepsRepeatedRuneWithOtherContent(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"suffix breaks the run", "草草草草草草草草草我"},
		{"url makes it normal text", "草草草草草草草草草草 https://example.com/a/b"},
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
			if len(rec.messages) != 1 {
				t.Fatalf("replies = %#v, want 1", rec.messages)
			}

			var records, logs int64
			db.Model(&models.ForwardRecord{}).Count(&records)
			db.Model(&models.ForwardLog{}).Count(&logs)
			if records != 1 || logs != 2 {
				t.Errorf("records=%d logs=%d, want 1/2", records, logs)
			}
		})
	}
}

func TestProcessorRepeatedRuneForwardIsSkipped(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	first := forwardedTextMessage(10, forwardingUserID, "草草草草草草草草草草")
	first.OriginalSender = forwardOriginBotUser()
	second := forwardedTextMessage(11, forwardingUserID+1, "草草草草草草草草草草")
	second.OriginalSender = forwardOriginBotUser()
	processForwardMessages(t, p, rec, first, second)

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
}

func TestProcessorKeepsTenRuneTextBoundary(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		wantAlerts int
		wantRecord int64
	}{
		{"ten runes", "abcdefghij", 1, 1},
		{"nine runes", "abcdefghi", 0, 0},
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
			if len(rec.messages) != tt.wantAlerts {
				t.Errorf("replies = %#v, want %d", rec.messages, tt.wantAlerts)
			}
			var records int64
			db.Model(&models.ForwardRecord{}).Count(&records)
			if records != tt.wantRecord {
				t.Errorf("records = %d, want %d", records, tt.wantRecord)
			}
		})
	}
}

func TestProcessorIgnoresBotMessages(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	newBotMessage := func(id int) *telebot.Message {
		msg := textMessage(testChatID, 99, "Bot", "hello mars bot", id)
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
		name    string
		subject string
		build   func(msgID int, userID int64, uniqueID string) *telebot.Message
	}{
		{
			name:    "photo",
			subject: "这张图",
			build: func(msgID int, userID int64, uniqueID string) *telebot.Message {
				msg := textMessage(testChatID, userID, "A", "", msgID)
				msg.Photo = &telebot.Photo{File: telebot.File{UniqueID: uniqueID}}
				return msg
			},
		},
		{
			name:    "video",
			subject: "这段视频",
			build: func(msgID int, userID int64, uniqueID string) *telebot.Message {
				msg := textMessage(testChatID, userID, "A", "", msgID)
				msg.Video = &telebot.Video{File: telebot.File{UniqueID: uniqueID}}
				return msg
			},
		},
		{
			name:    "animation",
			subject: "这个动图",
			build: func(msgID int, userID int64, uniqueID string) *telebot.Message {
				msg := textMessage(testChatID, userID, "A", "", msgID)
				msg.Animation = &telebot.Animation{File: telebot.File{UniqueID: uniqueID}}
				return msg
			},
		},
		{
			name:    "document",
			subject: "这个文件",
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
			if !strings.Contains(rec.messages[0], tt.subject) {
				t.Errorf("reply = %q, want media subject %q", rec.messages[0], tt.subject)
			}
			assertAlertCopyOmissions(t, rec.messages[0])
			if strings.Contains(rec.messages[0], "这句话") {
				t.Errorf("media reply must not talk about text: %q", rec.messages[0])
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

func TestProcessorMediaSkipsMissingUniqueID(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	missingID := textMessage(testChatID, 1, "A", "", 11)
	missingID.Photo = &telebot.Photo{}

	for _, msg := range []*telebot.Message{missingID, missingID} {
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

func TestFormatRankListResolvesNamesAtDisplayTime(t *testing.T) {
	resolver := names.New(nil)
	resolver.Observe(&telebot.User{ID: 1, FirstName: "Alice"})
	chat := &telebot.Chat{ID: testChatID, Type: telebot.ChatSuperGroup}
	stats := []models.UserStats{
		{UserID: 1, RescueCount: 3},
		{UserID: 2, RescueCount: 2},
	}

	out := FormatRankList(stats, chat, "测试群", "本周", resolver)
	for _, want := range []string{"Alice", "群友 2", "3 次", "2 次", "测试群"} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatRankList() = %q, want containing %q", out, want)
		}
	}
	if strings.Contains(out, "条不同内容") {
		t.Errorf("FormatRankList() = %q, should count occurrences, not distinct contents", out)
	}
}

func TestFormatRankListFallsBackWithoutResolver(t *testing.T) {
	chat := &telebot.Chat{ID: testChatID, Type: telebot.ChatSuperGroup}
	out := FormatRankList([]models.UserStats{{UserID: 9, RescueCount: 1}}, chat, "群", "本周", nil)
	if !strings.Contains(out, "群友 9") {
		t.Errorf("FormatRankList() = %q, want fallback name", out)
	}
}

func TestFormatRankListEscapesResolvedNames(t *testing.T) {
	resolver := names.New(nil)
	resolver.Observe(&telebot.User{ID: 1, FirstName: "<Alice&Bob>"})
	chat := &telebot.Chat{ID: testChatID, Type: telebot.ChatSuperGroup}

	out := FormatRankList([]models.UserStats{{UserID: 1, RescueCount: 1}}, chat, "群", "本周", resolver)
	if strings.Contains(out, "<Alice&Bob>") {
		t.Errorf("FormatRankList() contains unescaped name: %q", out)
	}
	if !strings.Contains(out, "&lt;Alice&amp;Bob&gt;") {
		t.Errorf("FormatRankList() = %q, want escaped name", out)
	}
}

func TestFormatRankListEmpty(t *testing.T) {
	out := FormatRankList(nil, &telebot.Chat{ID: testChatID}, "群", "本周", names.New(nil))
	if !strings.Contains(out, "还没有火星次数") {
		t.Errorf("FormatRankList() = %q, want empty message", out)
	}
}

func TestFormatRankListUsesPeriodLabel(t *testing.T) {
	chat := &telebot.Chat{ID: testChatID}
	for _, label := range []string{"本月", "总榜"} {
		out := FormatRankList(nil, chat, "群", label, names.New(nil))
		if !strings.Contains(out, label) {
			t.Errorf("FormatRankList(%q) = %q, want period label", label, out)
		}
		out = FormatRankList([]models.UserStats{{UserID: 1, RescueCount: 1}}, chat, "群", label, names.New(nil))
		if !strings.Contains(out, "（"+label+"）") {
			t.Errorf("FormatRankList(%q) = %q, want period in header", label, out)
		}
	}
}

func TestFormatUserStats(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	last := time.Date(2026, 11, 4, 12, 30, 0, 0, time.UTC)

	out := FormatUserStats(3, 5, 9, last, loc)
	for _, want := range []string{"本周：3 次", "本月：5 次", "累计：9 次", "2026-11-04 20:30"} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatUserStats() = %q, want containing %q", out, want)
		}
	}

	if out := FormatUserStats(0, 0, 0, time.Time{}, loc); !strings.Contains(out, "还没有火星次数") {
		t.Errorf("FormatUserStats(empty) = %q, want empty message", out)
	}
}
