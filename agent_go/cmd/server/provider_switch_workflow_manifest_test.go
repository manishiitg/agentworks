package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
)

// A Builder chat whose workflow names its own LLM runs on the manifest's provider; the request's provider (the Models page default) is never used. Comparing it
// with the retained CLI called every send a provider change, so a running Codex chat answered "queued" and was relaunched (local app, 2026-10-04).
func TestWorkflowManifestDecidesProviderOfABuilderChat(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	writeManifest := func(llm map[string]interface{}) {
		doc := map[string]interface{}{
			"id": "wf-w", "label": "W", "created_by": "alice",
			"access": map[string]interface{}{"owners": []string{"alice"}},
		}
		if llm != nil {
			doc["capabilities"] = map[string]interface{}{"llm_config": llm}
		}
		data, _ := json.Marshal(doc)
		env.mock.mu.Lock()
		env.mock.files["Workflow/w/workflow.json"] = string(data)
		env.mock.mu.Unlock()
	}
	claudeRequest := func(source string) QueryRequest {
		return QueryRequest{
			AgentMode: "workflow_phase", SelectedFolder: "Workflow/w", Provider: "claude-code", LLMConfigSource: source,
			LLMConfig: &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: "claude-code", ModelID: "sonnet"}},
		}
	}
	ctx := context.Background()

	writeManifest(map[string]interface{}{"schema_version": 1, "mode": "provider_profile", "provider": "codex-cli"})
	if !env.api.workflowManifestDecidesProvider(ctx, claudeRequest("")) {
		t.Error("a workflow with its own LLM decides the provider: the request's claude-code is not what runs")
	}
	if env.api.workflowManifestDecidesProvider(ctx, claudeRequest(llmConfigSourceAgentProfile)) {
		t.Error("a product profile's request LLM wins over the manifest")
	}
	notWorkflowChat := claudeRequest("")
	notWorkflowChat.AgentMode = "multi-agent"
	if env.api.workflowManifestDecidesProvider(ctx, notWorkflowChat) {
		t.Error("only Builder chats follow the manifest")
	}

	writeManifest(nil)
	if env.api.workflowManifestDecidesProvider(ctx, claudeRequest("")) {
		t.Error("a workflow without its own LLM runs on the request's provider")
	}
}
