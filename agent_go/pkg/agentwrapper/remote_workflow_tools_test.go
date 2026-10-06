package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/remoteplacement"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestRemoteWorkflowToolsCannotUpgradeAndFinalizeLateScope(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	placement := filepath.Join(t.TempDir(), "placements.json")
	t.Setenv(remoteplacement.FileEnv, placement)
	if err := os.WriteFile(placement, []byte(`{"workflows":{"Workflow/remote":"team"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"", "mcp_only", "full", "hybrid", "full_unconfined"} {
		runtime := runtimeConfigForLLMAgent(LLMAgentConfig{CodingAgentWorkingDir: filepath.Join(root, "Workflow/remote"), CodingAgentToolsMode: mode}, nil, nil, "", nil)
		w := &LLMAgentWrapper{runtime: runtime}
		if changed, err := w.UpgradeCodingAgentToolsToFull(); err != nil || changed || w.CodingAgentToolsMode() != "mcp_only" {
			t.Fatalf("requested %q: changed=%v err=%v mode=%q", mode, changed, err, w.CodingAgentToolsMode())
		}
	}

	const session = "remote-wrapper-late-scope"
	t.Cleanup(func() { common.ClearSessionShellConfig(session) })
	runtime := runtimeConfigForLLMAgent(LLMAgentConfig{SessionID: session, CodingAgentWorkingDir: t.TempDir(), CodingAgentToolsMode: "full"}, wrapperSessionTestModel{}, nil, "", loggerv2.NewNoop())
	draft, err := mcpagent.NewAgentFromDefinition(context.Background(), mcpagent.AgentDefinition{}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	w := &LLMAgentWrapper{agent: draft, runtime: runtime}
	t.Cleanup(func() { _ = w.Close() })
	if w.CodingAgentToolsMode() != "full" {
		t.Fatal("local draft unexpectedly restricted")
	}
	// The shell guard receives the workflow after the private CLI runtime was
	// built. Finalization must still replace native tools with bridge-only.
	common.SetSessionWorkflowPath(session, "Workflow/remote")
	if err := w.FinalizeDefinition(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := w.CodingAgentToolsMode(); got != "mcp_only" {
		t.Fatalf("finalized mode = %q", got)
	}
	if _, err := w.UpgradeCodingAgentToolsToFull(); err == nil {
		t.Fatal("finalized agent accepted an upgrade")
	}
}
