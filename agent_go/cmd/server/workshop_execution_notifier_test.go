package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkflowStepAutoNotificationDelay(t *testing.T) {
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	youngStep := BackgroundAgentSnapshot{
		ID:        "workflow-step-rds-session-timing",
		Kind:      "workflow_step",
		CreatedAt: now.Add(-3 * time.Second),
	}
	if got, want := workflowStepAutoNotificationDelay(youngStep, now), 17*time.Second; got != want {
		t.Fatalf("young step delay = %s, want %s", got, want)
	}

	oldStep := youngStep
	oldStep.CreatedAt = now.Add(-minimumWorkflowStepAutoNotificationAge)
	if got := workflowStepAutoNotificationDelay(oldStep, now); got != 0 {
		t.Fatalf("old step delay = %s, want immediate notification", got)
	}

	backgroundTask := youngStep
	backgroundTask.ID = "generic-research"
	backgroundTask.Kind = "generic_agent"
	if got := workflowStepAutoNotificationDelay(backgroundTask, now); got != 0 {
		t.Fatalf("non-step delay = %s, want immediate notification", got)
	}
}

func TestWorkshopExecutionNotifierSuppressesRepeatedAgentParentFailure(t *testing.T) {
	registry := NewBackgroundAgentRegistry()
	api := &StreamingAPI{bgAgentRegistry: registry}
	const (
		sessionID = "agent-parent-failure"
		parentID  = "workflow-full-script"
		childID   = "msgseq-script-execute"
		rootError = `agent step "shortform-script" item "execute-and-verify" reported STATUS: FAILED: sandbox unavailable`
	)
	parent := &BackgroundAgent{
		ID: parentID, Name: "Script [default]", SessionID: sessionID,
		Status: BGAgentRunning, CreatedAt: time.Now(),
	}
	child := &BackgroundAgent{
		ID: childID, ParentExecutionID: parentID, Name: "Agent item -> Script / execute-and-verify (user_message)",
		SessionID: sessionID, Kind: "agent_item", Status: BGAgentFailed, Error: rootError, CreatedAt: time.Now(),
		Metadata: map[string]string{"execution_type": "agent-item"},
	}
	registry.Register(sessionID, parent)
	registry.Register(sessionID, child)
	completionCh := registry.GetNotificationChannel(sessionID)

	notifier := &workshopExecutionBgNotifier{api: api, sessionID: sessionID}
	notifier.OnExecutionComplete(parentID, parent.Name, "", nil, fmt.Errorf("agent step 13 execution failed: %s", rootError))

	if got := parent.GetSnapshot().Metadata["notification_suppression"]; got != "repeated-agent-child-failure" {
		t.Fatalf("notification_suppression = %q, want repeated child failure marker", got)
	}
	select {
	case got := <-completionCh:
		t.Fatalf("repeated parent failure queued a second auto-notification for %q", got)
	default:
	}
}

func TestWorkshopExecutionNotifierSuppressesParentOwnedAgentSuccess(t *testing.T) {
	registry := NewBackgroundAgentRegistry()
	api := &StreamingAPI{bgAgentRegistry: registry}
	const (
		sessionID = "agent-child-success"
		parentID  = "workflow-full-script"
		childID   = "msgseq-script-execute"
	)
	parent := &BackgroundAgent{ID: parentID, Name: "Script [default]", SessionID: sessionID, Status: BGAgentRunning, CreatedAt: time.Now()}
	child := &BackgroundAgent{
		ID: childID, ParentExecutionID: parentID, Name: "Agent item -> Script / execute-and-verify (user_message)",
		SessionID: sessionID, Kind: "agent_item", Status: BGAgentRunning, CreatedAt: time.Now(),
		Metadata: map[string]string{"execution_type": "agent-item"},
	}
	registry.Register(sessionID, parent)
	registry.Register(sessionID, child)
	completionCh := registry.GetNotificationChannel(sessionID)

	notifier := &workshopExecutionBgNotifier{api: api, sessionID: sessionID}
	notifier.OnExecutionComplete(childID, child.Name, "script complete", child.Metadata, nil)

	if got := child.GetSnapshot().Metadata["notification_suppression"]; got != "parent-owned-agent-success" {
		t.Fatalf("notification_suppression = %q, want parent-owned success marker", got)
	}
	select {
	case got := <-completionCh:
		t.Fatalf("parent-owned agent success queued a duplicate auto-notification for %q", got)
	default:
	}
}

func TestWorkshopExecutionNotifierKeepsStandaloneAgentSuccess(t *testing.T) {
	registry := NewBackgroundAgentRegistry()
	api := &StreamingAPI{bgAgentRegistry: registry}
	const (
		sessionID = "standalone-agent-success"
		childID   = "msgseq-standalone-execute"
	)
	child := &BackgroundAgent{
		ID: childID, ParentExecutionID: "conversation-root-not-in-registry", Name: "Agent item -> Script / execute-and-verify (user_message)",
		SessionID: sessionID, Kind: "agent_item", Status: BGAgentRunning, CreatedAt: time.Now(),
	}
	registry.Register(sessionID, child)
	completionCh := registry.GetNotificationChannel(sessionID)

	notifier := &workshopExecutionBgNotifier{api: api, sessionID: sessionID}
	notifier.OnExecutionComplete(childID, child.Name, "script complete", nil, nil)

	select {
	case got := <-completionCh:
		if got != childID {
			t.Fatalf("completion id = %q, want %q", got, childID)
		}
	default:
		t.Fatal("standalone agent success notification was suppressed")
	}
}

func TestWorkshopExecutionNotifierKeepsDistinctParentFailureNotification(t *testing.T) {
	registry := NewBackgroundAgentRegistry()
	api := &StreamingAPI{bgAgentRegistry: registry}
	const (
		sessionID = "distinct-parent-failure"
		parentID  = "workflow-full-script"
	)
	parent := &BackgroundAgent{ID: parentID, Name: "Script [default]", SessionID: sessionID, Status: BGAgentRunning, CreatedAt: time.Now()}
	child := &BackgroundAgent{
		ID: "msgseq-child", ParentExecutionID: parentID, SessionID: sessionID, Kind: "agent_item",
		Status: BGAgentFailed, Error: "child failed validation", CreatedAt: time.Now(),
	}
	registry.Register(sessionID, parent)
	registry.Register(sessionID, child)
	completionCh := registry.GetNotificationChannel(sessionID)

	notifier := &workshopExecutionBgNotifier{api: api, sessionID: sessionID}
	notifier.OnExecutionComplete(parentID, parent.Name, "", nil, fmt.Errorf("parent failed while persisting the run summary"))

	select {
	case got := <-completionCh:
		if got != parentID {
			t.Fatalf("completion id = %q, want %q", got, parentID)
		}
	default:
		t.Fatal("distinct parent failure notification was suppressed")
	}
}

func TestWorkshopExecutionNotifierReportsUnexpectedContextCancelAsFailure(t *testing.T) {
	registry := NewBackgroundAgentRegistry()
	api := &StreamingAPI{
		bgAgentRegistry: registry,
	}
	const (
		sessionID = "pulse-harden-session"
		execID    = "harden-20000"
	)
	agent := &BackgroundAgent{
		ID:        execID,
		Name:      "Harden Workflow",
		SessionID: sessionID,
		Status:    BGAgentRunning,
		CreatedAt: time.Now().Add(-10 * time.Minute),
	}
	registry.Register(sessionID, agent)
	completionCh := registry.GetNotificationChannel(sessionID)

	notifier := &workshopExecutionBgNotifier{api: api, sessionID: sessionID}
	notifier.OnExecutionComplete(execID, agent.Name, "", nil, context.Canceled)

	snap := agent.GetSnapshot()
	if snap.Status != BGAgentFailed {
		t.Fatalf("status = %q, want failed", snap.Status)
	}
	if snap.Error != context.Canceled.Error() {
		t.Fatalf("error = %q, want %q", snap.Error, context.Canceled.Error())
	}
	select {
	case got := <-completionCh:
		if got != execID {
			t.Fatalf("completion id = %q, want %q", got, execID)
		}
	default:
		t.Fatal("expected failed background execution to queue a parent auto-notification")
	}
}

func TestWorkshopExecutionNotifierPreservesExplicitCancellation(t *testing.T) {
	registry := NewBackgroundAgentRegistry()
	api := &StreamingAPI{
		bgAgentRegistry: registry,
	}
	const (
		sessionID = "explicit-stop-session"
		execID    = "harden-stopped"
	)
	agent := &BackgroundAgent{
		ID:        execID,
		Name:      "Harden Workflow",
		SessionID: sessionID,
		Status:    BGAgentCanceled,
		CreatedAt: time.Now(),
	}
	registry.Register(sessionID, agent)
	completionCh := registry.GetNotificationChannel(sessionID)

	notifier := &workshopExecutionBgNotifier{api: api, sessionID: sessionID}
	notifier.OnExecutionComplete(execID, agent.Name, "", nil, context.Canceled)

	if got := agent.GetStatus(); got != BGAgentCanceled {
		t.Fatalf("status = %q, want canceled", got)
	}
	select {
	case got := <-completionCh:
		t.Fatalf("unexpected completion notification for explicitly canceled execution: %q", got)
	default:
	}
}

func TestWorkshopExecutionNotifierQueuesReviewerCompletionForParent(t *testing.T) {
	registry := NewBackgroundAgentRegistry()
	api := &StreamingAPI{bgAgentRegistry: registry}
	const (
		sessionID = "pulse-review-session"
		execID    = "pulse-review-learning-health"
	)
	agent := &BackgroundAgent{
		ID:        execID,
		Name:      "Pulse reviewer: learning health",
		SessionID: sessionID,
		Status:    BGAgentRunning,
		CreatedAt: time.Now(),
		Metadata: map[string]string{
			"execution_type":     "pulse-reviewer",
			"pulse_reviewer":     "true",
			"module":             "learning_health",
			"review_result_path": "pulse/reviews/run-1/learning_health.md",
		},
	}
	registry.Register(sessionID, agent)
	completionCh := registry.GetNotificationChannel(sessionID)

	notifier := &workshopExecutionBgNotifier{api: api, sessionID: sessionID}
	notifier.OnExecutionComplete(execID, agent.Name, "review complete", agent.Metadata, nil)

	if got := agent.GetStatus(); got != BGAgentCompleted {
		t.Fatalf("status = %q, want completed", got)
	}
	select {
	case got := <-completionCh:
		if got != execID {
			t.Fatalf("completion id = %q, want %q", got, execID)
		}
	default:
		t.Fatal("expected reviewer completion to queue a parent auto-notification")
	}
}

func TestWorkshopCompletionDisplayResultUsesPulseReviewArtifact(t *testing.T) {
	workspacePath := t.TempDir()
	resultPath := filepath.Join("pulse", "reviews", "run-1", "eval_health.md")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(workspacePath, resultPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspacePath, resultPath), []byte(`# Pulse reviewer result

- Module: eval_health

## Findings

The evaluation found a stale metric source.

**Next:** correct the source and verify the next run.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	got := workshopCompletionDisplayResult(workspacePath, "review saved at "+resultPath, map[string]string{
		"execution_type":     "pulse-reviewer",
		"review_result_path": resultPath,
	})

	want := "The evaluation found a stale metric source.\n\n**Next:** correct the source and verify the next run."
	if got != want {
		t.Fatalf("display result = %q, want %q", got, want)
	}
}

func TestWorkshopCompletionDisplayResultRejectsArtifactOutsideWorkspace(t *testing.T) {
	workspacePath := t.TempDir()
	got := workshopCompletionDisplayResult(workspacePath, "review saved", map[string]string{
		"execution_type":     "pulse-reviewer",
		"review_result_path": "../outside.md",
	})
	if got != "review saved" {
		t.Fatalf("display result = %q, want fallback", got)
	}
}
