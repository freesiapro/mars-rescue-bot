// Package names 在展示时解析用户姓名，不把姓名持久化到数据库。
package names

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/unicode/norm"
	"gopkg.in/telebot.v4"
)

// defaultMemberCacheTTL 是“原作者是否仍在本群”的内存缓存时间。
// 只为少打 getChatMember；过期后重新查询。不写入数据库。
const defaultMemberCacheTTL = time.Minute

var errMemberLookup = errors.New("member lookup unavailable")

// MemberProvider 抽象 Telegram 的群成员查询，便于测试。
// *telebot.Bot 实现了该接口。
type MemberProvider interface {
	ChatMemberOf(chat telebot.Recipient, user telebot.Recipient) (*telebot.ChatMember, error)
}

type cacheEntry struct {
	name    string
	expires time.Time
}

type memberCacheKey struct {
	chatID int64
	userID int64
}

type memberCacheEntry struct {
	inGroup bool
	expires time.Time
}

// Resolver 维护进程内姓名缓存，并在缓存未命中时通过 getChatMember 兜底。
// 姓名不会写入数据库；查询失败时返回“用户 <id>”。
// 成员是否仍在群内也走同一套查询，单独短时缓存，同样不落库。
type Resolver struct {
	mu          sync.RWMutex
	cache       map[int64]cacheEntry
	memberCache map[memberCacheKey]memberCacheEntry
	provider    MemberProvider
	ttl         time.Duration
	memberTTL   time.Duration
	now         func() time.Time
}

func New(provider MemberProvider) *Resolver {
	return &Resolver{
		cache:       make(map[int64]cacheEntry),
		memberCache: make(map[memberCacheKey]memberCacheEntry),
		provider:    provider,
		ttl:         10 * time.Minute,
		memberTTL:   defaultMemberCacheTTL,
		now:         time.Now,
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
// 优先使用未过期的缓存，其次调用 getChatMember；都失败时返回“用户 <id>”。
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

// NormalizeName 把姓名转成可比较的形式：Unicode NFC、去首尾空白、合并连续空白。
// 只用于“隐藏来源转发”的显示名比对，不写入数据库或日志。
func NormalizeName(name string) string {
	return strings.Join(strings.Fields(norm.NFC.String(name)), " ")
}

// MemberName 返回 userID 在 chat 中的当前显示名（first_name + " " + last_name）。
// 用户不在群内、缺少姓名或查询失败时返回 error；不做缓存，确保读到当前姓名。
func (r *Resolver) MemberName(chat *telebot.Chat, userID int64) (string, error) {
	if r == nil || r.provider == nil || chat == nil || userID <= 0 {
		return "", errMemberLookup
	}

	member, err := r.provider.ChatMemberOf(chat, &telebot.User{ID: userID})
	if err != nil {
		return "", err
	}
	if member == nil || !memberInGroup(member.Role) || member.User == nil {
		return "", errMemberLookup
	}
	name := fullName(member.User)
	if name == "" {
		return "", errMemberLookup
	}
	return name, nil
}

func fullName(user *telebot.User) string {
	if user == nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimSpace(user.FirstName) + " " + strings.TrimSpace(user.LastName))
}

func fallbackName(userID int64) string {
	return fmt.Sprintf("用户 %d", userID)
}

// InGroup 报告 userID 现在是否还在 chat 里。
// creator、administrator、member、restricted 视为仍在群内。
// left、kicked、其他状态，以及查询失败，都返回“无法确认在群内”：
// 调用方应据此不跳过转发。成功结果缓存 memberTTL；失败不缓存，下次重试。
func (r *Resolver) InGroup(chat *telebot.Chat, userID int64) (bool, error) {
	if r == nil || r.provider == nil || chat == nil || userID <= 0 {
		return false, errMemberLookup
	}

	key := memberCacheKey{chatID: chat.ID, userID: userID}
	r.mu.RLock()
	entry, ok := r.memberCache[key]
	r.mu.RUnlock()
	if ok && r.now().Before(entry.expires) {
		return entry.inGroup, nil
	}

	member, err := r.provider.ChatMemberOf(chat, &telebot.User{ID: userID})
	if err != nil {
		return false, err
	}
	inGroup := member != nil && memberInGroup(member.Role)

	r.mu.Lock()
	r.memberCache[key] = memberCacheEntry{inGroup: inGroup, expires: r.now().Add(r.memberTTL)}
	r.mu.Unlock()
	return inGroup, nil
}

func memberInGroup(role telebot.MemberStatus) bool {
	switch role {
	case telebot.Creator, telebot.Administrator, telebot.Member, telebot.Restricted:
		return true
	default:
		return false
	}
}
