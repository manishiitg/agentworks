package step_based_workflow

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	orchevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

func (hcpo *StepBasedWorkflowOrchestrator) recordInspectionEvents(ctx context.Context, events ...orchevents.BackgroundAgentTranscriptEvent) {
	if hcpo.BaseOrchestrator == nil || hcpo.httpSessionID == "" {
		return
	}
	owner := currentWorkshopParentExecutionID(ctx)
	if owner == "" {
		return
	}
	persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := hcpo.AppendBackgroundAgentTranscriptEvents(persistCtx, hcpo.httpSessionID, owner, events); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("Inspection evidence for %s: %v", owner, err))
	}
}

func (hcpo *StepBasedWorkflowOrchestrator) recordInspectionMode(ctx context.Context, mode string) {
	hcpo.recordInspectionEvents(ctx, orchevents.BackgroundAgentTranscriptEvent{Type: "execution_mode", Text: mode, Timestamp: time.Now()})
}

func agentItemInspectionMode(item AgentItem) string {
	switch item.Type {
	case "scripted":
		return "script"
	case "prevalidation":
		return "validation"
	default:
		return "agent"
	}
}

// A full run is a container: register the script step before dispatch, so it
// has its own durable owner even on the zero-model fast path. Existing manual
// and route execution owners are preserved.
type scriptInspectionOwnerKey struct{}

func (hcpo *StepBasedWorkflowOrchestrator) beginScriptInspection(ctx context.Context, step PlanStepInterface) (context.Context, func(string, error)) {
	if hcpo.httpSessionID != "" {
		ctx = orchestrator.WithBackgroundTranscriptSession(ctx, hcpo.httpSessionID)
	}
	owner := currentWorkshopParentExecutionID(ctx)
	if hcpo.BaseOrchestrator != nil && hcpo.GetLogger() != nil {
		hcpo.GetLogger().Info(fmt.Sprintf("[INSPECTION] script step=%s parent=%s recorder=%t session=%s", step.GetID(), owner, hcpo.workshopExecutionNotifier != nil, hcpo.httpSessionID))
	}
	finish := func(string, error) {}
	if hcpo.workshopExecutionNotifier != nil {
		ctx, finish = hcpo.beginAgentExecution(ctx, step)
	}
	if owner = currentWorkshopParentExecutionID(ctx); owner != "" {
		ctx = context.WithValue(ctx, scriptInspectionOwnerKey{}, owner)
	}
	return ctx, finish
}

func scriptInspectionEnd(start time.Time, stdout, stderr string, code int, status string, err error) []orchevents.BackgroundAgentTranscriptEvent {
	events := []orchevents.BackgroundAgentTranscriptEvent{
		{Timestamp: start, Type: "script_stdout", Text: stdout},
		{Timestamp: start, Type: "script_stderr", Text: stderr},
		{Timestamp: start, Type: "script_exit", Text: strconv.Itoa(code)},
		{Timestamp: start, Type: "script_status", Text: status},
		{Timestamp: start, Type: "script_end", Text: time.Now().Format(time.RFC3339Nano)},
	}
	if err != nil {
		events = append(events, orchevents.BackgroundAgentTranscriptEvent{Timestamp: start, Type: "script_error", Text: err.Error()})
	}
	return events
}

// An agent can generate/run main.py itself. Preserve its source at the end of
// this attempt, labeled as a completion snapshot rather than a dispatch receipt.
// Do not duplicate a controller-dispatched source already captured unchanged.
func (hcpo *StepBasedWorkflowOrchestrator) recordScriptCompletionSnapshot(ctx context.Context, path, source, output, errMsg string) {
	if source == "" || hcpo.BaseOrchestrator == nil || hcpo.httpSessionID == "" {
		return
	}
	owner := currentWorkshopParentExecutionID(ctx)
	if owner == "" {
		return
	}
	readCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	content, _ := hcpo.ReadWorkspaceFile(readCtx, orchevents.BackgroundAgentTranscriptPath(hcpo.GetWorkspacePath(), hcpo.httpSessionID, owner))
	if saved, err := orchevents.ParseBackgroundAgentTranscript(content); err == nil && saved != nil {
		for i := len(saved.Events) - 1; i >= 0; i-- {
			if saved.Events[i].Type == "script_source" {
				if saved.Events[i].Text == redaction.Text(source) {
					return
				}
				break
			}
		}
	}
	at := time.Now()
	hcpo.recordInspectionEvents(ctx,
		orchevents.BackgroundAgentTranscriptEvent{Timestamp: at, Type: "script_origin", Text: "completion_snapshot"},
		orchevents.BackgroundAgentTranscriptEvent{Timestamp: at, Type: "script_source", Text: source},
		orchevents.BackgroundAgentTranscriptEvent{Timestamp: at, Type: "script_path", Text: path},
		orchevents.BackgroundAgentTranscriptEvent{Timestamp: at, Type: "script_stdout", Text: output},
		orchevents.BackgroundAgentTranscriptEvent{Timestamp: at, Type: "script_error", Text: errMsg},
		orchevents.BackgroundAgentTranscriptEvent{Timestamp: at, Type: "script_status", Text: "snapshot"},
		orchevents.BackgroundAgentTranscriptEvent{Timestamp: at, Type: "script_end", Text: at.Format(time.RFC3339Nano)})
}
