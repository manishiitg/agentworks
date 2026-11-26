package claudecode

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestClaudeCodeTmuxIntegrationStalledTurnDiagnosisContract certifies
// CertStalledTurnDiagnosis (PLAT-116): a REAL, already-finished Claude Code
// turn must be independently rediscoverable by DiagnoseTurnCompletion with
// no turn in flight — proving the post-hoc read path the platform falls
// back to when its own live bridge from a completed turn stalls actually
// works against a genuine transcript, not just the synthetic fixtures in
// claudecode_turn_diagnostics_test.go.
func TestClaudeCodeTmuxIntegrationStalledTurnDiagnosisContract(t *testing.T) {
	skipClaudeInteractiveIntegration(t)

	adapter := NewClaudeCodeInteractiveAdapter(claudeInteractiveIntegrationModel(), &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	turnStart := time.Now().UTC()
	const userToken = "STALLED_DIAG_CLAUDE_9173"
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeSystem,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Do not use tools. Answer with only the exact token from the user message."},
			},
		},
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Token: " + userToken},
			},
		},
	})
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	got := strings.TrimSpace(resp.Choices[0].Content)
	if !strings.Contains(got, userToken) {
		t.Fatalf("content = %q, want user token %q", got, userToken)
	}

	nativeSessionID := experimentalClaudeSessionID(resp)
	if nativeSessionID == "" {
		t.Fatal("response did not include claude_code_session_id")
	}

	// The turn already completed live above (GenerateContent returned). This
	// certifies the SEPARATE, post-hoc path PLAT-116 added: an external
	// caller with no turn in flight asking whether this exact turn's own
	// transcript shows a committed end_turn, independent of whatever live
	// detection GenerateContent itself used internally.
	found, text := DiagnoseTurnCompletion(nativeSessionID, "", turnStart)
	if !found {
		t.Fatal("DiagnoseTurnCompletion did not find the real Claude Code turn's completion after the live turn already succeeded")
	}
	if !strings.Contains(text, userToken) {
		t.Fatalf("diagnosed text = %q, want it to contain token %s", text, userToken)
	}
}
