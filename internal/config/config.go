package config

import (
	"os"
	"time"

	"github.com/joho/godotenv"
)

var (
	BotToken string
	DBPath   string
	ProxyURL string
	// DedupSecret 用于对文本内容做 HMAC-SHA256。未配置时回退到 BOT_TOKEN，
	// 确保永远不会以无密钥方式存储文本哈希。
	DedupSecret string
	// StatsTZ 是排行榜周界计算使用的时区。
	StatsTZ string
)

func Init() {
	godotenv.Load()

	BotToken = os.Getenv("BOT_TOKEN")

	DBPath = os.Getenv("DB_PATH")
	if DBPath == "" {
		DBPath = "bot.db"
	}

	ProxyURL = os.Getenv("PROXY_URL")

	DedupSecret = os.Getenv("DEDUP_SECRET")
	if DedupSecret == "" {
		DedupSecret = BotToken
	}

	StatsTZ = os.Getenv("STATS_TZ")
	if StatsTZ == "" {
		StatsTZ = "Asia/Shanghai"
	}
}

// StatsLocation 返回排行榜使用的时区。
// 容器缺少 tzdata 时，对 Asia/Shanghai 回退到固定的 UTC+8，其他时区回退到 UTC。
func StatsLocation() *time.Location {
	return LoadLocation(StatsTZ)
}

// LoadLocation 按名称加载时区，失败时提供可预测的回退。
func LoadLocation(name string) *time.Location {
	if name == "" {
		name = "Asia/Shanghai"
	}
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	if name == "Asia/Shanghai" || name == "CST" {
		return time.FixedZone("CST", 8*60*60)
	}
	return time.UTC
}
