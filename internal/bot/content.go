package bot

import (
	"strings"

	"gopkg.in/telebot.v4"
	"mars-rescue-bot/internal/textnorm"
)

// MessageContent 是正常检测与 /hyw 查询共用的内容判定结果。
// Key 为空时不会写入数据库，SkipDecision 说明未参与检测的原因。
type MessageContent struct {
	Key          string
	Hash         string
	Kind         string // channel / media / text，用于提醒文案和日志
	KeyType      string // 展示用：文本、图片、视频、动图、文件、频道转发
	MediaSubject string
	SkipDecision string
}

// AnalyzeMessageContent 按正常去重的顺序判定消息内容键。
// 检测流程与 /hyw 查询都必须调用它，避免两套规则漂移。
func AnalyzeMessageContent(msg *telebot.Message, secret string) MessageContent {
	if msg == nil {
		return MessageContent{SkipDecision: DecisionNoContent}
	}
	if key, ok := channelForwardKey(msg); ok {
		return MessageContent{
			Key:     key,
			Hash:    contentKeyHash(secret, key),
			Kind:    "channel",
			KeyType: "频道转发",
		}
	}
	if key, subject, ok := mediaContentKey(msg); ok {
		return MessageContent{
			Key:          key,
			Hash:         contentKeyHash(secret, key),
			Kind:         "media",
			KeyType:      mediaKeyType(msg),
			MediaSubject: subject,
		}
	}
	// 支持的媒体类型却拿不到 file_unique_id：有内容，但没有可用的去重键。
	if msg.Photo != nil || msg.Video != nil || msg.Animation != nil || msg.Document != nil {
		return MessageContent{SkipDecision: DecisionNoContent}
	}
	// 音频不在支持范围；没有文本时按内容判断是未支持类型还是空消息。
	text := messagePlainText(msg)
	if msg.Audio != nil || text == "" {
		if messageHasContent(msg) {
			return MessageContent{SkipDecision: DecisionUnsupportedType}
		}
		return MessageContent{SkipDecision: DecisionNoContent}
	}

	if strings.HasPrefix(text, "/") {
		return MessageContent{SkipDecision: DecisionCommand}
	}

	normalized := textnorm.Normalize(text)
	if normalized == "" {
		return MessageContent{SkipDecision: DecisionNoContent}
	}
	if !textnorm.HasContent(normalized, textnorm.DefaultMinRunes) {
		return MessageContent{SkipDecision: DecisionShortTextSkipped}
	}
	if textnorm.IsRepeatedSingleRune(normalized) {
		return MessageContent{SkipDecision: DecisionRepeatedCharSkipped}
	}

	key := textContentKey(secret, normalized)
	return MessageContent{
		Key:     key,
		Hash:    contentKeyHash(secret, key),
		Kind:    "text",
		KeyType: "文本",
	}
}

func mediaKeyType(msg *telebot.Message) string {
	switch {
	case msg.Photo != nil:
		return "图片"
	case msg.Video != nil:
		return "视频"
	case msg.Animation != nil:
		return "动图"
	case msg.Document != nil:
		return "文件"
	default:
		return "媒体"
	}
}
