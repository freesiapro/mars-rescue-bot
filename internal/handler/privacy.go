package handler

import (
	"fmt"
	"log/slog"

	"gopkg.in/telebot.v4"
	"mars-rescue-bot/internal/logger"
)

func (h *CommandHandler) handlePrivacy(c telebot.Context) error {
	msg := c.Message()
	if msg == nil || msg.Chat == nil {
		return nil
	}
	if msg.Chat.Type == telebot.ChatPrivate {
		return c.Reply("这个命令要在群里用。")
	}
	slog.Info("处理 /mars_privacy 命令",
		"trace_id", logger.TraceIDFromContext(c),
		"chat_id", msg.Chat.ID,
	)
	return c.Reply(formatPrivacy(h.retentionDays), telebot.ModeHTML)
}

func formatPrivacy(retentionDays int) string {
	return fmt.Sprintf(`<b>这个机器人存什么</b>

<b>会留下</b>
• 文字：只存 HMAC-SHA256 摘要，不存原文，也还原不回来
• 图片、视频、动图和文件：只存 Telegram 的 <code>file_unique_id</code>，不下载文件
• 群、人和消息的数字 ID；从频道转进来的，还会记下原频道和原消息
• 火星次数：谁把同样的内容又发了几次。不存用户名，也不存名字
• 本群开关、忽略名单和时间

<b>不会留下</b>
• 消息原文、文件内容和下载链接
• 用户名和名字

<b>名字怎么显示</b>
名字只留在这次运行的内存里，展示时再向 Telegram 查现在的名字。查不到就显示“群友 123”这样。

<b>保留多久</b>
默认 %d 天，可用环境变量 <code>DATA_RETENTION_DAYS</code> 调整。启动时清一次，之后每 24 小时清掉过期记录。榜单和 /mars_me 里的「累计」只算还在保留期内的重复次数。`, retentionDays)
}
