package bot

import (
	"strings"

	"gopkg.in/telebot.v4"
)

// richMessageRouteText 是富文本消息进入 telebot OnText 的占位正文。
//
// telebot v4.0.0-beta.10 能解析 Message.RichMessage，但 ProcessUpdate 只按
// Text、媒体和转发等字段分派；直接富文本这些字段都为空，转发富文本也只会
// 命中 OnForward。这里在 telebot 自带的 MiddlewarePoller 里补一个空白
// Text，让两类富文本都走 OnText；正文提取仍优先 RichMessage，空白占位
// 不会成为内容键、命令或日志里的正文。
const richMessageRouteText = " "

// NewRichMessagePoller 用 telebot 自带的 MiddlewarePoller 包装原始 Poller，
// 不新增收消息链路。Filter 只修改 Message，不改变其它更新类型。
func NewRichMessagePoller(original telebot.Poller) *telebot.MiddlewarePoller {
	return telebot.NewMiddlewarePoller(original, NormalizeRichMessageUpdate)
}

// NormalizeRichMessageUpdate 是 MiddlewarePoller 的 Filter。
//
// 只处理 RichMessage 非空、且 Text / Caption / 媒体都为空的 Message，
// 直接富文本和转发富文本都包含在内。已有正文或媒体的消息保持原样，
// 避免改变普通消息的既有分派行为。返回 true 表示更新继续交给 telebot。
func NormalizeRichMessageUpdate(update *telebot.Update) bool {
	if update == nil {
		return true
	}
	msg := update.Message
	if msg == nil || msg.RichMessage == nil {
		return true
	}
	if strings.TrimSpace(msg.Text) != "" || strings.TrimSpace(msg.Caption) != "" || msg.Media() != nil {
		return true
	}
	msg.Text = richMessageRouteText
	return true
}
