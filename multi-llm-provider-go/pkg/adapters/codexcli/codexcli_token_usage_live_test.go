package codexcli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestCodexTokenUsageLive is the CertTokenUsage P0 proof for Codex: a real
// turn reports input/output token counts through GenerationInfo, the
// surface the cost ledger reads.
//
// Gated behind -coding-cli-p0-live; requires a real codex CLI, node, tmux.
func TestCodexTokenUsageLive(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	owner := "codex-usage-live-" + codexRandomHex(4)
	workDir := t.TempDir()
	marker := "USAGE_" + strings.ToUpper(codexRandomHex(4))

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Task "+marker+": what is 2+2? Reply with one line: the answer, a space, then the task ID.")},
		WithInteractiveSessionID(owner),
		WithPersistentInteractiveSession(true),
		WithProjectDirID(workDir),
	)
	if err != nil {
		t.Fatalf("GenerateContent error = %v", err)
	}
	final := ""
	if len(resp.Choices) == 1 {
		final = strings.TrimSpace(resp.Choices[0].Content)
	}
	if !strings.Contains(final, marker) {
		t.Fatalf("turn did not produce the marker; final=%q", final)
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].GenerationInfo == nil {
		t.Fatal("turn reported no GenerationInfo")
	}
	usage := llmtypes.ExtractUsageFromGenerationInfo(resp.Choices[0].GenerationInfo)
	if usage == nil || usage.InputTokens <= 0 || usage.OutputTokens <= 0 {
		t.Fatalf("usage = %+v, want live input/output counts", usage)
	}
}
