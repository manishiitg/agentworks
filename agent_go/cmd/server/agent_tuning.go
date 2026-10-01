package server

import (
	"os"
	"strconv"
	"strings"
	"time"

	agent "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentwrapper"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// applySharedLLMAgentTuning fills the LLMAgentConfig fields whose resolution
// must be identical for the root chat agent (handleQuery) and delegated
// sub-agents: tool timeout, context summarization,
// context editing, large-output offloading, and parallel tool execution.
//
// Priority for each field: request > preset (when non-nil) > environment >
// default. Sub-agents pass preset=nil — they inherit from the parent request
// and environment only.
//
// This is the single place to change these defaults. Before this helper the
// same ~200 lines of closures were duplicated in both config literals and had
// already started to drift.
func applySharedLLMAgentTuning(cfg *agent.LLMAgentConfig, req *QueryRequest, preset *workflowtypes.PresetLLMConfig) {
	// AgentWorks owns durable memory through product/workflow stores. Claude
	// Code's provider-private auto-memory lives outside the governed workspace,
	// is invisible to other providers, and can conflict with those stores. Keep
	// it disabled for every AgentWorks Claude session, including delegated agents.
	if strings.EqualFold(strings.TrimSpace(string(cfg.Provider)), "claude-code") {
		if cfg.CodingAgentSecretEnvironment == nil {
			cfg.CodingAgentSecretEnvironment = make(map[string]string)
		}
		cfg.CodingAgentSecretEnvironment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "1"
	}
	cfg.ToolTimeout = resolveToolTimeout()

	// Context offloading: tool outputs larger than this (tokens) go to the filesystem.
	cfg.LargeOutputThreshold = envPositiveInt("LARGE_OUTPUT_THRESHOLD", 0) // 0 = library default (10000)

	cfg.EnableParallelToolExecution = os.Getenv("ENABLE_PARALLEL_TOOL_EXECUTION") != "false"
}

// resolveToolTimeout reads TOOL_EXECUTION_TIMEOUT; 0 means no per-tool timeout.
func resolveToolTimeout() time.Duration {
	if envVal := os.Getenv("TOOL_EXECUTION_TIMEOUT"); envVal != "" {
		if timeout, err := time.ParseDuration(envVal); err == nil {
			return timeout
		}
	}
	return 0
}

// envPositiveInt returns the env var as an int when it parses positive,
// otherwise the fallback.
func envPositiveInt(name string, fallback int) int {
	if envVal := os.Getenv(name); envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil && v > 0 {
			return v
		}
	}
	return fallback
}

// buildSecretNamesPrompt renders the system-prompt section listing available
// secret names (never values — those live in the current coding agent's shell
// environment).
// Shared by the root chat agent and delegated sub-agents so both describe
// secrets to the LLM identically. Returns "" when there are no secrets.
func buildSecretNamesPrompt(secrets []struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}) string {
	if len(secrets) == 0 {
		return ""
	}
	var secretNames []string
	for _, s := range secrets {
		secretNames = append(secretNames, "`SECRET_"+s.Name+"`")
	}
	return "\n## Secrets\n\nAvailable in the authorized shell environment: " + strings.Join(secretNames, ", ") + ". Use injected references; never ask for, print, log or save values.\n"
}
