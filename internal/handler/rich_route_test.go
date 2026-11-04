package handler

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"gopkg.in/telebot.v4"
	"mars-rescue-bot/internal/bot"
	"mars-rescue-bot/internal/logger"
	"mars-rescue-bot/internal/models"
	"mars-rescue-bot/internal/names"
	"mars-rescue-bot/internal/repo"
	"mars-rescue-bot/internal/testutil"
)

// oneUpdatePoller 是 MiddlewarePoller 的测试输入：只发一条更新，然后等待停止。
type oneUpdatePoller struct {
	update telebot.Update
}

func (p *oneUpdatePoller) Poll(_ *telebot.Bot, updates chan telebot.Update, stop chan struct{}) {
	select {
	case updates <- p.update:
	case <-stop:
		return
	}
	<-stop
}

// TestRichMessagesReachProcessorThroughMiddlewarePoller 用真实注册函数
// MessageHandler.Register，验证直接富文本和转发富文本都经过
// MiddlewarePoller 的 Filter、telebot ProcessUpdate，最终只调用一次
// Processor.Handle（一次消息只产生一条 dedup decision 和一条收到消息）。
func TestRichMessagesReachProcessorThroughMiddlewarePoller(t *testing.T) {
	data, err := os.ReadFile("../bot/testdata/rich_message_sample.json")
	if err != nil {
		t.Fatalf("read rich sample: %v", err)
	}

	tests := []struct {
		name   string
		direct bool
	}{
		{name: "direct", direct: true},
		{name: "forwarded", direct: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := captureDefaultLog(t)

			db := testutil.SetupDB(t,
				&models.ForwardRecord{},
				&models.ForwardLog{},
				&models.UserStats{},
				&models.ChatSettings{},
				&models.ChatIgnoredUser{},
			)
			p := bot.NewProcessor(repo.New(db), hywTestSecret, names.New(nil), time.UTC)

			b, err := telebot.NewBot(telebot.Settings{
				Token:       "123456:TEST",
				Offline:     true,
				Synchronous: true,
			})
			if err != nil {
				t.Fatalf("new offline bot: %v", err)
			}
			b.Use(logger.TraceMiddleware)
			NewMessageHandler(p).Register(b)

			var msg telebot.Message
			if err := json.Unmarshal(data, &msg); err != nil {
				t.Fatalf("decode sample: %v", err)
			}
			msg.Chat = &telebot.Chat{ID: hywTestChatID, Type: telebot.ChatSuperGroup}
			msg.Sender = &telebot.User{ID: 1000000001, FirstName: "Sample"}
			if tt.direct {
				msg.Origin = nil
				msg.OriginalSender = nil
				msg.OriginalChat = nil
			}

			middlewarePoller := telebot.NewMiddlewarePoller(
				&oneUpdatePoller{update: telebot.Update{Message: &msg}},
				bot.NormalizeRichMessageUpdate,
			)
			out := make(chan telebot.Update, 1)
			stop := make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(done)
				middlewarePoller.Poll(b, out, stop)
			}()
			defer func() {
				close(stop)
				<-done
			}()

			select {
			case normalized := <-out:
				if normalized.Message == nil || normalized.Message.Text != " " {
					t.Fatalf("normalized message Text = %+v, want routing placeholder", normalized.Message)
				}
				if normalized.Message.RichMessage == nil {
					t.Fatal("normalized message lost RichMessage")
				}
				b.ProcessUpdate(normalized)
			case <-time.After(2 * time.Second):
				t.Fatal("MiddlewarePoller did not deliver the update")
			}

			lines := parseHYWLogs(t, buf)
			decisions := 0
			received := 0
			for _, line := range lines {
				switch line.Msg {
				case "dedup decision":
					decisions++
					if line.Decision != bot.DecisionPairedSilent {
						t.Errorf("decision = %q, want %q", line.Decision, bot.DecisionPairedSilent)
					}
					if line.MsgType != "rich_message" {
						t.Errorf("msg_type = %q, want rich_message", line.MsgType)
					}
				case "收到消息":
					received++
				}
			}
			if decisions != 1 {
				t.Errorf("dedup decision lines = %d, want exactly 1: %s", decisions, buf.String())
			}
			if received != 1 {
				t.Errorf("received message lines = %d, want exactly 1: %s", received, buf.String())
			}
		})
	}
}
