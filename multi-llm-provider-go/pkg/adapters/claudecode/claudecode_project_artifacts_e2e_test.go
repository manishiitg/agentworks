package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestClaudeCodeTmuxRealProjectArtifactsLifecycle is an end-to-end test
// that proves the WithWriteProjectInstructionFile lifecycle against a
// real `claude` binary by exercising the SAFE projections (the ones not
// gated behind MLP_ENABLE_UNSAFE_WORKSPACE_PROJECTIONS).
//
// The current safe set for Claude Code is just <workingDir>/CLAUDE.md —
// the .mcp.json projection is disabled by default because it triggers
// Claude Code v2.1.150's "New MCP server found in .mcp.json — approve?"
// discovery prompt that the tmux adapter cannot dismiss. See Claude
// Code tmux adapter for the gate explanation.
//
// Verifications after the call returns:
//   - <workingDir>/CLAUDE.md is gone (proves write→cleanup ran;
//     without it the per-session file would persist)
//   - the call itself completed without timing out (proves the
//     CLAUDE.md write does not trigger a prompt; vs the .mcp.json write
//     that does)
//
// Skipped unless RUN_CLAUDE_CODE_TMUX_INTEGRATION=1 and the
// `claude` binary is on PATH (same gate as sibling tmux E2Es).
func TestClaudeCodeTmuxRealProjectArtifactsLifecycle(t *testing.T) {
	skipClaudeInteractiveIntegration(t)

	tmp := t.TempDir()
	claudeMdPath := filepath.Join(tmp, "CLAUDE.md")

	adapter := NewClaudeCodeInteractiveAdapter(claudeInteractiveIntegrationModel(), &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = CleanupClaudeCodeTmuxSessions(context.Background()) })

	// Provide a system prompt so the CLAUDE.md write path fires inside
	// buildClaudeArgs. Without a non-empty system prompt the projection
	// helper short-circuits and the test wouldn't be exercising the
	// feature.
	_, callErr := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role:  llmtypes.ChatMessageTypeSystem,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply concisely."}},
		},
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Reply with exactly the single word OK and nothing else."},
			},
		},
	},
		WithWorkingDir(tmp),
		WithWriteProjectInstructionFile(true),
	)
	if callErr != nil {
		t.Fatalf("GenerateContent error = %v", callErr)
	}

	// CLAUDE.md must be gone post-cleanup — no prior file existed in
	// the tmp dir, so removeFiles takes the os.Remove path.
	if _, err := os.Stat(claudeMdPath); !os.IsNotExist(err) {
		t.Errorf("CLAUDE.md leaked past cleanup; stat err = %v", err)
	}
}
