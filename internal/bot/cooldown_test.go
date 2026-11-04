package bot

import (
	"testing"
	"time"
)

func TestCooldownAllow(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	c := NewCooldown()
	c.now = func() time.Time { return now }

	const chatID = int64(-100)

	if !c.Allow(chatID, 60*time.Second) {
		t.Fatal("first reply should be allowed")
	}
	if c.Allow(chatID, 60*time.Second) {
		t.Error("second reply within cooldown should be denied")
	}

	now = now.Add(59 * time.Second)
	if c.Allow(chatID, 60*time.Second) {
		t.Error("reply at 59s should still be denied")
	}

	now = now.Add(2 * time.Second)
	if !c.Allow(chatID, 60*time.Second) {
		t.Error("reply after cooldown should be allowed")
	}
}

func TestCooldownZeroAlwaysAllows(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	c := NewCooldown()
	c.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if !c.Allow(-100, 0) {
			t.Fatalf("reply %d with zero cooldown should be allowed", i+1)
		}
	}
}

func TestCooldownPerChat(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	c := NewCooldown()
	c.now = func() time.Time { return now }

	if !c.Allow(-100, time.Minute) {
		t.Fatal("chat A first reply should be allowed")
	}
	if !c.Allow(-200, time.Minute) {
		t.Error("chat B should not be affected by chat A cooldown")
	}
}
