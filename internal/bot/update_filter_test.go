package bot

import (
	"testing"

	"gopkg.in/telebot.v4"
)

func TestNormalizeRichMessageUpdate(t *testing.T) {
	rich := func() *telebot.RichMessage {
		return &telebot.RichMessage{
			Blocks: []telebot.RichBlock{{
				Type: telebot.RichBlockParagraph,
				Text: &telebot.RichText{Kind: telebot.RichTextPlain, Plain: "rich body"},
			}},
		}
	}

	tests := []struct {
		name     string
		update   *telebot.Update
		wantText string
	}{
		{
			name:     "direct rich message",
			update:   &telebot.Update{Message: &telebot.Message{RichMessage: rich()}},
			wantText: richMessageRouteText,
		},
		{
			name: "forwarded rich message",
			update: &telebot.Update{Message: &telebot.Message{
				RichMessage: rich(),
				Origin:      &telebot.MessageOrigin{Type: "hidden_user"},
			}},
			wantText: richMessageRouteText,
		},
		{
			name: "rich message with text is untouched",
			update: &telebot.Update{Message: &telebot.Message{
				RichMessage: rich(),
				Text:        "already routed",
			}},
			wantText: "already routed",
		},
		{
			name: "rich message with caption is untouched",
			update: &telebot.Update{Message: &telebot.Message{
				RichMessage: rich(),
				Caption:     "caption",
			}},
		},
		{
			name: "rich message with media is untouched",
			update: &telebot.Update{Message: &telebot.Message{
				RichMessage: rich(),
				Photo:       &telebot.Photo{File: telebot.File{FileID: "f"}},
			}},
		},
		{
			name:     "plain text is untouched",
			update:   &telebot.Update{Message: &telebot.Message{Text: "plain"}},
			wantText: "plain",
		},
		{
			name:   "nil update",
			update: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeRichMessageUpdate(tt.update); !got {
				t.Fatal("NormalizeRichMessageUpdate() = false, want true")
			}
			if tt.update == nil || tt.update.Message == nil {
				return
			}
			if got := tt.update.Message.Text; got != tt.wantText {
				t.Errorf("Text = %q, want %q", got, tt.wantText)
			}
		})
	}
}

func TestNormalizedRichSampleStillUsesRichBody(t *testing.T) {
	msg := decodeRichSample(t, readRichSample(t))
	update := &telebot.Update{Message: msg}
	if !NormalizeRichMessageUpdate(update) {
		t.Fatal("NormalizeRichMessageUpdate() = false, want true")
	}
	if msg.Text != richMessageRouteText {
		t.Fatalf("Text = %q, want routing placeholder", msg.Text)
	}
	if got := messagePlainText(msg); got != realSamplePlainText {
		t.Errorf("messagePlainText() = %q, want rich sample text", got)
	}
	if content := AnalyzeMessageContent(msg, contentTestSecret); content.Key == "" {
		t.Errorf("content = %+v, want rich text key, not the placeholder", content)
	}
	fields := MessagePresentFields(msg)
	if containsString(fields, "text") {
		t.Errorf("present_fields = %#v, must not include routing placeholder text", fields)
	}
	if !containsString(fields, "rich_message") {
		t.Errorf("present_fields = %#v, want rich_message", fields)
	}
}
