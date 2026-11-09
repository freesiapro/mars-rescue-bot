package bot

import "gopkg.in/telebot.v4"

// citationExempt 判断一条已经有内容键、且不是频道转发的消息要不要整条跳过。
// 跳过发生在入库之前：不写记录、不提醒、不进排行榜。
func citationExempt(msg *telebot.Message, key, secret string, members GroupMembership) bool {
	if msg == nil || key == "" {
		return false
	}
	if exemptInGroupForward(msg, members) {
		return true
	}
	return replyRepeatsTarget(msg, key, secret)
}

// exemptInGroupForward 只处理非频道 forward_origin。
// hidden_user 没有用户 ID，不跳过，也不做显示名匹配。
// 旧的 forward_from 字段不参与：线上更新只带 forward_origin。
func exemptInGroupForward(msg *telebot.Message, members GroupMembership) bool {
	if msg.Origin == nil || msg.Chat == nil || msg.Sender == nil {
		return false
	}
	switch msg.Origin.Type {
	case "channel", "hidden_user":
		return false
	case "chat":
		return msg.Origin.SenderChat != nil &&
			msg.Origin.SenderChat.ID != 0 &&
			msg.Origin.SenderChat.ID == msg.Chat.ID
	case "user":
		if msg.Origin.Sender == nil || msg.Origin.Sender.ID <= 0 || msg.Sender.ID <= 0 {
			return false
		}
		if msg.Origin.Sender.ID == msg.Sender.ID {
			return true
		}
		if members == nil {
			return false
		}
		inGroup, err := members.InGroup(msg.Chat, msg.Origin.Sender.ID)
		if err != nil || !inGroup {
			return false
		}
		return true
	default:
		return false
	}
}

// replyRepeatsTarget 在回复的内容键与被回复消息的内容键相同时跳过。
// 比较的是指纹本身。被回复消息若属于豁免转发，仍用它的原文键，而不是跳过之后的空键。
func replyRepeatsTarget(msg *telebot.Message, key, secret string) bool {
	if msg.ReplyTo == nil {
		return false
	}
	replied := fingerprintMessage(msg.ReplyTo, secret)
	return replied.Key != "" && replied.Key == key
}
