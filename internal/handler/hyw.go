package handler

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/telebot.v4"
	"mars-rescue-bot/internal/bot"
	"mars-rescue-bot/internal/models"
)

const (
	hywUsage       = "请先回复要查询的消息，再发送 /hyw。可以查看它有没有被记录、一共出现几次（含第一次），以及这次有没有提醒。"
	hywPrivateHint = "请在群聊中回复消息后使用 /hyw，私聊无法查询。"
	hywLookupError = "查询失败，请稍后重试。"
	hywNotRecorded = "📒 本群没有这条内容的记录。可能当时没有检测到，或记录已超过保留期。"
	hywNotThisMsg  = "📒 同样的内容在本群有记录，但这条消息本身不在其中。可能当时没有记上，或已经超过保留期。不能据此判断这条有没有被检测到或提醒。"
)

// handleHYW 是只读查询：根据被回复消息重新计算内容键，再查当前群的记录。
// 不调用 Processor，也不写 forward_records / forward_logs / user_stats。
func (h *CommandHandler) handleHYW(c telebot.Context) error {
	msg := c.Message()
	if msg == nil {
		return nil
	}
	// 已注册命令由 telebot 直接交给命令处理器，不再经过 OnText；这里补上
	// 与 Processor 命令豁免一致的唯一一条 command 决策日志。
	bot.LogCommandDecision(msg)

	if msg.Chat == nil || msg.Sender == nil {
		return c.Reply(hywUsage)
	}
	if msg.Chat.Type == telebot.ChatPrivate {
		return c.Reply(hywPrivateHint)
	}

	target := msg.ReplyTo
	if target == nil {
		return c.Reply(hywUsage)
	}
	if target.Sender != nil && target.Sender.IsBot {
		return sendHYWReply(c, target, "📒 机器人发送的消息不记录，也不会提醒。")
	}

	content := bot.AnalyzeMessageContent(target, h.dedupSecret)
	if content.Key == "" {
		return sendHYWReply(c, target, hywSkipReason(content.SkipDecision))
	}

	record, found, err := h.repo.FindRecordByKey(msg.Chat.ID, content.Key)
	if err != nil {
		return c.Reply(hywLookupError)
	}
	if !found {
		return sendHYWReply(c, target, hywNotRecorded)
	}

	logs, err := h.repo.ListRecordLogs(record.ID)
	if err != nil {
		return c.Reply(hywLookupError)
	}
	if !hywTargetRecorded(record, logs, target.ID) {
		return sendHYWReply(c, target, hywNotThisMsg)
	}

	return sendHYWReply(c, target, formatHYWLookup(record, logs, target, h.now(), h.statsLoc))
}

func sendHYWReply(c telebot.Context, target *telebot.Message, text string) error {
	return c.Send(text, &telebot.SendOptions{
		ReplyTo:           target,
		AllowWithoutReply: true,
	})
}

func hywSkipReason(decision string) string {
	switch decision {
	case bot.DecisionBotIgnored:
		return "📒 机器人发送的消息不记录，也不会提醒。"
	case bot.DecisionCommand:
		return "📒 命令不记录，也不会提醒。"
	case bot.DecisionMentionOnly:
		return "📒 这条只有 @，不记录，也不会提醒。"
	case bot.DecisionShortTextSkipped:
		return "📒 内容太短，或只有表情符号，不记录。"
	case bot.DecisionRepeatedCharSkipped:
		return "📒 同一个字重复刷屏，不记录。"
	case bot.DecisionNoContent:
		return "📒 这条没有可记录的内容。"
	case bot.DecisionUnsupportedType:
		return "📒 这类消息不会记录，也不会提醒。"
	default:
		return "📒 这条消息不会记录，也不会提醒。"
	}
}

func hywTargetRecorded(record *models.ForwardRecord, logs []models.ForwardLog, targetMsgID int) bool {
	if record.FirstTargetMsgID == targetMsgID {
		return true
	}
	if record.LastOccurrenceMsgID != nil && *record.LastOccurrenceMsgID == targetMsgID {
		return true
	}
	for _, log := range logs {
		if log.TargetMsgID == targetMsgID {
			return true
		}
	}
	return false
}

func formatHYWLookup(record *models.ForwardRecord, logs []models.ForwardLog, target *telebot.Message, now time.Time, loc *time.Location) string {
	firstAt := record.CreatedAt
	latestAt := record.UpdatedAt
	if len(logs) > 0 {
		if firstAt.IsZero() {
			firstAt = logs[0].CreatedAt
		}
		// 流水时间就是出现时间，优先于记录的 UpdatedAt（后者可能被配对回滚更新）。
		if !logs[len(logs)-1].CreatedAt.IsZero() {
			latestAt = logs[len(logs)-1].CreatedAt
		}
	}
	first := bot.FormatShortTime(firstAt, now, loc)
	latest := bot.FormatShortTime(latestAt, now, loc)
	if first == "" {
		first = "未知"
	}
	if latest == "" {
		latest = "未知"
	}

	var sb strings.Builder
	sb.WriteString("📒 这条已记录。\n")
	fmt.Fprintf(&sb, "一共出现 %d 次（含第一次）。\n", record.ForwardCount)
	fmt.Fprintf(&sb, "最早 %s，最近 %s。\n", first, latest)
	sb.WriteString(deriveHYWDecision(record, logs, target.ID))
	return sb.String()
}

// deriveHYWDecision 只使用库中确实存在的证据推导，不臆造每条消息的决策：
//   - 首次出现一定是 paired_silent；
//   - 能找到上一条流水且消息 ID 差 ≤5 时，是 adjacent_repeat_exempt；
//   - 其余情况只有“最新一次 + 当前已无待配对”能确定为 alerted；
//   - 冷却抑制、发送失败和后续配对会让中间消息的决策无法从流水唯一还原。
func deriveHYWDecision(record *models.ForwardRecord, logs []models.ForwardLog, targetMsgID int) string {
	if record.FirstTargetMsgID == targetMsgID {
		return "这是第一次出现，只记录，没有提醒。"
	}

	index := -1
	for i := range logs {
		if logs[i].TargetMsgID == targetMsgID {
			index = i
			break
		}
	}
	if index < 0 {
		return "这条消息的单次记录已经不在了，没法判断当时有没有提醒。"
	}
	if index == 0 {
		return "更早的出现已经不在记录里，没法判断这次有没有提醒。"
	}
	if delta := targetMsgID - logs[index-1].TargetMsgID; delta >= 0 && delta <= bot.AdjacentRepeatGap {
		return "和上一条挨得很近，所以没有提醒。"
	}

	isLatest := false
	if record.LastOccurrenceMsgID != nil {
		isLatest = *record.LastOccurrenceMsgID == targetMsgID
	} else if len(logs) > 0 {
		isLatest = logs[len(logs)-1].TargetMsgID == targetMsgID
	}
	if !isLatest {
		return "后面又出现过同样的内容，没法判断这次有没有提醒。"
	}
	if hywEffectivePending(record) {
		return "这次有没有发出提醒，现有记录看不出来。可能只做了记录，也可能提醒被间隔拦住，或当时没有发出去。"
	}
	return "这次发出了提醒。"
}

func hywEffectivePending(record *models.ForwardRecord) bool {
	if record.PendingPair != nil {
		return *record.PendingPair
	}
	return record.ForwardCount%2 == 1
}
