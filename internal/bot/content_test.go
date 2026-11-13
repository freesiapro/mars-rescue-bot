package bot

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf16"

	"gopkg.in/telebot.v4"
)

const contentTestSecret = "content-test-secret"

func TestAnalyzeMessageContentIgnoresFormattingEntities(t *testing.T) {
	text := "😀 标题 bold 火星 italic underline strike spoiler code pre link quote expand emoji tail"
	plain := textMessage(testChatID, 1, "A", text, 10)

	formatted := textMessage(testChatID, 2, "B", text, 11)
	entities := []struct {
		entityType telebot.EntityType
		token      string
		url        string
		language   string
	}{
		{telebot.EntityBold, "bold", "", ""},
		{telebot.EntityItalic, "italic", "", ""},
		{telebot.EntityUnderline, "underline", "", ""},
		{telebot.EntityStrikethrough, "strike", "", ""},
		{telebot.EntitySpoiler, "spoiler", "", ""},
		{telebot.EntityCode, "code", "", ""},
		{telebot.EntityCodeBlock, "pre", "", "go"},
		{telebot.EntityTextLink, "link", "https://example.com/post", ""},
		{telebot.EntityBlockquote, "quote", "", ""},
		{telebot.EntityEBlockquote, "expand", "", ""},
		{telebot.EntityCustomEmoji, "emoji", "", ""},
	}
	for _, tt := range entities {
		offset := utf16Offset(text, tt.token)
		if offset < 0 {
			t.Fatalf("token %q not found in %q", tt.token, text)
		}
		formatted.Entities = append(formatted.Entities, telebot.MessageEntity{
			Type:          tt.entityType,
			Offset:        offset,
			Length:        utf16Length(tt.token),
			URL:           tt.url,
			Language:      tt.language,
			CustomEmojiID: "5368324170671202286",
		})
	}

	plainContent := AnalyzeMessageContent(plain, contentTestSecret)
	formattedContent := AnalyzeMessageContent(formatted, contentTestSecret)
	if plainContent.Key == "" || formattedContent.Key == "" {
		t.Fatalf("keys = %q / %q, want non-empty", plainContent.Key, formattedContent.Key)
	}
	if plainContent.Key != formattedContent.Key {
		t.Errorf("formatted key = %q, want plain key %q", formattedContent.Key, plainContent.Key)
	}
	if formattedContent.Kind != "text" || formattedContent.KeyType != "文本" {
		t.Errorf("content = %+v, want text/文本", formattedContent)
	}

	// 实体只标记 UTF-16 范围，不参与内容键计算；越界实体也不能触发 panic 或改变结果。
	formatted.Entities = append(formatted.Entities, telebot.MessageEntity{
		Type: telebot.EntityBold, Offset: 1 << 20, Length: 99,
	})
	again := AnalyzeMessageContent(formatted, contentTestSecret)
	if again.Key != plainContent.Key {
		t.Errorf("out-of-range entity key = %q, want %q", again.Key, plainContent.Key)
	}
	if got := formatted.EntityText(telebot.MessageEntity{
		Type: telebot.EntityBold, Offset: 1 << 20, Length: 99,
	}); got != "" {
		t.Errorf("EntityText(out of range) = %q, want empty", got)
	}
}

func TestAnalyzeMessageContentRichTextMatchesPlainText(t *testing.T) {
	const richJSON = `{
		"message_id": 42,
		"rich_message": {
			"blocks": [
				{"type": "heading", "size": 1, "text": [{"type": "bold", "text": "Hello"}, " ", {"type": "italic", "text": "Mars"}]},
				{"type": "paragraph", "text": ["See ", {"type": "text_link", "text": "the post", "url": "https://example.com"}, " ", {"type": "custom_emoji", "custom_emoji_id": "123", "alternative_text": "😀"}, " ", {"type": "spoiler", "text": "now"}]},
				{"type": "blockquote", "credit": "Credit", "blocks": [{"type": "paragraph", "text": [{"type": "underline", "text": "quoted"}, " ", {"type": "strikethrough", "text": "text"}]}]},
				{"type": "pre", "language": "go", "text": [{"type": "code", "text": "fmt.Println"}]},
				{"type": "list", "items": [
					{"blocks": [{"type": "paragraph", "text": "first item"}]},
					{"blocks": [{"type": "paragraph", "text": "second item"}]}
				]},
				{"type": "table", "cells": [[{"text": "a"}, {"text": "b"}], [{"text": "c"}, {"text": "d"}]], "caption": "table cap"},
				{"type": "details", "summary": "Summary", "blocks": [{"type": "paragraph", "text": "details body"}]}
			]
		}
	}`
	var rich telebot.Message
	if err := json.Unmarshal([]byte(richJSON), &rich); err != nil {
		t.Fatalf("decode rich message: %v", err)
	}
	if rich.Text != "" {
		t.Fatalf("Text = %q, want empty for a rich message", rich.Text)
	}
	if rich.RichMessage == nil {
		t.Fatal("RichMessage = nil, want decoded rich message")
	}

	wantText := strings.Join([]string{
		"Hello Mars",
		"See the post 😀 now",
		"quoted text",
		"Credit",
		"fmt.Println",
		"first item",
		"second item",
		"a b",
		"c d",
		"table cap",
		"Summary",
		"details body",
	}, "\n")
	if got := messagePlainText(&rich); got != wantText {
		t.Errorf("messagePlainText(rich) = %q, want %q", got, wantText)
	}

	plain := textMessage(testChatID, 1, "A", wantText, 10)
	richContent := AnalyzeMessageContent(&rich, contentTestSecret)
	plainContent := AnalyzeMessageContent(plain, contentTestSecret)
	if richContent.Key == "" || plainContent.Key == "" {
		t.Fatalf("keys = %q / %q, want non-empty", richContent.Key, plainContent.Key)
	}
	if richContent.Key != plainContent.Key {
		t.Errorf("rich key = %q, want plain key %q", richContent.Key, plainContent.Key)
	}
	if richContent.Kind != "text" || richContent.KeyType != "文本" {
		t.Errorf("rich content = %+v, want text/文本", richContent)
	}
}

func TestAnalyzeMessageContentRichMediaCaption(t *testing.T) {
	const richJSON = `{
		"message_id": 43,
		"rich_message": {
			"blocks": [
				{"type": "photo", "photo": [{"file_id": "small", "file_unique_id": "photo-small"}, {"file_id": "large", "file_unique_id": "photo-large"}], "caption": {"text": [{"type": "bold", "text": "captioned"}, " ", {"type": "italic", "text": "photo in rich text"}]}}
			]
		}
	}`
	var rich telebot.Message
	if err := json.Unmarshal([]byte(richJSON), &rich); err != nil {
		t.Fatalf("decode rich message: %v", err)
	}

	content := AnalyzeMessageContent(&rich, contentTestSecret)
	if content.Key == "" {
		t.Fatalf("content = %+v, want a content key from the rich caption", content)
	}
	if content.Kind != "text" || content.KeyType != "文本" {
		t.Errorf("content = %+v, want rich captions deduped as text", content)
	}

	plain := textMessage(testChatID, 1, "A", "captioned photo in rich text", 10)
	plainContent := AnalyzeMessageContent(plain, contentTestSecret)
	if content.Key != plainContent.Key {
		t.Errorf("rich caption key = %q, want plain text key %q", content.Key, plainContent.Key)
	}
}

func TestAnalyzeMessageContentMediaCaptionStaysMediaKeyed(t *testing.T) {
	formatted := textMessage(testChatID, 1, "A", "", 10)
	formatted.Photo = &telebot.Photo{File: telebot.File{UniqueID: "photo-unique"}}
	formatted.Caption = "formatted caption"
	formatted.CaptionEntities = []telebot.MessageEntity{
		{Type: telebot.EntityBold, Offset: 0, Length: 9},
		{Type: telebot.EntitySpoiler, Offset: 10, Length: 7},
	}

	plain := textMessage(testChatID, 2, "B", "", 11)
	plain.Photo = &telebot.Photo{File: telebot.File{UniqueID: "photo-unique"}}
	plain.Caption = "another caption"

	for name, msg := range map[string]*telebot.Message{"formatted": formatted, "plain": plain} {
		content := AnalyzeMessageContent(msg, contentTestSecret)
		if content.Key != "media:photo:photo-unique" {
			t.Errorf("%s key = %q, want media:photo:photo-unique", name, content.Key)
		}
		if content.Kind != "media" || content.KeyType != "图片" {
			t.Errorf("%s content = %+v, want media/图片", name, content)
		}
	}
}

func TestAnalyzeMessageContentRichMessageWithoutText(t *testing.T) {
	rich := textMessage(testChatID, 1, "A", "", 10)
	rich.RichMessage = &telebot.RichMessage{
		Blocks: []telebot.RichBlock{{Type: telebot.RichBlockDivider}},
	}
	content := AnalyzeMessageContent(rich, contentTestSecret)
	if content.Key != "" {
		t.Errorf("key = %q, want empty for a rich message with no text", content.Key)
	}
	if content.SkipDecision != DecisionUnsupportedType {
		t.Errorf("decision = %q, want %q", content.SkipDecision, DecisionUnsupportedType)
	}
}

func TestAnalyzeMessageContentContentScoreThreshold(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantSkip string
		wantKey  bool
		wantKind string
	}{
		{name: "emoji decorated two words", text: "Product ⬆️ Engineer", wantSkip: DecisionShortTextSkipped},
		{name: "thanks a lot", text: "Thanks a lot", wantSkip: DecisionShortTextSkipped},
		{name: "good morning everyone", text: "Good morning everyone", wantSkip: DecisionShortTextSkipped},
		{name: "i agree with you", text: "I agree with you", wantSkip: DecisionShortTextSkipped},
		{name: "five latin words", text: "Please review this pull request", wantKey: true, wantKind: "text"},
		{name: "nine chinese characters", text: "这个方案还要再讨论", wantSkip: DecisionShortTextSkipped},
		{name: "ten chinese characters", text: "这个方案还需要再讨论", wantKey: true, wantKind: "text"},
		{name: "mixed reaches ten", text: "明天 meeting 改到下午三点", wantKey: true, wantKind: "text"},
		{name: "mixed below ten", text: "看下 documentation 谢谢", wantSkip: DecisionShortTextSkipped},
		{name: "price is four words", text: "100 USD per month", wantSkip: DecisionShortTextSkipped},
		{name: "mention plus short content", text: "@Alice 今天天气不错", wantSkip: DecisionShortTextSkipped},
		{name: "pure link", text: "https://example.com/a/b", wantKey: true, wantKind: "link"},
		{name: "pure emoji", text: "🚀🚀🚀", wantSkip: DecisionNoContent},
		{name: "repeated single rune", text: "哈哈哈哈哈哈哈哈哈哈", wantSkip: DecisionRepeatedCharSkipped},
		{name: "long cyrillic", text: "привет мир как", wantKey: true, wantKind: "text"},
		{name: "short cyrillic", text: "привет мир", wantSkip: DecisionShortTextSkipped},
		{name: "long arabic", text: "مرحبا بالعالم", wantKey: true, wantKind: "text"},
		{name: "short arabic", text: "مرحبا", wantSkip: DecisionShortTextSkipped},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := AnalyzeMessageContent(textMessage(testChatID, 1, "A", tt.text, 10), contentTestSecret)
			if content.SkipDecision != tt.wantSkip {
				t.Errorf("SkipDecision = %q, want %q (content = %+v)", content.SkipDecision, tt.wantSkip, content)
			}
			if tt.wantSkip != "" && content.Key != "" {
				t.Errorf("Key = %q, want empty for skipped text", content.Key)
			}
			if tt.wantKey && content.Key == "" {
				t.Errorf("Key is empty, want a content key (content = %+v)", content)
			}
			if tt.wantKind != "" && content.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", content.Kind, tt.wantKind)
			}
		})
	}
}

func utf16Offset(text, token string) int {
	idx := strings.Index(text, token)
	if idx < 0 {
		return -1
	}
	return len(utf16.Encode([]rune(text[:idx])))
}

func utf16Length(text string) int {
	return len(utf16.Encode([]rune(text)))
}
