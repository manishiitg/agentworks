package mcpagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/agent/codeexec/shellfixture"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/internal/agentreview"
	"github.com/manishiitg/mcpagent/llm"
)

// toolNamesFromEvents extracts the streamed ToolCallStartEvent names.
func toolNamesFromEvents(evs []*events.AgentEvent) []string {
	var names []string
	for _, ev := range evs {
		if tse, ok := ev.Data.(*events.ToolCallStartEvent); ok {
			names = append(names, tse.ToolName)
		}
	}
	return names
}

// newRealBridgeAgentWithShell builds an Agent for the given provider on the
// REAL bridge but registers a CUSTOM execute_shell_command handler (for fault
// injection). Returns the agent + a cleanup func. shellEnv (with the executor
// URL/token) is passed to the handler so it can perform the real execution
// when it chooses to.
func newRealBridgeAgentWithShell(t *testing.T, ctx context.Context, tc multiTurnProviderCase, workDir string, handler func(ctx context.Context, args map[string]interface{}, shellEnv []string) (string, error)) (*Agent, func()) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "mcp_servers.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	apiURL, apiToken := startRealExecutorServer(t, configPath)
	llmModel, err := llm.InitializeLLM(llm.Config{Provider: tc.provider, ModelID: tc.modelID})
	if err != nil {
		t.Fatalf("InitializeLLM: %v", err)
	}
	sessionID := "toolfail-" + realBridgeRandHex(4)
	agentOpts := []agentOption{
		withProvider(tc.provider), withAPIConfig(apiURL, apiToken),
		withStreaming(true), withCodingAgentWorkingDir(workDir),
		withSessionID(sessionID),
	}
	if tc.provider == llm.ProviderCodexCLI {
		agentOpts = append(agentOpts, withCodingAgentToolsMode(codingAgentToolsMCPOnly))
	}
	var untrust func()
	if tc.provider == llm.ProviderAgyCLI {
		// Peers take the interactive lane from the session id alone; agy's
		// lane switch is the persistent flag, so without it this tmux row
		// would silently run agy on exec. Force the sidecar lane + trust.
		agentOpts = append(agentOpts, tc.persistentOpt(true))
		untrust, err = trustAgyWorkdirForTmuxRow(workDir)
		if err != nil {
			t.Fatalf("trust agy workdir: %v", err)
		}
		keyed, keyErr := agyKeyModeForTmuxRow(untrust)
		if keyErr != nil {
			untrust()
			t.Fatalf("agy key mode: %v", keyErr)
		}
		untrust = keyed
	}
	agent, err := newAgent(ctx, llmModel, configPath, agentOpts...)
	if err != nil {
		if untrust != nil {
			untrust()
		}
		t.Fatalf("newAgent: %v", err)
	}
	shellEnv := append(BuildSafeEnvironment(), "MCP_API_URL="+apiURL, "MCP_API_TOKEN="+apiToken)
	if regErr := agent.registerCustomTool(
		"execute_shell_command", codeexec.ShellCommandDescription, codeexec.ShellCommandParams,
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			return handler(ctx, args, shellEnv)
		},
		"workspace_advanced",
	); regErr != nil {
		_ = agent.Close()
		if untrust != nil {
			untrust()
		}
		t.Fatalf("RegisterCustomTool: %v", regErr)
	}
	return agent, func() {
		_ = agent.Close()
		if tc.provider == llm.ProviderAgyCLI {
			closePersistentInteractiveSession(tc, sessionID)
		}
		if untrust != nil {
			untrust()
		}
	}
}

// TestRealBridgeStreamingToolFailureRecovery proves the stream + turn degrade
// GRACEFULLY when a tool fails MID-STREAM, on every provider: the bridge tool
// fails its first call, the error reaches the model, the model retries the
// SAME command, the stream keeps flowing, and the turn recovers and
// completes. Table-driven across all providers — was Claude-only (see
// docs/layer_test_coverage.html §matrix).
func TestRealBridgeStreamingToolFailureRecovery(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the real-bridge tool-failure e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range multiTurnProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			defer cancel()

			workDir := t.TempDir()
			codeWord := "BUILD_ID_" + realBridgeRandHex(6)
			buildIDPath := filepath.Join(workDir, "build_id.txt")
			if err := os.WriteFile(buildIDPath, []byte(codeWord), 0o600); err != nil {
				t.Fatal(err)
			}

			// Fail the FIRST tool call (mid-stream), really run every call after that.
			var calls int32
			agent, cleanup := newRealBridgeAgentWithShell(t, ctx, tc, workDir,
				func(ctx context.Context, args map[string]interface{}, shellEnv []string) (string, error) {
					if atomic.AddInt32(&calls, 1) == 1 {
						return "", fmt.Errorf("TRANSIENT_TOOL_FAILURE: the tool backend was briefly unavailable; retry the same command")
					}
					return shellfixture.ExecuteShellCommand(ctx, args, shellEnv)
				})
			defer cleanup()

			listener := &recordingAgentEventListener{}
			agent.addEventListener(listener)

			answer, err := agent.ask(ctx, fmt.Sprintf(
				"You are a build assistant with one tool: execute_shell_command. Write one short sentence, then run exactly: cat %s\n"+
					"If a tool call fails with a transient error, RETRY the same command once. Then reply with the build id it printed.", buildIDPath))
			// (1) The turn must complete — a mid-stream tool failure must not crash or hang it.
			if err != nil {
				t.Fatalf("turn errored on a mid-stream tool failure (must degrade gracefully): %v", err)
			}

			nCalls := atomic.LoadInt32(&calls)
			cleanText, _, contentChunks := captureRealBridge(listener.events)
			toolNames := toolNamesFromEvents(listener.events)
			t.Logf("[%s] tool-failure recovery: calls=%d tool-events=%d content=%d; answer=%q", tc.name, nCalls, len(toolNames), contentChunks, strings.TrimSpace(answer))

			// (2) The failure reached the model and it retried.
			if nCalls < 2 {
				t.Fatalf("model did not retry after the tool failure; the tool was called %d time(s)", nCalls)
			}
			// (3) It recovered — the build id is obtainable ONLY via a SUCCESSFUL tool run.
			if !strings.Contains(answer, codeWord) {
				t.Fatalf("did not recover from the mid-stream failure; answer=%q", answer)
			}
			// (4) The stream stayed alive through the failure: >=2 tool-call events + content.
			if len(toolNames) < 2 {
				t.Fatalf("expected >=2 streamed tool-call events across failure+retry; got %d (%v)", len(toolNames), toolNames)
			}
			if contentChunks == 0 {
				t.Fatalf("no content streamed across the failure/retry")
			}
			// (5) Bridge-only policy still holds on the failure path — except
			// Codex, whose native functions.exec tool cannot be disabled by any
			// flag (documented, pre-existing limitation). assertNoNativeWrites
			// (the weaker guarantee real_bridge_streaming_e2e_test.go uses for
			// Codex) assumes a read-only sandbox, which buildRealBridgeAgent
			// does not configure here — skip the check entirely for Codex
			// rather than assert something this test's setup can't guarantee.
			if tc.strictBridgeOnly {
				assertBridgeOrWebsearchOnly(t, toolNames, collectToolLifecycle(listener.events))
			}

			rec := agentreview.WriteWithCriteria(t, "TestRealBridgeStreamingToolFailureRecovery_"+tc.name,
				realBridgeReviewSummary(tc.provider,
					tc.name+" recovers from a MID-STREAM bridge tool failure: first call fails, model retries the same command, stream continues, turn completes with the build id",
					"AGY alpha recovers from a bridge tool failure: the first call fails, it retries, and the final-only answer contains the build ID"),
				realBridgeReviewCriteria(tc.provider),
				agyReviewFacts(tc.provider, map[string]any{
					"provider":               tc.name,
					"tool_handler_calls":     nCalls,
					"streamed_tool_events":   len(toolNames),
					"tool_names":             toolNames,
					"content_chunks":         contentChunks,
					"clean_stream":           strings.TrimSpace(cleanText),
					"answer":                 strings.TrimSpace(answer),
					"recovered_build_id":     codeWord,
					"injected_first_failure": "TRANSIENT_TOOL_FAILURE on call #1",
				}),
				map[string]any{"retried": nCalls >= 2, "recovered": strings.Contains(answer, codeWord), "streamed": contentChunks > 0},
			)
			agentreview.RequireReviewed(t, rec)
		})
	}
}

// TestRealBridgeStreamingToolFailureGiveUp proves the OTHER failure path, on
// every provider: a tool that ALWAYS fails must not hang or crash the turn —
// the model gives up after a bounded retry, the stream stays clean, and the
// turn ends WITHOUT fabricating success (the build id, obtainable only via a
// real tool run, is absent from the answer). Table-driven across all 4
// providers — was Claude-only (see docs/layer_test_coverage.html §matrix).
func TestRealBridgeStreamingToolFailureGiveUp(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the real-bridge tool-failure e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range multiTurnProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			defer cancel()

			workDir := t.TempDir()
			codeWord := "BUILD_ID_" + realBridgeRandHex(6)
			buildIDPath := filepath.Join(workDir, "build_id.txt")
			if err := os.WriteFile(buildIDPath, []byte(codeWord), 0o600); err != nil {
				t.Fatal(err)
			}

			// The tool ALWAYS fails.
			var calls int32
			agent, cleanup := newRealBridgeAgentWithShell(t, ctx, tc, workDir,
				func(ctx context.Context, args map[string]interface{}, shellEnv []string) (string, error) {
					atomic.AddInt32(&calls, 1)
					return "", fmt.Errorf("PERMANENT_TOOL_FAILURE: the tool backend is unavailable")
				})
			defer cleanup()

			listener := &recordingAgentEventListener{}
			agent.addEventListener(listener)

			answer, err := agent.ask(ctx, fmt.Sprintf(
				"You are a build assistant with one tool: execute_shell_command. Write one short sentence, then run exactly: cat %s\n"+
					"If the tool call keeps failing, do NOT retry more than once — reply that the build id could NOT be read and stop.", buildIDPath))
			// (1) The turn must complete gracefully — no crash, no hang — even though the
			// tool never succeeds.
			if err != nil {
				t.Fatalf("turn errored on a permanently-failing tool (must give up gracefully): %v", err)
			}

			nCalls := atomic.LoadInt32(&calls)
			cleanText, _, contentChunks := captureRealBridge(listener.events)
			toolNames := toolNamesFromEvents(listener.events)
			t.Logf("[%s] tool-failure give-up: calls=%d tool-events=%d content=%d; tools=%v; answer=%q", tc.name, nCalls, len(toolNames), contentChunks, toolNames, strings.TrimSpace(answer))

			// (2) The tool was actually attempted. nCalls (this test's own Go
			// handler counter) is the direct signal for Claude/Codex/Pi, which
			// route straight to the registered custom tool. Cursor routes
			// through its own GetMcpTools/CallMcpTool meta-tool wrapper first —
			// found live: nCalls stayed 0 there even though the model's own
			// narration and a real CallMcpTool event both confirmed two real
			// attempts ("the execute_shell_command tool failed with a
			// connection error on both attempts"). toolNames (real streamed
			// tool-call events) is the provider-agnostic signal that actually
			// proves an attempt happened; accept either.
			if (tc.provider == llm.ProviderCodexCLI && nCalls == 0) || (nCalls == 0 && len(toolNames) == 0) {
				t.Fatalf("the failing tool was never called (no handler invocation and no streamed tool-call event)")
			}
			// (3) No FABRICATED success: the build id is obtainable ONLY via a real tool
			// run, which never succeeded, so it must NOT appear in the answer.
			if strings.Contains(answer, codeWord) {
				t.Fatalf("model fabricated success — answer contains the build id %q though the tool always failed: %q", codeWord, answer)
			}
			// (4) The stream stayed clean (content flowed, no corruption).
			if contentChunks == 0 {
				t.Fatalf("no content streamed while handling the failure")
			}
			// Skip for Codex — see the identical note in
			// TestRealBridgeStreamingToolFailureRecovery above.
			if tc.strictBridgeOnly {
				assertBridgeOrWebsearchOnly(t, toolNames, collectToolLifecycle(listener.events))
			}

			rec := agentreview.WriteWithCriteria(t, "TestRealBridgeStreamingToolFailureGiveUp_"+tc.name,
				realBridgeReviewSummary(tc.provider,
					tc.name+" gives up gracefully on a PERMANENTLY failing bridge tool: bounded retry, stream stays clean, turn ends without fabricating the build id",
					"AGY alpha gives up on a permanently failing bridge tool: bounded retries and a clean final-only answer without a fabricated build ID"),
				realBridgeReviewCriteria(tc.provider),
				agyReviewFacts(tc.provider, map[string]any{
					"provider":             tc.name,
					"tool_handler_calls":   nCalls,
					"streamed_tool_events": len(toolNames),
					"tool_names":           toolNames,
					"content_chunks":       contentChunks,
					"clean_stream":         strings.TrimSpace(cleanText),
					"answer":               strings.TrimSpace(answer),
					"unreachable_build_id": codeWord,
					"injected_failure":     "PERMANENT_TOOL_FAILURE on every call",
				}),
				map[string]any{"completed_no_hang": true, "no_fabricated_success": !strings.Contains(answer, codeWord), "streamed": contentChunks > 0},
			)
			agentreview.RequireReviewed(t, rec)
		})
	}
}
