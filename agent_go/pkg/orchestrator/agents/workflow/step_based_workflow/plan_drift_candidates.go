package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// planDriftReviewContractVersion identifies which set of checks a completed
// plan_drift_review is required to have run. Bump it whenever the set of
// checks the reviewer turn must perform changes (a new Group 1/2 deterministic
// check, or a new Group 3 judgment check in plan-drift-review.md) -- a step's
// own persisted-edit NeedsReview flag only fires on an edit to THAT step, so
// it never catches a change to the review contract itself. A review recorded
// under an older contract version is due again even though the step hasn't
// changed (second independent PLAT-259 review, 2026-08-30: phase B added
// route_structural_isolation/route_eval_pairing, but a routing step already
// marked reviewed before that change stayed clean and silently never got
// re-reviewed against the new checks). History: 1 = original 9 deterministic
// checks + step-description/learnings/KB/DB-normalization judgment checks
// (PLAT-258 phases 1-6). 2 = adds route_structural_isolation/
// route_eval_pairing for routing steps (PLAT-259 phase B). 3 adds canonical
// reference-backed checks for regular/scripted, agent, todo_task,
// routing, and branch steps. Step types without a new check keep version 2.
const planDriftReviewContractVersion = 3

const (
	agentSequenceBestPracticesDriftCheckID = "agent_best_practices"
	scriptedBestPracticesDriftCheckID      = "scripted_best_practices"
	routingBestPracticesDriftCheckID       = "routing_best_practices"
	branchBestPracticesDriftCheckID        = "branch_best_practices"
)

func requiredStepTypeBestPracticesCheckID(stepType string) string {
	switch strings.TrimSpace(stepType) {
	case string(StepTypeRegular):
		return scriptedBestPracticesDriftCheckID
	case string(StepTypeAgent):
		return agentSequenceBestPracticesDriftCheckID
	case string(StepTypeRouting):
		return routingBestPracticesDriftCheckID
	case string(StepTypeBranch):
		return branchBestPracticesDriftCheckID
	default:
		return ""
	}
}

func requiredPlanDriftReviewContractVersion(stepType string) int {
	if requiredStepTypeBestPracticesCheckID(stepType) != "" {
		return 3
	}
	return 2
}

const contextDependencyFilenameDriftCheckID = "context_dependency_filenames"

func checkContextDependencyFilenames(plan *PlanningResponse, stepID string) StepDriftCheck {
	check := StepDriftCheck{
		CheckID:  contextDependencyFilenameDriftCheckID,
		Status:   stepDriftCheckStatusPass,
		Evidence: "Every context dependency was checked against declared step IDs and uses an artifact filename.",
	}
	if plan == nil {
		check.Evidence = "Plan could not be decoded; no step-ID dependency comparison was available."
		return check
	}
	stepIDs := make(map[string]bool)
	for _, info := range collectAllSteps(append(append([]PlanStepInterface{}, plan.Steps...), plan.OrphanSteps...)) {
		if info.Step != nil {
			stepIDs[strings.TrimSpace(info.Step.GetID())] = true
		}
	}
	for _, info := range collectAllSteps(append(append([]PlanStepInterface{}, plan.Steps...), plan.OrphanSteps...)) {
		if info.Step == nil || strings.TrimSpace(info.Step.GetID()) != stepID {
			continue
		}
		for _, dep := range info.Step.GetContextDependencies() {
			dep = strings.TrimSpace(dep)
			if stepIDs[dep] {
				check.Status = stepDriftCheckStatusFail
				check.Evidence = fmt.Sprintf("context_dependencies contains step ID %q; declare the producer's context_output filename instead.", dep)
				return check
			}
		}
		break
	}
	return check
}

// PlanDriftCandidate is one step with no drift_review record at all, or one
// whose record has needs_review==true (flagged stale by a dependency-
// triggering plan edit since its last completed review), together with the
// deterministic Group 1/2 checks Go could run for it without an agent turn.
//
// This is deliberately the same "computed on read" shape as
// CollectPlanChangeBacklog: nothing is persisted, so a candidate can never be
// lost by anything failing to record, and the list always reflects the
// current step_config.json.
type PlanDriftCandidate struct {
	StepID string `json:"step_id"`
	// StepType is the plan.json step type ("routing", "branch", "regular",
	// ...), precomputed so the reviewer turn can tell which candidates are
	// routing steps (the "route"/major-fork concept, PLAT-259) without an
	// extra lookup -- routing steps get two additional judgment checks
	// (route_structural_isolation, route_eval_pairing) that branch and every
	// other step type do not. Empty if plan.json could not be read/parsed.
	StepType string           `json:"step_type,omitempty"`
	Checks   []StepDriftCheck `json:"checks"`
}

// PlanDriftDueItem is the lightweight UI/status projection of due-ness. It
// intentionally omits deterministic check execution so polling the Pulse UI
// does not repeatedly run report, DB, validation, and scripted-code checks.
type PlanDriftDueItem struct {
	StepID   string `json:"step_id"`
	StepType string `json:"step_type,omitempty"`
	Reason   string `json:"reason"`
}

// CollectPlanDriftDueItems computes the same due boundary as
// CollectPlanDriftCandidates without running the expensive evidence checks.
// It is safe for frequently refreshed status surfaces.
func CollectPlanDriftDueItems(workspacePath string) ([]PlanDriftDueItem, error) {
	workspacePath = strings.Trim(strings.TrimSpace(workspacePath), "/")
	if workspacePath == "" {
		return nil, nil
	}
	planPath := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(workspacePath), PlanningFolderName, "plan.json")
	planRaw, err := os.ReadFile(planPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read planning/plan.json: %w", err)
	}
	stepIDs, err := planStepIDsFromPlanJSON(string(planRaw))
	if err != nil {
		return nil, err
	}
	if len(stepIDs) == 0 {
		return nil, nil
	}
	stepTypeByID := map[string]string{}
	var plan PlanningResponse
	if err := json.Unmarshal(planRaw, &plan); err == nil {
		collectStepTypesByID(plan.Steps, stepTypeByID)
	}

	configPath := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(workspacePath), PlanningFolderName, "step_config.json")
	byID := map[string]StepConfig{}
	configRaw, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read planning/step_config.json: %w", err)
		}
	} else {
		configs, parseErr := ParseStepConfigContent(string(configRaw))
		if parseErr != nil {
			return nil, fmt.Errorf("parse planning/step_config.json: %w", parseErr)
		}
		for _, cfg := range configs {
			byID[cfg.ID] = cfg
		}
	}

	refFlags := openReferenceMapFlags(workspacePath, byID, stepIDs)
	items := make([]PlanDriftDueItem, 0)
	for id := range stepIDs {
		cfg, ok := byID[id]
		reason := ""
		requiredVersion := requiredPlanDriftReviewContractVersion(stepTypeByID[id])
		switch {
		case !ok || cfg.AgentConfigs == nil || cfg.AgentConfigs.DriftReview == nil:
			reason = "No completed Plan Drift review exists for this step."
		case cfg.AgentConfigs.DriftReview.NeedsReview:
			reason = "The plan changed or a prior drift check remains unresolved."
		case cfg.AgentConfigs.DriftReview.ContractVersion < requiredVersion:
			reason = "The saved review predates the current Plan Drift contract."
		case refFlags[id] != "":
			reason = refFlags[id]
		}
		if reason != "" {
			items = append(items, PlanDriftDueItem{StepID: id, StepType: stepTypeByID[id], Reason: reason})
		}
	}
	if cfg, ok := byID[WorkflowDriftReviewStepID]; ok && cfg.AgentConfigs != nil && cfg.AgentConfigs.DriftReview != nil && cfg.AgentConfigs.DriftReview.NeedsReview {
		items = append(items, PlanDriftDueItem{StepID: WorkflowDriftReviewStepID, StepType: "workflow", Reason: "A deleted step's dependent artifacts still require review."})
	}
	if reason := refFlags[WorkflowDriftReviewStepID]; reason != "" && !hasDriftDueItem(items, WorkflowDriftReviewStepID) {
		items = append(items, PlanDriftDueItem{StepID: WorkflowDriftReviewStepID, StepType: "workflow", Reason: reason})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].StepID < items[j].StepID })
	return items, nil
}

// planDriftPlainFileReader adapts the workspace-relative paths that
// normalizePathForWorkspaceAPI produces to a plain filesystem read, for
// callers outside a tool-call context (no readFile callback available). This
// is read-only evidence gathering, never a mutation path.
func planDriftPlainFileReader(_ context.Context, workspaceRelativePath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(workspaceRelativePath)))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// planStepIDsFromPlanJSON returns every step id declared in plan.json's
// top-level "steps" array, recursing into routing/branch sub-agent steps the
// same way collectRawPlanStepIDs already does for the agent code
// migration. Deliberately excludes "orphan_steps" — those are detached,
// non-executing steps, not live plan surface a drift review needs to cover.
func planStepIDsFromPlanJSON(planContent string) (map[string]bool, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal([]byte(planContent), &document); err != nil {
		return nil, fmt.Errorf("parse planning/plan.json: %w", err)
	}
	var rawSteps []json.RawMessage
	if err := json.Unmarshal(document["steps"], &rawSteps); err != nil {
		return nil, fmt.Errorf("parse planning/plan.json steps: %w", err)
	}
	ids := map[string]bool{}
	for _, raw := range rawSteps {
		collectRawPlanStepIDs(raw, ids)
	}
	return ids, nil
}

// collectStepTypesByID records step.StepType() for every step, recursing
// into a todo_task's predefined_routes' sub_agent_step exactly the way
// collectRawPlanStepIDs/collectKnownWorkflowStepIDs already recurse for step
// IDs -- a nested sub-agent step (which can itself be a routing or branch
// step) is a real, independently-typed plan.json step, not part of its
// parent todo_task's own type.
func collectStepTypesByID(steps []PlanStepInterface, out map[string]string) {
	for _, step := range steps {
		if step == nil {
			continue
		}
		if id := strings.TrimSpace(step.GetID()); id != "" {
			out[id] = string(step.StepType())
		}
		if todo, ok := step.(*AgentPlanStep); ok {
			for _, route := range todo.PredefinedRoutes {
				if route.SubAgentStep == nil {
					continue
				}
				collectStepTypesByID([]PlanStepInterface{route.SubAgentStep}, out)
			}
		}
	}
}

// CollectPlanDriftCandidates scans planning/plan.json for the plan's real
// step set, left-joins it against planning/step_config.json's drift_review
// records, and runs the deterministic checks Go can run for each step still
// pending review — so a plan_drift_review reviewer turn starts from
// pre-computed evidence instead of re-deriving it via tool calls.
//
// A step with no step_config.json row at all is exactly as pending as one
// with a row but a flagged (needs_review==true) or missing drift_review: the
// invariant is "has this step been reviewed since it last existed in this
// configured form," and a missing config row has never been reviewed.
// Deriving the step set from step_config.json alone (an earlier version of
// this function did) silently drops any step that never got a config row
// written for an unrelated reason — plan.json is the source of truth for
// which steps exist.
//
// Returns a non-nil error when plan.json or step_config.json exists but
// cannot be read or parsed — the caller must treat that as evidence
// unavailable, never as "nothing is due." A missing plan.json (no plan
// authored yet) or a missing step_config.json (no step has ever been
// configured) are not errors: both legitimately mean "review step_config.json
// coverage from scratch," which the join already handles as 100% pending.
//
// Coverage is intentionally partial for this first pass: Check 2
// (validation_schema DB rules) only covers a step_config.json override, not a
// plan.json-declared schema with no override present (step_config.json's own
// contract is that its validation_schema takes precedence over plan.json's,
// so a step relying purely on the plan.json-declared schema is silently out
// of scope here). Checks 5 (validation_schema file rules, needs the step's
// most recent real run output — no run-folder resolver exists yet) and 13
// (orphaned tables, needs every step's SQL references aggregated across the
// whole plan) are deliberately omitted; the reviewer turn checks both
// directly, the same way it does the Group 3 judgment checks.
func CollectPlanDriftCandidates(ctx context.Context, workspacePath string) ([]PlanDriftCandidate, error) {
	workspacePath = strings.Trim(strings.TrimSpace(workspacePath), "/")
	if workspacePath == "" {
		return nil, nil
	}

	planPath := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(workspacePath), PlanningFolderName, "plan.json")
	planRaw, err := os.ReadFile(planPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read planning/plan.json: %w", err)
	}
	stepIDs, err := planStepIDsFromPlanJSON(string(planRaw))
	if err != nil {
		return nil, err
	}
	if len(stepIDs) == 0 {
		return nil, nil
	}

	// Precompute each candidate's plan.json step type -- lets the reviewer
	// turn tell routing steps (the "route"/major-fork concept) apart from
	// branch and everything else without an extra lookup. Best-effort: a
	// step type this typed parse doesn't recognize just leaves StepType
	// empty for that id, it does not fail the whole scan (planStepIDsFromPlanJSON's
	// raw-JSON walk above is what actually determines candidacy). Recurses
	// into todo_task predefined_routes' sub_agent_step the same way
	// planStepIDsFromPlanJSON's raw-JSON walk does -- a nested routing step
	// is a real candidate (its id is in stepIDs) and must not silently lose
	// its type, or route_structural_isolation/route_eval_pairing get skipped
	// for it (second independent review, 2026-08-30).
	stepTypeByID := map[string]string{}
	var plan PlanningResponse
	planDecoded := false
	if err := json.Unmarshal(planRaw, &plan); err == nil {
		planDecoded = true
		collectStepTypesByID(plan.Steps, stepTypeByID)
	}

	configPath := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(workspacePath), PlanningFolderName, "step_config.json")
	byID := map[string]StepConfig{}
	configRaw, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read planning/step_config.json: %w", err)
		}
		// No step_config.json at all: every plan step is pending, byID stays empty.
	} else {
		configs, parseErr := ParseStepConfigContent(string(configRaw))
		if parseErr != nil {
			return nil, fmt.Errorf("parse planning/step_config.json: %w", parseErr)
		}
		for _, cfg := range configs {
			byID[cfg.ID] = cfg
		}
	}

	refFlags := openReferenceMapFlags(workspacePath, byID, stepIDs)
	var pendingStepIDs []string
	for id := range stepIDs {
		cfg, ok := byID[id]
		requiredVersion := requiredPlanDriftReviewContractVersion(stepTypeByID[id])
		if !ok || cfg.AgentConfigs == nil || cfg.AgentConfigs.DriftReview == nil ||
			cfg.AgentConfigs.DriftReview.NeedsReview ||
			cfg.AgentConfigs.DriftReview.ContractVersion < requiredVersion ||
			refFlags[id] != "" {
			pendingStepIDs = append(pendingStepIDs, id)
		}
	}
	sort.Strings(pendingStepIDs)

	// The workflow-level record (WorkflowDriftReviewStepID) is a DIFFERENT
	// invariant from every real step's: a missing record here means "this
	// workflow has never deleted a step," not "pending" — flagWorkflowDriftReviewOnDeletion
	// only ever creates the record when a deletion actually happens, so
	// requiring a first review of a record that was never created by any real
	// event would force every workflow into a needless workflow-level audit
	// pass. Only an EXISTING record with NeedsReview==true is pending.
	workflowLevelPending := false
	if cfg, ok := byID[WorkflowDriftReviewStepID]; ok && cfg.AgentConfigs != nil && cfg.AgentConfigs.DriftReview != nil && cfg.AgentConfigs.DriftReview.NeedsReview {
		workflowLevelPending = true
	}
	if refFlags[WorkflowDriftReviewStepID] != "" {
		workflowLevelPending = true
	}
	if len(pendingStepIDs) == 0 && !workflowLevelPending {
		return nil, nil
	}

	// Workflow-wide checks run once and are attached to every candidate step:
	// a report query or db/README contract break isn't owned by one step, but
	// every step with an unreviewed record should see it as evidence.
	reportCheck, _ := CheckReportQueryCompatibility(ctx, workspacePath, planDriftPlainFileReader)
	readmeCheck, _ := CheckDBReadmeContract(ctx, workspacePath, planDriftPlainFileReader)

	// Description layout (contract 1.0.46): a pure text check, so it runs from the
	// raw plan even when the typed decode failed.
	descriptions, descErr := planStepDescriptionsFromPlanJSON(planRaw)

	candidates := make([]PlanDriftCandidate, 0, len(pendingStepIDs)+1)
	for _, stepID := range pendingStepIDs {
		checks := []StepDriftCheck{reportCheck, readmeCheck}
		if planDecoded {
			checks = append(checks, checkContextDependencyFilenames(&plan, stepID))
		}
		if descErr == nil {
			checks = append(checks, checkDescriptionLayout(descriptions, stepID))
		}

		scriptedCheck, _ := CheckScriptedCodeDBQueries(ctx, workspacePath, stepID, planDriftPlainFileReader)
		checks = append(checks, scriptedCheck)

		if cfg, ok := byID[stepID]; ok && cfg.ValidationSchema != nil && len(cfg.ValidationSchema.DB) > 0 {
			dbCheck, _ := CheckValidationSchemaDBRules(ctx, workspacePath, cfg.ValidationSchema.DB)
			checks = append(checks, dbCheck)
		}

		candidates = append(candidates, PlanDriftCandidate{StepID: stepID, StepType: stepTypeByID[stepID], Checks: checks})
	}
	if workflowLevelPending {
		// No Go-computable per-step evidence exists for a deletion audit — it
		// requires reading planning/changelog/'s delete_plan_steps entries and
		// tracing dependent artifacts, which is exactly the reviewer turn's
		// job (see plan-drift-review.md's workflow-level deletion audit
		// section). Still attach the workflow-wide report/README checks every
		// other candidate gets, for the same reason they're workflow-wide.
		candidates = append(candidates, PlanDriftCandidate{
			StepID:   WorkflowDriftReviewStepID,
			StepType: "workflow",
			Checks:   []StepDriftCheck{reportCheck, readmeCheck},
		})
	}
	return candidates, nil
}

func hasDriftDueItem(items []PlanDriftDueItem, id string) bool {
	for _, item := range items {
		if item.StepID == id {
			return true
		}
	}
	return false
}
