package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"strings"
	"time"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// crewStepPollInterval is the cadence at which a crew step polls its Crew
// run. Crews answer on the order of minutes, so five seconds keeps run
// metadata fresh without hammering the store.
const crewStepPollInterval = 5 * time.Second

// crewStepMaxRecoveryAttempts caps how many terminal-failed duplicate
// deliveries one crew step invocation looks past before giving up. Each
// iteration consumes one real prior failed attempt, so the cap is a
// failsafe, not a retry budget.
const crewStepMaxRecoveryAttempts = 100

// crewStepRunner implements stepworkflow.CrewStepRunner: it invokes a Crew
// trigger for one workflow crew step and polls the run to terminal state.
// One runner serves one workflow run; it closes over the run owner, so crew
// lookups stay user-scoped without trusting step content for identity.
type crewStepRunner struct {
	crews        *ProductScheduleService
	userID       string
	pollInterval time.Duration
}

func newCrewStepRunner(crews *ProductScheduleService, userID string) *crewStepRunner {
	return &crewStepRunner{crews: crews, userID: userID, pollInterval: crewStepPollInterval}
}

// crewRunnerForRun returns the server-owned crew runner for a run, or nil
// when the run has no user or no crew service. The owner resolves the same
// way as every other per-user lookup: live claims when present, the
// single-user default otherwise. Every run path (UI runs, Builder chat
// runs, workshop execute_step/run_full_workflow, live input, schedules,
// webhooks) resolves the runner through here so crew steps execute
// identically everywhere.
func crewRunnerForRun(ctx context.Context, schedules *ProductScheduleService) stepworkflow.CrewStepRunner {
	if ctx == nil || schedules == nil {
		return nil
	}
	userID := GetUserIDFromContext(ctx)
	if strings.TrimSpace(userID) == "" {
		return nil
	}
	return newCrewStepRunner(schedules, userID)
}

// RunCrewStep invokes the trigger and returns the successful Crew run's
// final response. Anything else — revoked access, crew run failed or
// stopped, timeout, or cancellation — returns an error.
func (r *crewStepRunner) RunCrewStep(ctx context.Context, req stepworkflow.CrewStepRequest) (stepworkflow.CrewStepResult, error) {
	if r == nil || r.crews == nil {
		return stepworkflow.CrewStepResult{}, fmt.Errorf("crew steps are unavailable: the crew service is not bound")
	}
	caller := triggerCaller{Type: triggerCallerWorkflow, ID: req.WorkflowID}
	// Fail fast on revoked access before dispatching, using the same
	// permission check as trigger invocation.
	if err := r.crews.checkInternalCrewAccess(ctx, r.userID, req.ProfileID, req.ProjectID, req.TriggerID, caller); err != nil {
		return stepworkflow.CrewStepResult{}, fmt.Errorf("crew step %q cannot invoke its trigger: %w", req.StepID, err)
	}
	runScope := strings.TrimSpace(req.ExecutionID)
	if runScope == "" {
		// No execution identity (a Builder/Workshop step run): every call is
		// a new request. Keying on the run folder instead made a follow-up
		// run of the same step adopt the previous run's stored answer in
		// seconds, since every re-run shares the folder (server A 2026-09-24,
		// crew-cicd-deploy-status). Retries inside this call still share
		// the key below.
		runScope = strings.TrimSpace(req.WorkflowRunFolder) + "/adhoc-" + uuid.NewString()
	}
	payload, err := json.Marshal(map[string]interface{}{
		"source":           "workflow",
		"workflow_id":      req.WorkflowID,
		"workflow_run_id":  runScope,
		"workflow_step_id": req.StepID,
		"group":            req.Group,
		"instruction":      req.Instruction,
		"inputs":           req.Inputs,
	})
	if err != nil {
		return stepworkflow.CrewStepResult{}, fmt.Errorf("crew step %q cannot encode its trigger payload: %w", req.StepID, err)
	}
	// The delivery ID is stable per step attempt, so a retried run adopts
	// the live delivery instead of invoking the trigger twice. It keys on
	// the immutable execution identity: run folders are reused between
	// executions and would let a later execution adopt a stale success.
	// The trigger and its destination join the key so retargeting a step
	// (or flipping isolated/crew_chat) fires a new run instead of
	// adopting the previous trigger's success.
	destination := runDestinationIsolated
	if _, binding, _, _, terr := r.crews.findInternalProductTrigger(ctx, r.userID, req.ProfileID, req.ProjectID, req.TriggerID); terr == nil {
		if owner, ok := crewProjectOwnerID(binding.WorkspacePath); ok {
			owners := r.crews.projectCallerOwners(ctx, r.userID, triggerLinkCaller{Stamp: caller})
			destination = runDestination(!projectsShareOwner(owners, []string{owner}))
		}
	}
	base := crewStepDeliveryBase(req.WorkflowID, runScope, req.Group, req.StepID, req.TriggerID, destination)
	runID := ""
	for attempt := 0; ; attempt++ {
		deliveryID := base
		if attempt > 0 {
			deliveryID = fmt.Sprintf("%s#retry-%d", base, attempt)
		}
		if attempt > crewStepMaxRecoveryAttempts {
			return stepworkflow.CrewStepResult{}, fmt.Errorf("crew step %q gave up after %d prior failed attempts", req.StepID, attempt)
		}
		result, err := r.crews.dispatchInternalProductTrigger(ctx, internalCrewTriggerCall{
			UserID: r.userID, ProfileID: req.ProfileID, ProjectID: req.ProjectID, TriggerID: req.TriggerID,
			Caller: caller, WorkflowRunID: runScope, WorkflowStepID: req.StepID,
			DeliveryID: deliveryID, Payload: payload,
		})
		if err != nil {
			return stepworkflow.CrewStepResult{}, fmt.Errorf("crew step %q cannot invoke its trigger: %w", req.StepID, err)
		}
		runID = result.RunID
		if !result.Duplicate {
			break
		}
		// A duplicate adopts the earlier delivery: a live run is polled
		// below, a successful run is returned as-is, and a terminally
		// failed run re-invokes with a fresh delivery ID.
		status, err := r.crews.getInternalProductTriggerRun(ctx, r.userID, req.ProfileID, req.ProjectID, req.TriggerID, runID, caller)
		if err != nil {
			if ctxErr := crewStepContextError(ctx, req, runID); ctxErr != nil {
				return stepworkflow.CrewStepResult{}, ctxErr
			}
			return stepworkflow.CrewStepResult{}, fmt.Errorf("crew step %q cannot read its adopted run: %w", req.StepID, err)
		}
		if !status.Terminal {
			break
		}
		if status.Status == "success" {
			adopted := crewStepResultFromStatus(status)
			adopted.Timeline = appendCrewPollTransition(nil, status.Status)
			return adopted, nil
		}
	}
	return r.pollCrewStep(ctx, req, caller, runID)
}

// pollCrewStep waits for one Crew run to reach terminal state. The first
// poll is immediate; later polls wait pollInterval. Timeout and
// cancellation arrive through ctx, which the executor deadlines from the
// step's timeout_seconds.
func (r *crewStepRunner) pollCrewStep(ctx context.Context, req stepworkflow.CrewStepRequest, caller triggerCaller, runID string) (stepworkflow.CrewStepResult, error) {
	interval := r.pollInterval
	if interval <= 0 {
		interval = crewStepPollInterval
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	partial := stepworkflow.CrewStepResult{CrewRunID: runID}
	for {
		select {
		case <-ctx.Done():
			return partial, crewStepContextError(ctx, req, runID)
		case <-timer.C:
		}
		status, err := r.crews.getInternalProductTriggerRun(ctx, r.userID, req.ProfileID, req.ProjectID, req.TriggerID, runID, caller)
		if err != nil {
			if ctxErr := crewStepContextError(ctx, req, runID); ctxErr != nil {
				return partial, ctxErr
			}
			return partial, fmt.Errorf("crew step %q lost its crew run %s: %w", req.StepID, runID, err)
		}
		observed := crewStepResultFromStatus(status)
		observed.Timeline = appendCrewPollTransition(partial.Timeline, status.Status)
		partial = observed
		if !status.Terminal {
			timer.Reset(interval)
			continue
		}
		if status.Status == "success" {
			return partial, nil
		}
		detail := strings.TrimSpace(status.Error)
		if detail == "" {
			detail = "no error detail recorded"
		}
		return partial, fmt.Errorf("crew step %q failed: crew run %s ended %s: %s", req.StepID, runID, status.Status, detail)
	}
}

// crewStepContextError reports a timeout or cancellation when ctx ended,
// or nil when ctx is still alive. Poll requests that fail because the
// deadline lapsed mid-request surface as timeouts, not request errors.
func crewStepContextError(ctx context.Context, req stepworkflow.CrewStepRequest, runID string) error {
	if ctx.Err() == nil {
		return nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("crew step %q timed out after %d seconds waiting for crew run %s", req.StepID, req.TimeoutSeconds, runID)
	}
	return fmt.Errorf("crew step %q canceled while waiting for crew run %s: %w", req.StepID, runID, ctx.Err())
}

func crewStepResultFromStatus(status productWebhookRunStatus) stepworkflow.CrewStepResult {
	return stepworkflow.CrewStepResult{
		CrewRunID: status.RunID, SessionID: status.SessionID,
		Status: status.Status, FinalResponse: status.FinalResponse,
		Error: status.Error, StartedAt: status.StartedAt, CompletedAt: status.CompletedAt,
		Usage: status.Usage,
	}
}

// crewStepDeliveryBase builds the idempotency key for one step attempt.
// runScope is the immutable execution identity (see RunCrewStep); group is
// the variable group, which shares the execution but renders its own
// instruction and inputs and must never adopt another group's delivery.
func crewStepDeliveryBase(workflowID, runScope, group, stepID, triggerID, destination string) string {
	return "crew-step:" + workflowID + ":" + runScope + ":" + group + ":" + stepID + ":" + triggerID + ":" + destination
}

// appendCrewPollTransition records a newly observed Crew run status. Only
// changes extend the timeline, so a long steady poll adds no noise.
func appendCrewPollTransition(timeline []stepworkflow.CrewPollTransition, status string) []stepworkflow.CrewPollTransition {
	status = strings.TrimSpace(status)
	if status == "" {
		return timeline
	}
	if len(timeline) > 0 && timeline[len(timeline)-1].Status == status {
		return timeline
	}
	return append(timeline, stepworkflow.CrewPollTransition{Status: status, At: time.Now().UTC()})
}

// checkInternalCrewAccess verifies that a workflow run may invoke a Crew
// trigger, using the same permission check as trigger invocation. Runs call
// it before the first step executes and crew steps call it before each
// invocation, so revoked access fails fast.
func (s *ProductScheduleService) checkInternalCrewAccess(ctx context.Context, userID, profileID, projectID, triggerID string, caller triggerCaller) error {
	_, _, _, trigger, err := s.findInternalProductTrigger(ctx, userID, profileID, projectID, triggerID)
	if err != nil {
		return err
	}
	if !trigger.Caller.matchesPresented(triggerCallerWorkflow, caller) {
		return ErrInternalCallerMismatch
	}
	return nil
}

// preflightCrewSteps verifies Crew access before the run starts: every crew
// attachment must still bind a crew the runner may access, and every crew
// step must still invoke its bound trigger. Attachment checks run even when
// the plan holds no crew step — ordinary downstream steps can read through
// the alias, so a revoked crew must fail the run before any step executes.
// Plans without crew steps or attachments cost one manifest read.
func preflightCrewSteps(ctx context.Context, crews *ProductScheduleService, userID, workspacePath string) error {
	if crews == nil {
		return nil
	}
	plan, err := readPlanFromWorkspace(ctx, workspacePath)
	if err != nil || plan == nil {
		return nil
	}
	seenCrew := false
	for _, step := range plan.Steps {
		if _, ok := step.(*stepworkflow.CrewPlanStep); ok {
			seenCrew = true
			break
		}
	}
	manifest, exists, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !exists || manifest == nil {
		if !seenCrew {
			return nil
		}
		return fmt.Errorf("cannot verify crew access: workflow manifest is unavailable")
	}
	for _, attachment := range manifest.CrewAttachments {
		if err := workflowtypes.ValidateCrewAttachmentBinding(attachment); err != nil {
			return fmt.Errorf("crew attachment %q cannot run: %w; re-attach it read-only with manage_crew_attachment before running", attachment.Alias, err)
		}
		profileID := normalizeInternalProfileID(attachment.CrewProfileID)
		_, binding, _, _, err := crews.projectManifestAnyOwner(ctx, userID, profileID, strings.TrimSpace(attachment.CrewProjectID))
		if err != nil {
			return fmt.Errorf("crew attachment %q cannot run: crew project %q is unavailable or access was revoked: %w", attachment.Alias, strings.TrimSpace(attachment.CrewProjectID), err)
		}
		// The stored root must equal the freshly authorized binding. Shape
		// checks alone accept another owner's same-named project path, so
		// authorizing the project without comparing roots would grant a
		// workspace the check never authorized.
		authorized := workflowtypes.CanonicalCrewAttachmentRoot(binding.WorkspacePath)
		if authorized == "" || workflowtypes.CanonicalCrewAttachmentRoot(attachment.CrewWorkspacePath) != authorized {
			return fmt.Errorf("crew attachment %q cannot run: its stored workspace no longer matches crew project %q; re-attach it read-only with manage_crew_attachment before running", attachment.Alias, strings.TrimSpace(attachment.CrewProjectID))
		}
	}
	if !seenCrew {
		return nil
	}
	for _, step := range plan.Steps {
		crew, ok := step.(*stepworkflow.CrewPlanStep)
		if !ok {
			continue
		}
		if !crewProjectAttached(manifest, crew.CrewProfileID, crew.CrewProjectID) {
			return fmt.Errorf("crew step %q cannot run: crew project %q is not attached to this workflow; attach it read-only with manage_crew_attachment before running", crew.GetID(), crew.CrewProjectID)
		}
		caller := triggerCaller{Type: triggerCallerWorkflow, ID: manifest.ID}
		if err := crews.checkInternalCrewAccess(ctx, userID, crew.CrewProfileID, crew.CrewProjectID, crew.TriggerID, caller); err != nil {
			return fmt.Errorf("crew step %q cannot run: crew access check failed: %w", crew.GetID(), err)
		}
	}
	return nil
}

// crewProjectAttached reports whether a crew project is attached to the
// workflow under any alias.
func crewProjectAttached(manifest *WorkflowManifest, profileID, projectID string) bool {
	if manifest == nil {
		return false
	}
	profileID = normalizeInternalProfileID(profileID)
	for _, attachment := range manifest.CrewAttachments {
		if strings.TrimSpace(attachment.CrewProfileID) == profileID && strings.TrimSpace(attachment.CrewProjectID) == strings.TrimSpace(projectID) {
			return true
		}
	}
	return false
}
