package server

import (
	"context"
	"testing"
)

func TestAutoPublishedCodingAgentLLMsIncludeConcreteClaudeAndCodexModels(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "claude-code,codex-cli")
	withFakeExecutable(t, "claude")
	withFakeExecutable(t, "codex")
	withClaudeStatus(t, `{"loggedIn":true}`, nil)
	withCodexStatus(t, "Logged in using ChatGPT", nil)

	llms := buildAutoPublishedCodingAgentLLMs(context.Background(), nil)

	for _, want := range []string{
		"auto:claude-code:claude-haiku-4-5-20251001:high",
		"auto:claude-code:claude-haiku-4-5-20251001:max",
		"auto:claude-code:claude-sonnet-5-5:high",
		"auto:claude-code:claude-opus-5-5:max",
		"auto:codex-cli:gpt-5.4-mini:high",
	} {
		if !containsPublishedLLMID(llms, want) {
			t.Fatalf("auto-published ids missing %q; got %#v", want, publishedLLMIDs(llms))
		}
	}
	if containsPublishedLLMModel(llms, "claude-code", "claude-code") {
		t.Fatalf("auto-published entries should not include claude-code alias: %#v", publishedLLMIDs(llms))
	}
	if containsPublishedLLMModel(llms, "claude-code", "claude-opus-4-7") {
		t.Fatalf("auto-published entries should not include removed Claude Opus 4.7: %#v", publishedLLMIDs(llms))
	}
	for _, removed := range []string{"gpt-5.4", "gpt-5.5"} {
		if containsPublishedLLMModel(llms, "codex-cli", removed) {
			t.Fatalf("auto-published entries should not include removed model %s", removed)
		}
	}
	if containsPublishedLLMModel(llms, "codex-cli", "codex-cli") {
		t.Fatalf("auto-published entries should not include codex-cli alias: %#v", publishedLLMIDs(llms))
	}
}

func TestAutoPublishedCodingAgentLLMsSkipSavedDuplicate(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "claude-code")
	withFakeExecutable(t, "claude")
	withClaudeStatus(t, `{"loggedIn":true}`, nil)

	saved := []StoredPublishedLLM{{
		ID:       "user-claude-opus-high",
		Name:     "Custom Claude Opus High",
		Provider: "claude-code",
		ModelID:  "claude-opus-5-5",
		Options:  map[string]interface{}{"reasoning_effort": "high"},
	}}

	llms := buildAutoPublishedCodingAgentLLMs(context.Background(), saved)
	if containsPublishedLLMID(llms, "auto:claude-code:claude-opus-5-5:high") {
		t.Fatalf("auto-published duplicate was generated despite saved override: %#v", publishedLLMIDs(llms))
	}
	if !containsPublishedLLMID(llms, "auto:claude-code:claude-opus-5-5:max") {
		t.Fatalf("non-duplicate effort should still be generated: %#v", publishedLLMIDs(llms))
	}
}

func TestSanitizePersistedPublishedLLMsDropsAutoEntries(t *testing.T) {
	llms := sanitizePersistedPublishedLLMs([]StoredPublishedLLM{
		{
			ID:       "auto:claude-code:claude-opus-4-8:high",
			Name:     "Claude Opus Auto",
			Provider: "claude-code",
			ModelID:  "claude-opus-4-8",
			Options:  map[string]interface{}{"reasoning_effort": "high"},
			Source:   autoPublishedLLMSource,
		},
		{
			ID:       "user-1",
			Name:     "User Codex",
			Provider: "codex-cli",
			ModelID:  "gpt-5.5",
			Options:  map[string]interface{}{"reasoning_effort": "xhigh"},
		},
	})

	if len(llms) != 1 {
		t.Fatalf("persisted count = %d, want 1: %#v", len(llms), llms)
	}
	if llms[0].ID != "user-1" {
		t.Fatalf("persisted id = %q, want user-1", llms[0].ID)
	}
}

func containsPublishedLLMID(llms []StoredPublishedLLM, id string) bool {
	for _, llm := range llms {
		if llm.ID == id {
			return true
		}
	}
	return false
}

func containsPublishedLLMModel(llms []StoredPublishedLLM, provider, modelID string) bool {
	for _, llm := range llms {
		if llm.Provider == provider && llm.ModelID == modelID {
			return true
		}
	}
	return false
}

func publishedLLMIDs(llms []StoredPublishedLLM) []string {
	ids := make([]string, 0, len(llms))
	for _, llm := range llms {
		ids = append(ids, llm.ID)
	}
	return ids
}
