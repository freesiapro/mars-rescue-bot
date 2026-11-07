// Package names 在展示时解析用户姓名，不把姓名持久化到数据库。
package names

import (
	"fmt"
	"sync"
	"time"

	"gopkg.in/telebot.v4"
)

// MemberProvider 抽象 Telegram 的群成员查询，便于测试。
// *telebot.Bot 实现了该接口。
type MemberProvider interface {
	ChatMemberOf(chat telebot.Recipient, user telebot.Recipient) (*telebot.ChatMember, error)
}

type cacheEntry struct {
	name    string
	expires time.Time
}

// Resolver 维护进程内姓名缓存，并在缓存未命中时通过 getChatMember 兜底。
// 姓名不会写入数据库；查询失败时返回“群友 <id>”。
type Resolver struct {
	mu       sync.RWMutex
	cache    map[int64]cacheEntry
	provider MemberProvider
	ttl      time.Duration
	now      func() time.Time
}

func New(provider MemberProvider) *Resolver {
	return &Resolver{
		cache:    make(map[int64]cacheEntry),
		provider: provider,
		ttl:      10 * time.Minute,
		now:      time.Now,
	}
}

// Observe 用消息里的发送者信息更新缓存。只保存在内存中。
func (r *Resolver) Observe(user *telebot.User) {
	if r == nil || user == nil || user.ID == 0 {
		return
	}
	name := displayName(user)
	if name == "" {
		return
	}

	r.mu.Lock()
	r.cache[user.ID] = cacheEntry{name: name, expires: r.now().Add(r.ttl)}
	r.mu.Unlock()
}

// Name 返回 userID 的展示名。
// 优先使用未过期的缓存，其次调用 getChatMember；都失败时返回“群友 <id>”。
func (r *Resolver) Name(chat *telebot.Chat, userID int64) string {
	if userID <= 0 {
		return ""
	}
	if r == nil {
		return fallbackName(userID)
	}

	r.mu.RLock()
	entry, ok := r.cache[userID]
	r.mu.RUnlock()
	if ok && r.now().Before(entry.expires) {
		return entry.name
	}

	if r.provider != nil && chat != nil {
		member, err := r.provider.ChatMemberOf(chat, &telebot.User{ID: userID})
		if err == nil && member != nil && member.User != nil {
			if name := displayName(member.User); name != "" {
				r.mu.Lock()
				r.cache[userID] = cacheEntry{name: name, expires: r.now().Add(r.ttl)}
				r.mu.Unlock()
				return name
			}
		}
	}

	return fallbackName(userID)
}

func displayName(user *telebot.User) string {
	if user == nil {
		return ""
	}
	if user.FirstName != "" {
		return user.FirstName
	}
	return user.Username
}

func fallbackName(userID int64) string {
	return fmt.Sprintf("群友 %d", userID)
}
