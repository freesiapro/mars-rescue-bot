package handler

import (
	"errors"
	"testing"

	"gopkg.in/telebot.v3"
)

type fakeChatMemberProvider struct {
	member *telebot.ChatMember
	err    error
	calls  int
}

func (f *fakeChatMemberProvider) ChatMemberOf(chat telebot.Recipient, user telebot.Recipient) (*telebot.ChatMember, error) {
	f.calls++
	return f.member, f.err
}

func TestIsAdminRole(t *testing.T) {
	if !IsAdminRole(telebot.Creator) {
		t.Error("Creator should be an admin role")
	}
	if !IsAdminRole(telebot.Administrator) {
		t.Error("Administrator should be an admin role")
	}
	for _, role := range []telebot.MemberStatus{telebot.Member, telebot.Restricted, telebot.Left, telebot.Kicked} {
		if IsAdminRole(role) {
			t.Errorf("role %q should not be an admin role", role)
		}
	}
}

func TestAdminCheckerIsAdmin(t *testing.T) {
	chat := &telebot.Chat{ID: -100, Type: telebot.ChatSuperGroup}
	user := &telebot.User{ID: 7}

	tests := []struct {
		name   string
		member *telebot.ChatMember
		err    error
		want   bool
	}{
		{"creator", &telebot.ChatMember{Role: telebot.Creator}, nil, true},
		{"administrator", &telebot.ChatMember{Role: telebot.Administrator}, nil, true},
		{"member", &telebot.ChatMember{Role: telebot.Member}, nil, false},
		{"nil member", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &fakeChatMemberProvider{member: tt.member, err: tt.err}
			checker := NewAdminChecker(provider)
			got, err := checker.IsAdmin(chat, user)
			if err != nil {
				t.Fatalf("IsAdmin() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("IsAdmin() = %v, want %v", got, tt.want)
			}
			if provider.calls != 1 {
				t.Errorf("provider calls = %d, want 1", provider.calls)
			}
		})
	}
}

func TestAdminCheckerPropagatesError(t *testing.T) {
	wantErr := errors.New("telegram unavailable")
	checker := NewAdminChecker(&fakeChatMemberProvider{err: wantErr})
	_, err := checker.IsAdmin(&telebot.Chat{ID: -100}, &telebot.User{ID: 7})
	if !errors.Is(err, wantErr) {
		t.Fatalf("IsAdmin() error = %v, want %v", err, wantErr)
	}
}

func TestAdminCheckerMissingInputs(t *testing.T) {
	provider := &fakeChatMemberProvider{member: &telebot.ChatMember{Role: telebot.Creator}}
	checker := NewAdminChecker(provider)

	if ok, err := checker.IsAdmin(nil, &telebot.User{ID: 7}); err != nil || ok {
		t.Errorf("nil chat: ok=%v err=%v, want false/nil", ok, err)
	}
	if ok, err := checker.IsAdmin(&telebot.Chat{ID: -100}, nil); err != nil || ok {
		t.Errorf("nil user: ok=%v err=%v, want false/nil", ok, err)
	}
	if provider.calls != 0 {
		t.Errorf("provider calls = %d, want 0", provider.calls)
	}
}
