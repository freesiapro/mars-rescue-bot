package bot

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"time"

	"gopkg.in/telebot.v3"
	"mars-rescue-bot/internal/logger"
	"mars-rescue-bot/internal/models"
	"mars-rescue-bot/internal/repo"
)

// Processor 处理群内重复内容检测与回复。
// 配对规则：同一 (群, 内容键) 的出现按顺序两两配对。第 1、3、5… 次只记录，
// 第 2、4、6… 次与上一次组成一对并发送火星提醒；每次出现都照常计入统计。
type Processor struct {
	repo        *repo.Repo
	cooldown    *Cooldown
	dedupSecret string
	maxAttempts int
	sleep       func(time.Duration)
}

func NewProcessor(r *repo.Repo, dedupSecret string) *Processor {
	return &Processor{
		repo:        r,
		cooldown:    NewCooldown(),
		dedupSecret: dedupSecret,
		maxAttempts: maxReplyAttempts,
		sleep:       time.Sleep,
	}
}

// Handle 是 telebot 的处理器入口。
func (p *Processor) Handle(c telebot.Context) error {
	msg := c.Message()
	traceID := logger.TraceIDFromContext(c)
	return p.ProcessMessage(msg, traceID, func(text string) error {
		return sendReplyWithRetry(func() error {
			return c.Reply(text, telebot.ModeHTML)
		}, p.maxAttempts, p.sleep)
	})
}

// ProcessMessage 执行去重与回复逻辑。
// reply 由调用方提供，便于在纯 Go 测试中捕获输出。
func (p *Processor) ProcessMessage(msg *telebot.Message, traceID string, reply func(string) error) error {
	if msg == nil || msg.Chat == nil || msg.Chat.Type == telebot.ChatPrivate {
		return nil
	}
	// 匿名管理员或频道发言可能没有 Sender，后续逻辑依赖其身份，安全跳过。
	if msg.Sender == nil {
		slog.Debug("skip message without sender", "chat_id", msg.Chat.ID)
		return nil
	}

	settings, err := p.repo.GetChatSettings(msg.Chat.ID)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		slog.Debug("chat disabled, skip", "chat_id", msg.Chat.ID)
		return nil
	}

	ignored, err := p.repo.IsIgnored(msg.Chat.ID, msg.Sender.ID)
	if err != nil {
		return err
	}
	if ignored {
		slog.Debug("sender ignored, skip", "chat_id", msg.Chat.ID, "user_id", msg.Sender.ID)
		return nil
	}

	for _, try := range []func(*telebot.Message) (*duplicateEvent, error){
		p.tryChannelForward,
		p.tryTextDuplicate,
	} {
		event, err := try(msg)
		if err != nil {
			return err
		}
		if event != nil {
			return p.emitReply(msg, event, settings, traceID, reply)
		}
	}
	return nil
}

type duplicateEvent struct {
	record *models.ForwardRecord
	kind   string
	sender *telebot.User
	// seq 是本次出现后的 forward_count，用于提醒发送失败时条件回滚配对状态。
	seq int64
}

// === 场景 A：频道转发 ===
func (p *Processor) tryChannelForward(msg *telebot.Message) (*duplicateEvent, error) {
	if !msg.IsForwarded() || msg.OriginalChat == nil || msg.OriginalChat.Type != telebot.ChatChannel {
		return nil, nil
	}

	if err := p.repo.UpsertUserStats(msg.Sender.ID, msg.Sender.Username, msg.Sender.FirstName); err != nil {
		return nil, err
	}

	key := fmt.Sprintf("channel:%d:%d", msg.OriginalChat.ID, msg.OriginalMessageID)
	rec, isNew, err := p.repo.FindOrCreateRecord(msg.Chat.ID, key, msg.ID, msg.Sender.ID)
	if err != nil || isNew {
		return nil, err
	}

	decision, err := p.repo.DecideAndLog(rec, msg.ID, msg.Sender.ID)
	if err != nil {
		return nil, err
	}
	if !decision.Alert {
		return nil, nil
	}
	return &duplicateEvent{record: rec, kind: "channel", sender: msg.Sender, seq: decision.Seq}, nil
}

// === 场景 B：纯文本重复 ===
func (p *Processor) tryTextDuplicate(msg *telebot.Message) (*duplicateEvent, error) {
	if msg.IsForwarded() || msg.Text == "" || msg.Photo != nil || msg.Video != nil || msg.Audio != nil || msg.Document != nil {
		return nil, nil
	}
	// 命令不该进入去重逻辑（未注册的命令会被 telebot 交给 OnText）。
	if strings.HasPrefix(strings.TrimSpace(msg.Text), "/") {
		return nil, nil
	}

	if err := p.repo.UpsertUserStats(msg.Sender.ID, msg.Sender.Username, msg.Sender.FirstName); err != nil {
		return nil, err
	}

	key := textContentKey(p.dedupSecret, msg.Text)
	rec, isNew, err := p.repo.FindOrCreateRecord(msg.Chat.ID, key, msg.ID, msg.Sender.ID)
	if err != nil || isNew {
		return nil, err
	}

	// 决策 1：不区分发送者，自己重复发也计入配对并提醒。
	decision, err := p.repo.DecideAndLog(rec, msg.ID, msg.Sender.ID)
	if err != nil {
		return nil, err
	}
	if !decision.Alert {
		return nil, nil
	}
	return &duplicateEvent{record: rec, kind: "text", sender: msg.Sender, seq: decision.Seq}, nil
}

func (p *Processor) emitReply(msg *telebot.Message, event *duplicateEvent, settings *models.ChatSettings, traceID string, reply func(string) error) error {
	// 配对状态已在 DecideAndLog 中随计数一起原子翻转。但“取消配对”只在提醒
	// 真正发出后才成立：冷却抑制或发送失败时都要回滚，否则这一对会被静默吞掉。
	cooldown := time.Duration(settings.CooldownSeconds) * time.Second
	if !p.cooldown.Allow(msg.Chat.ID, cooldown) {
		slog.Info("reply suppressed by cooldown",
			"trace_id", traceID,
			"chat_id", msg.Chat.ID,
			"cooldown_seconds", settings.CooldownSeconds,
		)
		if err := p.repo.RevertPairClaim(event.record, event.seq); err != nil {
			slog.Error("revert pair claim after cooldown failed",
				"trace_id", traceID,
				"chat_id", msg.Chat.ID,
				"error", err,
			)
		}
		return nil
	}

	text := formatDuplicate(msg.Chat, event, settings.MentionEnabled)
	if err := reply(text); err != nil {
		slog.Error("send reply failed", "trace_id", traceID, "chat_id", msg.Chat.ID, "error", err)
		// 发送失败（例如 429 重试耗尽）不算配对成功，恢复待配对状态，
		// 让后续出现仍有机会重新提醒；条件回滚保证不会覆盖并发推进的新状态。
		if rbErr := p.repo.RevertPairClaim(event.record, event.seq); rbErr != nil {
			slog.Error("revert pair claim after send failure failed",
				"trace_id", traceID,
				"chat_id", msg.Chat.ID,
				"error", rbErr,
			)
		}
		return err
	}
	return nil
}

func formatDuplicate(chat *telebot.Chat, event *duplicateEvent, mention bool) string {
	who := formatDisplayName(event.sender)
	if mention {
		who = formatMention(event.sender)
	}

	link, hasLink := messageLink(chat, event.record.FirstTargetMsgID)
	switch event.kind {
	case "channel":
		if hasLink {
			return fmt.Sprintf("🚨 %s 火星救援！<a href=\"%s\">这条消息</a>已经被打捞过一次了，你是来炒冷饭的？",
				who, html.EscapeString(link))
		}
		return fmt.Sprintf("🚨 %s 火星救援！这条消息已经被打捞过一次了，你是来炒冷饭的？", who)
	default:
		count := event.record.ForwardCount
		if hasLink {
			return fmt.Sprintf("🚨 %s 火星救援！<a href=\"%s\">这句话</a>已被发送 %d 次，你是第 %d 个没看聊天记录的",
				who, html.EscapeString(link), count, count)
		}
		return fmt.Sprintf("🚨 %s 火星救援！这句话已被发送 %d 次，你是第 %d 个没看聊天记录的",
			who, count, count)
	}
}

// textContentKey 生成带密钥的文本内容键。
// 只存储 HMAC-SHA256 十六进制结果，不存储原文。
func textContentKey(secret, text string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(text))
	return "text:hmac:" + hex.EncodeToString(mac.Sum(nil))
}

// messageLink 生成消息链接。
// 公开群使用 t.me/<username>/<msg_id>；私有超级群使用
// t.me/c/<去掉 -100 后的内部 ID>/<msg_id>；普通群无法生成可用链接。
func messageLink(chat *telebot.Chat, messageID int) (string, bool) {
	if chat == nil || messageID <= 0 {
		return "", false
	}
	if chat.Username != "" {
		return fmt.Sprintf("https://t.me/%s/%d", chat.Username, messageID), true
	}
	if chat.Type != telebot.ChatSuperGroup && chat.Type != telebot.ChatChannel {
		return "", false
	}
	if chat.ID >= -1000000000000 {
		return "", false
	}
	internalID := -chat.ID - 1000000000000
	if internalID <= 0 {
		return "", false
	}
	return fmt.Sprintf("https://t.me/c/%d/%d", internalID, messageID), true
}

func formatDisplayName(user *telebot.User) string {
	name := ""
	if user != nil {
		switch {
		case user.FirstName != "":
			name = user.FirstName
		case user.Username != "":
			name = user.Username
		}
	}
	if name == "" {
		name = "某位群友"
	}
	return html.EscapeString(name)
}

func formatMention(user *telebot.User) string {
	if user == nil {
		return "某位群友"
	}
	return fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>", user.ID, formatDisplayName(user))
}

func FormatRankList(stats []models.UserStats, chatTitle string) string {
	if len(stats) == 0 {
		return "📊 本周暂无火星记录"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🏆 <b>%s</b> 火星指数榜（本周）\n\n", html.EscapeString(chatTitle)))
	for i, s := range stats {
		name := s.FirstName
		if s.Username != "" {
			name = "@" + s.Username
		}
		name = html.EscapeString(name)
		medal := ""
		switch i {
		case 0:
			medal = "🥇 "
		case 1:
			medal = "🥈 "
		case 2:
			medal = "🥉 "
		}
		sb.WriteString(fmt.Sprintf("%s%d. %s — %d 条不同内容\n", medal, i+1, name, s.RescueCount))
	}
	return sb.String()
}
