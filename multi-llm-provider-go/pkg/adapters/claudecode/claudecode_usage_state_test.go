package claudecode

import (
	"os"
	"testing"
)

// UsageLimitState reads the statusline sidecar: unknown before the first
// response writes rate_limits, below the limit, or exhausted at >= 99%.
func TestUsageLimitStateFromStatusline(t *testing.T) {
	session := "mlp-claude-code-usage-state-test"
	path := claudeStatuslinePath(session)
	t.Cleanup(func() { _ = os.Remove(path) })
	write := func(body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if known, _ := UsageLimitState(session); known {
		t.Fatal("no sidecar must be unknown")
	}
	write(`{"model":{"id":"x"}}`)
	if known, _ := UsageLimitState(session); known {
		t.Fatal("a sidecar without rate_limits (fresh pane) must be unknown")
	}
	write(`{"rate_limits":{"five_hour":{"used_percentage":17,"resets_at":1790497800},"seven_day":{"used_percentage":35,"resets_at":1790870000}}}`)
	if known, exhausted := UsageLimitState(session); !known || exhausted {
		t.Fatalf("17%%/35%%: known=%v exhausted=%v", known, exhausted)
	}
	write(`{"rate_limits":{"five_hour":{"used_percentage":99.4,"resets_at":1790497800}}}`)
	if known, exhausted := UsageLimitState(session); !known || !exhausted {
		t.Fatalf("99.4%%: known=%v exhausted=%v", known, exhausted)
	}
}
