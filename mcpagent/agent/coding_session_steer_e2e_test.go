package mcpagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/internal/agentreview"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
)

// firstToolCallSignal is a thread-safe listener that closes a channel the moment
// the first tool-call starts — the point at which the turn is provably in flight
// and blocked, so a steered message lands mid-turn.
type firstToolCallSignal struct {
	once sync.Once
	ch   chan struct{}
}

func newFirstToolCallSignal() *firstToolCallSignal {
	return &firstToolCallSignal{ch: make(chan struct{})}
}

func (l *firstToolCallSignal) HandleEvent(_ context.Context, event *events.AgentEvent) error {
	// Start OR end: the muse exec lane is END-only (its wire has no tool-
	// started event, only tool.result), so a start-only signal never fires
	// for it. For start-emitting providers the start still wins the race,
	// so their timing is unchanged; for END-only lanes the first end marks
	// a tool call provably underway-or-done, and the callers re-assert
	// isTurnInFlight before delivering.
	switch event.Data.(type) {
	case *events.ToolCallStartEvent, *events.ToolCallEndEvent:
		l.once.Do(func() { close(l.ch) })
	}
	return nil
}

func (l *firstToolCallSignal) Name() string { return "first-tool-call-signal" }

// TestCodingSessionDeliverSteerMidTurn proves Deliver's steer path through
// the REAL bridge, on every provider: while a turn is in flight (blocked in a
// long tool call), Deliver injects a live instruction into the RUNNING turn,
// and the model obeys it in the same turn's final reply. Table-driven across
// all providers — was Claude-only (see docs/layer_test_coverage.html
// §matrix).
//
// The turn is pinned open by a `sleep` tool call so the steer provably arrives
// mid-turn (Deliver sees TurnInFlight()==true and routes to live input). The
// steered secret word can ONLY appear in the reply if the mid-turn message
// actually reached the model.
//
// OPT-IN (RUN_MCPAGENT_STEER_E2E), separate from the main real-bridge gate,
// because live-model mid-turn timing is intermittently flaky: an observed run
// had claude hang after the steer and never finish the turn. The steer-vs-query
// ROUTING decision is covered deterministically by TestDecideDelivery and the
// underlying send-keys live-input mechanism is certified upstream
// (CertLiveInput/CertBusyLiveInput), so this e2e is a strong-but-manual
// integration proof, not an always-on gate.
func TestCodingSessionDeliverSteerMidTurn(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_STEER_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_STEER_E2E=1 to run the (flaky, live-model) real-bridge steer e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range multiTurnProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()

			workDir := t.TempDir()
			convID := "steer-" + realBridgeRandHex(4)
			steerWord := "PURPLE_" + realBridgeRandHex(6)
			toolProof := "DONE_" + realBridgeRandHex(6)
			proofPath := filepath.Join(workDir, "completed.txt")
			inputPath := filepath.Join(workDir, "tool-input.txt")
			if err := os.WriteFile(inputPath, []byte(toolProof), 0600); err != nil {
				t.Fatal(err)
			}

			agent, cleanup, err := buildRealBridgeAgent(ctx, tc, t.TempDir(), workDir, convID, true)
			if err != nil {
				t.Fatalf("build agent: %v", err)
			}
			defer cleanup()
			queuedTransport := !agent.supportsSteering()
			secondWord := "SECOND_" + realBridgeRandHex(6)

			signal := newFirstToolCallSignal()
			agent.addEventListener(signal)
			store := NewMemoryCodingSessionStore()

			var nativeSession *Session
			var capture *turnEndCapture
			if tc.provider == llm.ProviderCodexCLI || tc.provider == llm.ProviderPiCLI || (tc.provider == llm.ProviderClaudeCode && os.Getenv("CLAUDE_CODE_TRANSPORT") == "sdk") || queuedTransport {
				capture = &turnEndCapture{toolEnded: make(chan struct{}), arrived: make(chan struct{}, 8)}
				agent.addEventListener(capture)
				nativeSession, err = agent.Start(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer nativeSession.Close()
			}

			// Start the turn: a long tool call holds it open long enough to steer into.
			type turnResult struct {
				answer string
				err    error
			}
			done := make(chan turnResult, 1)
			go func() {
				prompt := fmt.Sprintf("Run exactly this one shell command and wait for it: sleep 25 && cat %s | tee %s. When it finishes, report COMMAND_DONE followed by the unknown word it printed, then stop.", inputPath, proofPath)
				if nativeSession != nil {
					result, runErr := nativeSession.Run(ctx, Turn{Input: prompt})
					done <- turnResult{answer: result.Text, err: runErr}
				} else {
					ans, aerr := agent.continueConversation(ctx, convID, prompt, store)
					done <- turnResult{answer: ans, err: aerr}
				}
			}()

			// Wait until the turn is provably mid-tool, then give it a moment to settle
			// into the blocking sleep before steering.
			if tc.provider == llm.ProviderAgyCLI {
				// Agy's sidecar emits tool events post-hoc (after the turn),
				// so no tool signal can mark the mid-turn point. Wait for the
				// turn to be provably in flight, then steer into the 25s sleep
				// window: mid-turn is proven by TurnInFlight, mid-tool is not
				// observable on this lane.
				deadline := time.Now().Add(3 * time.Minute)
				for !agent.isTurnInFlight() {
					if time.Now().After(deadline) {
						t.Fatalf("timed out waiting for the agy turn to go in flight")
					}
					time.Sleep(500 * time.Millisecond)
				}
				// TurnInFlight fires at turn start (mount phase), well before
				// the sidecar exists — also wait for sidecar registration,
				// else Deliver has nobody to steer.
				for !agycli.AgyInteractiveSessionActive(convID) {
					if time.Now().After(deadline) {
						t.Fatalf("timed out waiting for the agy sidecar to register")
					}
					time.Sleep(500 * time.Millisecond)
				}
				time.Sleep(2 * time.Second)
			} else {
				select {
				case <-signal.ch:
				case <-time.After(3 * time.Minute):
					t.Fatalf("timed out waiting for the first tool call to start the turn")
				}
			}
			if !agent.isTurnInFlight() {
				t.Fatalf("turn is not marked in flight at the tool-call boundary")
			}
			time.Sleep(2 * time.Second)

			// Steer a new instruction INTO the running turn via the library entry point.
			instruction := fmt.Sprintf("Additional instruction: in your final reply, also include the exact word %s on its own line.", steerWord)
			var delivered Delivered
			if nativeSession != nil {
				receipt, sendErr := nativeSession.Send(ctx, instruction)
				err = sendErr
				if receipt.Status == UserMessageDeliveryStatusQueuedForInjection {
					delivered.Mode = DeliveryModeQueued
				}
				if receipt.Status == UserMessageDeliveryStatusSentToCLI && (receipt.Transport == llm.CodingAgentTransportAppServer || receipt.Transport == llm.CodingAgentTransportRPC || receipt.Transport == llm.CodingAgentTransportSDK) {
					delivered.Mode = DeliveryModeSteered
				}
			} else {
				delivered, err = agent.deliver(ctx, convID, instruction, store)
			}
			if err != nil {
				t.Fatalf("Deliver (steer mid-turn): %v", err)
			}
			expectedMode := DeliveryModeSteered
			if queuedTransport {
				expectedMode = DeliveryModeQueued
			}
			if delivered.Mode != expectedMode {
				t.Fatalf("Deliver routed to %q; want %q (a live turn on a steerable transport)", delivered.Mode, DeliveryModeSteered)
			}

			if queuedTransport {
				receipt, err := nativeSession.Send(ctx, "Another instruction: also include "+secondWord+" in the final reply, in addition to the previous requested word and COMMAND_DONE.")
				if err != nil || receipt.Status != UserMessageDeliveryStatusQueuedForInjection {
					t.Fatalf("second queued request: %#v %v", receipt, err)
				}
			}
			var res turnResult
			select {
			case res = <-done:
			case <-time.After(8 * time.Minute):
				t.Fatalf("turn did not complete after steering (live-model mid-turn hang — see the opt-in note on this test)")
			}
			if res.err != nil {
				t.Fatalf("steered turn errored: %v", res.err)
			}
			if capture != nil {
				completions := capture.snapshot()
				if len(completions) != 1 {
					t.Fatalf("got %d completions for native steered turn", len(completions))
				}
				if completions[0].metadata[LiveInputFollowupMetadataKey] == true {
					t.Fatal("app-server completion incorrectly waits for a tmux follow-up")
				}
			}
			answer := strings.TrimSpace(res.answer)
			t.Logf("[%s] steered turn answer: %q", tc.name, answer)

			// The tool ran (COMMAND_DONE) AND the mid-turn steer landed (steerWord).
			if proof, err := os.ReadFile(proofPath); err != nil || string(proof) != toolProof {
				t.Fatalf("native tool never completed its actual file write: %v", err)
			}
			if !strings.Contains(answer, toolProof) {
				t.Fatalf("answer never consumed the tool result: %q", answer)
			}
			if !strings.Contains(answer, "COMMAND_DONE") {
				t.Fatalf("the long tool call did not complete in-turn; answer=%q", answer)
			}
			if !strings.Contains(answer, steerWord) {
				t.Fatalf("steer did NOT reach the running turn: answer %q missing the steered word %q", answer, steerWord)
			}

			if queuedTransport && !strings.Contains(answer, secondWord) {
				t.Fatalf("lost second queued request: %q", answer)
			}
			reviewName := "TestCodingSessionDeliverSteerMidTurn_" + tc.name
			if queuedTransport {
				reviewName += "_Queued"
			}
			reviewSummary := tc.name + ": live input reaches a running turn"
			if queuedTransport {
				reviewSummary = tc.name + ": two queued inputs both reach the next native-resumed turn after a real MCP tool completes"
			}
			rec := agentreview.WriteWithCriteria(t, reviewName,
				reviewSummary,
				[]string{"delivery mode is steered for a live-input transport, queued only for a query-only transport", "the real shell produced its completion file and the final answer contains the hidden tool-result word", "the final answer contains the steer word with no fabricated claim of completion", "native Session.Run produces exactly one canonical completion; this record does not certify browser rendering or complete token accounting"},
				agyReviewFacts(tc.provider, map[string]any{
					"provider":                        tc.name,
					"conversation_id":                 convID,
					"delivery_mode":                   string(delivered.Mode),
					"second_queued_word":              secondWord,
					"second_queued_word_obeyed":       !queuedTransport || strings.Contains(answer, secondWord),
					"steered_word":                    steerWord,
					"turn_answer":                     answer,
					"tool_completed":                  strings.Contains(answer, toolProof),
					"hidden_tool_word":                toolProof,
					"actual_completion_file_verified": true,
					"steer_word_obeyed":               strings.Contains(answer, steerWord),
					"steer_reached_model":             "steered word only appears if the mid-turn live input reached the model",
				}),
				map[string]any{"steered_mid_turn": strings.Contains(answer, steerWord), "tool_result_consumed": strings.Contains(answer, toolProof), "actual_file_verified": true},
			)
			agentreview.RequireReviewed(t, rec)
		})
	}
}
