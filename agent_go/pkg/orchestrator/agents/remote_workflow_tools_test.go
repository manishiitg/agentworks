package agents

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/remoteplacement"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type remoteToolsTestModel struct{}

func (remoteToolsTestModel) GenerateContent(context.Context, []llmtypes.MessageContent, ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	return &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{Content: "done"}}}, nil
}
func (remoteToolsTestModel) GetModelID() string { return "remote-tools-test" }
func (remoteToolsTestModel) GetModelMetadata(string) (*llmtypes.ModelMetadata, error) {
	return &llmtypes.ModelMetadata{ModelID: "remote-tools-test"}, nil
}

func TestRemoteWorkflowAgentConstructionAndLateFinalization(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	placement := filepath.Join(t.TempDir(), "placements.json")
	t.Setenv(remoteplacement.FileEnv, placement)
	if err := os.WriteFile(placement, []byte(`{"workflows":{"Workflow/remote":"team"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, late := range []bool{false, true} {
		session := "remote-workflow-agent"
		common.ClearSessionShellConfig(session)
		t.Cleanup(func() { common.ClearSessionShellConfig(session) })
		if !late {
			common.SetSessionWorkflowPath(session, "Workflow/remote")
		}
		// The CLI cwd is a local scratch folder, not the workflow's path.
		a, err := NewBaseAgent(context.Background(), TodoPlannerExecutionAgentType, "remote-step", remoteToolsTestModel{}, "", nil, nil, nil, false, SimpleAgent, nil, "", "", "remote-tools-test", 0, "", 1, "openai", loggerv2.NewNoop(), false, nil, 0, false, nil, nil, session, t.TempDir(), false, false, false, "", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !late && a.runtime.Coding.AgentToolsMode != "mcp_only" {
			t.Fatal("remote construction did not restrict tools")
		}
		if late {
			a.runtime.Coding.AgentToolsMode = "full"
			common.SetSessionWorkflowPath(session, "Workflow/remote")
		}
		if err := a.finalizeDefinition(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := a.runtime.Coding.AgentToolsMode; got != "mcp_only" {
			t.Fatalf("finalized mode = %q", got)
		}
		// Identity rebuilds and resumed agents must keep the restriction too.
		a.runtime.Coding.AgentToolsMode = "full"
		if err := a.replaceDefinition(context.Background(), a.definition, true); err != nil {
			t.Fatal(err)
		}
		if got := a.runtime.Coding.AgentToolsMode; got != "mcp_only" {
			t.Fatalf("rebuilt mode = %q", got)
		}
		_ = a.session.Close()
		_ = a.agent.Close()
	}
}
