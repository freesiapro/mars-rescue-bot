package handler

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gopkg.in/telebot.v4"
	"mars-rescue-bot/internal/bot"
	"mars-rescue-bot/internal/config"
	"mars-rescue-bot/internal/logger"
	"mars-rescue-bot/internal/models"
	"mars-rescue-bot/internal/names"
	"mars-rescue-bot/internal/repo"
)

type CommandHandler struct {
	repo          *repo.Repo
	admins        *AdminChecker
	names         *names.Resolver
	now           func() time.Time
	statsLoc      *time.Location
	retentionDays int
	dedupSecret   string
}

func NewCommandHandler(r *repo.Repo, admins *AdminChecker, resolver *names.Resolver, dedupSecret string) *CommandHandler {
	if resolver == nil {
		resolver = names.New(nil)
	}
	retentionDays := config.DataRetentionDays
	if retentionDays <= 0 {
		retentionDays = config.DefaultDataRetentionDays
	}
	return &CommandHandler{
		repo:          r,
		admins:        admins,
		names:         resolver,
		now:           time.Now,
		statsLoc:      config.StatsLocation(),
		retentionDays: retentionDays,
		dedupSecret:   dedupSecret,
	}
}

func (h *CommandHandler) Register(bot *telebot.Bot) {
	bot.Handle("/start", h.handleStart)
	bot.Handle("/hyw", h.handleHYW)
	bot.Handle("/mars_rank", h.handleRank)
	bot.Handle("/mars_me", h.handleMe)
	bot.Handle("/mars_privacy", h.handlePrivacy)
	bot.Handle("/mars_off", h.handleOff)
	bot.Handle("/mars_on", h.handleOn)
	bot.Handle("/mars_status", h.handleStatus)
	bot.Handle("/mars_ignore", h.handleIgnore)
	bot.Handle("/mars_unignore", h.handleUnignore)
	bot.Handle("/mars_ignore_list", h.handleIgnoreList)
	bot.Handle("/mars_cooldown", h.handleCooldown)
	bot.Handle("/mars_mention", h.handleMention)
}

func (h *CommandHandler) handleStart(c telebot.Context) error {
	traceID := logger.TraceIDFromContext(c)
	sender := c.Sender()
	if sender == nil {
		slog.Warn("skip /start without sender", "trace_id", traceID)
		return nil
	}
	slog.Info("处理 /start 命令",
		"trace_id", traceID,
		"user_id", sender.ID,
	)
	return c.Reply(
		"火星救援。\n\n" +
			"同样的话、图、文件，或者同一条频道转发再出现时，我会提一句。通常隔一次提一次；挨得很近的连刷只计数，不提醒。设了提醒间隔的话，间隔之内也不会再提。\n" +
			"第一次出现只记下，不算火星次数。哈哈哈哈、666666 这种同一个字刷出来的，不记。\n" +
			"文字会忽略大小写和标点。转发也算：频道转发认原帖，别的转发认文字，或图片、视频、文件本身。\n\n" +
			"/hyw - 回复一条消息，看看有没有记下（只查，不改）\n" +
			"/mars_rank - 本群火星次数。后面可以加 week、month 或 all，默认本周\n" +
			"/mars_me - 看自己的火星次数\n" +
			"/mars_privacy - 看这个机器人存了什么\n" +
			"/mars_status - 看本群开关（管理员）\n" +
			"/mars_off - 关掉本群检测（管理员）\n" +
			"/mars_on - 打开本群检测（管理员）\n" +
			"/mars_cooldown - 设置两次提醒最少隔几秒，0 到 86400，0 表示不间隔（管理员）\n" +
			"/mars_mention - 写 on 或 off，提醒里要不要 @ 人（管理员）\n" +
			"/mars_ignore - 忽略某人之后的重复内容。回复对方，或写数字 ID（管理员）\n" +
			"/mars_unignore - 取消忽略。回复对方，或写数字 ID（管理员）\n" +
			"/mars_ignore_list - 看忽略名单（管理员）",
	)
}

func (h *CommandHandler) handleRank(c telebot.Context) error {
	traceID := logger.TraceIDFromContext(c)
	msg := c.Message()
	if msg == nil || msg.Chat == nil {
		slog.Debug("skip /mars_rank without message", "trace_id", traceID)
		return nil
	}
	if msg.Sender == nil {
		slog.Debug("skip /mars_rank without sender", "trace_id", traceID, "chat_id", msg.Chat.ID)
		return nil
	}

	period, err := parseRankPeriod(c.Args())
	if err != nil {
		return c.Reply(err.Error(), telebot.ModeHTML)
	}
	var since time.Time
	switch period {
	case rankPeriodMonth:
		since = monthStart(h.now(), h.statsLoc)
	case rankPeriodAll:
		// 零值表示不限时间。
	default:
		since = weekStart(h.now(), h.statsLoc)
	}

	slog.Info("处理 /mars_rank 命令",
		"trace_id", traceID,
		"chat_id", msg.Chat.ID,
		"user_id", msg.Sender.ID,
		"period", string(period),
	)

	if msg.Chat.Type == telebot.ChatPrivate {
		slog.Debug("私聊中忽略 /mars_rank",
			"trace_id", traceID,
			"user_id", msg.Sender.ID,
		)
		return c.Reply("这个命令要在群里用。")
	}

	chatTitle := msg.Chat.Title
	if chatTitle == "" {
		chatTitle = msg.Chat.Username
	}
	if chatTitle == "" {
		chatTitle = "本群"
	}

	stats, err := h.repo.GetTopRescuers(msg.Chat.ID, since, 10)
	if err != nil {
		slog.Error("获取排行榜失败",
			"trace_id", traceID,
			"chat_id", msg.Chat.ID,
			"error", err.Error(),
		)
		return c.Reply("榜单没取到，请稍后再试。")
	}

	text := bot.FormatRankList(stats, msg.Chat, chatTitle, period.label(), h.names)
	return c.Reply(text, telebot.ModeHTML)
}

func (h *CommandHandler) handleMe(c telebot.Context) error {
	traceID := logger.TraceIDFromContext(c)
	msg := c.Message()
	if msg == nil || msg.Chat == nil || msg.Sender == nil {
		slog.Debug("skip /mars_me without message or sender", "trace_id", traceID)
		return nil
	}
	if msg.Chat.Type == telebot.ChatPrivate {
		return c.Reply("这个命令要在群里用。")
	}

	slog.Info("处理 /mars_me 命令",
		"trace_id", traceID,
		"chat_id", msg.Chat.ID,
		"user_id", msg.Sender.ID,
	)

	now := h.now()
	weekCount, err := h.repo.CountUserOccurrences(msg.Chat.ID, msg.Sender.ID, weekStart(now, h.statsLoc))
	if err != nil {
		return h.commandError(c, "本周次数没读出来", err)
	}
	monthCount, err := h.repo.CountUserOccurrences(msg.Chat.ID, msg.Sender.ID, monthStart(now, h.statsLoc))
	if err != nil {
		return h.commandError(c, "本月次数没读出来", err)
	}
	totalCount, err := h.repo.CountUserOccurrences(msg.Chat.ID, msg.Sender.ID, time.Time{})
	if err != nil {
		return h.commandError(c, "累计次数没读出来", err)
	}
	last, _, err := h.repo.LastUserOccurrence(msg.Chat.ID, msg.Sender.ID)
	if err != nil {
		return h.commandError(c, "最近一次记录没读出来", err)
	}

	return c.Reply(bot.FormatUserStats(weekCount, monthCount, totalCount, last, h.statsLoc), telebot.ModeHTML)
}

// --- 群级治理命令（仅管理员/群主） ---

func (h *CommandHandler) handleOff(c telebot.Context) error {
	msg, ok := h.authorize(c)
	if !ok {
		return nil
	}
	if err := h.repo.SetChatEnabled(msg.Chat.ID, false, msg.Sender.ID); err != nil {
		return h.commandError(c, "没能关掉本群检测", err)
	}
	return c.Reply("本群检测已关掉：不再记录，也不提醒。用 /mars_on 可以重新打开。")
}

func (h *CommandHandler) handleOn(c telebot.Context) error {
	msg, ok := h.authorize(c)
	if !ok {
		return nil
	}
	if err := h.repo.SetChatEnabled(msg.Chat.ID, true, msg.Sender.ID); err != nil {
		return h.commandError(c, "没能打开本群检测", err)
	}
	return c.Reply("本群检测已打开。")
}

func (h *CommandHandler) handleStatus(c telebot.Context) error {
	msg, ok := h.authorize(c)
	if !ok {
		return nil
	}
	settings, err := h.repo.GetChatSettings(msg.Chat.ID)
	if err != nil {
		return h.commandError(c, "本群设置没读出来", err)
	}
	ignoredCount, err := h.repo.CountIgnoredUsers(msg.Chat.ID)
	if err != nil {
		return h.commandError(c, "忽略名单没读出来", err)
	}
	return c.Reply(formatStatus(settings, ignoredCount), telebot.ModeHTML)
}

func (h *CommandHandler) handleIgnore(c telebot.Context) error {
	msg, ok := h.authorize(c)
	if !ok {
		return nil
	}
	target, err := parseIgnoreTarget(c.Args(), msg.ReplyTo)
	if err != nil {
		return c.Reply(err.Error(), telebot.ModeHTML)
	}
	added, err := h.repo.AddIgnoredUser(msg.Chat.ID, target, msg.Sender.ID)
	if err != nil {
		return h.commandError(c, "没能加入忽略名单", err)
	}
	if !added {
		return c.Reply(fmt.Sprintf("这个人已经在忽略名单里了（ID %d）。", target))
	}
	return c.Reply(fmt.Sprintf("已忽略这个人之后的重复内容（ID %d）。", target))
}

func (h *CommandHandler) handleUnignore(c telebot.Context) error {
	msg, ok := h.authorize(c)
	if !ok {
		return nil
	}
	target, err := parseIgnoreTarget(c.Args(), msg.ReplyTo)
	if err != nil {
		return c.Reply(err.Error(), telebot.ModeHTML)
	}
	removed, err := h.repo.RemoveIgnoredUser(msg.Chat.ID, target)
	if err != nil {
		return h.commandError(c, "没能移出忽略名单", err)
	}
	if !removed {
		return c.Reply(fmt.Sprintf("忽略名单里没有这个人（ID %d）。", target))
	}
	return c.Reply(fmt.Sprintf("已取消忽略（ID %d）。", target))
}

func (h *CommandHandler) handleIgnoreList(c telebot.Context) error {
	msg, ok := h.authorize(c)
	if !ok {
		return nil
	}
	ignored, err := h.repo.ListIgnoredUsers(msg.Chat.ID)
	if err != nil {
		return h.commandError(c, "忽略名单没读出来", err)
	}
	return c.Reply(formatIgnoreList(ignored), telebot.ModeHTML)
}

func (h *CommandHandler) handleCooldown(c telebot.Context) error {
	msg, ok := h.authorize(c)
	if !ok {
		return nil
	}
	seconds, err := parseCooldownSeconds(c.Args())
	if err != nil {
		return c.Reply(err.Error(), telebot.ModeHTML)
	}
	if err := h.repo.SetChatCooldown(msg.Chat.ID, seconds, msg.Sender.ID); err != nil {
		return h.commandError(c, "没能设置提醒间隔", err)
	}
	if seconds == 0 {
		return c.Reply("提醒间隔已取消。到了该提醒的时候会马上发。")
	}
	return c.Reply(fmt.Sprintf("两次提醒至少间隔 %d 秒。", seconds))
}

func (h *CommandHandler) handleMention(c telebot.Context) error {
	msg, ok := h.authorize(c)
	if !ok {
		return nil
	}
	enabled, err := parseMentionOption(c.Args())
	if err != nil {
		return c.Reply(err.Error(), telebot.ModeHTML)
	}
	if err := h.repo.SetChatMention(msg.Chat.ID, enabled, msg.Sender.ID); err != nil {
		return h.commandError(c, "没能设置 @ 提醒", err)
	}
	if enabled {
		return c.Reply("提醒里会 @ 重复发送的人。")
	}
	return c.Reply("提醒里不再 @ 人。")
}

// authorize 校验群聊与管理员身份。失败时已回复用户，返回 ok=false。
func (h *CommandHandler) authorize(c telebot.Context) (*telebot.Message, bool) {
	msg := c.Message()
	if msg == nil || msg.Chat == nil {
		return nil, false
	}
	if msg.Chat.Type == telebot.ChatPrivate {
		_ = c.Reply("这个命令要在群里用。")
		return nil, false
	}
	if msg.Sender == nil {
		_ = c.Reply("没认出是谁发的，这条先不处理。")
		return nil, false
	}

	ok, err := h.admins.IsAdmin(msg.Chat, msg.Sender)
	if err != nil {
		slog.Error("管理员校验失败",
			"trace_id", logger.TraceIDFromContext(c),
			"chat_id", msg.Chat.ID,
			"user_id", msg.Sender.ID,
			"error", err.Error(),
		)
		_ = c.Reply("暂时没能确认你是不是管理员，请稍后再试。")
		return nil, false
	}
	if !ok {
		_ = c.Reply("只有群主或管理员能用这个命令。")
		return nil, false
	}
	return msg, true
}

func (h *CommandHandler) commandError(c telebot.Context, action string, err error) error {
	slog.Error(action,
		"trace_id", logger.TraceIDFromContext(c),
		"error", err.Error(),
	)
	return c.Reply(action + "，请稍后重试。")
}

func formatStatus(settings *models.ChatSettings, ignoredCount int64) string {
	state := "开着"
	if !settings.Enabled {
		state = "关着"
	}
	mention := "会"
	if !settings.MentionEnabled {
		mention = "不会"
	}
	cooldown := fmt.Sprintf("%d 秒", settings.CooldownSeconds)
	if settings.CooldownSeconds == 0 {
		cooldown = "0 秒（不间隔）"
	}
	var sb strings.Builder
	sb.WriteString("<b>本群设置</b>\n")
	sb.WriteString(fmt.Sprintf("检测：%s\n", state))
	sb.WriteString(fmt.Sprintf("提醒间隔：%s\n", cooldown))
	sb.WriteString(fmt.Sprintf("提醒里 @ 人：%s\n", mention))
	sb.WriteString(fmt.Sprintf("忽略名单：%d 人", ignoredCount))
	return sb.String()
}

func formatIgnoreList(ignored []models.ChatIgnoredUser) string {
	if len(ignored) == 0 {
		return "忽略名单是空的。"
	}

	shown := ignored
	if len(shown) > ignoreListLimit {
		shown = shown[:ignoreListLimit]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<b>忽略名单</b>（%d 人）\n\n", len(ignored)))
	for i, item := range shown {
		sb.WriteString(fmt.Sprintf("%d. <code>%d</code>\n", i+1, item.UserID))
	}
	if len(ignored) > len(shown) {
		sb.WriteString(fmt.Sprintf("只列出前 %d 人。\n", ignoreListLimit))
	}
	return sb.String()
}
