package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	mcpagent "github.com/manishiitg/mcpagent/agent"
)

type stepPromptCaptureModel struct{}

func (m *stepPromptCaptureModel) GetModelID() string { return "fixture" }
func (m *stepPromptCaptureModel) GetModelMetadata(string) (*llmtypes.ModelMetadata, error) {
	return &llmtypes.ModelMetadata{}, nil
}
func (m *stepPromptCaptureModel) GenerateContent(context.Context, []llmtypes.MessageContent, ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	panic("prompt inspection must not invoke a provider")
}

// Exercise the app constructor and actual SDK outgoing prompt. The provider is
// deterministic; no browser, CLI or external model is started.
func TestWorkflowBaseUsesOnDemandTransportSkill(t *testing.T) {
	config := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(config, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	model := &stepPromptCaptureModel{}
	tool := mcpagent.ToolDefinition{Name: "query_workflow_db", Description: "Read workflow facts.", InputSchema: map[string]interface{}{"type": "object"}, Execute: func(context.Context, map[string]interface{}) (string, error) { return "facts", nil }}
	base, err := NewBaseAgent(t.Context(), TodoPlannerExecutionAgentType, "Fixture", model, "Workflow orientation.", []string{mcpclient.NoServers}, []mcpagent.ToolDefinition{tool}, nil, true, AgentMode(""), nil, "", config, "fixture", 0, "auto", 2, "", loggerv2.NewNoop(), false, false, nil, nil, "", "", false, false, false, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	view := base.agent.Definition()
	found := false
	for _, skill := range view.SkillDefinitions {
		if skill.Name == "runtime-http-tools" {
			found = strings.Contains(skill.Content, "--fail-with-body") && strings.Contains(skill.Content, "get_api_spec")
		}
	}
	if !found {
		t.Fatal("transport procedures were not available as a skill")
	}
	admitted := false
	for _, tool := range view.Tools {
		if tool.Name == "database" {
			admitted = true
		}
	}
	if !admitted {
		t.Fatal("progressive discovery removed an admitted workflow tool")
	}
	prompt := mcpagent.ReadAgentSystemPrompt(t.Context(), base.agent)
	if !strings.Contains(prompt, "Workflow orientation.") || !strings.Contains(prompt, "runtime-http-tools") || !strings.Contains(prompt, "<available_tools>") || strings.Contains(prompt, "--fail-with-body") {
		t.Fatalf("outgoing prompt lost orientation/tool inventory or inlined transport procedures: %s", prompt)
	}
	t.Logf("OUTGOING_PROMPT_BYTES=%d", len(prompt))
}

// Full CLI is a step-execution choice; a Builder/reviewer or a runtime without
// the dedicated step output link must retain its existing tool mode.
func TestNativeStepToolsStayScopedToLinkedExecutors(t *testing.T) {
	for _, kind := range []AgentType{TodoPlannerExecutionAgentType, GenericExecutionAgentType, DelegatingAgentType} {
		if got := workflowExecutionNativeToolsMode(kind, true, true, true); got != "full" {
			t.Fatalf("step %s mode=%q", kind, got)
		}
		if got := workflowExecutionNativeToolsMode(kind, true, true, false); got != "" {
			t.Fatalf("unlinked executor %s received native mode", kind)
		}
	}
	for _, kind := range []AgentType{TodoPlannerInteractiveWorkshopAgentType, TodoPlannerExecutionQAAgentType, ConditionalAgentType, TodoPlannerPlanningAgentType} {
		if got := workflowExecutionNativeToolsMode(kind, true, true, true); got != "" {
			t.Fatalf("non-executor %s received native mode", kind)
		}
	}
}

// PLAT-746-356: native steps previously lost the exact environment their
// bridge shell held; copying it must never opt Builder/Run chats into it.
func TestNativeWorkflowChildGetsItsOwnBridgeEnvironment(t *testing.T) {
	config := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(config, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		kind   AgentType
		linked bool
		want   bool
	}{
		{"step", TodoPlannerExecutionAgentType, true, true},
		{"unlinked-chat", GenericExecutionAgentType, false, false},
		{"builder", TodoPlannerInteractiveWorkshopAgentType, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := "native-env-" + tc.name
			common.SetSessionShellEnv(session, map[string]string{"STEP_OUTPUT_DIR": "/run/current/output", "SECRET_SELECTED": "selected", "VAR_TOPIC": "example"})
			t.Cleanup(func() { common.ClearSessionShellConfig(session) })
			output := ""
			if tc.linked {
				output = t.TempDir()
			}
			base, err := NewBaseAgent(t.Context(), tc.kind, "Fixture", &stepPromptCaptureModel{}, "Work.", []string{mcpclient.NoServers}, nil, nil, true, AgentMode(""), nil, "", config, "fixture", 0, "auto", 2, "", loggerv2.NewNoop(), false, false, nil, nil, session, t.TempDir(), false, true, true, output, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer base.Close()
			env := base.runtime.Coding.SecretEnvironment
			if !tc.want {
				if len(env) != 0 {
					t.Fatal("non-execution chat received child environment")
				}
				return
			}
			for key, want := range common.GetSessionShellEnv(session) {
				if env[key] != want {
					t.Errorf("native child lost %s", key)
				}
			}
			// The constructor owns a snapshot, not a map that a later child can mutate.
			common.SetSessionShellEnv(session, map[string]string{"STEP_OUTPUT_DIR": "/run/replacement/output"})
			if env["STEP_OUTPUT_DIR"] != "/run/current/output" {
				t.Fatal("child environment changed after construction")
			}
		})
	}
}
