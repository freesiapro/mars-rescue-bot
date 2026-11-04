package handler

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"gopkg.in/telebot.v3"
)

const (
	// MaxCooldownSeconds 限制管理员可配置的最大冷却时间（24 小时）。
	MaxCooldownSeconds = 24 * 60 * 60
	// ignoreListLimit 限制 /mars_ignore_list 单次展示的人数。
	ignoreListLimit = 100
)

var (
	errCooldownUsage = errors.New("用法：/mars_cooldown [秒数]，范围 0-86400")
	errCooldownRange = errors.New("冷却秒数需在 0 到 86400 之间")
	errMentionUsage  = errors.New("用法：/mars_mention on|off")
	errIgnoreUsage   = errors.New("请回复某个用户的消息，或提供用户 ID：/mars_ignore [user_id]")
)

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
