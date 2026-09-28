package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	agentevents "github.com/manishiitg/mcpagent/events"
)

func TestCleanupStaleCodingAgentTmuxSessionsClosesMissingOwner(t *testing.T) {
	store := terminals.NewStore()
	sessionID := "missing-owner-session"
	tmuxSession := "mlp-codex-cli-orphan"
	store.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(time.Now(), sessionID, "main:"+sessionID, tmuxSession))
	api := &StreamingAPI{terminalStore: store}

	gotArgs := stubTerminalTmuxCommand(t)

	closed := api.cleanupStaleCodingAgentTmuxSessions(time.Now())

	if closed != 1 {
		t.Fatalf("closed = %d, want 1", closed)
	}
	if got := strings.Join(*gotArgs, " "); got != "kill-session -t "+tmuxSession {
		t.Fatalf("tmux args = %q, want kill-session", got)
	}
	snapshot, ok := store.Get(sessionID + ":main:" + sessionID)
	if !ok {
		t.Fatal("expected stale terminal snapshot to remain visible")
	}
	if snapshot.Active || snapshot.State != "stale" || snapshot.TmuxSession != "" {
		t.Fatalf("snapshot active/state/tmux = %v/%q/%q, want false/stale/empty", snapshot.Active, snapshot.State, snapshot.TmuxSession)
	}
}

func TestCleanupStaleCodingAgentTmuxSessionsClosesCompletedIdleBackstop(t *testing.T) {
	now := time.Now()
	store := terminals.NewStore()
	sessionID := "completed-session"
	tmuxSession := "mlp-codex-cli-idle"
	store.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(now.Add(-4*time.Hour), sessionID, "main:"+sessionID, tmuxSession))
	api := &StreamingAPI{
		terminalStore: store,
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "completed"},
		},
	}

	gotArgs := stubTerminalTmuxCommand(t)

	closed := api.cleanupStaleCodingAgentTmuxSessions(now)

	if closed != 1 {
		t.Fatalf("closed = %d, want 1", closed)
	}
	if got := strings.Join(*gotArgs, " "); got != "kill-session -t "+tmuxSession {
		t.Fatalf("tmux args = %q, want kill-session", got)
	}
}

func TestCleanupStaleCodingAgentTmuxSessionsUnregistersMatchingDurableSession(t *testing.T) {
	now := time.Now()
	store := terminals.NewStore()
	const sessionID = "completed-session-with-durable-registry"
	const tmuxSession = "mlp-claude-code-idle-durable"
	store.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(now.Add(-4*time.Hour), sessionID, "main:"+sessionID, tmuxSession))
	retainedSession := startRegisteredTestTmuxSession(t, sessionID, tmuxSession)
	api := &StreamingAPI{
		terminalStore: store,
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "completed"},
		},
	}
	stubTerminalTmuxCommand(t)

	if closed := api.cleanupStaleCodingAgentTmuxSessions(now); closed != 1 {
		t.Fatalf("closed = %d, want 1", closed)
	}
	if _, ok := mcpagent.LookupSession(sessionID); ok {
		t.Fatal("reaper left the matching durable session registered")
	}
	if retainedSession.ActiveTurnID() != "" {
		t.Fatal("reaped durable session retained an active turn")
	}
}

func TestCleanupStaleCodingAgentTmuxSessionsKeepsRecentCompletedSession(t *testing.T) {
	now := time.Now()
	store := terminals.NewStore()
	sessionID := "recent-completed-session"
	tmuxSession := "mlp-claude-code-recent"
	store.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(now.Add(-30*time.Minute), sessionID, "main:"+sessionID, tmuxSession))
	api := &StreamingAPI{
		terminalStore: store,
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "completed"},
		},
	}

	gotArgs := stubTerminalTmuxCommand(t)

	closed := api.cleanupStaleCodingAgentTmuxSessions(now)

	if closed != 0 {
		t.Fatalf("closed = %d, want 0", closed)
	}
	if len(*gotArgs) != 0 {
		t.Fatalf("tmux command should not run for recent completed session, got %v", *gotArgs)
	}
	snapshot, ok := store.Get(sessionID + ":main:" + sessionID)
	if !ok || snapshot.TmuxSession != tmuxSession {
		t.Fatalf("snapshot tmux = %q ok=%v, want %q", snapshot.TmuxSession, ok, tmuxSession)
	}
}

func TestCleanupStaleCodingAgentTmuxSessionsKeepsCompletedScheduleDuringContinuationWindow(t *testing.T) {
	now := time.Now()
	store := terminals.NewStore()
	sessionID := "schedule-cron--123"
	tmuxSession := "mlp-claude-code-scheduled"
	terminalID := sessionID + ":main:" + sessionID
	store.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(now, sessionID, "main:"+sessionID, tmuxSession))
	if _, ok := store.MarkTurnCompleted(terminalID); !ok {
		t.Fatal("expected scheduled main terminal")
	}
	settled, _ := store.GetRaw(terminalID)
	api := &StreamingAPI{
		terminalStore: store,
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "completed", TriggeredBy: "cron"},
		},
	}

	gotArgs := stubTerminalTmuxCommand(t)
	if closed := api.cleanupStaleCodingAgentTmuxSessions(settled.UpdatedAt.Add(31 * time.Second)); closed != 0 {
		t.Fatalf("closed during continuation window = %d, want 0", closed)
	}
	if len(*gotArgs) != 0 {
		t.Fatalf("tmux command should not run during continuation window, got %v", *gotArgs)
	}
	if closed := api.cleanupStaleCodingAgentTmuxSessions(settled.UpdatedAt.Add(defaultCodingAgentTmuxOrphanIdleTimeout + time.Second)); closed != 1 {
		t.Fatalf("closed after idle backstop = %d, want 1", closed)
	}
	if got := strings.Join(*gotArgs, " "); got != "kill-session -t "+tmuxSession {
		t.Fatalf("tmux args = %q, want kill-session after idle backstop", got)
	}
}

func TestCleanupStaleCodingAgentTmuxSessionsKeepsRunningActiveSession(t *testing.T) {
	now := time.Now()
	store := terminals.NewStore()
	sessionID := "running-session"
	tmuxSession := "mlp-codex-cli-running"
	store.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(now.Add(-4*time.Hour), sessionID, "workflow-step:review-plan", tmuxSession))
	api := &StreamingAPI{
		terminalStore: store,
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "running"},
		},
	}

	gotArgs := stubTerminalTmuxCommand(t)

	closed := api.cleanupStaleCodingAgentTmuxSessions(now)

	if closed != 0 {
		t.Fatalf("closed = %d, want 0", closed)
	}
	if len(*gotArgs) != 0 {
		t.Fatalf("tmux command should not run for active running session, got %v", *gotArgs)
	}
}

func TestCleanupStaleCodingAgentTmuxSessionsClosesStoppedSessionImmediately(t *testing.T) {
	now := time.Now()
	store := terminals.NewStore()
	sessionID := "stopped-session"
	tmuxSession := "mlp-pi-cli-stopped"
	store.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(now, sessionID, "main:"+sessionID, tmuxSession))
	api := &StreamingAPI{
		terminalStore: store,
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "stopped"},
		},
	}

	gotArgs := stubTerminalTmuxCommand(t)

	closed := api.cleanupStaleCodingAgentTmuxSessions(now)

	if closed != 1 {
		t.Fatalf("closed = %d, want 1", closed)
	}
	if got := strings.Join(*gotArgs, " "); got != "kill-session -t "+tmuxSession {
		t.Fatalf("tmux args = %q, want kill-session", got)
	}
}

func TestCleanupBoundedTmuxKeepsDismissedSnapshotUntilDisplayDeadline(t *testing.T) {
	for _, providerPrefix := range []string{"mlp-claude-code-int", "mlp-codex-cli-int", "mlp-cursor-cli-int", "mlp-pi-cli-int"} {
		providerPrefix := providerPrefix
		t.Run(providerPrefix, func(t *testing.T) {
			now := time.Now()
			store := terminals.NewStore()
			sessionID := "bounded-session-" + providerPrefix
			tmuxSession := providerPrefix + "-bounded"
			terminalID := sessionID + ":workflow-step:review-plan"
			store.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(now, sessionID, "workflow-step:review-plan", tmuxSession))
			store.HandleEvent(sessionID, storeevents.Event{
				Type:          "streaming_end",
				Timestamp:     now,
				SessionID:     sessionID,
				ExecutionID:   "workflow-step:review-plan",
				ExecutionKind: "workflow_step",
				Data: &agentevents.AgentEvent{
					Type: agentevents.StreamingEnd,
					Data: &agentevents.StreamingEndEvent{
						BaseEventData: agentevents.BaseEventData{Metadata: map[string]interface{}{
							"kind":                       "terminal",
							"tmux_session":               tmuxSession,
							"terminal_retention_seconds": 1800,
						}},
					},
				},
			})
			if !store.Dismiss(terminalID) {
				t.Fatal("expected snapshot dismissal")
			}
			api := &StreamingAPI{
				terminalStore: store,
				activeSessions: map[string]*ActiveSessionInfo{
					sessionID: {SessionID: sessionID, Status: "running"},
				},
			}
			gotArgs := stubTerminalTmuxCommand(t)

			if closed := api.cleanupStaleCodingAgentTmuxSessions(now.Add(29 * time.Second)); closed != 0 {
				t.Fatalf("closed before shared process deadline = %d, want 0", closed)
			}
			closed := api.cleanupStaleCodingAgentTmuxSessions(now.Add(31 * time.Second))
			if closed != 1 {
				t.Fatalf("closed = %d, want 1", closed)
			}
			if got := strings.Join(*gotArgs, " "); got != "kill-session -t "+tmuxSession {
				t.Fatalf("tmux args = %q, want kill-session", got)
			}
			raw, ok := store.GetRaw(terminalID)
			if !ok {
				t.Fatal("process cleanup deleted retained snapshot")
			}
			if raw.TmuxSession != "" || raw.ProcessState != "closed" || raw.SnapshotKind != "archived" {
				t.Fatalf("raw tmux/process/kind = %q/%q/%q", raw.TmuxSession, raw.ProcessState, raw.SnapshotKind)
			}
			if raw.ClosesAt == nil || !raw.ClosesAt.After(now.Add(20*time.Minute)) {
				t.Fatalf("snapshot display deadline was not retained: %v", raw.ClosesAt)
			}
			if _, visible := store.Get(terminalID); visible {
				t.Fatal("dismissed snapshot became visible during cleanup")
			}
		})
	}
}

func TestCleanupConflictingPiCLIInteractiveSessionsClosesManualSameWorkingDir(t *testing.T) {
	now := time.Now()
	store := terminals.NewStore()
	oldSessionID := "old-chat-session"
	newSessionID := "new-chat-session"
	tmuxSession := "mlp-pi-cli-int-conflict"
	workingDir := "/tmp/workspace-docs/_users/default/Chats"
	store.HandleEvent(oldSessionID, codingAgentTmuxReaperChunkEvent(now, oldSessionID, "main:"+oldSessionID, tmuxSession))
	store.HandleEvent(oldSessionID, codingAgentTmuxStatusLineEvent(now, oldSessionID, tmuxSession, workingDir))
	terminalID := oldSessionID + ":main:" + oldSessionID
	store.MarkCompleted(terminalID)
	api := &StreamingAPI{
		terminalStore: store,
		activeSessions: map[string]*ActiveSessionInfo{
			oldSessionID: {SessionID: oldSessionID, Status: "completed"},
			newSessionID: {SessionID: newSessionID, Status: "running"},
		},
	}

	gotArgs := stubTerminalTmuxCommand(t)
	stubOwnedTmuxSession(t, api, tmuxSession)

	closed := api.cleanupConflictingPiCLIInteractiveSessions(newSessionID, workingDir, "test")

	if closed != 1 {
		t.Fatalf("closed = %d, want 1", closed)
	}
	if got := strings.Join(*gotArgs, " "); got != "kill-session -t "+tmuxSession {
		t.Fatalf("tmux args = %q, want kill-session", got)
	}
	snapshot, ok := store.Get(terminalID)
	if !ok {
		t.Fatal("expected terminal snapshot to remain visible")
	}
	if snapshot.Active || snapshot.State != "completed" || snapshot.TmuxSession != "" {
		t.Fatalf("snapshot active/state/tmux = %v/%q/%q, want false/completed/empty", snapshot.Active, snapshot.State, snapshot.TmuxSession)
	}
	if got := api.activeSessions[oldSessionID].Status; got != "completed" {
		t.Fatalf("old session status = %q, want completed", got)
	}
}

func TestCleanupConflictingPiCLIInteractiveSessionsStopsRunningManualSameWorkingDir(t *testing.T) {
	now := time.Now()
	store := terminals.NewStore()
	oldSessionID := "running-old-chat-session"
	newSessionID := "new-chat-session"
	tmuxSession := "mlp-pi-cli-int-running-conflict"
	workingDir := "/tmp/workspace-docs/_users/default/Chats"
	store.HandleEvent(oldSessionID, codingAgentTmuxReaperChunkEvent(now, oldSessionID, "main:"+oldSessionID, tmuxSession))
	store.HandleEvent(oldSessionID, codingAgentTmuxStatusLineEvent(now, oldSessionID, tmuxSession, workingDir))
	cancelCalled := false
	api := &StreamingAPI{
		terminalStore: store,
		activeSessions: map[string]*ActiveSessionInfo{
			oldSessionID: {SessionID: oldSessionID, Status: "running"},
			newSessionID: {SessionID: newSessionID, Status: "running"},
		},
		agentCancelFuncs: map[string]context.CancelFunc{
			oldSessionID: func() { cancelCalled = true },
		},
		sessionBusy: map[string]bool{oldSessionID: true},
	}

	gotArgs := stubTerminalTmuxCommand(t)
	stubOwnedTmuxSession(t, api, tmuxSession)

	closed := api.cleanupConflictingPiCLIInteractiveSessions(newSessionID, workingDir, "test")

	if closed != 1 {
		t.Fatalf("closed = %d, want 1", closed)
	}
	if got := strings.Join(*gotArgs, " "); got != "kill-session -t "+tmuxSession {
		t.Fatalf("tmux args = %q, want kill-session", got)
	}
	if !cancelCalled {
		t.Fatal("expected old session cancel func to run")
	}
	if got := api.activeSessions[oldSessionID].Status; got != "stopped" {
		t.Fatalf("old session status = %q, want stopped", got)
	}
	if api.isSessionBusy(oldSessionID) {
		t.Fatal("expected old session busy flag to be cleared")
	}
}

func TestCleanupConflictingPiCLIInteractiveSessionsPreservesForeignOwner(t *testing.T) {
	now := time.Now()
	store := terminals.NewStore()
	oldSessionID := "foreign-chat-session"
	newSessionID := "new-chat-session"
	tmuxSession := "mlp-pi-cli-int-foreign"
	workingDir := "/tmp/workspace-docs/_users/default/Chats"
	store.HandleEvent(oldSessionID, codingAgentTmuxReaperChunkEvent(now, oldSessionID, "main:"+oldSessionID, tmuxSession))
	store.HandleEvent(oldSessionID, codingAgentTmuxStatusLineEvent(now, oldSessionID, tmuxSession, workingDir))
	api := &StreamingAPI{
		terminalStore: store,
		activeSessions: map[string]*ActiveSessionInfo{
			oldSessionID: {SessionID: oldSessionID, Status: "completed"},
			newSessionID: {SessionID: newSessionID, Status: "running"},
		},
	}
	gotArgs := stubTerminalTmuxCommand(t)
	oldOutput := runTerminalTmuxOutputCommand
	runTerminalTmuxOutputCommand = func(context.Context, ...string) (string, error) {
		return tmuxSession + "\tforeign-instance\t999999\t1\t1", nil
	}
	t.Cleanup(func() { runTerminalTmuxOutputCommand = oldOutput })

	if closed := api.cleanupConflictingPiCLIInteractiveSessions(newSessionID, workingDir, "test"); closed != 0 {
		t.Fatalf("closed = %d, want 0", closed)
	}
	if len(*gotArgs) != 0 {
		t.Fatalf("foreign-owned tmux must not be killed: %v", *gotArgs)
	}
}

func TestCleanupConflictingPiCLIInteractiveSessionsKeepsCronSameWorkingDir(t *testing.T) {
	now := time.Now()
	store := terminals.NewStore()
	cronSessionID := "cron-chat-session"
	newSessionID := "new-chat-session"
	tmuxSession := "mlp-pi-cli-int-cron"
	workingDir := "/tmp/workspace-docs/_users/default/Chats"
	store.HandleEvent(cronSessionID, codingAgentTmuxReaperChunkEvent(now, cronSessionID, "main:"+cronSessionID, tmuxSession))
	store.HandleEvent(cronSessionID, codingAgentTmuxStatusLineEvent(now, cronSessionID, tmuxSession, workingDir))
	api := &StreamingAPI{
		terminalStore: store,
		activeSessions: map[string]*ActiveSessionInfo{
			cronSessionID: {SessionID: cronSessionID, Status: "running", TriggeredBy: "cron"},
			newSessionID:  {SessionID: newSessionID, Status: "running"},
		},
	}

	gotArgs := stubTerminalTmuxCommand(t)

	closed := api.cleanupConflictingPiCLIInteractiveSessions(newSessionID, workingDir, "test")

	if closed != 0 {
		t.Fatalf("closed = %d, want 0", closed)
	}
	if len(*gotArgs) != 0 {
		t.Fatalf("tmux command should not run for cron session, got %v", *gotArgs)
	}
	snapshot, ok := store.Get(cronSessionID + ":main:" + cronSessionID)
	if !ok || snapshot.TmuxSession != tmuxSession {
		t.Fatalf("cron snapshot tmux = %q ok=%v, want %q", snapshot.TmuxSession, ok, tmuxSession)
	}
}

func TestCleanupConflictingPiCLIInteractiveSessionsPreservesUntrackedTmux(t *testing.T) {
	workingDir := "/tmp/workspace-docs/_users/default/Chats"
	tmuxSession := "mlp-pi-cli-int-live-orphan"
	api := &StreamingAPI{terminalStore: terminals.NewStore()}

	var gotKill []string
	oldRun := runTerminalTmuxCommand
	runTerminalTmuxCommand = func(ctx context.Context, stdin string, args ...string) error {
		gotKill = append([]string(nil), args...)
		return nil
	}
	oldOutput := runTerminalTmuxOutputCommand
	runTerminalTmuxOutputCommand = func(ctx context.Context, args ...string) (string, error) {
		return strings.Join([]string{
			tmuxSession + "\t" + workingDir,
			"mlp-pi-cli-int-other\t/tmp/other",
			"mlp-codex-cli-int-other\t" + workingDir,
		}, "\n"), nil
	}
	t.Cleanup(func() {
		runTerminalTmuxCommand = oldRun
		runTerminalTmuxOutputCommand = oldOutput
	})

	closed := api.cleanupConflictingPiCLIInteractiveSessions("new-chat-session", workingDir, "test")

	if closed != 0 {
		t.Fatalf("closed = %d, want 0", closed)
	}
	if len(gotKill) != 0 {
		t.Fatalf("untracked tmux must not be killed: %v", gotKill)
	}
}

func stubOwnedTmuxSession(t *testing.T, api *StreamingAPI, tmuxSession string) {
	t.Helper()
	registry := api.ensureTerminalLeaseRegistry()
	if registry == nil {
		t.Fatal("expected terminal lease registry")
	}
	original := runTerminalTmuxOutputCommand
	runTerminalTmuxOutputCommand = func(_ context.Context, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "display-message" {
			return fmt.Sprintf("%s\t%s\t%d\t%d\t1",
				tmuxSession,
				registry.InstanceID(),
				registry.OwnerPID(),
				registry.OwnerStartedAt().Unix(),
			), nil
		}
		return original(context.Background(), args...)
	}
	t.Cleanup(func() { runTerminalTmuxOutputCommand = original })
}

// TestSessionHasLiveCodingTmuxTracksReap covers the gate that decides whether an
// active-tab /api/query auto-resumes (re-launch with --resume + materialize) after
// the session's tmux is gone. A live pane → true; once the reaper closes it
// (MarkStale → Active=false, TmuxSession="") → false, which is the signal that the
// next turn must re-launch the native session instead of running against a dead pane.
func TestSessionHasLiveCodingTmuxTracksReap(t *testing.T) {
	store := terminals.NewStore()
	sessionID := "idle-active-session"
	tmuxSession := "mlp-pi-cli-active"
	store.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(time.Now(), sessionID, "workflow-step:child", "mlp-pi-cli-child"))
	api := &StreamingAPI{terminalStore: store}

	if !api.sessionHasLiveCodingTmux(sessionID) {
		t.Fatal("expected child pane to count as aggregate live session work")
	}
	if api.sessionHasLiveMainCodingTmux(sessionID) {
		t.Fatal("child pane must not count as a live main chat terminal")
	}

	store.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(time.Now(), sessionID, "main:"+sessionID, tmuxSession))

	if !api.sessionHasLiveCodingTmux(sessionID) {
		t.Fatal("expected a live coding tmux while the pane is Active with a tmux_session")
	}
	if !api.sessionHasLiveMainCodingTmux(sessionID) {
		t.Fatal("expected a live main coding tmux for the main-agent pane")
	}
	if _, ok := store.MarkTurnCompleted(sessionID + ":main:" + sessionID); !ok {
		t.Fatal("expected to settle the main-agent turn")
	}
	if !api.sessionHasLiveCodingTmux(sessionID) || !api.sessionHasLiveMainCodingTmux(sessionID) {
		t.Fatal("a settled turn must retain its live main-agent tmux")
	}

	// Reap the pane (the 3h idle path): MarkStale clears Active + TmuxSession.
	if _, ok := store.MarkStale(sessionID + ":main:" + sessionID); !ok {
		t.Fatal("expected to mark the terminal stale")
	}
	if _, ok := store.MarkStale(sessionID + ":workflow-step:child"); !ok {
		t.Fatal("expected to mark the child terminal stale")
	}
	if api.sessionHasLiveCodingTmux(sessionID) {
		t.Fatal("expected no live coding tmux after all panes were reaped/stale")
	}

	// A nil store / unknown session must be safe (no panic, false).
	if (&StreamingAPI{}).sessionHasLiveCodingTmux(sessionID) {
		t.Fatal("expected false with no terminal store")
	}
	if api.sessionHasLiveCodingTmux("never-seen") {
		t.Fatal("expected false for an unknown session")
	}
}

func TestCodingAgentSnapshotIsMainAgentRequiresOwnershipEvidence(t *testing.T) {
	tests := []struct {
		name     string
		snapshot terminals.Snapshot
		want     bool
	}{
		{name: "unknown metadata", snapshot: terminals.Snapshot{SessionID: "session-1"}, want: false},
		{name: "explicit main kind", snapshot: terminals.Snapshot{SessionID: "session-1", ExecutionKind: "main_agent"}, want: true},
		{name: "explicit child kind overrides owner", snapshot: terminals.Snapshot{SessionID: "session-1", ExecutionKind: "workflow_step", OwnerID: "main:session-1"}, want: false},
		{name: "legacy main owner", snapshot: terminals.Snapshot{SessionID: "session-1", OwnerID: "main:session-1"}, want: true},
		{name: "child owner", snapshot: terminals.Snapshot{SessionID: "session-1", OwnerID: "workflow-step:collect"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := codingAgentSnapshotIsMainAgent(test.snapshot); got != test.want {
				t.Fatalf("codingAgentSnapshotIsMainAgent() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCodingAgentTmuxOrphanIdleTimeoutHasOneHourCeiling(t *testing.T) {
	t.Setenv(envCodingAgentTmuxOrphanIdleSeconds, "7200")
	if got := codingAgentTmuxOrphanIdleTimeout(); got != time.Hour {
		t.Fatalf("timeout = %v, want one-hour ceiling", got)
	}

	t.Setenv(envCodingAgentTmuxOrphanIdleSeconds, "900")
	if got := codingAgentTmuxOrphanIdleTimeout(); got != 15*time.Minute {
		t.Fatalf("shorter timeout = %v, want 15m", got)
	}
}

func stubTerminalTmuxCommand(t *testing.T) *[]string {
	t.Helper()
	t.Setenv(envCodingAgentTmuxOrphanIdleSeconds, "")
	gotArgs := []string{}
	oldRun := runTerminalTmuxCommand
	runTerminalTmuxCommand = func(ctx context.Context, stdin string, args ...string) error {
		gotArgs = append([]string(nil), args...)
		return nil
	}
	oldOutput := runTerminalTmuxOutputCommand
	runTerminalTmuxOutputCommand = func(ctx context.Context, args ...string) (string, error) {
		return "", nil
	}
	t.Cleanup(func() {
		runTerminalTmuxCommand = oldRun
		runTerminalTmuxOutputCommand = oldOutput
	})
	return &gotArgs
}

func codingAgentTmuxReaperChunkEvent(timestamp time.Time, sessionID, executionID, tmuxSession string) storeevents.Event {
	executionKind := "workflow_step"
	scope := "workflow_step"
	if strings.HasPrefix(executionID, "main:") {
		executionKind = "main_agent"
		scope = "main_agent"
	}
	return storeevents.Event{
		Type:          "streaming_chunk",
		Timestamp:     timestamp,
		SessionID:     sessionID,
		ExecutionID:   executionID,
		ExecutionKind: executionKind,
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingChunk,
			Data: &agentevents.StreamingChunkEvent{
				BaseEventData: agentevents.BaseEventData{
					Metadata: map[string]interface{}{
						"kind":           "terminal",
						"tmux_session":   tmuxSession,
						"execution_kind": executionKind,
						"scope":          scope,
					},
				},
				Content:    "pane",
				ChunkIndex: 1,
			},
		},
	}
}

func codingAgentTmuxStatusLineEvent(timestamp time.Time, sessionID, tmuxSession, workingDir string) storeevents.Event {
	return storeevents.Event{
		Type:      "status_line",
		Timestamp: timestamp,
		SessionID: sessionID,
		Data: &agentevents.AgentEvent{
			Type:      "status_line",
			Timestamp: timestamp,
			SessionID: sessionID,
			Data: &agentevents.GenericEventData{
				Data: map[string]interface{}{
					"provider":     "pi-cli",
					"model":        "google/gemini-3.5-flash",
					"tmux_session": tmuxSession,
					"metadata": map[string]interface{}{
						"working_dir": workingDir,
					},
				},
			},
		},
	}
}

// TestIsCodingAgentTmuxSessionNameRecognizesEveryRegisteredProvider is the
// completeness check that was missing: muse-cli was added as a 5th coding
// CLI provider without this function (or gracefulCloseCodingCLITmuxByName,
// or closeAllCodingCLIInteractiveSessionsForOwner) being updated to include
// it, so its tmux sessions were invisible to orphan-session recovery.
// Table-driven so adding a 6th provider without a row here is an obvious
// gap on review, not a silent one.
func TestIsCodingAgentTmuxSessionNameRecognizesEveryRegisteredProvider(t *testing.T) {
	cases := []struct {
		provider string
		tmuxName string
	}{
		{"claude-code", "mlp-claude-code-abc123"},
		{"codex-cli", "mlp-codex-cli-abc123"},
		{"cursor-cli", "mlp-cursor-cli-abc123"},
		{"pi-cli", "mlp-pi-cli-abc123"},
		{"muse-cli", "mlp-muse-abc123"},
		{"agy-cli", "agy-int-abc123"},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			if !isCodingAgentTmuxSessionName(tc.tmuxName) {
				t.Errorf("isCodingAgentTmuxSessionName(%q) = false, want true for registered provider %s", tc.tmuxName, tc.provider)
			}
		})
	}
	if isCodingAgentTmuxSessionName("some-unrelated-tmux-session") {
		t.Error("expected an unrelated tmux session name to not be recognized as a coding-agent session")
	}
}
