package server

import (
	"errors"
	"testing"
)

// Git drafts still use a tool-free Claude request: removing 970 must not
// restore tmux Bash/Read under the app account (original 970 security fix).
func TestGitDraftClaudeRefusesNonSDK(t *testing.T) {
	for _, transport := range []string{"tmux", "", "SDK", " sdk ", "structured"} {
		t.Run(transport, func(t *testing.T) {
			t.Setenv("CLAUDE_CODE_TRANSPORT", transport)
			api := &StreamingAPI{}
			_, err := api.publicationText(t.Context(), "actor", "git_draft", &publishedProject{Provider: "claude-code"}, "data", "system")
			if !errors.Is(err, errToolFreeClaudeNeedsSDK) {
				t.Fatalf("not refused by transport rule: %v", err)
			}
		})
	}
	if err := toolFreeTransportAllowed("claude-code", "sdk"); err != nil {
		t.Fatal(err)
	}
}
