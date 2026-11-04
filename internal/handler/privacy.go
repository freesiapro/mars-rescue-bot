package handler

import (
	"fmt"
	"log/slog"

	"gopkg.in/telebot.v3"
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
	return fmt.Sprintf(`🔒 <b>隐私与数据说明</b>

<b>保存的内容</b>
• 文本内容键：归一化文本的 HMAC-SHA256，不保存原文
• 媒体标识：Telegram <code>file_unique_id</code>，不下载文件内容
• Telegram ID：群 ID、用户 ID、消息 ID；频道转发含原频道与原消息 ID
• 排行榜：用户 ID 与出现次数，不保存用户名或姓名
• 群设置、忽略名单与时间戳

<b>不保存</b>
• 消息原文、媒体文件内容或下载链接
• 用户名、名字等姓名快照

<b>姓名展示</b>
姓名只在进程内缓存，展示时通过 <code>getChatMember</code> 查询；查询失败显示“用户 &lt;id&gt;”。

<b>保留期</b>
默认保留 %d 天（环境变量 <code>DATA_RETENTION_DAYS</code>）。启动时和每 24 小时分批删除过期流水与记录；排行榜“累计”只统计保留期内的流水。`, retentionDays)
}
