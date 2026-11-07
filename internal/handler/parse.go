package handler

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"gopkg.in/telebot.v4"
)

const (
	// MaxCooldownSeconds 限制管理员可配置的最大冷却时间（24 小时）。
	MaxCooldownSeconds = 24 * 60 * 60
	// ignoreListLimit 限制 /mars_ignore_list 单次展示的人数。
	ignoreListLimit = 100
)

var (
	errCooldownUsage = errors.New("请提供秒数，例如 /mars_cooldown 30。范围为 0–86400，0 表示不间隔。")
	errCooldownRange = errors.New("秒数须在 0 到 86400 之间。")
	errMentionUsage  = errors.New("请使用 /mars_mention on 或 /mars_mention off。")
	errIgnoreUsage   = errors.New("请回复目标用户的消息，或在命令后填写数字 ID。")
	errRankUsage     = errors.New("请使用 /mars_rank、/mars_rank week、/mars_rank month 或 /mars_rank all。")
)

type rankPeriod string

const (
	rankPeriodWeek  rankPeriod = "week"
	rankPeriodMonth rankPeriod = "month"
	rankPeriodAll   rankPeriod = "all"
)

func (p rankPeriod) label() string {
	switch p {
	case rankPeriodMonth:
		return "本月"
	case rankPeriodAll:
		return "总榜"
	default:
		return "本周"
	}
}

// parseRankPeriod 解析 /mars_rank 的可选周期参数，缺省为本周。
func parseRankPeriod(args []string) (rankPeriod, error) {
	if len(args) == 0 {
		return rankPeriodWeek, nil
	}
	if len(args) != 1 {
		return "", errRankUsage
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case string(rankPeriodWeek):
		return rankPeriodWeek, nil
	case string(rankPeriodMonth):
		return rankPeriodMonth, nil
	case string(rankPeriodAll):
		return rankPeriodAll, nil
	default:
		return "", errRankUsage
	}
}

// parseCooldownSeconds 解析 /mars_cooldown 的单个参数。
func parseCooldownSeconds(args []string) (int, error) {
	if len(args) != 1 {
		return 0, errCooldownUsage
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(args[0]))
	if err != nil || seconds < 0 || seconds > MaxCooldownSeconds {
		return 0, errCooldownRange
	}
	return seconds, nil
}

// parseMentionOption 解析 /mars_mention on|off，大小写不敏感。
func parseMentionOption(args []string) (bool, error) {
	if len(args) != 1 {
		return false, errMentionUsage
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "on":
		return true, nil
	case "off":
		return false, nil
	default:
		return false, errMentionUsage
	}
}

// parseIgnoreTarget 优先使用被回复消息的发送者，否则解析数字 user id。
func parseIgnoreTarget(args []string, replyTo *telebot.Message) (int64, error) {
	if replyTo != nil && replyTo.Sender != nil {
		return replyTo.Sender.ID, nil
	}
	if len(args) != 1 {
		return 0, errIgnoreUsage
	}
	userID, err := strconv.ParseInt(strings.TrimSpace(args[0]), 10, 64)
	if err != nil || userID <= 0 {
		return 0, errIgnoreUsage
	}
	return userID, nil
}

// weekStart 返回 now 所在周（周一 00:00）的起点。
func weekStart(now time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	offset := (int(local.Weekday()) + 6) % 7 // Monday=0 ... Sunday=6
	return midnight.AddDate(0, 0, -offset)
}

// monthStart 返回 now 所在自然月（1 日 00:00）的起点。
func monthStart(now time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
}
