package llmproviders

import "testing"

func TestIsAllowedCodingAgentControlKey(t *testing.T) {
	for _, key := range []string{"Escape", "C-c", "Enter", "Up", "Down"} {
		if !IsAllowedCodingAgentControlKey(key) {
			t.Fatalf("expected control key %q to be allowed", key)
		}
	}

	for _, key := range []string{"", "Left", "Right", "Tab", "enter", "ctrl-c"} {
		if IsAllowedCodingAgentControlKey(key) {
			t.Fatalf("expected control key %q to be rejected", key)
		}
	}
}
