package server

import (
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
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

	// A step started from a Run-mode session (an MCP execute_step) never edits the workflow and must not hold the
	// lock (server A 2026-10-09: an MCP smoke run blocked a co-owner's Builder chat for 13 minutes).
	t.Cleanup(func() { common.ClearSessionShellConfig(exec.SessionID) })
	common.SetSessionWorkflowReadOnly(exec.SessionID, true)
	if trackedExecutionBlocksNewWorkflowBuilderChat(exec) {
		t.Fatal("work started from a Run-mode session blocked a builder chat")
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
		"Autopilot reviewer": {
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
