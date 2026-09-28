//go:build coding_cli_p0_live

package types

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

var (
	codingCLIP0ProvidersFlag     = flag.String("coding-cli-p0-providers", "claude-code,codex-cli,cursor-cli,pi-cli,muse-cli,agy-cli", "comma-separated live coding CLI providers")
	codingCLIP0WorkspaceAPIFlag  = flag.String("coding-cli-p0-workspace-api", "http://127.0.0.1:18744", "live workspace API URL")
	codingCLIP0WorkspaceDocsFlag = flag.String("coding-cli-p0-workspace-docs", "", "absolute workspace-docs path")
)

// TestCodingCLIWorkflowP0CompletionAdvancesNextStep is the application-level
// P0 contract. Adapter tests prove that a CLI can finish a turn; this test
// proves AgentWorks observes that completion, persists only the final assistant
// message, and starts the next workflow step. Both steps must perform real file
// operations through mcpagent's api-bridge; a tool-less CLI run is not P0
// workflow evidence because coding CLIs render and complete differently while
// MCP calls are active. A provider is not workflow-certified if this test fails.
func TestCodingCLIWorkflowP0CompletionAdvancesNextStep(t *testing.T) {
	providers := codingCLIP0Providers(t)
	requested := requestedCodingCLIP0Providers(t, providers)
	for _, provider := range requested {
		provider := provider
		t.Run(provider.name, func(t *testing.T) {
			t.Cleanup(func() { provider.cleanup(context.Background()) })
			runCodingCLIWorkflowP0(t, provider)
		})
	}
}

func TestCodingCLIWorkflowP0ProviderMatrix(t *testing.T) {
	providers := codingCLIP0Providers(t)
	for _, name := range []string{"claude-code", "codex-cli", "cursor-cli", "pi-cli", "muse-cli", "agy-cli"} {
		provider, ok := providers[name]
		if !ok {
			t.Fatalf("active coding CLI %s is missing from the workflow P0 matrix", name)
		}
		if provider.provider == "" || provider.model == "" || provider.requiredBin == "" || provider.cleanup == nil {
			t.Fatalf("coding CLI %s has an incomplete workflow P0 definition: %#v", name, provider)
		}
	}
	if len(providers) != 6 {
		t.Fatalf("workflow P0 matrix has %d providers, want exactly the six active coding CLIs", len(providers))
	}
}

type codingCLIP0Provider struct {
	connectionID string
	name         string
	provider     llm.Provider
	model        string
	requiredBin  string
	apiKeys      *llm.ProviderAPIKeys
	cleanup      func(context.Context)
}

func codingCLIP0Providers(t *testing.T) map[string]codingCLIP0Provider {
	t.Helper()
	optional := func(value string) *string {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil
		}
		return &value
	}
	model := func(env, fallback string) string {
		if value := strings.TrimSpace(os.Getenv(env)); value != "" {
			return value
		}
		return fallback
	}
	piKey := firstNonEmptyEnv("PI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY")

	return map[string]codingCLIP0Provider{
		"claude-code": {
			name:        "claude-code",
			provider:    llm.ProviderClaudeCode,
			model:       model("CLAUDE_CODE_WORKFLOW_P0_MODEL", "claude-haiku-4-5-20251001"),
			requiredBin: "claude",
			apiKeys:     &llm.ProviderAPIKeys{ClaudeCodeOAuthToken: optional(os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"))},
			cleanup:     func(ctx context.Context) { _ = llmproviders.CleanupClaudeCodeTmuxSessions(ctx) },
		},
		"codex-cli": {
			name:        "codex-cli",
			provider:    llm.ProviderCodexCLI,
			model:       model("CODEX_CLI_WORKFLOW_P0_MODEL", "gpt-5.6-luna"),
			requiredBin: "codex",
			apiKeys:     &llm.ProviderAPIKeys{CodexCLI: optional(os.Getenv("CODEX_API_KEY"))},
			cleanup:     func(ctx context.Context) { _ = llmproviders.CleanupCodexCLIInteractiveSessions(ctx) },
		},
		"cursor-cli": {
			name:        "cursor-cli",
			provider:    llm.ProviderCursorCLI,
			model:       model("CURSOR_CLI_WORKFLOW_P0_MODEL", "composer-2.5"),
			requiredBin: "cursor-agent",
			apiKeys:     &llm.ProviderAPIKeys{CursorCLI: optional(os.Getenv("CURSOR_API_KEY"))},
			cleanup:     func(ctx context.Context) { _ = llmproviders.CleanupCursorCLIInteractiveSessions(ctx) },
		},
		"pi-cli": {
			name:        "pi-cli",
			provider:    llm.ProviderPiCLI,
			model:       model("PI_CLI_WORKFLOW_P0_MODEL", "google/gemini-3.5-flash"),
			requiredBin: "pi",
			apiKeys:     &llm.ProviderAPIKeys{PiCLI: optional(piKey)},
			cleanup:     func(ctx context.Context) { _ = llmproviders.CleanupPiCLIInteractiveSessions(ctx) },
		},
		"muse-cli": {
			name:        "muse-cli",
			provider:    llm.ProviderMuseCLI,
			model:       model("MUSE_CLI_WORKFLOW_P0_MODEL", "muse-spark-1.3-contributor"),
			requiredBin: "muse",
			apiKeys:     &llm.ProviderAPIKeys{MuseCLI: optional(os.Getenv("META_API_KEY"))},
			cleanup:     func(ctx context.Context) { _ = llmproviders.CleanupMuseCLIInteractiveSessions(ctx) },
		},
		"agy-cli": {
			name:        "agy-cli",
			provider:    llm.ProviderAgyCLI,
			model:       model("AGY_CLI_WORKFLOW_P0_MODEL", "gemini-3.8-flash-high"),
			requiredBin: "agy",
			apiKeys:     &llm.ProviderAPIKeys{AgyCLI: optional(os.Getenv("GEMINI_API_KEY"))},
			cleanup:     func(ctx context.Context) { _ = llmproviders.CleanupAgyCLIInteractiveSessions(ctx) },
		},
	}
}

func requestedCodingCLIP0Providers(t *testing.T, providers map[string]codingCLIP0Provider) []codingCLIP0Provider {
	t.Helper()
	requested := strings.TrimSpace(*codingCLIP0ProvidersFlag)
	if requested == "" || requested == "all" {
		requested = "claude-code,codex-cli,cursor-cli,pi-cli,muse-cli,agy-cli"
	}

	var out []codingCLIP0Provider
	for _, name := range strings.Split(requested, ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		provider, ok := providers[name]
		if !ok {
			t.Fatalf("unknown -coding-cli-p0-providers entry %q", name)
		}
		if _, err := exec.LookPath(provider.requiredBin); err != nil {
			if provider.name == "pi-cli" {
				if _, npxErr := exec.LookPath("npx"); npxErr == nil {
					out = append(out, provider)
					continue
				}
			}
			t.Fatalf("P0 provider %s requires %s in PATH: %v", provider.name, provider.requiredBin, err)
		}
		if provider.name == "pi-cli" && provider.apiKeys.PiCLI == nil {
			t.Fatal("P0 provider pi-cli requires PI_API_KEY, GEMINI_API_KEY, or GOOGLE_API_KEY")
		}
		out = append(out, provider)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func runCodingCLIWorkflowP0(t *testing.T, provider codingCLIP0Provider) {
	t.Helper()
	for _, bin := range []string{"tmux", "node"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("P0 workflow requires %s in PATH: %v", bin, err)
		}
	}
	requireLocalMCPBridgeForPiWorkflowE2E(t)

	wsAPI := strings.TrimSpace(*codingCLIP0WorkspaceAPIFlag)
	t.Setenv("WORKSPACE_API_URL", wsAPI)
	if err := requireWorkspaceAPIReachable(wsAPI); err != nil {
		t.Fatalf("P0 workspace API at %s is unreachable: %v", wsAPI, err)
	}
	wsRoot := strings.TrimSpace(*codingCLIP0WorkspaceDocsFlag)
	if wsRoot == "" {
		wsRoot = "/Users/mipl/ai-work/mcp-agent-builder-go/workspace-docs"
	}
	t.Setenv("WORKSPACE_DOCS_PATH", wsRoot)

	relWorkspace := "Workflow/_p0_cli_" + provider.name + "_" + filepath.Base(t.TempDir())
	workspaceDisk := filepath.Join(wsRoot, relWorkspace)
	if os.Getenv("KEEP_E2E_WORKSPACE") == "" {
		t.Cleanup(func() { _ = os.RemoveAll(workspaceDisk) })
	}
	if err := os.MkdirAll(filepath.Join(workspaceDisk, "planning"), 0o755); err != nil {
		t.Fatalf("mkdir planning: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceDisk, "variables"), 0o755); err != nil {
		t.Fatalf("mkdir variables: %v", err)
	}
	firstBridgeToken := "P0_BRIDGE_FIRST_" + strings.ToUpper(strings.ReplaceAll(provider.name, "-", "_"))
	secondBridgeToken := "P0_BRIDGE_SECOND_" + strings.ToUpper(strings.ReplaceAll(provider.name, "-", "_"))
	// Workflow shell writes are intentionally scoped to the active step output
	// folder. The following step may read the shared execution tree, but it may
	// only write inside its own output folder. Keep the P0 proof inside those
	// production folder-guard boundaries.
	firstBridgeFile := filepath.Join(workspaceDisk, "runs", "iteration-0", "default", "execution", "p0-first", "p0-bridge-first.txt")
	secondBridgeFile := filepath.Join(workspaceDisk, "runs", "iteration-0", "default", "execution", "p0-second", "p0-bridge-second.txt")
	if err := writeJSON(filepath.Join(workspaceDisk, "variables", "variables.json"), map[string]interface{}{
		"variables":       []interface{}{},
		"groups":          []map[string]interface{}{{"name": "default", "values": map[string]string{}, "enabled": true}},
		"extraction_date": time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("write variables: %v", err)
	}
	if err := writeJSON(filepath.Join(workspaceDisk, "planning", "plan.json"), map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"type": "regular", "id": "p0-first", "title": "First P0 turn",
				"description":          fmt.Sprintf("Use the declared MCP bridge shell tool (api-bridge.execute_shell_command / api_bridge_execute_shell_command), never a built-in shell or file tool, to run this exact file operation:\nprintf '%%s\\n' '%s' > '%s' && cat '%s'\nAfter the bridge call succeeds, return only this completion summary and status—do not reproduce the tool name, arguments, command, or output envelope:\nP0_FIRST_COMPLETED %s\nSTATUS: COMPLETED", firstBridgeToken, firstBridgeFile, firstBridgeFile, firstBridgeToken),
				"context_dependencies": []string{}, "context_output": "p0-bridge-first.txt", "next_step_id": "p0-second",
			},
			{
				"type": "regular", "id": "p0-second", "title": "Second P0 turn",
				"description":          fmt.Sprintf("Use the declared MCP bridge shell tool (api-bridge.execute_shell_command / api_bridge_execute_shell_command), never a built-in shell or file tool, to verify the prior file and create the next one:\ntest \"$(cat '%s')\" = '%s' && printf '%%s\\n' '%s' > '%s' && cat '%s'\nAfter the bridge call succeeds, return only this completion summary and status—do not reproduce the tool name, arguments, command, or output envelope:\nP0_SECOND_STARTED_AFTER_FIRST %s\nSTATUS: COMPLETED", firstBridgeFile, firstBridgeToken, secondBridgeToken, secondBridgeFile, secondBridgeFile, secondBridgeToken),
				"context_dependencies": []string{"p0-bridge-first.txt"}, "context_output": "p0-bridge-second.txt", "next_step_id": "end",
			},
		},
	}); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	if err := writeJSON(filepath.Join(workspaceDisk, "planning", "step_config.json"), map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				"id": "p0-first",
				"agent_configs": map[string]interface{}{
					"enabled_custom_tools": []string{"workspace_advanced:execute_shell_command"},
				},
			},
			{
				"id": "p0-second",
				"agent_configs": map[string]interface{}{
					"enabled_custom_tools": []string{"workspace_advanced:execute_shell_command"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("write step config: %v", err)
	}

	model := &workflowtypes.AgentLLMConfig{Provider: string(provider.provider), ModelID: provider.model, ConnectionID: provider.connectionID}
	preset := &workflowtypes.PresetLLMConfig{
		SchemaVersion: workflowtypes.LLMConfigSchemaVersion, Mode: workflowtypes.LLMConfigModeExplicit,
		BuilderLLM: model, PulseLLM: model,
		TieredConfig: &workflowtypes.TieredLLMConfig{Tier1: model, Tier2: model, Tier3: model},
	}
	workflowLLM := &orchestrator.LLMConfig{
		Primary: orchestrator.LLMModel{Provider: string(provider.provider), ModelID: provider.model, ConnectionID: provider.connectionID},
		APIKeys: provider.apiKeys,
	}
	// Match production workflow construction: every execution agent receives the
	// workspace registry, then step_config narrows it to the one bridge tool this
	// P0 contract exercises. Passing nil tools here makes provider behavior decide
	// whether the test works and does not test AgentWorks' actual MCP contract.
	workspaceTools, workspaceExecutors, workspaceCategories, _ := virtualtools.CreateWorkspaceToolRegistryUntyped(virtualtools.WorkspaceToolRegistryConfig{
		WorkspaceAPIURL: wsAPI,
		SessionID:       "coding-cli-p0-" + provider.name,
	})
	wo, err := NewWorkflowOrchestrator("", 0.2, "workflow", loggerv2.NewNoop(), nil, nil, nil, nil, false, workspaceTools, workspaceExecutors, workflowLLM, 6, workspaceCategories, preset)
	if err != nil {
		t.Fatalf("NewWorkflowOrchestrator: %v", err)
	}
	wo.SetExecutionOptions(&stepworkflow.ExecutionOptions{
		SelectedRunFolder: "iteration-0",
		ExecutionStrategy: stepworkflow.ExecutionStrategyStartFromBeginningNoHuman,
		EnabledGroupNames: []string{"default"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if _, err := wo.Execute(ctx, "Run the two-step coding CLI P0 completion contract.", relWorkspace, map[string]interface{}{"workflowStatus": workflowtypes.WorkflowStatusPreVerification}); err != nil {
		t.Fatalf("workflow did not advance after %s completion: %v", provider.name, err)
	}
	assertStepExecutionResultContains(t, workspaceDisk, "p0-first", "P0_FIRST_COMPLETED "+firstBridgeToken)
	assertStepExecutionResultContains(t, workspaceDisk, "p0-second", "P0_SECOND_STARTED_AFTER_FIRST "+secondBridgeToken)
	assertCodingCLIP0BridgeFile(t, firstBridgeFile, firstBridgeToken)
	assertCodingCLIP0BridgeFile(t, secondBridgeFile, secondBridgeToken)
	assertCodingCLIP0CleanStepResult(t, workspaceDisk, "p0-first")
	assertCodingCLIP0CleanStepResult(t, workspaceDisk, "p0-second")
	t.Logf("P0 workflow bridge/final-result/advance contract passed for %s/%s", provider.name, provider.model)
}

func assertCodingCLIP0BridgeFile(t *testing.T, path, want string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("MCP bridge file operation did not create %s: %v", path, err)
	}
	if strings.TrimSpace(string(body)) != want {
		t.Fatalf("MCP bridge file %s = %q, want %q", path, strings.TrimSpace(string(body)), want)
	}
}

func assertCodingCLIP0CleanStepResult(t *testing.T, workspaceDisk, stepID string) {
	t.Helper()
	matches := executionAttemptResultLogs(filepath.Join(workspaceDisk, "runs", "*", "*", "logs", stepID, "execution", "execution-attempt-*-iteration-*.json"))
	if len(matches) == 0 {
		t.Fatalf("%s: no execution result log found", stepID)
	}
	body, err := os.ReadFile(matches[len(matches)-1])
	if err != nil {
		t.Fatalf("%s: read execution result: %v", stepID, err)
	}
	var entry struct {
		ExecutionResult string `json:"execution_result"`
	}
	if err := json.Unmarshal(body, &entry); err != nil {
		t.Fatalf("%s: parse execution result: %v", stepID, err)
	}
	if strings.TrimSpace(entry.ExecutionResult) == "" {
		t.Fatalf("%s: empty execution result", stepID)
	}
	for _, forbidden := range []string{
		"api-bridge.",
		"api_bridge_",
		"execute_shell_command(",
		`"command":`,
		`"stdout":`,
		"ctrl+o to expand",
		"Called\n└",
		"Calling api-bridge",
	} {
		if strings.Contains(entry.ExecutionResult, forbidden) {
			t.Fatalf("%s: persisted final result leaked MCP/TUI trail %q:\n%s", stepID, forbidden, entry.ExecutionResult)
		}
	}
}
