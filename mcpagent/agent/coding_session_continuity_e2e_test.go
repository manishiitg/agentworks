package mcpagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/internal/agentreview"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
)

// TestCodingSessionContinuityAfterLoss proves the library's continuity
// contract end-to-end through the REAL bridge, on every provider: a fact
// stated in turn 1 is recalled in turn 2 EVEN AFTER the live tmux session is
// destroyed between turns. Table-driven across all providers — was
// Claude-only (see docs/layer_test_coverage.html §matrix). Native --resume
// support may genuinely differ per provider, so this is exactly the kind of
// gap worth closing rather than assuming Claude's behavior generalizes.
//
// This is the keep-alive-vs-resume decision the abstraction owns: turn 1 runs on
// a warm persistent session and ContinueConversation persists the handle; we then
// kill the tmux session to simulate a crash / idle-eviction / process restart;
// turn 2's ContinueConversation finds no live session and must relaunch + --resume
// from the persisted native session id. The only way the code word survives that
// is genuine provider-native resume — it was never written to any file.
func TestCodingSessionContinuityAfterLoss(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the real-bridge continuity e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

	for _, tc := range multiTurnProviderCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s CLI required", tc.binary)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()

			workDir := t.TempDir()
			convID := "continuity-" + realBridgeRandHex(4)
			codeWord := "RESUME_WORD_" + realBridgeRandHex(6) // only ever spoken, never written to disk

			agent, cleanup, err := buildRealBridgeAgent(ctx, tc, t.TempDir(), workDir, convID, true)
			if err != nil {
				t.Fatalf("build agent: %v", err)
			}
			defer cleanup()

			// Durable store: the same file store backs both turns, exactly as a product
			// would persist the handle across a restart.
			store := NewFileCodingSessionStore(t.TempDir())

			// --- turn 1: state the code word (no tools; pure conversation memory) ---
			ans1, err := agent.continueConversation(ctx,
				convID,
				fmt.Sprintf("Please remember this code word for later: %s. Just reply OK — do not write it anywhere.", codeWord),
				store)
			if err != nil {
				t.Fatalf("turn 1 (state code word): %v", err)
			}
			t.Logf("[%s] turn 1 answer: %q", tc.name, strings.TrimSpace(ans1))

			handle := agent.currentAgentSessionHandle()
			if handle == nil || handle.Provider.NativeSessionID == "" {
				t.Fatalf("turn 1 produced no resumable native session id: %+v", handle)
			}

			// --- simulate session loss: destroy the live tmux session ---
			tmuxSession := strings.TrimSpace(handle.Provider.TmuxSession)
			if tc.provider == llm.ProviderCodexCLI {
				codexcli.CloseCodexCLIInteractiveSessionForOwner(handle.SessionID, "test process loss")
			} else if tc.provider == llm.ProviderCursorCLI {
				cursorcli.CloseCursorCLIInteractiveSessionForOwner(handle.SessionID, "test process loss")
			} else if tc.provider == llm.ProviderPiCLI {
				llm.ClosePiCLIInteractiveSessionForOwner(handle.SessionID, "test process loss")
			} else {
				if _, err := exec.LookPath("tmux"); err != nil {
					t.Fatal("tmux required to simulate session loss")
				}
				if tmuxSession == "" {
					t.Fatalf("no tmux session name on the handle; cannot simulate loss: %+v", handle.Provider)
				}
				killOut, _ := exec.Command("tmux", "kill-session", "-t", tmuxSession).CombinedOutput() //nolint:gosec // tmuxSession is an adapter-generated session name, not user input
				t.Logf("[%s] killed tmux session %q to force resume: %s", tc.name, tmuxSession, strings.TrimSpace(string(killOut)))
				// Confirm it is actually gone.
				if lsErr := exec.Command("tmux", "has-session", "-t", tmuxSession).Run(); lsErr == nil { //nolint:gosec // tmuxSession is an adapter-generated session name, not user input
					t.Fatalf("tmux session %q still alive after kill — cannot prove the resume path", tmuxSession)
				}
			}

			// --- turn 2: recall the code word — only native --resume can supply it ---
			ans2, err := agent.continueConversation(ctx,
				convID,
				"What was the exact code word I asked you to remember earlier? Reply with ONLY that word.",
				store)
			if err != nil {
				t.Fatalf("turn 2 (recall after session loss): %v", err)
			}
			recall := strings.TrimSpace(ans2)
			t.Logf("[%s] turn 2 recall: %q", tc.name, recall)

			if !strings.Contains(recall, codeWord) {
				t.Fatalf("continuity FAILED across session loss: recall %q does not contain the code word %q (native --resume did not restore conversation memory)", recall, codeWord)
			}

			rec := agentreview.WriteWithCriteria(t, "TestCodingSessionContinuityAfterLoss_"+tc.name,
				tc.name+": ContinueConversation continuity survives live-session loss: a code word stated in turn 1 is recalled in turn 2 after its process is closed, via native resume off the persisted handle",
				realBridgeReviewCriteria(tc.provider),
				agyReviewFacts(tc.provider, map[string]any{
					"provider":              tc.name,
					"conversation_id":       convID,
					"code_word":             codeWord,
					"turn1_answer":          strings.TrimSpace(ans1),
					"native_session_id":     handle.Provider.NativeSessionID,
					"killed_tmux_session":   tmuxSession,
					"turn2_recall":          recall,
					"recalled_after_loss":   strings.Contains(recall, codeWord),
					"code_word_only_spoken": "never written to disk — recall proves provider-native resume",
				}),
				map[string]any{"resumed_after_loss": strings.Contains(recall, codeWord)},
			)
			agentreview.RequireReviewed(t, rec)
		})
	}
}
