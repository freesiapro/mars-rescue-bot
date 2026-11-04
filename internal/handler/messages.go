package handler

import (
	"gopkg.in/telebot.v3"
	"mars-rescue-bot/internal/bot"
)

type MessageHandler struct {
	processor *bot.Processor
}

func NewMessageHandler(processor *bot.Processor) *MessageHandler {
	return &MessageHandler{processor: processor}
}

func (h *MessageHandler) Register(teleBot *telebot.Bot) {
	teleBot.Handle(telebot.OnText, h.processor.Handle)
	teleBot.Handle(telebot.OnMedia, h.processor.Handle)
}
