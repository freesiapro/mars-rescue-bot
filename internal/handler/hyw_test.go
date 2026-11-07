package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"gopkg.in/telebot.v4"
	"gorm.io/gorm"
	"mars-rescue-bot/internal/bot"
	"mars-rescue-bot/internal/models"
	"mars-rescue-bot/internal/names"
	"mars-rescue-bot/internal/repo"
	"mars-rescue-bot/internal/testutil"
)

const (
	hywTestChatID = int64(-1009876543210)
	hywTestSecret = "hyw-test-secret"
)

type hywCall struct {
	text string
	opts []interface{}
}

type fakeHYWContext struct {
	telebot.Context
	msg     *telebot.Message
	sent    []hywCall
	replied []hywCall
}

func (f *fakeHYWContext) Message() *telebot.Message { return f.msg }

func (f *fakeHYWContext) Send(what interface{}, opts ...interface{}) error {
	f.sent = append(f.sent, hywCall{text: fmt.Sprint(what), opts: opts})
	return nil
}

func (f *fakeHYWContext) Reply(what interface{}, opts ...interface{}) error {
	f.replied = append(f.replied, hywCall{text: fmt.Sprint(what), opts: opts})
	return nil
}

type hywLogLine struct {
	Level     string `json:"level"`
	Msg       string `json:"msg"`
	ChatID    int64  `json:"chat_id"`
	MessageID int    `json:"message_id"`
	MsgType   string `json:"msg_type"`
	Decision  string `json:"decision"`
	UserID    int64  `json:"user_id"`
	KeyHash   string `json:"key_hash"`
}

func setupHYW(t *testing.T) (*CommandHandler, *repo.Repo, *gorm.DB) {
	t.Helper()
	db := testutil.SetupDB(t,
		&models.ForwardRecord{},
		&models.ForwardLog{},
		&models.UserStats{},
		&models.ChatSettings{},
		&models.ChatIgnoredUser{},
	)
	r := repo.New(db)
	h := NewCommandHandler(r, nil, names.New(nil), hywTestSecret)
	h.now = func() time.Time { return time.Date(2026, 11, 4, 15, 0, 0, 0, time.UTC) }
	h.statsLoc = time.UTC
	return h, r, db
}

func hywTextMessage(chatID, userID int64, text string, msgID int) *telebot.Message {
	return &telebot.Message{
		ID:     msgID,
		Text:   text,
		Sender: &telebot.User{ID: userID, FirstName: "HYW", Username: "hyw"},
		Chat:   &telebot.Chat{ID: chatID, Type: telebot.ChatSuperGroup},
	}
}

func hywPhotoMessage(chatID, userID int64, uniqueID string, msgID int) *telebot.Message {
	msg := hywTextMessage(chatID, userID, "", msgID)
	msg.Photo = &telebot.Photo{File: telebot.File{UniqueID: uniqueID}}
	return msg
}

func hywChannelMessage(chatID, userID int64, originChatID int64, originMsgID, msgID int) *telebot.Message {
	msg := hywTextMessage(chatID, userID, "channel post", msgID)
	msg.Origin = &telebot.MessageOrigin{
		Type:      "channel",
		Chat:      &telebot.Chat{ID: originChatID, Type: telebot.ChatChannel},
		MessageID: originMsgID,
	}
	return msg
}

func hywCommandMessage(chatID int64, target *telebot.Message) *telebot.Message {
	return &telebot.Message{
		ID:      9000,
		Text:    "/hyw",
		Sender:  &telebot.User{ID: 42, FirstName: "Asker", Username: "asker"},
		Chat:    &telebot.Chat{ID: chatID, Type: telebot.ChatSuperGroup},
		ReplyTo: target,
	}
}

// seedHYWMessage 用与线上检测相同的纯函数算出内容键，再走仓储层写入一次出现。
// 它不经过 Processor，便于在 handler 包里精确构造配对/相邻状态。
func seedHYWMessage(t *testing.T, r *repo.Repo, msg *telebot.Message, adjacentGap int) *models.ForwardRecord {
	t.Helper()
	content := bot.AnalyzeMessageContent(msg, hywTestSecret)
	if content.Key == "" {
		t.Fatalf("seed message %d has no content key: %+v", msg.ID, content)
	}
	if err := r.UpsertUserStats(msg.Sender.ID); err != nil {
		t.Fatalf("UpsertUserStats(user %d) error = %v", msg.Sender.ID, err)
	}
	record, isNew, err := r.FindOrCreateRecord(msg.Chat.ID, content.Key, msg.ID, msg.Sender.ID)
	if err != nil {
		t.Fatalf("FindOrCreateRecord(msg %d) error = %v", msg.ID, err)
	}
	if isNew {
		return record
	}
	if _, err := r.DecideAndLog(record, msg.ID, msg.Sender.ID, adjacentGap); err != nil {
		t.Fatalf("DecideAndLog(msg %d) error = %v", msg.ID, err)
	}
	return record
}

func captureDefaultLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	old := slog.Default()
	buf := &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

func parseHYWLogs(t *testing.T, buf *bytes.Buffer) []hywLogLine {
	t.Helper()
	var out []hywLogLine
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var line hywLogLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("decode log %q: %v", raw, err)
		}
		out = append(out, line)
	}
	return out
}

func runHYW(t *testing.T, h *CommandHandler, chatID int64, target *telebot.Message) (*fakeHYWContext, []hywLogLine) {
	t.Helper()
	buf := captureDefaultLog(t)
	ctx := &fakeHYWContext{msg: hywCommandMessage(chatID, target)}
	if err := h.handleHYW(ctx); err != nil {
		t.Fatalf("handleHYW() error = %v", err)
	}
	lines := parseHYWLogs(t, buf)
	if len(lines) != 1 {
		t.Fatalf("decision lines = %d, want exactly 1: %s", len(lines), buf.String())
	}
	line := lines[0]
	if line.Msg != "dedup decision" || line.Decision != bot.DecisionCommand {
		t.Errorf("command log = %+v, want dedup decision / command", line)
	}
	if line.MsgType != "text" {
		t.Errorf("command msg_type = %q, want text", line.MsgType)
	}
	if line.KeyHash != "" {
		t.Errorf("command key_hash = %q, want omitted", line.KeyHash)
	}
	return ctx, lines
}

func assertHYWReplyToTarget(t *testing.T, ctx *fakeHYWContext, target *telebot.Message) string {
	t.Helper()
	if len(ctx.sent) != 1 {
		t.Fatalf("sent = %#v, want exactly 1", ctx.sent)
	}
	if len(ctx.replied) != 0 {
		t.Fatalf("replied = %#v, want none", ctx.replied)
	}
	opts := ctx.sent[0].opts
	if len(opts) != 1 {
		t.Fatalf("send opts = %#v, want one SendOptions", opts)
	}
	sendOpts, ok := opts[0].(*telebot.SendOptions)
	if !ok {
		t.Fatalf("send opts type = %T, want *telebot.SendOptions", opts[0])
	}
	if sendOpts.ReplyTo != target {
		t.Errorf("ReplyTo = %p, want queried message %p", sendOpts.ReplyTo, target)
	}
	if !sendOpts.AllowWithoutReply {
		t.Error("AllowWithoutReply = false, want true")
	}
	return ctx.sent[0].text
}

func TestHYWRecordedRealRichSample(t *testing.T) {
	h, r, _ := setupHYW(t)
	data, err := os.ReadFile("../bot/testdata/rich_message_sample.json")
	if err != nil {
		t.Fatalf("read rich sample: %v", err)
	}
	var target telebot.Message
	if err := json.Unmarshal(data, &target); err != nil {
		t.Fatalf("decode rich sample: %v", err)
	}
	if target.RichMessage == nil {
		t.Fatal("RichMessage = nil, want decoded sample")
	}
	target.Chat = &telebot.Chat{ID: hywTestChatID, Type: telebot.ChatSuperGroup}
	seedHYWMessage(t, r, &target, 0)

	ctx, _ := runHYW(t, h, hywTestChatID, &target)
	reply := assertHYWReplyToTarget(t, ctx, &target)
	if !strings.Contains(reply, "📒 已记录：文本 · 短码 ") {
		t.Errorf("reply = %q, want rich sample deduped as text", reply)
	}
	content := bot.AnalyzeMessageContent(&target, hywTestSecret)
	if !strings.Contains(reply, content.Hash) {
		t.Errorf("reply = %q, want 8-char hash %q", reply, content.Hash)
	}
	if strings.Contains(reply, "Introduction: Qwen") || strings.Contains(reply, content.Key) {
		t.Errorf("reply = %q, must not contain sample text or full key", reply)
	}
	t.Logf("hyw real rich reply: %s", reply)
}

func TestHYWReplyToBotRichMessage(t *testing.T) {
	h, _, _ := setupHYW(t)
	data, err := os.ReadFile("../bot/testdata/rich_message_sample.json")
	if err != nil {
		t.Fatalf("read rich sample: %v", err)
	}
	var target telebot.Message
	if err := json.Unmarshal(data, &target); err != nil {
		t.Fatalf("decode rich sample: %v", err)
	}
	target.Sender.IsBot = true

	ctx, _ := runHYW(t, h, hywTestChatID, &target)
	reply := assertHYWReplyToTarget(t, ctx, &target)
	if !strings.Contains(reply, "机器人发送") {
		t.Errorf("reply = %q, want bot explanation before content analysis", reply)
	}
	t.Logf("hyw bot rich reply: %s", reply)
}

func TestHYWNoContentIncludesPresentFields(t *testing.T) {
	h, _, _ := setupHYW(t)
	target := hywTextMessage(hywTestChatID, 1, "", 77)

	ctx, _ := runHYW(t, h, hywTestChatID, target)
	reply := assertHYWReplyToTarget(t, ctx, target)
	if !strings.Contains(reply, "没有可记录") {
		t.Errorf("reply = %q, want no-content explanation", reply)
	}
	for _, want := range []string{"字段：", "message_id", "chat", "from"} {
		if !strings.Contains(reply, want) {
			t.Errorf("reply = %q, want containing %q", reply, want)
		}
	}
	if strings.Contains(reply, "HYW") {
		t.Errorf("reply = %q, must not contain field values", reply)
	}
	t.Logf("hyw no-content reply: %s", reply)
}

func TestHYWRecordedText(t *testing.T) {
	h, r, _ := setupHYW(t)
	first := hywTextMessage(hywTestChatID, 1, "hyw recorded text", 10)
	second := hywTextMessage(hywTestChatID, 2, "hyw recorded text", 16)
	seedHYWMessage(t, r, first, 0)
	seedHYWMessage(t, r, second, 0)

	ctx, _ := runHYW(t, h, hywTestChatID, second)
	reply := assertHYWReplyToTarget(t, ctx, second)
	for _, want := range []string{
		"📒 已记录：文本 · 短码 ",
		"出现 2 次",
		"最早 ",
		"最近 ",
		"本次已发出提醒",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("reply = %q, want containing %q", reply, want)
		}
	}
	content := bot.AnalyzeMessageContent(second, hywTestSecret)
	if !strings.Contains(reply, content.Hash) {
		t.Errorf("reply = %q, want 8-char hash %q", reply, content.Hash)
	}
	if strings.Contains(reply, content.Key) {
		t.Errorf("reply = %q, must not contain full content key", reply)
	}
	if strings.Contains(reply, "hyw recorded text") {
		t.Errorf("reply = %q, must not contain message text", reply)
	}
	t.Logf("hyw reply: %s", reply)
}

func TestHYWRecordedPureLink(t *testing.T) {
	h, r, _ := setupHYW(t)
	const link = "https://example.com/a/b?x=1"
	first := hywTextMessage(hywTestChatID, 1, link, 10)
	second := hywTextMessage(hywTestChatID, 2, link, 16)
	seedHYWMessage(t, r, first, 0)
	seedHYWMessage(t, r, second, 0)

	ctx, _ := runHYW(t, h, hywTestChatID, second)
	reply := assertHYWReplyToTarget(t, ctx, second)
	for _, want := range []string{
		"📒 已记录：链接 · 短码 ",
		"出现 2 次",
		"本次已发出提醒",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("reply = %q, want containing %q", reply, want)
		}
	}
	content := bot.AnalyzeMessageContent(second, hywTestSecret)
	if !strings.Contains(reply, content.Hash) {
		t.Errorf("reply = %q, want 8-char hash %q", reply, content.Hash)
	}
	if strings.Contains(reply, "example.com") || strings.Contains(reply, content.Key) {
		t.Errorf("reply = %q, must not contain the URL or full content key", reply)
	}
	t.Logf("hyw link reply: %s", reply)
}

func TestHYWRecordedRichMessage(t *testing.T) {
	h, r, _ := setupHYW(t)
	plain := hywTextMessage(hywTestChatID, 1, "hyw rich text", 10)
	rich := hywTextMessage(hywTestChatID, 2, "", 16)
	const richJSON = `{
		"message_id": 16,
		"rich_message": {
			"blocks": [
				{"type": "paragraph", "text": [{"type": "bold", "text": "hyw rich"}, " ", {"type": "italic", "text": "text"}]}
			]
		}
	}`
	if err := json.Unmarshal([]byte(richJSON), &rich); err != nil {
		t.Fatalf("decode rich message: %v", err)
	}
	seedHYWMessage(t, r, plain, 0)
	seedHYWMessage(t, r, rich, 0)

	ctx, _ := runHYW(t, h, hywTestChatID, rich)
	reply := assertHYWReplyToTarget(t, ctx, rich)
	for _, want := range []string{
		"📒 已记录：文本 · 短码 ",
		"出现 2 次",
		"本次已发出提醒",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("reply = %q, want containing %q", reply, want)
		}
	}
	content := bot.AnalyzeMessageContent(rich, hywTestSecret)
	plainContent := bot.AnalyzeMessageContent(plain, hywTestSecret)
	if content.Key != plainContent.Key {
		t.Errorf("rich key = %q, want plain key %q", content.Key, plainContent.Key)
	}
	if !strings.Contains(reply, content.Hash) {
		t.Errorf("reply = %q, want 8-char hash %q", reply, content.Hash)
	}
	if strings.Contains(reply, content.Key) || strings.Contains(reply, "hyw rich text") {
		t.Errorf("reply = %q, must not contain full key or message text", reply)
	}
	t.Logf("hyw rich reply: %s", reply)
}

func TestHYWFirstOccurrenceDecision(t *testing.T) {
	h, r, _ := setupHYW(t)
	first := hywTextMessage(hywTestChatID, 1, "hyw first occurrence", 10)
	second := hywTextMessage(hywTestChatID, 2, "hyw first occurrence", 16)
	seedHYWMessage(t, r, first, 0)
	seedHYWMessage(t, r, second, 0)

	ctx, _ := runHYW(t, h, hywTestChatID, first)
	reply := assertHYWReplyToTarget(t, ctx, first)
	if !strings.Contains(reply, "第一次出现，已记录，未提醒") {
		t.Errorf("reply = %q, want first-occurrence paired_silent", reply)
	}
}

func TestHYWAdjacentDecision(t *testing.T) {
	h, r, _ := setupHYW(t)
	first := hywTextMessage(hywTestChatID, 1, "hyw adjacent text", 10)
	second := hywTextMessage(hywTestChatID, 2, "hyw adjacent text", 15)
	seedHYWMessage(t, r, first, 0)
	seedHYWMessage(t, r, second, bot.AdjacentRepeatGap)

	ctx, _ := runHYW(t, h, hywTestChatID, second)
	reply := assertHYWReplyToTarget(t, ctx, second)
	if !strings.Contains(reply, "连续刷屏，未发送提醒") {
		t.Errorf("reply = %q, want adjacent exemption", reply)
	}
	t.Logf("hyw adjacent reply: %s", reply)
}

func TestHYWRecordedMedia(t *testing.T) {
	h, r, _ := setupHYW(t)
	const uniqueID = "hyw-photo-unique"
	first := hywPhotoMessage(hywTestChatID, 1, uniqueID, 20)
	second := hywPhotoMessage(hywTestChatID, 2, uniqueID, 26)
	seedHYWMessage(t, r, first, 0)
	seedHYWMessage(t, r, second, 0)

	ctx, _ := runHYW(t, h, hywTestChatID, second)
	reply := assertHYWReplyToTarget(t, ctx, second)
	if !strings.Contains(reply, "📒 已记录：图片 · 短码 ") {
		t.Errorf("reply = %q, want photo key type", reply)
	}
	if !strings.Contains(reply, "出现 2 次") {
		t.Errorf("reply = %q, want cumulative count", reply)
	}
	for _, secret := range []string{uniqueID, "media:photo:" + uniqueID} {
		if strings.Contains(reply, secret) {
			t.Errorf("reply = %q, must not contain raw media key %q", reply, secret)
		}
	}
	t.Logf("hyw media reply: %s", reply)
}

func TestHYWRecordedChannelForward(t *testing.T) {
	h, r, _ := setupHYW(t)
	const originChatID = int64(-100777)
	first := hywChannelMessage(hywTestChatID, 1, originChatID, 55, 30)
	second := hywChannelMessage(hywTestChatID, 2, originChatID, 55, 36)
	seedHYWMessage(t, r, first, 0)
	seedHYWMessage(t, r, second, 0)

	ctx, _ := runHYW(t, h, hywTestChatID, second)
	reply := assertHYWReplyToTarget(t, ctx, second)
	if !strings.Contains(reply, "📒 已记录：频道转发 · 短码 ") {
		t.Errorf("reply = %q, want channel-forward key type", reply)
	}
	for _, secret := range []string{"-100777", "channel:-100777:55"} {
		if strings.Contains(reply, secret) {
			t.Errorf("reply = %q, must not contain raw channel key %q", reply, secret)
		}
	}
	t.Logf("hyw channel reply: %s", reply)
}

func TestHYWUnknownDecisionDoesNotFabricate(t *testing.T) {
	h, r, _ := setupHYW(t)
	base := hywTextMessage(hywTestChatID, 1, "hyw cooldown text", 10)
	content := bot.AnalyzeMessageContent(base, hywTestSecret)
	if err := r.UpsertUserStats(base.Sender.ID); err != nil {
		t.Fatalf("UpsertUserStats() error = %v", err)
	}
	if _, _, err := r.FindOrCreateRecord(hywTestChatID, content.Key, base.ID, base.Sender.ID); err != nil {
		t.Fatalf("FindOrCreateRecord(first) error = %v", err)
	}

	// 第 2 次提醒成功，第 3 次静默开新对，第 4 次本该提醒但被冷却抑制并回滚。
	// 库里只剩“最新一次 + 仍有待配对”的证据，无法区分静默配对和冷却回滚。
	for _, item := range []struct {
		msgID  int
		userID int64
	}{
		{16, 2},
		{22, 3},
		{28, 4},
	} {
		msg := hywTextMessage(hywTestChatID, item.userID, "hyw cooldown text", item.msgID)
		record, _, err := r.FindOrCreateRecord(hywTestChatID, content.Key, msg.ID, msg.Sender.ID)
		if err != nil {
			t.Fatalf("FindOrCreateRecord(msg %d) error = %v", msg.ID, err)
		}
		decision, err := r.DecideAndLog(record, msg.ID, msg.Sender.ID, 0)
		if err != nil {
			t.Fatalf("DecideAndLog(msg %d) error = %v", msg.ID, err)
		}
		if msg.ID == 28 {
			if err := r.RevertPairClaim(record, decision.Seq); err != nil {
				t.Fatalf("RevertPairClaim() error = %v", err)
			}
		}
	}

	ctx, _ := runHYW(t, h, hywTestChatID, hywTextMessage(hywTestChatID, 4, "hyw cooldown text", 28))
	reply := assertHYWReplyToTarget(t, ctx, ctx.msg.ReplyTo)
	if !strings.Contains(reply, "没法确定") {
		t.Errorf("reply = %q, want honest unknown decision", reply)
	}
	if strings.Contains(reply, "决策：alerted") {
		t.Errorf("reply = %q, must not fabricate alerted", reply)
	}
	t.Logf("hyw unknown reply: %s", reply)
}

func TestHYWNotRecorded(t *testing.T) {
	h, _, _ := setupHYW(t)
	target := hywTextMessage(hywTestChatID, 1, "hyw never recorded", 10)

	ctx, _ := runHYW(t, h, hywTestChatID, target)
	reply := assertHYWReplyToTarget(t, ctx, target)
	if !strings.Contains(reply, "没有这条记录") {
		t.Errorf("reply = %q, want not-recorded copy", reply)
	}
	t.Logf("hyw not recorded reply: %s", reply)
}

func TestHYWNoReply(t *testing.T) {
	h, _, _ := setupHYW(t)
	ctx, _ := runHYW(t, h, hywTestChatID, nil)
	if len(ctx.sent) != 0 {
		t.Fatalf("sent = %#v, want none", ctx.sent)
	}
	if len(ctx.replied) != 1 {
		t.Fatalf("replied = %#v, want one usage hint", ctx.replied)
	}
	if !strings.Contains(ctx.replied[0].text, "/hyw") {
		t.Errorf("usage = %q, want /hyw hint", ctx.replied[0].text)
	}
	t.Logf("hyw usage reply: %s", ctx.replied[0].text)
}

func TestHYWReplyToBot(t *testing.T) {
	h, _, _ := setupHYW(t)
	target := hywTextMessage(hywTestChatID, 1, "bot content", 10)
	target.Sender.IsBot = true

	ctx, _ := runHYW(t, h, hywTestChatID, target)
	reply := assertHYWReplyToTarget(t, ctx, target)
	if !strings.Contains(reply, "机器人发送") {
		t.Errorf("reply = %q, want bot explanation", reply)
	}
	t.Logf("hyw bot reply: %s", reply)
}

func TestHYWSkipReasons(t *testing.T) {
	tests := []struct {
		name   string
		target func() *telebot.Message
		want   string
	}{
		{
			name: "short text",
			target: func() *telebot.Message {
				return hywTextMessage(hywTestChatID, 1, "hi", 10)
			},
			want: "太短",
		},
		{
			name: "repeated char",
			target: func() *telebot.Message {
				return hywTextMessage(hywTestChatID, 1, "哈哈哈哈哈哈哈哈哈哈", 11)
			},
			want: "同一个字",
		},
		{
			name: "mention only",
			target: func() *telebot.Message {
				msg := hywTextMessage(hywTestChatID, 1, "@Bob", 15)
				msg.Entities = telebot.Entities{
					{Type: telebot.EntityMention, Offset: 0, Length: 4},
				}
				return msg
			},
			want: "只有 @",
		},
		{
			name: "mention only rich",
			target: func() *telebot.Message {
				msg := hywTextMessage(hywTestChatID, 1, "", 16)
				msg.RichMessage = &telebot.RichMessage{
					Blocks: []telebot.RichBlock{{
						Type: telebot.RichBlockParagraph,
						Text: &telebot.RichText{
							Kind: telebot.RichTextArray,
							Parts: []telebot.RichText{{
								Kind:     telebot.RichTextEntity,
								Type:     telebot.RichMention,
								Username: "Bob",
							}},
						},
					}},
				}
				return msg
			},
			want: "只有 @",
		},
		{
			name: "mention only channel forward",
			target: func() *telebot.Message {
				msg := hywTextMessage(hywTestChatID, 1, "@Bob", 17)
				msg.Entities = telebot.Entities{
					{Type: telebot.EntityMention, Offset: 0, Length: 4},
				}
				msg.Origin = &telebot.MessageOrigin{
					Type:      "channel",
					Chat:      &telebot.Chat{ID: -1001234500000, Type: telebot.ChatChannel},
					MessageID: 9201,
				}
				return msg
			},
			want: "只有 @",
		},
		{
			name: "command",
			target: func() *telebot.Message {
				return hywTextMessage(hywTestChatID, 1, "/mars_rank", 12)
			},
			want: "命令",
		},
		{
			name: "empty content",
			target: func() *telebot.Message {
				return hywTextMessage(hywTestChatID, 1, "", 13)
			},
			want: "没有可记录",
		},
		{
			name: "unsupported audio",
			target: func() *telebot.Message {
				msg := hywTextMessage(hywTestChatID, 1, "", 14)
				msg.Audio = &telebot.Audio{File: telebot.File{UniqueID: "audio"}}
				return msg
			},
			want: "不参与",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _, _ := setupHYW(t)
			target := tt.target()
			ctx, _ := runHYW(t, h, hywTestChatID, target)
			reply := assertHYWReplyToTarget(t, ctx, target)
			if !strings.Contains(reply, tt.want) {
				t.Errorf("reply = %q, want containing %q", reply, tt.want)
			}
			t.Logf("hyw skip reply: %s", reply)
		})
	}
}

func TestHYWOnlyCurrentChat(t *testing.T) {
	h, r, _ := setupHYW(t)
	const otherChatID = int64(-100111222333)
	other := hywTextMessage(otherChatID, 1, "hyw other chat", 10)
	seedHYWMessage(t, r, other, 0)

	target := hywTextMessage(hywTestChatID, 1, "hyw other chat", 10)
	ctx, _ := runHYW(t, h, hywTestChatID, target)
	reply := assertHYWReplyToTarget(t, ctx, target)
	if !strings.Contains(reply, "没有这条记录") {
		t.Errorf("reply = %q, want no cross-chat match", reply)
	}
}

func TestHYWReadOnly(t *testing.T) {
	h, r, db := setupHYW(t)
	for i, msgID := range []int{10, 16, 22} {
		seedHYWMessage(t, r, hywTextMessage(hywTestChatID, int64(i+1), "hyw read only", msgID), 0)
	}

	var before models.ForwardRecord
	if err := db.First(&before).Error; err != nil {
		t.Fatalf("load record before: %v", err)
	}
	countsBefore := tableCounts(t, db)

	ctx, _ := runHYW(t, h, hywTestChatID, hywTextMessage(hywTestChatID, 1, "hyw read only", 10))
	assertHYWReplyToTarget(t, ctx, ctx.msg.ReplyTo)

	var after models.ForwardRecord
	if err := db.First(&after).Error; err != nil {
		t.Fatalf("load record after: %v", err)
	}
	if after.ForwardCount != before.ForwardCount {
		t.Errorf("ForwardCount = %d, want %d", after.ForwardCount, before.ForwardCount)
	}
	if !sameBoolPtr(after.PendingPair, before.PendingPair) {
		t.Errorf("PendingPair = %v, want %v", after.PendingPair, before.PendingPair)
	}
	if !sameIntPtr(after.LastOccurrenceMsgID, before.LastOccurrenceMsgID) {
		t.Errorf("LastOccurrenceMsgID = %v, want %v", after.LastOccurrenceMsgID, before.LastOccurrenceMsgID)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", after.UpdatedAt, before.UpdatedAt)
	}
	if countsAfter := tableCounts(t, db); countsAfter != countsBefore {
		t.Errorf("table counts = %+v, want unchanged %+v", countsAfter, countsBefore)
	}
}

type hywTableCounts struct {
	Records int64
	Logs    int64
	Stats   int64
	Ignored int64
}

func tableCounts(t *testing.T, db *gorm.DB) hywTableCounts {
	t.Helper()
	var counts hywTableCounts
	for _, item := range []struct {
		model interface{}
		dst   *int64
	}{
		{&models.ForwardRecord{}, &counts.Records},
		{&models.ForwardLog{}, &counts.Logs},
		{&models.UserStats{}, &counts.Stats},
		{&models.ChatIgnoredUser{}, &counts.Ignored},
	} {
		if err := db.Model(item.model).Count(item.dst).Error; err != nil {
			t.Fatalf("count %T: %v", item.model, err)
		}
	}
	return counts
}

func sameBoolPtr(a, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func TestDeriveHYWDecisionRules(t *testing.T) {
	pendingFalse := false
	pendingTrue := true
	last := func(id int) *int { return &id }

	record := &models.ForwardRecord{
		FirstTargetMsgID:    10,
		LastOccurrenceMsgID: last(16),
		ForwardCount:        2,
		PendingPair:         &pendingFalse,
	}
	logs := []models.ForwardLog{{TargetMsgID: 10}, {TargetMsgID: 16}}
	if got := deriveHYWDecision(record, logs, 10); got != "第一次出现，已记录，未提醒" {
		t.Errorf("first occurrence = %q, want first-seen copy", got)
	}
	if got := deriveHYWDecision(record, logs, 16); got != "本次已发出提醒" {
		t.Errorf("latest successful alert = %q, want sent-alert copy", got)
	}

	record.LastOccurrenceMsgID = last(22)
	record.ForwardCount = 3
	record.PendingPair = &pendingTrue
	logs = []models.ForwardLog{{TargetMsgID: 10}, {TargetMsgID: 16}, {TargetMsgID: 22}}
	if got := deriveHYWDecision(record, logs, 16); !strings.Contains(got, "后面还有记录") {
		t.Errorf("non-latest = %q, want unknown", got)
	}
	if got := deriveHYWDecision(record, logs, 22); !strings.Contains(got, "没法确定") {
		t.Errorf("latest pending = %q, want unknown", got)
	}

	adjacentRecord := &models.ForwardRecord{
		FirstTargetMsgID:    10,
		LastOccurrenceMsgID: last(15),
		ForwardCount:        2,
		PendingPair:         &pendingTrue,
	}
	adjacentLogs := []models.ForwardLog{{TargetMsgID: 10}, {TargetMsgID: 15}}
	if got := deriveHYWDecision(adjacentRecord, adjacentLogs, 15); got != "连续刷屏，未发送提醒" {
		t.Errorf("adjacent = %q, want adjacent copy", got)
	}
	if got := deriveHYWDecision(adjacentRecord, nil, 15); !strings.Contains(got, "已超过保留期") {
		t.Errorf("missing log = %q, want unknown", got)
	}
}

func TestHYWTimeFormatMatchesAlertStyle(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 11, 4, 15, 0, 0, 0, loc)
	if got := bot.FormatShortTime(time.Date(2026, 11, 4, 12, 30, 0, 0, loc), now, loc); got != "12:30" {
		t.Errorf("today = %q, want 12:30", got)
	}
	if got := bot.FormatShortTime(time.Date(2026, 11, 3, 12, 30, 0, 0, loc), now, loc); got != "11-03 12:30" {
		t.Errorf("other day = %q, want 11-03 12:30", got)
	}
}
