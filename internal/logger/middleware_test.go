package logger

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"gopkg.in/telebot.v4"
)

func TestLogMessageReceivedOmitsUsername(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	LogMessageReceived("trace-1", &telebot.Message{
		ID:     42,
		Text:   "hello",
		Sender: &telebot.User{ID: 7, Username: "alice", FirstName: "Alice"},
		Chat:   &telebot.Chat{ID: -100, Type: telebot.ChatSuperGroup},
	})

	out := buf.String()
	if strings.Contains(out, `"username"`) || strings.Contains(out, "alice") || strings.Contains(out, "Alice") {
		t.Errorf("log output contains a username: %s", out)
	}
	if !strings.Contains(out, `"user_id":7`) {
		t.Errorf("log output missing user id: %s", out)
	}
	if !strings.Contains(out, `"message_id":42`) {
		t.Errorf("log output missing message id: %s", out)
	}
}

func TestLogMessageReceivedRecognizesRichMessage(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	LogMessageReceived("trace-rich", &telebot.Message{
		ID:          43,
		Sender:      &telebot.User{ID: 8, FirstName: "B"},
		Chat:        &telebot.Chat{ID: -100, Type: telebot.ChatSuperGroup},
		RichMessage: &telebot.RichMessage{Blocks: []telebot.RichBlock{{Type: telebot.RichBlockParagraph}}},
	})

	out := buf.String()
	if !strings.Contains(out, `"msg_type":"rich_message"`) {
		t.Errorf("log output = %s, want msg_type rich_message", out)
	}
	if strings.Contains(out, `"Type"`) || strings.Contains(out, "paragraph") {
		t.Errorf("log output leaked rich block structure: %s", out)
	}
}
