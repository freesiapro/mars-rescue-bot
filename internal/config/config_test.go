package config

import "testing"

func TestParseRetentionDays(t *testing.T) {
	tests := []struct {
		raw  string
		want int
	}{
		{"", DefaultDataRetentionDays},
		{"   ", DefaultDataRetentionDays},
		{"30", 30},
		{" 30 ", 30},
		{"180", 180},
		{"1", 1},
		{"0", DefaultDataRetentionDays},
		{"-5", DefaultDataRetentionDays},
		{"abc", DefaultDataRetentionDays},
	}
	for _, tt := range tests {
		if got := parseRetentionDays(tt.raw); got != tt.want {
			t.Errorf("parseRetentionDays(%q) = %d, want %d", tt.raw, got, tt.want)
		}
	}
}
