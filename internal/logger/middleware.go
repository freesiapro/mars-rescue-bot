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
			logMessageReceived(traceID, msg)
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

func logMessageReceived(traceID string, msg *telebot.Message) {
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

	// 判断消息类型，丰富日志信息
	msgType := "text"
	switch {
	case msg.IsForwarded():
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

	slog.Info("收到消息",
		"trace_id", traceID,
		"chat_id", chatID,
		"chat_type", chatType,
		"user_id", userID,
		"message_id", msg.ID,
		"msg_type", msgType,
		"forward", forwardInfo,
		"is_edited", msg.LastEdit != 0,
	)
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
