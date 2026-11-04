package handler

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gopkg.in/telebot.v3"
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
}

func NewCommandHandler(r *repo.Repo, admins *AdminChecker, resolver *names.Resolver) *CommandHandler {
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
	}
}

func (h *CommandHandler) Register(bot *telebot.Bot) {
	bot.Handle("/start", h.handleStart)
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
		"🚀 火星救援 Bot 已上线！\n\n" +
			"我会默默观察群聊中的重复内容。\n" +
			"同一内容按出现顺序两两配对（文本忽略标点、大小写）：再次出现时提醒，并和上一次抵消；下一次出现重新开一对。\n" +
			"配对只影响提醒，排行榜每次出现都照常计数。\n\n" +
			"命令:\n" +
			"/mars_rank [week|month|all] - 查看本周/本月/总火星指数榜\n" +
			"/mars_me - 查看自己的火星指数\n" +
			"/mars_privacy - 查看隐私与数据说明\n" +
			"/mars_status - 查看本群设置（管理员）\n" +
			"/mars_off - 关闭本群检测（管理员）\n" +
			"/mars_on - 开启本群检测（管理员）\n" +
			"/mars_cooldown [秒数] - 设置回复冷却（管理员）\n" +
			"/mars_mention on|off - 设置是否 @ 人（管理员）\n" +
			"/mars_ignore - 忽略某人的重复内容（管理员）\n" +
			"/mars_unignore - 取消忽略（管理员）\n" +
			"/mars_ignore_list - 查看忽略名单（管理员）",
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
		return c.Reply("请在群聊中使用此命令。")
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
		return c.Reply("获取排行榜失败，请稍后重试。")
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
		return c.Reply("请在群聊中使用此命令。")
	}

	slog.Info("处理 /mars_me 命令",
		"trace_id", traceID,
		"chat_id", msg.Chat.ID,
		"user_id", msg.Sender.ID,
	)

	now := h.now()
	weekCount, err := h.repo.CountUserOccurrences(msg.Chat.ID, msg.Sender.ID, weekStart(now, h.statsLoc))
	if err != nil {
		return h.commandError(c, "读取本周火星指数失败", err)
	}
	monthCount, err := h.repo.CountUserOccurrences(msg.Chat.ID, msg.Sender.ID, monthStart(now, h.statsLoc))
	if err != nil {
		return h.commandError(c, "读取本月火星指数失败", err)
	}
	totalCount, err := h.repo.CountUserOccurrences(msg.Chat.ID, msg.Sender.ID, time.Time{})
	if err != nil {
		return h.commandError(c, "读取累计火星指数失败", err)
	}
	last, _, err := h.repo.LastUserOccurrence(msg.Chat.ID, msg.Sender.ID)
	if err != nil {
		return h.commandError(c, "读取最近一次火星记录失败", err)
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
		return h.commandError(c, "关闭本群检测失败", err)
	}
	return c.Reply("已关闭本群的重复检测：不记录，也不回复。使用 /mars_on 重新开启。")
}

func (h *CommandHandler) handleOn(c telebot.Context) error {
	msg, ok := h.authorize(c)
	if !ok {
		return nil
	}
	if err := h.repo.SetChatEnabled(msg.Chat.ID, true, msg.Sender.ID); err != nil {
		return h.commandError(c, "开启本群检测失败", err)
	}
	return c.Reply("已开启本群的重复检测。")
}

func (h *CommandHandler) handleStatus(c telebot.Context) error {
	msg, ok := h.authorize(c)
	if !ok {
		return nil
	}
	settings, err := h.repo.GetChatSettings(msg.Chat.ID)
	if err != nil {
		return h.commandError(c, "读取本群设置失败", err)
	}
	ignoredCount, err := h.repo.CountIgnoredUsers(msg.Chat.ID)
	if err != nil {
		return h.commandError(c, "读取忽略名单失败", err)
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
		return h.commandError(c, "添加忽略名单失败", err)
	}
	if !added {
		return c.Reply(fmt.Sprintf("用户 %d 已经在忽略名单中。", target))
	}
	return c.Reply(fmt.Sprintf("已忽略用户 %d 的重复内容。", target))
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
		return h.commandError(c, "移除忽略名单失败", err)
	}
	if !removed {
		return c.Reply(fmt.Sprintf("用户 %d 不在忽略名单中。", target))
	}
	return c.Reply(fmt.Sprintf("已取消忽略用户 %d。", target))
}

func (h *CommandHandler) handleIgnoreList(c telebot.Context) error {
	msg, ok := h.authorize(c)
	if !ok {
		return nil
	}
	ignored, err := h.repo.ListIgnoredUsers(msg.Chat.ID)
	if err != nil {
		return h.commandError(c, "读取忽略名单失败", err)
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
		return h.commandError(c, "设置冷却失败", err)
	}
	if seconds == 0 {
		return c.Reply("已关闭回复冷却：每次配对提醒都会立即发送。")
	}
	return c.Reply(fmt.Sprintf("已将回复冷却设置为 %d 秒。", seconds))
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
		return h.commandError(c, "设置 @ 提醒失败", err)
	}
	if enabled {
		return c.Reply("已开启回复中的 @ 提醒。")
	}
	return c.Reply("已关闭回复中的 @ 提醒。")
}

// authorize 校验群聊与管理员身份。失败时已回复用户，返回 ok=false。
func (h *CommandHandler) authorize(c telebot.Context) (*telebot.Message, bool) {
	msg := c.Message()
	if msg == nil || msg.Chat == nil {
		return nil, false
	}
	if msg.Chat.Type == telebot.ChatPrivate {
		_ = c.Reply("请在群聊中使用此命令。")
		return nil, false
	}
	if msg.Sender == nil {
		_ = c.Reply("无法确认你的身份。")
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
		_ = c.Reply("无法确认管理员身份，请稍后重试。")
		return nil, false
	}
	if !ok {
		_ = c.Reply("仅群管理员可使用此命令。")
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
	state := "已开启"
	if !settings.Enabled {
		state = "已关闭"
	}
	mention := "开启"
	if !settings.MentionEnabled {
		mention = "关闭"
	}
	var sb strings.Builder
	sb.WriteString("⚙️ <b>火星救援设置</b>\n")
	sb.WriteString(fmt.Sprintf("状态：%s\n", state))
	sb.WriteString(fmt.Sprintf("回复冷却：%d 秒\n", settings.CooldownSeconds))
	sb.WriteString(fmt.Sprintf("@提醒：%s\n", mention))
	sb.WriteString(fmt.Sprintf("忽略名单：%d 人", ignoredCount))
	return sb.String()
}

func formatIgnoreList(ignored []models.ChatIgnoredUser) string {
	if len(ignored) == 0 {
		return "本群忽略名单为空。"
	}

	shown := ignored
	if len(shown) > ignoreListLimit {
		shown = shown[:ignoreListLimit]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🙈 <b>本群忽略名单</b>（%d 人）\n\n", len(ignored)))
	for i, item := range shown {
		sb.WriteString(fmt.Sprintf("%d. <code>%d</code>\n", i+1, item.UserID))
	}
	if len(ignored) > len(shown) {
		sb.WriteString(fmt.Sprintf("……仅展示前 %d 人\n", ignoreListLimit))
	}
	return sb.String()
}
