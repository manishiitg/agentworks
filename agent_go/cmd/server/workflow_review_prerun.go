package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/contractupgrade"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

// Workflow Review (Plan Drift) is a pre-run check, not a Pulse pass
// (PLAT-697 phase 0, docs/design/pulse_goal_owner.md). Before every run a code
// check reads the plan's drift state: the same due set Pulse's Gate used to be
// forced to act on (steps without a current drift review, steps flagged by a
// plan edit, new broken references from the reference map, plan changes without
// dependency receipts). Clean: the run starts with no AI call. Otherwise the
// Workflow Review agent runs once for that state, fixes what it safely can, and
// the run starts on the reviewed plan; a break it could not fix stops the run
// with the reason. One review per plan revision: a revision already reviewed is
// never reviewed again, so a run never loops on a break the review left.

const (
	workflowReviewScheduleID   = "workflow-review"
	workflowReviewStateFile    = "workflow_review.json"
	workflowReviewStateVersion = 1
	maxReviewedRevisions       = 50
	// workflowReviewQuietPeriod keeps the after-change review from starting in
	// the middle of a Builder editing session.
	workflowReviewQuietPeriod = 10 * time.Minute
	// workflowReviewEvaluateGap bounds how often the tick launcher looks at one
	// workflow.
	workflowReviewEvaluateGap    = 5 * time.Minute
	maxConcurrentWorkflowReviews = 1
)

const (
	workflowReviewOutcomeRunning    = "running"
	workflowReviewOutcomeClean      = "clean"
	workflowReviewOutcomeUnresolved = "unresolved"
	workflowReviewOutcomeIncomplete = "incomplete"
)

// workflowReviewItem is one due Workflow Review item: a plan step, or the
// workflow-level record.
type workflowReviewItem struct {
	StepID string `json:"step_id"`
	Reason string `json:"reason"`
}

// WorkflowReviewRecord is the latest Workflow Review, read by Pulse as an
// input (get_pulse_state) and shown in the Pulse tab's details.
type WorkflowReviewRecord struct {
	Revision   string               `json:"revision"`
	Trigger    string               `json:"trigger"`
	Reason     string               `json:"reason"`
	SessionID  string               `json:"session_id,omitempty"`
	StartedAt  string               `json:"started_at"`
	FinishedAt string               `json:"finished_at,omitempty"`
	Outcome    string               `json:"outcome"`
	Detail     string               `json:"detail,omitempty"`
	Unresolved []workflowReviewItem `json:"unresolved,omitempty"`
}

type workflowReviewState struct {
	Version           int                   `json:"version"`
	Latest            *WorkflowReviewRecord `json:"latest,omitempty"`
	ReviewedRevisions []string              `json:"reviewed_revisions,omitempty"`
	// IncompleteRevisions are revisions whose review did not finish. Runs on
	// them proceed: the review failing is not a break it found.
	IncompleteRevisions []string `json:"incomplete_revisions,omitempty"`
}

func (st *workflowReviewState) reviewed(revision string) bool {
	for _, r := range st.ReviewedRevisions {
		if r == revision {
			return true
		}
	}
	return false
}

func (st *workflowReviewState) incomplete(revision string) bool {
	for _, r := range st.IncompleteRevisions {
		if r == revision {
			return true
		}
	}
	return false
}

func appendBoundedRevision(list []string, revision string) []string {
	for _, r := range list {
		if r == revision {
			return list
		}
	}
	list = append(list, revision)
	if len(list) > maxReviewedRevisions {
		list = list[len(list)-maxReviewedRevisions:]
	}
	return list
}

var workflowReviewStateMu sync.Mutex

func workflowReviewStatePath(workspacePath string) string {
	return filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(strings.Trim(strings.TrimSpace(workspacePath), "/")),
		stepworkflow.PlanningFolderName, workflowReviewStateFile)
}

func readWorkflowReviewState(workspacePath string) workflowReviewState {
	var state workflowReviewState
	raw, err := os.ReadFile(workflowReviewStatePath(workspacePath))
	if err == nil {
		_ = json.Unmarshal(raw, &state)
	}
	return state
}

// updateWorkflowReviewState read-modify-writes the state file.
func updateWorkflowReviewState(workspacePath string, update func(*workflowReviewState)) {
	workflowReviewStateMu.Lock()
	defer workflowReviewStateMu.Unlock()
	state := readWorkflowReviewState(workspacePath)
	update(&state)
	state.Version = workflowReviewStateVersion
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	path := workflowReviewStatePath(workspacePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) != nil {
		return
	}
	if os.Rename(tmp, path) != nil {
		_ = os.Remove(tmp)
	}
}

// LatestWorkflowReview returns the latest Workflow Review, or nil.
func LatestWorkflowReview(workspacePath string) *WorkflowReviewRecord {
	return readWorkflowReviewState(workspacePath).Latest
}

// collectWorkflowReviewItems is the code-only check. It costs a stat walk when
// nothing changed (the reference map recomputes only on a changed fingerprint
// or flags version).
func collectWorkflowReviewItems(workspacePath string) ([]workflowReviewItem, error) {
	due, err := stepworkflow.CollectPlanDriftDueItems(workspacePath)
	if err != nil {
		return nil, err
	}
	items := make([]workflowReviewItem, 0, len(due)+1)
	for _, item := range due {
		items = append(items, workflowReviewItem{StepID: item.StepID, Reason: item.Reason})
	}
	if intake := stepworkflow.BuildPlanChangeDependencyIntake(stepworkflow.CollectPlanChangeBacklog(workspacePath)); intake.Failed {
		items = append(items, workflowReviewItem{
			StepID: stepworkflow.WorkflowDriftReviewStepID,
			Reason: fmt.Sprintf("%d plan change(s) have no record of their dependents being checked.", intake.FailureCount),
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].StepID != items[j].StepID {
			return items[i].StepID < items[j].StepID
		}
		return items[i].Reason < items[j].Reason
	})
	return items, nil
}

// workflowReviewRevision identifies the plan state a review answers: the due
// set and the plan files it reads. A later edit is a new revision even when it
// leaves the same items due; a review that leaves the same state keeps it, so
// the next run is not reviewed again.
func workflowReviewRevision(workspacePath string, items []workflowReviewItem) string {
	h := sha256.New()
	for _, item := range items {
		fmt.Fprintf(h, "%s|%s\n", item.StepID, item.Reason)
	}
	root := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(strings.Trim(strings.TrimSpace(workspacePath), "/")), stepworkflow.PlanningFolderName)
	for _, name := range []string{"plan.json", "step_config.json"} {
		if raw, err := os.ReadFile(filepath.Join(root, name)); err == nil {
			fmt.Fprintf(h, "%s:", name)
			h.Write(raw)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// workflowReviewItemsForRun keeps the items that concern the steps a run
// executes. stepIDs empty means the whole workflow. Workflow-level items
// concern every run.
func workflowReviewItemsForRun(items []workflowReviewItem, stepIDs []string) []workflowReviewItem {
	if len(stepIDs) == 0 {
		return items
	}
	want := map[string]bool{stepworkflow.WorkflowDriftReviewStepID: true}
	for _, id := range stepIDs {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	var out []workflowReviewItem
	for _, item := range items {
		if want[item.StepID] {
			out = append(out, item)
		}
	}
	return out
}

func describeWorkflowReviewItems(items []workflowReviewItem, max int) string {
	parts := make([]string, 0, len(items))
	for i, item := range items {
		if i == max {
			parts = append(parts, fmt.Sprintf("and %d more", len(items)-max))
			break
		}
		name := item.StepID
		if name == stepworkflow.WorkflowDriftReviewStepID {
			name = "workflow"
		}
		parts = append(parts, name+": "+strings.TrimSuffix(item.Reason, "."))
	}
	return strings.Join(parts, "; ")
}

// workflowReviewDecision is what the code check says about one run.
type workflowReviewDecision int

const (
	workflowReviewProceed workflowReviewDecision = iota
	workflowReviewNeeded
	workflowReviewBlocked
)

// decideWorkflowReview is the pure pre-run decision. It never calls an agent.
func decideWorkflowReview(workspacePath string, items []workflowReviewItem, stepIDs []string, state workflowReviewState) (workflowReviewDecision, string, []workflowReviewItem) {
	relevant := workflowReviewItemsForRun(items, stepIDs)
	if len(relevant) == 0 {
		return workflowReviewProceed, "", nil
	}
	revision := workflowReviewRevision(workspacePath, items)
	if state.incomplete(revision) {
		return workflowReviewProceed, revision, relevant
	}
	if state.reviewed(revision) {
		return workflowReviewBlocked, revision, relevant
	}
	return workflowReviewNeeded, revision, relevant
}

func workflowReviewBlockReason(items []workflowReviewItem) string {
	return "Workflow Review found problems it could not fix safely, so the run did not start: " +
		describeWorkflowReviewItems(items, 5) +
		". Fix them in the workflow's Builder chat (the Workflow Review chat has the details), then run again."
}

// ---------------------------------------------------------------------------
// Running the review

type workflowReviewInflight struct {
	done     chan struct{}
	revision string
	outcome  workflowReviewOutcome
}

type workflowReviewOutcome struct {
	completed bool
	sessionID string
	detail    string
}

var (
	workflowReviewInflightMu sync.Mutex
	workflowReviewInflights  = map[string]*workflowReviewInflight{}
	workflowReviewSessions   sync.Map // session ID -> true while a review runs
	workflowReviewEvaluated  = map[string]time.Time{}
)

func isWorkflowReviewSession(sessionID string) bool {
	_, ok := workflowReviewSessions.Load(strings.TrimSpace(sessionID))
	return ok
}

func workflowReviewRunning(workspacePath string) bool {
	workflowReviewInflightMu.Lock()
	defer workflowReviewInflightMu.Unlock()
	_, ok := workflowReviewInflights[workflowReviewKey(workspacePath)]
	return ok
}

func runningWorkflowReviews() int {
	workflowReviewInflightMu.Lock()
	defer workflowReviewInflightMu.Unlock()
	return len(workflowReviewInflights)
}

func workflowReviewKey(workspacePath string) string {
	return strings.Trim(strings.TrimSpace(workspacePath), "/")
}

// reviewWorkflowOnce runs the Workflow Review for this workflow, or waits for
// the one already running. Exactly one review runs per workflow at a time.
func (s *SchedulerService) reviewWorkflowOnce(ctx context.Context, workspacePath, trigger, revision string, items []workflowReviewItem) workflowReviewOutcome {
	key := workflowReviewKey(workspacePath)
	workflowReviewInflightMu.Lock()
	if existing, ok := workflowReviewInflights[key]; ok {
		workflowReviewInflightMu.Unlock()
		select {
		case <-existing.done:
			return existing.outcome
		case <-ctx.Done():
			return workflowReviewOutcome{detail: "stopped while waiting for the Workflow Review"}
		}
	}
	inflight := &workflowReviewInflight{done: make(chan struct{}), revision: revision}
	workflowReviewInflights[key] = inflight
	workflowReviewInflightMu.Unlock()
	defer func() {
		workflowReviewInflightMu.Lock()
		delete(workflowReviewInflights, key)
		workflowReviewInflightMu.Unlock()
		close(inflight.done)
	}()

	reason := describeWorkflowReviewItems(items, 8)
	started := time.Now().UTC().Format(time.RFC3339)
	updateWorkflowReviewState(workspacePath, func(st *workflowReviewState) {
		st.Latest = &WorkflowReviewRecord{Revision: revision, Trigger: trigger, Reason: reason, StartedAt: started, Outcome: workflowReviewOutcomeRunning}
	})
	if runner := workflowReviewSessionRunner; runner != nil {
		inflight.outcome = runner(s, ctx, workspacePath, trigger, reason)
	} else {
		inflight.outcome = s.runWorkflowReviewSession(ctx, workspacePath, trigger, reason)
	}

	// Record the result against the revision it answered and the one it left,
	// so neither is reviewed again.
	after, err := collectWorkflowReviewItems(workspacePath)
	finished := time.Now().UTC().Format(time.RFC3339)
	updateWorkflowReviewState(workspacePath, func(st *workflowReviewState) {
		record := &WorkflowReviewRecord{Revision: revision, Trigger: trigger, Reason: reason, SessionID: inflight.outcome.sessionID, StartedAt: started, FinishedAt: finished, Detail: inflight.outcome.detail}
		switch {
		case !inflight.outcome.completed:
			record.Outcome = workflowReviewOutcomeIncomplete
			st.IncompleteRevisions = appendBoundedRevision(st.IncompleteRevisions, revision)
		case err == nil && len(after) == 0:
			record.Outcome = workflowReviewOutcomeClean
			st.ReviewedRevisions = appendBoundedRevision(st.ReviewedRevisions, revision)
		default:
			record.Outcome = workflowReviewOutcomeUnresolved
			record.Unresolved = after
			st.ReviewedRevisions = appendBoundedRevision(st.ReviewedRevisions, revision)
			if err == nil {
				st.ReviewedRevisions = appendBoundedRevision(st.ReviewedRevisions, workflowReviewRevision(workspacePath, after))
			}
		}
		st.Latest = record
	})
	return inflight.outcome
}

// workflowReviewSessionRunner, when set (tests), replaces the Workflow Review
// conversation, so the pre-run decision can be tested without an agent.
var workflowReviewSessionRunner func(s *SchedulerService, ctx context.Context, workspacePath, trigger, reason string) workflowReviewOutcome

// runWorkflowReviewSession is Pulse's Plan Drift launcher moved out of Pulse:
// the same durable worklist row, the same reviewer contract
// (plan-drift-review.md) and the same receipt check, in a conversation of its
// own that blocks until the review turn completes.
func (s *SchedulerService) runWorkflowReviewSession(ctx context.Context, workspacePath, trigger, reason string) workflowReviewOutcome {
	if s == nil || s.api == nil {
		return workflowReviewOutcome{detail: "the scheduler is not available"}
	}
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found {
		return workflowReviewOutcome{detail: fmt.Sprintf("workflow.json could not be read: %v", err)}
	}
	sched := WorkflowSchedule{
		ID:           workflowReviewScheduleID,
		Name:         "Workflow Review",
		Description:  "Checks that the plan, steps, references and contracts still fit together before a run",
		ScheduleType: "cron",
		Timezone:     "UTC",
		Mode:         "workshop",
		WorkshopMode: "workshop",
	}
	sctx := buildScheduleContext(workspacePath, manifest, sched)
	sctx.TriggerSource = trigger
	if sctx.TriggerSource == "" {
		sctx.TriggerSource = "cron"
	}
	sessionID := s.newScheduleSessionID(sctx)
	workflowReviewSessions.Store(sessionID, true)
	defer workflowReviewSessions.Delete(sessionID)
	outcome := workflowReviewOutcome{sessionID: sessionID}

	if err := recordWorkflowReviewWorklist(ctx, workspacePath, sessionID, reason); err != nil {
		outcome.detail = fmt.Sprintf("could not record the review's worklist: %v", err)
		return outcome
	}
	baseReq := s.buildWorkshopRequest(ctx, sctx)
	turn := func(query string) error {
		reqMap := cloneStringInterfaceMap(baseReq)
		markPulseLifecycleTurn(reqMap)
		reqMap["query"] = query
		err := s.api.startSessionInternal(ctx, reqMap, sessionID, sctx.OwnerUserID, nil)
		contractupgrade.Revoke(sessionID)
		return err
	}
	scheduleLogf("[WORKFLOW REVIEW] starting for %s (session %s): %s", workspacePath, sessionID, reason)
	if err := turn(workflowReviewPrompt(workspacePath, sessionID, reason)); err != nil {
		outcome.detail = fmt.Sprintf("the review turn did not finish: %v", err)
		return outcome
	}
	if receiptErr := validatePulseDueModuleResultsFor(ctx, workspacePath, sessionID, pulseModulePlanDriftReview); receiptErr != nil {
		if err := turn(pulseLifecycleReviewFixContinuationStep(sessionID, receiptErr).query); err != nil {
			outcome.detail = fmt.Sprintf("the review did not record its result: %v", err)
			return outcome
		}
		if receiptErr = validatePulseDueModuleResultsFor(ctx, workspacePath, sessionID, pulseModulePlanDriftReview); receiptErr != nil {
			outcome.detail = fmt.Sprintf("the review did not record its result: %v", receiptErr)
			return outcome
		}
	}
	outcome.completed = true
	scheduleLogf("[WORKFLOW REVIEW] finished for %s (session %s)", workspacePath, sessionID)
	return outcome
}

func workflowReviewPrompt(workspacePath, sessionID, reason string) string {
	return fmt.Sprintf(`WORKFLOW REVIEW BEFORE A RUN. workspace_path=%q, pulse_run_id=%q, module=%q. This is the workflow's Workflow Review (Plan Drift), a check that runs before runs, like a compile step. It is not a Pulse pass and not goal work. The platform's code check found that the plan changed or has new breaks since the last review: %s.

Load read_skill(skills=[{"name":"builder-reference","path":"references/plan-drift-review.md"}]) and follow it exactly. Read the due steps with get_pulse_state(view="module", pulse_run_id=%q). Establish ground truth per due step, apply and verify safe workflow-owned fixes directly, and record each step with record_plan_drift_review. What you cannot fix safely, record as unresolved with its reason in plain words: the waiting run stops on it, and the Builder chat fixes it. Finish with one record_pulse_result for module plan_drift_review. Do not run the workflow, render a dashboard, back up, publish or notify; then stop.`,
		workspacePath, sessionID, pulseModulePlanDriftReview, reason, sessionID)
}

// recordWorkflowReviewWorklist writes only the plan_drift_review row for this
// review run, so the reviewer's typed receipts land exactly as they did inside
// Pulse while the other modules' Pulse state is left alone.
func recordWorkflowReviewWorklist(ctx context.Context, workspacePath, runID, reason string) error {
	normalized, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		return err
	}
	if db == nil {
		return fmt.Errorf("Pulse state database is not available")
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	modeReason := "Workflow Review before a run: " + reason
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO pulse_run_mode (workspace_path, pulse_run_id, mode, reason, recorded_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(workspace_path, pulse_run_id) DO UPDATE SET
			mode=excluded.mode, reason=excluded.reason, recorded_at=excluded.recorded_at`,
		normalized, runID, pulseRunModeDiscovery, modeReason, now); err != nil {
		return err
	}
	evidenceJSON, _ := json.Marshal([]string{"workflow_review:pre_run"})
	if _, err := tx.ExecContext(ctx, `INSERT INTO pulse_module_state (
			module, workspace_path, last_pulse_run_id, last_checked_at, last_decision,
			last_reason, last_gate_decision, last_result, last_result_reason,
			next_check_at, next_check_after_run_id, cooldown_runs,
			evidence_json, updated_at
		) VALUES (?, ?, ?, ?, 'due', ?, 'due', '', '', '', '', 0, ?, ?)
		ON CONFLICT(workspace_path, module) DO UPDATE SET
			last_pulse_run_id=excluded.last_pulse_run_id,
			last_checked_at=excluded.last_checked_at,
			last_decision=excluded.last_decision,
			last_reason=excluded.last_reason,
			last_gate_decision=excluded.last_gate_decision,
			last_result='',
			last_result_reason='',
			next_check_at='',
			next_check_after_run_id='',
			cooldown_runs=0,
			evidence_json=excluded.evidence_json,
			updated_at=excluded.updated_at`,
		pulseModulePlanDriftReview, normalized, runID, now, modeReason, string(evidenceJSON), now); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------------------------------------------------------------------------
// Before a run

// workflowReviewGate is the answer for one run.
type workflowReviewGate struct {
	Proceed     bool
	Reviewed    bool
	Notice      string
	BlockReason string
}

// ensureWorkflowReviewedBeforeRun is the blocking pre-run check for scheduled
// runs: code check, then (only when needed) the review, then the answer.
func (s *SchedulerService) ensureWorkflowReviewedBeforeRun(ctx context.Context, workspacePath string, stepIDs []string, trigger string) workflowReviewGate {
	items, err := collectWorkflowReviewItems(workspacePath)
	if err != nil {
		// The run reads the same files and reports the real error itself.
		return workflowReviewGate{Proceed: true, Notice: "Workflow Review could not read the plan: " + err.Error()}
	}
	decision, revision, relevant := decideWorkflowReview(workspacePath, items, stepIDs, readWorkflowReviewState(workspacePath))
	switch decision {
	case workflowReviewProceed:
		if len(relevant) > 0 {
			return workflowReviewGate{Proceed: true, Notice: "The last Workflow Review of this plan did not finish; running anyway."}
		}
		return workflowReviewGate{Proceed: true}
	case workflowReviewBlocked:
		return workflowReviewGate{BlockReason: workflowReviewBlockReason(relevant)}
	}
	outcome := s.reviewWorkflowOnce(ctx, workspacePath, trigger, revision, items)
	if ctx.Err() != nil {
		return workflowReviewGate{BlockReason: "stopped during the Workflow Review"}
	}
	after, err := collectWorkflowReviewItems(workspacePath)
	if err != nil || !outcome.completed {
		return workflowReviewGate{Proceed: true, Reviewed: true, Notice: "The Workflow Review did not finish (" + outcome.detail + "); running anyway."}
	}
	if left := workflowReviewItemsForRun(after, stepIDs); len(left) > 0 {
		return workflowReviewGate{Reviewed: true, BlockReason: workflowReviewBlockReason(left)}
	}
	return workflowReviewGate{Proceed: true, Reviewed: true}
}

// manualRunWorkflowReview is the pre-run check for run tools called from a
// chat (Builder, Run, a Pulse turn). It never blocks the tool call on an agent:
// when a review is needed it starts it as a background job of the calling
// chat, which shows "Reviewing the workflow before running…" and is notified
// when the review finishes.
func (api *StreamingAPI) manualRunWorkflowReview(ctx context.Context, sessionID, workspacePath, stepID, toolName string) (proceed bool, message string) {
	if api == nil || api.scheduler == nil || strings.TrimSpace(workspacePath) == "" || isWorkflowReviewSession(sessionID) {
		return true, ""
	}
	items, err := collectWorkflowReviewItems(workspacePath)
	if err != nil {
		return true, ""
	}
	var stepIDs []string
	if strings.TrimSpace(stepID) != "" {
		stepIDs = []string{stepID}
	}
	decision, revision, relevant := decideWorkflowReview(workspacePath, items, stepIDs, readWorkflowReviewState(workspacePath))
	switch decision {
	case workflowReviewProceed:
		return true, ""
	case workflowReviewBlocked:
		return false, "workflow_review_blocked: " + workflowReviewBlockReason(relevant)
	}
	if workflowReviewRunning(workspacePath) {
		return false, fmt.Sprintf("Reviewing the workflow before running… A Workflow Review is already running for this workflow, so %s did not start yet. You will be notified here when it finishes; then call %s again.", toolName, toolName)
	}
	api.startWorkflowReviewBackgroundJob(ctx, sessionID, workspacePath, revision, items, toolName)
	return false, fmt.Sprintf("Reviewing the workflow before running… The plan changed since the last Workflow Review (%s), so %s did not start yet. The review runs now as a background job of this chat; you will be notified here when it finishes. Then call %s again: it starts on the reviewed plan, or says what the review could not fix.",
		describeWorkflowReviewItems(relevant, 3), toolName, toolName)
}

// startWorkflowReviewBackgroundJob registers the review as a background job of
// the calling chat so the chat shows it running and gets its result.
func (api *StreamingAPI) startWorkflowReviewBackgroundJob(ctx context.Context, sessionID, workspacePath, revision string, items []workflowReviewItem, toolName string) {
	const name = "Workflow Review"
	instruction := "Reviewing the workflow before running…"
	if api.bgAgentRegistry == nil || strings.TrimSpace(sessionID) == "" {
		go api.scheduler.reviewWorkflowOnce(context.Background(), workspacePath, "manual", revision, items)
		return
	}
	agentID := api.bgAgentRegistry.NextID("workflow-review")
	bgCtx, bgCancel := context.WithCancel(context.Background())
	bgAgent := &BackgroundAgent{
		ID:                agentID,
		ParentExecutionID: api.currentConversationTurnExecutionID(sessionID),
		Name:              name,
		SessionID:         sessionID,
		Instruction:       instruction,
		Kind:              "delegation",
		Status:            BGAgentRunning,
		CreatedAt:         time.Now(),
		cancel:            bgCancel,
	}
	api.bgAgentRegistry.Register(sessionID, bgAgent)
	api.emitBackgroundAgentStarted(sessionID, agentID, name, instruction, "", orchEvents.ExecutionKindSubAgent)
	api.notifyBackgroundAgentStarted(sessionID, agentID)
	api.completionLoopStartedMu.Lock()
	if api.completionLoopStarted == nil {
		api.completionLoopStarted = make(map[string]bool)
	}
	if !api.completionLoopStarted[sessionID] {
		api.completionLoopStarted[sessionID] = true
		go api.backgroundCompletionLoop(sessionID)
	}
	api.completionLoopStartedMu.Unlock()

	go func() {
		defer bgCancel()
		outcome := api.scheduler.reviewWorkflowOnce(bgCtx, workspacePath, "manual", revision, items)
		duration := time.Since(bgAgent.CreatedAt).Truncate(time.Second).String()
		var result string
		after, err := collectWorkflowReviewItems(workspacePath)
		switch {
		case !outcome.completed:
			result = fmt.Sprintf("Workflow Review did not finish (%s). Call %s again; it runs without a review for this plan state.", outcome.detail, toolName)
		case err == nil && len(after) == 0:
			result = fmt.Sprintf("Workflow Review finished: the plan is consistent. Call %s again now to start the run on the reviewed plan.", toolName)
		default:
			result = fmt.Sprintf("Workflow Review finished but could not fix everything safely: %s. A run of the affected steps will not start until this is fixed in the Builder.", describeWorkflowReviewItems(after, 5))
		}
		bgAgent.SetResult(result)
		api.emitBackgroundAgentCompleted(sessionID, agentID, name, "completed", truncateForToolResponse(result, 500), "", duration)
		api.bgAgentRegistry.NotifyCompletion(sessionID, agentID)
	}()
}

// launchDueWorkflowReviews runs after changes: a Builder plan edit, a contract
// upgrade or a flags-version bump all change the due set, and the review runs
// in the background once the workflow has been quiet for a while, so most runs
// find it already done. Only workflows that run unattended (an enabled
// schedule) are reviewed ahead; the rest are reviewed before their next run.
func (s *SchedulerService) launchDueWorkflowReviews(ctx context.Context) {
	if s == nil || s.api == nil {
		return
	}
	if paused, _, err := s.IsGloballyPaused(ctx); err != nil || paused {
		return
	}
	discovered, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	for _, item := range discovered {
		if runningWorkflowReviews() >= maxConcurrentWorkflowReviews {
			return
		}
		if item.Manifest == nil || item.Manifest.Kind == "relay" || workflowSchedulesAllPaused(item.Manifest) || len(item.Manifest.Schedules) == 0 {
			continue
		}
		workspacePath := item.WorkspacePath
		if last, ok := workflowReviewEvaluated[workspacePath]; ok && now.Sub(last) < workflowReviewEvaluateGap {
			continue
		}
		workflowReviewEvaluated[workspacePath] = now
		if workflowReviewRunning(workspacePath) || s.findActiveNonBuilderExecutionForWorkspace(workspacePath) != nil {
			continue
		}
		if !workflowPlanQuiet(workspacePath, now) {
			continue
		}
		items, err := collectWorkflowReviewItems(workspacePath)
		if err != nil {
			continue
		}
		decision, revision, _ := decideWorkflowReview(workspacePath, items, nil, readWorkflowReviewState(workspacePath))
		if decision != workflowReviewNeeded {
			continue
		}
		scheduleLogf("[WORKFLOW REVIEW] plan changed for %s; reviewing ahead of its next run", workspacePath)
		go s.reviewWorkflowOnce(context.Background(), workspacePath, "cron", revision, items)
	}
}

// workflowPlanQuiet reports whether the plan files have not changed for the
// quiet period, so a review does not start mid-edit.
func workflowPlanQuiet(workspacePath string, now time.Time) bool {
	root := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(strings.Trim(strings.TrimSpace(workspacePath), "/")))
	for _, rel := range []string{"workflow.json", "planning/plan.json", "planning/step_config.json"} {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && now.Sub(info.ModTime()) < workflowReviewQuietPeriod {
			return false
		}
	}
	return true
}
