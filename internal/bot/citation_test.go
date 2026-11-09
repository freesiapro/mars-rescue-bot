package bot

import (
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.in/telebot.v4"
	"gorm.io/gorm"
	"mars-rescue-bot/internal/models"
	"mars-rescue-bot/internal/names"
	"mars-rescue-bot/internal/repo"
)

const citeText = "hello mars bot"

type roleMembers struct {
	role  telebot.MemberStatus
	err   error
	calls int
}

func (m *roleMembers) ChatMemberOf(telebot.Recipient, telebot.Recipient) (*telebot.ChatMember, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return &telebot.ChatMember{Role: m.role}, nil
}

func setupCitation(t *testing.T, members names.MemberProvider) (*Processor, *repo.Repo, *gorm.DB) {
	t.Helper()
	p, r, db := setupProcessor(t)
	p.names = names.New(members)
	return p, r, db
}

func processAll(t *testing.T, p *Processor, rec *replyRecorder, msgs ...*telebot.Message) {
	t.Helper()
	for i, msg := range msgs {
		if err := p.ProcessMessage(msg, "trace", rec.reply); err != nil {
			t.Fatalf("ProcessMessage() %d error = %v", i+1, err)
		}
	}
}

func countRows(t *testing.T, db *gorm.DB, model interface{}) int64 {
	t.Helper()
	var n int64
	if err := db.Model(model).Count(&n).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

func assertRankCount(t *testing.T, r *repo.Repo, want int) {
	t.Helper()
	stats, err := r.GetTopRescuers(testChatID, time.Time{}, 10)
	if err != nil {
		t.Fatalf("GetTopRescuers() error = %v", err)
	}
	if len(stats) != want {
		t.Fatalf("rank entries = %d, want %d (%#v)", len(stats), want, stats)
	}
}

func userOrigin(msg *telebot.Message, authorID int64, name string) *telebot.Message {
	msg.Origin = &telebot.MessageOrigin{
		Type:   "user",
		Sender: &telebot.User{ID: authorID, FirstName: name},
	}
	return msg
}

func TestCitationInGroupAuthorForward(t *testing.T) {
	for _, role := range []telebot.MemberStatus{telebot.Creator, telebot.Administrator, telebot.Member, telebot.Restricted} {
		t.Run(string(role), func(t *testing.T) {
			members := &roleMembers{role: role}
			p, r, db := setupCitation(t, members)
			buf := captureDecisionLog(t, p)
			rec := &replyRecorder{}

			authorID := int64(8001)
			first := textMessage(testChatID, authorID, "Author", citeText, 10)
			second := userOrigin(textMessage(testChatID, 8002, "Forwarder", citeText, 11), authorID, "Author")
			third := userOrigin(textMessage(testChatID, 8003, "Again", citeText, 12), authorID, "Author")
			processAll(t, p, rec, first, second, third)

			if len(rec.messages) != 0 {
				t.Fatalf("replies = %#v, want none", rec.messages)
			}
			if got := countRows(t, db, &models.ForwardRecord{}); got != 1 {
				t.Errorf("records = %d, want 1", got)
			}
			if got := countRows(t, db, &models.ForwardLog{}); got != 1 {
				t.Errorf("logs = %d, want 1", got)
			}
			assertRankCount(t, r, 0)
			if members.calls != 1 {
				t.Errorf("getChatMember calls = %d, want 1", members.calls)
			}
			lines := readDecisions(t, buf)
			if len(lines) != 3 {
				t.Fatalf("decisions = %d, want 3", len(lines))
			}
			if lines[1].Decision != DecisionCitationSkipped || lines[1].KeyHash != "" {
				t.Errorf("forward decision = %+v, want citation_skipped without key", lines[1])
			}
			if lines[2].Decision != DecisionCitationSkipped {
				t.Errorf("second forward decision = %q, want %q", lines[2].Decision, DecisionCitationSkipped)
			}
		})
	}

	t.Run("author left", func(t *testing.T) {
		members := &roleMembers{role: telebot.Left}
		p, r, db := setupCitation(t, members)
		rec := &replyRecorder{}
		first := textMessage(testChatID, 8001, "Author", citeText, 10)
		second := userOrigin(textMessage(testChatID, 8002, "Forwarder", citeText, 11), 8001, "Author")
		processAll(t, p, rec, first, second)

		if len(rec.messages) != 1 || !strings.Contains(rec.messages[0], "🚨 火星救援！") {
			t.Fatalf("replies = %#v, want one alert", rec.messages)
		}
		var record models.ForwardRecord
		if err := db.First(&record).Error; err != nil {
			t.Fatalf("load record: %v", err)
		}
		if record.ContentKey != textKeyFor(p.dedupSecret, citeText) {
			t.Errorf("ContentKey = %q, want hand-typed text key", record.ContentKey)
		}
		if record.ForwardCount != 2 {
			t.Errorf("ForwardCount = %d, want 2", record.ForwardCount)
		}
		assertRankCount(t, r, 1)
		if members.calls != 1 {
			t.Errorf("getChatMember calls = %d, want 1", members.calls)
		}
	})

	t.Run("author kicked", func(t *testing.T) {
		members := &roleMembers{role: telebot.Kicked}
		p, _, _ := setupCitation(t, members)
		rec := &replyRecorder{}
		first := textMessage(testChatID, 8001, "Author", citeText, 10)
		second := userOrigin(textMessage(testChatID, 8002, "Forwarder", citeText, 11), 8001, "Author")
		processAll(t, p, rec, first, second)
		if len(rec.messages) != 1 {
			t.Fatalf("replies = %#v, want one alert", rec.messages)
		}
	})
}

func TestCitationSelfForwardThenHandTypedStillAlerts(t *testing.T) {
	members := &roleMembers{err: errors.New("should not be called")}
	p, r, db := setupCitation(t, members)
	rec := &replyRecorder{}

	first := textMessage(testChatID, 8001, "Me", citeText, 10)
	self := userOrigin(textMessage(testChatID, 8001, "Me", citeText, 11), 8001, "Me")
	again := textMessage(testChatID, 8001, "Me", citeText, 12)
	processAll(t, p, rec, first, self, again)

	if members.calls != 0 {
		t.Errorf("getChatMember calls = %d, want 0 for self-forward", members.calls)
	}
	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1", rec.messages)
	}
	if !strings.Contains(rec.messages[0], "你自己") {
		t.Errorf("reply = %q, want self-repeat copy", rec.messages[0])
	}
	if got := countRows(t, db, &models.ForwardRecord{}); got != 1 {
		t.Errorf("records = %d, want 1", got)
	}
	if got := countRows(t, db, &models.ForwardLog{}); got != 2 {
		t.Errorf("logs = %d, want 2 (self-forward not stored)", got)
	}
	assertRankCount(t, r, 1)
}

func TestCitationHiddenUserIsNotSkipped(t *testing.T) {
	p, _, db := setupCitation(t, &roleMembers{role: telebot.Member})
	rec := &replyRecorder{}

	newHidden := func(msgID int, userID int64) *telebot.Message {
		msg := textMessage(testChatID, userID, "Forwarder", citeText, msgID)
		msg.Origin = &telebot.MessageOrigin{Type: "hidden_user", SenderUsername: "Forwarder"}
		return msg
	}
	processAll(t, p, rec, newHidden(10, 8002), newHidden(11, 8003))

	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1", rec.messages)
	}
	var record models.ForwardRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if record.ContentKey != textKeyFor(p.dedupSecret, citeText) {
		t.Errorf("ContentKey = %q, want text key", record.ContentKey)
	}
}

func TestCitationChatOrigin(t *testing.T) {
	t.Run("this chat", func(t *testing.T) {
		p, r, db := setupCitation(t, nil)
		rec := &replyRecorder{}
		first := textMessage(testChatID, 8001, "Author", citeText, 10)
		second := textMessage(testChatID, 8002, "Forwarder", citeText, 11)
		second.Origin = &telebot.MessageOrigin{
			Type:       "chat",
			SenderChat: &telebot.Chat{ID: testChatID, Type: telebot.ChatSuperGroup},
		}
		hand := textMessage(testChatID, 8001, "Author", citeText, 12)
		processAll(t, p, rec, first, second, hand)

		if len(rec.messages) != 1 || !strings.Contains(rec.messages[0], "你自己") {
			t.Fatalf("replies = %#v, want the later hand-typed repeat", rec.messages)
		}
		if got := countRows(t, db, &models.ForwardLog{}); got != 2 {
			t.Errorf("logs = %d, want 2", got)
		}
		assertRankCount(t, r, 1)
	})

	t.Run("other chat", func(t *testing.T) {
		p, _, db := setupCitation(t, nil)
		rec := &replyRecorder{}
		newForward := func(msgID int, userID int64) *telebot.Message {
			msg := textMessage(testChatID, userID, "Forwarder", citeText, msgID)
			msg.Origin = &telebot.MessageOrigin{
				Type:       "chat",
				SenderChat: &telebot.Chat{ID: -100777, Type: telebot.ChatSuperGroup},
			}
			return msg
		}
		processAll(t, p, rec, newForward(10, 8002), newForward(11, 8003))
		if len(rec.messages) != 1 {
			t.Fatalf("replies = %#v, want 1", rec.messages)
		}
		var record models.ForwardRecord
		if err := db.First(&record).Error; err != nil {
			t.Fatalf("load record: %v", err)
		}
		if record.ContentKey != textKeyFor(p.dedupSecret, citeText) {
			t.Errorf("ContentKey = %q, want text key", record.ContentKey)
		}
	})
}

func TestCitationChannelForwardStaysDuplicate(t *testing.T) {
	p, _, db := setupCitation(t, &roleMembers{role: telebot.Member})
	rec := &replyRecorder{}

	newChannel := func(msgID int, userID int64, automatic bool) *telebot.Message {
		msg := textMessage(testChatID, userID, "Forwarder", "https://example.com/mars-rescue", msgID)
		msg.AutomaticForward = automatic
		msg.Origin = &telebot.MessageOrigin{
			Type:      "channel",
			Chat:      &telebot.Chat{ID: -100888, Type: telebot.ChatChannel},
			MessageID: 77,
		}
		return msg
	}
	first := newChannel(10, 8001, true)
	second := newChannel(11, 8002, false)
	second.ReplyTo = first
	processAll(t, p, rec, first, second)

	if len(rec.messages) != 1 || !strings.Contains(rec.messages[0], "这条转发") {
		t.Fatalf("replies = %#v, want channel alert", rec.messages)
	}
	var record models.ForwardRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if record.ContentKey != "channel:-100888:77" {
		t.Errorf("ContentKey = %q, want channel:-100888:77", record.ContentKey)
	}
}

func TestCitationChannelLinkDoesNotMatchTypedLink(t *testing.T) {
	p, _, db := setupCitation(t, nil)
	rec := &replyRecorder{}
	const link = "https://example.com/mars-rescue"

	typed := textMessage(testChatID, 8001, "Typist", link, 10)
	channel := textMessage(testChatID, 8002, "Forwarder", link, 11)
	channel.Origin = &telebot.MessageOrigin{
		Type:      "channel",
		Chat:      &telebot.Chat{ID: -100888, Type: telebot.ChatChannel},
		MessageID: 90,
	}
	again := textMessage(testChatID, 8003, "Again", link, 12)
	again.Origin = &telebot.MessageOrigin{
		Type:      "channel",
		Chat:      &telebot.Chat{ID: -100888, Type: telebot.ChatChannel},
		MessageID: 90,
	}
	processAll(t, p, rec, typed, channel)
	if len(rec.messages) != 0 {
		t.Fatalf("replies after typed link and a different channel post = %#v, want none", rec.messages)
	}
	if got := countRows(t, db, &models.ForwardRecord{}); got != 2 {
		t.Fatalf("records = %d, want 2", got)
	}
	processAll(t, p, rec, again)
	if len(rec.messages) != 1 || !strings.Contains(rec.messages[0], "这条转发") {
		t.Fatalf("replies = %#v, want the same channel post to alert", rec.messages)
	}
}

func TestCitationInGroupPhotoSkippedOutsidePhotoAlerts(t *testing.T) {
	t.Run("author in group", func(t *testing.T) {
		p, r, db := setupCitation(t, &roleMembers{role: telebot.Member})
		rec := &replyRecorder{}
		first := textMessage(testChatID, 8001, "Cam", "", 10)
		first.Photo = &telebot.Photo{File: telebot.File{UniqueID: "cite-photo"}}
		second := textMessage(testChatID, 8002, "Forwarder", "", 11)
		second.Photo = &telebot.Photo{File: telebot.File{UniqueID: "cite-photo"}}
		userOrigin(second, 8001, "Cam")
		processAll(t, p, rec, first, second)
		if len(rec.messages) != 0 {
			t.Fatalf("replies = %#v, want none", rec.messages)
		}
		if got := countRows(t, db, &models.ForwardRecord{}); got != 1 {
			t.Errorf("records = %d, want 1", got)
		}
		assertRankCount(t, r, 0)
	})

	t.Run("author outside", func(t *testing.T) {
		p, _, _ := setupCitation(t, &roleMembers{role: telebot.Left})
		rec := &replyRecorder{}
		first := textMessage(testChatID, 8001, "Cam", "", 10)
		first.Photo = &telebot.Photo{File: telebot.File{UniqueID: "cite-photo"}}
		second := textMessage(testChatID, 8002, "Forwarder", "", 11)
		second.Photo = &telebot.Photo{File: telebot.File{UniqueID: "cite-photo"}}
		userOrigin(second, 8099, "Outsider")
		processAll(t, p, rec, first, second)
		if len(rec.messages) != 1 || !strings.Contains(rec.messages[0], "这张图") {
			t.Fatalf("replies = %#v, want photo alert", rec.messages)
		}
	})
}

func TestCitationForwardThenComment(t *testing.T) {
	p, _, db := setupCitation(t, &roleMembers{role: telebot.Member})
	buf := captureDecisionLog(t, p)
	rec := &replyRecorder{}

	original := textMessage(testChatID, 8001, "Author", citeText, 10)
	forwarded := userOrigin(textMessage(testChatID, 8002, "Forwarder", citeText, 11), 8001, "Author")
	shortComment := textMessage(testChatID, 8002, "Forwarder", "同意", 12)
	shortComment.ReplyTo = forwarded
	longComment := textMessage(testChatID, 8002, "Forwarder", "我觉得这里还可以再看看", 13)
	longComment.ReplyTo = forwarded
	processAll(t, p, rec, original, forwarded, shortComment, longComment)

	if len(rec.messages) != 0 {
		t.Fatalf("replies = %#v, want none", rec.messages)
	}
	if got := countRows(t, db, &models.ForwardRecord{}); got != 2 {
		t.Errorf("records = %d, want original plus the new comment", got)
	}
	lines := readDecisions(t, buf)
	if len(lines) != 4 {
		t.Fatalf("decisions = %d, want 4", len(lines))
	}
	if lines[1].Decision != DecisionCitationSkipped {
		t.Errorf("forward decision = %q, want %q", lines[1].Decision, DecisionCitationSkipped)
	}
	if lines[2].Decision != DecisionShortTextSkipped {
		t.Errorf("short comment decision = %q, want %q", lines[2].Decision, DecisionShortTextSkipped)
	}
	if lines[3].Decision != DecisionPairedSilent {
		t.Errorf("long comment decision = %q, want %q", lines[3].Decision, DecisionPairedSilent)
	}
}

func TestCitationReplyAndQuote(t *testing.T) {
	t.Run("quote with new body is not skipped", func(t *testing.T) {
		p, _, db := setupCitation(t, nil)
		buf := captureDecisionLog(t, p)
		rec := &replyRecorder{}
		first := textMessage(testChatID, 8001, "Author", citeText, 10)
		reply := textMessage(testChatID, 8002, "Replier", "a fresh comment here", 11)
		reply.ReplyTo = first
		reply.Quote = &telebot.TextQuote{Text: citeText, Manual: true}
		processAll(t, p, rec, first, reply)
		if len(rec.messages) != 0 {
			t.Fatalf("replies = %#v, want none", rec.messages)
		}
		if got := countRows(t, db, &models.ForwardRecord{}); got != 2 {
			t.Errorf("records = %d, want 2", got)
		}
		lines := readDecisions(t, buf)
		if lines[1].Decision != DecisionPairedSilent {
			t.Errorf("reply decision = %q, want %q", lines[1].Decision, DecisionPairedSilent)
		}
	})

	t.Run("reply body matches target", func(t *testing.T) {
		p, r, db := setupCitation(t, nil)
		rec := &replyRecorder{}
		first := textMessage(testChatID, 8001, "Author", citeText, 10)
		reply := textMessage(testChatID, 8002, "Replier", "Hello, MARS bot", 11)
		reply.ReplyTo = first
		reply.Quote = &telebot.TextQuote{Text: citeText, Manual: true}
		processAll(t, p, rec, first, reply)
		if len(rec.messages) != 0 {
			t.Fatalf("replies = %#v, want none", rec.messages)
		}
		if got := countRows(t, db, &models.ForwardRecord{}); got != 1 {
			t.Errorf("records = %d, want 1", got)
		}
		if got := countRows(t, db, &models.ForwardLog{}); got != 1 {
			t.Errorf("logs = %d, want 1", got)
		}
		assertRankCount(t, r, 0)
	})

	t.Run("reply to A with body of B still alerts", func(t *testing.T) {
		p, _, _ := setupCitation(t, nil)
		rec := &replyRecorder{}
		bodyB := textMessage(testChatID, 8001, "Author", citeText, 10)
		bodyA := textMessage(testChatID, 8003, "Other", "another topic entirely", 11)
		reply := textMessage(testChatID, 8002, "Replier", citeText, 12)
		reply.ReplyTo = bodyA
		reply.Quote = &telebot.TextQuote{Text: "another topic entirely", Manual: true}
		processAll(t, p, rec, bodyB, bodyA, reply)
		if len(rec.messages) != 1 {
			t.Fatalf("replies = %#v, want the duplicate of B", rec.messages)
		}
	})

	t.Run("same photo replied", func(t *testing.T) {
		p, _, db := setupCitation(t, nil)
		rec := &replyRecorder{}
		first := textMessage(testChatID, 8001, "Cam", "", 10)
		first.Photo = &telebot.Photo{File: telebot.File{UniqueID: "cite-photo"}}
		reply := textMessage(testChatID, 8002, "Replier", "", 11)
		reply.Photo = &telebot.Photo{File: telebot.File{UniqueID: "cite-photo"}}
		reply.ReplyTo = first
		processAll(t, p, rec, first, reply)
		if len(rec.messages) != 0 {
			t.Fatalf("replies = %#v, want none", rec.messages)
		}
		if got := countRows(t, db, &models.ForwardRecord{}); got != 1 {
			t.Errorf("records = %d, want 1", got)
		}
	})
}

func TestCitationPlainPasteStillAlerts(t *testing.T) {
	p, _, _ := setupCitation(t, nil)
	rec := &replyRecorder{}
	first := textMessage(testChatID, 8001, "A", citeText, 10)
	second := textMessage(testChatID, 8002, "B", citeText, 11)
	processAll(t, p, rec, first, second)
	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want 1", rec.messages)
	}
}

func TestCitationMemberLookupFailureDoesNotSkip(t *testing.T) {
	members := &roleMembers{err: errors.New("telegram down")}
	p, _, db := setupCitation(t, members)
	rec := &replyRecorder{}
	first := textMessage(testChatID, 8001, "Author", citeText, 10)
	second := userOrigin(textMessage(testChatID, 8002, "Forwarder", citeText, 11), 8001, "Author")
	processAll(t, p, rec, first, second)
	if members.calls != 1 {
		t.Errorf("getChatMember calls = %d, want 1", members.calls)
	}
	if len(rec.messages) != 1 {
		t.Fatalf("replies = %#v, want one alert", rec.messages)
	}
	var record models.ForwardRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if record.ForwardCount != 2 {
		t.Errorf("ForwardCount = %d, want 2", record.ForwardCount)
	}
}
