package bot

import (
	"fmt"
	"testing"

	"gopkg.in/telebot.v3"
)

// newOfflineBot 创建一个不访问 Telegram 的 bot，用于在代码层验证 telebot 行为。
func newOfflineBot(t *testing.T) *telebot.Bot {
	t.Helper()
	b, err := telebot.NewBot(telebot.Settings{
		Token:       "123456:TEST",
		Offline:     true,
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("new offline bot: %v", err)
	}
	return b
}

func commandUpdate(text string, userID int64) telebot.Update {
	return telebot.Update{Message: &telebot.Message{
		Text:   text,
		Sender: &telebot.User{ID: userID, FirstName: "A"},
		Chat:   &telebot.Chat{ID: -1001234567890, Type: telebot.ChatSuperGroup},
	}}
}

// TestTelebotMediaRouting 验证 OnMedia 会接收照片/视频/文件等媒体消息，
// 而 OnText 只接收纯文本。
func TestTelebotMediaRouting(t *testing.T) {
	b := newOfflineBot(t)

	var events []string
	b.Handle(telebot.OnText, func(c telebot.Context) error {
		events = append(events, "text")
		return nil
	})
	b.Handle(telebot.OnMedia, func(c telebot.Context) error {
		events = append(events, "media")
		return nil
	})

	chat := &telebot.Chat{ID: -1001234567890, Type: telebot.ChatSuperGroup}
	sender := &telebot.User{ID: 7, FirstName: "A"}

	tests := []struct {
		name string
		msg  *telebot.Message
		want string
	}{
		{
			name: "photo with caption",
			msg: &telebot.Message{ID: 1, Chat: chat, Sender: sender,
				Photo: &telebot.Photo{File: telebot.File{FileID: "photo"}}, Caption: "look"},
			want: "media",
		},
		{
			name: "document",
			msg: &telebot.Message{ID: 2, Chat: chat, Sender: sender,
				Document: &telebot.Document{File: telebot.File{FileID: "doc"}}},
			want: "media",
		},
		{
			name: "plain text",
			msg:  &telebot.Message{ID: 3, Chat: chat, Sender: sender, Text: "hello"},
			want: "text",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events = nil
			b.ProcessUpdate(telebot.Update{Message: tt.msg})
			if len(events) != 1 || events[0] != tt.want {
				t.Fatalf("events = %#v, want [%q]", events, tt.want)
			}
		})
	}
}

// TestTelebotCommandRouting 验证 telebot v3 的路由与参数解析行为：
//   - 已注册命令进入命令处理器，Args() 返回命令后的参数；
//   - 带 @bot 后缀的命令在 Me.Username 已知时同样命中；
//   - 普通文本与未注册命令进入 OnText 处理器。
func TestTelebotCommandRouting(t *testing.T) {
	b := newOfflineBot(t)
	b.Me.Username = "my_bot"

	var events []string
	b.Handle("/mars_rank", func(c telebot.Context) error {
		events = append(events, fmt.Sprintf("cmd args=%v sender=%d chat=%d", c.Args(), c.Sender().ID, c.Chat().ID))
		return nil
	})
	b.Handle(telebot.OnText, func(c telebot.Context) error {
		events = append(events, "text:"+c.Text())
		return nil
	})

	tests := []struct {
		name string
		text string
		want string
	}{
		{"registered command with args", "/mars_rank 30", "cmd args=[30] sender=7 chat=-1001234567890"},
		{"registered command at bot", "/mars_rank@my_bot 5", "cmd args=[5] sender=7 chat=-1001234567890"},
		{"plain text", "hello world", "text:hello world"},
		{"unknown command falls through to OnText", "/unknown_cmd x", "text:/unknown_cmd x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events = nil
			b.ProcessUpdate(commandUpdate(tt.text, 7))
			if len(events) != 1 || events[0] != tt.want {
				t.Fatalf("events = %#v, want [%q]", events, tt.want)
			}
		})
	}
}
