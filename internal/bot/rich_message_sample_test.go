package bot

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"gopkg.in/telebot.v4"
)

func readRichSample(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/rich_message_sample.json")
	if err != nil {
		t.Fatalf("read rich message sample: %v", err)
	}
	return data
}

func decodeRichSample(t *testing.T, data []byte) *telebot.Message {
	t.Helper()
	var msg telebot.Message
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("decode sample message: %v", err)
	}
	return &msg
}

// realSamplePlainText 是线上样本（message_id 1000001）去掉格式后的可见文本。
// 列表项的 label（“•”）不参与内容，嵌套的 bold 片段保留文字。
const realSamplePlainText = "Introduction: Qwen\n" +
	"Hello! I am Qwen (Tongyi Qianwen), a large language model independently developed by Alibaba Group's Tongyi Lab.\n" +
	"About My Capabilities\n" +
	"As a large language model, I am designed to assist with a wide variety of tasks, including but not limited to:\n" +
	"Logical Reasoning: Solving complex mathematical problems and logical puzzles.\n" +
	"Coding: Writing, understanding, and debugging code across multiple programming languages.\n" +
	"Content Creation: Generating creative writing, marketing copy, and structured documents.\n" +
	"Multilingual Support: Communicating fluently in numerous languages.\n" +
	"Knowledge Cutoff\n" +
	"My training data has a knowledge cutoff date of 2026. This means my internal knowledge reflects information available up to that point. For events or developments occurring after my cutoff, I recommend consulting the latest available sources for the most accurate and up-to-date information.\n" +
	"I am here to help you with your questions, tasks, and creative projects. How can I assist you today?"

func TestRichMessageRealSampleDirect(t *testing.T) {
	msg := decodeRichSample(t, readRichSample(t))
	if msg.Text != "" || msg.Caption != "" {
		t.Fatalf("sample has text=%q caption=%q, want rich-only message", msg.Text, msg.Caption)
	}
	if msg.RichMessage == nil {
		t.Fatal("RichMessage = nil, telebot did not parse rich_message")
	}
	if got := len(msg.RichMessage.Blocks); got != 8 {
		t.Fatalf("blocks = %d, want 8", got)
	}
	if got := messagePlainText(msg); got != realSamplePlainText {
		t.Errorf("messagePlainText() = %q, want %q", got, realSamplePlainText)
	}

	content := AnalyzeMessageContent(msg, contentTestSecret)
	if content.Key == "" {
		t.Fatalf("content = %+v, want a text content key", content)
	}
	if content.Kind != "text" || content.KeyType != "文本" || content.SkipDecision != "" {
		t.Errorf("content = %+v, want text/文本 with no skip decision", content)
	}
}

// 线上 /hyw 读取的是命令消息里的 reply_to_message。这里用同一个真实样本
// 包进命令更新，验证 telebot 的 ReplyTo 反序列化也会填充 RichMessage。
func TestRichMessageRealSampleAsReplyTo(t *testing.T) {
	sample := readRichSample(t)
	prefix := []byte(`{"update_id":1,"message":{"message_id":1000002,"text":"/hyw","from":{"id":42,"is_bot":false,"first_name":"A"},"chat":{"id":-1001234567890,"type":"supergroup"},"reply_to_message":`)
	wrapped := append(prefix, sample...)
	wrapped = append(wrapped, []byte(`}}`)...)

	var update telebot.Update
	if err := json.Unmarshal(wrapped, &update); err != nil {
		t.Fatalf("decode /hyw update: %v", err)
	}
	if update.Message == nil || update.Message.ReplyTo == nil {
		t.Fatal("ReplyTo = nil, want the replied-to rich message")
	}
	target := update.Message.ReplyTo
	if target.RichMessage == nil {
		t.Fatal("ReplyTo.RichMessage = nil, telebot did not parse nested rich_message")
	}
	if got := len(target.RichMessage.Blocks); got != 8 {
		t.Fatalf("ReplyTo blocks = %d, want 8", got)
	}
	if got := messagePlainText(target); got != realSamplePlainText {
		t.Errorf("ReplyTo plain text = %q, want %q", got, realSamplePlainText)
	}

	direct := decodeRichSample(t, sample)
	if got, want := AnalyzeMessageContent(target, contentTestSecret).Key, AnalyzeMessageContent(direct, contentTestSecret).Key; got != want {
		t.Errorf("reply_to key = %q, want direct message key %q", got, want)
	}
}

func TestRichMessageEmojiAndMultibyte(t *testing.T) {
	const raw = `{
		"message_id": 51,
		"rich_message": {"blocks": [
			{"type": "paragraph", "text": ["火星救援 🚀 ", {"type": "bold", "text": "重复内容"}, " 😀 全角：ＡＢＣ"]}
		]}
	}`
	msg := decodeRichSample(t, []byte(raw))
	const want = "火星救援 🚀 重复内容 😀 全角：ＡＢＣ"
	if got := messagePlainText(msg); got != want {
		t.Errorf("messagePlainText() = %q, want %q", got, want)
	}
	if content := AnalyzeMessageContent(msg, contentTestSecret); content.Key == "" {
		t.Errorf("content = %+v, want non-empty key for emoji/multibyte text", content)
	}
}

// TestRichBlockPlainTextRobustness 覆盖官方块类型的提取规则，以及未知块、
// 没有 text 的块不能 panic 或吞掉其他块。
// TestRichSampleDedupedByProcessor 验证真实样本不只解析得出来，还能像
// 普通文本一样完成首次记录与第二次提醒的完整配对流程。
func TestRichSampleDedupedByProcessor(t *testing.T) {
	data := readRichSample(t)
	msg := decodeRichSample(t, data)
	p, _, _ := setupProcessor(t)
	buf := captureDecisionLog(t, p)
	replies := &replyRecorder{}

	if err := p.ProcessMessage(msg, "trace", replies.reply); err != nil {
		t.Fatalf("first ProcessMessage() error = %v", err)
	}
	lines := readDecisions(t, buf)
	if len(lines) != 1 {
		t.Fatalf("decision lines = %d, want 1", len(lines))
	}
	if lines[0].Decision != DecisionPairedSilent {
		t.Errorf("first decision = %q, want %q", lines[0].Decision, DecisionPairedSilent)
	}
	if lines[0].MsgType != "rich_message" {
		t.Errorf("first msg_type = %q, want rich_message", lines[0].MsgType)
	}
	if len(lines[0].KeyHash) != 8 {
		t.Errorf("first key_hash = %q, want 8 hex chars", lines[0].KeyHash)
	}
	if got := len(replies.snapshot()); got != 0 {
		t.Errorf("first replies = %d, want 0", got)
	}

	second := decodeRichSample(t, data)
	second.ID = msg.ID + 1
	if err := p.ProcessMessage(second, "trace", replies.reply); err != nil {
		t.Fatalf("second ProcessMessage() error = %v", err)
	}
	lines = readDecisions(t, buf)
	if len(lines) != 2 {
		t.Fatalf("decision lines = %d, want 2", len(lines))
	}
	if lines[1].Decision != DecisionAlerted {
		t.Errorf("second decision = %q, want %q", lines[1].Decision, DecisionAlerted)
	}
	if lines[1].KeyHash != lines[0].KeyHash {
		t.Errorf("key_hash changed: %q vs %q", lines[0].KeyHash, lines[1].KeyHash)
	}
	if got := len(replies.snapshot()); got != 1 {
		t.Errorf("second replies = %d, want 1", got)
	}
}

// TestTelebotDoesNotRouteRichMessage 固定 telebot 的原始行为：没有
// MiddlewarePoller 归一化时，富文本消息不会命中 OnText/OnMedia。
// 这正是 update_filter.go 需要补占位 Text 的原因。
func TestTelebotDoesNotRouteRichMessage(t *testing.T) {
	b := newOfflineBot(t)
	var events []string
	b.Handle(telebot.OnText, func(telebot.Context) error {
		events = append(events, "text")
		return nil
	})
	b.Handle(telebot.OnMedia, func(telebot.Context) error {
		events = append(events, "media")
		return nil
	})

	msg := decodeRichSample(t, readRichSample(t))
	msg.Chat = &telebot.Chat{ID: -1001234567890, Type: telebot.ChatSuperGroup}
	msg.Sender = &telebot.User{ID: 1000000001, FirstName: "Sample"}
	b.ProcessUpdate(telebot.Update{Message: msg})

	if len(events) != 0 {
		t.Errorf("telebot routed rich message to %#v, want no OnText/OnMedia handler", events)
	}
}

func TestRichBlockPlainTextRobustness(t *testing.T) {
	const raw = `{
		"message_id": 61,
		"rich_message": {"blocks": [
			{"type": "heading", "size": 1, "text": "Heading"},
			{"type": "paragraph", "text": [{"type": "bold", "text": "bold"}, " and ", {"type": "italic", "text": "italic"}]},
			{"type": "pre", "language": "go", "text": [{"type": "code", "text": "fmt.Println"}]},
			{"type": "footer", "text": "Footer"},
			{"type": "blockquote", "credit": "Credit", "blocks": [{"type": "paragraph", "text": "quoted"}]},
			{"type": "expandable_blockquote", "text": "expandable", "credit": "Expandable Credit"},
			{"type": "pullquote", "text": "pull", "credit": "Pull Credit"},
			{"type": "list", "items": [
				{"label": "1.", "blocks": [{"type": "paragraph", "text": "item one"}]},
				{"label": "2.", "blocks": [{"type": "paragraph", "text": "item two"}]}
			]},
			{"type": "table", "cells": [[{"text": "A"}, {"text": "B"}], [{"text": "C"}, {"text": "D"}]], "caption": "Table caption"},
			{"type": "details", "summary": "Summary", "blocks": [{"type": "paragraph", "text": "details body"}]},
			{"type": "photo", "photo": [{"file_id": "f", "file_unique_id": "u"}], "caption": {"text": "photo caption", "credit": "photo credit"}},
			{"type": "future_block", "text": "future text"},
			{"type": "mystery_block"},
			{"type": "divider"},
			{"type": "anchor", "name": "target"},
			{"type": "paragraph"}
		]}
	}`
	msg := decodeRichSample(t, []byte(raw))
	want := strings.Join([]string{
		"Heading",
		"bold and italic",
		"fmt.Println",
		"Footer",
		"quoted",
		"Credit",
		"expandable",
		"Expandable Credit",
		"pull",
		"Pull Credit",
		"item one",
		"item two",
		"A B",
		"C D",
		"Table caption",
		"Summary",
		"details body",
		"photo caption",
		"photo credit",
		"future text",
	}, "\n")
	if got := messagePlainText(msg); got != want {
		t.Errorf("messagePlainText() = %q, want %q", got, want)
	}

	types := messageRichBlockTypes(msg)
	for _, wantType := range []string{"blockquote", "details", "expandable_blockquote", "future_block", "list", "mystery_block", "photo", "table"} {
		if !containsString(types, wantType) {
			t.Errorf("block types = %#v, want containing %q", types, wantType)
		}
	}
	if !sortStrings(types) {
		t.Errorf("block types = %#v, want sorted", types)
	}
}

func TestRichMessageWithoutVisibleText(t *testing.T) {
	const raw = `{
		"message_id": 71,
		"chat": {"id": -1001234567890, "type": "supergroup"},
		"from": {"id": 7, "is_bot": false, "first_name": "A"},
		"rich_message": {"blocks": [{"type": "divider"}, {"type": "anchor", "name": "x"}, {"type": "future_block"}]}
	}`
	msg := decodeRichSample(t, []byte(raw))
	if got := messagePlainText(msg); got != "" {
		t.Errorf("messagePlainText() = %q, want empty", got)
	}
	content := AnalyzeMessageContent(msg, contentTestSecret)
	if content.Key != "" || content.SkipDecision != DecisionUnsupportedType {
		t.Errorf("content = %+v, want unsupported_type without a key", content)
	}

	fields := messagePresentFields(msg)
	for _, want := range []string{"chat", "from", "message_id", "rich_message"} {
		if !containsString(fields, want) {
			t.Errorf("present fields = %#v, want containing %q", fields, want)
		}
	}
	types := messageRichBlockTypes(msg)
	wantTypes := []string{"anchor", "divider", "future_block"}
	if strings.Join(types, ",") != strings.Join(wantTypes, ",") {
		t.Errorf("block types = %#v, want %#v", types, wantTypes)
	}
}

func TestMessagePresentFieldsExcludesEmptyValues(t *testing.T) {
	msg := &telebot.Message{
		ID: 81,
		Chat: &telebot.Chat{
			ID:   -1001234567890,
			Type: telebot.ChatSuperGroup,
		},
		Sender: &telebot.User{ID: 7, FirstName: "A"},
		RichMessage: &telebot.RichMessage{
			Blocks: []telebot.RichBlock{{Type: telebot.RichBlockParagraph}},
		},
	}
	fields := MessagePresentFields(msg)
	want := []string{"chat", "from", "message_id", "rich_message"}
	if strings.Join(fields, ",") != strings.Join(want, ",") {
		t.Errorf("present fields = %#v, want %#v", fields, want)
	}
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func sortStrings(list []string) bool {
	for i := 1; i < len(list); i++ {
		if list[i-1] > list[i] {
			return false
		}
	}
	return true
}
