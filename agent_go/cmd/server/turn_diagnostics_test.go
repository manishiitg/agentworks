package server

import "testing"

// Only Muse turns get diagnostics, and a session with no recorded request adds nothing.
func TestTurnDiagnosticsOnlyForMuse(t *testing.T) {
	api := &StreamingAPI{lastQueryRequests: map[string]QueryRequest{
		"codex-session": {Provider: "codex-cli"},
		"muse-session":  {Provider: "muse-cli"},
	}}
	if got := api.turnDiagnostics("codex-session"); got != "" {
		t.Fatalf("a non-Muse turn adds nothing, got %q", got)
	}
	if got := api.turnDiagnostics("unknown"); got != "" {
		t.Fatalf("an unknown session adds nothing, got %q", got)
	}
	if got := api.turnDiagnostics("muse-session"); got != "pool=missing" {
		t.Fatalf("a Muse turn with no running CLI says so, got %q", got)
	}
}
