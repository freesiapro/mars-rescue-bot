package bot

import (
	"errors"
	"testing"
	"time"

	"gopkg.in/telebot.v4"
)

func TestSendReplyWithRetrySucceedsAfterFloodValue(t *testing.T) {
	attempts := 0
	var waits []time.Duration

	err := sendReplyWithRetry(func() error {
		attempts++
		if attempts < 3 {
			return telebot.FloodError{RetryAfter: 2}
		}
		return nil
	}, 3, func(d time.Duration) { waits = append(waits, d) })

	if err != nil {
		t.Fatalf("sendReplyWithRetry() error = %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
	if len(waits) != 2 || waits[0] != 2*time.Second || waits[1] != 2*time.Second {
		t.Errorf("waits = %v, want [2s 2s]", waits)
	}
}

func TestSendReplyWithRetrySucceedsAfterFloodPointer(t *testing.T) {
	attempts := 0
	var waits []time.Duration

	err := sendReplyWithRetry(func() error {
		attempts++
		if attempts == 1 {
			return &telebot.FloodError{RetryAfter: 1}
		}
		return nil
	}, 3, func(d time.Duration) { waits = append(waits, d) })

	if err != nil {
		t.Fatalf("sendReplyWithRetry() error = %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
	if len(waits) != 1 || waits[0] != time.Second {
		t.Errorf("waits = %v, want [1s]", waits)
	}
}

func TestSendReplyWithRetryDoesNotRetryOtherErrors(t *testing.T) {
	wantErr := errors.New("network down")
	attempts := 0

	err := sendReplyWithRetry(func() error {
		attempts++
		return wantErr
	}, 3, func(time.Duration) { t.Error("sleep should not be called") })

	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}

func TestSendReplyWithRetryExhaustsAttempts(t *testing.T) {
	attempts := 0
	var waits []time.Duration

	err := sendReplyWithRetry(func() error {
		attempts++
		return telebot.FloodError{RetryAfter: 1}
	}, 3, func(d time.Duration) { waits = append(waits, d) })

	if err == nil {
		t.Fatal("expected error after exhausting attempts")
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
	if len(waits) != 2 {
		t.Errorf("waits = %v, want 2 waits", waits)
	}
}

func TestSendReplyWithRetryCapsWait(t *testing.T) {
	var waits []time.Duration

	_ = sendReplyWithRetry(func() error {
		return telebot.FloodError{RetryAfter: 3600}
	}, 2, func(d time.Duration) { waits = append(waits, d) })

	if len(waits) != 1 {
		t.Fatalf("waits = %v, want 1 wait", waits)
	}
	if waits[0] != maxFloodWait {
		t.Errorf("wait = %v, want capped %v", waits[0], maxFloodWait)
	}
}
