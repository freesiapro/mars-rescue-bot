package bot

import (
	"sync"
	"time"
)

// Cooldown 是进程内的群级回复冷却器。
// 当前项目是单实例 SQLite 部署，内存实现足够，且避免每次提醒都写数据库。
type Cooldown struct {
	mu   sync.Mutex
	last map[int64]time.Time
	now  func() time.Time
}

func NewCooldown() *Cooldown {
	return &Cooldown{
		last: make(map[int64]time.Time),
		now:  time.Now,
	}
}

// Allow 报告本次回复是否允许，并在允许时记录本次回复时间。
// cooldown 为 0 时每条消息都允许。
func (c *Cooldown) Allow(chatID int64, cooldown time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if cooldown > 0 {
		if last, ok := c.last[chatID]; ok && now.Sub(last) < cooldown {
			return false
		}
	}
	c.last[chatID] = now
	return true
}
