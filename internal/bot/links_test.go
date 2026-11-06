package bot

import (
	"strings"
	"testing"

	"gopkg.in/telebot.v4"
	"mars-rescue-bot/internal/models"
)

func linkMessage(text string, msgID int) *telebot.Message {
	return textMessage(testChatID, 301, "Linker", text, msgID)
}

func TestAnalyzeMessageContentPureLink(t *testing.T) {
	const raw = "https://example.com/a/b?x=1&utm_source=chat#frag"
	content := AnalyzeMessageContent(linkMessage(raw, 801), contentTestSecret)
	if content.Key == "" {
		t.Fatalf("content = %+v, want a link key", content)
	}
	if content.SkipDecision != "" {
		t.Errorf("decision = %q, want no skip decision", content.SkipDecision)
	}
	if content.Kind != "link" || content.KeyType != "链接" {
		t.Errorf("content = %+v, want link/链接", content)
	}
	if len(content.Hash) != 8 {
		t.Errorf("hash = %q, want 8 hex chars", content.Hash)
	}
	if !strings.HasPrefix(content.Key, linkContentKeyPrefix) {
		t.Errorf("key = %q, want prefix %q", content.Key, linkContentKeyPrefix)
	}
	if strings.Contains(content.Key, "example.com") {
		t.Errorf("key = %q, must not contain the raw URL", content.Key)
	}
}

func TestAnalyzeMessageContentRichPureLink(t *testing.T) {
	msg := &telebot.Message{
		ID:     802,
		Chat:   &telebot.Chat{ID: testChatID, Type: telebot.ChatSuperGroup},
		Sender: &telebot.User{ID: 301, FirstName: "Linker"},
		RichMessage: &telebot.RichMessage{Blocks: []telebot.RichBlock{{
			Type: "paragraph",
			Text: &telebot.RichText{Kind: telebot.RichTextPlain, Plain: "https://example.com/a"},
		}}},
	}
	content := AnalyzeMessageContent(msg, contentTestSecret)
	if content.Kind != "link" || content.KeyType != "链接" {
		t.Errorf("content = %+v, want link/链接", content)
	}
	if got := messageType(msg); got != "link" {
		t.Errorf("msg_type = %q, want link", got)
	}
}

func TestAnalyzeMessageContentURLAndTextLinkEntities(t *testing.T) {
	const url = "https://example.com/post?utm_source=a"
	plain := linkMessage(url, 811)

	withURLEntity := linkMessage(url, 812)
	withURLEntity.Entities = telebot.Entities{{
		Type:   telebot.EntityURL,
		Offset: 0,
		Length: utf16Length(url),
	}}

	textLink := linkMessage("点击这里", 813)
	textLink.Entities = telebot.Entities{{
		Type:   telebot.EntityTextLink,
		Offset: 0,
		Length: utf16Length("点击这里"),
		URL:    url,
	}}

	otherAnchor := linkMessage("click here", 814)
	otherAnchor.Entities = telebot.Entities{{
		Type:   telebot.EntityTextLink,
		Offset: 0,
		Length: utf16Length("click here"),
		URL:    url,
	}}

	want := AnalyzeMessageContent(plain, contentTestSecret)
	if want.Kind != "link" {
		t.Fatalf("plain content = %+v, want link", want)
	}
	for name, msg := range map[string]*telebot.Message{
		"url entity":   withURLEntity,
		"text_link":    textLink,
		"other anchor": otherAnchor,
	} {
		got := AnalyzeMessageContent(msg, contentTestSecret)
		if got.Key != want.Key {
			t.Errorf("%s key = %q, want %q", name, got.Key, want.Key)
		}
		if got.Kind != "link" || got.KeyType != "链接" {
			t.Errorf("%s content = %+v, want link/链接", name, got)
		}
	}
}

func TestAnalyzeMessageContentMultipleLinksKeepOrder(t *testing.T) {
	const first = "https://a.example/one https://b.example/two"
	const second = "https://b.example/two\nhttps://a.example/one"

	ordered := AnalyzeMessageContent(linkMessage(first, 821), contentTestSecret)
	same := AnalyzeMessageContent(linkMessage(first, 822), contentTestSecret)
	reversed := AnalyzeMessageContent(linkMessage(second, 823), contentTestSecret)

	if ordered.Key == "" || same.Key != ordered.Key {
		t.Errorf("same-order keys = %q / %q, want equal", ordered.Key, same.Key)
	}
	if reversed.Key == ordered.Key {
		t.Errorf("reversed key = %q, want different from ordered key", reversed.Key)
	}
}

func TestAnalyzeMessageContentLinkCanonicalizationIsConservative(t *testing.T) {
	const raw = "HTTPS://Example.COM/A/b?utm_source=x&b=2#Frag"
	const lowered = "https://example.com/A/b?utm_source=x&b=2#Frag"

	same := []string{
		raw,
		lowered,
	}
	base := AnalyzeMessageContent(linkMessage(same[0], 831), contentTestSecret)
	if base.Key == "" {
		t.Fatalf("content = %+v, want link key", base)
	}
	for _, text := range same[1:] {
		got := AnalyzeMessageContent(linkMessage(text, 832), contentTestSecret)
		if got.Key != base.Key {
			t.Errorf("key for %q = %q, want %q", text, got.Key, base.Key)
		}
	}

	different := []string{
		"https://example.com/A/b?utm_source=x&b=2#Frag/",
		"http://example.com/A/b?utm_source=x&b=2#Frag",
		"https://example.com/A/b?b=2&utm_source=x#Frag",
		"https://www.example.com/A/b?utm_source=x&b=2#Frag",
	}
	for i, text := range different {
		got := AnalyzeMessageContent(linkMessage(text, 833+i), contentTestSecret)
		if got.Key == base.Key {
			t.Errorf("key for %q = base key, want different (conservative normalization)", text)
		}
	}
}

func TestAnalyzeMessageContentLinkWithOtherTextStaysText(t *testing.T) {
	const text = "请看这个内容真的很有意思 https://example.com/a/b"
	content := AnalyzeMessageContent(linkMessage(text, 841), contentTestSecret)
	if content.Kind != "text" || content.KeyType != "文本" {
		t.Errorf("content = %+v, want text/文本", content)
	}
	if want := textKeyFor(contentTestSecret, text); content.Key != want {
		t.Errorf("key = %q, want existing text key %q", content.Key, want)
	}
}

func TestAnalyzeMessageContentShortTextWithLinkStillSkipped(t *testing.T) {
	const text = "看 https://example.com/a/b"
	content := AnalyzeMessageContent(linkMessage(text, 851), contentTestSecret)
	if content.Key != "" {
		t.Errorf("key = %q, want empty", content.Key)
	}
	if content.SkipDecision != DecisionShortTextSkipped {
		t.Errorf("decision = %q, want %q", content.SkipDecision, DecisionShortTextSkipped)
	}
}

func TestProcessorCountsPureLinks(t *testing.T) {
	p, _, db := setupProcessor(t)
	buf := captureDecisionLog(t, p)
	replies := &replyRecorder{}
	const link = "https://example.com/a/b?x=1"

	for i := 0; i < 4; i++ {
		if err := p.ProcessMessage(linkMessage(link, 10+i), "trace", replies.reply); err != nil {
			t.Fatalf("ProcessMessage(%d) error = %v", i, err)
		}
	}

	lines := readDecisions(t, buf)
	if len(lines) != 4 {
		t.Fatalf("decision lines = %d, want 4: %s", len(lines), buf.String())
	}
	wantDecisions := []string{
		DecisionPairedSilent,
		DecisionAlerted,
		DecisionPairedSilent,
		DecisionAlerted,
	}
	for i, want := range wantDecisions {
		if lines[i].Decision != want {
			t.Errorf("decision[%d] = %q, want %q", i, lines[i].Decision, want)
		}
		if lines[i].MsgType != "link" {
			t.Errorf("msg_type[%d] = %q, want link", i, lines[i].MsgType)
		}
		if len(lines[i].KeyHash) != 8 {
			t.Errorf("key_hash[%d] = %q, want 8 hex chars", i, lines[i].KeyHash)
		}
		if lines[i].KeyHash != lines[0].KeyHash {
			t.Errorf("key_hash[%d] = %q, want %q", i, lines[i].KeyHash, lines[0].KeyHash)
		}
	}
	if got := len(replies.snapshot()); got != 2 {
		t.Errorf("replies = %d, want 2 (second and fourth occurrences)", got)
	}

	var records, logs int64
	if err := db.Model(&models.ForwardRecord{}).Count(&records).Error; err != nil {
		t.Fatalf("count records: %v", err)
	}
	if err := db.Model(&models.ForwardLog{}).Count(&logs).Error; err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if records != 1 || logs != 4 {
		t.Errorf("records=%d logs=%d, want 1/4", records, logs)
	}
	var record models.ForwardRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if record.ForwardCount != 4 {
		t.Errorf("ForwardCount = %d, want 4 (every pure link occurrence counts)", record.ForwardCount)
	}
	var stats int64
	if err := db.Model(&models.UserStats{}).Count(&stats).Error; err != nil {
		t.Fatalf("count user stats: %v", err)
	}
	if stats != 1 {
		t.Errorf("user_stats rows = %d, want 1", stats)
	}

	if strings.Contains(buf.String(), "example.com") {
		t.Errorf("decision log contains the raw URL: %s", buf.String())
	}
}

func TestProcessorDifferentLinksAreIndependent(t *testing.T) {
	p, _, db := setupProcessor(t)
	replies := &replyRecorder{}

	links := []string{
		"https://example.com/one",
		"https://example.com/two",
	}
	for i, link := range links {
		for j := 0; j < 2; j++ {
			if err := p.ProcessMessage(linkMessage(link, 20+i*10+j), "trace", replies.reply); err != nil {
				t.Fatalf("ProcessMessage(%q) error = %v", link, err)
			}
		}
	}
	if got := len(replies.snapshot()); got != 2 {
		t.Errorf("replies = %d, want 2 (one alert per link)", got)
	}

	var records int64
	if err := db.Model(&models.ForwardRecord{}).Count(&records).Error; err != nil {
		t.Fatalf("count records: %v", err)
	}
	if records != 2 {
		t.Errorf("records = %d, want 2", records)
	}
}
