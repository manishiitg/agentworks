package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	"time"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Only the authenticated scheduler can bind a direct webhook execution. A
// public /api/query request cannot set this context value or choose hook files.
type directWebhookExecutionKey struct{}

func directWebhookPreflight(manifest *WorkflowManifest) error {
	if manifest == nil || !manifestContractIsExecutionCompatible(manifest) || manifest.CodeLayoutVersion != 1 {
		return fmt.Errorf("manually update the workflow contract in Workshop before invoking its webhook: %w", errWorkflowContractMigrationRequired)
	}
	return nil
}

func configureDirectWebhookRequest(req map[string]interface{}, sctx *ScheduleContext, runFolder string) (*stepworkflow.ExecutionOptions, error) {
	req["agent_mode"] = "workflow"
	delete(req, "phase_id")
	delete(req, "keep_native_session_alive")
	// This is a run label, not a natural-language instruction to a Builder.
	req["query"] = "Webhook: " + sctx.Schedule.Name
	optsJSON, err := json.Marshal(req["execution_options"])
	if err != nil {
		return nil, err
	}
	opts := &stepworkflow.ExecutionOptions{}
	if err := json.Unmarshal(optsJSON, opts); err != nil {
		return nil, err
	}
	opts.SelectedRunFolder = runFolder
	opts.RouteSelections = sctx.Schedule.RouteSelections
	if sctx.Schedule.Webhook != nil {
		opts.WebhookStepID = sctx.Schedule.Webhook.StepID
	}
	if sctx.WorkflowKind == "relay" {
		opts.EnabledGroupNames = nil
		opts.RelayLegacyGroupName = sctx.RelayLegacyGroupName
	} else {
		opts.EnabledGroupNames = sctx.Schedule.GroupNames
	}
	opts.WebhookInputFile = webhookInputPath(sctx.WorkspacePath, sctx.WebhookInput.RunID)
	opts.WebhookVariables = sctx.WebhookInput.Variables
	// Keep the serialized options for setup; hidden delivery values travel only
	// in the scheduler-owned context below.
	serializedOpts := req["execution_options"].(map[string]interface{})
	serializedOpts["selected_run_folder"] = runFolder
	serializedOpts["route_selections"] = opts.RouteSelections
	return opts, nil
}

// executeWebhookJob shares authenticated workflow initialization with manual
// execution, then calls the plan executor directly. It never creates a Builder
// turn, upgrades a plan, or reconciles another invocation's run folder.
func (s *SchedulerService) executeWebhookJob(ctx context.Context, sctx *ScheduleContext, runID string) (string, string, error) {
	manifest, found, err := ReadWorkflowManifest(ctx, sctx.WorkspacePath)
	if err != nil {
		return "", "", err
	}
	if !found {
		return "", "", fmt.Errorf("webhook workflow manifest not found")
	}
	if err := directWebhookPreflight(manifest); err != nil {
		return "", "", err
	}
	if manifest.Kind == "relay" {
		if err := validateRelayOutputStep(ctx, sctx.WorkspacePath, manifest.RelayOutputStepID); err != nil {
			return "", "", err
		}
	}
	runFolder, err := webhookExecutionRunFolder(sctx, runID)
	if err != nil {
		return "", "", err
	}
	s.stateStoreMu.RLock()
	store := s.stateStore
	s.stateStoreMu.RUnlock()
	if store == nil {
		return "", runFolder, fmt.Errorf("webhook run store unavailable")
	}
	if err := store.AssignRunFolder(ctx, runID, runFolder); err != nil {
		return "", runFolder, err
	}
	sessionID := s.newScheduleSessionID(sctx)
	s.updateRuntimeState(scheduleRuntimeKey(sctx), func(state *ScheduleRuntimeState) {
		state.LastSessionID = sessionID
	})
	if err := UpdateScheduleRun(ctx, sctx.WorkspacePath, runID, "running", "", nil, runFolder, sessionID); err != nil {
		return sessionID, runFolder, err
	}
	req := s.buildWorkshopRequest(ctx, sctx)
	opts, err := configureDirectWebhookRequest(req, sctx, runFolder)
	if err != nil {
		return sessionID, runFolder, err
	}
	s.sessionLogf(sctx, sessionID, "[WEBHOOK] Direct workflow execution (run=%s folder=%s)", runID, runFolder)
	startedAt := time.Now().UTC()
	err = s.api.startSessionInternal(context.WithValue(ctx, directWebhookExecutionKey{}, opts), req, sessionID, sctx.OwnerUserID, nil)
	sctx.ProducedRunEvidence = err == nil || s.scheduledWorkflowExecutionProducedEvidence(sessionID, startedAt)
	return sessionID, runFolder, err
}

// Resume must reuse the recorded folder. If its binding disappeared, fail
// instead of allocating an empty folder and replaying completed side effects.
func webhookExecutionRunFolder(sctx *ScheduleContext, runID string) (string, error) {
	if sctx.CapacityResumeRunID == "" {
		return allocateWebhookRunFolder(sctx.WorkspacePath, runID)
	}
	root, err := openWebhookRunRoot(sctx.WorkspacePath, schedulerstate.Run{RunID: runID, RunFolder: sctx.CapacityResumeRunFolder})
	if err != nil {
		return "", fmt.Errorf("capacity resume run folder unavailable: %w", err)
	}
	root.Close()
	return sctx.CapacityResumeRunFolder, nil
}
