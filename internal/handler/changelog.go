package handler

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"unicode/utf16"

	"gopkg.in/telebot.v4"
	"mars-rescue-bot/internal/logger"
)

const (
	changelogDefaultN = 1
	// MaxChangelogN 限制一次 /changelog 最多展示的更新条数。
	MaxChangelogN = 10

	// Telegram 单条文本上限为 4096 个 UTF-16 码元。这里留出余量，
	// 让分块、续接标记和以后的小改动都不会贴到硬上限。
	changelogChunkLimit = 4000

	changelogContinuation = "（续）\n"
)

var errChangelogUsage = errors.New("参数需为正整数，例如 /changelog 或 /changelog 3。")

// ChangelogEntry 是一条面向群友的更新记录。
type ChangelogEntry struct {
	Date    string
	Title   string
	Bullets []string
}

// parseChangelogN 解析 /changelog 的可选条数。缺省为 1，超过上限按上限
// 处理并通过 capped 告诉调用方需要在回复里说明。
func parseChangelogN(args []string) (n int, capped bool, err error) {
	if len(args) == 0 {
		return changelogDefaultN, false, nil
	}
	if len(args) != 1 {
		return 0, false, errChangelogUsage
	}
	value, err := strconv.Atoi(strings.TrimSpace(args[0]))
	if err != nil || value <= 0 {
		return 0, false, errChangelogUsage
	}
	if value > MaxChangelogN {
		return MaxChangelogN, true, nil
	}
	return value, false, nil
}

// utf16CodeUnits 返回 Telegram 计长度时使用的 UTF-16 码元数。
// 常用汉字等 BMP 字符算 1，emoji 等增补平面字符算 2。
func utf16CodeUnits(s string) int {
	total := 0
	for _, r := range s {
		n := utf16.RuneLen(r)
		if n < 0 {
			n = 1
		}
		total += n
	}
	return total
}

// splitUTF16 按 UTF-16 码元上限切分字符串，只在完整字符边界处切开。
func splitUTF16(s string, limit int) (head, tail string) {
	if limit <= 0 {
		return "", s
	}
	units := 0
	for i, r := range s {
		n := utf16.RuneLen(r)
		if n < 0 {
			n = 1
		}
		if units+n > limit {
			return s[:i], s[i:]
		}
		units += n
	}
	return s, ""
}

// formatChangelogChunks 把最近 n 条记录格式化成若干条纯文本消息。
// 优先按整条记录分块；单条记录本身超长时，再按行和字符切分。
func formatChangelogChunks(entries []ChangelogEntry, n int, capped bool) []string {
	if n < 1 {
		n = 1
	}
	if n > len(entries) {
		n = len(entries)
	}
	if n == 0 {
		return []string{"📣 还没有更新记录。"}
	}

	var prefix strings.Builder
	if capped {
		fmt.Fprintf(&prefix, "一次最多显示 %d 条，以下为最近的更新：\n\n", MaxChangelogN)
	}
	fmt.Fprintf(&prefix, "📣 最近 %d 次更新\n", n)

	builder := changelogChunkBuilder{
		limit:   changelogChunkLimit,
		current: prefix.String(),
	}
	for _, entry := range entries[:n] {
		var block strings.Builder
		fmt.Fprintf(&block, "\n%s · %s\n", entry.Date, entry.Title)
		for _, bullet := range entry.Bullets {
			fmt.Fprintf(&block, "• %s\n", bullet)
		}
		builder.appendBlock(block.String())
	}
	return builder.finish()
}

type changelogChunkBuilder struct {
	limit     int
	chunks    []string
	current   string
	hasRecord bool
}

func (b *changelogChunkBuilder) fits(s string) bool {
	return utf16CodeUnits(b.current)+utf16CodeUnits(s) <= b.limit
}

func (b *changelogChunkBuilder) flush() {
	if b.current == "" {
		return
	}
	b.chunks = append(b.chunks, b.current)
	b.current = ""
	b.hasRecord = false
}

func (b *changelogChunkBuilder) startContinuation() {
	b.current = changelogContinuation
	b.hasRecord = false
}

func (b *changelogChunkBuilder) appendBlock(block string) {
	if b.fits(block) {
		b.current += block
		b.hasRecord = true
		return
	}

	// 当前消息里已经有记录时，先按记录边界断开，剩余部分另起一条。
	if b.hasRecord {
		b.flush()
		b.startContinuation()
	}
	if b.fits(block) {
		b.current += block
		b.hasRecord = true
		return
	}

	for _, line := range strings.SplitAfter(block, "\n") {
		if line == "" {
			continue
		}
		b.appendLine(line)
	}
}

func (b *changelogChunkBuilder) appendLine(line string) {
	if b.fits(line) {
		b.current += line
		if strings.TrimSpace(line) != "" {
			b.hasRecord = true
		}
		return
	}

	// 当前消息已有记录时先换一条，避免把一条记录拆得太碎。
	if b.hasRecord {
		b.flush()
		b.startContinuation()
	}

	for line != "" {
		if b.fits(line) {
			b.current += line
			if strings.TrimSpace(line) != "" {
				b.hasRecord = true
			}
			return
		}

		remaining := b.limit - utf16CodeUnits(b.current)
		if remaining <= 0 {
			b.flush()
			b.startContinuation()
			continue
		}
		head, tail := splitUTF16(line, remaining)
		if head == "" {
			b.flush()
			b.startContinuation()
			continue
		}

		b.current += head
		if strings.TrimSpace(head) != "" {
			b.hasRecord = true
		}
		line = tail
		if line != "" {
			b.flush()
			b.startContinuation()
		}
	}
}

func (b *changelogChunkBuilder) finish() []string {
	b.flush()
	if len(b.chunks) > 0 {
		last := len(b.chunks) - 1
		b.chunks[last] = strings.TrimRight(b.chunks[last], "\n")
	}
	return b.chunks
}

func (h *CommandHandler) handleChangelog(c telebot.Context) error {
	traceID := logger.TraceIDFromContext(c)
	sender := c.Sender()
	if sender == nil {
		slog.Warn("skip /changelog without sender", "trace_id", traceID)
		return nil
	}

	n, capped, err := parseChangelogN(c.Args())
	if err != nil {
		return c.Reply(err.Error())
	}

	chunks := formatChangelogChunks(changelogEntries, n, capped)
	slog.Info("处理 /changelog 命令",
		"trace_id", traceID,
		"user_id", sender.ID,
		"count", n,
		"capped", capped,
		"messages", len(chunks),
	)
	for i, chunk := range chunks {
		if i == 0 {
			err = c.Reply(chunk)
		} else {
			err = c.Send(chunk)
		}
		if err != nil {
			return fmt.Errorf("send /changelog message %d/%d: %w", i+1, len(chunks), err)
		}
	}
	return nil
}
