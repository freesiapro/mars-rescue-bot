package bot

import "gopkg.in/telebot.v4"
import "mars-rescue-bot/internal/models"

// originTimeTolerance 是认定“转发的就是本群那一条”的时间容差，单位秒。
//
// forward_origin.date 和当初那条消息的 date 都是 Telegram 服务器的 Unix 秒，
// 同一条消息应当相等。留 2 秒只吸收偶发的 1 秒取整差，避免把几秒后在别的
// 聊天另发的同一段文字当成同一条。
const originTimeTolerance int64 = 2

// OccurrenceLoader 按内容键读取本群已经记下的出现。没有记录时返回空切片。
type OccurrenceLoader func(contentKey string) ([]models.ForwardLog, error)

// PrepareMessageContent 计算内容键，并决定这条是不是引用豁免。
// 频道转发保持原键。回复内容与被回复消息相同则跳过。
// 用户转发用原作者 ID + origin.date 对照每一条已记录出现的发送者与消息时间；
// 对上才跳过，对不上就照常去重。旧流水没有消息时间时，才退回“原作者仍在本群”。
func PrepareMessageContent(msg *telebot.Message, secret string, members GroupMembership, load OccurrenceLoader) (MessageContent, error) {
	content := fingerprintMessage(msg, secret)
	if content.Key == "" || content.Kind == "channel" {
		return content, nil
	}
	if replyRepeatsTarget(msg, content.Key, secret) {
		return MessageContent{SkipDecision: DecisionCitationSkipped}, nil
	}
	if !forwardCitationCandidate(msg) {
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
	if forwardCitationSkip(msg, logs, members) {
		return MessageContent{SkipDecision: DecisionCitationSkipped}, nil
	}
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
// hidden_user 没有用户 ID，不参与；频道转发已在上游排除。
func forwardCitationCandidate(msg *telebot.Message) bool {
	if msg == nil || msg.Origin == nil || msg.Chat == nil || msg.Sender == nil {
		return false
	}
	switch msg.Origin.Type {
	case "user":
		return msg.Origin.Sender != nil && msg.Origin.Sender.ID > 0 && msg.Sender.ID > 0
	case "chat":
		return sameChatOrigin(msg)
	default:
		return false
	}
}

func sameChatOrigin(msg *telebot.Message) bool {
	return msg.Origin != nil && msg.Origin.SenderChat != nil &&
		msg.Origin.SenderChat.ID != 0 && msg.Chat != nil &&
		msg.Origin.SenderChat.ID == msg.Chat.ID
}

func forwardCitationSkip(msg *telebot.Message, logs []models.ForwardLog, members GroupMembership) bool {
	switch msg.Origin.Type {
	case "user":
		return userForwardMatchesRecord(msg, logs, members)
	case "chat":
		return chatForwardMatchesRecord(msg, logs)
	default:
		return false
	}
}

// userForwardMatchesRecord 用原作者和 origin.date 对照本群每一次出现。
// 转发自己也走这里：只有对上本群那一次才跳过，别处的相同内容仍提醒。
// 一条带时间的流水都没有、但已有旧流水时，退回成员查询。
func userForwardMatchesRecord(msg *telebot.Message, logs []models.ForwardLog, members GroupMembership) bool {
	authorID := msg.Origin.Sender.ID
	originUnix := msg.Origin.DateUnixtime
	hasTimed := false
	for _, log := range logs {
		if log.MessageUnix == nil || *log.MessageUnix <= 0 {
			continue
		}
		hasTimed = true
		if originUnix > 0 && log.SenderID == authorID && abs64(*log.MessageUnix-originUnix) <= originTimeTolerance {
			return true
		}
	}
	if hasTimed || len(logs) == 0 {
		return false
	}
	return memberStillInGroup(members, msg.Chat, authorID)
}

// chatForwardMatchesRecord 在 sender_chat 就是本群时，用 origin.date 对照已记录的消息时间。
// 没有可比较的时间（没有流水、全是旧流水，或 origin 没有 date）时保持跳过。
func chatForwardMatchesRecord(msg *telebot.Message, logs []models.ForwardLog) bool {
	if !sameChatOrigin(msg) {
		return false
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
				return true
			}
		}
		if hasTimed {
			return false
		}
	}
	return true
}

func memberStillInGroup(members GroupMembership, chat *telebot.Chat, userID int64) bool {
	if members == nil || chat == nil || userID <= 0 {
		return false
	}
	inGroup, err := members.InGroup(chat, userID)
	return err == nil && inGroup
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
