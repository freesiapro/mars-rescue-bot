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

	logMessageReceived("trace-1", &telebot.Message{
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
