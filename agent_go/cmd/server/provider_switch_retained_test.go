package server

import (
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
)

// Switching the provider on the Models page must make the retained CLI of the old provider the wrong target (Code on Excellence, 2026-10-04: after Muse -> Codex
// the next sends went to the old Muse record and answered 409 delivery_uncertain).
func TestRetainedCLIProviderDiffersFromTheRequestedProvider(t *testing.T) {
	const sessionID = "product-provider-switch"
	terminalStore := terminals.NewStore()
	terminalStore.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(
		time.Now(), sessionID, "main:"+sessionID, "mlp-muse-product-provider-switch",
	))
	api := &StreamingAPI{terminalStore: terminalStore}

	for name, tc := range map[string]struct {
		requested string
		want      bool
	}{
		"same provider":             {"muse-cli", false},
		"same provider, other case": {"Muse-CLI", false},
		"switched to codex":         {"codex-cli", true},
		"switched to claude":        {"claude-code", true},
		"request names no provider": {"", false},
		"request names only spaces": {"  ", false},
	} {
		if got := api.retainedCLIProviderDiffers(sessionID, tc.requested); got != tc.want {
			t.Errorf("%s: differs(%q) = %v, want %v", name, tc.requested, got, tc.want)
		}
	}
	if api.retainedCLIProviderDiffers("no-retained-cli", "codex-cli") {
		t.Error("nothing retained: a provider cannot differ")
	}

	// The provider comes from the request, or from its LLM config when the request names none.
	if got := requestedProviderOf(QueryRequest{Provider: " codex-cli "}); got != "codex-cli" {
		t.Errorf("requested provider = %q", got)
	}
	configured := QueryRequest{LLMConfig: &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: "claude-code"}}}
	if got := requestedProviderOf(configured); got != "claude-code" {
		t.Errorf("requested provider from the LLM config = %q", got)
	}
}
