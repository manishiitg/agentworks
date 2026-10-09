package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

const cursorOnlyPublishedList = `[{"id":"agentworks-default","name":"cursor-cli (cursor-cli)","provider":"cursor-cli","model_id":"cursor-cli"}]`

// With an explicitly published list, a locked deployment offers exactly that
// list: a workflow's saved OpenAI config must not keep running on OpenAI just
// because the platform knows the model.
func TestLockedLLMRestrictsToThePublishedListWhenOneIsConfigured(t *testing.T) {
	t.Setenv("LLM_CONFIG_LOCKED", "true")
	t.Setenv("DEFAULT_PUBLISHED_LLMS", cursorOnlyPublishedList)
	t.Setenv("DEFAULT_PUBLISHED_LLMS_PATH", "")

	if !isAllowedDefaultLLM("cursor-cli", "cursor-cli") {
		t.Fatal("the published entry itself must be allowed")
	}
	if isAllowedDefaultLLM("openai", "gpt-5.2") {
		t.Fatal("a known model of another provider must be refused when a published list is configured")
	}
	if isAllowedDefaultLLM("cursor-cli", "gpt-5") {
		t.Fatal("another model of the published provider must be refused too")
	}
}

// Without an explicit list the historical behaviour stands: any model the
// platform knows for a locked provider is accepted.
func TestLockedLLMWithoutPublishedListKeepsKnownModels(t *testing.T) {
	t.Setenv("LLM_CONFIG_LOCKED", "true")
	t.Setenv("DEFAULT_PUBLISHED_LLMS", "")
	t.Setenv("DEFAULT_PUBLISHED_LLMS_PATH", "")

	if !isAllowedDefaultLLM("cursor-cli", "gpt-5") {
		t.Fatal("a curated cursor-cli model must stay allowed in the legacy mode")
	}
}

// A product profile's own binding (Video Studio pins claude-code) is operator
// configuration and wins under the lock; a user's or a workflow's choice that
// is not published falls back to the server default.
func TestResolveLockedLLMHonoursProfileBindingsAndFallsBackOtherwise(t *testing.T) {
	t.Setenv("LLM_CONFIG_LOCKED", "true")
	t.Setenv("DEFAULT_PUBLISHED_LLMS", cursorOnlyPublishedList)
	t.Setenv("DEFAULT_PUBLISHED_LLMS_PATH", "")

	profileBound := &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: "claude-code", ModelID: "claude-sonnet-5-5"}}
	if p, m := resolveLockedLLM(profileBound, llmConfigSourceAgentProfile); p != "claude-code" || m != "claude-sonnet-5-5" {
		t.Fatalf("profile binding must win under the lock, got %s/%s", p, m)
	}

	userChoice := &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: "openai", ModelID: "gpt-5.2"}}
	p, m := resolveLockedLLM(userChoice, "")
	if p == "openai" {
		t.Fatalf("an unpublished user choice must not run under the lock, got %s/%s", p, m)
	}
	dp, dm := getPrimaryProviderAndModelFromDefaults()
	if p != dp || m != dm {
		t.Fatalf("unpublished choice must fall back to the server default %s/%s, got %s/%s", dp, dm, p, m)
	}

	published := &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: "cursor-cli", ModelID: "cursor-cli"}}
	if p, m := resolveLockedLLM(published, ""); p != "cursor-cli" || m != "cursor-cli" {
		t.Fatalf("the published entry must be accepted, got %s/%s", p, m)
	}
}

// A workflow's saved llm_config is what the Builder chat, scheduled runs and
// step tiers actually use, so the lock must rewrite it. Locking to a
// coding-agent provider (Cursor) means locking to that provider's role
// profile -- all tiers on Cursor's auto routing as of 2026-09-03 (a live server A
// run hit quota_exhausted on the previously-pinned grok-4.6 with no
// fallback) -- not flattening every role onto one hardcoded model; the saved
// config is never mutated and passes through untouched off-lock.
func TestLockedPresetLLMConfigLocksToThePublishedProvidersProfile(t *testing.T) {
	t.Setenv("LLM_CONFIG_LOCKED", "true")
	t.Setenv("DEFAULT_PUBLISHED_LLMS", cursorOnlyPublishedList)
	t.Setenv("DEFAULT_PUBLISHED_LLMS_PATH", "")

	saved := &workflowtypes.PresetLLMConfig{
		SchemaVersion: 2,
		Mode:          workflowtypes.LLMConfigModeExplicit,
		BuilderLLM:    &workflowtypes.AgentLLMConfig{Provider: "claude-code", ModelID: "claude-sonnet-5-5"},
		TieredConfig:  &workflowtypes.TieredLLMConfig{Tier1: &workflowtypes.AgentLLMConfig{Provider: "openai", ModelID: "gpt-5.2"}},
	}
	got := lockedPresetLLMConfig(saved)
	if got == saved || got.Mode != workflowtypes.LLMConfigModeProviderProfile || got.Provider != "cursor-cli" {
		t.Fatalf("expected a provider-profile lock on cursor-cli, got %+v", got)
	}
	if got.BuilderLLM != nil || got.TieredConfig != nil || got.PulseLLM != nil {
		t.Fatal("explicit roles must be dropped so the provider profile resolves them")
	}
	builder, tiers, ok := workflowtypes.ResolveProviderProfileConfig(got)
	if !ok || builder == nil || tiers == nil {
		t.Fatal("provider profile must resolve")
	}
	if builder.Provider != "cursor-cli" || builder.ModelID != "auto" {
		t.Fatalf("builder = %+v, want cursor-cli/auto", builder)
	}
	if tiers.Tier1.ModelID != "auto" || tiers.Tier2.ModelID != "auto" || tiers.Tier3.ModelID != "auto" {
		t.Fatalf("tiers = %+v/%+v/%+v, want auto / auto / auto", tiers.Tier1, tiers.Tier2, tiers.Tier3)
	}
	if saved.BuilderLLM.Provider != "claude-code" {
		t.Fatal("the saved config must not be mutated")
	}
	// The profile's own models are published by implication.
	if !isAllowedDefaultLLM("cursor-cli", "auto") {
		t.Fatal("cursor-cli/auto must be allowed under the lock")
	}
	if isAllowedDefaultLLM("cursor-cli", "gpt-5") || isAllowedDefaultLLM("cursor-cli", "composer-2.5") || isAllowedDefaultLLM("openai", "gpt-5.2") {
		t.Fatal("models outside the published provider's profile must stay refused")
	}
	if lockedPresetLLMConfig(nil) == nil {
		t.Fatal("a workflow with no llm_config still gets the locked profile")
	}

	t.Setenv("LLM_CONFIG_LOCKED", "false")
	if got := lockedPresetLLMConfig(saved); got != saved {
		t.Fatal("off-lock the saved config must pass through untouched")
	}
}
