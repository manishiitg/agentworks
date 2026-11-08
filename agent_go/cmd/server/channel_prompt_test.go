package server

import (
	"strings"
	"testing"
)

// The session-end-words rule must live in the always-present channel prompt
// fragment, not only in the lazily-loaded deployed-channel reference doc:
// trivial one-word turns ("end") answer without ever loading that doc, so the
// model announced "Session ended" (issue 202, E1). Every bot platform fragment
// must carry the rule in-context from session start.
func TestChannelPromptCarriesSessionContinuityRule(t *testing.T) {
	for _, platform := range []string{"slack", "whatsapp"} {
		fragment := buildChannelFormattingInstructions(platform)
		if fragment == "" {
			t.Fatalf("%s: expected non-empty channel fragment", platform)
		}
		if !strings.Contains(fragment, "Session-end words are ordinary text") {
			t.Errorf("%s: channel fragment missing session-end-words rule", platform)
		}
		if !strings.Contains(fragment, "session ended") {
			t.Errorf("%s: channel fragment must forbid \"session ended\" announcements", platform)
		}
	}
}
