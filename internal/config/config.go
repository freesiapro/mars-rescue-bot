package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const DefaultDataRetentionDays = 180

var (
	BotToken string
	DBPath   string
	ProxyURL string
	// DedupSecret 用于对文本内容做 HMAC-SHA256。未配置时回退到 BOT_TOKEN，
	// 确保永远不会以无密钥方式存储文本哈希。
	DedupSecret string
	// StatsTZ 是排行榜周界计算使用的时区。
	StatsTZ string
	// DataRetentionDays 是保留数据的天数，超过该天数的流水与记录会被清理。
	DataRetentionDays int
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

	DataRetentionDays = parseRetentionDays(os.Getenv("DATA_RETENTION_DAYS"))
}

// parseRetentionDays 解析 DATA_RETENTION_DAYS；缺失、非法或非正数时使用默认值。
func parseRetentionDays(raw string) int {
	days, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || days <= 0 {
		return DefaultDataRetentionDays
	}
	return days
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
