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
		return c.Reply("请在群聊中使用此命令。")
	}
	slog.Info("处理 /mars_privacy 命令",
		"trace_id", logger.TraceIDFromContext(c),
		"chat_id", msg.Chat.ID,
	)
	return c.Reply(formatPrivacy(h.retentionDays), telebot.ModeHTML)
}

func formatPrivacy(retentionDays int) string {
	return fmt.Sprintf(`🔒 <b>隐私与数据</b>

<b>会保存</b>
• 文字：归一化后的 HMAC-SHA256，不保存原文
• 图片、视频、动图、文件：Telegram 的 <code>file_unique_id</code>，不下载文件
• 群、用户、消息的数字 ID；频道转发还包括原频道与原消息 ID
• 火星次数：用户 ID 与重复出现次数，不保存用户名或姓名
• 群设置、忽略名单与时间

<b>不保存</b>
• 消息原文、文件内容或下载链接
• 用户名、姓名

<b>姓名显示</b>
姓名只保留在本次运行的内存中，展示时向 Telegram 查询。查询失败显示“用户 &lt;id&gt;”。

<b>保留期</b>
默认 %d 天，由环境变量 <code>DATA_RETENTION_DAYS</code> 设置。启动时清理一次，之后每 24 小时清理过期记录。榜单和 /mars_me 的「累计」只统计保留期内的重复次数。`, retentionDays)
}
