package server

import (
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	workflowtypes "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

func TestRunningWorkflowListIncludesWorkflowBuilderTask(t *testing.T) {
	startedAt := time.Now().UTC()
	api := &StreamingAPI{
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			"builder-1": {
				ExecutionID:   "builder-1",
				SessionID:     "session-builder",
				Source:        trackedExecutionSourceWorkshopBackground,
				Kind:          "workflow_builder_task",
				Name:          "Review plan drift",
				Title:         "Review plan drift",
				PresetQueryID: "preset-1",
				WorkspacePath: "Workflow/video-project",
				PhaseID:       "workflow-builder",
				PhaseName:     "Workflow Builder",
				Status:        trackedExecutionStatusRunning,
				UserID:        "user-1",
				StartedAt:     startedAt,
			},
		},
	}

	running := api.listRunningWorkflowExecutions("user-1")
	if len(running) != 1 {
		t.Fatalf("running len = %d, want 1", len(running))
	}
	if running[0].SessionID != "session-builder" {
		t.Fatalf("session_id = %q, want session-builder", running[0].SessionID)
	}
	if running[0].Kind != "workflow_builder_task" {
		t.Fatalf("kind = %q, want workflow_builder_task", running[0].Kind)
	}

	api.trackedWorkflowExecutionsMux.RLock()
	found := api.runningWorkflowListExecutionBySessionLocked("session-builder")
	api.trackedWorkflowExecutionsMux.RUnlock()
	if found == nil {
		t.Fatal("runningWorkflowListExecutionBySessionLocked did not find builder task")
	}
}

// TestRunningWorkflowListCarriesTheCollapsedDisplayStatus pins PLAT-095's
// follow-up: ActiveSessionInfo has always shipped a pre-collapsed
// busy/idle/stopped DisplayStatus alongside its raw RuntimeState, computed
// from the exact same snapshot so the two can never disagree.
// ActiveWorkflowExecution — what /api/workflow/running (the Global Monitor's
// workflow view) actually returns — shipped RuntimeState but had no
// DisplayStatus field at all, forcing every consumer to re-derive the
// collapse from raw Phase itself instead of trusting an authoritative answer.
func TestRunningWorkflowListCarriesTheCollapsedDisplayStatus(t *testing.T) {
	startedAt := time.Now().UTC()
	api := &StreamingAPI{
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			"exec-1": {
				ExecutionID:   "exec-1",
				SessionID:     "session-busy",
				Source:        trackedExecutionSourceWorkshopBackground,
				Kind:          "workflow_builder_task",
				WorkspacePath: "Workflow/video-project",
				Status:        trackedExecutionStatusRunning,
				UserID:        "user-1",
				StartedAt:     startedAt,
			},
		},
	}
	api.setSessionBusy("session-busy", true)

	running := api.listRunningWorkflowExecutions("user-1")
	if len(running) != 1 {
		t.Fatalf("running len = %d, want 1", len(running))
	}
	if running[0].RuntimeState == nil {
		t.Fatal("RuntimeState was not populated")
	}
	if running[0].DisplayStatus != sessionExecutionDisplayBusy {
		t.Fatalf("DisplayStatus = %q, want %q — the collapsed status must match RuntimeState.Phase, not be left for the caller to re-derive",
			running[0].DisplayStatus, sessionExecutionDisplayBusy)
	}
	if got := sessionDisplayStatusFromRuntime(*running[0].RuntimeState).Status; got != running[0].DisplayStatus {
		t.Fatalf("DisplayStatus %q disagrees with sessionDisplayStatusFromRuntime(RuntimeState) %q — they must be computed from the same snapshot", running[0].DisplayStatus, got)
	}
}

func TestWorkspaceRunningWorkflowUsesSameLifecycleProjectionAsGlobalMonitor(t *testing.T) {
	startedAt := time.Now().UTC()
	api := &StreamingAPI{trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
		"exec-workspace": {
			ExecutionID: "exec-workspace", SessionID: "session-workspace", Source: trackedExecutionSourceConversationTurn,
			Kind: "workflow_builder_task", WorkspacePath: "Workflow/video-project",
			Status: trackedExecutionStatusRunning, UserID: "user-1", StartedAt: startedAt,
		},
	}}
	api.setSessionBusy("session-workspace", true)

	global := api.listRunningWorkflowExecutions("user-1")
	workspace := api.listRunningWorkflowExecutionsForWorkspace("Workflow/video-project")
	if len(global) != 1 || len(workspace) != 1 {
		t.Fatalf("global=%d workspace=%d, want one lifecycle projection in both", len(global), len(workspace))
	}
	if workspace[0].RuntimeState == nil || workspace[0].DisplayStatus != global[0].DisplayStatus {
		t.Fatalf("workspace lifecycle %+v does not match Global Monitor %+v", workspace[0], global[0])
	}
}

func TestRunningWorkflowListKeepsInternalWorkflowStepsOut(t *testing.T) {
	api := &StreamingAPI{
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			"step-1": {
				ExecutionID:   "step-1",
				SessionID:     "session-builder",
				Source:        trackedExecutionSourceWorkshopBackground,
				Kind:          "workflow_step",
				Name:          "Step -> collect data",
				WorkspacePath: "Workflow/video-project",
				PhaseID:       "workflow-builder",
				Status:        trackedExecutionStatusRunning,
				UserID:        "user-1",
				StartedAt:     time.Now().UTC(),
			},
		},
	}

	running := api.listRunningWorkflowExecutions("user-1")
	if len(running) != 0 {
		t.Fatalf("running len = %d, want 0 for internal workflow step", len(running))
	}
}

func TestFindRunningTrackedExecutionForWorkspaceWhereDoesNotLetNewerScheduleHideBuilder(t *testing.T) {
	now := time.Now().UTC()
	api := &StreamingAPI{
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			"builder": {
				ExecutionID:   "builder",
				SessionID:     "chat-session",
				WorkspacePath: "Workflow/demo",
				PhaseID:       "workflow-builder",
				Status:        trackedExecutionStatusRunning,
				StartedAt:     now.Add(-time.Minute),
			},
			"schedule": {
				ExecutionID:   "schedule",
				SessionID:     "schedule-cron--demo_1",
				WorkspacePath: "Workflow/demo",
				PhaseID:       "execute-workflow",
				Status:        trackedExecutionStatusRunning,
				TriggeredBy:   "cron",
				StartedAt:     now,
			},
		},
	}

	found := api.findRunningTrackedExecutionForWorkspaceWhere("Workflow/demo", func(exec *TrackedWorkflowExecution) bool {
		return trackedExecutionBlocksNewWorkflowBuilderChat(exec)
	})
	if found == nil || found.SessionID != "chat-session" {
		t.Fatalf("builder lookup = %#v, want chat-session", found)
	}
}

func TestScheduledWorkflowBuilderPhaseDoesNotBlockInteractiveBuilderChat(t *testing.T) {
	now := time.Now().UTC()
	api := &StreamingAPI{
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			"agent": {
				ExecutionID:   "msgseq-execute-allocate-execute-and-verify-1",
				SessionID:     "schedule-cron--5227790a_1784694634941870000",
				Source:        trackedExecutionSourceWorkshopBackground,
				Kind:          "workflow_builder_task",
				Name:          "Agent item -> [Execute] Allocator",
				Title:         "Agent item -> [Execute] Allocator",
				WorkspacePath: "Workflow/social-media",
				PhaseID:       "workflow-builder",
				PhaseName:     "Workflow Builder",
				Status:        trackedExecutionStatusRunning,
				TriggeredBy:   "workflow_builder",
				StartedAt:     now,
			},
		},
	}

	found := api.findRunningTrackedExecutionForWorkspaceWhere("Workflow/social-media", func(exec *TrackedWorkflowExecution) bool {
		return trackedExecutionBlocksNewWorkflowBuilderChat(exec)
	})
	if found != nil {
		t.Fatalf("builder lookup = %#v, want nil for scheduled agent work", found)
	}
}

func TestInteractiveWorkflowBuilderTaskBlocksNewBuilderChat(t *testing.T) {
	now := time.Now().UTC()
	exec := &TrackedWorkflowExecution{
		ExecutionID:   "builder-chat-1",
		SessionID:     "builder-chat-session",
		Source:        trackedExecutionSourceWorkshopBackground,
		Kind:          "workflow_builder_task",
		WorkspacePath: "Workflow/social-media",
		PhaseID:       "workflow-builder",
		Status:        trackedExecutionStatusRunning,
		TriggeredBy:   "workflow_builder",
		StartedAt:     now,
	}

	if !trackedExecutionBlocksNewWorkflowBuilderChat(exec) {
		t.Fatal("interactive workflow-builder task should block a second builder chat")
	}

	// The refusal names who holds the workflow (server A: two co-owners kept getting a bare 409).
	withMemoryUserDirectory(t, `{"users":[{"id":"holder","username":"sam","can_create":false,"can_edit":true}]}`)
	exec.UserID = "holder"
	if name, msg := workflowBusyMessage(exec, "other"); name != "sam" || !strings.Contains(msg, "sam is using the Builder") || !strings.Contains(msg, "Run mode") {
		t.Fatalf("busy message for another user = %q, %q", name, msg)
	}
	if name, msg := workflowBusyMessage(exec, "holder"); name != "" || !strings.Contains(msg, "You are already using the Builder") {
		t.Fatalf("busy message for the holder = %q, %q", name, msg)
	}

	// A step started from a Run-mode session (an MCP execute_step) never edits the workflow and must not hold the
	// lock (server A 2026-10-09: an MCP smoke run blocked a co-owner's Builder chat for 13 minutes).
	t.Cleanup(func() { common.ClearSessionShellConfig(exec.SessionID) })
	common.SetSessionWorkflowReadOnly(exec.SessionID, true)
	if trackedExecutionBlocksNewWorkflowBuilderChat(exec) {
		t.Fatal("work started from a Run-mode session blocked a builder chat")
	}
}

// The chat/schedule pairing is symmetric, and only one half was implemented.
// trackedExecutionBlocksNewWorkflowBuilderChat already lets a user open a chat
// while scheduled work runs; the reverse — a schedule firing while a user has a
// chat open — was rejected with 409 workflow_busy in a few milliseconds, telling
// a cron job to "stop the running chat", which it cannot do.
func TestScheduledRequestBypassesWorkflowBusy(t *testing.T) {
	for name, tc := range map[string]struct {
		sessionID   string
		triggeredBy string
		want        bool
	}{
		"manual schedule trigger": {sessionID: "schedule-manual--44163d28_1785290407859979000", want: true},
		"cron schedule session":   {sessionID: "schedule-cron--8cfba228_1784606404126176000", want: true},
		"cron trigger field":      {sessionID: "b3c7f380-f405-4661-86d4-a9f984a151ce", triggeredBy: "cron", want: true},
		"scheduler trigger field": {sessionID: "b3c7f380-f405-4661-86d4-a9f984a151ce", triggeredBy: "scheduled", want: true},
		"ordinary user chat":      {sessionID: "d0afa8ad-5af3-4a3d-aeea-a6dfa7646975", triggeredBy: "workflow_builder", want: false},
		"bare uuid session":       {sessionID: "b3c7f380-f405-4661-86d4-a9f984a151ce", want: false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := scheduledRequestBypassesWorkflowBusy(tc.sessionID, tc.triggeredBy); got != tc.want {
				t.Fatalf("scheduledRequestBypassesWorkflowBusy(%q, %q) = %v, want %v",
					tc.sessionID, tc.triggeredBy, got, tc.want)
			}
		})
	}
}

// Both directions of the same rule, stated together so neither half can be
// removed without the other's intent being visible.
func TestChatAndScheduleDoNotBlockEachOther(t *testing.T) {
	userChat := &TrackedWorkflowExecution{
		Status:      trackedExecutionStatusRunning,
		PhaseID:     "workflow-builder",
		SessionID:   "d0afa8ad-5af3-4a3d-aeea-a6dfa7646975",
		TriggeredBy: "workflow_builder",
	}
	scheduled := &TrackedWorkflowExecution{
		Status:    trackedExecutionStatusRunning,
		PhaseID:   "workflow-builder",
		SessionID: "schedule-manual--44163d28_1785290407859979000",
	}

	// A running schedule must not block a user opening a chat.
	if trackedExecutionBlocksNewWorkflowBuilderChat(scheduled) {
		t.Fatal("scheduled work must not block a user's builder chat")
	}
	// A running user chat must not block a schedule firing.
	if !scheduledRequestBypassesWorkflowBusy(scheduled.SessionID, scheduled.TriggeredBy) {
		t.Fatal("a schedule must not be refused because a user chat is open")
	}
	// A second interactive chat is still refused — that guard is the point.
	if !trackedExecutionBlocksNewWorkflowBuilderChat(userChat) {
		t.Fatal("a running user chat must still block another interactive chat")
	}
	if scheduledRequestBypassesWorkflowBusy(userChat.SessionID, userChat.TriggeredBy) {
		t.Fatal("an ordinary chat must not claim the scheduled bypass")
	}
}

func TestOnlyFullWorkflowExecutionBlocksSchedule(t *testing.T) {
	for name, tc := range map[string]struct {
		exec *TrackedWorkflowExecution
		want bool
	}{
		"interactive chat": {
			exec: &TrackedWorkflowExecution{Status: trackedExecutionStatusRunning, Kind: "workflow_builder_task"},
			want: false,
		},
		"Pulse reviewer": {
			exec: &TrackedWorkflowExecution{Status: trackedExecutionStatusRunning, Kind: "background_task", PhaseID: "strategy-auditor"},
			want: false,
		},
		"full workflow": {
			exec: &TrackedWorkflowExecution{Status: trackedExecutionStatusRunning, Kind: "full_workflow"},
			want: true,
		},
		"completed full workflow": {
			exec: &TrackedWorkflowExecution{Status: trackedExecutionStatusCompleted, Kind: "full_workflow"},
			want: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := trackedExecutionBlocksScheduledWorkflow(tc.exec); got != tc.want {
				t.Fatalf("trackedExecutionBlocksScheduledWorkflow() = %v, want %v", got, tc.want)
			}
		})
	}
}

// PLAT-766: when someone else is using the Builder on a workflow, another person's Builder message runs in Run mode
// (told who has it) instead of being refused; the holder's own second chat is not handed over.
func TestBuilderHeldByAnotherPersonMovesTheTurnToRunMode(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[{"id":"holder","username":"sam","can_edit":true},{"id":"other","username":"owner","can_create":true}]}`)
	api := &StreamingAPI{trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
		"builder-1": {
			ExecutionID: "builder-1", SessionID: "holder-session", Source: trackedExecutionSourceWorkshopBackground,
			Kind: "workflow_builder_task", WorkspacePath: "Workflow/example-workflow", PhaseID: "workflow-builder",
			Status: trackedExecutionStatusRunning, UserID: "holder", TriggeredBy: "workflow_builder", StartedAt: time.Now().UTC(),
		},
	}}
	req := QueryRequest{AgentMode: "workflow_phase", PhaseID: workflowtypes.WorkflowStatusWorkflowBuilder, SelectedFolder: "Workflow/example-workflow",
		ExecutionOptions: &ExecutionOptions{WorkshopMode: "builder"}}
	if got := api.workflowBuilderHeldByOther("other-session", "other", req); got != "sam" {
		t.Fatalf("another person's Builder message: holder = %q, want sam", got)
	}
	if got := api.workflowBuilderHeldByOther("holder-session-2", "holder", req); got != "" {
		t.Fatalf("the holder's own second chat was handed over to Run mode (holder %q)", got)
	}
	req.builderHeldBy = "sam"
	if notice := agentSessionModeForTurn(req, "other", "other-session", nil, true); !strings.Contains(notice, "sam is using the Builder") || !strings.Contains(notice, "submit_workflow_suggestion") {
		t.Fatalf("Run-mode notice = %q", notice)
	}
}
