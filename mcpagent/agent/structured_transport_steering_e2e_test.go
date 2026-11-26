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

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/agent/codeexec/shellfixture"
	"github.com/manishiitg/mcpagent/internal/agentreview"
	"github.com/manishiitg/mcpagent/llm"
)

// buildStructuredBridgeAgent stands up an Agent for the given provider on the
// REAL bridge but running over the STRUCTURED/JSON transport (codex exec --json /
// cursor-agent --print / pi --print --mode json) instead of tmux. Mirrors
// buildRealBridgeAgent (real_bridge_multiturn_concurrent_e2e_test.go) but swaps
// the persistent-tmux option for the structured-transport option, so the json
// column can be exercised through the same real-bridge + execute_shell_command
// setup the tmux tests use. Returns the agent + a cleanup func.
func buildStructuredBridgeAgent(ctx context.Context, tc structuredTransportProviderCase, tmpBase, workDir, sessionID string) (*Agent, func(), error) {
	configPath := filepath.Join(tmpBase, "mcp_servers.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		return nil, nil, err
	}
	apiURL, apiToken, stopExecutor, err := bootRealExecutor(configPath)
	if err != nil {
		return nil, nil, err
	}
	llmModel, err := llm.InitializeLLM(llm.Config{Provider: tc.provider, ModelID: tc.modelID})
	if err != nil {
		stopExecutor()
		return nil, nil, err
	}
	agent, err := newAgent(ctx, llmModel, configPath,
		withProvider(tc.provider),
		withAPIConfig(apiURL, apiToken),
		withStreaming(true),
		withCodingAgentWorkingDir(workDir),
		withSessionID(sessionID),
		tc.structuredOption,
	)
	if err != nil {
		stopExecutor()
		return nil, nil, err
	}
	shellEnv := append(BuildSafeEnvironment(), "MCP_API_URL="+apiURL, "MCP_API_TOKEN="+apiToken)
	if regErr := agent.registerCustomTool(
		"execute_shell_command", codeexec.ShellCommandDescription, codeexec.ShellCommandParams,
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			return shellfixture.ExecuteShellCommand(ctx, args, shellEnv)
		}, "workspace_advanced",
	); regErr != nil {
		_ = agent.Close()
		stopExecutor()
		return nil, nil, regErr
	}
	return agent, func() { _ = agent.Close(); stopExecutor() }, nil
}

// TestStructuredTransportDeliverQueuesMidTurn is the live-CLI proof of the
// transport-aware steering fix, on the json/structured transport. It is the
// json counterpart to TestCodingSessionDeliverSteerMidTurn (tmux), and it
// asserts the OPPOSITE routing outcome: while a real structured turn is in
// flight (pinned open by a slow shell tool), Deliver must route to
// DeliveryModeQueued — NOT DeliveryModeSteered — because a one-shot
// `--json`/`--print` process has no live pane to inject into. Before the fix,
// SupportsSteering() keyed only off the provider contract and would have
// reported the structured run as steerable, so Deliver would have tried a tmux
// live-input send into a process that isn't a tmux session at all.
//
// The deterministic routing truth table is covered by
// TestSupportsSteeringFalseOnStructuredTransport / TestDeliverQueuesOnStructuredCodingAgent;
// this e2e proves the same decision holds against a REAL structured CLI turn and
// that the turn still completes cleanly after a mid-turn Deliver (no mis-routed
// injection, no hang).
//
// OPT-IN (RUN_MCPAGENT_STEER_E2E), like the tmux steer e2e, because live-model
// mid-turn timing is intermittently flaky. Claude is absent from
// structuredTransportProviderCases by design — it has no structured lane.
func TestStructuredTransportDeliverQueuesMidTurn(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_STEER_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_STEER_E2E=1 to run the (flaky, live-model) real-bridge structured steer e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range structuredTransportProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()

			workDir := t.TempDir()
			convID := "jsonsteer-" + realBridgeRandHex(4)
			queuedWord := "TEAL_" + realBridgeRandHex(6)

			agent, cleanup, err := buildStructuredBridgeAgent(ctx, tc, t.TempDir(), workDir, convID)
			if err != nil {
				t.Fatalf("build structured agent: %v", err)
			}
			defer cleanup()

			signal := newFirstToolCallSignal()
			agent.addEventListener(signal)
			store := NewMemoryCodingSessionStore()

			type turnResult struct {
				answer string
				err    error
			}
			done := make(chan turnResult, 1)
			go func() {
				ans, aerr := agent.continueConversation(ctx, convID,
					"Run exactly this one shell command and wait for it: sleep 25 && echo COMMAND_DONE. "+
						"When it finishes, report the word it printed, then stop.", store)
				done <- turnResult{answer: ans, err: aerr}
			}()

			// Wait until the turn is provably mid-tool, then let it settle into the
			// blocking sleep before delivering.
			if tc.provider == llm.ProviderAgyCLI {
				// Agy's exec lane streams no tool events at all (one content
				// chunk per turn), so the tool-call signal can never fire.
				// Mid-turn is proven by the in-flight flag instead; the
				// sleep-25 tool keeps the turn open with wide margin after
				// it, and the row needs mid-TURN, not mid-tool.
				inFlightDeadline := time.Now().Add(5 * time.Minute)
				for !agent.isTurnInFlight() {
					if time.Now().After(inFlightDeadline) {
						t.Fatalf("timed out waiting for the agy structured turn to go in flight")
					}
					time.Sleep(500 * time.Millisecond)
				}
			} else {
				select {
				case <-signal.ch:
				case <-time.After(5 * time.Minute):
					t.Fatalf("timed out waiting for the first tool call to start the structured turn")
				}
				if !agent.isTurnInFlight() {
					t.Fatalf("turn is not marked in flight at the tool-call boundary")
				}
			}
			time.Sleep(2 * time.Second)

			// THE ASSERTION: on a structured transport this must QUEUE, not steer.
			delivered, err := agent.deliver(ctx, convID,
				fmt.Sprintf("Additional instruction: in your final reply, also include the exact word %s on its own line.", queuedWord),
				store)
			if err != nil {
				t.Fatalf("Deliver (mid-turn, structured): %v", err)
			}
			if delivered.Mode != DeliveryModeQueued {
				t.Fatalf("Deliver routed to %q; want %q — a live structured turn is query-only and must queue, not steer (this is exactly the transport-blind bug the fix closes)", delivered.Mode, DeliveryModeQueued)
			}

			var res turnResult
			select {
			case res = <-done:
			case <-time.After(8 * time.Minute):
				t.Fatalf("structured turn did not complete after a mid-turn Deliver (possible mis-routed injection/hang — the very failure the fix prevents)")
			}
			if res.err != nil {
				t.Fatalf("structured turn errored after mid-turn Deliver: %v", res.err)
			}
			answer := strings.TrimSpace(res.answer)

			// A queued message on a query-only transport is drained at the NEXT
			// LLM-call boundary through the steer buffer — which, when the current
			// turn still has a post-tool LLM call left (it does: the sleep tool's
			// result comes back and the model answers), is a boundary WITHIN this
			// same ContinueConversation. So the queued word must end up in exactly
			// one of two places, and never silently dropped:
			//   (a) already consumed this turn -> present in the final answer, or
			//   (b) still retained            -> present in DrainSteerMessages.
			// This is the transport-accurate integrity check (a tmux steer, by
			// contrast, would have landed the word live in the running pane). Found
			// live: real Codex structured consumed it this turn (case a).
			drained := agent.drainSteerMessages()
			inAnswer := strings.Contains(answer, queuedWord)
			inBuffer := false
			for _, m := range drained {
				if strings.Contains(m.Text, queuedWord) {
					inBuffer = true
					break
				}
			}
			t.Logf("[%s] structured queued-Deliver: mode=%q answer=%q queuedWord_in_answer=%v in_buffer=%v", tc.name, delivered.Mode, answer, inAnswer, inBuffer)
			if !inAnswer && !inBuffer {
				t.Fatalf("the queued mid-turn message was neither delivered to the model this turn nor retained for the next — it was silently lost; answer=%q drained=%v", answer, drained)
			}

			rec := agentreview.Write(t, "TestStructuredTransportDeliverQueuesMidTurn_"+tc.name,
				tc.name+": on the structured/json transport a mid-turn Deliver QUEUES (does not steer), the turn completes cleanly, and the queued message is provably delivered-or-retained, never lost",
				map[string]any{
					"provider":              tc.name,
					"transport":             "structured/json",
					"conversation_id":       convID,
					"delivery_mode":         string(delivered.Mode),
					"queued_word":           queuedWord,
					"turn_answer":           answer,
					"queued_word_in_answer": inAnswer,
					"queued_word_in_buffer": inBuffer,
					"routing_expectation":   "structured is query-only: Deliver must return queued, not steered; the queue drains at the next LLM-call boundary (possibly within this turn)",
				},
				map[string]any{"queued_not_steered": delivered.Mode == DeliveryModeQueued, "queued_msg_delivered_or_retained": inAnswer || inBuffer},
			)
			agentreview.RequireReviewed(t, rec)
		})
	}
}
