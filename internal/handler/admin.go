package handler

import (
	"errors"
	"fmt"

	"gopkg.in/telebot.v3"
)

// ChatMemberProvider 抽象 telebot 的管理员查询，便于测试。
// *telebot.Bot 实现了该接口。
type ChatMemberProvider interface {
	ChatMemberOf(chat telebot.Recipient, user telebot.Recipient) (*telebot.ChatMember, error)
}

// AdminChecker 负责群管理员/群主校验。
type AdminChecker struct {
	provider ChatMemberProvider
}

func NewAdminChecker(provider ChatMemberProvider) *AdminChecker {
	return &AdminChecker{provider: provider}
}

// IsAdmin 报告 user 是否是 chat 的管理员或群主。
// 查询失败时返回错误，由调用方决定如何提示。
func (a *AdminChecker) IsAdmin(chat *telebot.Chat, user *telebot.User) (bool, error) {
	if chat == nil || user == nil {
		return false, nil
	}
	if a == nil || a.provider == nil {
		return false, errors.New("chat member provider is not configured")
	}

	member, err := a.provider.ChatMemberOf(chat, user)
	if err != nil {
		return false, fmt.Errorf("query chat member: %w", err)
	}
	if member == nil {
		return false, nil
	}
	return IsAdminRole(member.Role), nil
}

// IsAdminRole 判断 Telegram 群成员角色是否为管理员或群主。
func IsAdminRole(role telebot.MemberStatus) bool {
	return role == telebot.Administrator || role == telebot.Creator
}
