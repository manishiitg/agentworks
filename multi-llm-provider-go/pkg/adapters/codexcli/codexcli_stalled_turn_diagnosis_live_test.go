package codexcli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestCodexCLIRealInteractiveStalledTurnDiagnosisContract certifies
// CertStalledTurnDiagnosis (PLAT-116): a REAL, already-finished Codex turn
// must be independently rediscoverable by DiagnoseTurnCompletion with no
// turn in flight — proving the post-hoc read path the platform falls back
// to when its own live bridge from a completed turn stalls actually works
// against a genuine rollout, not just the synthetic fixtures in
// codexcli_turn_diagnostics_test.go.
func TestCodexCLIRealInteractiveStalledTurnDiagnosisContract(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	ownerSessionID := "codex-stalled-diag-" + codexRandomHex(4)
	token := "STALLED_DIAG_" + codexRandomHex(4)
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}

	options := []llmtypes.CallOption{
		WithInteractiveSessionID(ownerSessionID),
		WithPersistentInteractiveSession(true),
		WithDisableShellTool(),
		WithApprovalPolicy("never"),
		WithReasoningEffort("low"),
	}

	turnStart := time.Now().UTC()
	prompt := fmt.Sprintf("This is a real Codex CLI P0 test. Do not use tools. Reply exactly:\nacknowledged %s", token)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}},
	}, options...)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	content := strings.TrimSpace(resp.Choices[0].Content)
	if !strings.Contains(content, token) {
		t.Fatalf("content = %q, want token %s", content, token)
	}

	// The turn already completed live above (GenerateContent returned). This
	// certifies the SEPARATE, post-hoc path PLAT-116 added: an external
	// caller with no turn in flight asking whether this exact turn's own
	// rollout shows it finished, independent of whatever live detection
	// GenerateContent itself used internally.
	found, completedAt, message := DiagnoseTurnCompletion(workingDir, turnStart)
	if !found {
		t.Fatal("DiagnoseTurnCompletion did not find the real Codex turn's completion after the live turn already succeeded")
	}
	if completedAt.Before(turnStart) {
		t.Fatalf("completedAt = %v, want at or after turnStart %v", completedAt, turnStart)
	}
	if !strings.Contains(message, token) {
		t.Fatalf("diagnosed last_agent_message = %q, want it to contain token %s", message, token)
	}
}
