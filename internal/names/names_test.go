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
	if got := r.Name(nil, 7); got != "群友 7" {
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

	if got := r.Name(&telebot.Chat{ID: -100}, 7); got != "群友 7" {
		t.Errorf("Name() = %q, want fallback", got)
	}
}

func TestResolverNilAndInvalidInputs(t *testing.T) {
	var r *Resolver
	if got := r.Name(nil, 7); got != "群友 7" {
		t.Errorf("nil resolver Name() = %q, want fallback", got)
	}
	r.Observe(nil)
	r.Observe(&telebot.User{ID: 7})

	if got := r.Name(nil, 0); got != "" {
		t.Errorf("Name(0) = %q, want empty", got)
	}
}
