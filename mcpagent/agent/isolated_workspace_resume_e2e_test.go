package mcpagent

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/llm"
)

// TestIsolatedWorkspaceNativeResumeAcrossTurns is the end-to-end proof for the
// isolated-workspace resume fix.
//
// The bug: coding CLIs key a resumable conversation by WORKING DIRECTORY
// (claude files it under ~/.claude/projects/<slugified-cwd>/). The isolated
// workspace was a fresh os.MkdirTemp per Agent — and a new Agent is built for
// every turn — so turn 2 ran in a different cwd, the CLI could not find turn
// 1's conversation, and the turn silently restarted as a fresh conversation
// ("No conversation found with session ID"). Agent.Close also rm -rf'd the dir
// between turns, so even a recorded path pointed at deleted state.
//
// Why a code word and not a log assertion: the ONLY way the word survives into
// turn 2 is genuine provider-native resume. It is spoken, never written to any
// file, and turn 2 runs on a brand-new Agent with empty local history. Same
// technique as TestCodingSessionContinuityAfterLoss; the difference here is
// IsolatedSessionWorkspace, which is what actually broke.
func TestIsolatedWorkspaceNativeResumeAcrossTurns(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the isolated-workspace resume e2e")
	}

	// BOTH transports, because they fail differently. Structured is the one
	// that broke in production: its adapters historically recorded no
	// WorkingDir in the handle, so resume fell back to the caller's real
	// workspace instead of the isolated dir the conversation lived in. tmux
	// records WorkingDir but previously pointed it at a per-Agent random dir
	// that Agent.Close deleted. A pass on one proves nothing about the other.
	transports := []struct {
		name       string
		structured bool
	}{
		{"Tmux", false},
		{"Structured", true},
	}

	for _, tr := range transports {
		tr := tr
		for _, tc := range multiTurnProviderCases {
			tc := tc
			t.Run(tr.name+"/"+tc.name, func(t *testing.T) {
				if _, err := exec.LookPath(tc.binary); err != nil {
					t.Skipf("%s CLI required", tc.binary)
				}
				t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))

				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				defer cancel()

				workDir := t.TempDir() // the "real" workspace resume must NOT fall back to
				sessionID := "isolated-resume-" + realBridgeRandHex(4)
				codeWord := "ISOLATED_WORD_" + realBridgeRandHex(6)

				// The session dir is derived, so it is knowable before the first turn.
				wantDir := isolatedWorkspaceDirForSession(sessionID)
				if wantDir == "" {
					t.Fatal("expected a derivable isolated workspace dir for a non-empty session id")
				}
				t.Cleanup(func() { CloseSession(sessionID) })

				// ---- Turn 1: speak the code word inside the isolated workspace ----
				agent1, cleanup1, err := buildRealBridgeAgent(ctx, tc, t.TempDir(), workDir, sessionID, !tr.structured)
				if err != nil {
					t.Fatalf("build turn-1 agent: %v", err)
				}
				agent1.isolatedSessionWorkspace = true
				if tr.structured {
					agent1.codingAgentTransport = llm.CodingAgentTransportStructured
				}

				if _, err := agent1.ask(ctx, "Remember this code word exactly: "+codeWord+". Reply with just: OK"); err != nil {
					cleanup1()
					t.Fatalf("turn 1 failed: %v", err)
				}
				if got := agent1.ensureIsolatedWorkspaceDir(); got != wantDir {
					cleanup1()
					t.Fatalf("turn 1 ran in %q, want the session-derived dir %q", got, wantDir)
				}
				// The handle is what production persists between turns (chat history
				// stores it; the next turn's Agent applies it). Without carrying it
				// over, turn 2 has no native session id to resume and simply starts a
				// new conversation — so the test would "fail" for a reason that has
				// nothing to do with the working-directory bug under test.
				handle := agent1.currentAgentSessionHandle()
				if handle == nil || handle.Provider.Empty() {
					cleanup1()
					t.Fatalf("turn 1 produced no coding-provider session handle; nothing to resume from")
				}
				if got := strings.TrimSpace(handle.Provider.WorkingDir); got != wantDir {
					// Pins the structured-adapter half of the fix: the handle must
					// record the dir the conversation was actually created in.
					t.Errorf("handle recorded WorkingDir=%q, want the isolated dir %q", got, wantDir)
				}
				cleanup1() // closes agent1 — must NOT destroy the session workspace

				// The dir (and the CLI conversation inside it) must outlive one turn.
				if _, err := os.Stat(wantDir); err != nil {
					t.Fatalf("isolated workspace must survive Agent.Close between turns: %v", err)
				}

				// ---- Turn 2: brand-new Agent, same session, must recall the word ----
				agent2, cleanup2, err := buildRealBridgeAgent(ctx, tc, t.TempDir(), workDir, sessionID, !tr.structured)
				if err != nil {
					t.Fatalf("build turn-2 agent: %v", err)
				}
				defer cleanup2()
				agent2.isolatedSessionWorkspace = true
				if tr.structured {
					agent2.codingAgentTransport = llm.CodingAgentTransportStructured
				}
				// Exactly what production does with the persisted handle.
				agent2.applyAgentSessionHandle(handle)

				if got := agent2.ensureIsolatedWorkspaceDir(); got != wantDir {
					t.Fatalf("turn 2 resolved %q, want the SAME dir as turn 1 %q — a different cwd is exactly why native resume failed", got, wantDir)
				}
				// Resume must return to the isolated dir, never the real workspace.
				if got := agent2.resolveContinuationWorkingDir(""); got != wantDir {
					t.Fatalf("resume working dir = %q, want %q (must never fall back to the real workspace %q)", got, wantDir, workDir)
				}

				answer, err := agent2.ask(ctx, "What exact code word did I ask you to remember? Reply with only that word.")
				if err != nil {
					t.Fatalf("turn 2 failed: %v", err)
				}
				if !strings.Contains(answer, codeWord) {
					t.Fatalf("turn 2 did not recall the code word — the conversation did not resume.\nwant substring: %s\ngot: %s", codeWord, answer)
				}

				// ---- Session end reclaims the workspace (the leak side of the fix) ----
				CloseSession(sessionID)
				if _, err := os.Stat(wantDir); !os.IsNotExist(err) {
					t.Errorf("CloseSession must remove the isolated workspace; stat err=%v (dir=%q)", err, wantDir)
				}
			})
		}
	}
}
