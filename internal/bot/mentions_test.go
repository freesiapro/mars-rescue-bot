package bot

import (
	"encoding/json"
	"testing"

	"gopkg.in/telebot.v4"
	"mars-rescue-bot/internal/models"
)

func mentionTextMessage(text string, msgID int) *telebot.Message {
	return textMessage(testChatID, 201, "Mention", text, msgID)
}

func addEntity(msg *telebot.Message, entityType telebot.EntityType, token string) {
	msg.Entities = append(msg.Entities, telebot.MessageEntity{
		Type:   entityType,
		Offset: utf16Offset(msg.Text, token),
		Length: utf16Length(token),
	})
}

func setChannelOrigin(msg *telebot.Message, originChatID int64, originMsgID int) {
	msg.Origin = &telebot.MessageOrigin{
		Type:      "channel",
		Chat:      &telebot.Chat{ID: originChatID, Type: telebot.ChatChannel},
		MessageID: originMsgID,
	}
}

func decodeRichMessage(t *testing.T, richJSON string) *telebot.Message {
	t.Helper()
	var msg telebot.Message
	if err := json.Unmarshal([]byte(richJSON), &msg); err != nil {
		t.Fatalf("decode rich message: %v", err)
	}
	if msg.RichMessage == nil {
		t.Fatal("RichMessage = nil, want decoded rich message")
	}
	return &msg
}

func TestAnalyzeMessageContentMentionOnly(t *testing.T) {
	tests := []struct {
		name string
		msg  func(t *testing.T) *telebot.Message
	}{
		{
			name: "single mention entity",
			msg: func(t *testing.T) *telebot.Message {
				msg := mentionTextMessage("@Bob", 601)
				addEntity(msg, telebot.EntityMention, "@Bob")
				return msg
			},
		},
		{
			name: "single mention without entity",
			msg: func(t *testing.T) *telebot.Message {
				return mentionTextMessage("@Bob", 602)
			},
		},
		{
			name: "multiple mentions",
			msg: func(t *testing.T) *telebot.Message {
				msg := mentionTextMessage("@Bob @Alice", 603)
				addEntity(msg, telebot.EntityMention, "@Bob")
				addEntity(msg, telebot.EntityMention, "@Alice")
				return msg
			},
		},
		{
			name: "mention plus whitespace and punctuation",
			msg: func(t *testing.T) *telebot.Message {
				msg := mentionTextMessage("@Bob  ,  !!! ...", 604)
				addEntity(msg, telebot.EntityMention, "@Bob")
				return msg
			},
		},
		{
			name: "mention after emoji offset",
			msg: func(t *testing.T) *telebot.Message {
				msg := mentionTextMessage("😀 @Bob ‼️", 605)
				addEntity(msg, telebot.EntityMention, "@Bob")
				return msg
			},
		},
		{
			name: "text_mention entity",
			msg: func(t *testing.T) *telebot.Message {
				msg := mentionTextMessage("Bob", 606)
				addEntity(msg, telebot.EntityTMention, "Bob")
				return msg
			},
		},
		{
			name: "text_mention plus punctuation",
			msg: func(t *testing.T) *telebot.Message {
				msg := mentionTextMessage("Bob!?", 607)
				msg.Entities = append(msg.Entities, telebot.MessageEntity{
					Type: telebot.EntityTMention, Offset: 0, Length: 3,
				})
				return msg
			},
		},
		{
			name: "forwarded mention",
			msg: func(t *testing.T) *telebot.Message {
				msg := mentionTextMessage("@Bob", 608)
				addEntity(msg, telebot.EntityMention, "@Bob")
				msg.Origin = &telebot.MessageOrigin{Type: "hidden_user"}
				return msg
			},
		},
		{
			name: "rich mention entity",
			msg: func(t *testing.T) *telebot.Message {
				return decodeRichMessage(t, `{
					"message_id": 609,
					"rich_message": {"blocks": [
						{"type": "paragraph", "text": [{"type": "mention", "username": "Bob"}]}
					]}
				}`)
			},
		},
		{
			name: "rich multiple mentions",
			msg: func(t *testing.T) *telebot.Message {
				return decodeRichMessage(t, `{
					"message_id": 610,
					"rich_message": {"blocks": [
						{"type": "paragraph", "text": [
							{"type": "mention", "username": "Bob"},
							" ",
							{"type": "mention", "username": "Alice"}
						]}
					]}
				}`)
			},
		},
		{
			name: "rich plain mention without entity",
			msg: func(t *testing.T) *telebot.Message {
				return decodeRichMessage(t, `{
					"message_id": 611,
					"rich_message": {"blocks": [
						{"type": "paragraph", "text": "@Bob"}
					]}
				}`)
			},
		},
		{
			name: "rich text_mention entity",
			msg: func(t *testing.T) *telebot.Message {
				return decodeRichMessage(t, `{
					"message_id": 612,
					"rich_message": {"blocks": [
						{"type": "paragraph", "text": [
							{"type": "text_mention", "user": {"id": 7, "first_name": "Bob"}}
						]}
					]}
				}`)
			},
		},
		{
			name: "rich mention after emoji",
			msg: func(t *testing.T) *telebot.Message {
				return decodeRichMessage(t, `{
					"message_id": 613,
					"rich_message": {"blocks": [
						{"type": "paragraph", "text": ["😀 ", {"type": "mention", "username": "Bob"}]}
					]}
				}`)
			},
		},
		{
			name: "rich nested block mention",
			msg: func(t *testing.T) *telebot.Message {
				return decodeRichMessage(t, `{
					"message_id": 614,
					"rich_message": {"blocks": [
						{"type": "blockquote", "blocks": [
							{"type": "paragraph", "text": [{"type": "mention", "username": "Bob"}]}
						]}
					]}
				}`)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.msg(t)
			content := AnalyzeMessageContent(msg, contentTestSecret)
			if content.Key != "" {
				t.Errorf("key = %q, want empty for mention-only message", content.Key)
			}
			if content.SkipDecision != DecisionMentionOnly {
				t.Errorf("decision = %q, want %q", content.SkipDecision, DecisionMentionOnly)
			}
		})
	}
}

func TestAnalyzeMessageContentChannelForwardMentionOnly(t *testing.T) {
	const originChatID = int64(-1001234500000)
	const originMsgID = 9001

	tests := []struct {
		name string
		msg  func(t *testing.T) *telebot.Message
	}{
		{
			name: "channel forward plain mention",
			msg: func(t *testing.T) *telebot.Message {
				msg := mentionTextMessage("@Bob", 701)
				addEntity(msg, telebot.EntityMention, "@Bob")
				setChannelOrigin(msg, originChatID, originMsgID)
				return msg
			},
		},
		{
			name: "channel forward rich mention",
			msg: func(t *testing.T) *telebot.Message {
				msg := decodeRichMessage(t, `{
					"message_id": 702,
					"rich_message": {"blocks": [
						{"type": "paragraph", "text": [{"type": "mention", "username": "Bob"}]}
					]}
				}`)
				setChannelOrigin(msg, originChatID, originMsgID)
				return msg
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := AnalyzeMessageContent(tt.msg(t), contentTestSecret)
			if content.Key != "" {
				t.Errorf("key = %q, want empty for channel-forward mention-only message", content.Key)
			}
			if content.SkipDecision != DecisionMentionOnly {
				t.Errorf("decision = %q, want %q", content.SkipDecision, DecisionMentionOnly)
			}
		})
	}
}

func TestAnalyzeMessageContentChannelForwardWithTextUsesChannelKey(t *testing.T) {
	const originChatID = int64(-1001234500000)
	const originMsgID = 9002

	msg := mentionTextMessage("@Alice 你好", 711)
	addEntity(msg, telebot.EntityMention, "@Alice")
	setChannelOrigin(msg, originChatID, originMsgID)

	content := AnalyzeMessageContent(msg, contentTestSecret)
	if content.SkipDecision != "" {
		t.Errorf("decision = %q, want no skip decision", content.SkipDecision)
	}
	if content.Kind != "channel" || content.KeyType != "频道转发" {
		t.Errorf("content = %+v, want channel/频道转发", content)
	}
	wantKey := "channel:-1001234500000:9002"
	if content.Key != wantKey {
		t.Errorf("key = %q, want %q", content.Key, wantKey)
	}
}

func TestAnalyzeMessageContentChannelForwardMediaWithMentionUsesChannelKey(t *testing.T) {
	const originChatID = int64(-1001234500000)
	const originMsgID = 9003

	plain := mentionTextMessage("", 712)
	plain.Photo = &telebot.Photo{File: telebot.File{UniqueID: "photo-unique"}}
	plain.Caption = "@Bob"
	plain.CaptionEntities = telebot.Entities{
		{Type: telebot.EntityMention, Offset: 0, Length: 4},
	}
	setChannelOrigin(plain, originChatID, originMsgID)

	rich := decodeRichMessage(t, `{
		"message_id": 713,
		"rich_message": {"blocks": [
			{"type": "photo", "photo": [{"file_id": "f", "file_unique_id": "rich-photo"}]},
			{"type": "paragraph", "text": [{"type": "mention", "username": "Bob"}]}
		]}
	}`)
	setChannelOrigin(rich, originChatID, originMsgID)

	for name, msg := range map[string]*telebot.Message{"plain media": plain, "rich media": rich} {
		content := AnalyzeMessageContent(msg, contentTestSecret)
		if content.SkipDecision != "" {
			t.Errorf("%s decision = %q, want no skip decision", name, content.SkipDecision)
		}
		if content.Kind != "channel" || content.KeyType != "频道转发" {
			t.Errorf("%s content = %+v, want channel/频道转发", name, content)
		}
		wantKey := "channel:-1001234500000:9003"
		if content.Key != wantKey {
			t.Errorf("%s key = %q, want %q", name, content.Key, wantKey)
		}
	}
}

func TestAnalyzeMessageContentMentionWithContentStillDetected(t *testing.T) {
	tests := []struct {
		name string
		msg  func(t *testing.T) *telebot.Message
	}{
		{
			name: "mention plus text",
			msg: func(t *testing.T) *telebot.Message {
				msg := mentionTextMessage("@Alice 今天天气不错", 621)
				addEntity(msg, telebot.EntityMention, "@Alice")
				return msg
			},
		},
		{
			name: "mention plus latin text",
			msg: func(t *testing.T) *telebot.Message {
				msg := mentionTextMessage("@Bob hello mars", 622)
				addEntity(msg, telebot.EntityMention, "@Bob")
				return msg
			},
		},
		{
			name: "text_mention plus text",
			msg: func(t *testing.T) *telebot.Message {
				msg := mentionTextMessage("Bob hello mars", 623)
				addEntity(msg, telebot.EntityTMention, "Bob")
				return msg
			},
		},
		{
			name: "email is not a mention",
			msg: func(t *testing.T) *telebot.Message {
				return mentionTextMessage("foo@bar.com hello mars", 624)
			},
		},
		{
			name: "rich mention plus text",
			msg: func(t *testing.T) *telebot.Message {
				return decodeRichMessage(t, `{
					"message_id": 625,
					"rich_message": {"blocks": [
						{"type": "paragraph", "text": [
							{"type": "mention", "username": "Bob"},
							" hello mars bot"
						]}
					]}
				}`)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.msg(t)
			content := AnalyzeMessageContent(msg, contentTestSecret)
			if content.Key == "" {
				t.Fatalf("content = %+v, want a content key", content)
			}
			if content.SkipDecision != "" {
				t.Errorf("decision = %q, want no skip decision", content.SkipDecision)
			}
			if content.Kind != "text" || content.KeyType != "文本" {
				t.Errorf("content = %+v, want text/文本", content)
			}
		})
	}
}

// 提及部分继续参与内容键：这与本次改动前的行为一致。
func TestAnalyzeMessageContentMentionStaysInContentKey(t *testing.T) {
	msg := mentionTextMessage("@Bob hello mars", 631)
	addEntity(msg, telebot.EntityMention, "@Bob")

	content := AnalyzeMessageContent(msg, contentTestSecret)
	if want := textKeyFor(contentTestSecret, "@Bob hello mars"); content.Key != want {
		t.Errorf("key = %q, want %q (mention must stay in the key)", content.Key, want)
	}
}

func TestAnalyzeMessageContentPunctuationWithoutMentionIsNotMentionOnly(t *testing.T) {
	msg := mentionTextMessage("... !!!", 641)
	content := AnalyzeMessageContent(msg, contentTestSecret)
	if content.SkipDecision != DecisionNoContent {
		t.Errorf("decision = %q, want %q", content.SkipDecision, DecisionNoContent)
	}
}

// 纯 @ 消息只产生一条 mention_only 决策日志，不写记录、流水和排行榜。
func TestProcessorMentionOnlyDoesNotWrite(t *testing.T) {
	p, _, db := setupProcessor(t)
	buf := captureDecisionLog(t, p)
	replies := &replyRecorder{}

	msg := mentionTextMessage("@Bob", 651)
	addEntity(msg, telebot.EntityMention, "@Bob")
	if err := p.ProcessMessage(msg, "trace", replies.reply); err != nil {
		t.Fatalf("ProcessMessage() error = %v", err)
	}

	lines := readDecisions(t, buf)
	if len(lines) != 1 {
		t.Fatalf("decision lines = %d, want 1: %s", len(lines), buf.String())
	}
	if lines[0].Decision != DecisionMentionOnly {
		t.Errorf("decision = %q, want %q", lines[0].Decision, DecisionMentionOnly)
	}
	if lines[0].KeyHash != "" {
		t.Errorf("key_hash = %q, want omitted", lines[0].KeyHash)
	}
	if got := len(replies.snapshot()); got != 0 {
		t.Errorf("replies = %d, want 0", got)
	}

	var records, logs, stats int64
	if err := db.Model(&models.ForwardRecord{}).Count(&records).Error; err != nil {
		t.Fatalf("count records: %v", err)
	}
	if err := db.Model(&models.ForwardLog{}).Count(&logs).Error; err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if err := db.Model(&models.UserStats{}).Count(&stats).Error; err != nil {
		t.Fatalf("count stats: %v", err)
	}
	if records != 0 || logs != 0 || stats != 0 {
		t.Errorf("records/logs/stats = %d/%d/%d, want 0/0/0", records, logs, stats)
	}
}

// 纯 @ 的频道转发同样不入库、不配对、不提醒；普通文本和富文本都覆盖。
func TestProcessorChannelForwardMentionOnlyDoesNotWrite(t *testing.T) {
	const originChatID = int64(-1001234500000)

	tests := []struct {
		name string
		msg  func(t *testing.T, msgID int) *telebot.Message
	}{
		{
			name: "plain",
			msg: func(t *testing.T, msgID int) *telebot.Message {
				msg := mentionTextMessage("@Bob", msgID)
				addEntity(msg, telebot.EntityMention, "@Bob")
				setChannelOrigin(msg, originChatID, 9101)
				return msg
			},
		},
		{
			name: "rich",
			msg: func(t *testing.T, msgID int) *telebot.Message {
				msg := decodeRichMessage(t, `{
					"message_id": 9102,
					"rich_message": {"blocks": [
						{"type": "paragraph", "text": [{"type": "mention", "username": "Bob"}]}
					]}
				}`)
				msg.ID = msgID
				msg.Chat = &telebot.Chat{ID: testChatID, Type: telebot.ChatSuperGroup}
				msg.Sender = &telebot.User{ID: 201, FirstName: "Mention"}
				setChannelOrigin(msg, originChatID, 9101)
				return msg
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _, db := setupProcessor(t)
			buf := captureDecisionLog(t, p)
			replies := &replyRecorder{}

			for _, msgID := range []int{721, 722} {
				msg := tt.msg(t, msgID)
				if err := p.ProcessMessage(msg, "trace", replies.reply); err != nil {
					t.Fatalf("ProcessMessage(%d) error = %v", msgID, err)
				}
			}

			lines := readDecisions(t, buf)
			if len(lines) != 2 {
				t.Fatalf("decision lines = %d, want 2: %s", len(lines), buf.String())
			}
			for _, line := range lines {
				if line.Decision != DecisionMentionOnly {
					t.Errorf("decision = %q, want %q", line.Decision, DecisionMentionOnly)
				}
				if line.MsgType != "channel_forward" {
					t.Errorf("msg_type = %q, want channel_forward", line.MsgType)
				}
				if line.KeyHash != "" {
					t.Errorf("key_hash = %q, want omitted", line.KeyHash)
				}
			}
			if got := len(replies.snapshot()); got != 0 {
				t.Errorf("replies = %d, want 0", got)
			}

			var records, logs, stats int64
			if err := db.Model(&models.ForwardRecord{}).Count(&records).Error; err != nil {
				t.Fatalf("count records: %v", err)
			}
			if err := db.Model(&models.ForwardLog{}).Count(&logs).Error; err != nil {
				t.Fatalf("count logs: %v", err)
			}
			if err := db.Model(&models.UserStats{}).Count(&stats).Error; err != nil {
				t.Fatalf("count stats: %v", err)
			}
			if records != 0 || logs != 0 || stats != 0 {
				t.Errorf("records/logs/stats = %d/%d/%d, want 0/0/0", records, logs, stats)
			}
		})
	}
}
