package step_based_workflow

import (
	"strings"
	"testing"
)

func TestParseAdvisorSpecializationDecisionContext(t *testing.T) {
	strategy, goal, err := parseAdvisorSpecializationDecisionContext(`Proposal:
Specialize the reviewers.
Strategy Auditor specialization:
Inspect current-channel concentration and funnel leakage.
Goal Advisor specialization:
Explore partnerships and product-led referral loops.`)
	if err != nil {
		t.Fatalf("parse specialization context: %v", err)
	}
	if strategy != "Inspect current-channel concentration and funnel leakage." {
		t.Fatalf("strategy = %q", strategy)
	}
	if goal != "Explore partnerships and product-led referral loops." {
		t.Fatalf("goal = %q", goal)
	}

	if _, _, err := parseAdvisorSpecializationDecisionContext("Strategy Auditor specialization: only one"); err == nil {
		t.Fatal("missing Goal Advisor section should fail")
	}
}

func TestApplyAdvisorSpecializationToManifestIsVersionedAndIdempotent(t *testing.T) {
	base := `{"schema_version":1,"id":"demo","label":"Demo","capabilities":{},"schedules":[]}`
	updated, specialization, already, err := applyAdvisorSpecializationToManifest(
		base,
		"advisor-specialization-20260806T120000Z",
		"Inspect the current funnel.",
		"Explore a new distribution channel.",
		"2026-08-06T12:00:00Z",
	)
	if err != nil || already {
		t.Fatalf("first activation err=%v already=%v", err, already)
	}
	if specialization.Version != 1 || specialization.ApprovedInputID == "" {
		t.Fatalf("unexpected specialization: %#v", specialization)
	}

	unchanged, same, already, err := applyAdvisorSpecializationToManifest(
		updated,
		specialization.ApprovedInputID,
		specialization.StrategyAuditor,
		specialization.GoalAdvisor,
		"2026-08-06T12:01:00Z",
	)
	if err != nil || !already {
		t.Fatalf("idempotent activation err=%v already=%v", err, already)
	}
	if unchanged != updated || same.Version != 1 {
		t.Fatalf("idempotent activation changed manifest or version")
	}

	_, next, already, err := applyAdvisorSpecializationToManifest(
		updated,
		"advisor-specialization-20260806T130000Z",
		"Inspect cohorts inside the current strategy.",
		"Explore an ecosystem partnership.",
		"2026-08-06T13:00:00Z",
	)
	if err != nil || already || next.Version != 2 {
		t.Fatalf("replacement activation err=%v already=%v version=%d", err, already, next.Version)
	}
}

func TestWorkshopStageAgentIdentityIsDistinctAndSafe(t *testing.T) {
	first := newWorkshopStageAgentIdentity("Pulse reviewer - artifact review")
	second := newWorkshopStageAgentIdentity("Pulse reviewer - artifact review")
	if first == second {
		t.Fatalf("stage identities must be unique, got %q", first)
	}
	for _, identity := range []string{first, second} {
		if !strings.Contains(identity, "_pulse-reviewer-artifact-review-") {
			t.Fatalf("unexpected sanitized stage identity %q", identity)
		}
		if strings.ContainsAny(identity, " /") {
			t.Fatalf("stage identity contains unsafe separators: %q", identity)
		}
		if err := ValidatePulseReviewIdentity(identity, "technical_review"); err != nil {
			t.Fatalf("stage identity is not receipt-safe: %v", err)
		}
	}
}
