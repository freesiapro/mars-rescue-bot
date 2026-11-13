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
	// CitationReason 说明引用豁免的判定结果：命中时是豁免原因，
	// 未命中时是没豁免的原因。只写日志，不影响检测结果。
	CitationReason string
}

// GroupMembership 报告某用户现在是否还在群里，并解析当前显示名。
// 查询失败必须返回 error，调用方会据此保留这条转发，而不是当成引用跳过。
type GroupMembership interface {
	InGroup(chat *telebot.Chat, userID int64) (bool, error)
	// MemberName 返回当前显示名 first_name + " " + last_name（trim）。
	// 用户不在群内、缺少姓名或查询失败时返回 error。
	// 是否相等由调用方用 names.NormalizeName 统一规范化后判断。
	MemberName(chat *telebot.Chat, userID int64) (string, error)
}

// AnalyzeMessageContent 只计算内容键，不查历史出现，也不做引用豁免。
// 检测流程与 /hyw 必须调用 PrepareMessageContent，避免两套规则漂移。
func AnalyzeMessageContent(msg *telebot.Message, secret string) MessageContent {
	return fingerprintMessage(msg, secret)
}

// fingerprintMessage 只计算内容键，不应用引用豁免。
// 回复比对必须用它，避免被回复的那条本身是豁免转发时键被清掉。
func fingerprintMessage(msg *telebot.Message, secret string) MessageContent {
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
	// 避免因为去掉提及而改变内容键或内容分。
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

	// 纯链接消息单独使用链接键。它没有“普通文字”，不能走文本内容分下限，
	// 否则 URL 会在内容分计算里被剥掉而误判为太短。
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
	if !textnorm.HasEnoughContent(normalized) {
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
