package picli

import (
	"testing"
	"time"
)

func TestPiDurableAckBudget(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("PI_DURABLE_ACK_SECONDS", "")
		if got := piDurableAckBudget(); got != 60*time.Second {
			t.Fatalf("budget = %v, want 60s", got)
		}
	})
	t.Run("override", func(t *testing.T) {
		t.Setenv("PI_DURABLE_ACK_SECONDS", "90")
		if got := piDurableAckBudget(); got != 90*time.Second {
			t.Fatalf("budget = %v, want 90s", got)
		}
	})
	t.Run("clamped", func(t *testing.T) {
		t.Setenv("PI_DURABLE_ACK_SECONDS", "1")
		if got := piDurableAckBudget(); got != 5*time.Second {
			t.Fatalf("budget = %v, want 5s floor", got)
		}
		t.Setenv("PI_DURABLE_ACK_SECONDS", "9999")
		if got := piDurableAckBudget(); got != 300*time.Second {
			t.Fatalf("budget = %v, want 300s ceiling", got)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		t.Setenv("PI_DURABLE_ACK_SECONDS", "soon")
		if got := piDurableAckBudget(); got != 60*time.Second {
			t.Fatalf("budget = %v, want 60s default", got)
		}
	})
}
