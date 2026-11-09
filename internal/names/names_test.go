package names

import (
	"errors"
	"testing"
	"time"

	"gopkg.in/telebot.v4"
)

type fakeProvider struct {
	member *telebot.ChatMember
	err    error
	calls  int
}

func (f *fakeProvider) ChatMemberOf(chat telebot.Recipient, user telebot.Recipient) (*telebot.ChatMember, error) {
	f.calls++
	return f.member, f.err
}

func TestResolverObserveAndName(t *testing.T) {
	r := New(nil)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }

	r.Observe(&telebot.User{ID: 7, FirstName: "Alice", Username: "alice"})
	if got := r.Name(nil, 7); got != "Alice" {
		t.Errorf("Name() = %q, want Alice", got)
	}

	now = now.Add(11 * time.Minute)
	if got := r.Name(nil, 7); got != "用户 7" {
		t.Errorf("Name() after expiry = %q, want fallback", got)
	}
}

func TestResolverUsesUsernameWhenFirstNameMissing(t *testing.T) {
	r := New(nil)
	r.Observe(&telebot.User{ID: 7, Username: "alice"})
	if got := r.Name(nil, 7); got != "alice" {
		t.Errorf("Name() = %q, want alice", got)
	}
}

func TestResolverFetchesFromProvider(t *testing.T) {
	provider := &fakeProvider{member: &telebot.ChatMember{User: &telebot.User{ID: 7, FirstName: "Fetched"}}}
	r := New(provider)
	chat := &telebot.Chat{ID: -100, Type: telebot.ChatSuperGroup}

	if got := r.Name(chat, 7); got != "Fetched" {
		t.Errorf("Name() = %q, want Fetched", got)
	}
	if provider.calls != 1 {
		t.Errorf("provider calls = %d, want 1", provider.calls)
	}

	// 第二次命中缓存，不再请求 Telegram。
	if got := r.Name(chat, 7); got != "Fetched" {
		t.Errorf("cached Name() = %q, want Fetched", got)
	}
	if provider.calls != 1 {
		t.Errorf("provider calls = %d, want 1 after cache hit", provider.calls)
	}
}

func TestResolverFallsBackOnProviderError(t *testing.T) {
	provider := &fakeProvider{err: errors.New("telegram unavailable")}
	r := New(provider)

	if got := r.Name(&telebot.Chat{ID: -100}, 7); got != "用户 7" {
		t.Errorf("Name() = %q, want fallback", got)
	}
}

func TestResolverNilAndInvalidInputs(t *testing.T) {
	var r *Resolver
	if got := r.Name(nil, 7); got != "用户 7" {
		t.Errorf("nil resolver Name() = %q, want fallback", got)
	}
	r.Observe(nil)
	r.Observe(&telebot.User{ID: 7})

	if got := r.Name(nil, 0); got != "" {
		t.Errorf("Name(0) = %q, want empty", got)
	}
}

func TestResolverInGroup(t *testing.T) {
	chat := &telebot.Chat{ID: -100, Type: telebot.ChatSuperGroup}
	other := &telebot.Chat{ID: -200, Type: telebot.ChatSuperGroup}

	for _, tt := range []struct {
		role telebot.MemberStatus
		want bool
	}{
		{telebot.Creator, true},
		{telebot.Administrator, true},
		{telebot.Member, true},
		{telebot.Restricted, true},
		{telebot.Left, false},
		{telebot.Kicked, false},
		{"", false},
	} {
		t.Run(string(tt.role), func(t *testing.T) {
			provider := &fakeProvider{member: &telebot.ChatMember{Role: tt.role}}
			resolver := New(provider)
			got, err := resolver.InGroup(chat, 7)
			if err != nil {
				t.Fatalf("InGroup() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("InGroup() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("cache is per chat and expires", func(t *testing.T) {
		provider := &fakeProvider{member: &telebot.ChatMember{Role: telebot.Member}}
		resolver := New(provider)
		now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
		resolver.now = func() time.Time { return now }

		if _, err := resolver.InGroup(chat, 7); err != nil {
			t.Fatalf("first InGroup() error = %v", err)
		}
		if _, err := resolver.InGroup(chat, 7); err != nil {
			t.Fatalf("cached InGroup() error = %v", err)
		}
		if provider.calls != 1 {
			t.Fatalf("calls = %d, want 1 while cache is fresh", provider.calls)
		}
		if _, err := resolver.InGroup(other, 7); err != nil {
			t.Fatalf("other chat InGroup() error = %v", err)
		}
		if provider.calls != 2 {
			t.Fatalf("calls = %d, want 2 for a different chat", provider.calls)
		}
		now = now.Add(defaultMemberCacheTTL + time.Second)
		if _, err := resolver.InGroup(chat, 7); err != nil {
			t.Fatalf("expired InGroup() error = %v", err)
		}
		if provider.calls != 3 {
			t.Fatalf("calls = %d, want 3 after expiry", provider.calls)
		}
	})

	t.Run("lookup error is not cached", func(t *testing.T) {
		provider := &fakeProvider{err: errors.New("telegram down")}
		resolver := New(provider)
		if _, err := resolver.InGroup(chat, 7); err == nil {
			t.Fatal("InGroup() error = nil, want lookup failure")
		}
		provider.err = nil
		provider.member = &telebot.ChatMember{Role: telebot.Member}
		got, err := resolver.InGroup(chat, 7)
		if err != nil {
			t.Fatalf("retry InGroup() error = %v", err)
		}
		if !got {
			t.Fatal("retry InGroup() = false, want true")
		}
		if provider.calls != 2 {
			t.Fatalf("calls = %d, want 2", provider.calls)
		}
	})

	t.Run("nil member is not in group", func(t *testing.T) {
		provider := &fakeProvider{}
		resolver := New(provider)
		got, err := resolver.InGroup(chat, 7)
		if err != nil {
			t.Fatalf("InGroup() error = %v", err)
		}
		if got {
			t.Fatal("InGroup() = true, want false")
		}
		if _, err := resolver.InGroup(chat, 7); err != nil {
			t.Fatalf("cached InGroup() error = %v", err)
		}
		if provider.calls != 1 {
			t.Fatalf("calls = %d, want 1", provider.calls)
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		if _, err := New(nil).InGroup(chat, 7); err == nil {
			t.Fatal("nil provider error = nil")
		}
		var resolver *Resolver
		if _, err := resolver.InGroup(chat, 7); err == nil {
			t.Fatal("nil resolver error = nil")
		}
		if _, err := New(&fakeProvider{member: &telebot.ChatMember{Role: telebot.Member}}).InGroup(nil, 7); err == nil {
			t.Fatal("nil chat error = nil")
		}
	})
}
