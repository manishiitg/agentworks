package guidance

import (
	"strings"
	"testing"
)

func TestPulseGateAssessesImpactAndAccumulatesEvidence(t *testing.T) {
	gate, err := renderFromRegistry("pulse-gate", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Runtime intake does not force Technical Review",
		"did the error prevent the step from doing its job?",
		"successful retry or workflow recovery with adequate",
		"Absence of a concern is",
		"not reactivate historical workflow-observation",
		"`last_ran_at`", "`last_checked_at` records a Gate check",
		"repeated skips must not reset the evidence window",
		"Count distinct completed, comparable workflow runs",
		"Chat turns", "tool calls, retries, unrelated routes",
		"do not impose a universal run-count threshold",
		"Do not keep moving an unmet boundary",
		"All three non-drift reviews may be skipped",
		"New critical regressions, security/data-loss risks",
		"Ordinary recovered errors do not override it",
		"deterministic hard requirement for `plan_change_dependencies` belongs to",
		"reviewer must inspect each surface and record",
		"until those structured receipts are complete",
		"plan_drift_review.due=true",
		// Plan Drift runs first, not alone: Technical and Goal Work are still
		// decided on their merits; only Architecture waits (2026-09-25).
		"Plan Drift runs first, not alone",
		"Do not skip them because Plan Drift is",
		"Only Architecture waits for a later cycle",
	} {
		if !strings.Contains(gate, want) {
			t.Errorf("missing Gate contract: %q", want)
		}
	}
	for _, stale := range []string{"A failed verified deterministic intake cannot be cooled down or skipped", "Select **at most two**", "Plan Drift is an exclusive prerequisite pass"} {
		if strings.Contains(gate, stale) {
			t.Errorf("stale mandatory runtime review contract: %q", stale)
		}
	}
}
