package codexcli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestCodexCLIRealProjectArtifactsLifecycle is an end-to-end test that
// proves the WithWriteProjectInstructionFile lifecycle against a
// real `codex` binary by exercising the projected files (AGENTS.md
// + .codex/config.toml [mcp_servers.*] tables).
//
// The hooks.json + deny-builtin.sh projection was REMOVED — codex's
// first-class --disable <feature> CLI flags (see
// codexBridgeOnlyDisabledFeatures in options.go) cover the
// deny-builtin lever without needing a workspace hook script. See
// the comment in codexcli_project_artifacts.go's removed-helper
// note for the full rationale.
//
// Verifications after the call returns:
//   - <workingDir>/AGENTS.md byte-restored to operator pre-seed
//   - mtime advanced past pre-seed (proves the writer fired and
//     then restored — without this, byte-restore would pass via
//     the null hypothesis of "writer never ran")
//   - .codex/hooks.json was NOT created (proves the hooks
//     projection stays disabled)
//
// Skipped unless RUN_CODEX_CLI_REAL_E2E=1 (or RUN_CODEX_CLI_INTERACTIVE_E2E=1)
// and codex+tmux are on PATH.
func TestCodexCLIRealProjectArtifactsLifecycle(t *testing.T) {
	requireRealCodexCLIE2E(t)
	t.Cleanup(func() { _ = CleanupCodexCLIInteractiveSessions(context.Background()) })

	tmp := t.TempDir()
	agentsPath := filepath.Join(tmp, "AGENTS.md")

	operatorAGENTS := []byte("# Operator AGENTS.md\n\nThis content MUST be restored on cleanup.\n")
	if err := os.WriteFile(agentsPath, operatorAGENTS, 0o600); err != nil {
		t.Fatalf("seed AGENTS.md: %v", err)
	}
	preAgents, _ := os.Stat(agentsPath)
	preMTime := preAgents.ModTime()
	time.Sleep(10 * time.Millisecond)

	adapter := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	mcpJSON := `{"orchestrator-bridge":{"command":"/tmp/mcpbridge","env":{"MCP_API_URL":"http://localhost:9999"}}}`

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
		WithInteractiveSessionID("codex-project-artifacts-"+codexRandomHex(4)),
		WithPersistentInteractiveSession(true),
		WithProjectDirID(tmp),
		WithApprovalPolicy("never"),
		WithReasoningEffort("low"),
		WithWriteProjectInstructionFile(true),
		WithMCPServers(mcpJSON),
	)
	if callErr != nil {
		t.Fatalf("GenerateContent error = %v", callErr)
	}

	// Persistent session mode keeps the tmux session alive across
	// turns and defers cleanup until the session is torn down. The
	// byte-restore lifecycle assertions below depend on cleanup
	// having run, so force-close the persistent session here.
	if err := CleanupCodexCLIInteractiveSessions(context.Background()); err != nil {
		t.Fatalf("force-cleanup of persistent codex session: %v", err)
	}

	postAgents, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("AGENTS.md must still exist after cleanup (byte-restored): %v", err)
	}
	if string(postAgents) != string(operatorAGENTS) {
		t.Errorf("cleanup must restore operator AGENTS.md byte-for-byte\n  want: %s\n  got:  %s", operatorAGENTS, postAgents)
	}

	postInfo, _ := os.Stat(agentsPath)
	if !postInfo.ModTime().After(preMTime) {
		t.Errorf("AGENTS.md mtime must advance past pre-seed (proves the adapter touched the file mid-session and then restored); preSeed=%v post=%v", preMTime, postInfo.ModTime())
	}

	// .codex/hooks.json MUST NOT have been written — this projection
	// is gated behind MLP_ENABLE_UNSAFE_WORKSPACE_PROJECTIONS, which
	// is not set during the test.
	if _, err := os.Stat(filepath.Join(tmp, ".codex", "hooks.json")); err == nil {
		t.Errorf(".codex/hooks.json was created despite MLP_ENABLE_UNSAFE_WORKSPACE_PROJECTIONS being unset; the unsafe-projection gate is broken")
	}
}
