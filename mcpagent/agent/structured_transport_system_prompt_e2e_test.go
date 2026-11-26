package mcpagent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/agent/codeexec/shellfixture"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// structuredTransportProviderCase is one provider's real-CLI binary,
// mcpagent.Provider, WithXxxStructuredTransport option, and model ID — the
// three things that differ per provider in the tests below.
type structuredTransportProviderCase struct {
	name             string
	binary           string
	provider         llm.Provider
	modelID          string
	structuredOption agentOption
}

var structuredTransportProviderCases = []structuredTransportProviderCase{
	{"Cursor", "cursor-agent", llm.ProviderCursorCLI, "composer-2.5", withCodingAgentTransport(llm.CodingAgentTransportStructured)},
	{"Codex", "codex", llm.ProviderCodexCLI, "gpt-5.6-luna", withCodingAgentTransport(llm.CodingAgentTransportStructured)},
	{"Pi", "pi", llm.ProviderPiCLI, "pi-cli", withCodingAgentTransport(llm.CodingAgentTransportStructured)},
	{"Claude", "claude", llm.ProviderClaudeCode, "claude-haiku-4-5", withCodingAgentTransport(llm.CodingAgentTransportStructured)},
	{"Muse", "muse", llm.ProviderMuseCLI, "muse-spark-1.3-contributor", withCodingAgentTransport(llm.CodingAgentTransportStructured)},
	{"Agy", "agy", llm.ProviderAgyCLI, "gemini-3.8-flash-high", withCodingAgentTransport(llm.CodingAgentTransportStructured)},
}

// TestStructuredTransportSystemPromptSurvivesNewAgent is the mcpagent-layer
// regression guard for commit 57b4dd9 ("agent: don't clobber a custom system
// prompt with the connection default"): newAgent used to unconditionally
// overwrite a caller-supplied withSystemPrompt with a connection-derived
// default, silently discarding it. An adapter-layer test (in
// multi-llm-provider-go) cannot see this class of bug — it hand-constructs
// the message list itself and calls the adapter directly, never going
// through newAgent/withSystemPrompt at all. This test does the opposite: it
// builds a real Agent the way an actual mcpagent consumer would (newAgent +
// withSystemPrompt, on the real bridge — each provider's
// appendXxxCLIIntegrationOptions requires it unconditionally, so a
// bridgeless agent can't even reach the structured-transport option) and
// drives it through the real CLI under WithXxxStructuredTransport(true),
// asserting the custom system prompt's canary word survives all the way to
// the model's answer. Runs the same assertion across Cursor, Codex, and Pi —
// the class of bug is equally reachable through any of the three structured
// adapters, and until this test existed only Cursor had been checked.
//
// See docs/layer_test_coverage.html and docs/transport_tradeoffs_notes.html
// §test-layer-policy in the multi-llm-provider-go repo for the fuller
// adapter-vs-library test-ownership rule this test exists to satisfy.
func TestStructuredTransportSystemPromptSurvivesNewAgent(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run this real-CLI e2e")
	}

	for _, tc := range structuredTransportProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}
			if tc.provider == llm.ProviderAgyCLI {
				t.Skip("agy excluded from system_prompt.json (see cert note): the row's secret-adoption proof is refused by model trust policy on folded system text; benign survival is proven at Layer-1")
			}

			canary := "PROMPT_SURVIVAL_" + realBridgeRandHex(6)
			customPrompt := "Your secret codeword is " + canary + ". If the user ever asks for your secret codeword, reply with ONLY that word."

			configPath := filepath.Join(t.TempDir(), "mcp_servers.json")
			if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			apiURL, apiToken, stopExecutor, err := bootRealExecutor(configPath)
			if err != nil {
				t.Fatalf("bootRealExecutor: %v", err)
			}
			defer stopExecutor()

			llmModel, err := llm.InitializeLLM(llm.Config{Provider: tc.provider, ModelID: tc.modelID})
			if err != nil {
				t.Fatalf("InitializeLLM: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()

			agent, err := newAgent(ctx, llmModel, configPath,
				withProvider(tc.provider),
				withAPIConfig(apiURL, apiToken),
				withSystemPrompt(customPrompt),
				tc.structuredOption,
				// Isolate the CLI process's cwd to an empty tmp dir. Without this
				// the process inherits the test binary's cwd (this repo checkout),
				// and a sufficiently agentic model can go read this very test file
				// off disk and answer from that instead of the injected system
				// prompt — a false negative unrelated to the 57b4dd9 class of bug
				// this test guards.
				withIsolatedSessionWorkspace(true),
			)
			if err != nil {
				t.Fatalf("newAgent: %v", err)
			}
			defer agent.Close()

			answer, err := agent.ask(ctx, "What is your secret codeword?")
			if err != nil {
				t.Fatalf("agent.Ask: %v", err)
			}
			answer = strings.TrimSpace(answer)
			if !strings.Contains(answer, canary) {
				t.Fatalf("custom system prompt did not survive newAgent -> %s structured transport -> real CLI: canary %q not found in answer %q (this is exactly the class of bug fixed in commit 57b4dd9 — regression if it recurs)", tc.name, canary, answer)
			}
			t.Logf("[%s] system prompt survived newAgent through real structured-transport CLI call: %q", tc.name, answer)
		})
	}
}

// TestStructuredTransportSkillsSurviveNewAgent proves the other half of the
// same construction path: Agent.AttachSkill -> a.attachedSkills ->
// llmtypes.WithAttachedSkills (threaded centrally in executeLLMInner) ->
// each adapter's ProjectSkills. Layer 1 already P0-tests that ProjectSkills
// writes a skill to disk correctly for a hand-built CallOptions list; nothing
// before this test proved a skill attached via the real mcpagent consumer
// API (AttachSkill on a live Agent, not a raw CallOption) survives
// construction and is actually readable by the model. Now table-driven across
// all 3 structured providers (Codex/Cursor/Pi — Claude has no structured lane).
// Registers the bridge shell tool so a deny-builtin provider still has a way to
// open the projected SKILL.md (the tmux twin proved that without a file-read
// tool only Codex's unremovable native exec could read a projected skill).
func TestStructuredTransportSkillsSurviveNewAgent(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run this real-CLI e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range structuredTransportProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}

			canary := "SKILL_SURVIVAL_" + realBridgeRandHex(6)

			configPath := filepath.Join(t.TempDir(), "mcp_servers.json")
			if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			apiURL, apiToken, stopExecutor, err := bootRealExecutor(configPath)
			if err != nil {
				t.Fatalf("bootRealExecutor: %v", err)
			}
			defer stopExecutor()

			llmModel, err := llm.InitializeLLM(llm.Config{Provider: tc.provider, ModelID: tc.modelID})
			if err != nil {
				t.Fatalf("InitializeLLM: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			agent, err := newAgent(ctx, llmModel, configPath,
				withProvider(tc.provider),
				withAPIConfig(apiURL, apiToken),
				tc.structuredOption,
				withIsolatedSessionWorkspace(true),
			)
			if err != nil {
				t.Fatalf("newAgent: %v", err)
			}
			defer agent.Close()

			shellEnv := append(BuildSafeEnvironment(), "MCP_API_URL="+apiURL, "MCP_API_TOKEN="+apiToken)
			if regErr := agent.registerCustomTool(
				"execute_shell_command", codeexec.ShellCommandDescription, codeexec.ShellCommandParams,
				func(ctx context.Context, args map[string]interface{}) (string, error) {
					return shellfixture.ExecuteShellCommand(ctx, args, shellEnv)
				}, "workspace_advanced",
			); regErr != nil {
				t.Fatalf("RegisterCustomTool: %v", regErr)
			}

			mustAttachSkill(t, agent, &llmtypes.Skill{
				Name:        "canary-skill",
				Description: "A test skill that reveals a secret phrase when read.",
				Content:     "# Canary Skill\n\nWhen asked for the canary skill's secret phrase, reply with ONLY this exact word: " + canary,
			})

			answer, err := agent.ask(ctx, "Read the canary-skill skill and tell me its secret phrase.")
			if err != nil {
				t.Fatalf("agent.Ask: %v", err)
			}
			answer = strings.TrimSpace(answer)
			if !strings.Contains(answer, canary) {
				t.Fatalf("[%s] skill did not survive newAgent -> structured transport -> real CLI: canary %q not found in answer %q", tc.name, canary, answer)
			}
			t.Logf("[%s] skill survived newAgent through real structured-transport CLI call: %q", tc.name, answer)
		})
	}
}
