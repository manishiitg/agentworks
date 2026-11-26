package mcpagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/internal/agentreview"
	"github.com/manishiitg/mcpagent/llm"
)

// buildMessageModes reconstructs the three user-facing message modes from a turn's
// mcpagent-layer events, using the StreamingChunkEvent.Source + IsDelta fields:
//
//	mode1 rawTerminal      — the raw tmux pane (Source == "terminal"): what a
//	                         terminal UI shows. Concatenated verbatim.
//	mode3 streamingMessage — the assistant's words for a no-terminal UI: clean
//	                         content (Source != "terminal"), TOOL CHUNKS DROPPED
//	                         (tool calls are ToolCallStartEvent, a different event
//	                         type, so they never appear here), with token-level
//	                         DELTAS concatenated verbatim and block chunks joined
//	                         as lines — the granularity-aware reassembly.
//
// mode2 (non-streaming full message) is the SAME text delivered all at once; the
// test compares it to the turn's final answer.
func buildMessageModes(evs []*events.AgentEvent) (rawTerminal, streamingMessage string) {
	var term strings.Builder
	var blocks []string
	var deltaBuf strings.Builder
	flushDeltas := func() {
		if deltaBuf.Len() == 0 {
			return
		}
		if s := strings.TrimSpace(deltaBuf.String()); s != "" {
			blocks = append(blocks, s)
		}
		deltaBuf.Reset()
	}
	for _, ev := range evs {
		sc, ok := ev.Data.(*events.StreamingChunkEvent)
		if !ok || sc.IsToolCall {
			continue
		}
		if sc.Source == events.StreamingChunkSourceTerminal {
			term.WriteString(sc.Content)
			continue
		}
		if sc.IsDelta {
			deltaBuf.WriteString(sc.Content) // verbatim — never split a token
			continue
		}
		if strings.TrimSpace(sc.Content) == "" {
			continue
		}
		flushDeltas()
		blocks = append(blocks, strings.TrimSpace(sc.Content))
	}
	flushDeltas()
	return term.String(), strings.Join(blocks, "\n")
}

// TestRealBridgeMessageModes proves the three consumer message modes can be
// built from a REAL turn's mcpagent events (through the real bridge), using
// Source + IsDelta — the fields those modes were designed around, on every
// provider. Table-driven across all providers — was Claude-only (see
// docs/layer_test_coverage.html §matrix). It replaces the earlier
// workbench-stand-in draft that faked mode2 == mode3.
//
//	mode1 raw tmux        : the terminal pane is available and separable.
//	mode2 non-streaming   : the full assistant message = the turn's final answer.
//	mode3 streaming       : the reassembled clean stream — TOOLS REMOVED, clean of
//	                        ANSI, and consistent with the final answer.
func TestRealBridgeMessageModes(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the real-bridge message-modes e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range multiTurnProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}
			if tc.provider == llm.ProviderAgyCLI {
				// Excluded from message_modes.tmux (see the Layer-2 row
				// note): the sidecar emits one content chunk per turn with
				// no pane stream, so mode1 (raw terminal reconstruction)
				// is unrepresentable on this lane.
				t.Skip("agy sidecar has no terminal stream; message modes do not apply")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			defer cancel()

			workDir := t.TempDir()
			codeWord := "BUILD_ID_" + realBridgeRandHex(6)
			buildIDPath := filepath.Join(workDir, "build_id.txt")
			if err := os.WriteFile(buildIDPath, []byte(codeWord), 0o600); err != nil {
				t.Fatal(err)
			}

			agent, cleanup, err := buildRealBridgeAgent(ctx, tc, t.TempDir(), workDir, "modes-"+realBridgeRandHex(4), false)
			if err != nil {
				t.Fatalf("build agent: %v", err)
			}
			defer cleanup()
			listener := &recordingAgentEventListener{}
			agent.addEventListener(listener)

			answer, err := agent.ask(ctx, fmt.Sprintf(
				"You are a build assistant with one tool: execute_shell_command. Write one short sentence of narration, then run exactly: cat %s\nThen reply with the build id it printed.", buildIDPath))
			if err != nil {
				t.Fatalf("agent.Ask: %v", err)
			}
			finalAnswer := strings.TrimSpace(answer)

			mode1RawTmux, mode3Streaming := buildMessageModes(listener.events)
			toolNames := toolNamesFromEvents(listener.events)
			t.Logf("[%s] message modes: mode1(terminal len=%d) mode3(streaming)=%q; final=%q; tools=%v",
				tc.name, len(mode1RawTmux), mode3Streaming, finalAnswer, toolNames)

			// mode3 (streaming): non-empty, TOOLS REMOVED, clean of terminal ANSI, and
			// consistent with the final answer (both carry the build id the model read).
			if strings.TrimSpace(mode3Streaming) == "" {
				t.Fatalf("mode3 (streaming) message is empty")
			}
			if strings.Contains(mode3Streaming, "\x1b") {
				t.Fatalf("mode3 streaming message leaked raw terminal ANSI: %q", mode3Streaming)
			}
			// NOTE (found live extending this to every provider): Pi's default
			// narration style is more technical/verbose than Claude/Codex/Cursor's
			// and can legitimately SAY a raw tool/API name as part of its own
			// prose (observed: "...retrieve the parameters for
			// `api_bridge_execute_shell_command`"). That's a real difference in
			// Pi's assistant-text quality worth knowing about, but it is NOT the
			// same failure as a ToolCallEvent leaking into the content channel —
			// mode1/mode3 event-type separation (Source field) worked correctly
			// in that run. Left as a hard failure rather than loosened: silently
			// weakening this check would also hide a genuine leak on any
			// provider, and the two failure modes need to stay distinguishable
			// even though they currently share one assertion.
			for _, banned := range []string{"execute_shell_command", "mcp__api-bridge", "ToolCall", "tool_use"} {
				if strings.Contains(mode3Streaming, banned) {
					t.Fatalf("mode3 streaming leaked tool activity (%q): %q", banned, mode3Streaming)
				}
			}
			if !strings.Contains(mode3Streaming, codeWord) {
				t.Fatalf("mode3 streaming message does not contain the build id from the turn: %q", mode3Streaming)
			}
			// mode2 (non-streaming full message) == the turn's final answer; it must carry
			// the build id and agree with the streaming view.
			if !strings.Contains(finalAnswer, codeWord) {
				t.Fatalf("mode2 (final answer) missing the build id: %q", finalAnswer)
			}
			// mode1 (raw tmux): the terminal pane is available AND separable from mode3 —
			// it contains the raw frames (ANSI), which mode3 does not.
			if tc.provider == llm.ProviderCodexCLI || tc.provider == llm.ProviderPiCLI || tc.provider == llm.ProviderCursorCLI {
				if mode1RawTmux != "" {
					t.Fatal("native protocol leaked terminal output")
				}
			} else if strings.TrimSpace(mode1RawTmux) == "" {
				t.Fatalf("mode1 (raw tmux) is empty — the terminal view is not available")
			}

			summary := tc.name + ": real bridge Source+IsDelta reconstruct streaming and final text; terminal output follows the transport contract"
			rec := agentreview.Write(t, "TestRealBridgeMessageModes_"+tc.name,
				summary,
				map[string]any{
					"provider":                   tc.name,
					"mode1_raw_tmux_head":        mcpFirstN(mode1RawTmux, 240),
					"mode2_non_streaming_final":  finalAnswer,
					"mode3_streaming":            mode3Streaming,
					"streamed_matches_final":     strings.Contains(mode3Streaming, codeWord) && strings.Contains(finalAnswer, codeWord),
					"tool_names_kept_out_of_msg": toolNames,
					"build_id":                   codeWord,
				},
				map[string]any{"has_terminal": mode1RawTmux != "", "streaming_nonempty": mode3Streaming != "", "tools_removed": true},
			)
			agentreview.RequireReviewed(t, rec)
		})
	}
}

func mcpFirstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
