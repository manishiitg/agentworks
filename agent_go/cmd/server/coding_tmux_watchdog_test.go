package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	agentevents "github.com/manishiitg/mcpagent/events"
)

func TestInspectCodingTmuxPaneStateUsesRealPaneState(t *testing.T) {
	original := runTerminalTmuxOutputCommand
	t.Cleanup(func() { runTerminalTmuxOutputCommand = original })

	tests := []struct {
		name   string
		output string
		err    error
		want   codingTmuxPaneState
	}{
		{name: "alive", output: "0\n", want: codingTmuxPaneAlive},
		{name: "dead", output: "1\n", want: codingTmuxPaneDead},
		{name: "missing", err: errors.New("can't find session: mlp-test"), want: codingTmuxPaneMissing},
		{name: "transient failure", err: errors.New("temporary tmux failure"), want: codingTmuxPaneUnknown},
		{name: "unexpected response", output: "", want: codingTmuxPaneUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runTerminalTmuxOutputCommand = func(context.Context, ...string) (string, error) {
				return tc.output, tc.err
			}
			if got := inspectCodingTmuxPaneState("mlp-test"); got != tc.want {
				t.Fatalf("inspectCodingTmuxPaneState = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInspectCodingTmuxPaneStateRejectsEmptySession(t *testing.T) {
	if got := inspectCodingTmuxPaneState("  "); got != codingTmuxPaneMissing {
		t.Fatalf("inspectCodingTmuxPaneState(empty) = %v, want missing", got)
	}
}

// A dead coding-CLI pane's own output (a crash message, an auth failure, a
// bridge error) used to be thrown away — the watchdog killed the pane before
// anything ever looked at what it said, leaving only "tmux pane exited
// unexpectedly" for every future investigation to start over from. This
// confirms the capture happens for a dead pane, with the right tmux target,
// before cleanup.
func TestCodingTmuxWatchdogCapturesDeadPaneOutputBeforeKill(t *testing.T) {
	oldOutput := runTerminalTmuxOutputCommand
	oldCapture := captureTmuxPanePlainForWatchdog
	oldKill := runTmuxKill
	t.Cleanup(func() {
		runTerminalTmuxOutputCommand = oldOutput
		captureTmuxPanePlainForWatchdog = oldCapture
		runTmuxKill = oldKill
	})
	runTerminalTmuxOutputCommand = func(context.Context, ...string) (string, error) { return "1\n", nil }
	var capturedFor []string
	captureTmuxPanePlainForWatchdog = func(tmux string) string {
		capturedFor = append(capturedFor, tmux)
		return "Error: codex-cli crashed on startup\nexit status 1"
	}
	var killedFor []string
	runTmuxKill = func(_ context.Context, tmux string) error {
		killedFor = append(killedFor, tmux)
		return nil
	}

	store := terminals.NewStore()
	sessionID := "dead-pane-session"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "main:"+sessionID, "mlp-codex-cli-dead", "stable pane", 1))
	api := &StreamingAPI{terminalStore: store}

	api.reapRateLimitedCodingSessionsOnce(map[string]codingWatchdogObservation{})

	if len(capturedFor) != 1 || capturedFor[0] != "mlp-codex-cli-dead" {
		t.Fatalf("captureTmuxPanePlainForWatchdog calls = %v, want exactly one call for mlp-codex-cli-dead", capturedFor)
	}
	if len(killedFor) != 1 || killedFor[0] != "mlp-codex-cli-dead" {
		t.Fatalf("runTmuxKill calls = %v, want exactly one call for mlp-codex-cli-dead", killedFor)
	}
}

func TestTailLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"fewer lines than n returns everything", "a\nb", 5, "a\nb"},
		{"exact n returns everything", "a\nb\nc", 3, "a\nb\nc"},
		{"more lines than n keeps only the tail", "a\nb\nc\nd\ne", 2, "d\ne"},
		{"CRLF is normalized like the rest of this package", "a\r\nb\r\nc", 2, "b\nc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tailLines(tc.in, tc.n); got != tc.want {
				t.Fatalf("tailLines(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}

func TestCodingTmuxWatchdogReconcilesFromActualPaneState(t *testing.T) {
	originalOutput := runTerminalTmuxOutputCommand
	originalCapture := captureTmuxPanePlainForWatchdog
	originalKill := runTmuxKill
	originalClose := closeCodingCLITmuxForWatchdog
	t.Cleanup(func() {
		runTerminalTmuxOutputCommand = originalOutput
		captureTmuxPanePlainForWatchdog = originalCapture
		runTmuxKill = originalKill
		closeCodingCLITmuxForWatchdog = originalClose
	})
	captureTmuxPanePlainForWatchdog = func(string) string { return "" }
	runTmuxKill = func(context.Context, string) error { return nil }
	closeCodingCLITmuxForWatchdog = func(string, string) bool { return true }

	tests := []struct {
		name       string
		paneState  string
		paneErr    error
		wantActive bool
		wantState  string
		wantTmux   bool
		ticks      int
	}{
		{name: "live pane stays active", paneState: "0\n", wantActive: true, wantState: "running", wantTmux: true, ticks: 1},
		{name: "dead active pane fails", paneState: "1\n", wantActive: false, wantState: "failed", wantTmux: false, ticks: 1},
		{name: "missing active pane fails after confirmation", paneErr: errors.New("can't find session: mlp-test"), wantActive: false, wantState: "failed", wantTmux: false, ticks: codingWatchdogMissingConfirmChecks},
		{name: "unknown failure stays active", paneErr: errors.New("temporary tmux failure"), wantActive: true, wantState: "running", wantTmux: true, ticks: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := terminals.NewStore()
			sessionID := "watchdog-session"
			terminalID := sessionID + ":main:" + sessionID
			store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "main:"+sessionID, "mlp-test", "stable pane", 1))
			api := &StreamingAPI{terminalStore: store}

			runTerminalTmuxOutputCommand = func(context.Context, ...string) (string, error) {
				return tc.paneState, tc.paneErr
			}
			streak := map[string]codingWatchdogObservation{}
			for i := 0; i < tc.ticks; i++ {
				api.reapRateLimitedCodingSessionsOnce(streak)
			}

			snapshot, ok := store.Get(terminalID)
			if !ok {
				t.Fatalf("terminal snapshot missing")
			}
			if snapshot.Active != tc.wantActive || snapshot.State != tc.wantState {
				t.Fatalf("active/state = %v/%q, want %v/%q", snapshot.Active, snapshot.State, tc.wantActive, tc.wantState)
			}
			if gotTmux := snapshot.TmuxSession != ""; gotTmux != tc.wantTmux {
				t.Fatalf("tmux present = %v, want %v", gotTmux, tc.wantTmux)
			}
		})
	}
}

func TestCodingTmuxWatchdogDoesNotFailOnFirstMissingObservation(t *testing.T) {
	oldOutput := runTerminalTmuxOutputCommand
	t.Cleanup(func() { runTerminalTmuxOutputCommand = oldOutput })
	runTerminalTmuxOutputCommand = func(context.Context, ...string) (string, error) {
		return "", errors.New("can't find session: mlp-test")
	}

	store := terminals.NewStore()
	sessionID := "missing-pane-race"
	terminalID := sessionID + ":main:" + sessionID
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "main:"+sessionID, "mlp-test", "stable pane", 1))
	api := &StreamingAPI{terminalStore: store}
	streak := map[string]codingWatchdogObservation{}

	api.reapRateLimitedCodingSessionsOnce(streak)

	snapshot, ok := store.Get(terminalID)
	if !ok || !snapshot.Active || snapshot.State != "running" || snapshot.TmuxSession == "" {
		t.Fatalf("first missing observation changed terminal state: %#v", snapshot)
	}
}

func TestCodingTmuxWatchdogRequiresDistinctTicksPerTerminal(t *testing.T) {
	oldOutput := runTerminalTmuxOutputCommand
	oldCapture := captureTmuxPanePlainForWatchdog
	t.Cleanup(func() {
		runTerminalTmuxOutputCommand = oldOutput
		captureTmuxPanePlainForWatchdog = oldCapture
	})
	runTerminalTmuxOutputCommand = func(context.Context, ...string) (string, error) {
		return "0", nil
	}
	captureTmuxPanePlainForWatchdog = func(string) string {
		return "You've hit your usage limit"
	}

	store := terminals.NewStore()
	sessionID := "shared-watchdog-session"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:one", "mlp-codex-cli-int-one", "limited", 1))
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:two", "mlp-codex-cli-int-two", "limited", 1))
	api := &StreamingAPI{
		terminalStore: store,
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "running"},
		},
	}
	streak := map[string]codingWatchdogObservation{}

	api.reapRateLimitedCodingSessionsOnce(streak)

	if got := api.activeSessions[sessionID].Status; got != "running" {
		t.Fatalf("session stopped after one tick across two terminals: %q", got)
	}
	if streak["mlp-codex-cli-int-one"].count != 1 || streak["mlp-codex-cli-int-two"].count != 1 {
		t.Fatalf("terminal streaks = %#v, want one observation each", streak)
	}
}

func TestCodingTmuxWatchdogRateLimitedChildDoesNotStopParent(t *testing.T) {
	oldOutput := runTerminalTmuxOutputCommand
	oldCapture := captureTmuxPanePlainForWatchdog
	oldKill := runTmuxKill
	oldClose := closeCodingCLITmuxForWatchdog
	t.Cleanup(func() {
		runTerminalTmuxOutputCommand = oldOutput
		captureTmuxPanePlainForWatchdog = oldCapture
		runTmuxKill = oldKill
		closeCodingCLITmuxForWatchdog = oldClose
	})
	runTerminalTmuxOutputCommand = func(context.Context, ...string) (string, error) { return "0", nil }
	captureTmuxPanePlainForWatchdog = func(string) string { return "You've hit your usage limit" }
	runTmuxKill = func(context.Context, string) error { return nil }
	closeCodingCLITmuxForWatchdog = func(string, string) bool { return true }

	store := terminals.NewStore()
	sessionID := "scheduled-parent"
	terminalID := sessionID + ":workflow-step:collect-insider"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(
		sessionID,
		"workflow-step:collect-insider",
		"mlp-claude-code-child",
		"limited",
		1,
	))
	api := &StreamingAPI{
		terminalStore: store,
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "running"},
		},
		stoppedSessions: make(map[string]bool),
	}
	streak := map[string]codingWatchdogObservation{}

	api.reapRateLimitedCodingSessionsOnce(streak)
	api.reapRateLimitedCodingSessionsOnce(streak)

	if got := api.activeSessions[sessionID].Status; got != "running" {
		t.Fatalf("parent status = %q, want running", got)
	}
	if api.isSessionMarkedStopped(sessionID) {
		t.Fatal("child rate limit marked the parent session stopped")
	}
	snapshot, ok := store.Get(terminalID)
	if !ok {
		t.Fatal("child terminal snapshot missing")
	}
	if snapshot.State != "failed" || snapshot.Active {
		t.Fatalf("child state/active = %q/%v, want failed/false", snapshot.State, snapshot.Active)
	}
}

func TestCodingTmuxWatchdogRateLimitedMainStopsSession(t *testing.T) {
	oldOutput := runTerminalTmuxOutputCommand
	oldCapture := captureTmuxPanePlainForWatchdog
	oldKill := runTmuxKill
	oldClose := closeCodingCLITmuxForWatchdog
	oldCloseAllRuntime := closeAllCodingCLISessionsForRuntimeCancel
	oldCloseRuntime := closeCodingAgentTmuxForRuntimeCancel
	t.Cleanup(func() {
		runTerminalTmuxOutputCommand = oldOutput
		captureTmuxPanePlainForWatchdog = oldCapture
		runTmuxKill = oldKill
		closeCodingCLITmuxForWatchdog = oldClose
		closeAllCodingCLISessionsForRuntimeCancel = oldCloseAllRuntime
		closeCodingAgentTmuxForRuntimeCancel = oldCloseRuntime
	})
	runTerminalTmuxOutputCommand = func(context.Context, ...string) (string, error) { return "0", nil }
	captureTmuxPanePlainForWatchdog = func(string) string { return "You've hit your usage limit" }
	runTmuxKill = func(context.Context, string) error { return nil }
	closeCodingCLITmuxForWatchdog = func(string, string) bool { return true }
	closeAllCodingCLISessionsForRuntimeCancel = func(string, string) {}
	closeCodingAgentTmuxForRuntimeCancel = func(string, string) bool { return true }

	store := terminals.NewStore()
	sessionID := "main-rate-limited"
	event := terminalRouteChunkEvent(sessionID, "main:"+sessionID, "mlp-claude-code-main", "limited", 1)
	event.ExecutionKind = "main_agent"
	chunk := event.Data.Data.(*agentevents.StreamingChunkEvent)
	chunk.Metadata["execution_kind"] = "main_agent"
	store.HandleEvent(sessionID, event)
	api := &StreamingAPI{
		terminalStore: store,
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "running"},
		},
		stoppedSessions: make(map[string]bool),
	}
	streak := map[string]codingWatchdogObservation{}

	api.reapRateLimitedCodingSessionsOnce(streak)
	api.reapRateLimitedCodingSessionsOnce(streak)

	if got := api.activeSessions[sessionID].Status; got != "error" {
		t.Fatalf("main session status = %q, want error", got)
	}
	if !api.isSessionMarkedStopped(sessionID) {
		t.Fatal("main rate limit did not stop the session")
	}
}

func TestCodingTmuxWatchdogRequiresStableEvidence(t *testing.T) {
	oldOutput := runTerminalTmuxOutputCommand
	oldCapture := captureTmuxPanePlainForWatchdog
	t.Cleanup(func() {
		runTerminalTmuxOutputCommand = oldOutput
		captureTmuxPanePlainForWatchdog = oldCapture
	})
	runTerminalTmuxOutputCommand = func(context.Context, ...string) (string, error) { return "0", nil }
	contents := []string{
		"You've hit your usage limit · resets at 10:00\nretrying request",
		"You've hit your usage limit · resets at 10:00\nrequest resumed",
	}
	captureTmuxPanePlainForWatchdog = func(string) string {
		content := contents[0]
		contents = contents[1:]
		return content
	}

	store := terminals.NewStore()
	sessionID := "main-session"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "main:"+sessionID, "mlp-claude-main", "limited", 1))
	api := &StreamingAPI{
		terminalStore: store,
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "running"},
		},
		stoppedSessions: make(map[string]bool),
	}
	streak := map[string]codingWatchdogObservation{}

	api.reapRateLimitedCodingSessionsOnce(streak)
	api.reapRateLimitedCodingSessionsOnce(streak)

	if got := streak["mlp-claude-main"].count; got != 1 {
		t.Fatalf("changing evidence count = %d, want reset to 1", got)
	}
	if got := api.activeSessions[sessionID].Status; got != "running" {
		t.Fatalf("session status = %q, want running", got)
	}
}

func TestCodingWatchdogRateLimitEvidenceIgnoresOldScrollback(t *testing.T) {
	lines := []string{"You've hit your usage limit"}
	for i := 0; i < codingWatchdogRateLimitTailLines; i++ {
		lines = append(lines, "current progress")
	}
	if got := codingWatchdogRateLimitEvidence(strings.Join(lines, "\n")); got != "" {
		t.Fatalf("old scrollback evidence = %q, want empty", got)
	}
}

func TestCodingWatchdogRateLimitEvidenceRecognizesClaudeLimitMenu(t *testing.T) {
	content := "What do you want to do?\n\n❯ 1. Stop and wait for limit to reset\n  2. Upgrade your plan\n\nEnter to confirm · Esc to cancel"
	if got := codingWatchdogRateLimitEvidence(content); got == "" {
		t.Fatal("Claude usage-limit menu was not recognized as rate-limit evidence")
	}
}

func TestCodingWatchdogLimitReasonNamesProviderAndRecovery(t *testing.T) {
	reason := codingWatchdogLimitReason(terminals.Snapshot{Status: terminals.Status{ProviderLabel: "Claude Code"}})
	if !strings.Contains(reason, "Claude Code has reached its usage limit") || !strings.Contains(reason, "Try again after") {
		t.Fatalf("limit reason = %q", reason)
	}
}

// server A SDE crew: Claude narrating a third-party throttle ("Notion
// rate-limited that query") in classic-renderer scrollback made the watchdog
// cancel a healthy session every 30s.
func TestCodingWatchdogRateLimitEvidenceIgnoresAssistantNarration(t *testing.T) {
	pane := strings.Join([]string{
		"● Notion rate-limited that query — let me wait a moment and retry.",
		"  Called api-bridge (ctrl+o to expand)",
		"● The API returned a rate limit exceeded note from Notion earlier, handled",
		"  by retrying; quota exceeded warnings were from the vendor, not me.",
		"❯ in our dashboard can we show subtickets also clearly",
		"● Valid and live.",
		"✻ Cooked for 1m 11s · done 8:14 AM",
		"❯ ",
	}, "\n")
	if evidence := codingWatchdogRateLimitEvidence(pane); evidence != "" {
		t.Fatalf("assistant narration must not look like a provider limit: %q", evidence)
	}
	realError := strings.Join([]string{
		"● Checking the ticket.",
		"  ⎿  API Error: 429 Too Many Requests",
		"❯ ",
	}, "\n")
	if codingWatchdogRateLimitEvidence(realError) == "" {
		t.Fatal("a provider API error under ⎿ must still be detected")
	}
	cliLine := "Rate limit reached for requests\n❯ "
	if codingWatchdogRateLimitEvidence(cliLine) == "" {
		t.Fatal("a CLI status line must still be detected")
	}
}

// server A 2026-09-27: a resumed SDE crew pane redrew the user's own message
// "...we got rate limited in mcp"; the watchdog read it as a limit wall and
// killed every new turn. User messages, assistant replies and continuation
// lines the window cannot attribute are not evidence; real notices are.
func TestCodingWatchdogIgnoresUserAndReplyTextAboutLimits(t *testing.T) {
	for name, pane := range map[string]string{
		"user message":            "> can you save this key and for dashboard lets use api.. instead of mcp .. we got rate\n  limited in mcp\n\n● Saved.\n\n❯ check any new comments in notion\n  ⏵⏵ auto mode on",
		"reply":                   "● No GitHub activity today. Now checking Notion (may still be\n  rate-limited from earlier).\n\n❯ ",
		"window starts mid-block": "  rate-limited from earlier), which is a plan-level throttle.\n  Nothing else changed.\n\n❯ ",
		"input box":               "❯ you have reached your usage limit is what notion said",
	} {
		if got := codingWatchdogRateLimitEvidence(pane); got != "" {
			t.Errorf("%s: misread as a limit wall", name)
		}
	}
	for name, pane := range map[string]string{
		"claude status": "● Working on it\n\nYou've hit your session limit · resets 11pm (UTC)\n/usage-credits to finish what you're working on.",
		"tool result":   "● Calling the API\n  ⎿  API Error: 429 Too Many Requests\n\n❯ ",
		"codex":         "■ You've reached your usage limit. Upgrade to Pro.",
	} {
		if got := codingWatchdogRateLimitEvidence(pane); got == "" {
			t.Errorf("%s: real limit notice missed", name)
		}
	}
}

// The CLI's own usage numbers outrank screen text: usage known and below every
// limit means limit-looking text is not a wall, however often it is seen;
// usage known and exhausted still confirms the wall.
func TestCodingTmuxWatchdogTrustsCLIUsageOverScreenText(t *testing.T) {
	oldOutput, oldCapture, oldUsage := runTerminalTmuxOutputCommand, captureTmuxPanePlainForWatchdog, codingWatchdogStructuredUsage
	t.Cleanup(func() {
		runTerminalTmuxOutputCommand, captureTmuxPanePlainForWatchdog, codingWatchdogStructuredUsage = oldOutput, oldCapture, oldUsage
	})
	runTerminalTmuxOutputCommand = func(context.Context, ...string) (string, error) { return "0", nil }
	captureTmuxPanePlainForWatchdog = func(string) string { return "You've hit your usage limit" }

	// One tick: below-limit usage must not even start a confirmation streak;
	// exhausted usage starts it like before. Several ticks below the limit
	// must never stop the session.
	run := func(exhausted bool, ticks int) (map[string]codingWatchdogObservation, string) {
		codingWatchdogStructuredUsage = func(string) (bool, bool) { return true, exhausted }
		store := terminals.NewStore()
		sessionID := "usage-overrule-session"
		store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:one", "mlp-claude-code-usage-overrule", "limited", 1))
		api := &StreamingAPI{
			terminalStore:  store,
			activeSessions: map[string]*ActiveSessionInfo{sessionID: {SessionID: sessionID, Status: "running"}},
		}
		streak := map[string]codingWatchdogObservation{}
		for i := 0; i < ticks; i++ {
			api.reapRateLimitedCodingSessionsOnce(streak)
		}
		return streak, api.activeSessions[sessionID].Status
	}
	if streak, status := run(false, codingWatchdogConfirmChecks+2); len(streak) != 0 || status != "running" {
		t.Fatalf("usage below limits: streak=%#v status=%q", streak, status)
	}
	if streak, _ := run(true, 1); streak["mlp-claude-code-usage-overrule"].count != 1 {
		t.Fatalf("usage exhausted: wall evidence not counted: %#v", streak)
	}
}

// A main pane that vanishes while a turn is in flight was replaced by that
// turn (the provider's submit retry starts a fresh pane): the watchdog must not
// cancel the session and kill the retry (server A SDE crew, 2026-09-27). Without an
// in-flight turn, a vanished main pane still fails the session.
func TestCodingTmuxWatchdogLeavesReplacedMainPaneToRunningTurn(t *testing.T) {
	oldOutput := runTerminalTmuxOutputCommand
	t.Cleanup(func() { runTerminalTmuxOutputCommand = oldOutput })
	runTerminalTmuxOutputCommand = func(context.Context, ...string) (string, error) {
		return "", errors.New("can't find session: mlp-claude-code-replaced")
	}
	run := func(turnInFlight bool) string {
		store := terminals.NewStore()
		sessionID := "replaced-main-pane"
		event := terminalRouteChunkEvent(sessionID, "main:"+sessionID, "mlp-claude-code-replaced", "resumed", 1)
		event.ExecutionKind = "main_agent"
		meta := event.Data.Data.(*agentevents.StreamingChunkEvent).Metadata
		meta["execution_kind"], meta["scope"] = "main_agent", "main_agent"
		delete(meta, "current_step_id")
		delete(meta, "workflow_path")
		store.HandleEvent(sessionID, event)
		api := &StreamingAPI{
			terminalStore:    store,
			activeSessions:   map[string]*ActiveSessionInfo{sessionID: {SessionID: sessionID, Status: "running"}},
			agentCancelFuncs: map[string]context.CancelFunc{},
			stoppedSessions:  map[string]bool{},
		}
		if turnInFlight {
			api.agentCancelFuncs[sessionID] = func() {}
		}
		streak := map[string]codingWatchdogObservation{}
		for i := 0; i < codingWatchdogMissingConfirmChecks+1; i++ {
			api.reapRateLimitedCodingSessionsOnce(streak)
		}
		return api.activeSessions[sessionID].Status
	}
	if status := run(true); status != "running" {
		t.Fatalf("turn in flight: session status = %q, want running (the retry must survive)", status)
	}
	if status := run(false); status == "running" {
		t.Fatal("no turn in flight: a vanished main pane must still fail the session")
	}
}
