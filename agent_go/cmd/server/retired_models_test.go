package server

import "testing"

func TestRetiredClaudeSonnet5MapsToSonnet55(t *testing.T) {
	if got := currentCodingAgentModel("claude-code", "claude-sonnet-5"); got != "claude-sonnet-5-5" {
		t.Fatalf("claude-sonnet-5 -> %q, want claude-sonnet-5-5", got)
	}
	if got := currentCodingAgentModel("cursor-cli", "claude-sonnet-5"); got != "claude-sonnet-5" {
		t.Fatalf("other providers must be unchanged, got %q", got)
	}
}
