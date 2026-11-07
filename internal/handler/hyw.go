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
	hywUsage       = "请先回复一条消息，再发送 /hyw。"
	hywPrivateHint = "请在群聊中回复消息后使用 /hyw。"
	hywLookupError = "查询失败，请稍后重试。"
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
		return sendHYWReply(c, target, "📒 机器人发送的消息不记录。")
	}

	content := bot.AnalyzeMessageContent(target, h.dedupSecret)
	if content.Key == "" {
		return sendHYWReply(c, target, hywSkipReason(content.SkipDecision, bot.MessagePresentFields(target)))
	}

	record, found, err := h.repo.FindRecordByKey(msg.Chat.ID, content.Key)
	if err != nil {
		return c.Reply(hywLookupError)
	}
	if !found {
		return sendHYWReply(c, target, "📒 没有这条记录。可能是第一次出现，或已超过保留期。")
	}

	logs, err := h.repo.ListRecordLogs(record.ID)
	if err != nil {
		return c.Reply(hywLookupError)
	}
	if !hywTargetRecorded(record, logs, target.ID) {
		return sendHYWReply(c, target, "📒 有相同内容的记录，但这条消息没有留下记录。可能当时未记录，或已超过保留期。")
	}

	return sendHYWReply(c, target, formatHYWLookup(record, logs, target, content, h.now(), h.statsLoc))
}

func sendHYWReply(c telebot.Context, target *telebot.Message, text string) error {
	return c.Send(text, &telebot.SendOptions{
		ReplyTo:           target,
		AllowWithoutReply: true,
	})
}

func hywSkipReason(decision string, presentFields []string) string {
	switch decision {
	case bot.DecisionBotIgnored:
		return "📒 机器人发送的消息不记录。"
	case bot.DecisionCommand:
		return "📒 命令不记录。"
	case bot.DecisionMentionOnly:
		return "📒 只有 @ 的消息不记录。"
	case bot.DecisionShortTextSkipped:
		return "📒 内容太短，或只有表情符号，不记录。"
	case bot.DecisionRepeatedCharSkipped:
		return "📒 同一个字重复刷屏，不记录。"
	case bot.DecisionNoContent:
		return withPresentFields("📒 没有可记录的内容。", presentFields)
	case bot.DecisionUnsupportedType:
		return withPresentFields("📒 这类消息不参与查重，未记录。", presentFields)
	default:
		return "📒 这条消息不参与查重，未记录。"
	}
}

// withPresentFields 只在没有内容时补充字段名，方便现场判断是字段缺失、
// 未解析还是确实为空。只列字段名，绝不输出字段值或正文。
func withPresentFields(base string, presentFields []string) string {
	if len(presentFields) == 0 {
		return base
	}
	return base + "\n字段：" + strings.Join(presentFields, ", ")
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

func formatHYWLookup(record *models.ForwardRecord, logs []models.ForwardLog, target *telebot.Message, content bot.MessageContent, now time.Time, loc *time.Location) string {
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
	fmt.Fprintf(&sb, "📒 已记录：%s · 短码 %s\n", content.KeyType, content.Hash)
	fmt.Fprintf(&sb, "出现 %d 次（含第一次） · 最早 %s · 最近 %s\n", record.ForwardCount, first, latest)
	fmt.Fprintf(&sb, "%s", deriveHYWDecision(record, logs, target.ID))
	return sb.String()
}

// deriveHYWDecision 只使用库中确实存在的证据推导，不臆造每条消息的决策：
//   - 首次出现一定是 paired_silent；
//   - 能找到上一条流水且消息 ID 差 ≤5 时，是 adjacent_repeat_exempt；
//   - 其余情况只有“最新一次 + 当前已无待配对”能确定为 alerted；
//   - 冷却抑制、发送失败和后续配对会让中间消息的决策无法从流水唯一还原。
func deriveHYWDecision(record *models.ForwardRecord, logs []models.ForwardLog, targetMsgID int) string {
	if record.FirstTargetMsgID == targetMsgID {
		return "第一次出现，已记录，未提醒"
	}

	index := -1
	for i := range logs {
		if logs[i].TargetMsgID == targetMsgID {
			index = i
			break
		}
	}
	if index < 0 {
		return "没法确定：这条记录已超过保留期"
	}
	if index == 0 {
		return "没法确定：更早的记录已超过保留期"
	}
	if delta := targetMsgID - logs[index-1].TargetMsgID; delta >= 0 && delta <= bot.AdjacentRepeatGap {
		return "连续刷屏，未发送提醒"
	}

	isLatest := false
	if record.LastOccurrenceMsgID != nil {
		isLatest = *record.LastOccurrenceMsgID == targetMsgID
	} else if len(logs) > 0 {
		isLatest = logs[len(logs)-1].TargetMsgID == targetMsgID
	}
	if !isLatest {
		return "没法确定：后面还有记录，无法判断是否已提醒"
	}
	if hywEffectivePending(record) {
		return "没法确定：可能仅作记录，也可能提醒被间隔拦住或发送失败"
	}
	return "本次已发出提醒"
}

func hywEffectivePending(record *models.ForwardRecord) bool {
	if record.PendingPair != nil {
		return *record.PendingPair
	}
	return record.ForwardCount%2 == 1
}
