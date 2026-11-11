package bot

import (
	"errors"
	"log/slog"
	"strings"

	"gopkg.in/telebot.v4"
	"mars-rescue-bot/internal/models"
	"mars-rescue-bot/internal/names"
)

// originTimeTolerance 是认定“转发的就是本群那一条”的时间容差，单位秒。
//
// forward_origin.date 和当初那条消息的 date 都是 Telegram 服务器的 Unix 秒，
// 同一条消息应当相等。留 2 秒只吸收偶发的 1 秒取整差，避免把几秒后在别的
// 聊天另发的同一段文字当成同一条。
const originTimeTolerance int64 = 2

// hiddenLegacyTimeWindowSeconds 是隐藏来源转发比对旧流水插入时间的单向窗口。
// 升级前的流水没有 message_unix，只能看机器人记录这条消息的时间：它不可能
// 早于原消息的发送时间，因此要求 0 <= created_at - origin.date <= 60 秒。
const hiddenLegacyTimeWindowSeconds int64 = 60

var errMemberLookup = errors.New("member lookup unavailable")

// 引用豁免命中原因，写入决策日志的 reason 字段。
const (
	citationReasonAuthorTime         = "author_time"
	citationReasonMemberFallback     = "member_fallback"
	citationReasonChatTime           = "chat_time"
	citationReasonChatNoTimedLog     = "chat_no_timed_log"
	citationReasonHiddenExactTime    = "hidden_exact_time"
	citationReasonHiddenLegacyWindow = "hidden_legacy_window"
	citationReasonReplySameKey       = "reply_same_key"
)

// 未命中豁免的原因，同样只写日志。
const (
	citationMissNoRecord            = "no_record"
	citationMissNoTimedMatch        = "no_timed_match"
	citationMissMemberLookupFailed  = "member_lookup_failed"
	citationMissNotInGroup          = "not_in_group"
	citationMissHiddenNameMismatch  = "hidden_name_mismatch"
	citationMissHiddenOutsideWindow = "hidden_outside_window"
	citationMissOriginNotCandidate  = "origin_not_candidate"
)

// OccurrenceLoader 按内容键读取本群已经记下的出现。没有记录时返回空切片。
type OccurrenceLoader func(contentKey string) ([]models.ForwardLog, error)

// PrepareMessageContent 计算内容键，并决定这条是不是引用豁免。
// 频道转发保持原键。回复内容与被回复消息相同则跳过。
// 用户转发用原作者 ID + origin.date 对照每一条已记录出现的发送者与消息时间；
// 对上才跳过，对不上就照常去重。旧流水没有消息时间时，才退回“原作者仍在本群”。
// 隐藏来源转发（hidden_user）无法取得原作者 ID：带消息时间的流水要求
// origin.date 完全相等；旧流水则要求记录时间落在原消息之后 60 秒内，
// 且该流水发送者的当前显示名与 origin.sender_user_name 一致。
func PrepareMessageContent(msg *telebot.Message, secret string, members GroupMembership, load OccurrenceLoader) (MessageContent, error) {
	content := fingerprintMessage(msg, secret)
	if content.Key == "" || content.Kind == "channel" {
		return content, nil
	}
	if replyRepeatsTarget(msg, content.Key, secret) {
		return MessageContent{SkipDecision: DecisionCitationSkipped, CitationReason: citationReasonReplySameKey}, nil
	}
	if !forwardCitationCandidate(msg) {
		// 有转发来源但没有可用于对照的字段（例如隐藏来源缺少时间、
		// 用户来源缺少原作者 ID），日志里说明它没有进入引用豁免。
		if msg != nil && msg.Origin != nil {
			content.CitationReason = citationMissOriginNotCandidate
		}
		return content, nil
	}
	var logs []models.ForwardLog
	if load != nil {
		var err error
		logs, err = load(content.Key)
		if err != nil {
			return MessageContent{}, err
		}
	}
	skip, reason := forwardCitationSkip(msg, logs, members)
	if skip {
		return MessageContent{SkipDecision: DecisionCitationSkipped, CitationReason: reason}, nil
	}
	content.CitationReason = reason
	return content, nil
}

// messageUnix 返回写入流水的 Telegram date。未知时为 0，仓储层会存成 NULL。
func messageUnix(msg *telebot.Message) int64 {
	if msg == nil || msg.Unixtime <= 0 {
		return 0
	}
	return msg.Unixtime
}

// forwardCitationCandidate 只关心可能指向本群某条消息的转发。
// 频道转发已在上游排除。
func forwardCitationCandidate(msg *telebot.Message) bool {
	if msg == nil || msg.Origin == nil || msg.Chat == nil {
		return false
	}
	switch msg.Origin.Type {
	case "user":
		return msg.Sender != nil && msg.Origin.Sender != nil &&
			msg.Origin.Sender.ID > 0 && msg.Sender.ID > 0
	case "chat":
		return msg.Sender != nil && sameChatOrigin(msg)
	case "hidden_user":
		// 没有用户 ID，只能靠原发送时间；没有时间就不参与豁免。
		return msg.Origin.DateUnixtime > 0
	default:
		return false
	}
}

func sameChatOrigin(msg *telebot.Message) bool {
	return msg.Origin != nil && msg.Origin.SenderChat != nil &&
		msg.Origin.SenderChat.ID != 0 && msg.Chat != nil &&
		msg.Origin.SenderChat.ID == msg.Chat.ID
}

func forwardCitationSkip(msg *telebot.Message, logs []models.ForwardLog, members GroupMembership) (bool, string) {
	switch msg.Origin.Type {
	case "user":
		return userForwardMatchesRecord(msg, logs, members)
	case "chat":
		return chatForwardMatchesRecord(msg, logs)
	case "hidden_user":
		return hiddenForwardMatchesRecord(msg, logs, members)
	default:
		return false, citationMissOriginNotCandidate
	}
}

// userForwardMatchesRecord 用原作者和 origin.date 对照本群每一次出现。
// 转发自己也走这里：只有对上本群那一次才跳过，别处的相同内容仍提醒。
// 一条带时间的流水都没有、但已有旧流水时，退回成员查询。
func userForwardMatchesRecord(msg *telebot.Message, logs []models.ForwardLog, members GroupMembership) (bool, string) {
	authorID := msg.Origin.Sender.ID
	originUnix := msg.Origin.DateUnixtime
	hasTimed := false
	for _, log := range logs {
		if log.MessageUnix == nil || *log.MessageUnix <= 0 {
			continue
		}
		hasTimed = true
		if originUnix > 0 && log.SenderID == authorID && abs64(*log.MessageUnix-originUnix) <= originTimeTolerance {
			return true, citationReasonAuthorTime
		}
	}
	if len(logs) == 0 {
		return false, citationMissNoRecord
	}
	if hasTimed {
		return false, citationMissNoTimedMatch
	}
	inGroup, err := memberStillInGroup(members, msg.Chat, authorID)
	if err != nil {
		return false, citationMissMemberLookupFailed
	}
	if inGroup {
		return true, citationReasonMemberFallback
	}
	return false, citationMissNotInGroup
}

// chatForwardMatchesRecord 在 sender_chat 就是本群时，用 origin.date 对照已记录的消息时间。
// 没有可比较的时间（没有流水、全是旧流水，或 origin 没有 date）时保持跳过。
func chatForwardMatchesRecord(msg *telebot.Message, logs []models.ForwardLog) (bool, string) {
	if !sameChatOrigin(msg) {
		return false, citationMissOriginNotCandidate
	}
	originUnix := msg.Origin.DateUnixtime
	if originUnix > 0 {
		hasTimed := false
		for _, log := range logs {
			if log.MessageUnix == nil || *log.MessageUnix <= 0 {
				continue
			}
			hasTimed = true
			if abs64(*log.MessageUnix-originUnix) <= originTimeTolerance {
				return true, citationReasonChatTime
			}
		}
		if hasTimed {
			return false, citationMissNoTimedMatch
		}
	}
	return true, citationReasonChatNoTimedLog
}

// hiddenForwardMatchesRecord 处理隐藏来源的转发。Telegram 只给原发送者姓名和
// 原发送时间，没有用户 ID：
//   - 带 message_unix 的流水：要求时间完全相等，不比对姓名；
//   - 旧流水（message_unix 为空）：要求记录时间不早于原消息、且相差不超过 60 秒，
//     并且流水发送者现在的显示名与 origin.sender_user_name 一致。
//
// 任一流水命中即跳过；姓名查不到、成员已离群或姓名不一致都不跳过。
func hiddenForwardMatchesRecord(msg *telebot.Message, logs []models.ForwardLog, members GroupMembership) (bool, string) {
	originUnix := msg.Origin.DateUnixtime
	if originUnix <= 0 {
		return false, citationMissOriginNotCandidate
	}

	hasTimed := false
	for _, log := range logs {
		if log.MessageUnix == nil || *log.MessageUnix <= 0 {
			continue
		}
		hasTimed = true
		if *log.MessageUnix == originUnix {
			return true, citationReasonHiddenExactTime
		}
	}

	var (
		nameMismatch  bool
		lookupFailure bool
	)
	for _, log := range logs {
		if log.MessageUnix != nil && *log.MessageUnix > 0 {
			continue
		}
		delta := log.CreatedAt.Unix() - originUnix
		if delta < 0 || delta > hiddenLegacyTimeWindowSeconds {
			continue
		}
		ok, err := hiddenNameMatches(members, msg.Chat, log.SenderID, msg.Origin.SenderUsername)
		if err != nil {
			lookupFailure = true
			continue
		}
		if ok {
			return true, citationReasonHiddenLegacyWindow
		}
		nameMismatch = true
	}

	switch {
	case nameMismatch:
		return false, citationMissHiddenNameMismatch
	case lookupFailure:
		return false, citationMissMemberLookupFailed
	case hasTimed:
		return false, citationMissNoTimedMatch
	case len(logs) == 0:
		return false, citationMissNoRecord
	default:
		return false, citationMissHiddenOutsideWindow
	}
}

// hiddenNameMatches 用 getChatMember 取流水发送者的当前显示名，与隐藏来源给出的
// 姓名规范化后比较。查询失败会打 Warn，由调用方按“不豁免”处理。
func hiddenNameMatches(members GroupMembership, chat *telebot.Chat, userID int64, originName string) (bool, error) {
	if userID <= 0 || strings.TrimSpace(originName) == "" {
		return false, nil
	}
	if members == nil || chat == nil {
		return false, errMemberLookup
	}
	name, err := members.MemberName(chat, userID)
	if err != nil {
		slog.Warn("hidden_user name lookup failed",
			"chat_id", chat.ID,
			"user_id", userID,
			"error", err,
		)
		return false, err
	}
	return names.NormalizeName(name) == names.NormalizeName(originName), nil
}

func memberStillInGroup(members GroupMembership, chat *telebot.Chat, userID int64) (bool, error) {
	if members == nil || chat == nil || userID <= 0 {
		return false, errMemberLookup
	}
	inGroup, err := members.InGroup(chat, userID)
	if err != nil {
		slog.Warn("member lookup failed",
			"chat_id", chat.ID,
			"user_id", userID,
			"error", err,
		)
		return false, err
	}
	return inGroup, nil
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// replyRepeatsTarget 在回复的内容键与被回复消息的内容键相同时跳过。
// 比较的是指纹本身。被回复消息若属于豁免转发，仍用它的原文键，而不是跳过之后的空键。
func replyRepeatsTarget(msg *telebot.Message, key, secret string) bool {
	if msg == nil || msg.ReplyTo == nil || key == "" {
		return false
	}
	replied := fingerprintMessage(msg.ReplyTo, secret)
	return replied.Key != "" && replied.Key == key
}
