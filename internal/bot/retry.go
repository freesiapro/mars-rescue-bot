package bot

import (
	"errors"
	"time"

	"gopkg.in/telebot.v3"
)

const (
	// maxReplyAttempts 是遇到 429 时最多的发送尝试次数（含首次）。
	maxReplyAttempts = 3
	// maxFloodWait 限制单次退避等待，避免 retry_after 异常大时长时间阻塞轮询。
	maxFloodWait = 30 * time.Second
)

// sendReplyWithRetry 调用 send，遇到 Telegram 429 时按 retry_after 退避重试。
// 非 FloodError 立即返回；重试次数达到上限后返回最后一次错误。
func sendReplyWithRetry(send func() error, attempts int, sleep func(time.Duration)) error {
	if attempts < 1 {
		attempts = 1
	}
	if sleep == nil {
		sleep = time.Sleep
	}

	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		err = send()
		if err == nil {
			return nil
		}

		wait, ok := floodRetryAfter(err)
		if !ok || attempt == attempts {
			return err
		}
		if wait > maxFloodWait {
			wait = maxFloodWait
		}
		sleep(wait)
	}
	return err
}

// floodRetryAfter 从错误中提取 Telegram FloodError 的 retry_after。
// telebot 在不同路径上可能返回值或指针，两种都兼容。
func floodRetryAfter(err error) (time.Duration, bool) {
	var value telebot.FloodError
	if errors.As(err, &value) && value.RetryAfter > 0 {
		return time.Duration(value.RetryAfter) * time.Second, true
	}

	var pointer *telebot.FloodError
	if errors.As(err, &pointer) && pointer != nil && pointer.RetryAfter > 0 {
		return time.Duration(pointer.RetryAfter) * time.Second, true
	}

	return 0, false
}
