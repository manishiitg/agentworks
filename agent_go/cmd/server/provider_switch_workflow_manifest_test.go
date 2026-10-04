package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
)

// The provider a request names is not always the one that runs: a Builder chat follows its workflow's own LLM, and a locked server replaces a choice that is
// neither a product profile's nor published. Comparing the retained CLI with the raw request provider called every send a provider change, so a running Codex
// chat answered "queued" and was relaunched (local app, 2026-10-04).
func TestEffectiveProviderIsWhatActuallyRuns(t *testing.T) {
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
	if got := env.api.effectiveProviderOf(ctx, claudeRequest("")); got != "codex-cli" {
		t.Errorf("a workflow with its own LLM runs on it, not on the request's claude-code: got %q", got)
	}
	if got := env.api.effectiveProviderOf(ctx, claudeRequest(llmConfigSourceAgentProfile)); got != "claude-code" {
		t.Errorf("a product profile's request LLM wins over the manifest: got %q", got)
	}
	notWorkflowChat := claudeRequest("")
	notWorkflowChat.AgentMode = "multi-agent"
	if got := env.api.effectiveProviderOf(ctx, notWorkflowChat); got != "claude-code" {
		t.Errorf("only Builder chats follow the manifest: got %q", got)
	}

	writeManifest(nil)
	if got := env.api.effectiveProviderOf(ctx, claudeRequest("")); got != "claude-code" {
		t.Errorf("a workflow without its own LLM runs on the request's provider: got %q", got)
	}

	// A locked server runs its default for a choice nobody published, whatever chat it is.
	t.Setenv("LLM_CONFIG_LOCKED", "true")
	lockedProvider, _ := resolveLockedLLM(claudeRequest("").LLMConfig, "")
	if got := env.api.effectiveProviderOf(ctx, notWorkflowChat); got != lockedProvider {
		t.Errorf("locked server: got %q, want the locked answer %q", got, lockedProvider)
	}
	if got := env.api.effectiveProviderOf(ctx, claudeRequest(llmConfigSourceAgentProfile)); got != "claude-code" {
		t.Errorf("locked server still honours a product profile's choice: got %q", got)
	}
}
