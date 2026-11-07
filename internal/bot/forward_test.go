package bot

import (
	"strings"
	"testing"

	"gopkg.in/telebot.v4"
	"mars-rescue-bot/internal/models"
)

const (
	forwardingUserID = int64(7001)
	forwardOriginBot = int64(9001)
)

func forwardOriginBotUser() *telebot.User {
	return &telebot.User{ID: forwardOriginBot, FirstName: "NoticeBot", Username: "notice_bot", IsBot: true}
}

func forwardedTextMessage(msgID int, userID int64, text string) *telebot.Message {
	return textMessage(testChatID, userID, "Forwarder", text, msgID)
}

func forwardedPhotoMessage(msgID int, userID int64) *telebot.Message {
	msg := textMessage(testChatID, userID, "Forwarder", "", msgID)
	msg.Photo = &telebot.Photo{File: telebot.File{UniqueID: "bot-photo-unique"}}
	msg.Caption = "bot photo caption"
	msg.OriginalSender = forwardOriginBotUser()
	return msg
}

func processForwardMessages(t *testing.T, p *Processor, rec *replyRecorder, msgs ...*telebot.Message) {
	t.Helper()
	for i, msg := range msgs {
		if err := p.ProcessMessage(msg, "trace", rec.reply); err != nil {
			t.Fatalf("ProcessMessage() occurrence %d error = %v", i+1, err)
		}
	}
}

func TestForwardedBotTextTwiceAlerts(t *testing.T) {
	tests := []struct {
		name  string
		apply func(*telebot.Message)
	}{
		{
			name: "legacy forward_from",
			apply: func(msg *telebot.Message) {
				msg.OriginalSender = forwardOriginBotUser()
			},
		},
		{
			name: "forward_origin user",
			apply: func(msg *telebot.Message) {
				msg.Origin = &telebot.MessageOrigin{Type: "user", Sender: forwardOriginBotUser()}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _, db := setupProcessor(t)
			rec := &replyRecorder{}

			first := forwardedTextMessage(10, forwardingUserID, "bot notice text")
			second := forwardedTextMessage(11, forwardingUserID+1, "bot notice text")
			tt.apply(first)
			tt.apply(second)
			processForwardMessages(t, p, rec, first, second)

			if len(rec.messages) != 1 {
				t.Fatalf("replies = %#v, want 1", rec.messages)
			}
			if !strings.Contains(rec.messages[0], "火星救援，") {
				t.Errorf("reply = %q, want duplicate alert", rec.messages[0])
			}

			var record models.ForwardRecord
			if err := db.First(&record).Error; err != nil {
				t.Fatalf("load record: %v", err)
			}
			wantKey := textKeyFor(p.dedupSecret, "bot notice text")
			if record.ContentKey != wantKey {
				t.Errorf("ContentKey = %q, want %q (same key as hand-typed text)", record.ContentKey, wantKey)
			}
			if record.ForwardCount != 2 {
				t.Errorf("ForwardCount = %d, want 2", record.ForwardCount)
			}
		})
	}
}

func TestForwardedBotPhotoTwiceAlerts(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	first := forwardedPhotoMessage(10, forwardingUserID)
	second := forwardedPhotoMessage(11, forwardingUserID+1)
	processForwardMessages(t, p, rec, first, second)

	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1", rec.messages)
	}
	if !strings.Contains(rec.messages[0], "这张图") {
		t.Errorf("reply = %q, want media subject", rec.messages[0])
	}

	var record models.ForwardRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if record.ContentKey != "media:photo:bot-photo-unique" {
		t.Errorf("ContentKey = %q, want media:photo:bot-photo-unique", record.ContentKey)
	}
}

func TestModernForwardOriginChannelUsesChannelKey(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	newChannelForward := func(msgID int, userID int64) *telebot.Message {
		msg := forwardedTextMessage(msgID, userID, "channel post")
		msg.Origin = &telebot.MessageOrigin{
			Type:      "channel",
			Chat:      &telebot.Chat{ID: -100888, Type: telebot.ChatChannel},
			MessageID: 77,
		}
		return msg
	}
	processForwardMessages(t, p, rec, newChannelForward(10, forwardingUserID), newChannelForward(11, forwardingUserID+1))

	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1", rec.messages)
	}
	if !strings.Contains(rec.messages[0], "这条转发") {
		t.Errorf("reply = %q, want channel-forward subject", rec.messages[0])
	}

	var record models.ForwardRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if record.ContentKey != "channel:-100888:77" {
		t.Errorf("ContentKey = %q, want channel:-100888:77", record.ContentKey)
	}
}

func TestForwarderBotIsIgnored(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	first := forwardedTextMessage(10, forwardOriginBot, "forwarded by a bot")
	first.Sender.IsBot = true
	first.OriginalSender = &telebot.User{ID: 42, FirstName: "Human"}
	second := forwardedTextMessage(11, forwardOriginBot, "forwarded by a bot")
	second.Sender.IsBot = true
	second.OriginalSender = &telebot.User{ID: 42, FirstName: "Human"}
	processForwardMessages(t, p, rec, first, second)

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

func TestForwardedAndHandTypedTextShareKey(t *testing.T) {
	p, _, db := setupProcessor(t)
	rec := &replyRecorder{}

	forwarded := forwardedTextMessage(10, forwardingUserID, "same content")
	forwarded.OriginalSender = forwardOriginBotUser()
	handTyped := textMessage(testChatID, forwardingUserID+1, "Typist", "same content", 11)
	processForwardMessages(t, p, rec, forwarded, handTyped)

	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1", rec.messages)
	}
	var record models.ForwardRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if record.ContentKey != textKeyFor(p.dedupSecret, "same content") {
		t.Errorf("ContentKey = %q, want hand-typed text key", record.ContentKey)
	}
}
