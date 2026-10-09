package step_based_workflow

import (
	"context"
	"strings"
	"testing"
)

// The Pulse Fixer is the only stage with mutation authority, so it is the only
// stage that repeatedly completes real work and then fails to RECORD it. Every
// assertion here is about whether a rejection carries enough for the agent to
// converge on the next attempt: the members of a closed set, the complete
// required field set plus what actually arrived, or the shape a decode wanted.
func assertContainsAll(t *testing.T, err error, wants []string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a rejection, got nil")
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rejection %q is missing %q", err.Error(), want)
		}
	}
}

func validFixedVerifiedDisposition() PulseFindingDisposition {
	return PulseFindingDisposition{
		Fingerprint:  "fp-1",
		FindingID:    "PUL-1",
		AttemptID:    "fix-abc",
		Disposition:  FindingDispositionFixedVerified,
		Summary:      "Selector pool widened.",
		ChangedFiles: []string{"planning/step_config.json"},
		Verification: []PulseFindingVerification{{
			Check:   "selector diversity test",
			Verdict: VerificationPassed,
		}},
	}
}

func TestFindingDispositionRejectionsNameTheContractTheyEnforce(t *testing.T) {
	// Hardcoded on purpose: these are the contracts the message must publish.
	// Deriving them from the same slice the message uses would let a dropped
	// value pass unnoticed in both places at once.
	allDispositions := []string{
		FindingDispositionFixedVerified, FindingDispositionVerifiedNoChange,
		FindingDispositionChangedUnverified, FindingDispositionAwaitingUser,
		FindingDispositionExternalAction, FindingDispositionFailed,
		FindingDispositionRejected,
	}
	allVerdicts := []string{VerificationPassed, VerificationFailed, VerificationInconclusive}
	allExternalOwners := []string{"platform", "user", "vendor", "workflow_owner"}

	tests := []struct {
		name        string
		disposition PulseFindingDisposition
		want        []string
	}{
		{
			// The live failure: the Fixer wrote "shared workflow runtime" and then
			// "server A dev voi..." across two runs, both meaning platform.
			name: "invalid external_owner names the closed set and disambiguates platform",
			disposition: PulseFindingDisposition{
				Fingerprint: "fp-1", FindingID: "HARNESS-RUN-FULL-WORKFLOW-HUMAN-INPUT-LOSS",
				Disposition: FindingDispositionExternalAction, Summary: "Owned by the harness.",
				ExternalOwner: "shared workflow runtime", ReasonCode: "missing_platform_tool",
				ReopenCondition: "harness exposes a resume tool",
			},
			want: append([]string{
				`invalid external_owner "shared workflow runtime"`,
				"Must be one of:", "shared runtime",
			}, allExternalOwners...),
		},
		{
			name: "external_action_required names the whole required set and what arrived",
			disposition: PulseFindingDisposition{
				Fingerprint: "fp-1", FindingID: "PUL-081D57BD",
				Disposition: FindingDispositionExternalAction, Summary: "Owned elsewhere.",
				ExternalOwner: "platform",
			},
			want: append([]string{
				"external_owner=set", "reason_code=missing", "reopen_condition=missing",
			}, allExternalOwners...),
		},
		{
			name: "fixed_verified names changed_files and the disposition to use instead",
			disposition: PulseFindingDisposition{
				Fingerprint: "fp-1", FindingID: "EH-2026-07-30-01",
				Disposition: FindingDispositionFixedVerified, Summary: "Fixed.",
			},
			want: []string{"changed_files", "changed_files=missing", "verified_no_change"},
		},
		{
			name: "changed_unverified names changed_files and the disposition to use instead",
			disposition: PulseFindingDisposition{
				Fingerprint: "fp-1", FindingID: "PUL-E3D98FEF",
				Disposition: FindingDispositionChangedUnverified, Summary: "Applied, unproven.",
				NextCheck: "the next producing run",
			},
			want: []string{"changed_files", "changed_files=missing", "awaiting_run"},
		},
		{
			name: "invalid disposition names every accepted value",
			disposition: PulseFindingDisposition{
				Fingerprint: "fp-1", FindingID: "PUL-1",
				Disposition: "done", Summary: "Handled.",
			},
			want: append([]string{`invalid disposition "done"`, "Must be one of:"}, allDispositions...),
		},
		{
			name: "missing verification check names the entry and the verdict set",
			disposition: PulseFindingDisposition{
				Fingerprint: "fp-1", FindingID: "PUL-5D41A7E0",
				Disposition: FindingDispositionRejected, Summary: "Not a problem.",
				Verification: []PulseFindingVerification{{Verdict: VerificationPassed}},
			},
			want: append([]string{"verification[0]", "check"}, allVerdicts...),
		},
		{
			name: "invalid verdict names the closed set and the value that arrived",
			disposition: PulseFindingDisposition{
				Fingerprint: "fp-1", FindingID: "PUL-1",
				Disposition: FindingDispositionRejected, Summary: "Not a problem.",
				Verification: []PulseFindingVerification{{Check: "ran the suite", Verdict: "ok"}},
			},
			want: append([]string{`invalid verdict "ok"`, "verification[0]"}, allVerdicts...),
		},
		{
			name: "unpaired refs report both lengths",
			disposition: func() PulseFindingDisposition {
				disposition := validFixedVerifiedDisposition()
				disposition.BeforeRefs = []string{"a", "b"}
				return disposition
			}(),
			want: []string{"before_refs", "after_refs", "before_refs=2", "after_refs=0"},
		},
		{
			name: "fixed_verified verdict shortfall reports the observed counts",
			disposition: func() PulseFindingDisposition {
				disposition := validFixedVerifiedDisposition()
				disposition.Verification[0].Verdict = VerificationInconclusive
				return disposition
			}(),
			want: []string{"passed=0, failed=0, inconclusive=1", "changed_unverified"},
		},
		{
			name: "changed_unverified rejects a failed immediate check",
			disposition: PulseFindingDisposition{
				Fingerprint: "fp-1", FindingID: "PUL-1",
				Disposition: FindingDispositionChangedUnverified, Summary: "Applied.",
				ChangedFiles: []string{"a.json"}, NextCheck: "next scheduled run",
				Verification: []PulseFindingVerification{{Check: "ran", Verdict: VerificationFailed}},
			},
			want: []string{"passed=0, failed=1, inconclusive=0", "failed immediate check", "Use failed"},
		},
		{
			name: "verified_no_change verdict shortfall reports the observed counts",
			disposition: PulseFindingDisposition{
				Fingerprint: "fp-1", FindingID: "PUL-1",
				Disposition: FindingDispositionVerifiedNoChange, Summary: "Not a problem.",
				Verification: []PulseFindingVerification{{Check: "ran", Verdict: VerificationFailed}},
			},
			want: []string{"passed=0, failed=1, inconclusive=0", "passed verification"},
		},
		{
			name: "failed disposition without a failed check reports the counts",
			disposition: PulseFindingDisposition{
				Fingerprint: "fp-1", FindingID: "PUL-1",
				Disposition: FindingDispositionFailed, Summary: "Could not fix.",
				Verification: []PulseFindingVerification{{Check: "ran", Verdict: VerificationPassed}},
			},
			want: []string{"passed=1, failed=0, inconclusive=0", `"failed"`},
		},
		{
			name: "missing internal lifecycle identity names the public issue id",
			disposition: PulseFindingDisposition{
				FindingID: "PUL-1", Disposition: FindingDispositionRejected, Summary: "Not a problem.",
			},
			want: []string{"issue_id", "lifecycle identity was not resolved", "issue_id=set"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertContainsAll(t, validateFindingDisposition(tt.disposition), tt.want)
		})
	}
}

// The message sweep must not have moved any accept/reject boundary.
func TestFindingDispositionAcceptanceIsUnchangedByMessageDetail(t *testing.T) {
	accepted := []PulseFindingDisposition{
		validFixedVerifiedDisposition(),
		{
			Fingerprint: "fp-1", FindingID: "PUL-1", AttemptID: "fix-abc",
			Disposition: FindingDispositionChangedUnverified, Summary: "Applied.",
			ChangedFiles: []string{"a.json"}, NextCheck: "the next digest run",
			Verification: []PulseFindingVerification{{Check: "ran", Verdict: VerificationInconclusive}},
		},
		{
			Fingerprint: "fp-1", FindingID: "PUL-1",
			Disposition: FindingDispositionExternalAction, Summary: "Owned by the harness.",
			ExternalOwner: "platform", ReasonCode: "missing_platform_tool",
			ReopenCondition: "harness exposes a resume tool",
		},
	}
	for index, disposition := range accepted {
		if err := validateFindingDisposition(disposition); err != nil {
			t.Fatalf("accepted[%d] (%s) was rejected: %v", index, disposition.Disposition, err)
		}
	}

	// Every external_owner in the closed set must still be accepted, and nothing
	// outside it.
	for _, owner := range []string{"platform", "user", "vendor", "workflow_owner"} {
		disposition := PulseFindingDisposition{
			Fingerprint: "fp-1", FindingID: "PUL-1",
			Disposition: FindingDispositionExternalAction, Summary: "Owned elsewhere.",
			ExternalOwner: owner, ReasonCode: "policy", ReopenCondition: "policy changes",
		}
		if err := validateFindingDisposition(disposition); err != nil {
			t.Fatalf("external_owner %q was rejected: %v", owner, err)
		}
	}
	for _, owner := range []string{"shared workflow runtime", "Platform", "team", ""} {
		disposition := PulseFindingDisposition{
			Fingerprint: "fp-1", FindingID: "PUL-1",
			Disposition: FindingDispositionExternalAction, Summary: "Owned elsewhere.",
			ExternalOwner: owner, ReasonCode: "policy", ReopenCondition: "policy changes",
		}
		if err := validateFindingDisposition(disposition); err == nil {
			t.Fatalf("external_owner %q was accepted", owner)
		}
	}

	// Every disposition value the message advertises must actually be routable.
	for _, disposition := range pulseFindingDispositionValues {
		err := validateFindingDisposition(PulseFindingDisposition{
			Fingerprint: "fp-1", FindingID: "PUL-1", Disposition: disposition, Summary: "s",
		})
		if err != nil && strings.Contains(err.Error(), "invalid disposition") {
			t.Fatalf("advertised disposition %q is not accepted by the validator", disposition)
		}
	}
}

// TestBackendOpenedFixAttemptRejectionsNameTheContract covers the path that
// replaced the start_pulse_fix_attempt tool. The attempt record is now created
// from the disposition itself, so its rejections are the only place left that
// can teach the caller the contract.
func TestBackendOpenedFixAttemptRejectionsNameTheContract(t *testing.T) {
	ctx := context.Background()
	workspace := concernsWorkspace(t)
	module := "bug_review"
	concern := filedReviewConcern(t, workspace, "pulse-1", module, "stale selector repeats accounts")

	err := validateFindingDisposition(PulseFindingDisposition{
		Fingerprint: concern.Fingerprint, FindingID: "BUG-1",
		Disposition: FindingDispositionFixedVerified, Summary: "Claimed fixed.",
		Verification: []PulseFindingVerification{{Check: "c", Verdict: VerificationPassed}},
	})
	assertContainsAll(t, err, []string{"requires changed_files", "changed_files=missing", "verified_no_change"})

	err = validateFindingDisposition(PulseFindingDisposition{
		Fingerprint: concern.Fingerprint, FindingID: "BUG-1",
		Disposition: FindingDispositionChangedUnverified, Summary: "Applied.",
		NextCheck:    "next run",
		Verification: []PulseFindingVerification{{Check: "c", Verdict: VerificationInconclusive}},
	})
	assertContainsAll(t, err, []string{"requires changed_files", "changed_files=missing", "awaiting_run"})

	db, dbErr := openRunConcernsDB(ctx, workspace, false)
	if dbErr != nil || db == nil {
		t.Fatalf("open lifecycle db: db=%v err=%v", db, dbErr)
	}
	defer db.Close()

	// A fingerprint that was never filed cannot be addressed at all.
	err = RecordPulseFindingDispositionsTx(ctx, db, module, "pulse-1", []PulseFindingDisposition{{
		Fingerprint: "PUL-1", FindingID: "PUL-1",
		Disposition: FindingDispositionFixedVerified, Summary: "Claimed fixed.",
		ChangedFiles: []string{"planning/plan.json"},
		Verification: []PulseFindingVerification{{Check: "c", Verdict: VerificationPassed}},
	}}, "")
	assertContainsAll(t, err, []string{"no concern with fingerprint", "get_pulse_state(view=\"backlog\")", "issue.id"})

	// A rejected concern still cannot re-enter fixing without adjudication.
	if err := ResolveRunConcern(ctx, workspace, concern.Fingerprint, ConcernStatusRejected, "pulse", "not a problem"); err != nil {
		t.Fatalf("reject concern: %v", err)
	}
	err = RecordPulseFindingDispositionsTx(ctx, db, module, "pulse-1", []PulseFindingDisposition{{
		Fingerprint: concern.Fingerprint, FindingID: "BUG-1",
		Disposition: FindingDispositionFixedVerified, Summary: "Claimed fixed.",
		ChangedFiles: []string{"planning/plan.json"},
		Verification: []PulseFindingVerification{{Check: "c", Verdict: VerificationPassed}},
	}}, "")
	assertContainsAll(t, err, []string{"was rejected", "new adjudication", "BUG-1"})
}

func TestPulseImpactRejectionsNameTheContractTheyEnforce(t *testing.T) {
	ctx := context.Background()
	workspace := concernsWorkspace(t)

	// Hardcoded contracts, for the same reason as the disposition sets above.
	impactTypes := []string{"direct_goal", "reliability", "measurement", "presentation_maintenance"}
	verdicts := []string{"improved", "unchanged", "regressed", "inconclusive", "confounded"}
	statuses := []string{"awaiting_evidence", "measuring", "assessed", "retired"}
	sourceTypes := []string{"attempt", "experiment", "finding", "review"}
	directions := []string{"increase", "decrease", "maintain"}
	confidences := []string{"low", "medium", "high"}

	validIntervention := func() PulseIntervention {
		return PulseIntervention{
			Title: "t", CriterionID: "c", ImpactType: "reliability",
			Metric: "m", ExpectedDirection: "increase",
		}
	}

	tests := []struct {
		name   string
		update PulseImpactUpdate
		want   []string
	}{
		{
			name:   "empty update names all three arrays and gives a shape",
			update: PulseImpactUpdate{},
			want:   []string{"interventions", "observations", "assessments", "criterion_id", "observed_at"},
		},
		{
			// The live failure: "each observation requires criterion_id, metric,
			// run_id, and observed_at" named the set but never which one was absent.
			name: "observation names the complete required set and what arrived",
			update: PulseImpactUpdate{Observations: []PulseGoalObservation{{
				CriterionID: "c", Metric: "m", ObservedAt: "2026-08-01T00:00:00Z",
			}}},
			want: []string{
				"observations[0]", "criterion_id", "metric", "run_id", "observed_at",
				"criterion_id=set", "metric=set", "run_id=missing", "observed_at=set",
			},
		},
		{
			name: "observation without value or status says which alternatives exist",
			update: PulseImpactUpdate{Observations: []PulseGoalObservation{{
				CriterionID: "c", Metric: "m", RunID: "r", ObservedAt: "2026-08-01T00:00:00Z",
			}}},
			want: []string{"observations[0]", "value", "status"},
		},
		{
			name: "intervention names the complete required set and what arrived",
			update: PulseImpactUpdate{Interventions: []PulseIntervention{{
				Title: "t", ImpactType: "reliability", ExpectedDirection: "increase",
			}}},
			want: []string{
				"interventions[0]", "title", "criterion_id", "metric",
				"title=set", "criterion_id=missing", "metric=missing",
			},
		},
		{
			name: "invalid impact_type names the closed set",
			update: PulseImpactUpdate{Interventions: []PulseIntervention{func() PulseIntervention {
				intervention := validIntervention()
				intervention.ImpactType = "goal"
				return intervention
			}()}},
			want: append([]string{`invalid impact_type "goal"`, "Must be one of:"}, impactTypes...),
		},
		{
			name: "invalid expected_direction names the closed set",
			update: PulseImpactUpdate{Interventions: []PulseIntervention{func() PulseIntervention {
				intervention := validIntervention()
				intervention.ExpectedDirection = "up"
				return intervention
			}()}},
			want: append([]string{`invalid expected_direction "up"`}, directions...),
		},
		{
			name: "invalid intervention status names the closed set",
			update: PulseImpactUpdate{Interventions: []PulseIntervention{func() PulseIntervention {
				intervention := validIntervention()
				intervention.Status = "open"
				return intervention
			}()}},
			want: append([]string{`invalid status "open"`}, statuses...),
		},
		{
			name: "invalid source_type names the closed set",
			update: PulseImpactUpdate{Interventions: []PulseIntervention{func() PulseIntervention {
				intervention := validIntervention()
				intervention.Sources = []PulseInterventionSource{{SourceType: "commit", SourceID: "abc"}}
				return intervention
			}()}},
			want: append([]string{`invalid source_type "commit"`, "sources[0]"}, sourceTypes...),
		},
		{
			name: "incomplete source names both fields and the closed set",
			update: PulseImpactUpdate{Interventions: []PulseIntervention{func() PulseIntervention {
				intervention := validIntervention()
				intervention.Sources = []PulseInterventionSource{{SourceType: "attempt"}}
				return intervention
			}()}},
			want: append([]string{"sources[0]", "source_type=set", "source_id=missing"}, sourceTypes...),
		},
		{
			name: "assessment names the complete required set and what arrived",
			update: PulseImpactUpdate{Assessments: []PulseImpactAssessment{{
				InterventionID: "int-1", Verdict: "improved", BeforeWindow: "runs 1-3",
			}}},
			want: append([]string{
				"assessments[0]", "intervention_id", "before_window", "after_window",
				"confidence", "assessed_at",
				"intervention_id=set", "before_window=set", "after_window=missing",
				"confidence=missing", "assessed_at=missing",
			}, confidences...),
		},
		{
			name: "invalid assessment verdict names the closed set",
			update: PulseImpactUpdate{Assessments: []PulseImpactAssessment{{
				InterventionID: "int-1", Verdict: "better", BeforeWindow: "a", AfterWindow: "b",
				Confidence: "high", AssessedAt: "2026-08-01T00:00:00Z",
			}}},
			want: append([]string{`invalid verdict "better"`}, verdicts...),
		},
		{
			name: "invalid assessment confidence names the closed set",
			update: PulseImpactUpdate{Assessments: []PulseImpactAssessment{{
				InterventionID: "int-1", Verdict: "improved", BeforeWindow: "a", AfterWindow: "b",
				Confidence: "certain", AssessedAt: "2026-08-01T00:00:00Z",
			}}},
			want: append([]string{`invalid confidence "certain"`}, confidences...),
		},
		{
			name: "unknown intervention_id says how to make it exist",
			update: PulseImpactUpdate{Assessments: []PulseImpactAssessment{{
				InterventionID: "int-missing", Verdict: "improved", BeforeWindow: "a",
				AfterWindow: "b", Confidence: "high", AssessedAt: "2026-08-01T00:00:00Z",
			}}},
			want: []string{"assessments[0]", `"int-missing"`, "interventions", "impact_ledger"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := RecordPulseImpactUpdate(ctx, workspace, tt.update)
			assertContainsAll(t, err, tt.want)
		})
	}

	// Behavior guard: every advertised value in each closed set is still valid.
	for _, value := range impactTypes {
		if !validPulseImpactType(value) {
			t.Fatalf("advertised impact_type %q is rejected", value)
		}
	}
	for _, value := range verdicts {
		if !validPulseImpactVerdict(value) {
			t.Fatalf("advertised verdict %q is rejected", value)
		}
	}
	for _, value := range statuses {
		if !validPulseInterventionStatus("fix_bundle", value) {
			t.Fatalf("advertised intervention status %q is rejected", value)
		}
	}
	for _, value := range sourceTypes {
		if !validPulseImpactSourceType(value) {
			t.Fatalf("advertised source_type %q is rejected", value)
		}
	}
	for _, value := range []string{"direct-goal", "Reliability", ""} {
		if validPulseImpactType(value) {
			t.Fatalf("impact_type %q outside the advertised set was accepted", value)
		}
	}
}

// A disposition with several independent problems must report all of them in
// one rejection, not one problem per round trip.
//
// On 2026-08-04 finding PUL-70B1057E took four sequential record_pulse_result
// rejections over 24 minutes to satisfy, because validateFindingDisposition and
// validateReviewerVerificationDispositions each returned on the first
// violation and only revealed the next one after the caller fixed it. The
// individual field checks are independent of each other — nothing about a
// missing summary depends on whether changed_files was also supplied — so all
// of them can be evaluated and reported together.
func TestFindingDispositionCombinesAllViolationsInOneRejection(t *testing.T) {
	err := validateFindingDisposition(PulseFindingDisposition{
		Fingerprint: "fp-1",
		FindingID:   "PUL-MULTI",
		Disposition: FindingDispositionChangedUnverified,
		// Summary and changed_files are omitted, and the verification carries an
		// invalid verdict — three independent problems.
		Verification: []PulseFindingVerification{{Check: "some check", Verdict: "maybe"}},
	})
	assertContainsAll(t, err, []string{
		"requires summary",
		"changed_unverified requires changed_files",
		`invalid verdict "maybe"`,
	})
	if got := strings.Count(err.Error(), "\n1)"); got != 1 {
		t.Fatalf("rejection is not numbered as a combined list: %s", err.Error())
	}
}

// A disposition with exactly one problem must not read like a list of one.
func TestFindingDispositionSingleViolationStaysUnnumbered(t *testing.T) {
	disposition := validFixedVerifiedDisposition()
	disposition.Summary = ""
	err := validateFindingDisposition(disposition)
	assertContainsAll(t, err, []string{`finding "PUL-1"`, "requires summary"})
	if strings.Contains(err.Error(), "1)") {
		t.Fatalf("single violation formatted as a numbered list: %s", err.Error())
	}
}
