package bot

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"gopkg.in/telebot.v4"
	"gorm.io/gorm"
	"mars-rescue-bot/internal/models"
)

func hiddenOrigin(msg *telebot.Message, senderName string, unix int64) *telebot.Message {
	msg.Origin = &telebot.MessageOrigin{
		Type:           "hidden_user",
		SenderUsername: senderName,
		DateUnixtime:   unix,
	}
	return msg
}

// setLogCreatedAt 把一条流水的插入时间改成指定时刻，用来构造升级前的旧流水。
func setLogCreatedAt(t *testing.T, db *gorm.DB, targetMsgID int, at time.Time) {
	t.Helper()
	if err := db.Model(&models.ForwardLog{}).
		Where("target_msg_id = ?", targetMsgID).
		Update("created_at", at).Error; err != nil {
		t.Fatalf("update log created_at: %v", err)
	}
}

// captureWarnLog 捕获全局 slog 输出。citation 里的成员查询 Warn 走 slog.Default。
func captureWarnLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

func assertNoReplyAndOnlyFirstLog(t *testing.T, rec *replyRecorder, db *gorm.DB) {
	t.Helper()
	if len(rec.messages) != 0 {
		t.Fatalf("replies = %#v, want none", rec.messages)
	}
	if got := countRows(t, db, &models.ForwardRecord{}); got != 1 {
		t.Errorf("records = %d, want 1", got)
	}
	if got := countRows(t, db, &models.ForwardLog{}); got != 1 {
		t.Errorf("logs = %d, want 1", got)
	}
}

func TestCitationHiddenUserTimedLog(t *testing.T) {
	const sentAt = int64(1_700_000_000)

	t.Run("exact time is skipped without name lookup", func(t *testing.T) {
		members := &roleMembers{role: telebot.Member, err: errors.New("name lookup must not run")}
		p, r, db := setupCitation(t, members)
		buf := captureDecisionLog(t, p)
		rec := &replyRecorder{}

		first := atUnix(textMessage(testChatID, 8001, "Author", citeText, 10), sentAt)
		forwarded := hiddenOrigin(textMessage(testChatID, 8002, "Forwarder", citeText, 11), "Author", sentAt)
		processAll(t, p, rec, first, forwarded)

		assertNoReplyAndOnlyFirstLog(t, rec, db)
		assertRankCount(t, r, 0)
		if members.calls != 0 {
			t.Errorf("getChatMember calls = %d, want 0 when the recorded time matches", members.calls)
		}
		lines := readDecisions(t, buf)
		if len(lines) != 2 {
			t.Fatalf("decisions = %d, want 2", len(lines))
		}
		line := lines[1]
		if line.Decision != DecisionCitationSkipped {
			t.Errorf("decision = %q, want %q", line.Decision, DecisionCitationSkipped)
		}
		if line.Reason != citationReasonHiddenExactTime {
			t.Errorf("reason = %q, want %q", line.Reason, citationReasonHiddenExactTime)
		}
		if line.OriginType != "hidden_user" {
			t.Errorf("origin_type = %q, want hidden_user", line.OriginType)
		}
		if line.OriginDate != sentAt {
			t.Errorf("origin_date = %d, want %d", line.OriginDate, sentAt)
		}
	})

	t.Run("one second difference still alerts", func(t *testing.T) {
		p, _, _ := setupCitation(t, &roleMembers{role: telebot.Member})
		buf := captureDecisionLog(t, p)
		rec := &replyRecorder{}

		first := atUnix(textMessage(testChatID, 8001, "Author", citeText, 10), sentAt)
		forwarded := hiddenOrigin(textMessage(testChatID, 8002, "Forwarder", citeText, 11), "Author", sentAt+1)
		processAll(t, p, rec, first, forwarded)

		if len(rec.messages) != 1 {
			t.Fatalf("replies = %#v, want one alert", rec.messages)
		}
		lines := readDecisions(t, buf)
		if len(lines) != 2 {
			t.Fatalf("decisions = %d, want 2", len(lines))
		}
		if lines[1].Decision != DecisionAlerted {
			t.Errorf("decision = %q, want %q", lines[1].Decision, DecisionAlerted)
		}
		if lines[1].Reason != citationMissNoTimedMatch {
			t.Errorf("reason = %q, want %q", lines[1].Reason, citationMissNoTimedMatch)
		}
	})
}

func TestCitationHiddenUserLegacyLog(t *testing.T) {
	const sentAt = int64(1_700_000_000)
	author := &telebot.User{ID: 8001, FirstName: "Hidden", LastName: "Person"}

	t.Run("inside window with matching display name is skipped", func(t *testing.T) {
		members := &roleMembers{role: telebot.Member, user: author}
		p, r, db := setupCitation(t, members)
		buf := captureDecisionLog(t, p)
		rec := &replyRecorder{}

		// 没有 Unixtime 的第一条会写成 message_unix=NULL 的旧流水。
		first := textMessage(testChatID, 8001, "Hidden", citeText, 10)
		processAll(t, p, rec, first)
		setLogCreatedAt(t, db, 10, time.Unix(sentAt+30, 0))

		// 姓名前后空白和连续空白不影响比对。
		forwarded := hiddenOrigin(textMessage(testChatID, 8002, "Forwarder", citeText, 11), "  Hidden   Person  ", sentAt)
		processAll(t, p, rec, forwarded)

		assertNoReplyAndOnlyFirstLog(t, rec, db)
		assertRankCount(t, r, 0)
		if members.calls != 1 {
			t.Errorf("getChatMember calls = %d, want 1", members.calls)
		}
		lines := readDecisions(t, buf)
		if len(lines) != 2 {
			t.Fatalf("decisions = %d, want 2", len(lines))
		}
		if lines[1].Decision != DecisionCitationSkipped {
			t.Errorf("decision = %q, want %q", lines[1].Decision, DecisionCitationSkipped)
		}
		if lines[1].Reason != citationReasonHiddenLegacyWindow {
			t.Errorf("reason = %q, want %q", lines[1].Reason, citationReasonHiddenLegacyWindow)
		}
	})

	t.Run("inside window with different name alerts", func(t *testing.T) {
		members := &roleMembers{
			role: telebot.Member,
			user: &telebot.User{ID: 8001, FirstName: "Someone", LastName: "Else"},
		}
		p, _, db := setupCitation(t, members)
		buf := captureDecisionLog(t, p)
		rec := &replyRecorder{}

		first := textMessage(testChatID, 8001, "Hidden", citeText, 10)
		processAll(t, p, rec, first)
		setLogCreatedAt(t, db, 10, time.Unix(sentAt+30, 0))

		forwarded := hiddenOrigin(textMessage(testChatID, 8002, "Forwarder", citeText, 11), "Hidden Person", sentAt)
		processAll(t, p, rec, forwarded)

		if len(rec.messages) != 1 {
			t.Fatalf("replies = %#v, want one alert", rec.messages)
		}
		lines := readDecisions(t, buf)
		if len(lines) != 2 {
			t.Fatalf("decisions = %d, want 2", len(lines))
		}
		if lines[1].Reason != citationMissHiddenNameMismatch {
			t.Errorf("reason = %q, want %q", lines[1].Reason, citationMissHiddenNameMismatch)
		}
	})

	t.Run("outside window alerts even when the name matches", func(t *testing.T) {
		for _, tt := range []struct {
			name  string
			delta int64
		}{
			{name: "too late", delta: hiddenLegacyTimeWindowSeconds + 1},
			{name: "recorded before the original message", delta: -1},
		} {
			t.Run(tt.name, func(t *testing.T) {
				members := &roleMembers{role: telebot.Member, user: author}
				p, _, db := setupCitation(t, members)
				buf := captureDecisionLog(t, p)
				rec := &replyRecorder{}

				first := textMessage(testChatID, 8001, "Hidden", citeText, 10)
				processAll(t, p, rec, first)
				setLogCreatedAt(t, db, 10, time.Unix(sentAt+tt.delta, 0))

				forwarded := hiddenOrigin(textMessage(testChatID, 8002, "Forwarder", citeText, 11), "Hidden Person", sentAt)
				processAll(t, p, rec, forwarded)

				if len(rec.messages) != 1 {
					t.Fatalf("replies = %#v, want one alert", rec.messages)
				}
				if members.calls != 0 {
					t.Errorf("getChatMember calls = %d, want 0 outside the window", members.calls)
				}
				lines := readDecisions(t, buf)
				if len(lines) != 2 {
					t.Fatalf("decisions = %d, want 2", len(lines))
				}
				if lines[1].Reason != citationMissHiddenOutsideWindow {
					t.Errorf("reason = %q, want %q", lines[1].Reason, citationMissHiddenOutsideWindow)
				}
			})
		}
	})

	t.Run("member who left the group alerts", func(t *testing.T) {
		members := &roleMembers{role: telebot.Left, user: author}
		p, _, db := setupCitation(t, members)
		rec := &replyRecorder{}

		first := textMessage(testChatID, 8001, "Hidden", citeText, 10)
		processAll(t, p, rec, first)
		setLogCreatedAt(t, db, 10, time.Unix(sentAt+30, 0))

		forwarded := hiddenOrigin(textMessage(testChatID, 8002, "Forwarder", citeText, 11), "Hidden Person", sentAt)
		processAll(t, p, rec, forwarded)

		if len(rec.messages) != 1 {
			t.Fatalf("replies = %#v, want one alert", rec.messages)
		}
	})

	t.Run("lookup failure alerts and logs a warning", func(t *testing.T) {
		members := &roleMembers{err: errors.New("telegram down")}
		p, _, db := setupCitation(t, members)
		decisions := captureDecisionLog(t, p)
		warnings := captureWarnLog(t)
		rec := &replyRecorder{}

		first := textMessage(testChatID, 8001, "Hidden", citeText, 10)
		processAll(t, p, rec, first)
		setLogCreatedAt(t, db, 10, time.Unix(sentAt+30, 0))

		forwarded := hiddenOrigin(textMessage(testChatID, 8002, "Forwarder", citeText, 11), "Hidden Person", sentAt)
		processAll(t, p, rec, forwarded)

		if len(rec.messages) != 1 {
			t.Fatalf("replies = %#v, want one alert", rec.messages)
		}
		lines := readDecisions(t, decisions)
		if len(lines) != 2 {
			t.Fatalf("decisions = %d, want 2", len(lines))
		}
		if lines[1].Reason != citationMissMemberLookupFailed {
			t.Errorf("reason = %q, want %q", lines[1].Reason, citationMissMemberLookupFailed)
		}

		assertWarnFields(t, warnings, testChatID, 8001)
		if strings.Contains(warnings.String(), "Hidden Person") {
			t.Errorf("warning leaked the display name: %s", warnings.String())
		}
	})
}

func TestCitationHiddenExemptionNeedsForwardOrigin(t *testing.T) {
	members := &roleMembers{err: errors.New("should not be called")}
	p, _, _ := setupCitation(t, members)
	rec := &replyRecorder{}

	// 复制粘贴没有 Origin，不能套用隐藏来源的豁免规则。
	first := textMessage(testChatID, 8001, "A", citeText, 10)
	second := textMessage(testChatID, 8002, "B", citeText, 11)
	processAll(t, p, rec, first, second)

	if members.calls != 0 {
		t.Errorf("getChatMember calls = %d, want 0", members.calls)
	}
	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want one alert", rec.messages)
	}
}

func assertWarnFields(t *testing.T, buf *bytes.Buffer, wantChatID, wantUserID int64) {
	t.Helper()
	found := false
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			t.Fatalf("decode warning %q: %v", raw, err)
		}
		if entry["level"] != "WARN" {
			continue
		}
		found = true
		if got := int64(entry["chat_id"].(float64)); got != wantChatID {
			t.Errorf("warning chat_id = %d, want %d", got, wantChatID)
		}
		if got := int64(entry["user_id"].(float64)); got != wantUserID {
			t.Errorf("warning user_id = %d, want %d", got, wantUserID)
		}
		if entry["error"] == "" || entry["error"] == nil {
			t.Errorf("warning has no error field: %s", raw)
		}
	}
	if !found {
		t.Fatalf("no WARN log found: %s", buf.String())
	}
}
