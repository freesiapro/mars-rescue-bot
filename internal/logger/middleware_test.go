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

func TestLogMessageReceivedRecognizesOriginOnlyForward(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	const originDate = int64(1_700_000_000)
	LogMessageReceived("trace-origin", &telebot.Message{
		ID:     44,
		Text:   "hidden source text",
		Sender: &telebot.User{ID: 9, FirstName: "Forwarder"},
		Chat:   &telebot.Chat{ID: -100, Type: telebot.ChatSuperGroup},
		Origin: &telebot.MessageOrigin{
			Type:           "hidden_user",
			SenderUsername: "Secret Original Name",
			DateUnixtime:   originDate,
		},
	})

	out := buf.String()
	for _, want := range []string{
		`"msg_type":"forward"`,
		`"forward_origin":"hidden_user"`,
		`"has_sender_name":true`,
		`"origin_date":1700000000`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log output = %s, want containing %s", out, want)
		}
	}
	if strings.Contains(out, "Secret Original Name") {
		t.Errorf("log output leaked the original sender name: %s", out)
	}
	if strings.Contains(out, "origin_user_id") {
		t.Errorf("log output has origin_user_id for a hidden source: %s", out)
	}
}

func TestLogMessageReceivedOriginUserID(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	LogMessageReceived("trace-user-origin", &telebot.Message{
		ID:     45,
		Text:   "user source text",
		Sender: &telebot.User{ID: 10, FirstName: "Forwarder"},
		Chat:   &telebot.Chat{ID: -100, Type: telebot.ChatSuperGroup},
		Origin: &telebot.MessageOrigin{
			Type:         "user",
			Sender:       &telebot.User{ID: 77, FirstName: "Original"},
			DateUnixtime: 1_700_000_001,
		},
	})

	out := buf.String()
	for _, want := range []string{
		`"msg_type":"forward"`,
		`"forward_origin":"user"`,
		`"origin_user_id":77`,
		`"origin_date":1700000001`,
		`"has_sender_name":false`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log output = %s, want containing %s", out, want)
		}
	}
	if strings.Contains(out, "Original") {
		t.Errorf("log output leaked the original sender name: %s", out)
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
