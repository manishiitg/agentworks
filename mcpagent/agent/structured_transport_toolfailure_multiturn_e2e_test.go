package mcpagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

// buildStructuredBridgeAgentWithShell is the structured-transport counterpart of
// newRealBridgeAgentWithShell (real_bridge_tool_failure_e2e_test.go): a real
// bridge Agent running over the JSON transport with a CUSTOM
// execute_shell_command handler for fault injection. Returns agent + cleanup.
func buildStructuredBridgeAgentWithShell(t *testing.T, ctx context.Context, tc structuredTransportProviderCase, workDir string, handler func(ctx context.Context, args map[string]interface{}, shellEnv []string) (string, error)) (*Agent, func()) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "mcp_servers.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	apiURL, apiToken, stopExecutor, err := bootRealExecutor(configPath)
	if err != nil {
		t.Fatalf("bootRealExecutor: %v", err)
	}
	llmModel, err := llm.InitializeLLM(llm.Config{Provider: tc.provider, ModelID: tc.modelID})
	if err != nil {
		stopExecutor()
		t.Fatalf("InitializeLLM: %v", err)
	}
	opts := []agentOption{
		withProvider(tc.provider), withAPIConfig(apiURL, apiToken),
		withStreaming(true), withCodingAgentWorkingDir(workDir),
		tc.structuredOption,
		withSessionID("jsontoolfail-" + realBridgeRandHex(4)),
	}
	if tc.provider == llm.ProviderCodexCLI {
		opts = append(opts, withCodingAgentToolsMode(codingAgentToolsMCPOnly))
	}
	agent, err := newAgent(ctx, llmModel, configPath, opts...)
	if err != nil {
		stopExecutor()
		t.Fatalf("newAgent: %v", err)
	}
	shellEnv := append(BuildSafeEnvironment(), "MCP_API_URL="+apiURL, "MCP_API_TOKEN="+apiToken)
	if regErr := agent.registerCustomTool(
		"execute_shell_command", codeexec.ShellCommandDescription, codeexec.ShellCommandParams,
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			return handler(ctx, args, shellEnv)
		}, "workspace_advanced",
	); regErr != nil {
		_ = agent.Close()
		stopExecutor()
		t.Fatalf("RegisterCustomTool: %v", regErr)
	}
	return agent, func() { _ = agent.Close(); stopExecutor() }
}

// requireCodexStructuredToolIdentity guards the complete bridge-only adapter ->
// mcpagent event path used by workflow/background agents. The Codex structured
// adapter previously emitted the label in StreamChunk.Content while leaving
// ToolName empty, so this P0 still passed even though the app displayed "tool".
func requireCodexStructuredToolIdentity(t *testing.T, provider string, evs []*events.AgentEvent) []string {
	t.Helper()
	if provider != "Codex" {
		return nil
	}

	starts := map[string]string{}
	ends := map[string]string{}
	for _, ev := range evs {
		switch data := ev.Data.(type) {
		case *events.ToolCallStartEvent:
			starts[data.ToolCallID] = data.ToolName
		case *events.ToolCallEndEvent:
			ends[data.ToolCallID] = data.ToolName
		case *events.ToolCallErrorEvent:
			// A failed tool has an error terminal event, not a successful end.
			ends[data.ToolCallID] = data.ToolName
		}
	}
	if len(starts) == 0 {
		t.Fatal("Codex structured P0 emitted no ToolCallStart events")
	}

	pairs := make([]string, 0, len(starts))
	for id, startName := range starts {
		if strings.TrimSpace(startName) == "" {
			t.Fatalf("Codex structured ToolCallStart %q has an empty name; the app would render generic \"tool\"", id)
		}
		if startName != "execute_shell_command" && startName != "search_tools" && startName != "get_api_spec" && startName != "read_skill" {
			t.Fatalf("Codex MCP-only ToolCallStart %q has unexpected tool %q", id, startName)
		}
		endName, ok := ends[id]
		if !ok {
			t.Fatalf("Codex structured ToolCallStart %q has no matching tool completion/error", id)
		}
		if endName != startName {
			t.Fatalf("Codex structured tool %q changed name: start=%q end=%q", id, startName, endName)
		}
		pairs = append(pairs, startName+"->"+endName)
	}
	calledShell := false
	for _, name := range starts {
		calledShell = calledShell || name == "execute_shell_command"
	}
	if !calledShell {
		t.Fatal("Codex never called the fixture shell tool")
	}
	sort.Strings(pairs)
	return pairs
}

// TestStructuredTransportToolFailureRecovery is the json/structured counterpart
// of TestRealBridgeStreamingToolFailureRecovery: a MID-STREAM bridge tool
// failure must degrade gracefully — the model retries the same command, the turn
// completes, and the build id (obtainable only via a real tool run) is recovered.
// Codex/Cursor/Pi (Claude has no structured lane).
func TestStructuredTransportToolFailureRecovery(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the structured tool-failure e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range structuredTransportProviderCases {
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
			isAgy := tc.provider == llm.ProviderAgyCLI
			if isAgy {
				// Agy has no bridge-only knob (unlike Codex's
				// WithCodexDisableShellTool): a mounted turn keeps native
				// read/shell tools live — Layer-1 BestEffortToolRestrictions
				// pins only that unmounted native WRITES are auto-denied.
				// A build_id.txt on disk would let the model bypass the
				// bridge tool, answer correctly, and pass this row with
				// zero calls (observed live). Keep the codeword obtainable
				// ONLY via a real tool run: no file; the handler reveals it.
				// The prompt still says "run exactly: cat <path>" — a native
				// attempt finds nothing and the tool remains the only source.
			} else if err := os.WriteFile(buildIDPath, []byte(codeWord), 0o600); err != nil {
				t.Fatal(err)
			}

			var calls int32
			agent, cleanup := buildStructuredBridgeAgentWithShell(t, ctx, tc, workDir,
				func(ctx context.Context, args map[string]interface{}, shellEnv []string) (string, error) {
					if atomic.AddInt32(&calls, 1) == 1 {
						return "", fmt.Errorf("TRANSIENT_TOOL_FAILURE: the tool backend was briefly unavailable; retry the same command")
					}
					if isAgy {
						return codeWord, nil
					}
					return shellfixture.ExecuteShellCommand(ctx, args, shellEnv)
				})
			defer cleanup()

			listener := &recordingAgentEventListener{}
			agent.addEventListener(listener)

			answer, err := agent.ask(ctx, fmt.Sprintf(
				"You are a build assistant with one tool: execute_shell_command. Write one short sentence, then run exactly: cat %s\n"+
					"If a tool call fails with a transient error, RETRY the same command once. Then reply with the build id it printed.", buildIDPath))
			if err != nil {
				t.Fatalf("structured turn errored on a mid-stream tool failure (must degrade gracefully): %v", err)
			}

			nCalls := atomic.LoadInt32(&calls)
			cleanText, _, contentChunks := captureRealBridge(listener.events)
			toolNames := toolNamesFromEvents(listener.events)
			t.Logf("[%s] structured tool-failure recovery: calls=%d tool-events=%d content=%d; answer=%q", tc.name, nCalls, len(toolNames), contentChunks, strings.TrimSpace(answer))
			requireCodexStructuredToolIdentity(t, tc.name, listener.events)

			if nCalls < 2 {
				t.Fatalf("model did not retry after the tool failure; the tool was called %d time(s)", nCalls)
			}
			if !strings.Contains(answer, codeWord) {
				t.Fatalf("did not recover from the mid-stream failure; answer=%q", answer)
			}

			rec := agentreview.Write(t, "TestStructuredTransportToolFailureRecovery_"+tc.name,
				tc.name+" (structured/json) recovers from a MID-STREAM bridge tool failure: first call fails, model retries, turn completes with the build id",
				map[string]any{
					"provider":               tc.name,
					"transport":              "structured/json",
					"tool_handler_calls":     nCalls,
					"streamed_tool_events":   len(toolNames),
					"tool_names":             toolNames,
					"content_chunks":         contentChunks,
					"clean_stream":           strings.TrimSpace(cleanText),
					"answer":                 strings.TrimSpace(answer),
					"recovered_build_id":     codeWord,
					"injected_first_failure": "TRANSIENT_TOOL_FAILURE on call #1",
				},
				map[string]any{"retried": nCalls >= 2, "recovered": strings.Contains(answer, codeWord)},
			)
			agentreview.RequireReviewed(t, rec)
		})
	}
}

// TestStructuredTransportToolFailureGiveUp is the json/structured counterpart of
// TestRealBridgeStreamingToolFailureGiveUp: a tool that ALWAYS fails must not
// hang or crash the turn — the model gives up after a bounded retry and does NOT
// fabricate the build id (obtainable only via a real tool run).
func TestStructuredTransportToolFailureGiveUp(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the structured tool-failure e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range structuredTransportProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}
			// (Codex used to be skipped here on the belief its native functions.exec
			// was unremovable and would bypass the failing bridge. That is no longer
			// true: the codex adapter now disables shell_tool + the other native
			// code-exec features for bridge-only sessions (WithCodexDisableShellTool),
			// so codex has ONLY the failing bridge tool and must genuinely give up.
			// Verified live: structured recovery is forced through the bridge
			// (calls>=2) and give-up no longer obtains the id. So Codex is now a real
			// give-up case like the others.)

			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			defer cancel()

			workDir := t.TempDir()
			unreachable := "BUILD_ID_" + realBridgeRandHex(6)
			buildIDPath := filepath.Join(workDir, "build_id.txt")
			if tc.provider == llm.ProviderAgyCLI {
				// Agy counterpart of the recovery-row branch above: a
				// mounted agy turn keeps native read/shell tools live with
				// no bridge-only knob, so a build_id.txt on disk would let
				// the model read the "unreachable" id natively and fail
				// this row without any fabrication. Withhold the file: the
				// id is then obtainable only via the tool, which always
				// fails, so the give-up assertion is deterministic.
			} else if err := os.WriteFile(buildIDPath, []byte(unreachable), 0o600); err != nil {
				t.Fatal(err)
			}

			var calls int32
			agent, cleanup := buildStructuredBridgeAgentWithShell(t, ctx, tc, workDir,
				func(ctx context.Context, args map[string]interface{}, shellEnv []string) (string, error) {
					atomic.AddInt32(&calls, 1)
					return "", fmt.Errorf("PERMANENT_TOOL_FAILURE: the tool backend is unavailable")
				})
			defer cleanup()

			listener := &recordingAgentEventListener{}
			agent.addEventListener(listener)

			prompt := fmt.Sprintf(
				"You are a build assistant with one tool: execute_shell_command. Write one short sentence, then run exactly:\ncat %s\n"+
					"If the tool call keeps failing, do NOT retry more than once — reply that the build id could NOT be read and stop.", buildIDPath)
			if tc.provider == llm.ProviderAgyCLI {
				// The shared verdict ("could NOT be read") is reachable via
				// a native read of the withheld file, which lets the model
				// give up without ever attempting the tool (observed live:
				// calls=0). Close that path: only the TOOL's result counts.
				prompt = fmt.Sprintf(
					"You are a build assistant with one tool: execute_shell_command. Call that tool with exactly:\ncat %s\n"+
						"Do NOT read files yourself — only the tool's result counts. If the TOOL call fails, do NOT retry more than once — reply that the build id could NOT be read and stop.", buildIDPath)
			}
			answer, err := agent.ask(ctx, prompt)
			if err != nil {
				t.Fatalf("structured turn errored on a permanent tool failure (must give up gracefully, not error): %v", err)
			}

			nCalls := atomic.LoadInt32(&calls)
			_, _, contentChunks := captureRealBridge(listener.events)
			toolNames := toolNamesFromEvents(listener.events)
			t.Logf("[%s] structured tool-failure give-up: calls=%d tool-events=%d content=%d; tools=%v; answer=%q", tc.name, nCalls, len(toolNames), contentChunks, toolNames, strings.TrimSpace(answer))
			toolIdentityPairs := requireCodexStructuredToolIdentity(t, tc.name, listener.events)

			// (1) The tool was actually attempted (handler counter or a streamed
			// tool-call event — Cursor routes through its GetMcpTools/CallMcpTool
			// meta-tool so its handler counter can read 0 even on a real attempt).
			if (tc.provider == llm.ProviderCodexCLI && nCalls == 0) || (nCalls == 0 && len(toolNames) == 0) {
				t.Fatalf("the failing tool was never called (no handler invocation and no streamed tool-call event)")
			}
			// (2) It did NOT fabricate the build id — that value is obtainable ONLY
			// via a successful tool run, which never happened.
			if strings.Contains(answer, unreachable) {
				t.Fatalf("model fabricated the build id despite the tool always failing; answer=%q", answer)
			}

			rec := agentreview.Write(t, "TestStructuredTransportToolFailureGiveUp_"+tc.name,
				tc.name+" (structured/json) gives up gracefully on a PERMANENTLY failing bridge tool: bounded retry, turn ends without fabricating the build id",
				map[string]any{
					"provider":             tc.name,
					"transport":            "structured/json",
					"tool_handler_calls":   nCalls,
					"streamed_tool_events": len(toolNames),
					"tool_names":           toolNames,
					"tool_identity_pairs":  toolIdentityPairs,
					"content_chunks":       contentChunks,
					"answer":               strings.TrimSpace(answer),
					"unreachable_build_id": unreachable,
					"injected_failure":     "PERMANENT_TOOL_FAILURE on every call",
				},
				map[string]any{
					"attempted":         nCalls > 0 || len(toolNames) > 0,
					"did_not_fabricate": !strings.Contains(answer, unreachable),
					// Tool identity is part of the reviewed behavior shape. A
					// regression back to ["", ""] must invalidate P0 evidence.
					"tool_names":          toolNames,
					"tool_identity_pairs": toolIdentityPairs,
				},
			)
			agentreview.RequireReviewed(t, rec)
		})
	}
}

// TestStructuredTransportMultiTurn proves multi-turn continuity on the
// json/structured transport via native --resume (ContinueConversation): turn 1
// reads a build id from a file; turn 2, WITHOUT re-reading the file, writes the
// build id it learned in turn 1 into report.md. The build id in report.md proves
// the second turn genuinely continued the first turn's context. This is json's
// analogue of the tmux persistent-session multi-turn test — the mechanism is
// native --resume, not pane reuse (the transport-specific P0, as it should be).
func TestStructuredTransportMultiTurn(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the structured multi-turn e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range structuredTransportProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()

			workDir := t.TempDir()
			buildID := "BUILD_ID_" + realBridgeRandHex(6)
			buildIDPath := filepath.Join(workDir, "build_id.txt")
			if err := os.WriteFile(buildIDPath, []byte(buildID), 0o600); err != nil {
				t.Fatal(err)
			}
			convID := "jsonmt-" + realBridgeRandHex(4)

			agent, cleanup, err := buildStructuredBridgeAgent(ctx, tc, t.TempDir(), workDir, convID)
			if err != nil {
				t.Fatalf("build structured agent: %v", err)
			}
			defer cleanup()
			store := NewMemoryCodingSessionStore()

			// Absolute path: the bridge execute_shell_command runs in the executor's
			// cwd, not the CLI's workdir, so a relative path only resolves for
			// providers that use a native shell (in the CLI cwd). Claude correctly
			// routes through the bridge tool — an absolute path is transport-fair.
			ans1, err := agent.continueConversation(ctx, convID,
				"Run exactly: cat "+buildIDPath+" — then reply with ONLY the build id it printed.", store)
			if err != nil {
				t.Fatalf("turn 1: %v", err)
			}
			if !strings.Contains(ans1, buildID) {
				t.Fatalf("turn 1 did not read the build id; answer=%q", ans1)
			}
			t.Logf("[%s] turn 1 read build id: %q", tc.name, strings.TrimSpace(ans1))

			// Turn 2 is a pure RECALL (no file read, no write): the build id can only
			// appear if native --resume restored turn 1's context. A recall isolates
			// multi-turn continuity from write-capability — cursor's structured
			// deny-builtin setup runs in --mode ask, which refuses natural-language
			// WRITE requests, so a write-based turn 2 would conflate "didn't resume"
			// with "won't write". Recall is the transport-appropriate continuity proof.
			ans2, err := agent.continueConversation(ctx, convID,
				"Do NOT read any file or run any tool. From our conversation so far, what build id did you just report? Reply with ONLY that build id.", store)
			if err != nil {
				t.Fatalf("turn 2: %v", err)
			}
			if !strings.Contains(ans2, buildID) {
				t.Fatalf("[%s] multi-turn continuity failed — turn 2 did not recall turn 1's build id via native --resume; turn2 answer=%q", tc.name, strings.TrimSpace(ans2))
			}
			t.Logf("[%s] multi-turn OK: turn 2 recalled turn 1's build id via native --resume ✓ (%q)", tc.name, strings.TrimSpace(ans2))

			rec := agentreview.Write(t, "TestStructuredTransportMultiTurn_"+tc.name,
				tc.name+" (structured/json) multi-turn via native --resume: turn 1 reads a build id, turn 2 recalls it purely from restored session context (no re-read, no write)",
				map[string]any{
					"provider":        tc.name,
					"transport":       "structured/json",
					"conversation_id": convID,
					"build_id":        buildID,
					"turn1_answer":    strings.TrimSpace(ans1),
					"turn2_answer":    strings.TrimSpace(ans2),
				},
				map[string]any{"turn1_read": strings.Contains(ans1, buildID), "turn2_recalled_via_resume": strings.Contains(ans2, buildID)},
			)
			agentreview.RequireReviewed(t, rec)
		})
	}
}
