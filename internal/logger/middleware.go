package logger

import (
	"log/slog"

	"gopkg.in/telebot.v4"
)

const traceContextKey = "trace_id"

func TraceMiddleware(next telebot.HandlerFunc) telebot.HandlerFunc {
	return func(c telebot.Context) error {
		traceID := NewTraceID()
		c.Set(traceContextKey, traceID)

		msg := c.Message()
		if msg != nil {
			LogMessageReceived(traceID, msg)
		} else {
			// Message() 为 nil 的分支（如 inline query、无关联 message 的 callback 等）
			logUpdateReceived(traceID, c)
		}

		err := next(c)

		if err != nil {
			slog.Error("处理消息出错",
				"trace_id", traceID,
				"error", err.Error(),
			)
		}

		return err
	}
}

// LogMessageReceived 输出与 TraceMiddleware 相同的“收到消息”日志。
// 富文本消息经 MiddlewarePoller 归一化后同样走 TraceMiddleware，
// 不再需要单独调用。
func LogMessageReceived(traceID string, msg *telebot.Message) {
	chatID := int64(0)
	chatType := ""
	if msg.Chat != nil {
		chatID = msg.Chat.ID
		chatType = string(msg.Chat.Type)
	}

	userID := int64(0)
	if msg.Sender != nil {
		userID = msg.Sender.ID
	}

	// 判断消息类型，丰富日志信息。
	// 新版 forward_origin（Message.Origin）不一定填旧版 forward_from 字段，
	// 因此 IsForwarded() 之外还要看 Origin，否则隐藏来源等转发会被记成普通消息。
	forwarded := msg.IsForwarded() || msg.Origin != nil
	msgType := "text"
	switch {
	case msg.RichMessage != nil:
		// 富文本消息也可能是转发；类型上仍优先标成 rich_message，
		// forward 字段会单独说明转发来源。
		msgType = "rich_message"
	case forwarded:
		msgType = "forward"
	case msg.Photo != nil:
		msgType = "photo"
	case msg.Video != nil:
		msgType = "video"
	case msg.Audio != nil:
		msgType = "audio"
	case msg.Document != nil:
		msgType = "document"
	case msg.Sticker != nil:
		msgType = "sticker"
	case msg.Location != nil:
		msgType = "location"
	case msg.Voice != nil:
		msgType = "voice"
	case msg.Contact != nil:
		msgType = "contact"
	case msg.Text == "" && msg.Caption != "":
		msgType = "caption"
	}

	forwardInfo := ""
	if msg.IsForwarded() && msg.OriginalChat != nil {
		forwardInfo = string(msg.OriginalChat.Type)
	}

	// 新版 forward_origin 的细分来源：user / chat / channel / hidden_user。
	// 旧版字段没有 Origin 时留空，仍由上面的 forward 字段说明来源类型。
	forwardOrigin := ""
	originUserID := int64(0)
	originDate := int64(0)
	hasSenderName := msg.OriginalSenderName != ""
	if msg.Origin != nil {
		forwardOrigin = msg.Origin.Type
		if msg.Origin.Sender != nil && msg.Origin.Sender.ID > 0 {
			originUserID = msg.Origin.Sender.ID
		}
		originDate = msg.Origin.DateUnixtime
		if msg.Origin.SenderUsername != "" {
			hasSenderName = true
		}
	}

	attrs := []any{
		"trace_id", traceID,
		"chat_id", chatID,
		"chat_type", chatType,
		"user_id", userID,
		"message_id", msg.ID,
		"msg_type", msgType,
		"forward", forwardInfo,
		"forward_origin", forwardOrigin,
		"has_sender_name", hasSenderName,
		"is_edited", msg.LastEdit != 0,
	}
	if originUserID > 0 {
		attrs = append(attrs, "origin_user_id", originUserID)
	}
	if originDate > 0 {
		attrs = append(attrs, "origin_date", originDate)
	}
	slog.Info("收到消息", attrs...)
}

func logUpdateReceived(traceID string, c telebot.Context) {
	update := c.Update()
	updateType := "unknown"
	switch {
	case update.Callback != nil:
		updateType = "callback_query"
	case update.Query != nil:
		updateType = "inline_query"
	case update.ShippingQuery != nil:
		updateType = "shipping_query"
	case update.PreCheckoutQuery != nil:
		updateType = "pre_checkout_query"
	case update.Poll != nil:
		updateType = "poll"
	case update.PollAnswer != nil:
		updateType = "poll_answer"
	case update.MyChatMember != nil:
		updateType = "my_chat_member"
	case update.ChatMember != nil:
		updateType = "chat_member"
	case update.ChatJoinRequest != nil:
		updateType = "chat_join_request"
	case update.Boost != nil:
		updateType = "chat_boost"
	case update.BoostRemoved != nil:
		updateType = "boost_removed"
	}

	slog.Info("收到更新",
		"trace_id", traceID,
		"update_id", update.ID,
		"update_type", updateType,
	)
}

func TraceIDFromContext(c telebot.Context) string {
	if v, ok := c.Get(traceContextKey).(string); ok {
		return v
	}
	return ""
}
