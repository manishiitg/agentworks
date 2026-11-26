package mcpagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/agent/codeexec/shellfixture"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/internal/agentreview"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
)

// multiTurnProviderCase is one provider's real-CLI binary, mcpagent.Provider,
// model ID, and persistent-session option — generalized from the
// Claude-only helper this file originally had, so multi-turn and
// concurrency isolation get proven against every real coding-agent CLI, not
// just Claude (see docs/layer_test_coverage.html §matrix — this was the
// single largest "Claude only" gap in mcpagent's real e2e coverage).
type multiTurnProviderCase struct {
	name          string
	binary        string
	provider      llm.Provider
	modelID       string
	persistentOpt func(bool) agentOption
	// Strict assertions are disabled for the documented Codex execution-tool
	// and Muse session-control exceptions. Muse has a separate live P0 proof
	// of its best-effort native-tool restrictions in the provider adapter.
	strictBridgeOnly bool
}

var multiTurnProviderCases = []multiTurnProviderCase{
	{"Claude", "claude", llm.ProviderClaudeCode, "claude-haiku-4-5", withClaudeCodePersistentInteractiveSession, true},
	{"Codex", "codex", llm.ProviderCodexCLI, "gpt-5.6-luna", withCodexPersistentInteractiveSession, false},
	{"Cursor", "cursor-agent", llm.ProviderCursorCLI, "composer-2.5", withCursorPersistentInteractiveSession, true},
	{"Pi", "pi", llm.ProviderPiCLI, "google/gemini-3.8-flash", withPiPersistentInteractiveSession, true},
	// Muse's internal session controls can bypass PreToolUse.
	{"Muse", "muse", llm.ProviderMuseCLI, "muse-spark-1.3-contributor", withMusePersistentInteractiveSession, false},
	// Agy runs its tmux rows in the TUI sidecar (persistent interactive);
	// strictBridgeOnly=false: a mounted turn approves natives alongside the
	// bridge (--dangerously-skip-permissions is the only tool switch).
	{"Agy", "agy", llm.ProviderAgyCLI, "gemini-3.8-flash-high", withAgyPersistentInteractiveSession, false},
}

// closePersistentInteractiveSession tears down the provider's persistent tmux
// session for this owner (sessionID), if one exists. Agent.Close()
// deliberately does NOT do this in production: a persistent session must
// survive its owning Agent being closed so a later turn's fresh Agent
// (same session ID) can reuse it — that's the whole mechanism these
// multi-turn tests rely on. But a test process never comes back for a later
// turn once it exits, so without an explicit close here every
// persistent-session test leaks its tmux session forever. Found live: 134
// orphaned mlp-* tmux sessions had accumulated from this file's testing
// across several days before this was added.
func closePersistentInteractiveSession(tc multiTurnProviderCase, sessionID string) {
	switch tc.provider {
	case llm.ProviderClaudeCode:
		claudecode.CloseClaudeCodeInteractiveSessionForOwner(sessionID, "test cleanup")
	case llm.ProviderCodexCLI:
		codexcli.CloseCodexCLIInteractiveSessionForOwner(sessionID, "test cleanup")
	case llm.ProviderCursorCLI:
		cursorcli.CloseCursorCLIInteractiveSessionForOwner(sessionID, "test cleanup")
	case llm.ProviderPiCLI:
		picli.ClosePiCLIInteractiveSessionForOwner(sessionID, "test cleanup")
	case llm.ProviderMuseCLI:
		musecli.KillMusePersistentSession(sessionID)
	case llm.ProviderAgyCLI:
		agycli.CloseAgyCLIInteractiveSessionForOwner(sessionID, "test cleanup")
	}
}

// trustAgyWorkdirForTmuxRow grants agy's caller-duty workspace trust for a
// tmux row's workdir and returns the restore func for the row's cleanup. A
// sidecar booted in an untrusted cwd fails loudly on the trust gate, so
// every tmux-row builder calls this for agy (peers need no trust).
func trustAgyWorkdirForTmuxRow(workDir string) (func(), error) {
	if err := os.MkdirAll(workDir, 0o750); err != nil {
		return nil, err
	}
	return agycli.TrustAgyWorkspaceDir(workDir)
}

// agyKeyModeForTmuxRow chains the AGY_P0_KEY_MODE provider flip onto untrust,
// so existing cleanup paths restore both. Without the flag it is a pass-
// through (stored-login truth preserved).
func agyKeyModeForTmuxRow(untrust func()) (func(), error) {
	restore, err := agycli.AgyEnsureKeyMode()
	if err != nil {
		return nil, err
	}
	return func() {
		restore()
		if untrust != nil {
			untrust()
		}
	}, nil
}

// agyReviewFacts stamps the auth mode under test onto an agy row's review
// facts (peers pass through untouched): a key-mode proof must say so on its
// own artifact.
func agyReviewFacts(provider llm.Provider, facts map[string]any) map[string]any {
	if provider == llm.ProviderAgyCLI {
		facts["auth_mode"] = agycli.AgyTestAuthMode()
	}
	return facts
}

// AGY's alpha sidecar exposes live terminal frames, but its conversation DB
// yields assistant content and tool receipts only after each turn. Review its
// final answer and tool correctness without claiming token-level streaming.
func realBridgeReviewCriteria(provider llm.Provider) []string {
	if provider != llm.ProviderAgyCLI {
		return agentreview.StreamingCriteria
	}
	return []string{
		"no duplicated lines, prompt echo, internal instructions, or terminal escape codes in the answer",
		"human-readable final answer with coherent formatting and no invented work",
		"real intended bridge tools completed and their results agree with the answer and on-disk work",
		"multi-turn context, native resume, session isolation, and failure behavior match the test's claim",
		"AGY alpha delivers assistant content once at turn completion; do not describe it as token-streamed or claim the post-hoc tool events were live",
	}
}

func realBridgeReviewSummary(provider llm.Provider, standard, agyAlpha string) string {
	if provider == llm.ProviderAgyCLI {
		return agyAlpha
	}
	return standard
}

// buildRealBridgeAgent stands up an Agent for the given provider, wired to
// the REAL bridge: its own executor HTTP server, the real mcpbridge, and a
// registered real execute_shell_command. t-less (usable from concurrency
// goroutines): callers pass explicit temp/work dirs. Returns the agent + a
// cleanup func.
func buildRealBridgeAgent(ctx context.Context, tc multiTurnProviderCase, tmpBase, workDir, sessionID string, persistent bool) (*Agent, func(), error) {
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
	if tc.provider == llm.ProviderAgyCLI {
		// Agy has no non-persistent interactive mode: the persistent flag
		// IS its tmux-lane switch, so a tmux row passing persistent=false
		// would silently drop agy to the exec lane — the bypass this
		// contract forbids. Force the sidecar lane on every tmux row.
		persistent = true
	}
	var untrust func()
	if tc.provider == llm.ProviderAgyCLI {
		// Trust both the real workdir and the session-derived isolated dir:
		// callers may flip isolatedSessionWorkspace post-build (the
		// isolated-resume test does), moving the sidecar's cwd. Entries are
		// exact-path and restored on cleanup, so an unused one is harmless.
		var untrustWorkdir, untrustIsolated func()
		untrustWorkdir, err = trustAgyWorkdirForTmuxRow(workDir)
		if err != nil {
			stopExecutor()
			return nil, nil, err
		}
		untrustIsolated, err = trustAgyWorkdirForTmuxRow(isolatedWorkspaceDirForSession(sessionID))
		if err != nil {
			untrustWorkdir()
			stopExecutor()
			return nil, nil, err
		}
		untrust = func() { untrustIsolated(); untrustWorkdir() }
		keyed, keyErr := agyKeyModeForTmuxRow(untrust)
		if keyErr != nil {
			untrust()
			stopExecutor()
			return nil, nil, keyErr
		}
		untrust = keyed
	}
	opts := []agentOption{
		withProvider(tc.provider),
		withAPIConfig(apiURL, apiToken),
		withStreaming(true),
		withCodingAgentWorkingDir(workDir),
		withSessionID(sessionID),
	}
	if persistent {
		opts = append(opts, tc.persistentOpt(true))
	}
	agent, err := newAgent(ctx, llmModel, configPath, opts...)
	if err != nil {
		if untrust != nil {
			untrust()
		}
		stopExecutor()
		return nil, nil, err
	}
	shellEnv := append(BuildSafeEnvironment(), "MCP_API_URL="+apiURL, "MCP_API_TOKEN="+apiToken,
		"MCP_CUSTOM="+apiURL+"/s/"+sessionID+"/tools/custom",
		"MCP_MCP="+apiURL+"/s/"+sessionID+"/tools/mcp",
		"MCP_AUTH=Authorization: Bearer "+apiToken)
	if regErr := agent.registerCustomTool(
		"execute_shell_command", codeexec.ShellCommandDescription, codeexec.ShellCommandParams,
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			return shellfixture.ExecuteShellCommand(ctx, args, shellEnv)
		}, "workspace_advanced",
	); regErr != nil {
		_ = agent.Close()
		if untrust != nil {
			untrust()
		}
		stopExecutor()
		return nil, nil, regErr
	}
	return agent, func() {
		_ = agent.Close()
		stopExecutor()
		if persistent {
			closePersistentInteractiveSession(tc, sessionID)
		}
		if untrust != nil {
			untrust()
		}
	}, nil
}

// captureRealBridge collapses a listener's events into the CLEAN streamed text
// (Source != terminal, tools dropped), the tool-call count, and the clean content
// chunk count.
func captureRealBridge(evs []*events.AgentEvent) (cleanText string, toolCalls, contentChunks int) {
	// Delta-aware joining: a provider streaming fine-grained deltas (e.g. Pi,
	// mid-token) must have its pieces concatenated VERBATIM, with no
	// separator — inserting "\n" between every chunk unconditionally, as this
	// function used to, splits words/ids mid-string whenever a delta boundary
	// falls there (observed live: "BUILD_ID_4aa\n94683070c"). Only join
	// complete, non-delta blocks with "\n". Mirrors buildMessageModes's
	// already-correct logic (streaming_message_modes_e2e_test.go) — that
	// function got this fix, this one didn't, found extending
	// TestRealBridgeStreamingToolFailureRecovery to Pi.
	var sb strings.Builder
	inDelta := false
	for _, ev := range evs {
		switch d := ev.Data.(type) {
		case *events.StreamingChunkEvent:
			if d.IsToolCall || d.Content == "" || (!d.IsDelta && strings.TrimSpace(d.Content) == "") || d.Source == events.StreamingChunkSourceTerminal {
				continue
			}
			if d.IsDelta {
				sb.WriteString(d.Content) // verbatim — never split a token
				inDelta = true
			} else {
				if inDelta {
					sb.WriteString("\n")
					inDelta = false
				}
				sb.WriteString(d.Content)
				sb.WriteString("\n")
			}
			contentChunks++
		case *events.ToolCallStartEvent:
			toolCalls++
		}
	}
	return sb.String(), toolCalls, contentChunks
}

func validateCanonicalTurnContract(evs []*events.AgentEvent, result Result) error {
	if strings.TrimSpace(result.TurnID) == "" {
		return fmt.Errorf("Session.Run returned no turn id")
	}
	var completions []*events.UnifiedCompletionEvent
	for _, event := range evs {
		completion, ok := event.Data.(*events.UnifiedCompletionEvent)
		if !ok || event.TurnID != result.TurnID {
			continue
		}
		completions = append(completions, completion)
	}
	if len(completions) != 1 {
		return fmt.Errorf("turn %s emitted %d canonical completions, want exactly one", result.TurnID, len(completions))
	}
	completion := completions[0]
	if completion.Metadata["canonical_turn_completion"] != true || completion.Metadata["turn_id"] != result.TurnID {
		return fmt.Errorf("turn %s completion lacks canonical identity: %#v", result.TurnID, completion.Metadata)
	}
	if completion.Status != "completed" || strings.TrimSpace(completion.FinalResult) != strings.TrimSpace(result.Text) {
		return fmt.Errorf("turn %s completion disagrees with result: status=%q completion=%q result=%q", result.TurnID, completion.Status, completion.FinalResult, result.Text)
	}
	return nil
}

// TestRealBridgeStreamingMultiTurn proves streaming works across MULTIPLE
// turns on a PERSISTENT coding-agent session through the real bridge, on
// every provider: turn 2 reuses the same tmux session (so the cold-turn
// readiness gate is skipped), streams, and the model carries turn-1 context
// (the build id) into a real file write on turn 2. Table-driven across all 4
// providers — was Claude-only (see docs/layer_test_coverage.html §matrix).
func TestRealBridgeStreamingMultiTurn(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the real-bridge multi-turn e2e")
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
			reportPath := filepath.Join(workDir, "report.md")
			if err := os.WriteFile(buildIDPath, []byte(codeWord), 0o600); err != nil {
				t.Fatal(err)
			}

			agent, cleanup, err := buildRealBridgeAgent(ctx, tc, t.TempDir(), workDir, "mt-"+realBridgeRandHex(4), true)
			if err != nil {
				t.Fatalf("build agent: %v", err)
			}
			defer cleanup()
			listener := &recordingAgentEventListener{}
			agent.addEventListener(listener)
			runtimeSession, err := agent.Start(ctx)
			if err != nil {
				t.Fatalf("start session: %v", err)
			}
			defer runtimeSession.Close()

			// Turn 1 — read the build id through the real shell tool.
			result1, err := runtimeSession.Run(ctx, Turn{Input: fmt.Sprintf(
				"You are a build assistant with one tool: execute_shell_command. Write one short sentence, then run exactly: cat %s\nReply with the build id it printed (you do not otherwise know it).", buildIDPath)})
			if err != nil {
				t.Fatalf("turn 1: %v", err)
			}
			ans1 := result1.Text
			turn1End := len(listener.events)
			if err := validateCanonicalTurnContract(listener.events[:turn1End], result1); err != nil {
				t.Fatal(err)
			}
			session1 := strings.TrimSpace(agent.codingProviderSessionHandle.TmuxSession)
			if tc.provider == llm.ProviderCodexCLI || tc.provider == llm.ProviderPiCLI || tc.provider == llm.ProviderCursorCLI {
				session1 = agent.codingProviderSessionHandle.NativeSessionID
			}
			_, t1tools, t1content := captureRealBridge(listener.events[:turn1End])
			if !strings.Contains(ans1, codeWord) {
				t.Fatalf("turn 1 answer missing the build id (tool did not run): %q", ans1)
			}
			if t1tools == 0 || t1content == 0 {
				t.Fatalf("turn 1 did not stream: tools=%d content=%d", t1tools, t1content)
			}

			// Turn 2 — SAME session; carry the turn-1 build id into a real file write.
			turn2Start := time.Now()
			followup := fmt.Sprintf("Using the build id from the previous step, use execute_shell_command to write a GitHub-flavored markdown table to %s with a header row and the rows '| build_id | <that build id> |' and '| status | ok |'. Then run: cat %s and reply with its contents.", reportPath, reportPath)
			if contract, ok := llm.GetCodingAgentProviderContract(tc.provider, tc.modelID); ok && contract.Transport != llm.CodingAgentTransportTmux {
				// PLAT-872-24: exercise the host's retained Send shortcut after
				// a real completed turn, then its normal new-turn fallback.
				_, sendErr := runtimeSession.Send(ctx, followup)
				var deliveryErr *CodingAgentDeliveryError
				if !errors.As(sendErr, &deliveryErr) || deliveryErr.Kind != DeliveryErrorKindNoSession {
					t.Fatalf("idle Send must prove non-delivery, got %v", sendErr)
				}
				if _, statErr := os.Stat(reportPath); !os.IsNotExist(statErr) {
					t.Fatalf("idle Send unexpectedly wrote report: %v", statErr)
				}
			}
			result2, err := runtimeSession.Run(ctx, Turn{Input: followup})
			if err != nil {
				t.Fatalf("turn 2: %v", err)
			}
			ans2 := result2.Text
			turn2Elapsed := time.Since(turn2Start)
			session2 := strings.TrimSpace(agent.codingProviderSessionHandle.TmuxSession)
			if tc.provider == llm.ProviderCodexCLI || tc.provider == llm.ProviderPiCLI || tc.provider == llm.ProviderCursorCLI {
				session2 = agent.codingProviderSessionHandle.NativeSessionID
			}
			_, t2tools, t2content := captureRealBridge(listener.events[turn1End:])
			if err := validateCanonicalTurnContract(listener.events[turn1End:], result2); err != nil {
				t.Fatal(err)
			}

			// Session reused across turns → the cold-turn readiness wait is skipped on
			// turn 2 (it only runs on a freshly created session).
			if session1 == "" || session1 != session2 {
				t.Fatalf("persistent session was NOT reused across turns: session1=%q session2=%q", session1, session2)
			}
			if t2tools == 0 || t2content == 0 {
				t.Fatalf("turn 2 did not stream: tools=%d content=%d", t2tools, t2content)
			}
			// Context continuity + real write: report.md on disk carries the turn-1 build id.
			//nolint:gosec // G304: reportPath is a test-controlled temp path.
			report, rerr := os.ReadFile(reportPath)
			if rerr != nil {
				t.Fatalf("report.md not written on turn 2: %v", rerr)
			}
			if !strings.Contains(string(report), codeWord) || !strings.Contains(string(report), "|") {
				t.Fatalf("turn 2 did not carry the turn-1 build id into report.md (context not preserved): %q", string(report))
			}

			t.Logf("[%s] multi-turn OK: reused session=%s; turn1(tools=%d content=%d) turn2(tools=%d content=%d elapsed=%s)",
				tc.name, session1, t1tools, t1content, t2tools, t2content, turn2Elapsed.Round(time.Second))

			rec := agentreview.WriteWithCriteria(t, "TestRealBridgeStreamingMultiTurn_"+tc.name,
				tc.name+" persistent multi-turn through the REAL bridge: turn 1 reads a build id, turn 2 reuses the session and writes it into report.md",
				realBridgeReviewCriteria(tc.provider),
				agyReviewFacts(tc.provider, map[string]any{
					"provider":                tc.name,
					"reused_provider_session": session1 == session2,
					"turn1_answer":            strings.TrimSpace(ans1),
					"turn1_tool_events":       t1tools,
					"turn1_content_chunks":    t1content,
					"turn2_answer":            strings.TrimSpace(ans2),
					"turn2_tool_events":       t2tools,
					"turn2_content_chunks":    t2content,
					"report_md_on_disk":       string(report),
					"build_id_only_in_file":   codeWord,
				}),
				map[string]any{"reused": session1 == session2, "turn1_streamed": t1tools > 0 && t1content > 0, "turn2_streamed": t2tools > 0 && t2content > 0},
			)
			agentreview.RequireReviewed(t, rec)
		})
	}
}

// TestRealBridgeStreamingConcurrent proves parallel coding-agent sessions
// through the real bridge stay ISOLATED, on every provider: each session
// reads its OWN build id and neither its answer nor its stream leaks the
// other session's build id. Table-driven across all providers — was
// Claude-only (see docs/layer_test_coverage.html §matrix).
func TestRealBridgeStreamingConcurrent(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_REAL_BRIDGE_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_REAL_BRIDGE_E2E=1 to run the real-bridge concurrency e2e")
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

			const n = 2
			type worker struct{ codeWord, workDir, tmpBase, sessionID, buildIDPath string }
			workers := make([]worker, n)
			for i := range workers {
				wd := t.TempDir()
				w := worker{
					codeWord:    fmt.Sprintf("BUILD_ID_%d_%s", i, realBridgeRandHex(5)),
					workDir:     wd,
					tmpBase:     t.TempDir(),
					sessionID:   fmt.Sprintf("conc-%d-%s", i, realBridgeRandHex(4)),
					buildIDPath: filepath.Join(wd, "build_id.txt"),
				}
				if err := os.WriteFile(w.buildIDPath, []byte(w.codeWord), 0o600); err != nil {
					t.Fatal(err)
				}
				workers[i] = w
			}

			type result struct {
				answer, cleanText string
				turnID            string
				tools             int
				canonical         bool
				err               error
			}
			results := make([]result, n)
			var wg sync.WaitGroup
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					w := workers[i]
					agent, cleanup, err := buildRealBridgeAgent(ctx, tc, w.tmpBase, w.workDir, w.sessionID, false)
					if err != nil {
						results[i] = result{err: err}
						return
					}
					defer cleanup()
					listener := &recordingAgentEventListener{}
					agent.addEventListener(listener)
					runtimeSession, startErr := agent.Start(ctx)
					if startErr != nil {
						results[i] = result{err: startErr}
						return
					}
					defer runtimeSession.Close()
					runResult, askErr := runtimeSession.Run(ctx, Turn{Input: fmt.Sprintf(
						"You are a build assistant with one tool: execute_shell_command. Write one short sentence, then run exactly: cat %s\nReply with the build id it printed.", w.buildIDPath)})
					if askErr != nil {
						results[i] = result{err: askErr}
						return
					}
					if contractErr := validateCanonicalTurnContract(listener.events, runResult); contractErr != nil {
						results[i] = result{err: contractErr}
						return
					}
					text, tools, _ := captureRealBridge(listener.events)
					results[i] = result{answer: runResult.Text, cleanText: text, turnID: runResult.TurnID, tools: tools, canonical: true}
				}(i)
			}
			wg.Wait()

			for i, r := range results {
				if r.err != nil {
					t.Fatalf("worker %d failed: %v", i, r.err)
				}
				if !strings.Contains(r.answer, workers[i].codeWord) {
					t.Fatalf("worker %d answer missing its OWN build id %q: %q", i, workers[i].codeWord, r.answer)
				}
				if r.tools == 0 {
					t.Fatalf("worker %d did not stream a tool call", i)
				}
				if !r.canonical || r.turnID == "" {
					t.Fatalf("worker %d lacked canonical terminal contract", i)
				}
				for j := range workers {
					if j == i {
						continue
					}
					if strings.Contains(r.answer, workers[j].codeWord) || strings.Contains(r.cleanText, workers[j].codeWord) {
						t.Fatalf("ISOLATION BREACH: worker %d leaked worker %d's build id %q (answer=%q)", i, j, workers[j].codeWord, r.answer)
					}
				}
				t.Logf("[%s] worker %d isolated: own=%s tools=%d", tc.name, i, workers[i].codeWord, r.tools)
			}

			rec := agentreview.WriteWithCriteria(t, "TestRealBridgeStreamingConcurrent_"+tc.name,
				realBridgeReviewSummary(tc.provider,
					fmt.Sprintf("%d parallel %s sessions through the REAL bridge, each reading its own build id — stream isolation", n, tc.name),
					"2 parallel AGY alpha sessions through the real bridge, each reading its own build ID — session isolation with final-only answers"),
				realBridgeReviewCriteria(tc.provider),
				agyReviewFacts(tc.provider, map[string]any{
					"provider":         tc.name,
					"worker0_answer":   strings.TrimSpace(results[0].answer),
					"worker0_build_id": workers[0].codeWord,
					"worker1_answer":   strings.TrimSpace(results[1].answer),
					"worker1_build_id": workers[1].codeWord,
					"no_cross_leak":    true,
				}),
				map[string]any{"workers": n, "isolated": true},
			)
			agentreview.RequireReviewed(t, rec)
		})
	}
}
