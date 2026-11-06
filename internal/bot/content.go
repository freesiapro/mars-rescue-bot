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
	Kind         string // channel / media / link / text，用于提醒文案和日志
	KeyType      string // 展示用：文本、链接、图片、视频、动图、文件、频道转发
	MediaSubject string
	SkipDecision string
}

// AnalyzeMessageContent 按正常去重的顺序判定消息内容键。
// 检测流程与 /hyw 查询都必须调用它，避免两套规则漂移。
func AnalyzeMessageContent(msg *telebot.Message, secret string) MessageContent {
	if msg == nil {
		return MessageContent{SkipDecision: DecisionNoContent}
	}

	text := messagePlainText(msg)

	// 命令最先豁免，包括转发和频道转发的命令文本。
	if strings.HasPrefix(text, "/") {
		return MessageContent{SkipDecision: DecisionCommand}
	}

	// 只有 @ 提及的消息不参与判重，对所有来源生效：普通消息、非频道转发、
	// 频道转发和富文本。这一步必须在频道/媒体键之前，否则频道转发的纯 @
	// 会先拿到 channel 键并入库。带媒体（含富文本媒体块）的消息不是“纯 @”，
	// 跳过本分支，按原有频道/媒体规则处理。
	// 有其它实际内容的消息仍走原规则，提及部分继续参与内容键（不在这里剔除），
	// 避免 "@Alice 今天天气不错" 因为去掉提及而跌破 10 字符下限被误跳过。
	if !messageHasNonTextPayload(msg) && messageMentionOnly(msg) {
		return MessageContent{SkipDecision: DecisionMentionOnly}
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
	if msg.Audio != nil {
		if messageHasContent(msg) {
			return MessageContent{SkipDecision: DecisionUnsupportedType}
		}
		return MessageContent{SkipDecision: DecisionNoContent}
	}

	if text == "" {
		if messageHasContent(msg) {
			return MessageContent{SkipDecision: DecisionUnsupportedType}
		}
		return MessageContent{SkipDecision: DecisionNoContent}
	}

	// 纯链接消息单独使用链接键。它没有“普通文字”，不能走 10 字符下限，
	// 否则 URL 会在 HasContent 里被剥掉而误判为太短。
	if urls, ok := messageLinkURLs(msg); ok {
		key := linkContentKey(secret, urls)
		return MessageContent{
			Key:     key,
			Hash:    contentKeyHash(secret, key),
			Kind:    "link",
			KeyType: "链接",
		}
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
