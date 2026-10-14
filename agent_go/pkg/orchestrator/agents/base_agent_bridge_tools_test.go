package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	mcpagent "github.com/manishiitg/mcpagent/agent"
)

func TestPlatformBridgeToolsIncludesReadImageOnlyWhenRegistered(t *testing.T) {
	if got := platformBridgeToolsFromDirectTools([]mcpagent.ToolDefinition{{Name: "send_email"}}); len(got) != 0 {
		t.Fatalf("platform bridge tools without read_image = %v, want none", got)
	}

	got := platformBridgeToolsFromDirectTools([]mcpagent.ToolDefinition{
		{Name: "send_email"},
		{Name: "read_image"},
	})
	if len(got) != 1 || got[0] != "read_image" {
		t.Fatalf("platform bridge tools = %v, want [read_image]", got)
	}
}

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
	base, err := NewBaseAgent(t.Context(), TodoPlannerExecutionAgentType, "Fixture", model, "Workflow orientation.", []string{mcpclient.NoServers}, []mcpagent.ToolDefinition{tool}, nil, true, AgentMode(""), nil, "", config, "fixture", 0, "auto", 2, "", loggerv2.NewNoop(), false, nil, 0, false, nil, nil, "", "", false, false, false, "", nil, nil)
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
		if tool.Name == "query_workflow_db" {
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
