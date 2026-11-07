package handler

import (
	"strings"
	"testing"
)

func TestFormatPrivacy(t *testing.T) {
	out := formatPrivacy(90)
	for _, want := range []string{
		"HMAC-SHA256",
		"file_unique_id",
		"不存用户名，也不存名字",
		"向 Telegram 查现在的名字",
		"群友 123",
		"90 天",
		"DATA_RETENTION_DAYS",
		"只算还在保留期内的重复次数",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formatPrivacy() = %q, want containing %q", out, want)
		}
	}
}
