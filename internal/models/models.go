package models

import "time"

// ForwardRecord 记录群聊中被重复发送的内容。
type ForwardRecord struct {
	ID               uint64 `gorm:"primaryKey"`
	ChatID           int64  `gorm:"notNull;index:idx_chat_key,unique"`
	ContentKey       string `gorm:"notNull;index:idx_chat_key,unique"` // "channel:123:456" / "text:abc..."
	ForwardCount     int    `gorm:"default:1"`
	FirstTargetMsgID int
	// FirstSenderID 是内容首次出现时的发送者。0 表示历史记录或未知。
	FirstSenderID int64 `gorm:"default:0"`
	// PendingPair 表示当前是否存在一个尚未配对的出现：
	//   1/true  = 有一次出现正在等待下一次相同内容来组成一对；
	//   0/false = 上一次出现已经配对并发出提醒，下一次出现将开启新的一对。
	// 使用指针是为了兼容升级前已有的行：AutoMigrate 新增列时旧行为 NULL，
	// 读取时按 ForwardCount 的奇偶推导（旧数据每次出现都会自增计数，
	// 因此计数为奇数说明还有一个未配对的出现）。新写入的行总是显式赋值。
	PendingPair *bool
	CreatedAt   time.Time `gorm:"autoCreateTime"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime"`
}

func (ForwardRecord) TableName() string {
	return "forward_records"
}

// ForwardLog 记录每次具体的转发/发送行为（用于排行榜和复读检测）。
type ForwardLog struct {
	ID          uint64    `gorm:"primaryKey"`
	RecordID    uint64    `gorm:"notNull;index;index:idx_record_sender"`
	SenderID    int64     `gorm:"notNull;index:idx_record_sender"`
	TargetMsgID int       `gorm:"notNull"`
	CreatedAt   time.Time `gorm:"autoCreateTime"`
}

func (ForwardLog) TableName() string {
	return "forward_logs"
}

// UserStats 排行榜用户统计。
type UserStats struct {
	UserID      int64     `gorm:"primaryKey"`
	Username    string    `gorm:"size:255"`
	FirstName   string    `gorm:"size:255"`
	RescueCount int       `gorm:"default:0"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime"`
}

func (UserStats) TableName() string {
	return "user_stats"
}

// ChatSettings 保存群级治理配置。
// 只包含开关与数值配置，不保存任何消息内容。
type ChatSettings struct {
	ChatID          int64 `gorm:"primaryKey"`
	Enabled         bool
	CooldownSeconds int
	MentionEnabled  bool
	UpdatedBy       int64
	CreatedAt       time.Time `gorm:"autoCreateTime"`
	UpdatedAt       time.Time `gorm:"autoUpdateTime"`
}

func (ChatSettings) TableName() string {
	return "chat_settings"
}

// ChatIgnoredUser 保存群级忽略名单。
// 只保存群 ID 与用户 ID，不保存消息内容或用户名快照。
type ChatIgnoredUser struct {
	ID        uint64 `gorm:"primaryKey"`
	ChatID    int64  `gorm:"notNull;uniqueIndex:idx_chat_ignored_user,priority:1"`
	UserID    int64  `gorm:"notNull;uniqueIndex:idx_chat_ignored_user,priority:2"`
	CreatedBy int64
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

func (ChatIgnoredUser) TableName() string {
	return "chat_ignored_users"
}
