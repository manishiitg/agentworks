package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

// Resolve the target from private executable/ownership records, at save and
// again at execution. A schedule cannot select another Crew's source or owner.
func crewTimerWorkflowContext(ctx context.Context, crewRoot, crewID, owner string, timer productschedule.Schedule) (*ScheduleContext, error) {
	binding, err := findCrewExecutableByID(timer.WorkflowID)
	if err != nil {
		return nil, err
	}
	if binding == nil || binding.CrewID != crewID || binding.CrewRoot != canonicalCrewWorkspaceRoot(crewRoot) || binding.Kind != "script" {
		return nil, fmt.Errorf("Choose a saved Python workflow from this Crew. Convert an old workflow before scheduling it.")
	}
	currentOwner, found := crewProjectOwnerID(binding.CrewRoot)
	if !found || owner == "" || currentOwner != owner {
		return nil, fmt.Errorf("This workflow must run as its current Crew owner. Ask that owner to save the schedule.")
	}
	manifest, found, err := crewScriptManifest(ctx, binding)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("Choose a saved workflow from this Crew.")
	}
	chat := strings.TrimPrefix(timer.ChatKey, crewID+codeChatSideMarker)
	if chat == crewID {
		chat = "main"
	}
	sctx := buildScheduleContext(binding.Root, manifest, WorkflowSchedule{ID: timer.ID, Name: timer.Name, Enabled: timer.Enabled,
		InputPreset: timer.InputPreset, Inputs: timer.Inputs, RouteSelections: timer.RouteSelections, ChatID: chat, Mode: "workshop", WorkshopMode: "run"})
	if timer.Enabled {
		definitions, err := readWorkflowInputDefinitions(ctx, binding.Root)
		if err != nil {
			return nil, err
		}
		values, err := workflow.ResolveWorkflowInputs(definitions, timer.InputPreset, timer.Inputs)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errScriptTimerInputs, err)
		}
		if err := workflow.ValidateWorkflowLaunchInputs(definitions, values); err != nil {
			return nil, fmt.Errorf("%w: %w", errScriptTimerInputs, err)
		}
		if err := validateExecutableSchedule(ctx, sctx); err != nil {
			return nil, err
		}
	}
	return sctx, nil
}

// The dispatcher already owns the Crew chat turn and the one ledger run.
// Launch saved Python directly and append ordinary durable chat receipts.
func (s *ProjectActionDispatcher) executeCrewWorkflowTimer(ctx context.Context, job productScheduleJob, sctx *ScheduleContext, conversation ProductConversationRecord, runsWorkspace, runID, trigger string) (answer string, runErr error) {
	scheduler := s.api.scheduler
	folder, err := allocateImmutableRunFolder(sctx.WorkspacePath, runID, "schedule", ".schedule-run-id")
	if err != nil {
		return "", err
	}
	if err := UpdateScheduleRun(ctx, runsWorkspace, runID, "running", "", nil, folder, conversation.SessionID); err != nil {
		return "", err
	}
	scheduler.stateStoreMu.RLock()
	err = scheduler.stateStore.AssignRunFolder(ctx, runID, folder)
	scheduler.stateStoreMu.RUnlock()
	if err != nil {
		return "", err
	}
	sctx.TriggerSource = trigger
	sctx.OriginSessionID, sctx.ChatDestinationSessionID = conversation.SessionID, conversation.SessionID
	if err := scheduler.appendCrewAutomationMessage(ctx, sctx, &conversation, runID, "started", ""); err != nil {
		return "", err
	}
	defer func() {
		status := "completed"
		detail := fmt.Sprintf("Run files: `%s/runs/%s`", sctx.WorkspacePath, folder)
		if runErr != nil {
			status = "failed"
			detail = runErr.Error() + "\n\n" + detail
		}
		if errors.Is(runErr, context.Canceled) {
			status = "stopped"
		}
		if err := scheduler.appendCrewAutomationMessage(context.WithoutCancel(ctx), sctx, &conversation, runID, status, detail); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("chat result delivery: %w", err))
		}
	}()
	sessionID := scheduler.newScheduleSessionID(sctx)
	if err := scheduler.startCrewScriptInvocation(ctx, sctx, runID, sessionID, folder, &job); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}
	return fmt.Sprintf("%s completed. Run files: `%s/runs/%s`", job.Schedule.Name, sctx.WorkspacePath, folder), nil
}
