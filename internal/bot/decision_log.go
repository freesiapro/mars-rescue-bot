package bot

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"

	"gopkg.in/telebot.v4"
)

// 每条消息最终只产生一个 dedup decision 值。新增分支时必须同步更新 README。
const (
	DecisionAlerted              = "alerted"
	DecisionPairedSilent         = "paired_silent"
	DecisionAdjacentRepeatExempt = "adjacent_repeat_exempt"
	DecisionShortTextSkipped     = "short_text_skipped"
	DecisionRepeatedCharSkipped  = "repeated_char_skipped"
	DecisionMentionOnly          = "mention_only"
	DecisionBotIgnored           = "bot_ignored"
	// DecisionForwardFromBotIgnored 是保留值：当前没有“转发来源是 bot”这一独立分支。
	// 只有 msg.Sender.IsBot（发送者本身是 bot）会被忽略；人类转发 bot 内容时
	// 与其他非频道转发一样按内容键去重，因此该值当前不可达。
	DecisionForwardFromBotIgnored = "forward_from_bot_ignored"
	DecisionIgnoredUser           = "ignored_user"
	DecisionDisabledChat          = "disabled_chat"
	DecisionCooldownSuppressed    = "cooldown_suppressed"
	DecisionSendFailedRolledBack  = "send_failed_rolled_back"
	DecisionNoContent             = "no_content"
	DecisionPrivateChat           = "private_chat"
	DecisionCommand               = "command"
	DecisionUnsupportedType       = "unsupported_type"
	DecisionNoMessage             = "no_message"
	DecisionNoChat                = "no_chat"
	DecisionNoSender              = "no_sender"
	DecisionProcessingError       = "processing_error"
)

// decisionRecorder 收集当前消息的处理结果，并在处理结束时输出且只输出一条日志。
// key 只用于生成短哈希，不会以明文写入日志。
type decisionRecorder struct {
	logger   *slog.Logger
	secret   string
	msg      *telebot.Message
	decision string
	key      string
	decided  bool
	logged   bool
}

func newDecisionRecorder(logger *slog.Logger, secret string, msg *telebot.Message) *decisionRecorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &decisionRecorder{logger: logger, secret: secret, msg: msg}
}

func (d *decisionRecorder) set(decision string) {
	d.decision = decision
	d.decided = true
}

func (d *decisionRecorder) setKey(key string) {
	d.key = key
}

func (d *decisionRecorder) log() {
	if d.logged {
		return
	}
	if !d.decided {
		// 兜底：某个新增分支忘记设置决策时，不能静默丢掉这条消息的日志。
		d.set(DecisionProcessingError)
	}
	d.logged = true

	var chatID int64
	var messageID int
	var userID int64
	if d.msg != nil {
		messageID = d.msg.ID
		if d.msg.Chat != nil {
			chatID = d.msg.Chat.ID
		}
		if d.msg.Sender != nil {
			userID = d.msg.Sender.ID
		}
	}

	attrs := []any{
		"chat_id", chatID,
		"message_id", messageID,
		"msg_type", messageType(d.msg),
		"decision", d.decision,
		"user_id", userID,
	}
	if d.key != "" {
		attrs = append(attrs, "key_hash", contentKeyHash(d.secret, d.key))
	}
	// no_content / unsupported_type 说明消息字段没能产生可检测内容。
	// 记录非空的顶层字段名和富文本块类型（不含任何字段值、正文），
	// 便于从线上日志判断是字段缺失、telebot 未解析，还是确实没有内容。
	if d.decision == DecisionNoContent || d.decision == DecisionUnsupportedType {
		fields := messagePresentFields(d.msg)
		if fields == nil {
			fields = []string{}
		}
		attrs = append(attrs, "present_fields", fields)
		if types := messageRichBlockTypes(d.msg); len(types) > 0 {
			attrs = append(attrs, "rich_block_types", types)
		}
	}
	d.logger.Info("dedup decision", attrs...)
}

// LogCommandDecision 为不经过 Processor 的已注册命令补上同一条决策日志。
// telebot 会把已注册命令直接交给命令处理器，不再经过 OnText；/hyw 用它保证
// 命令消息仍然只有一条 DecisionCommand，且不写入任何去重数据。
func LogCommandDecision(msg *telebot.Message) {
	rec := newDecisionRecorder(slog.Default(), "", msg)
	rec.set(DecisionCommand)
	rec.log()
}

// messageType 返回日志里的消息类型。频道转发优先于原始内容类型；
// 其余按实际载荷判断，不读取正文。
func messageType(msg *telebot.Message) string {
	if msg == nil {
		return "unknown"
	}
	if isChannelForward(msg) {
		return "channel_forward"
	}
	// 纯链接优先于 rich_message：这类消息实际走链接键，msg_type 应与
	// 内容键类型一致。含其它文字的富文本仍记为 rich_message。
	if isPureLinkMessage(msg) {
		return "link"
	}
	if msg.RichMessage != nil {
		return "rich_message"
	}
	switch {
	case msg.Photo != nil:
		return "photo"
	case msg.Video != nil:
		return "video"
	case msg.Animation != nil:
		return "animation"
	case msg.Document != nil:
		return "document"
	case msg.Audio != nil:
		return "audio"
	case msg.Sticker != nil:
		return "sticker"
	case msg.Voice != nil:
		return "voice"
	case msg.Location != nil:
		return "location"
	case msg.Contact != nil:
		return "contact"
	case msg.Text != "":
		return "text"
	case msg.Caption != "":
		return "caption"
	default:
		return "unknown"
	}
}

// contentKeyHash 生成可安全写入日志的键指纹。
// 文本键和链接键本身就是 HMAC-SHA256，直接取十六进制结果前 8 位；
// 媒体/频道键包含 file_unique_id 或原频道 ID，不能明文输出，因此用
// DEDUP_SECRET 再做一次 HMAC-SHA256，同样只取前 8 位。没有内容键时返回空。
func contentKeyHash(secret, key string) string {
	if key == "" {
		return ""
	}
	if strings.HasPrefix(key, textContentKeyPrefix) {
		sum := strings.TrimPrefix(key, textContentKeyPrefix)
		if len(sum) >= 8 {
			return sum[:8]
		}
	}
	if strings.HasPrefix(key, linkContentKeyPrefix) {
		sum := strings.TrimPrefix(key, linkContentKeyPrefix)
		if len(sum) >= 8 {
			return sum[:8]
		}
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(key))
	return hex.EncodeToString(mac.Sum(nil))[:8]
}

// fallbackDecision 处理没有命中任何检测分支的消息。
func fallbackDecision(msg *telebot.Message) string {
	if msg == nil {
		return DecisionNoMessage
	}
	if messageHasContent(msg) {
		return DecisionUnsupportedType
	}
	return DecisionNoContent
}

func messageHasContent(msg *telebot.Message) bool {
	if msg == nil {
		return false
	}
	return strings.TrimSpace(msg.Text) != "" || strings.TrimSpace(msg.Caption) != "" || msg.RichMessage != nil ||
		msg.Photo != nil || msg.Video != nil || msg.Animation != nil || msg.Document != nil ||
		msg.Audio != nil || msg.Sticker != nil || msg.Voice != nil ||
		msg.Location != nil || msg.Contact != nil
}
