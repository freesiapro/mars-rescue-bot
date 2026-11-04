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
	hywUsage       = "回复一条消息再用 /hyw，我帮你翻翻火星小本本。"
	hywPrivateHint = "群里用 /hyw 吧，私聊我翻不到小本本。"
	hywLookupError = "翻小本本翻出错了，稍后再试。"
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
		return sendHYWReply(c, target, "📒 这是 bot 发的，火星小本本不收。")
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
		return sendHYWReply(c, target, "📒 小本本上没这条。可能是第一次见，或者已经过保留期。")
	}

	logs, err := h.repo.ListRecordLogs(record.ID)
	if err != nil {
		return c.Reply(hywLookupError)
	}
	if !hywTargetRecorded(record, logs, target.ID) {
		return sendHYWReply(c, target, "📒 小本本上有这个内容，但没这条的流水；可能没记上，或者已经过保留期。")
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
		return "📒 这是 bot 发的，火星小本本不收。"
	case bot.DecisionCommand:
		return "📒 这是命令，不是聊天内容，小本本不收。"
	case bot.DecisionMentionOnly:
		return "📒 这条只有 @，不检测也不记录。"
	case bot.DecisionShortTextSkipped:
		return "📒 这条太短（或纯链接/表情），够不上火星标准。"
	case bot.DecisionRepeatedCharSkipped:
		return "📒 这条是同一个字复读，太敷衍，不记。"
	case bot.DecisionNoContent:
		return withPresentFields("📒 这条没内容，记不上。", presentFields)
	case bot.DecisionUnsupportedType:
		return withPresentFields("📒 这类消息不参与去重，没记。", presentFields)
	default:
		return "📒 这条不参与去重，没记。"
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
	fmt.Fprintf(&sb, "📒 记着呢：%s · 指纹 %s\n", content.KeyType, content.Hash)
	fmt.Fprintf(&sb, "累计 %d 次 · 首次 %s · 最近 %s\n", record.ForwardCount, first, latest)
	fmt.Fprintf(&sb, "决策：%s", deriveHYWDecision(record, logs, target.ID))
	return sb.String()
}

// deriveHYWDecision 只使用库中确实存在的证据推导，不臆造每条消息的决策：
//   - 首次出现一定是 paired_silent；
//   - 能找到上一条流水且消息 ID 差 ≤5 时，是 adjacent_repeat_exempt；
//   - 其余情况只有“最新一次 + 当前已无待配对”能确定为 alerted；
//   - 冷却抑制、发送失败和后续配对会让中间消息的决策无法从流水唯一还原。
func deriveHYWDecision(record *models.ForwardRecord, logs []models.ForwardLog, targetMsgID int) string {
	if record.FirstTargetMsgID == targetMsgID {
		return bot.DecisionPairedSilent + "（第一次见，先记下）"
	}

	index := -1
	for i := range logs {
		if logs[i].TargetMsgID == targetMsgID {
			index = i
			break
		}
	}
	if index < 0 {
		return "没法确定（这条的流水已经过保留期）"
	}
	if index == 0 {
		return "没法确定（前面的流水已经过保留期）"
	}
	if delta := targetMsgID - logs[index-1].TargetMsgID; delta >= 0 && delta <= bot.AdjacentRepeatGap {
		return bot.DecisionAdjacentRepeatExempt + "（跟上一波连着刷，没打扰）"
	}

	isLatest := false
	if record.LastOccurrenceMsgID != nil {
		isLatest = *record.LastOccurrenceMsgID == targetMsgID
	} else if len(logs) > 0 {
		isLatest = logs[len(logs)-1].TargetMsgID == targetMsgID
	}
	if !isLatest {
		return "没法确定（后面还有出现，逐条决策没入库）"
	}
	if hywEffectivePending(record) {
		return "没法确定（可能是静默配对，也可能是提醒被冷却/发送失败回滚）"
	}
	return bot.DecisionAlerted + "（这次提醒发出去了）"
}

func hywEffectivePending(record *models.ForwardRecord) bool {
	if record.PendingPair != nil {
		return *record.PendingPair
	}
	return record.ForwardCount%2 == 1
}
