package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// PLAT-556 decisions 2 and 4 plus the success metric, on the Pulse side.
//
// Decision 2: measured budgets (get_plan_prompt_health) and settled read-write
// learning make Architecture due with focus prompt_design / learning_quality.
// They never block a run or an edit; Architecture still decides what to do.
//
// Decision 4: Plan Drift stays the exclusive prerequisite only for the steps
// it flagged. Architecture may be deferred by Drift at most two Pulse passes
// in a row; on the third, or immediately when every budget-flagged step is
// outside Drift's set, it runs in the same pass scoped away from those steps.

const (
	evidencePromptBudgetDue      = "prompt_budget_due:"
	evidencePromptBudgetReviewed = "prompt_budget_reviewed:"
	evidenceArchitectureScoped   = "plan_drift_review:architecture_scoped"
	evidenceArchitectureExcludes = "architecture_scope_excludes:"
	evidenceArchitectureDeferred = "architecture_drift_deferrals:"
	maxArchitectureDriftDeferral = 2
)

const pulsePromptBudgetMetricsSchema = `CREATE TABLE IF NOT EXISTS pulse_prompt_budget_metrics (
	workspace_path TEXT NOT NULL,
	pulse_run_id TEXT NOT NULL,
	recorded_at TEXT NOT NULL,
	steps_with_descriptions INTEGER NOT NULL DEFAULT 0,
	total_description_chars INTEGER NOT NULL DEFAULT 0,
	largest_description_chars INTEGER NOT NULL DEFAULT 0,
	largest_description_step_id TEXT NOT NULL DEFAULT '',
	steps_over_budget INTEGER NOT NULL DEFAULT 0,
	dated_text_count INTEGER NOT NULL DEFAULT 0,
	duplicated_chars INTEGER NOT NULL DEFAULT 0,
	steps_without_layout INTEGER NOT NULL DEFAULT 0,
	consolidation_due_steps INTEGER NOT NULL DEFAULT 0,
	learning_settled_steps INTEGER NOT NULL DEFAULT 0,
	architecture_runs INTEGER NOT NULL DEFAULT 0,
	consolidations_applied INTEGER NOT NULL DEFAULT 0,
	consolidations_kept INTEGER NOT NULL DEFAULT 0,
	consolidations_restored INTEGER NOT NULL DEFAULT 0,
	validation_before_passed INTEGER NOT NULL DEFAULT 0,
	validation_before_known INTEGER NOT NULL DEFAULT 0,
	validation_after_passed INTEGER NOT NULL DEFAULT 0,
	budget_fingerprint TEXT NOT NULL DEFAULT '',
	architecture_budget_due INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (workspace_path, pulse_run_id)
)`

const pulseConsolidationSchema = `CREATE TABLE IF NOT EXISTS pulse_consolidation (
	_id INTEGER PRIMARY KEY AUTOINCREMENT,
	workspace_path TEXT NOT NULL,
	pulse_run_id TEXT NOT NULL,
	module TEXT NOT NULL,
	step_id TEXT NOT NULL,
	focus_key TEXT NOT NULL DEFAULT '',
	change_id TEXT NOT NULL DEFAULT '',
	chars_before INTEGER NOT NULL DEFAULT 0,
	chars_after INTEGER NOT NULL DEFAULT 0,
	validation_before TEXT NOT NULL DEFAULT 'unknown',
	validation_after TEXT NOT NULL,
	restored INTEGER NOT NULL DEFAULT 0,
	recorded_at TEXT NOT NULL
)`

// applyPulseSchedulingRules is every backend scheduling fact applied to a
// Gate worklist, in one order, for both worklist entry points.
func applyPulseSchedulingRules(ctx context.Context, workspacePath string, decisions []PulseWorklistDecision) ([]PulseWorklistDecision, error) {
	decisions, err := forcePendingPulseReviewRecoveries(ctx, workspacePath, decisions)
	if err != nil {
		return nil, err
	}
	budget, budgetErr := step_based_workflow.CollectPromptBudgetDue(ctx, workspacePath)
	if budgetErr != nil {
		// Unknown budget is not a reason to force or block anything.
		log.Printf("[PULSE] prompt budget scan failed for %s: %v", workspacePath, budgetErr)
		budget = step_based_workflow.PromptBudgetDue{}
	}
	previous := previousPulseModuleState(ctx, workspacePath, pulseModuleArchitectureReview)
	reviewed := promptBudgetStateReviewed(ctx, workspacePath, budget.Fingerprint)
	decisions = applyPromptBudgetArchitectureDue(decisions, budget, reviewed)
	decisions, err = applyDisabledPulseReviewModules(ctx, workspacePath, decisions)
	if err != nil {
		return nil, err
	}
	var driftSteps []string
	if pulsePlanDriftDue(decisions) {
		if candidates, err := step_based_workflow.CollectPlanDriftCandidates(ctx, workspacePath); err == nil {
			for _, c := range candidates {
				driftSteps = append(driftSteps, c.StepID)
			}
		}
	}
	return enforcePlanDriftExclusivePassScoped(decisions, previous, budget, driftSteps), nil
}

func previousPulseModuleState(ctx context.Context, workspacePath, module string) *PulseModuleState {
	states, err := getPulseModuleStates(ctx, workspacePath)
	if err != nil {
		return nil
	}
	for i := range states {
		if normalizePulseModule(states[i].Module) == module {
			return &states[i]
		}
	}
	return nil
}

func pulseEvidenceHas(evidence []string, item string) bool {
	for _, e := range evidence {
		if strings.TrimSpace(e) == item {
			return true
		}
	}
	return false
}

func pulseEvidenceValue(evidence []string, prefix string) (string, bool) {
	for _, e := range evidence {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix), true
		}
	}
	return "", false
}

func joinCapped(ids []string, max int) string {
	if len(ids) <= max {
		return strings.Join(ids, ",")
	}
	return strings.Join(ids[:max], ",") + fmt.Sprintf(",+%d more", len(ids)-max)
}

// applyPromptBudgetArchitectureDue makes Architecture due while a budget
// trigger state has not yet been reviewed. A state Architecture already
// completed a review of (same fingerprint) is not forced again; Gate may
// still choose to run it.
func applyPromptBudgetArchitectureDue(decisions []PulseWorklistDecision, budget step_based_workflow.PromptBudgetDue, reviewed bool) []PulseWorklistDecision {
	if !budget.Any() || budget.Fingerprint == "" {
		return decisions
	}
	out := append([]PulseWorklistDecision(nil), decisions...)
	for i := range out {
		if normalizePulseModule(out[i].Module) != pulseModuleArchitectureReview {
			continue
		}
		dueMark := evidencePromptBudgetDue + budget.Fingerprint
		reviewedMark := evidencePromptBudgetReviewed + budget.Fingerprint
		if pulseEvidenceHas(out[i].Evidence, dueMark) {
			return out // already applied to this worklist
		}
		if reviewed {
			out[i].Evidence = append(normalizePulseEvidence(out[i].Evidence), reviewedMark)
			return out
		}
		var parts []string
		evidence := []string{dueMark}
		if len(budget.PromptDesign) > 0 {
			parts = append(parts, fmt.Sprintf("%d step(s) over the prompt budget (size, dated text, duplication or missing layout): focus prompt_design", len(budget.PromptDesign)))
			evidence = append(evidence, "prompt_budget_focus:prompt_design", "prompt_budget_steps:"+joinCapped(budget.PromptDesign, 20))
		}
		if len(budget.LearningSettled) > 0 {
			ids := make([]string, 0, len(budget.LearningSettled))
			for _, s := range budget.LearningSettled {
				ids = append(ids, s.StepID)
			}
			parts = append(parts, fmt.Sprintf("%d read-write learning step(s) settled: focus learning_quality", len(ids)))
			evidence = append(evidence, "prompt_budget_focus:learning_quality", "learning_settled_steps:"+joinCapped(ids, 20))
		}
		out[i].Due = true
		out[i].Reason = "Budget trigger (PLAT-556): " + strings.Join(parts, "; ") + ". " + strings.TrimSpace(out[i].Reason)
		out[i].Evidence = append(normalizePulseEvidence(out[i].Evidence), evidence...)
		out[i].NextCheckAt = ""
		out[i].NextCheckAfterRunID = ""
		out[i].CooldownRuns = 0
		out[i].DeferReason = ""
	}
	return out
}

// enforcePlanDriftExclusivePassScoped is enforcePlanDriftExclusivePass with
// PLAT-556's bounded deferral for Architecture.
func enforcePlanDriftExclusivePassScoped(decisions []PulseWorklistDecision, previous *PulseModuleState, budget step_based_workflow.PromptBudgetDue, driftSteps []string) []PulseWorklistDecision {
	if !pulsePlanDriftDue(decisions) {
		return decisions
	}
	out := append([]PulseWorklistDecision(nil), decisions...)
	for i := range out {
		if normalizePulseModule(out[i].Module) != pulseModuleArchitectureReview || !out[i].Due {
			continue
		}
		if pulseEvidenceHas(out[i].Evidence, evidenceArchitectureScoped) {
			continue
		}
		prior := 0
		if previous != nil {
			if raw, ok := pulseEvidenceValue(previous.Evidence, evidenceArchitectureDeferred); ok {
				prior, _ = strconv.Atoi(raw)
			}
		}
		flagged := map[string]bool{}
		for _, id := range driftSteps {
			flagged[id] = true
		}
		budgetOutside := len(budget.PromptDesign) > 0 && pulseEvidenceHas(out[i].Evidence, evidencePromptBudgetDue+budget.Fingerprint)
		for _, id := range budget.PromptDesign {
			if flagged[id] {
				budgetOutside = false
				break
			}
		}
		if prior >= maxArchitectureDriftDeferral || budgetOutside {
			why := fmt.Sprintf("deferred by Plan Drift %d pass(es) in a row", prior)
			if budgetOutside {
				why = "every budget-flagged step is outside Plan Drift's set"
			}
			out[i].Reason = fmt.Sprintf("Runs alongside Plan Drift (%s), scoped away from the steps Plan Drift flagged: %s. %s", why, joinCapped(driftSteps, 30), out[i].Reason)
			out[i].Evidence = append(normalizePulseEvidence(out[i].Evidence), evidenceArchitectureScoped, evidenceArchitectureExcludes+strings.Join(driftSteps, ","))
			continue
		}
		out[i].Evidence = append(normalizePulseEvidence(out[i].Evidence), evidenceArchitectureDeferred+strconv.Itoa(prior+1))
	}
	// The remaining modules follow the original exclusive-prerequisite rule.
	return enforcePlanDriftExclusivePass(out)
}

// promptBudgetStateReviewed reports whether Architecture already completed a
// review in a pass where this exact budget state (fingerprint) made it due.
// Result rows overwrite module evidence, so the per-pass metric row carries
// the fingerprint and the audit row carries the completed result.
func promptBudgetStateReviewed(ctx context.Context, workspacePath, fingerprint string) bool {
	if fingerprint == "" {
		return false
	}
	normalized, db, err := openPulseModuleStateDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return false
	}
	defer db.Close()
	if ensurePulseModuleStateSchema(ctx, db) != nil {
		return false
	}
	var n int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pulse_prompt_budget_metrics m
		JOIN pulse_module_audit a ON a.workspace_path=m.workspace_path AND a.pulse_run_id=m.pulse_run_id
		WHERE m.workspace_path=? AND m.budget_fingerprint=? AND m.architecture_budget_due=1
		  AND a.module=? AND a.result IN ('done','changed')`, normalized, fingerprint, pulseModuleArchitectureReview).Scan(&n)
	return err == nil && n > 0
}

// pulseArchitectureScopedDuringDrift reports whether this run's worklist lets
// Architecture run in a pass where Plan Drift is due.
func pulseArchitectureScopedDuringDrift(ctx context.Context, workspacePath, pulseRunID string) bool {
	worklist, ok, err := getPulseWorklistForRun(ctx, workspacePath, pulseRunID)
	if err != nil || !ok {
		return false
	}
	state, exists := worklist[pulseModuleArchitectureReview]
	return exists && state.LastDecision == "due" && pulseEvidenceHas(state.Evidence, evidenceArchitectureScoped)
}

// PulseConsolidation is one consolidation Architecture applied and verified
// (or rolled back) in a pass.
type PulseConsolidation struct {
	StepID           string `json:"step_id"`
	FocusKey         string `json:"focus_key,omitempty"`
	ChangeID         string `json:"change_id,omitempty"`
	CharsBefore      int    `json:"chars_before,omitempty"`
	CharsAfter       int    `json:"chars_after,omitempty"`
	ValidationBefore string `json:"validation_before,omitempty"`
	ValidationAfter  string `json:"validation_after"`
	Restored         bool   `json:"restored,omitempty"`
}

func pulseConsolidationsFromToolArg(raw interface{}, module string) ([]PulseConsolidation, error) {
	if raw == nil {
		return nil, nil
	}
	if module != pulseModuleArchitectureReview {
		return nil, fmt.Errorf("consolidations belong to architecture_review results only")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var items []PulseConsolidation
	if err := json.Unmarshal(encoded, &items); err != nil {
		return nil, fmt.Errorf("consolidations must be an array of {step_id, change_id, validation_after, ...}: %w", err)
	}
	for i := range items {
		c := &items[i]
		c.StepID = strings.TrimSpace(c.StepID)
		if c.StepID == "" {
			return nil, fmt.Errorf("consolidations[%d].step_id is required", i)
		}
		c.ValidationAfter = strings.ToLower(strings.TrimSpace(c.ValidationAfter))
		if c.ValidationAfter != "passed" && c.ValidationAfter != "failed" {
			return nil, fmt.Errorf("consolidations[%d].validation_after must be passed or failed (the one comparison run after the edit)", i)
		}
		if c.ValidationAfter == "failed" && !c.Restored {
			return nil, fmt.Errorf("consolidations[%d] failed validation but is not restored: restore it with restore_step_from_changelog before recording the result", i)
		}
		c.ValidationBefore = strings.ToLower(strings.TrimSpace(c.ValidationBefore))
		if c.ValidationBefore != "passed" && c.ValidationBefore != "failed" {
			c.ValidationBefore = "unknown"
		}
	}
	return items, nil
}

func recordPulseConsolidations(ctx context.Context, workspacePath, pulseRunID, module string, items []PulseConsolidation) error {
	if len(items) == 0 {
		return nil
	}
	normalized, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensurePulseModuleStateSchema(ctx, db); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, c := range items {
		restored := 0
		if c.Restored {
			restored = 1
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO pulse_consolidation (workspace_path, pulse_run_id, module, step_id, focus_key, change_id,
			chars_before, chars_after, validation_before, validation_after, restored, recorded_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			normalized, pulseRunID, module, c.StepID, c.FocusKey, c.ChangeID, c.CharsBefore, c.CharsAfter, c.ValidationBefore, c.ValidationAfter, restored, now); err != nil {
			return err
		}
	}
	return nil
}

// PulsePromptBudgetMetric is one pass's success-metric row (PLAT-556).
type PulsePromptBudgetMetric struct {
	PulseRunID               string `json:"pulse_run_id"`
	RecordedAt               string `json:"recorded_at"`
	StepsWithDescriptions    int    `json:"steps_with_descriptions"`
	TotalDescriptionChars    int    `json:"total_description_chars"`
	LargestDescriptionChars  int    `json:"largest_description_chars"`
	LargestDescriptionStepID string `json:"largest_description_step_id,omitempty"`
	StepsOverBudget          int    `json:"steps_over_budget"`
	DatedTextCount           int    `json:"dated_text_count"`
	DuplicatedChars          int    `json:"duplicated_300_chars"`
	StepsWithoutLayout       int    `json:"steps_without_layout"`
	ConsolidationDueSteps    int    `json:"consolidation_due_steps"`
	LearningSettledSteps     int    `json:"learning_settled_steps"`
	ArchitectureRuns         int    `json:"architecture_runs"`
	ConsolidationsApplied    int    `json:"consolidations_applied"`
	ConsolidationsKept       int    `json:"consolidations_kept"`
	ConsolidationsRestored   int    `json:"consolidations_restored"`
	ValidationBeforePassed   int    `json:"validation_before_passed"`
	ValidationBeforeKnown    int    `json:"validation_before_known"`
	ValidationAfterPassed    int    `json:"validation_after_passed"`
}

// recordPulsePromptBudgetMetric stores this pass's measures. Cumulative
// Architecture/consolidation counts are as of the Gate of this pass.
func recordPulsePromptBudgetMetric(ctx context.Context, workspacePath, pulseRunID string) error {
	budget, err := step_based_workflow.CollectPromptBudgetDue(ctx, workspacePath)
	if err != nil {
		return err
	}
	normalized, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensurePulseModuleStateSchema(ctx, db); err != nil {
		return err
	}
	archBudgetDue := 0
	if worklist, ok, err := getPulseWorklistForRun(ctx, workspacePath, pulseRunID); err == nil && ok {
		if state, exists := worklist[pulseModuleArchitectureReview]; exists && state.LastDecision == "due" &&
			budget.Fingerprint != "" && pulseEvidenceHas(state.Evidence, evidencePromptBudgetDue+budget.Fingerprint) {
			archBudgetDue = 1
		}
	}
	h := budget.Health
	m := PulsePromptBudgetMetric{
		PulseRunID: pulseRunID, RecordedAt: time.Now().UTC().Format(time.RFC3339),
		StepsWithDescriptions: h.StepsWithDescriptions, TotalDescriptionChars: h.TotalDescriptionChars,
		LargestDescriptionChars: h.LargestDescriptionChars, LargestDescriptionStepID: h.LargestDescriptionStepID,
		StepsOverBudget: h.StepsOverBudget, DatedTextCount: h.DatedTextCount, DuplicatedChars: h.DuplicatedBudgetChars,
		StepsWithoutLayout: h.StepsWithoutLayout, ConsolidationDueSteps: len(budget.PromptDesign), LearningSettledSteps: len(budget.LearningSettled),
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pulse_module_audit WHERE workspace_path=? AND module=? AND result IN ('done','changed')`,
		normalized, pulseModuleArchitectureReview).Scan(&m.ArchitectureRuns); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*),
			COALESCE(SUM(CASE WHEN restored=0 THEN 1 ELSE 0 END),0),
			COALESCE(SUM(restored),0),
			COALESCE(SUM(CASE WHEN validation_before='passed' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN validation_before IN ('passed','failed') THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN validation_after='passed' THEN 1 ELSE 0 END),0)
		FROM pulse_consolidation WHERE workspace_path=?`, normalized).Scan(
		&m.ConsolidationsApplied, &m.ConsolidationsKept, &m.ConsolidationsRestored,
		&m.ValidationBeforePassed, &m.ValidationBeforeKnown, &m.ValidationAfterPassed); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO pulse_prompt_budget_metrics (workspace_path, pulse_run_id, recorded_at,
			steps_with_descriptions, total_description_chars, largest_description_chars, largest_description_step_id,
			steps_over_budget, dated_text_count, duplicated_chars, steps_without_layout, consolidation_due_steps, learning_settled_steps,
			architecture_runs, consolidations_applied, consolidations_kept, consolidations_restored,
			validation_before_passed, validation_before_known, validation_after_passed, budget_fingerprint, architecture_budget_due)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_path, pulse_run_id) DO UPDATE SET recorded_at=excluded.recorded_at,
			steps_with_descriptions=excluded.steps_with_descriptions, total_description_chars=excluded.total_description_chars,
			largest_description_chars=excluded.largest_description_chars, largest_description_step_id=excluded.largest_description_step_id,
			steps_over_budget=excluded.steps_over_budget, dated_text_count=excluded.dated_text_count, duplicated_chars=excluded.duplicated_chars,
			steps_without_layout=excluded.steps_without_layout, consolidation_due_steps=excluded.consolidation_due_steps,
			learning_settled_steps=excluded.learning_settled_steps, architecture_runs=excluded.architecture_runs,
			consolidations_applied=excluded.consolidations_applied, consolidations_kept=excluded.consolidations_kept,
			consolidations_restored=excluded.consolidations_restored, validation_before_passed=excluded.validation_before_passed,
			validation_before_known=excluded.validation_before_known, validation_after_passed=excluded.validation_after_passed,
			budget_fingerprint=excluded.budget_fingerprint, architecture_budget_due=excluded.architecture_budget_due`,
		normalized, m.PulseRunID, m.RecordedAt, m.StepsWithDescriptions, m.TotalDescriptionChars, m.LargestDescriptionChars, m.LargestDescriptionStepID,
		m.StepsOverBudget, m.DatedTextCount, m.DuplicatedChars, m.StepsWithoutLayout, m.ConsolidationDueSteps, m.LearningSettledSteps,
		m.ArchitectureRuns, m.ConsolidationsApplied, m.ConsolidationsKept, m.ConsolidationsRestored,
		m.ValidationBeforePassed, m.ValidationBeforeKnown, m.ValidationAfterPassed, budget.Fingerprint, archBudgetDue)
	return err
}

func getPulsePromptBudgetMetrics(ctx context.Context, workspacePath string, limit int) ([]PulsePromptBudgetMetric, error) {
	normalized, db, err := openPulseModuleStateDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return []PulsePromptBudgetMetric{}, err
	}
	defer db.Close()
	if err := ensurePulseModuleStateSchema(ctx, db); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT pulse_run_id, recorded_at, steps_with_descriptions, total_description_chars,
			largest_description_chars, largest_description_step_id, steps_over_budget, dated_text_count, duplicated_chars,
			steps_without_layout, consolidation_due_steps, learning_settled_steps, architecture_runs, consolidations_applied,
			consolidations_kept, consolidations_restored, validation_before_passed, validation_before_known, validation_after_passed
		FROM pulse_prompt_budget_metrics WHERE workspace_path=? ORDER BY recorded_at DESC, rowid DESC LIMIT ?`, normalized, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PulsePromptBudgetMetric{}
	for rows.Next() {
		var m PulsePromptBudgetMetric
		if err := rows.Scan(&m.PulseRunID, &m.RecordedAt, &m.StepsWithDescriptions, &m.TotalDescriptionChars,
			&m.LargestDescriptionChars, &m.LargestDescriptionStepID, &m.StepsOverBudget, &m.DatedTextCount, &m.DuplicatedChars,
			&m.StepsWithoutLayout, &m.ConsolidationDueSteps, &m.LearningSettledSteps, &m.ArchitectureRuns, &m.ConsolidationsApplied,
			&m.ConsolidationsKept, &m.ConsolidationsRestored, &m.ValidationBeforePassed, &m.ValidationBeforeKnown, &m.ValidationAfterPassed); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

