package bot

import (
	"strings"

	"gopkg.in/telebot.v4"
)

// messagePlainText 返回消息去掉格式后的文本内容。
// 普通格式化消息的正文仍在 Text 里，entities 只标记 UTF-16 范围；
// Bot API 10.1 富文本消息则把内容放在 RichMessage 的块树里。
func messagePlainText(msg *telebot.Message) string {
	if msg == nil {
		return ""
	}
	text := strings.TrimSpace(msg.Text)
	if msg.RichMessage != nil {
		if rich := richMessagePlainText(msg.RichMessage); rich != "" {
			text = rich
		}
	}
	return text
}

func richMessagePlainText(rich *telebot.RichMessage) string {
	if rich == nil {
		return ""
	}
	return strings.TrimSpace(richBlocksPlainText(rich.Blocks))
}

func richBlocksPlainText(blocks []telebot.RichBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for i := range blocks {
		if text := strings.TrimSpace(richBlockPlainText(blocks[i])); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// richBlockPlainText 只提取块里的可见文本，丢弃标题层级、列表符号、链接目标、
// 锚点等格式信息。块之间用换行连接，归一化时会折叠成空格，因此同一内容的
// 富文本和纯文本会得到相同的去重键。
func richBlockPlainText(block telebot.RichBlock) string {
	switch block.Type {
	case telebot.RichBlockDivider, telebot.RichBlockAnchor:
		return ""
	case telebot.RichBlockMath:
		return block.Expression
	case telebot.RichBlockList:
		parts := make([]string, 0, len(block.Items))
		for _, item := range block.Items {
			if text := richBlocksPlainText(item.Blocks); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case telebot.RichBlockTable:
		var parts []string
		for _, row := range block.Cells {
			cells := make([]string, 0, len(row))
			for _, cell := range row {
				if cell.Text == nil {
					continue
				}
				if text := strings.TrimSpace(cell.Text.String()); text != "" {
					cells = append(cells, text)
				}
			}
			if len(cells) > 0 {
				parts = append(parts, strings.Join(cells, " "))
			}
		}
		if block.TableCaption != nil {
			if text := strings.TrimSpace(block.TableCaption.String()); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	}

	var parts []string
	appendPart := func(text string) {
		if text = strings.TrimSpace(text); text != "" {
			parts = append(parts, text)
		}
	}
	if block.Text != nil {
		appendPart(block.Text.String())
	}
	if block.Summary != nil {
		appendPart(block.Summary.String())
	}
	if nested := richBlocksPlainText(block.Blocks); nested != "" {
		appendPart(nested)
	}
	if block.Caption != nil {
		appendPart(block.Caption.Text.String())
		if block.Caption.Credit != nil {
			appendPart(block.Caption.Credit.String())
		}
	}
	if block.Credit != nil {
		appendPart(block.Credit.String())
	}
	return strings.Join(parts, "\n")
}
