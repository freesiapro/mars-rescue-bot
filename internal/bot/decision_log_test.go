package bot

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"gopkg.in/telebot.v4"
	"mars-rescue-bot/internal/models"
	"mars-rescue-bot/internal/repo"
)

type capturedDecision struct {
	Level     string `json:"level"`
	Msg       string `json:"msg"`
	ChatID    int64  `json:"chat_id"`
	MessageID int    `json:"message_id"`
	MsgType   string `json:"msg_type"`
	Decision  string `json:"decision"`
	UserID    int64  `json:"user_id"`
	KeyHash   string `json:"key_hash"`
}

func captureDecisionLog(t *testing.T, p *Processor) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	p.logger = slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return buf
}

func readDecisions(t *testing.T, buf *bytes.Buffer) []capturedDecision {
	t.Helper()
	var out []capturedDecision
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var line capturedDecision
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("decode decision log %q: %v", raw, err)
		}
		if line.Msg != "dedup decision" {
			t.Fatalf("unexpected log message %q, want dedup decision", line.Msg)
		}
		out = append(out, line)
	}
	return out
}

func TestDecisionLogSkipBranches(t *testing.T) {
	tests := []struct {
		name     string
		prepare  func(t *testing.T, r *repo.Repo)
		msg      func() *telebot.Message
		want     string
		wantType string
		wantUser int64
	}{
		{
			name:     "no_message",
			msg:      func() *telebot.Message { return nil },
			want:     DecisionNoMessage,
			wantType: "unknown",
		},
		{
			name: "no_chat",
			msg: func() *telebot.Message {
				return &telebot.Message{ID: 21, Text: "chatless", Sender: &telebot.User{ID: 11, FirstName: "NoChat"}}
			},
			want:     DecisionNoChat,
			wantType: "text",
			wantUser: 11,
		},
		{
			name: "private_chat",
			msg: func() *telebot.Message {
				msg := textMessage(testChatID, 21, "Private", "private content", 22)
				msg.Chat.Type = telebot.ChatPrivate
				return msg
			},
			want:     DecisionPrivateChat,
			wantType: "text",
			wantUser: 21,
		},
		{
			name: "no_sender",
			msg: func() *telebot.Message {
				return &telebot.Message{ID: 23, Text: "anonymous", Chat: &telebot.Chat{ID: testChatID, Type: telebot.ChatSuperGroup}}
			},
			want:     DecisionNoSender,
			wantType: "text",
		},
		{
			name: "bot_ignored",
			msg: func() *telebot.Message {
				msg := textMessage(testChatID, 31, "Bot", "from a bot", 24)
				msg.Sender.IsBot = true
				return msg
			},
			want:     DecisionBotIgnored,
			wantType: "text",
			wantUser: 31,
		},
		{
			name: "disabled_chat",
			prepare: func(t *testing.T, r *repo.Repo) {
				t.Helper()
				if err := r.SetChatEnabled(testChatID, false, 1); err != nil {
					t.Fatalf("SetChatEnabled() error = %v", err)
				}
			},
			msg: func() *telebot.Message {
				return textMessage(testChatID, 41, "Disabled", "disabled content", 25)
			},
			want:     DecisionDisabledChat,
			wantType: "text",
			wantUser: 41,
		},
		{
			name: "ignored_user",
			prepare: func(t *testing.T, r *repo.Repo) {
				t.Helper()
				if _, err := r.AddIgnoredUser(testChatID, 51, 1); err != nil {
					t.Fatalf("AddIgnoredUser() error = %v", err)
				}
			},
			msg: func() *telebot.Message {
				return textMessage(testChatID, 51, "Ignored", "ignored content", 26)
			},
			want:     DecisionIgnoredUser,
			wantType: "text",
			wantUser: 51,
		},
		{
			name: "short_text_skipped",
			msg: func() *telebot.Message {
				return textMessage(testChatID, 61, "Short", "hi", 27)
			},
			want:     DecisionShortTextSkipped,
			wantType: "text",
			wantUser: 61,
		},
		{
			name: "repeated_char_skipped",
			msg: func() *telebot.Message {
				return textMessage(testChatID, 71, "Repeated", "哈哈哈哈哈哈", 28)
			},
			want:     DecisionRepeatedCharSkipped,
			wantType: "text",
			wantUser: 71,
		},
		{
			name: "command",
			msg: func() *telebot.Message {
				return textMessage(testChatID, 81, "Commander", "/mars_rank", 29)
			},
			want:     DecisionCommand,
			wantType: "text",
			wantUser: 81,
		},
		{
			name: "no_content_empty_text",
			msg: func() *telebot.Message {
				return textMessage(testChatID, 91, "Empty", "", 30)
			},
			want:     DecisionNoContent,
			wantType: "unknown",
			wantUser: 91,
		},
		{
			name: "no_content_blank_text",
			msg: func() *telebot.Message {
				return textMessage(testChatID, 92, "Blank", "   ", 31)
			},
			want:     DecisionNoContent,
			wantType: "text",
			wantUser: 92,
		},
		{
			name: "no_content_photo_without_file_id",
			msg: func() *telebot.Message {
				msg := textMessage(testChatID, 93, "Photo", "", 32)
				msg.Photo = &telebot.Photo{}
				return msg
			},
			want:     DecisionNoContent,
			wantType: "photo",
			wantUser: 93,
		},
		{
			name: "unsupported_type_audio",
			msg: func() *telebot.Message {
				msg := textMessage(testChatID, 94, "Audio", "", 33)
				msg.Audio = &telebot.Audio{File: telebot.File{UniqueID: "audio-unique"}}
				return msg
			},
			want:     DecisionUnsupportedType,
			wantType: "audio",
			wantUser: 94,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, r, _ := setupProcessor(t)
			if tt.prepare != nil {
				tt.prepare(t, r)
			}
			buf := captureDecisionLog(t, p)
			replies := &replyRecorder{}

			if err := p.ProcessMessage(tt.msg(), "trace", replies.reply); err != nil {
				t.Fatalf("ProcessMessage() error = %v", err)
			}
			lines := readDecisions(t, buf)
			if len(lines) != 1 {
				t.Fatalf("decision lines = %d, want exactly 1: %s", len(lines), buf.String())
			}
			line := lines[0]
			if line.Level != "INFO" {
				t.Errorf("level = %q, want INFO", line.Level)
			}
			if line.Decision != tt.want {
				t.Errorf("decision = %q, want %q", line.Decision, tt.want)
			}
			if line.MsgType != tt.wantType {
				t.Errorf("msg_type = %q, want %q", line.MsgType, tt.wantType)
			}
			if line.UserID != tt.wantUser {
				t.Errorf("user_id = %d, want %d", line.UserID, tt.wantUser)
			}
			if line.KeyHash != "" {
				t.Errorf("key_hash = %q, want omitted for this branch", line.KeyHash)
			}
			if got := len(replies.snapshot()); got != 0 {
				t.Errorf("replies = %d, want 0", got)
			}
		})
	}
}

func TestDecisionLogRichMessage(t *testing.T) {
	p, _, _ := setupProcessor(t)
	msg := textMessage(testChatID, 7, "A", "", 10)
	msg.RichMessage = &telebot.RichMessage{
		Blocks: []telebot.RichBlock{{
			Type: telebot.RichBlockParagraph,
			Text: &telebot.RichText{Kind: telebot.RichTextPlain, Plain: "rich decision content"},
		}},
	}
	buf := captureDecisionLog(t, p)
	replies := &replyRecorder{}

	if err := p.ProcessMessage(msg, "trace", replies.reply); err != nil {
		t.Fatalf("ProcessMessage() error = %v", err)
	}
	lines := readDecisions(t, buf)
	if len(lines) != 1 {
		t.Fatalf("decision lines = %d, want exactly 1: %s", len(lines), buf.String())
	}
	line := lines[0]
	if line.Decision != DecisionPairedSilent {
		t.Errorf("decision = %q, want %q", line.Decision, DecisionPairedSilent)
	}
	if line.MsgType != "rich_message" {
		t.Errorf("msg_type = %q, want rich_message", line.MsgType)
	}
	if len(line.KeyHash) != 8 {
		t.Errorf("key_hash = %q, want 8 hex chars", line.KeyHash)
	}
	if len(replies.snapshot()) != 0 {
		t.Errorf("replies = %#v, want none on first occurrence", replies.snapshot())
	}
}

func TestDecisionLogRouting(t *testing.T) {
	p, _, _ := setupProcessor(t)
	p.adjacentRepeatGap = AdjacentRepeatGap
	buf := captureDecisionLog(t, p)
	replies := &replyRecorder{}

	steps := []struct {
		messageID int
		want      string
	}{
		{10, DecisionPairedSilent},
		{15, DecisionAdjacentRepeatExempt},
		{100, DecisionAlerted},
		{200, DecisionPairedSilent},
	}
	for i, step := range steps {
		msg := textMessage(testChatID, int64(i+1), "Routing", "routing content", step.messageID)
		if err := p.ProcessMessage(msg, "trace", replies.reply); err != nil {
			t.Fatalf("ProcessMessage(msg %d) error = %v", step.messageID, err)
		}
		lines := readDecisions(t, buf)
		if len(lines) != i+1 {
			t.Fatalf("after msg %d, decision lines = %d, want %d", step.messageID, len(lines), i+1)
		}
		line := lines[len(lines)-1]
		if line.Decision != step.want {
			t.Errorf("msg %d decision = %q, want %q", step.messageID, line.Decision, step.want)
		}
		if line.MessageID != step.messageID {
			t.Errorf("message_id = %d, want %d", line.MessageID, step.messageID)
		}
		if line.MsgType != "text" {
			t.Errorf("msg_type = %q, want text", line.MsgType)
		}
		if len(line.KeyHash) != 8 {
			t.Errorf("key_hash length = %d, want 8", len(line.KeyHash))
		}
		if line.KeyHash != lines[0].KeyHash {
			t.Errorf("key_hash changed for the same content: %q vs %q", line.KeyHash, lines[0].KeyHash)
		}
	}
	if got := len(replies.snapshot()); got != 1 {
		t.Errorf("replies = %d, want 1", got)
	}
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		t.Logf("sample dedup decision: %s", raw)
	}
}

func TestDecisionLogCooldownSuppressed(t *testing.T) {
	p, r, _ := setupProcessor(t)
	if err := r.SetChatCooldown(testChatID, 60, 1); err != nil {
		t.Fatalf("SetChatCooldown() error = %v", err)
	}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	p.cooldown.now = func() time.Time { return now }
	buf := captureDecisionLog(t, p)
	replies := &replyRecorder{}

	steps := []struct {
		messageID int
		want      string
	}{
		{10, DecisionPairedSilent},
		{11, DecisionAlerted},
		{12, DecisionPairedSilent},
		{13, DecisionCooldownSuppressed},
	}
	for i, step := range steps {
		msg := textMessage(testChatID, int64(i+1), "Cooldown", "cooldown content", step.messageID)
		if err := p.ProcessMessage(msg, "trace", replies.reply); err != nil {
			t.Fatalf("ProcessMessage(msg %d) error = %v", step.messageID, err)
		}
		line := readDecisions(t, buf)[i]
		if line.Decision != step.want {
			t.Errorf("msg %d decision = %q, want %q", step.messageID, line.Decision, step.want)
		}
	}
	lines := readDecisions(t, buf)
	if len(lines) != len(steps) {
		t.Fatalf("decision lines = %d, want %d", len(lines), len(steps))
	}
	if len(lines[3].KeyHash) != 8 {
		t.Errorf("cooldown key_hash = %q, want 8-char hash", lines[3].KeyHash)
	}
	if got := len(replies.snapshot()); got != 1 {
		t.Errorf("replies = %d, want 1", got)
	}
}

func TestDecisionLogSendFailedRolledBack(t *testing.T) {
	p, _, _ := setupProcessor(t)
	buf := captureDecisionLog(t, p)
	replies := &replyRecorder{}

	if err := p.ProcessMessage(textMessage(testChatID, 1, "A", "retry me", 10), "trace", replies.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}
	sendErr := errors.New("telegram unavailable")
	err := p.ProcessMessage(textMessage(testChatID, 2, "B", "retry me", 11), "trace", func(string) error {
		return sendErr
	})
	if !errors.Is(err, sendErr) {
		t.Fatalf("second ProcessMessage() error = %v, want %v", err, sendErr)
	}

	lines := readDecisions(t, buf)
	if len(lines) != 2 {
		t.Fatalf("decision lines = %d, want 2", len(lines))
	}
	if lines[0].Decision != DecisionPairedSilent {
		t.Errorf("first decision = %q, want %q", lines[0].Decision, DecisionPairedSilent)
	}
	if lines[1].Decision != DecisionSendFailedRolledBack {
		t.Errorf("second decision = %q, want %q", lines[1].Decision, DecisionSendFailedRolledBack)
	}
	if len(lines[1].KeyHash) != 8 {
		t.Errorf("key_hash = %q, want 8-char hash", lines[1].KeyHash)
	}
}

func TestDecisionLogProcessingError(t *testing.T) {
	p, _, db := setupProcessor(t)
	buf := captureDecisionLog(t, p)

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB.DB() error = %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	err = p.ProcessMessage(textMessage(testChatID, 7, "Broken", "database down", 10), "trace", (&replyRecorder{}).reply)
	if err == nil {
		t.Fatal("ProcessMessage() error = nil, want database error")
	}

	lines := readDecisions(t, buf)
	if len(lines) != 1 {
		t.Fatalf("decision lines = %d, want 1", len(lines))
	}
	if lines[0].Decision != DecisionProcessingError {
		t.Errorf("decision = %q, want %q", lines[0].Decision, DecisionProcessingError)
	}
	if lines[0].MsgType != "text" {
		t.Errorf("msg_type = %q, want text", lines[0].MsgType)
	}
	if lines[0].UserID != 7 {
		t.Errorf("user_id = %d, want 7", lines[0].UserID)
	}
}

func TestDecisionLogNeverContainsTextOrNames(t *testing.T) {
	const (
		sensitiveText = "SensitiveText_9f3a must never be logged"
		sensitiveName = "SecretName_9f3a"
		sensitiveUser = "secret_user_9f3a"
	)

	t.Run("text", func(t *testing.T) {
		p, _, _ := setupProcessor(t)
		buf := captureDecisionLog(t, p)
		replies := &replyRecorder{}

		for i := 0; i < 2; i++ {
			msg := textMessage(testChatID, 12345, sensitiveName, sensitiveText, 10+i)
			msg.Sender.Username = sensitiveUser
			if err := p.ProcessMessage(msg, "trace", replies.reply); err != nil {
				t.Fatalf("ProcessMessage() error = %v", err)
			}
		}

		out := buf.String()
		for _, secret := range []string{sensitiveText, sensitiveName, sensitiveUser, "SensitiveText", "SecretName"} {
			if strings.Contains(out, secret) {
				t.Errorf("decision log leaked %q: %s", secret, out)
			}
		}
		fullKey := textKeyFor(p.dedupSecret, sensitiveText)
		if strings.Contains(out, fullKey) {
			t.Errorf("decision log contains full content key %q", fullKey)
		}
		fullHash := strings.TrimPrefix(fullKey, textContentKeyPrefix)
		lines := readDecisions(t, buf)
		if len(lines) != 2 {
			t.Fatalf("decision lines = %d, want 2", len(lines))
		}
		if lines[0].KeyHash != fullHash[:8] {
			t.Errorf("key_hash = %q, want first 8 chars %q", lines[0].KeyHash, fullHash[:8])
		}
	})

	t.Run("media", func(t *testing.T) {
		p, _, _ := setupProcessor(t)
		buf := captureDecisionLog(t, p)
		replies := &replyRecorder{}
		const (
			uniqueID = "SecretUniqueID_9f3a"
			caption  = "SecretCaption_9f3a"
		)

		for i := 0; i < 2; i++ {
			msg := textMessage(testChatID, 22345, sensitiveName, "", 20+i)
			msg.Sender.Username = sensitiveUser
			msg.Photo = &telebot.Photo{File: telebot.File{UniqueID: uniqueID}}
			msg.Caption = caption
			if err := p.ProcessMessage(msg, "trace", replies.reply); err != nil {
				t.Fatalf("ProcessMessage() error = %v", err)
			}
		}

		out := buf.String()
		key := "media:photo:" + uniqueID
		for _, secret := range []string{uniqueID, caption, sensitiveName, sensitiveUser, key} {
			if strings.Contains(out, secret) {
				t.Errorf("decision log leaked %q: %s", secret, out)
			}
		}
		lines := readDecisions(t, buf)
		if len(lines) != 2 {
			t.Fatalf("decision lines = %d, want 2", len(lines))
		}
		wantHash := contentKeyHash(p.dedupSecret, key)
		if lines[0].KeyHash != wantHash || len(lines[0].KeyHash) != 8 {
			t.Errorf("key_hash = %q, want HMAC prefix %q", lines[0].KeyHash, wantHash)
		}
		if lines[0].MsgType != "photo" {
			t.Errorf("msg_type = %q, want photo", lines[0].MsgType)
		}
	})
}

func TestDecisionLogChannelForwardTypeAndHash(t *testing.T) {
	p, _, _ := setupProcessor(t)
	buf := captureDecisionLog(t, p)
	replies := &replyRecorder{}

	newChannelForward := func(messageID int, userID int64) *telebot.Message {
		msg := forwardedTextMessage(messageID, userID, "channel post")
		msg.Origin = &telebot.MessageOrigin{
			Type:      "channel",
			Chat:      &telebot.Chat{ID: -100888, Type: telebot.ChatChannel},
			MessageID: 77,
		}
		return msg
	}
	processForwardMessages(t, p, replies, newChannelForward(10, forwardingUserID), newChannelForward(11, forwardingUserID+1))

	lines := readDecisions(t, buf)
	if len(lines) != 2 {
		t.Fatalf("decision lines = %d, want 2", len(lines))
	}
	wantHash := contentKeyHash(p.dedupSecret, "channel:-100888:77")
	for _, line := range lines {
		if line.MsgType != "channel_forward" {
			t.Errorf("msg_type = %q, want channel_forward", line.MsgType)
		}
		if line.KeyHash != wantHash || len(line.KeyHash) != 8 {
			t.Errorf("key_hash = %q, want 8-char HMAC prefix %q", line.KeyHash, wantHash)
		}
	}
	out := buf.String()
	for _, raw := range []string{"channel:-100888:77", "-100888"} {
		if strings.Contains(out, raw) {
			t.Errorf("decision log leaked raw channel key part %q: %s", raw, out)
		}
	}
}

func TestDecisionLogBotOriginForwardIsDeduped(t *testing.T) {
	p, _, _ := setupProcessor(t)
	buf := captureDecisionLog(t, p)
	replies := &replyRecorder{}

	first := forwardedTextMessage(10, forwardingUserID, "bot origin forward")
	first.OriginalSender = forwardOriginBotUser()
	second := forwardedTextMessage(11, forwardingUserID+1, "bot origin forward")
	second.OriginalSender = forwardOriginBotUser()
	processForwardMessages(t, p, replies, first, second)

	lines := readDecisions(t, buf)
	if len(lines) != 2 {
		t.Fatalf("decision lines = %d, want 2", len(lines))
	}
	if lines[0].Decision != DecisionPairedSilent || lines[1].Decision != DecisionAlerted {
		t.Errorf("decisions = %q/%q, want %q/%q", lines[0].Decision, lines[1].Decision, DecisionPairedSilent, DecisionAlerted)
	}
	for _, line := range lines {
		if line.Decision == DecisionForwardFromBotIgnored {
			t.Errorf("forwarded bot content used unreachable decision %q", DecisionForwardFromBotIgnored)
		}
	}
}

func TestDecisionLogExactlyOncePerMessage(t *testing.T) {
	p, _, _ := setupProcessor(t)
	buf := captureDecisionLog(t, p)
	replies := &replyRecorder{}

	botMsg := textMessage(testChatID, 99, "Bot", "bot message", 16)
	botMsg.Sender.IsBot = true
	messages := []*telebot.Message{
		textMessage(testChatID, 1, "A", "", 10),
		textMessage(testChatID, 1, "A", "/start", 11),
		textMessage(testChatID, 1, "A", "exactly once content", 12),
		textMessage(testChatID, 2, "B", "exactly once content", 13),
		textMessage(testChatID, 3, "C", "exactly once content", 14),
		botMsg,
		nil,
	}
	want := []string{
		DecisionNoContent,
		DecisionCommand,
		DecisionPairedSilent,
		DecisionAlerted,
		DecisionPairedSilent,
		DecisionBotIgnored,
		DecisionNoMessage,
	}

	for i, msg := range messages {
		if err := p.ProcessMessage(msg, "trace", replies.reply); err != nil {
			t.Fatalf("ProcessMessage(%d) error = %v", i, err)
		}
		lines := readDecisions(t, buf)
		if len(lines) != i+1 {
			t.Fatalf("after message %d, decision lines = %d, want %d", i, len(lines), i+1)
		}
		if lines[i].Decision != want[i] {
			t.Errorf("message %d decision = %q, want %q", i, lines[i].Decision, want[i])
		}
	}
}

// /hyw 走命令豁免：不建记录、不计数、不写流水，只有唯一一条 command 决策日志。
// 线上已注册命令由 telebot 直接分派；这里直接调用 ProcessMessage，验证豁免分支本身。
func TestHYWCommandDecisionExemptFromDedup(t *testing.T) {
	p, _, db := setupProcessor(t)
	buf := captureDecisionLog(t, p)
	replies := &replyRecorder{}

	msg := textMessage(testChatID, 7, "Asker", "/hyw", 10)
	if err := p.ProcessMessage(msg, "trace", replies.reply); err != nil {
		t.Fatalf("ProcessMessage(/hyw) error = %v", err)
	}

	lines := readDecisions(t, buf)
	if len(lines) != 1 {
		t.Fatalf("decision lines = %d, want exactly 1", len(lines))
	}
	line := lines[0]
	if line.Decision != DecisionCommand {
		t.Errorf("decision = %q, want %q", line.Decision, DecisionCommand)
	}
	if line.MsgType != "text" {
		t.Errorf("msg_type = %q, want text", line.MsgType)
	}
	if line.KeyHash != "" {
		t.Errorf("key_hash = %q, want omitted", line.KeyHash)
	}
	if got := len(replies.snapshot()); got != 0 {
		t.Errorf("replies = %d, want 0", got)
	}

	for _, item := range []struct {
		name  string
		model interface{}
	}{
		{"records", &models.ForwardRecord{}},
		{"logs", &models.ForwardLog{}},
		{"stats", &models.UserStats{}},
	} {
		var count int64
		if err := db.Model(item.model).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", item.name, err)
		}
		if count != 0 {
			t.Errorf("%s count = %d, want 0", item.name, count)
		}
	}
}
