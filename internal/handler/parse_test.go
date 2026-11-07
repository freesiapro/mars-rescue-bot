package handler

import (
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.in/telebot.v4"
	"mars-rescue-bot/internal/models"
)

func TestParseCooldownSeconds(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    int
		wantErr error
	}{
		{"valid", []string{"30"}, 30, nil},
		{"zero", []string{"0"}, 0, nil},
		{"max", []string{"86400"}, 86400, nil},
		{"trim spaces", []string{" 15 "}, 15, nil},
		{"missing arg", nil, 0, errCooldownUsage},
		{"too many args", []string{"1", "2"}, 0, errCooldownUsage},
		{"negative", []string{"-1"}, 0, errCooldownRange},
		{"over max", []string{"86401"}, 0, errCooldownRange},
		{"not a number", []string{"abc"}, 0, errCooldownRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCooldownSeconds(tt.args)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("seconds = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseMentionOption(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    bool
		wantErr bool
	}{
		{"on", []string{"on"}, true, false},
		{"off", []string{"off"}, false, false},
		{"uppercase", []string{"ON"}, true, false},
		{"mixed case", []string{"Off"}, false, false},
		{"missing", nil, false, true},
		{"invalid", []string{"yes"}, false, true},
		{"extra args", []string{"on", "off"}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMentionOption(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("enabled = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseIgnoreTarget(t *testing.T) {
	replied := &telebot.Message{Sender: &telebot.User{ID: 42}}
	channelReply := &telebot.Message{}

	tests := []struct {
		name    string
		args    []string
		replyTo *telebot.Message
		want    int64
		wantErr bool
	}{
		{"reply wins", []string{"99"}, replied, 42, false},
		{"numeric id", []string{"123"}, nil, 123, false},
		{"numeric id trimmed", []string{" 123 "}, nil, 123, false},
		{"reply without sender falls back", []string{"123"}, channelReply, 123, false},
		{"no target", nil, nil, 0, true},
		{"all args used", []string{"1", "2"}, nil, 0, true},
		{"not a number", []string{"alice"}, nil, 0, true},
		{"zero", []string{"0"}, nil, 0, true},
		{"negative", []string{"-5"}, nil, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseIgnoreTarget(tt.args, tt.replyTo)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("userID = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseRankPeriod(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    rankPeriod
		wantErr bool
	}{
		{"default week", nil, rankPeriodWeek, false},
		{"week", []string{"week"}, rankPeriodWeek, false},
		{"month", []string{"month"}, rankPeriodMonth, false},
		{"all", []string{"all"}, rankPeriodAll, false},
		{"uppercase", []string{"MONTH"}, rankPeriodMonth, false},
		{"trim spaces", []string{" all "}, rankPeriodAll, false},
		{"invalid", []string{"year"}, "", true},
		{"extra args", []string{"week", "month"}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRankPeriod(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseRankPeriod() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, errRankUsage) {
				t.Errorf("error = %v, want %v", err, errRankUsage)
			}
			if got != tt.want {
				t.Errorf("period = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRankPeriodLabel(t *testing.T) {
	tests := map[rankPeriod]string{
		rankPeriodWeek:  "本周",
		rankPeriodMonth: "本月",
		rankPeriodAll:   "总榜",
	}
	for period, want := range tests {
		if got := period.label(); got != want {
			t.Errorf("%q.label() = %q, want %q", period, got, want)
		}
	}
}

func TestMonthStart(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)

	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "mid month",
			now:  time.Date(2026, 11, 18, 15, 30, 0, 0, loc),
			want: time.Date(2026, 11, 1, 0, 0, 0, 0, loc),
		},
		{
			name: "first day",
			now:  time.Date(2026, 11, 1, 0, 0, 0, 0, loc),
			want: time.Date(2026, 11, 1, 0, 0, 0, 0, loc),
		},
		{
			name: "last day",
			now:  time.Date(2026, 11, 30, 23, 59, 0, 0, loc),
			want: time.Date(2026, 11, 1, 0, 0, 0, 0, loc),
		},
		{
			name: "input in another timezone",
			now:  time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC),
			want: time.Date(2026, 11, 1, 0, 0, 0, 0, loc),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := monthStart(tt.now, loc)
			if !got.Equal(tt.want) {
				t.Errorf("monthStart() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWeekStart(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)

	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "monday",
			now:  time.Date(2026, 11, 2, 15, 30, 0, 0, loc),
			want: time.Date(2026, 11, 2, 0, 0, 0, 0, loc),
		},
		{
			name: "sunday",
			now:  time.Date(2026, 11, 8, 23, 59, 0, 0, loc),
			want: time.Date(2026, 11, 2, 0, 0, 0, 0, loc),
		},
		{
			name: "cross month boundary",
			now:  time.Date(2026, 11, 1, 9, 0, 0, 0, loc),
			want: time.Date(2026, 10, 26, 0, 0, 0, 0, loc),
		},
		{
			name: "input in another timezone",
			now:  time.Date(2026, 11, 2, 1, 0, 0, 0, time.UTC),
			want: time.Date(2026, 11, 2, 0, 0, 0, 0, loc),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := weekStart(tt.now, loc)
			if !got.Equal(tt.want) {
				t.Errorf("weekStart() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatStatus(t *testing.T) {
	settings := &models.ChatSettings{Enabled: true, CooldownSeconds: 30, MentionEnabled: false}
	got := formatStatus(settings, 3)
	for _, want := range []string{"检测：开着", "提醒间隔：30 秒", "提醒里 @ 人：不会", "忽略名单：3 人"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatStatus() = %q, want containing %q", got, want)
		}
	}
}

func TestFormatIgnoreList(t *testing.T) {
	if got := formatIgnoreList(nil); got != "忽略名单是空的。" {
		t.Errorf("empty list = %q", got)
	}

	ignored := []models.ChatIgnoredUser{{UserID: 11}, {UserID: 22}}
	got := formatIgnoreList(ignored)
	for _, want := range []string{"2 人", "<code>11</code>", "<code>22</code>"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatIgnoreList() = %q, want containing %q", got, want)
		}
	}
}
