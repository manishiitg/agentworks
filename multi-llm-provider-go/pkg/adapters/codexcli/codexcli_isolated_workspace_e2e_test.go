package codexcli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestCodexCLIRealIsolatedTmpDirDoesNotTouchOuterWorkspace is the
// Phase B+ end-to-end validation for the workflow-step isolation
// design on Codex CLI, templated from
// cursorcli_isolated_workspace_e2e_test.go. See the cursor variant's
// docstring for the shared rationale; below covers codex specifics.
//
// Codex-specific anchors:
//   - Working-dir option is WithProjectDirID (codex's adapter uses
//     it as both the cwd AND the project-id path for hooks/config).
//   - Leaked artifacts to guard against in userWorkspace are
//     `AGENTS.md` and `.codex/`.
//   - Pairs with the WithCodexSandbox("workspace-write") that
//     mcpagent now pins (mcpagent commit c6bbfae). Sandbox is what
//     actually CONFINES apply_patch to cwd; this test verifies the
//     cwd is correctly the tmp dir, not the user workspace.
//
// Skipped unless RUN_CODEX_CLI_REAL_E2E=1 (or RUN_CODEX_CLI_INTERACTIVE_E2E=1)
// and codex+tmux are on PATH.
func TestCodexCLIRealIsolatedTmpDirDoesNotTouchOuterWorkspace(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	userWorkspace := t.TempDir()
	sentinelPath := filepath.Join(userWorkspace, "operator-do-not-touch.txt")
	sentinelContent := []byte("ISOLATION_SENTINEL_CODEX_42\nmust not be touched by codex built-ins\n")
	if err := os.WriteFile(sentinelPath, sentinelContent, 0o600); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}
	preMTime, _ := os.Stat(sentinelPath)
	preSeedMTime := preMTime.ModTime()
	time.Sleep(10 * time.Millisecond)

	isolatedWorkspace, err := os.MkdirTemp("", "mlp-cli-session-*")
	if err != nil {
		t.Fatalf("create isolated tmp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(isolatedWorkspace) })

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	_, callErr := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with exactly the word OK and nothing else."}},
		},
	},
		WithInteractiveSessionID("codex-isolated-"+codexRandomHex(4)),
		WithPersistentInteractiveSession(true),
		WithProjectDirID(isolatedWorkspace),
		WithApprovalPolicy("never"),
		WithReasoningEffort("low"),
	)
	if callErr != nil {
		t.Fatalf("GenerateContent error = %v", callErr)
	}

	// Force cleanup so persistent session teardown runs before assertions.
	if err := CleanupCodexCLIInteractiveSessions(context.Background()); err != nil {
		t.Fatalf("force-cleanup of persistent codex session: %v", err)
	}

	// 1. SENTINEL UNCHANGED
	postContent, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("sentinel must still exist: %v", err)
	}
	if string(postContent) != string(sentinelContent) {
		t.Errorf("sentinel was modified — isolation failed\n  want: %s\n  got:  %s", sentinelContent, postContent)
	}
	postInfo, _ := os.Stat(sentinelPath)
	if !postInfo.ModTime().Equal(preSeedMTime) {
		t.Errorf("sentinel mtime advanced — something touched the file\n  pre:  %v\n  post: %v", preSeedMTime, postInfo.ModTime())
	}

	// 2. NO CODEX ARTIFACTS IN USER WORKSPACE
	for _, leak := range []string{"AGENTS.md", ".codex"} {
		if _, err := os.Stat(filepath.Join(userWorkspace, leak)); err == nil {
			t.Errorf("%q leaked into user workspace — isolation failed", leak)
		}
	}
}
