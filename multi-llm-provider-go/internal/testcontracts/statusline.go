package testcontracts

import (
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// AssertStatusLineContract validates the invariants every statusline-emitting
// CLI adapter must satisfy. New providers get coverage by calling this helper
// instead of re-deriving the checks, keeping the cross-provider contract in one
// place.
//
//	wantProvider  the adapter's canonical display name (e.g. "cursor-cli",
//	              "claudecode") — what consumers render verbatim.
//	requireTokens demand real telemetry. Pass true for live runs; pass false for
//	              synthetic fixtures that intentionally omit some fields.
func AssertStatusLineContract(t testing.TB, sl *llmtypes.StatusLine, wantProvider string, requireTokens bool) {
	t.Helper()
	if sl == nil {
		t.Fatal("statusline contract: StatusLine is nil")
	}
	if sl.Provider != wantProvider {
		t.Errorf("statusline contract: Provider = %q, want %q (adapter must emit its canonical display name)", sl.Provider, wantProvider)
	}
	// The model must never equal the provider name — that renders a duplicate
	// "X · X" label (the claude-code placeholder-model regression).
	if sl.Model != "" && sl.Model == sl.Provider {
		t.Errorf("statusline contract: Model == Provider (%q) — placeholder model must be stripped to avoid a duplicate label", sl.Model)
	}
	if requireTokens {
		if sl.InputTokens == 0 && sl.OutputTokens == 0 && sl.TotalInputTokens == 0 && sl.TotalOutputTokens == 0 {
			t.Errorf("statusline contract: no token telemetry present: %+v", sl)
		}
	}
	// When a tmux session is carried it must be non-empty (consumers use it to
	// target the owning pane). Value is provider-specific, so only assert presence.
	if v, ok := sl.Metadata["tmux_session"]; ok {
		if s, _ := v.(string); strings.TrimSpace(s) == "" {
			t.Error("statusline contract: Metadata[tmux_session] present but empty")
		}
	}
}
