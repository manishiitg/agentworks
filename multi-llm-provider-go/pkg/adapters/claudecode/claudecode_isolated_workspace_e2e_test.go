package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestClaudeCodeTmuxRealIsolatedTmpDirDoesNotTouchOuterWorkspace
// is the Phase B+ end-to-end validation for the workflow-step
// isolation design (docs/WORKFLOW_STEP_ISOLATION.md) on Claude Code,
// templated from cursorcli_isolated_workspace_e2e_test.go. See the
// cursor variant's docstring for the shared rationale; below covers
// only the claude-specific specifics.
//
// Claude-specific anchors:
//   - The leaked artifacts to guard against in userWorkspace are
//     `.claude/` (rules, settings, plugins) and `.mcp.json`.
//   - Working-dir option is WithWorkingDir (claudecode's adapter).
//   - Uses the Claude Code tmux adapter (NewClaudeCodeInteractiveAdapter)
//     because that's the path mcpagent's coding-agent options drive.
//
// Skipped unless RUN_CLAUDE_CODE_TMUX_INTEGRATION=1 and the
// claude binary is on PATH.
func TestClaudeCodeTmuxRealIsolatedTmpDirDoesNotTouchOuterWorkspace(t *testing.T) {
	skipClaudeInteractiveIntegration(t)
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	userWorkspace := t.TempDir()
	sentinelPath := filepath.Join(userWorkspace, "operator-do-not-touch.txt")
	sentinelContent := []byte("ISOLATION_SENTINEL_CLAUDE_42\nmust not be touched by claude built-ins\n")
	if err := os.WriteFile(sentinelPath, sentinelContent, 0o600); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}
	preMTime := mustStatModTime(t, sentinelPath)
	time.Sleep(10 * time.Millisecond)

	isolatedWorkspace, err := os.MkdirTemp("", "mlp-cli-session-*")
	if err != nil {
		t.Fatalf("create isolated tmp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(isolatedWorkspace) })

	adapter := NewClaudeCodeInteractiveAdapter(claudeInteractiveIntegrationModel(), &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	_, callErr := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role:  llmtypes.ChatMessageTypeSystem,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply tersely."}},
		},
		{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with exactly the word OK and nothing else."}},
		},
	},
		WithWorkingDir(isolatedWorkspace),
	)
	if callErr != nil {
		t.Fatalf("GenerateContent error = %v", callErr)
	}

	// 1. SENTINEL UNCHANGED
	postContent, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("sentinel must still exist: %v", err)
	}
	if string(postContent) != string(sentinelContent) {
		t.Errorf("sentinel was modified — isolation failed\n  want: %s\n  got:  %s", sentinelContent, postContent)
	}
	if !mustStatModTime(t, sentinelPath).Equal(preMTime) {
		t.Errorf("sentinel mtime advanced — something touched the file")
	}

	// 2. NO CLAUDE ARTIFACTS IN USER WORKSPACE
	for _, leak := range []string{".claude", ".mcp.json"} {
		if _, err := os.Stat(filepath.Join(userWorkspace, leak)); err == nil {
			t.Errorf("%q leaked into user workspace — isolation failed", leak)
		}
	}
}

func mustStatModTime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.ModTime()
}

// (unused-import guard — strings is imported above only when other
// claude isolation tests assert on captured pane content)
var _ = strings.TrimSpace
