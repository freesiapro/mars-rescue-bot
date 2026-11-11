package handler

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"gopkg.in/telebot.v4"
)

type fakeChangelogContext struct {
	telebot.Context
	sender   *telebot.User
	args     []string
	replies  []string
	sends    []string
	replyErr error
	sendErr  error
}

func (f *fakeChangelogContext) Get(string) interface{} { return nil }

func (f *fakeChangelogContext) Sender() *telebot.User { return f.sender }

func (f *fakeChangelogContext) Args() []string { return f.args }

func (f *fakeChangelogContext) Reply(what interface{}, _ ...interface{}) error {
	if f.replyErr != nil {
		return f.replyErr
	}
	f.replies = append(f.replies, fmt.Sprint(what))
	return nil
}

func (f *fakeChangelogContext) Send(what interface{}, _ ...interface{}) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sends = append(f.sends, fmt.Sprint(what))
	return nil
}

func TestParseChangelogN(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantN      int
		wantCapped bool
		wantErr    bool
	}{
		{"default nil", nil, 1, false, false},
		{"default empty", []string{}, 1, false, false},
		{"one", []string{"1"}, 1, false, false},
		{"three", []string{"3"}, 3, false, false},
		{"trim spaces", []string{" 2 "}, 2, false, false},
		{"max", []string{"10"}, 10, false, false},
		{"over max", []string{"11"}, 10, true, false},
		{"far over max", []string{"99"}, 10, true, false},
		{"zero", []string{"0"}, 0, false, true},
		{"negative", []string{"-1"}, 0, false, true},
		{"decimal", []string{"1.5"}, 0, false, true},
		{"not a number", []string{"abc"}, 0, false, true},
		{"empty arg", []string{""}, 0, false, true},
		{"too many args", []string{"1", "2"}, 0, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotN, gotCapped, err := parseChangelogN(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseChangelogN() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, errChangelogUsage) {
				t.Errorf("error = %v, want %v", err, errChangelogUsage)
			}
			if gotN != tt.wantN {
				t.Errorf("n = %d, want %d", gotN, tt.wantN)
			}
			if gotCapped != tt.wantCapped {
				t.Errorf("capped = %v, want %v", gotCapped, tt.wantCapped)
			}
		})
	}
}

func TestUTF16CodeUnits(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{"empty", "", 0},
		{"ascii", "abc", 3},
		{"chinese counts one each", "火星救援", 4},
		{"emoji counts two", "📣", 2},
		{"mixed", "中📣a", 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := utf16CodeUnits(tt.text); got != tt.want {
				t.Fatalf("utf16CodeUnits(%q) = %d, want %d", tt.text, got, tt.want)
			}
		})
	}
}

func TestSplitUTF16(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		limit    int
		wantHead string
		wantTail string
	}{
		{"no split needed", "abc", 10, "abc", ""},
		{"zero limit", "abc", 0, "", "abc"},
		{"emoji exactly fits", "📣abc", 2, "📣", "abc"},
		{"emoji cannot fit remaining unit", "a📣b", 2, "a", "📣b"},
		{"split between ascii", "abcd", 2, "ab", "cd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			head, tail := splitUTF16(tt.text, tt.limit)
			if head != tt.wantHead || tail != tt.wantTail {
				t.Fatalf("splitUTF16(%q, %d) = (%q, %q), want (%q, %q)",
					tt.text, tt.limit, head, tail, tt.wantHead, tt.wantTail)
			}
			if !utf8.ValidString(head) || !utf8.ValidString(tail) {
				t.Fatalf("splitUTF16(%q, %d) produced invalid UTF-8", tt.text, tt.limit)
			}
		})
	}
}

func TestFormatChangelogChunks(t *testing.T) {
	t.Run("default shows only latest", func(t *testing.T) {
		chunks := formatChangelogChunks(changelogEntries, 1, false)
		if len(chunks) != 1 {
			t.Fatalf("chunks = %d, want 1: %#v", len(chunks), chunks)
		}
		got := changelogText(chunks)
		want := expectedChangelog(changelogEntries, 1, false)
		if got != want {
			t.Fatalf("formatChangelogChunks(1) = %q, want %q", got, want)
		}
		for _, absent := range []string{"2026-11-09", "一次最多显示 10 条，以下为最近的更新："} {
			if strings.Contains(got, absent) {
				t.Errorf("formatChangelogChunks(1) = %q, should not contain %q", got, absent)
			}
		}
	})

	t.Run("three shows three newest", func(t *testing.T) {
		chunks := formatChangelogChunks(changelogEntries, 3, false)
		got := changelogText(chunks)
		want := expectedChangelog(changelogEntries, 3, false)
		if got != want {
			t.Fatalf("formatChangelogChunks(3) = %q, want %q", got, want)
		}
		for _, wantText := range []string{
			"📣 最近 3 次更新",
			"2026-11-12 · 隐藏来源的本群消息转发也能识别",
			"2026-11-10 · 新增 /changelog 命令",
			"2026-11-09 · 本群消息的再次转发与相同回复不再记录",
		} {
			if !strings.Contains(got, wantText) {
				t.Errorf("formatChangelogChunks(3) = %q, want containing %q", got, wantText)
			}
		}
		if strings.Contains(got, "2026-11-07") {
			t.Errorf("formatChangelogChunks(3) = %q, should stop after three entries", got)
		}
	})

	t.Run("ten is not capped", func(t *testing.T) {
		chunks := formatChangelogChunks(changelogEntries, MaxChangelogN, false)
		got := changelogText(chunks)
		if got != expectedChangelog(changelogEntries, MaxChangelogN, false) {
			t.Fatalf("formatChangelogChunks(10) = %q, want expected entries", got)
		}
		if strings.Contains(got, "一次最多显示 10 条，以下为最近的更新：") {
			t.Errorf("formatChangelogChunks(10) = %q, should not contain the cap notice", got)
		}
		for _, entry := range changelogEntries {
			if !strings.Contains(got, entry.Date) || !strings.Contains(got, entry.Title) {
				t.Errorf("formatChangelogChunks(10) = %q, missing %s / %s", got, entry.Date, entry.Title)
			}
		}
	})

	t.Run("over max is capped", func(t *testing.T) {
		chunks := formatChangelogChunks(changelogEntries, MaxChangelogN, true)
		got := changelogText(chunks)
		want := expectedChangelog(changelogEntries, MaxChangelogN, true)
		if got != want {
			t.Fatalf("formatChangelogChunks(capped) = %q, want %q", got, want)
		}
		for _, wantText := range []string{
			"一次最多显示 10 条，以下为最近的更新：",
			"📣 最近 7 次更新",
			"2026-11-12 · 隐藏来源的本群消息转发也能识别",
			"2026-11-04 · 富文本、连续刷屏、普通转发与 /hyw",
		} {
			if !strings.Contains(got, wantText) {
				t.Errorf("formatChangelogChunks(capped) = %q, want containing %q", got, wantText)
			}
		}
	})
}

func TestChangelogCorrectedCopy(t *testing.T) {
	got := changelogText(formatChangelogChunks(changelogEntries, MaxChangelogN, false))
	for _, want := range []string{
		"本周榜和本月榜原本就按配置时区的周一开始、每月 1 日开始；本次修正了日期边界附近可能算错的问题。",
		"回复一条消息时，如果本条内容与被回复的那条相同，同样跳过；文字相同或同一个文件都算。",
		"同一内容连续刷屏时只记录、不提醒，也不会影响之后哪一次触发提醒；除该内容的第一次出现外，后续记录仍计入火星次数。",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("changelog copy missing %q", want)
		}
	}
	for _, old := range []string{
		"本周榜和本月榜改按配置时区的周一开始、每月 1 日开始统计，不受服务器时区影响。",
		"回复一条消息且正文与对方相同时，同样跳过。",
		"也不占用配对位置",
	} {
		if strings.Contains(got, old) {
			t.Errorf("changelog copy still contains old text %q", old)
		}
	}
}

func TestFormatChangelogChunksLongContent(t *testing.T) {
	entries := longChangelogEntries()
	chunks := formatChangelogChunks(entries, len(entries), false)
	if len(chunks) < 2 {
		t.Fatalf("chunks = %d, want multiple messages", len(chunks))
	}
	for i, chunk := range chunks {
		if got := utf16CodeUnits(chunk); got > changelogChunkLimit {
			t.Errorf("chunk %d has %d UTF-16 code units, limit %d", i, got, changelogChunkLimit)
		}
		if !utf8.ValidString(chunk) {
			t.Errorf("chunk %d is not valid UTF-8", i)
		}
		if i > 0 && !strings.HasPrefix(chunk, changelogContinuation) {
			t.Errorf("chunk %d = %q, want continuation prefix %q", i, chunk, changelogContinuation)
		}
	}

	got := changelogText(chunks)
	want := expectedChangelog(entries, len(entries), false)
	if got != want {
		t.Fatalf("long content changed after chunking:\ngot  %q\nwant %q", got, want)
	}

	// 再确认每条记录的标题和项目符号都按原顺序出现，没有在分块时丢失。
	position := 0
	for _, entry := range entries {
		position = requireOrderedText(t, got, position, entry.Date+" · "+entry.Title)
		for _, bullet := range entry.Bullets {
			position = requireOrderedText(t, got, position, bullet)
		}
	}
}

func TestFormatChangelogChunksOversizedRecord(t *testing.T) {
	huge := strings.Repeat("超长内容📣", 1200)
	entries := []ChangelogEntry{{
		Date:  "2026-10-01",
		Title: "单条超长记录",
		Bullets: []string{
			huge,
			"最后一条项目符号",
		},
	}}

	chunks := formatChangelogChunks(entries, 1, false)
	if len(chunks) < 2 {
		t.Fatalf("chunks = %d, want a single oversized record to be split", len(chunks))
	}
	for i, chunk := range chunks {
		if got := utf16CodeUnits(chunk); got > changelogChunkLimit {
			t.Errorf("chunk %d has %d UTF-16 code units, limit %d", i, got, changelogChunkLimit)
		}
		if !utf8.ValidString(chunk) {
			t.Errorf("chunk %d is not valid UTF-8", i)
		}
		if i > 0 && !strings.HasPrefix(chunk, changelogContinuation) {
			t.Errorf("chunk %d = %q, want continuation prefix %q", i, chunk, changelogContinuation)
		}
	}
	got := changelogText(chunks)
	if !strings.Contains(got, huge) {
		t.Errorf("oversized record content is incomplete after chunking")
	}
	if !strings.Contains(got, "最后一条项目符号") {
		t.Errorf("bullet after an oversized record was lost: %q", got)
	}
}

func TestFormatChangelogChunksNoEntries(t *testing.T) {
	chunks := formatChangelogChunks(nil, 1, false)
	if !slices.Equal(chunks, []string{"📣 还没有更新记录。"}) {
		t.Fatalf("chunks = %#v, want a single empty-state message", chunks)
	}
}

func TestHandleChangelog(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantText    []string
		absentText  []string
		wantErrText bool
	}{
		{
			name:       "default",
			args:       nil,
			wantText:   []string{"📣 最近 1 次更新", "2026-11-12 · 隐藏来源的本群消息转发也能识别", "转发时隐藏了来源账号、但内容其实来自本群的消息，现在也能识别；对得上本群已有记录就跳过，不提醒、不记录，也不计入火星次数。"},
			absentText: []string{"2026-11-10", "一次最多显示 10 条，以下为最近的更新："},
		},
		{
			name:       "three",
			args:       []string{"3"},
			wantText:   []string{"📣 最近 3 次更新", "2026-11-12", "2026-11-10", "2026-11-09"},
			absentText: []string{"2026-11-07", "一次最多显示 10 条，以下为最近的更新："},
		},
		{
			name:       "exact max has no cap notice",
			args:       []string{"10"},
			wantText:   []string{"📣 最近 7 次更新", "2026-11-04"},
			absentText: []string{"一次最多显示 10 条，以下为最近的更新："},
		},
		{
			name:     "over max",
			args:     []string{"99"},
			wantText: []string{"一次最多显示 10 条，以下为最近的更新：", "📣 最近 7 次更新"},
		},
		{
			name:        "invalid",
			args:        []string{"abc"},
			wantErrText: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &CommandHandler{}
			ctx := &fakeChangelogContext{
				sender: &telebot.User{ID: 7, FirstName: "Tester"},
				args:   tt.args,
			}
			if err := h.handleChangelog(ctx); err != nil {
				t.Fatalf("handleChangelog() error = %v", err)
			}
			if len(ctx.replies) != 1 {
				t.Fatalf("replies = %d, want 1: %#v", len(ctx.replies), ctx.replies)
			}
			if len(ctx.sends) != 0 {
				t.Fatalf("sends = %d, want 0 for a single-message reply: %#v", len(ctx.sends), ctx.sends)
			}
			got := ctx.replies[0]
			if tt.wantErrText {
				if got != errChangelogUsage.Error() {
					t.Errorf("reply = %q, want %q", got, errChangelogUsage.Error())
				}
				return
			}
			for _, want := range tt.wantText {
				if !strings.Contains(got, want) {
					t.Errorf("reply = %q, want containing %q", got, want)
				}
			}
			for _, absent := range tt.absentText {
				if strings.Contains(got, absent) {
					t.Errorf("reply = %q, should not contain %q", got, absent)
				}
			}
		})
	}
}

func TestHandleChangelogSendsAllChunks(t *testing.T) {
	original := changelogEntries
	changelogEntries = longChangelogEntries()
	defer func() { changelogEntries = original }()

	h := &CommandHandler{}
	ctx := &fakeChangelogContext{
		sender: &telebot.User{ID: 7, FirstName: "Tester"},
		args:   []string{"4"},
	}
	if err := h.handleChangelog(ctx); err != nil {
		t.Fatalf("handleChangelog() error = %v", err)
	}

	wantChunks := formatChangelogChunks(changelogEntries, 4, false)
	if len(wantChunks) < 2 {
		t.Fatalf("test data produced %d chunk(s), want multiple", len(wantChunks))
	}
	if len(ctx.replies) != 1 {
		t.Fatalf("replies = %d, want 1 (the first chunk): %#v", len(ctx.replies), ctx.replies)
	}
	if len(ctx.sends) != len(wantChunks)-1 {
		t.Fatalf("sends = %d, want %d: %#v", len(ctx.sends), len(wantChunks)-1, ctx.sends)
	}
	gotChunks := append(append([]string{}, ctx.replies...), ctx.sends...)
	if !slices.Equal(gotChunks, wantChunks) {
		t.Fatalf("sent chunks = %#v, want %#v", gotChunks, wantChunks)
	}
}

func TestHandleChangelogStopsAfterSendFailure(t *testing.T) {
	original := changelogEntries
	changelogEntries = longChangelogEntries()
	defer func() { changelogEntries = original }()

	boom := errors.New("send failed")
	h := &CommandHandler{}
	ctx := &fakeChangelogContext{
		sender:  &telebot.User{ID: 7, FirstName: "Tester"},
		args:    []string{"4"},
		sendErr: boom,
	}
	err := h.handleChangelog(ctx)
	if !errors.Is(err, boom) {
		t.Fatalf("handleChangelog() error = %v, want wrapping %v", err, boom)
	}
	if len(ctx.sends) != 0 {
		t.Fatalf("sends = %d, want no successful send after the failure: %#v", len(ctx.sends), ctx.sends)
	}
}

func longChangelogEntries() []ChangelogEntry {
	entries := make([]ChangelogEntry, 0, 4)
	for i := 1; i <= 4; i++ {
		bullets := make([]string, 0, 3)
		for j := 1; j <= 3; j++ {
			bullets = append(bullets, fmt.Sprintf(
				"长记录 %d-%d：%s",
				i,
				j,
				strings.Repeat("这是一段用于测试分块的中文内容。", 80),
			))
		}
		entries = append(entries, ChangelogEntry{
			Date:    fmt.Sprintf("2026-10-%02d", i),
			Title:   fmt.Sprintf("长记录 %d", i),
			Bullets: bullets,
		})
	}
	return entries
}

func expectedChangelog(entries []ChangelogEntry, n int, capped bool) string {
	if n < 1 {
		n = 1
	}
	if n > len(entries) {
		n = len(entries)
	}
	if n == 0 {
		return "📣 还没有更新记录。"
	}

	var sb strings.Builder
	if capped {
		fmt.Fprintf(&sb, "一次最多显示 %d 条，以下为最近的更新：\n\n", MaxChangelogN)
	}
	fmt.Fprintf(&sb, "📣 最近 %d 次更新\n", n)
	for _, entry := range entries[:n] {
		fmt.Fprintf(&sb, "\n%s · %s\n", entry.Date, entry.Title)
		for _, bullet := range entry.Bullets {
			fmt.Fprintf(&sb, "• %s\n", bullet)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// changelogText 拼回分块内容，去掉后续消息开头的“（续）”标记。
func changelogText(chunks []string) string {
	var sb strings.Builder
	for i, chunk := range chunks {
		if i > 0 {
			chunk = strings.TrimPrefix(chunk, changelogContinuation)
		}
		sb.WriteString(chunk)
	}
	return sb.String()
}

func requireOrderedText(t *testing.T, text string, start int, want string) int {
	t.Helper()
	index := strings.Index(text[start:], want)
	if index < 0 {
		t.Fatalf("text %q missing %q at or after offset %d", text, want, start)
	}
	return start + index + len(want)
}
