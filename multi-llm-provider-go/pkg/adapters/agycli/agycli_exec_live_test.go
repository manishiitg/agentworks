package agycli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/testcontracts"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func agyRandomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b)
}

// Live exec-lane P0 proofs for agy: one fresh print-mode turn earns
// fresh_launch (process starts, turn completes), done_detection (exit 0 +
// SUCCESS envelope), final_extraction (canary token in the reply), and
// token_usage (wire usage lands on the response).
//
// Auth: the stored `agy` Google login (no API key). Gated behind
// -coding-cli-p0-live so plain unit runs stay hermetic.

func requireRealAgyCLIE2E(t *testing.T) {
	t.Helper()
	if !*codingCLIP0Live {
		t.Skip("run through the live coding CLI P0 runner: go test ./pkg/adapters/agycli/ -run TestAgyCLIReal -args -coding-cli-p0-live")
	}
	if _, err := exec.LookPath("agy"); err != nil {
		t.Fatalf("real agy CLI tests require agy in PATH: %v", err)
	}
}

func TestAgyCLIRealExecFullContract(t *testing.T) {
	requireRealAgyCLIE2E(t)
	workDir := t.TempDir()
	token := "AGY_EXEC_CANARY_" + agyRandomHex(t, 4)

	adapter := NewAgyCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "Reply with exactly what the user asks for. Do not use tools."),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply with exactly this token and nothing else: "+token),
	}, WithWorkingDir(workDir))
	if err != nil {
		t.Fatalf("GenerateContent() error = %v", err)
	}
	if resp == nil || len(resp.Choices) == 0 {
		t.Fatal("GenerateContent() returned no choices")
	}
	// Shared final_extraction bar: canary present, no envelope/stderr chrome.
	testcontracts.AssertCleanFinalExtraction(t, "agy-cli", resp.Choices[0].Content,
		[]string{token}, []string{"conversation_id", "jetski:", "denied_actions"})
	if resp.Usage == nil || resp.Usage.InputTokens <= 0 || resp.Usage.OutputTokens <= 0 {
		t.Fatalf("usage = %+v, want positive input/output tokens", resp.Usage)
	}
	handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resp)
	if !ok || handle.Provider != "agy-cli" || handle.NativeSessionID == "" {
		t.Fatalf("missing agy coding provider handle: %#v ok=%v", handle, ok)
	}
	if handle.WorkingDir != workDir {
		t.Fatalf("handle working dir = %q, want %q", handle.WorkingDir, workDir)
	}
}

func TestAgyCLIRealReasoningEffortContract(t *testing.T) {
	requireRealAgyCLIE2E(t)
	// Unknown effort fails fast without touching the CLI; a known level
	// rides --effort through a real turn.
	adapter := NewAgyCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "hi"),
	}, WithWorkingDir(t.TempDir()), llmtypes.WithReasoningEffort("ultra"))
	if err == nil || !strings.Contains(err.Error(), "unknown reasoning effort") {
		t.Fatalf("error = %v, want fail-fast on unknown effort", err)
	}
	token := "AGY_EFFORT_" + agyRandomHex(t, 4)
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply with exactly this token and nothing else: "+token),
	}, WithWorkingDir(t.TempDir()), llmtypes.WithReasoningEffort("low"))
	if err != nil {
		t.Fatalf("effort=low turn error = %v", err)
	}
	if content := strings.TrimSpace(resp.Choices[0].Content); !strings.Contains(content, token) {
		t.Fatalf("content = %q, want %s", content, token)
	}
}
