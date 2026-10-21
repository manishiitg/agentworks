package server

import (
	"strings"
	"testing"
	"time"

	agent "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentwrapper"
)

// clearTuningEnv unsets every env var the shared tuning reads so tests see
// pure defaults regardless of the developer's shell.
func clearTuningEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"TOOL_EXECUTION_TIMEOUT",
		"ENABLE_PARALLEL_TOOL_EXECUTION",
	} {
		t.Setenv(name, "")
	}
}

func TestApplySharedLLMAgentTuningDefaults(t *testing.T) {
	clearTuningEnv(t)
	var cfg agent.LLMAgentConfig
	applySharedLLMAgentTuning(&cfg, &QueryRequest{}, nil)

	if !cfg.EnableParallelToolExecution {
		t.Error("parallel tool execution should default to enabled")
	}
	if cfg.ToolTimeout != 0 {
		t.Errorf("ToolTimeout default = %v, want 0", cfg.ToolTimeout)
	}
}

func TestApplySharedLLMAgentTuningDisablesClaudePrivateMemoryForEveryAgent(t *testing.T) {
	clearTuningEnv(t)
	claude := agent.LLMAgentConfig{
		Provider:                     "claude-code",
		CodingAgentSecretEnvironment: map[string]string{"SECRET_EXISTING": "kept"},
	}
	applySharedLLMAgentTuning(&claude, &QueryRequest{}, nil)
	if got := claude.CodingAgentSecretEnvironment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"]; got != "1" {
		t.Fatalf("Claude auto-memory control = %q, want 1", got)
	}
	if got := claude.CodingAgentSecretEnvironment["DISABLE_AUTOUPDATER"]; got != "1" {
		t.Fatalf("Claude must not update itself in place, got %q", got)
	}
	if claude.CodingAgentSecretEnvironment["SECRET_EXISTING"] != "kept" {
		t.Fatal("adding the Claude control replaced the existing scoped environment")
	}

	codex := agent.LLMAgentConfig{Provider: "codex-cli"}
	applySharedLLMAgentTuning(&codex, &QueryRequest{}, nil)
	if _, exists := codex.CodingAgentSecretEnvironment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"]; exists {
		t.Fatal("Claude-specific control was added to a non-Claude provider")
	}
}

func TestApplySharedLLMAgentTuningPriority(t *testing.T) {
	clearTuningEnv(t)
	// Env layer
	t.Setenv("ENABLE_PARALLEL_TOOL_EXECUTION", "false")
	t.Setenv("TOOL_EXECUTION_TIMEOUT", "90s")

	// Env beats defaults.
	var envCfg agent.LLMAgentConfig
	applySharedLLMAgentTuning(&envCfg, &QueryRequest{}, nil)
	if envCfg.EnableParallelToolExecution {
		t.Error("env should disable parallel tool execution")
	}
	if envCfg.ToolTimeout != 90*time.Second {
		t.Errorf("ToolTimeout = %v, want 90s", envCfg.ToolTimeout)
	}
}

// TestRootAndSubAgentTuningMatch locks the invariant that motivated the
// extraction: given the same request, the root chat agent and a delegated
// sub-agent resolve identical tuning (sub-agents just have no preset layer).
func TestRootAndSubAgentTuningMatch(t *testing.T) {
	clearTuningEnv(t)
	t.Setenv("TOOL_EXECUTION_TIMEOUT", "90s")

	req := &QueryRequest{}

	var rootCfg, subCfg agent.LLMAgentConfig
	applySharedLLMAgentTuning(&rootCfg, req, nil)
	applySharedLLMAgentTuning(&subCfg, req, nil)

	if rootCfg.EnableParallelToolExecution != subCfg.EnableParallelToolExecution ||
		rootCfg.ToolTimeout != subCfg.ToolTimeout {
		t.Errorf("root and sub-agent tuning diverged:\nroot: %+v\nsub:  %+v", rootCfg, subCfg)
	}
	if subCfg.ToolTimeout != 90*time.Second {
		t.Errorf("env values not applied: %v", subCfg.ToolTimeout)
	}
}

func TestBuildSecretNamesPrompt(t *testing.T) {
	type secret = struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if got := buildSecretNamesPrompt(nil); got != "" {
		t.Errorf("empty secrets should produce empty prompt, got %q", got)
	}
	prompt := buildSecretNamesPrompt([]secret{{Name: "API_KEY", Value: "shh"}})
	if !strings.Contains(prompt, "SECRET_API_KEY") {
		t.Errorf("prompt should reference SECRET_API_KEY, got %q", prompt)
	}
	if strings.Contains(prompt, "shh") {
		t.Error("prompt must never contain secret values")
	}
}
