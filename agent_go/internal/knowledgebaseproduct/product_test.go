package knowledgebaseproduct

import (
	"context"
	"slices"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestManifestSeparatesAccessBuilderAndContent(t *testing.T) {
	m, err := Manifest()
	if err != nil {
		t.Fatal(err)
	}
	p := BuiltinAgentProfile()
	if p.Product != ProfileID || p.Scope != agentprofiles.ProfileScopeProject || p.Runtime.AgentTools.Mode != "mcp_only" || p.Runtime.Workspace.Root != "Chats/Knowledgebase" {
		t.Fatalf("invalid product runtime: %+v", p.Runtime)
	}
	// The curator commands ship with the product and resolve their prompts (PLAT-618).
	if len(p.Commands) != 1 || p.Commands[0].Name != "organize" {
		t.Fatalf("curator commands: %+v", p.Commands)
	}
	if len(p.ToolPolicy.Enabled) != 9 || p.ToolPolicy.Enabled[0] != "brain_access" || len(p.Runtime.BridgeTools) != 9 || slices.Contains(p.ToolPolicy.Enabled, "brain_backup") || !slices.Contains(p.ToolPolicy.Enabled, "execute_shell_command") || len(p.Schedules) != 1 || p.Schedules[0].ID != "organize" || p.Schedules[0].Isolated || p.Schedules[0].Enabled {
		t.Fatalf("builder tools: %+v", p.ToolPolicy)
	}
	if len(m.Chat["mcp"].ExternalTools) != 5 {
		t.Fatal("Brain must expose five action-based tools (no backup tool, PLAT-633)", m.Chat["mcp"].ExternalTools)
	}
	if m.UI.FilesPanel || m.UI.WorkflowPanel || m.UI.Secrets {
		t.Fatal("access builder exposes general workspace capabilities")
	}
}

func TestAccessFactoryRetainsTrustedRuntimeIdentity(t *testing.T) {
	r := agentprofiles.NewRegistry()
	if err := RegisterAgentProfileRuntime(r, func(_ context.Context, runtime agentprofiles.ToolRuntimeContext, args map[string]any) (string, error) {
		if runtime.UserID != "priya" || runtime.Product != ProfileID {
			t.Fatalf("lost trusted runtime: %+v", runtime)
		}
		return "ok", nil
	}); err != nil {
		t.Fatal(err)
	}
	tool, err := r.BuildTool(agentprofiles.ToolBinding{ID: "knowledgebase.manage-access"}, agentprofiles.ToolRuntimeContext{UserID: "priya", Product: ProfileID})
	if err != nil {
		t.Fatal(err)
	}
	if tool.Name != "brain_access" {
		t.Fatal(tool.Name)
	}
	if _, err = tool.Execute(context.Background(), map[string]any{"identity_id": "someone-else", "action": "list"}); err != nil {
		t.Fatal(err)
	}
}
