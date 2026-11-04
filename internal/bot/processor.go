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
	"mars-rescue-bot/internal/names"
	"mars-rescue-bot/internal/repo"
	"mars-rescue-bot/internal/textnorm"
)

// AdjacentRepeatGap 是同一内容连续刷屏的 message_id 间隔阈值。
// 两次出现的消息 ID 相差不超过该值时，视为同一波刷屏：只计数，不提醒。
const AdjacentRepeatGap = 5

// Processor 处理群内重复内容检测与回复。
// 配对规则：同一 (群, 内容键) 的出现按顺序两两配对。第 1、3、5… 次只记录，
// 第 2、4、6… 次与上一次组成一对并发送火星提醒；每次出现都照常计入统计。
// 同一波连续刷屏（消息 ID 相差不超过 AdjacentRepeatGap）只计数，不提醒，
// 也不消耗配对位置。
type Processor struct {
	repo              *repo.Repo
	cooldown          *Cooldown
	names             *names.Resolver
	statsLoc          *time.Location
	dedupSecret       string
	maxAttempts       int
	sleep             func(time.Duration)
	now               func() time.Time
	adjacentRepeatGap int
}

func NewProcessor(r *repo.Repo, dedupSecret string, nameResolver *names.Resolver, statsLoc *time.Location) *Processor {
	if nameResolver == nil {
		nameResolver = names.New(nil)
	}
	if statsLoc == nil {
		statsLoc = time.UTC
	}
	return &Processor{
		repo:              r,
		cooldown:          NewCooldown(),
		names:             nameResolver,
		statsLoc:          statsLoc,
		dedupSecret:       dedupSecret,
		maxAttempts:       maxReplyAttempts,
		sleep:             time.Sleep,
		now:               time.Now,
		adjacentRepeatGap: AdjacentRepeatGap,
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
	// 机器人（包括本 bot）自己发送的消息不参与去重，避免互相触发。
	if msg.Sender.IsBot {
		return nil
	}
	p.names.Observe(msg.Sender)

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
		p.tryMediaDuplicate,
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
	// mediaSubject 是媒体提醒里的名词（这张图、这段视频…），只影响文案。
	mediaSubject string
	sender       *telebot.User
	// seq 是本次出现后的 forward_count，用于提醒发送失败时条件回滚配对状态。
	seq int64
}

// === 场景 A：频道转发 ===
func (p *Processor) tryChannelForward(msg *telebot.Message) (*duplicateEvent, error) {
	key, ok := channelForwardKey(msg)
	if !ok {
		return nil, nil
	}
	return p.logOccurrence(msg, key, "channel")
}

// channelForwardKey 优先读取新版 forward_origin，再回退旧版 forward_from_chat。
// 只有频道来源使用原帖 ID；用户/群组来源没有可用的原消息 ID，按内容去重。
func channelForwardKey(msg *telebot.Message) (string, bool) {
	if msg == nil {
		return "", false
	}
	if msg.Origin != nil && msg.Origin.Type == "channel" && msg.Origin.Chat != nil {
		return fmt.Sprintf("channel:%d:%d", msg.Origin.Chat.ID, msg.Origin.MessageID), true
	}
	if msg.OriginalChat != nil && msg.OriginalChat.Type == telebot.ChatChannel {
		return fmt.Sprintf("channel:%d:%d", msg.OriginalChat.ID, msg.OriginalMessageID), true
	}
	return "", false
}

func isChannelForward(msg *telebot.Message) bool {
	_, ok := channelForwardKey(msg)
	return ok
}

// === 场景 B：媒体重复 ===
// 图片取最大尺寸（telebot 已选择）的 file_unique_id；视频、动画、文件直接取其
// file_unique_id。不下载文件，也不保存文件内容。频道转发的媒体走频道分支；
// 其他转发与手打媒体使用同一个内容键。
func (p *Processor) tryMediaDuplicate(msg *telebot.Message) (*duplicateEvent, error) {
	if isChannelForward(msg) {
		return nil, nil
	}
	key, subject, ok := mediaContentKey(msg)
	if !ok {
		return nil, nil
	}
	event, err := p.logOccurrence(msg, key, "media")
	if event != nil {
		event.mediaSubject = subject
	}
	return event, err
}

// mediaContentKey 返回去重键和提醒文案里的名词。名词只影响展示，不参与去重。
func mediaContentKey(msg *telebot.Message) (key, subject string, ok bool) {
	switch {
	case msg.Photo != nil && msg.Photo.UniqueID != "":
		return "media:photo:" + msg.Photo.UniqueID, "这张图", true
	case msg.Video != nil && msg.Video.UniqueID != "":
		return "media:video:" + msg.Video.UniqueID, "这段视频", true
	case msg.Animation != nil && msg.Animation.UniqueID != "":
		return "media:animation:" + msg.Animation.UniqueID, "这个动图", true
	case msg.Document != nil && msg.Document.UniqueID != "":
		return "media:document:" + msg.Document.UniqueID, "这个文件", true
	}
	return "", "", false
}

// === 场景 C：纯文本重复 ===
func (p *Processor) tryTextDuplicate(msg *telebot.Message) (*duplicateEvent, error) {
	if isChannelForward(msg) || msg.Text == "" || msg.Photo != nil || msg.Video != nil || msg.Audio != nil || msg.Document != nil {
		return nil, nil
	}
	// 命令不该进入去重逻辑（未注册的命令会被 telebot 交给 OnText）。
	text := strings.TrimSpace(msg.Text)
	if text == "" || strings.HasPrefix(text, "/") {
		return nil, nil
	}

	normalized := textnorm.Normalize(text)
	// 短消息、纯 URL、纯表情/符号不参与文本去重。
	if !textnorm.HasContent(normalized, textnorm.DefaultMinRunes) {
		return nil, nil
	}
	// 同一字符刷屏（哈哈哈哈、666666）是口头禅，不记录也不计数。
	if textnorm.IsRepeatedSingleRune(normalized) {
		return nil, nil
	}

	key := textContentKey(p.dedupSecret, normalized)
	return p.logOccurrence(msg, key, "text")
}

// logOccurrence 写入本次出现并在需要时返回提醒事件。
func (p *Processor) logOccurrence(msg *telebot.Message, key, kind string) (*duplicateEvent, error) {
	if err := p.repo.UpsertUserStats(msg.Sender.ID); err != nil {
		return nil, err
	}

	rec, isNew, err := p.repo.FindOrCreateRecord(msg.Chat.ID, key, msg.ID, msg.Sender.ID)
	if err != nil || isNew {
		return nil, err
	}

	// 决策 1：不区分发送者，自己重复发也计入配对并提醒。
	decision, err := p.repo.DecideAndLog(rec, msg.ID, msg.Sender.ID, p.adjacentRepeatGap)
	if err != nil {
		return nil, err
	}
	if !decision.Alert {
		return nil, nil
	}
	return &duplicateEvent{record: rec, kind: kind, sender: msg.Sender, seq: decision.Seq}, nil
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

	text := p.formatDuplicate(msg.Chat, event, settings.MentionEnabled)
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

func (p *Processor) formatDuplicate(chat *telebot.Chat, event *duplicateEvent, mention bool) string {
	who := formatDisplayName(event.sender)
	if mention {
		who = formatMention(event.sender)
	}

	prefix := fmt.Sprintf("🚨 火星救援！%s，%s", who, event.subject())

	firstSenderID := event.record.FirstSenderID
	if firstSenderID <= 0 {
		return prefix + "群里早就有人发过了"
	}

	firstSeen := p.formatFirstSeen(event.record.CreatedAt)
	linkHTML := ""
	if link, ok := messageLink(chat, event.record.FirstTargetMsgID); ok {
		linkHTML = fmt.Sprintf("<a href=\"%s\">回现场</a>", html.EscapeString(link))
	}

	if event.sender != nil && event.sender.ID == firstSenderID {
		var sb strings.Builder
		sb.WriteString(prefix)
		sb.WriteString("你自己")
		if firstSeen != "" {
			sb.WriteString(" ")
			sb.WriteString(firstSeen)
		}
		sb.WriteString(" 就发过了 😂")
		if linkHTML != "" {
			sb.WriteString(" ")
			sb.WriteString(linkHTML)
		}
		return sb.String()
	}

	firstSender := p.names.Name(chat, firstSenderID)
	if firstSender == "" {
		firstSender = fmt.Sprintf("用户 %d", firstSenderID)
	}

	var sb strings.Builder
	sb.WriteString(prefix)
	sb.WriteString(" ")
	sb.WriteString(html.EscapeString(firstSender))
	if firstSeen != "" {
		sb.WriteString(" ")
		sb.WriteString(firstSeen)
	}
	sb.WriteString(" 就发过了")
	if linkHTML != "" {
		sb.WriteString("，")
		sb.WriteString(linkHTML)
	}
	return sb.String()
}

// formatFirstSeen 返回提醒里的短时间：当天只显示时:分，其他日期显示月-日 时:分。
func (p *Processor) formatFirstSeen(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	loc := p.statsLoc
	if loc == nil {
		loc = time.UTC
	}
	local := t.In(loc)
	now := time.Now().In(loc)
	if p.now != nil {
		now = p.now().In(loc)
	}
	if local.Year() == now.Year() && local.Month() == now.Month() && local.Day() == now.Day() {
		return local.Format("15:04")
	}
	return local.Format("01-02 15:04")
}

// subject 是提醒正文里的名词，必须与检测方式一致：
// 文本被归一化后比较，媒体按 file_unique_id 去重，频道转发按原帖识别。
func (e *duplicateEvent) subject() string {
	switch e.kind {
	case "channel":
		return "这条转发"
	case "media":
		if e.mediaSubject != "" {
			return e.mediaSubject
		}
		return "这条"
	default:
		return "这条"
	}
}

// textContentKey 生成带密钥的文本内容键。
// text 必须是 textnorm.Normalize 的结果；只存储 HMAC-SHA256 十六进制结果，不存储原文。
// v2 前缀用于区分旧版原始文本哈希，升级后旧 key 不再与新 key 匹配。
func textContentKey(secret, text string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(text))
	return "text:v2:" + hex.EncodeToString(mac.Sum(nil))
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

func FormatRankList(stats []models.UserStats, chat *telebot.Chat, chatTitle, periodLabel string, resolver *names.Resolver) string {
	if periodLabel == "" {
		periodLabel = "本周"
	}
	if len(stats) == 0 {
		return fmt.Sprintf("📊 %s暂无火星记录", periodLabel)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🏆 <b>%s</b> 火星指数榜（%s）\n\n", html.EscapeString(chatTitle), periodLabel))
	for i, s := range stats {
		name := resolver.Name(chat, s.UserID)
		if name == "" {
			name = fmt.Sprintf("用户 %d", s.UserID)
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
		sb.WriteString(fmt.Sprintf("%s%d. %s — %d 次\n", medal, i+1, name, s.RescueCount))
	}
	return sb.String()
}

// FormatUserStats 展示某用户在本群的本周、本月、累计出现次数与最近一次时间。
func FormatUserStats(weekCount, monthCount, totalCount int64, last time.Time, loc *time.Location) string {
	if totalCount == 0 {
		return "📊 你在本群还没有火星记录。"
	}
	if loc == nil {
		loc = time.UTC
	}
	lastText := "未知"
	if !last.IsZero() {
		lastText = last.In(loc).Format("2006-01-02 15:04")
	}
	return fmt.Sprintf("📊 <b>你的火星指数</b>\n本周：%d 次\n本月：%d 次\n累计：%d 次\n最近一次：%s",
		weekCount, monthCount, totalCount, html.EscapeString(lastText))
}
