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
		"不保存用户名或姓名",
		"向 Telegram 查询",
		"成员状态",
		"不写入数据库",
		"用户 &lt;id&gt;",
		"90 天",
		"DATA_RETENTION_DAYS",
		"只统计保留期内的重复次数",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formatPrivacy() = %q, want containing %q", out, want)
		}
	}
}
