package bot

import (
	"strings"
	"unicode"
	"unicode/utf16"

	"gopkg.in/telebot.v4"
)

// messageHasNonTextPayload 报告消息是否还带有文本之外的载荷（媒体、位置、
// 联系人、投票等）。这类消息不是“纯 @”，mention-only 规则必须让位给原有的
// 频道/媒体处理。富文本媒体块也算载荷，避免“提及 + 图片块”被当成纯 @。
func messageHasNonTextPayload(msg *telebot.Message) bool {
	if msg == nil {
		return false
	}
	if msg.Media() != nil {
		return true
	}
	if msg.Location != nil || msg.Contact != nil || msg.Venue != nil ||
		msg.Poll != nil || msg.Dice != nil || msg.Game != nil {
		return true
	}
	if msg.PaidMedia.Stars > 0 || len(msg.PaidMedia.PaidMedia) > 0 {
		return true
	}
	return msg.RichMessage != nil && richMessageHasMedia(msg.RichMessage)
}

func richMessageHasMedia(rich *telebot.RichMessage) bool {
	if rich == nil {
		return false
	}
	return richBlocksHaveMedia(rich.Blocks)
}

func richBlocksHaveMedia(blocks []telebot.RichBlock) bool {
	for i := range blocks {
		block := &blocks[i]
		switch block.Type {
		case telebot.RichBlockAnimationType:
			if block.Animation != nil {
				return true
			}
		case telebot.RichBlockAudioType:
			if block.Audio != nil {
				return true
			}
		case telebot.RichBlockPhotoType:
			if len(block.Photo) > 0 {
				return true
			}
		case telebot.RichBlockVideoType:
			if block.Video != nil {
				return true
			}
		case telebot.RichBlockVoiceNote:
			if block.VoiceNote != nil {
				return true
			}
		case telebot.RichBlockMap:
			if block.Location != nil {
				return true
			}
		}
		if richBlocksHaveMedia(block.Blocks) {
			return true
		}
		for _, item := range block.Items {
			if richBlocksHaveMedia(item.Blocks) {
				return true
			}
		}
	}
	return false
}

// messageMentionOnly 报告消息是否“只有 @ 提及”：去掉 mention / text_mention
// 实体以及文本中形如 @username 的片段后，只剩下空白、标点或符号。
//
// 富文本优先看 RichMessage 块树；普通消息看 Text。只有至少识别到一个提及时
// 才返回 true，避免把“只有标点”的普通消息误判成 mention_only。
func messageMentionOnly(msg *telebot.Message) bool {
	if msg == nil {
		return false
	}
	if msg.RichMessage != nil {
		return richMessageMentionOnly(msg.RichMessage)
	}
	return plainTextMentionOnly(msg.Text, msg.Entities)
}

// plainTextMentionOnly 处理普通文本消息。实体 Offset/Length 以 UTF-16 码元
// 计数，必须先按 UTF-16 掩码剔除提及，再处理没有实体覆盖的 @username 片段，
// 否则 emoji、多字节字符后面的偏移会错位。
func plainTextMentionOnly(text string, entities telebot.Entities) bool {
	if text == "" {
		return false
	}
	remaining, entityFound := stripMentionEntities(text, entities)
	remaining, textFound := stripAtUsernames(remaining)
	if !entityFound && !textFound {
		return false
	}
	return !hasMeaningfulContent(remaining)
}

// stripMentionEntities 按 UTF-16 范围剔除 mention 与 text_mention 实体。
// 越界实体按文本长度截断；只覆盖半个代理对的异常范围不会吞掉整个字符。
func stripMentionEntities(text string, entities telebot.Entities) (string, bool) {
	if len(entities) == 0 || text == "" {
		return text, false
	}
	units := utf16.Encode([]rune(text))
	mask := make([]bool, len(units))
	found := false
	for _, entity := range entities {
		if entity.Type != telebot.EntityMention && entity.Type != telebot.EntityTMention {
			continue
		}
		start := entity.Offset
		end := entity.Offset + entity.Length
		if start < 0 {
			start = 0
		}
		if end > len(mask) {
			end = len(mask)
		}
		if start >= end {
			continue
		}
		found = true
		for i := start; i < end; i++ {
			mask[i] = true
		}
	}
	if !found {
		return text, false
	}

	var b strings.Builder
	b.Grow(len(text))
	unitIndex := 0
	for _, r := range text {
		size := 1
		if r > 0xFFFF {
			size = 2
		}
		covered := true
		for i := unitIndex; i < unitIndex+size && i < len(mask); i++ {
			if !mask[i] {
				covered = false
				break
			}
		}
		if !covered {
			b.WriteRune(r)
		}
		unitIndex += size
	}
	return b.String(), true
}

// stripAtUsernames 去掉文本中形如 @username 的片段。@ 前必须是文本开头或
// 非用户名字符，避免把 foo@bar 这类邮箱样式误当成提及。
func stripAtUsernames(text string) (string, bool) {
	if !strings.ContainsRune(text, '@') {
		return text, false
	}
	runes := []rune(text)
	var b strings.Builder
	b.Grow(len(text))
	found := false
	for i := 0; i < len(runes); {
		if runes[i] == '@' && (i == 0 || !isUsernameRune(runes[i-1])) {
			j := i + 1
			for j < len(runes) && isUsernameRune(runes[j]) {
				j++
			}
			if j > i+1 {
				found = true
				i = j
				continue
			}
		}
		b.WriteRune(runes[i])
		i++
	}
	return b.String(), found
}

func isUsernameRune(r rune) bool {
	return r == '_' ||
		(r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}

// hasMeaningfulContent 判断剩余文本里是否还有实际内容。
// 空白、标点、符号（包括 emoji）都不算内容，字母和数字才算。
func hasMeaningfulContent(text string) bool {
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// richMessageMentionOnly 遍历富文本块树，规则与普通消息一致：
// 跳过 mention / text_mention 节点，纯文本片段里的 @username 也去掉，
// 最后看是否还有字母或数字。遍历范围与 richBlockPlainText 保持一致，
// 避免块级 caption、表格单元格等位置的文字被漏掉。
func richMessageMentionOnly(rich *telebot.RichMessage) bool {
	if rich == nil {
		return false
	}
	scan := &mentionScan{}
	scanRichBlocks(rich.Blocks, scan)
	return scan.mention && !scan.content
}

type mentionScan struct {
	mention bool
	content bool
}

func (s *mentionScan) addPlain(text string) {
	if s.content || text == "" {
		return
	}
	remaining, found := stripAtUsernames(text)
	if found {
		s.mention = true
	}
	if hasMeaningfulContent(remaining) {
		s.content = true
	}
}

func (s *mentionScan) addRichText(text *telebot.RichText) {
	if s.content || text == nil {
		return
	}
	switch text.Kind {
	case telebot.RichTextPlain:
		s.addPlain(text.Plain)
	case telebot.RichTextArray:
		for i := range text.Parts {
			s.addRichText(&text.Parts[i])
		}
	default: // RichTextEntity
		switch text.Type {
		case telebot.RichMention, telebot.RichTextMentionType:
			s.mention = true
		case telebot.RichCustomEmoji:
			s.addPlain(text.AlternativeText)
		case telebot.RichMathematicalExpression:
			s.addPlain(text.Expression)
		case telebot.RichAnchor:
			// 锚点没有可见文本。
		default:
			s.addRichText(text.Text)
		}
	}
}

func scanRichBlocks(blocks []telebot.RichBlock, scan *mentionScan) {
	for i := range blocks {
		scanRichBlock(&blocks[i], scan)
		if scan.content {
			return
		}
	}
}

func scanRichBlock(block *telebot.RichBlock, scan *mentionScan) {
	if scan.content {
		return
	}
	switch block.Type {
	case telebot.RichBlockDivider, telebot.RichBlockAnchor:
		return
	case telebot.RichBlockMath:
		scan.addPlain(block.Expression)
		return
	case telebot.RichBlockList:
		for _, item := range block.Items {
			scanRichBlocks(item.Blocks, scan)
		}
		return
	case telebot.RichBlockTable:
		for _, row := range block.Cells {
			for _, cell := range row {
				scan.addRichText(cell.Text)
			}
		}
		scan.addRichText(block.TableCaption)
		return
	}

	scan.addRichText(block.Text)
	scan.addRichText(block.Summary)
	scanRichBlocks(block.Blocks, scan)
	if block.Caption != nil {
		scan.addRichText(&block.Caption.Text)
		scan.addRichText(block.Caption.Credit)
	}
	scan.addRichText(block.Credit)
}
