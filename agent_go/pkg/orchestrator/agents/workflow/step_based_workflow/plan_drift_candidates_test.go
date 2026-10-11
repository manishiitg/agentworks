package step_based_workflow

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// planDriftCandidateWorkspace writes plan.json (the step-id source of truth)
// and, when stepConfigJSON is non-empty, step_config.json. A real db.sqlite
// is always created so the workflow-wide checks (report/README) have
// something to dry-run against.
func planDriftCandidateWorkspace(t *testing.T, workspacePath, planJSON, stepConfigJSON string) {
	t.Helper()
	docsDir := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsDir)
	planningDir := filepath.Join(docsDir, workspacePath, PlanningFolderName)
	if err := os.MkdirAll(planningDir, 0o755); err != nil {
		t.Fatalf("mkdir planning dir: %v", err)
	}
	if planJSON != "" {
		if err := os.WriteFile(filepath.Join(planningDir, "plan.json"), []byte(planJSON), 0o644); err != nil {
			t.Fatalf("write plan.json: %v", err)
		}
	}
	if stepConfigJSON != "" {
		if err := os.WriteFile(filepath.Join(planningDir, "step_config.json"), []byte(stepConfigJSON), 0o644); err != nil {
			t.Fatalf("write step_config.json: %v", err)
		}
	}
	dbDir := filepath.Join(docsDir, workspacePath, "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("mkdir db dir: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dbDir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open db.sqlite: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE emails(id INTEGER PRIMARY KEY, status TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	_ = db.Close()
}

const twoStepPlan = `{"steps":[{"id":"step-a","type":"regular"},{"id":"step-b","type":"regular"}]}`

const reviewedStepConfig = `{"steps":[
  {"id":"step-a","agent_configs":{"drift_review":{"reviewed_at":"2026-08-01T00:00:00Z","reviewed_by":"pulse:plan_drift_review","contract_version":3,"checks":[{"check_id":"report_query_compatibility","status":"pass","evidence":"all report queries ran cleanly"}]}}},
  {"id":"step-b","agent_configs":{"drift_review":{"reviewed_at":"2026-08-01T00:00:00Z","reviewed_by":"pulse:plan_drift_review","contract_version":3,"checks":[{"check_id":"report_query_compatibility","status":"pass","evidence":"all report queries ran cleanly"}]}}}
]}`

func TestCollectPlanDriftCandidatesNilWhenNoPlan(t *testing.T) {
	docsDir := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsDir)
	got, err := CollectPlanDriftCandidates(context.Background(), "Workflow/drift-candidates")
	if err != nil {
		t.Fatalf("CollectPlanDriftCandidates returned error for a missing plan.json: %v", err)
	}
	if got != nil {
		t.Fatalf("CollectPlanDriftCandidates = %#v, want nil when plan.json does not exist", got)
	}
}

// TestCollectPlanDriftCandidatesSurfacesWorkflowLevelPendingAfterDeletion
// covers PLAT-258's deletion coverage gap: even when every real plan step is
// cleanly reviewed, a flagged WorkflowDriftReviewStepID record (set by
// flagWorkflowDriftReviewOnDeletion when a step is deleted) must still
// surface as a candidate — the whole point is that a deleted step's own
// per-step record can never carry this signal again.
func TestCollectPlanDriftCandidatesSurfacesWorkflowLevelPendingAfterDeletion(t *testing.T) {
	stepConfigWithFlaggedWorkflowLevel := `{"steps":[
  {"id":"step-a","agent_configs":{"drift_review":{"reviewed_at":"2026-08-01T00:00:00Z","reviewed_by":"pulse:plan_drift_review","contract_version":3,"checks":[{"check_id":"report_query_compatibility","status":"pass","evidence":"all report queries ran cleanly"}]}}},
  {"id":"step-b","agent_configs":{"drift_review":{"reviewed_at":"2026-08-01T00:00:00Z","reviewed_by":"pulse:plan_drift_review","contract_version":3,"checks":[{"check_id":"report_query_compatibility","status":"pass","evidence":"all report queries ran cleanly"}]}}},
  {"id":"__workflow_drift_review__","agent_configs":{"drift_review":{"needs_review":true}}}
]}`
	planDriftCandidateWorkspace(t, "Workflow/drift-candidates", twoStepPlan, stepConfigWithFlaggedWorkflowLevel)
	got, err := CollectPlanDriftCandidates(context.Background(), "Workflow/drift-candidates")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("CollectPlanDriftCandidates = %#v, want exactly one workflow-level candidate", got)
	}
	if got[0].StepID != WorkflowDriftReviewStepID {
		t.Fatalf("candidate step_id = %q, want %q", got[0].StepID, WorkflowDriftReviewStepID)
	}
	if got[0].StepType != "workflow" {
		t.Fatalf("candidate step_type = %q, want \"workflow\"", got[0].StepType)
	}
}

// TestCollectPlanDriftCandidatesOmitsWorkflowLevelWhenNeverFlagged confirms
// the deliberately different invariant from a real step: a workflow that has
// never deleted a step (no WorkflowDriftReviewStepID entry exists at all)
// must NOT be treated as pending merely because the record is absent — unlike
// a real step, absence here means "nothing to review," not "due."
func TestCollectPlanDriftCandidatesOmitsWorkflowLevelWhenReviewedClean(t *testing.T) {
	stepConfigWithCleanWorkflowLevel := `{"steps":[
  {"id":"step-a","agent_configs":{"drift_review":{"reviewed_at":"2026-08-01T00:00:00Z","reviewed_by":"pulse:plan_drift_review","contract_version":3,"checks":[{"check_id":"report_query_compatibility","status":"pass","evidence":"all report queries ran cleanly"}]}}},
  {"id":"step-b","agent_configs":{"drift_review":{"reviewed_at":"2026-08-01T00:00:00Z","reviewed_by":"pulse:plan_drift_review","contract_version":3,"checks":[{"check_id":"report_query_compatibility","status":"pass","evidence":"all report queries ran cleanly"}]}}},
  {"id":"__workflow_drift_review__","agent_configs":{"drift_review":{"needs_review":false,"reviewed_at":"2026-08-02T00:00:00Z","checks":[{"check_id":"deleted_step_dependent_artifact_audit","status":"pass","evidence":"no dangling references found"}]}}}
]}`
	planDriftCandidateWorkspace(t, "Workflow/drift-candidates", twoStepPlan, stepConfigWithCleanWorkflowLevel)
	got, err := CollectPlanDriftCandidates(context.Background(), "Workflow/drift-candidates")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("CollectPlanDriftCandidates = %#v, want nil when the workflow-level record exists but is not flagged", got)
	}
}

func TestCollectPlanDriftCandidatesNilWhenAllReviewed(t *testing.T) {
	planDriftCandidateWorkspace(t, "Workflow/drift-candidates", twoStepPlan, reviewedStepConfig)
	got, err := CollectPlanDriftCandidates(context.Background(), "Workflow/drift-candidates")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("CollectPlanDriftCandidates = %#v, want nil when every plan step already has a drift_review record", got)
	}
}

func TestCollectPlanDriftDueItemsExplainsLiveDueStateWithoutRunningChecks(t *testing.T) {
	stepConfig := `{"steps":[
  {"id":"step-a","agent_configs":{"drift_review":{"needs_review":true,"contract_version":3,"checks":[{"check_id":"step_description_accuracy","status":"fail","evidence":"the saved description no longer matches the step","finding_id":"PUL-1234ABCD"}]}}},
  {"id":"step-b","agent_configs":{"drift_review":{"needs_review":false,"contract_version":3,"checks":[{"check_id":"step_description_accuracy","status":"pass","evidence":"the saved description matches the step"}]}}}
]}`
	planDriftCandidateWorkspace(t, "Workflow/drift-due-summary", twoStepPlan, stepConfig)
	items, err := CollectPlanDriftDueItems("Workflow/drift-due-summary")
	if err != nil {
		t.Fatalf("CollectPlanDriftDueItems: %v", err)
	}
	if len(items) != 1 || items[0].StepID != "step-a" {
		t.Fatalf("due items = %#v, want only step-a", items)
	}
	if !strings.Contains(items[0].Reason, "unresolved") {
		t.Fatalf("due reason = %q, want unresolved-drift explanation", items[0].Reason)
	}
}

// TestCollectPlanDriftCandidatesReflagsStaleContractVersion is the second
// independent PLAT-259 review's finding #1: a routing step reviewed BEFORE
// phase B added route_structural_isolation/route_eval_pairing has
// needs_review=false and would otherwise never resurface, even though the
// reviewer turn is now required to run two more checks against it. A
// drift_review with no contract_version at all (recorded before that field
// existed) or one below the current planDriftReviewContractVersion must be
// treated as due, exactly like needs_review=true.
func TestCollectPlanDriftCandidatesReflagsStaleContractVersion(t *testing.T) {
	planJSON := `{"steps":[{"type":"routing","id":"router-a","title":"Router","routing_question":"Which path?","routes":[
    {"route_id":"r1","route_name":"One","condition":"c","next_step_id":"end"},
    {"route_id":"r2","route_name":"Two","condition":"c","next_step_id":"end"}
  ]}]}`
	stepConfig := `{"steps":[{"id":"router-a","agent_configs":{"drift_review":{"needs_review":false,"reviewed_at":"2026-08-01T00:00:00Z","reviewed_by":"x","checks":[{"check_id":"report_query_compatibility","status":"pass","evidence":"reviewed before phase B added route checks"}]}}}]}`
	planDriftCandidateWorkspace(t, "Workflow/drift-candidates-stale-contract", planJSON, stepConfig)

	got, err := CollectPlanDriftCandidates(context.Background(), "Workflow/drift-candidates-stale-contract")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].StepID != "router-a" {
		t.Fatalf("CollectPlanDriftCandidates = %#v, want router-a re-flagged as due despite needs_review=false, because its drift_review predates the current contract version", got)
	}
}

func TestCollectPlanDriftCandidatesReflagsSupportedTypesForContractVersionThree(t *testing.T) {
	planJSON := `{"steps":[
  {"id":"regular-a","type":"regular"},
  {"id":"sequence-b","type":"agent","description":"Complete and verify the outcome.","items":[{"id":"verify","type":"user_message","message":"Re-open the evidence and repair verified gaps."}]}
]}`
	stepConfig := `{"steps":[
  {"id":"regular-a","agent_configs":{"drift_review":{"needs_review":false,"contract_version":2,"checks":[{"check_id":"step_description_accuracy","status":"pass","evidence":"regular step was reviewed under the still-current contract"}]}}},
  {"id":"sequence-b","agent_configs":{"drift_review":{"needs_review":false,"contract_version":2,"checks":[{"check_id":"step_description_accuracy","status":"pass","evidence":"sequence predates its type-specific best-practices check"}]}}}
]}`
	planDriftCandidateWorkspace(t, "Workflow/drift-candidates-agent-v3", planJSON, stepConfig)

	got, err := CollectPlanDriftCandidates(context.Background(), "Workflow/drift-candidates-agent-v3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("CollectPlanDriftCandidates = %#v, want regular-a and sequence-b due for their type-specific v3 contracts", got)
	}
	byID := map[string]string{}
	for _, candidate := range got {
		byID[candidate.StepID] = candidate.StepType
	}
	if byID["regular-a"] != "regular" || byID["sequence-b"] != "agent" {
		t.Fatalf("candidate types = %#v, want both supported step types", byID)
	}
}

func TestRequiredPlanDriftContractVersionIsTypeSpecific(t *testing.T) {
	for _, stepType := range []string{"regular", "agent", "routing", "branch"} {
		if got := requiredPlanDriftReviewContractVersion(stepType); got != 3 {
			t.Fatalf("required version for %s = %d, want 3", stepType, got)
		}
	}
	for _, stepType := range []string{"human_input", "workflow", ""} {
		if got := requiredPlanDriftReviewContractVersion(stepType); got != 2 {
			t.Fatalf("required version for %s = %d, want 2", stepType, got)
		}
	}
}

func TestContextDependencyFilenameCheckRejectsStepIDs(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: "producer", ContextOutput: FlexibleContextOutput("result.json")}},
		&RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: "consumer", ContextDependencies: []string{"producer"}}},
	}}
	check := checkContextDependencyFilenames(plan, "consumer")
	if check.Status != stepDriftCheckStatusFail || !strings.Contains(check.Evidence, "result") && !strings.Contains(check.Evidence, "producer") {
		t.Fatalf("step-ID dependency was not rejected: %+v", check)
	}
	plan.Steps[1].(*RegularPlanStep).ContextDependencies = []string{"result.json"}
	if check := checkContextDependencyFilenames(plan, "consumer"); check.Status != stepDriftCheckStatusPass {
		t.Fatalf("artifact filename dependency was rejected: %+v", check)
	}
}

func TestCollectPlanDriftCandidatesErrorsOnMalformedPlan(t *testing.T) {
	planDriftCandidateWorkspace(t, "Workflow/drift-candidates", "{not valid json", "")
	got, err := CollectPlanDriftCandidates(context.Background(), "Workflow/drift-candidates")
	if err == nil {
		t.Fatal("expected an error for malformed plan.json, got nil (a scan failure must never read as \"nothing is due\")")
	}
	if got != nil {
		t.Fatalf("CollectPlanDriftCandidates = %#v, want nil candidates alongside the error", got)
	}
}

func TestCollectPlanDriftCandidatesErrorsOnMalformedStepConfig(t *testing.T) {
	planDriftCandidateWorkspace(t, "Workflow/drift-candidates", twoStepPlan, "{not valid json")
	got, err := CollectPlanDriftCandidates(context.Background(), "Workflow/drift-candidates")
	if err == nil {
		t.Fatal("expected an error for malformed step_config.json, got nil (a scan failure must never read as \"nothing is due\")")
	}
	if got != nil {
		t.Fatalf("CollectPlanDriftCandidates = %#v, want nil candidates alongside the error", got)
	}
}

// The invariant is "has this step been reviewed since it exists in this
// configured form" — a plan step with NO step_config.json row at all has
// never been reviewed, exactly like one with a row but a null drift_review.
// A missing config row must not be invisible to the scan.
func TestCollectPlanDriftCandidatesIncludesStepsWithNoConfigRowAtAll(t *testing.T) {
	stepConfig := `{"steps":[{"id":"step-a","agent_configs":{"drift_review":{"reviewed_at":"2026-08-01T00:00:00Z","reviewed_by":"x","contract_version":3,"checks":[{"check_id":"report_query_compatibility","status":"pass","evidence":"already reviewed"}]}}}]}`
	planDriftCandidateWorkspace(t, "Workflow/drift-candidates", twoStepPlan, stepConfig)
	got, err := CollectPlanDriftCandidates(context.Background(), "Workflow/drift-candidates")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("CollectPlanDriftCandidates returned %d candidates, want exactly 1 (step-b has no step_config.json row at all)", len(got))
	}
	if got[0].StepID != "step-b" {
		t.Fatalf("candidate StepID = %q, want step-b", got[0].StepID)
	}
}

// When step_config.json does not exist at all, every plan step is pending —
// this must not be confused with the "no plan.json" nil case.
func TestCollectPlanDriftCandidatesAllStepsPendingWhenNoStepConfigFileAtAll(t *testing.T) {
	planDriftCandidateWorkspace(t, "Workflow/drift-candidates", twoStepPlan, "")
	got, err := CollectPlanDriftCandidates(context.Background(), "Workflow/drift-candidates")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("CollectPlanDriftCandidates returned %d candidates, want 2 (no step_config.json means every plan step is pending)", len(got))
	}
}

func TestCollectPlanDriftCandidatesListsUnreviewedStepsOnly(t *testing.T) {
	stepConfig := `{"steps":[
  {"id":"step-a","agent_configs":{"drift_review":{"reviewed_at":"2026-08-01T00:00:00Z","reviewed_by":"x","contract_version":3,"checks":[{"check_id":"report_query_compatibility","status":"pass","evidence":"already reviewed, must not reappear"}]}}},
  {"id":"step-b"}
]}`
	planDriftCandidateWorkspace(t, "Workflow/drift-candidates", twoStepPlan, stepConfig)
	got, err := CollectPlanDriftCandidates(context.Background(), "Workflow/drift-candidates")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("CollectPlanDriftCandidates returned %d candidates, want exactly 1 (step-a is already reviewed)", len(got))
	}
	if got[0].StepID != "step-b" {
		t.Fatalf("candidate StepID = %q, want step-b", got[0].StepID)
	}
}

// The stale flag model: a step WITH a drift_review record but
// needs_review==true is exactly as much a candidate as one with no record at
// all — its prior evidence is preserved (not part of this scan's decision),
// only the flag drives due-ness.
func TestCollectPlanDriftCandidatesIncludesFlaggedSteps(t *testing.T) {
	stepConfig := `{"steps":[
  {"id":"step-a","agent_configs":{"drift_review":{"needs_review":false,"reviewed_at":"2026-08-01T00:00:00Z","reviewed_by":"x","contract_version":3,"checks":[{"check_id":"report_query_compatibility","status":"pass","evidence":"clean, must not reappear"}]}}},
  {"id":"step-b","agent_configs":{"drift_review":{"needs_review":true,"reviewed_at":"2026-08-01T00:00:00Z","reviewed_by":"x","checks":[{"check_id":"report_query_compatibility","status":"pass","evidence":"stale after a later edit"}]}}}
]}`
	planDriftCandidateWorkspace(t, "Workflow/drift-candidates", twoStepPlan, stepConfig)
	got, err := CollectPlanDriftCandidates(context.Background(), "Workflow/drift-candidates")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("CollectPlanDriftCandidates returned %d candidates, want exactly 1 (step-b is flagged needs_review=true)", len(got))
	}
	if got[0].StepID != "step-b" {
		t.Fatalf("candidate StepID = %q, want step-b", got[0].StepID)
	}
}

// Every candidate carries the workflow-wide checks (report query compatibility,
// db/README contract) plus its own scripted-code check, even when the step has
// no learnings/<id>/main.py — CheckScriptedCodeDBQueries passes trivially in
// that case rather than being omitted, so a reviewer always sees a result for
// every check id it's responsible for filling in the gaps around.
func TestCollectPlanDriftCandidatesIncludesWorkflowWideAndScriptedChecks(t *testing.T) {
	singleStepPlan := `{"steps":[{"id":"step-b","type":"regular"}]}`
	planDriftCandidateWorkspace(t, "Workflow/drift-candidates", singleStepPlan, "")
	got, err := CollectPlanDriftCandidates(context.Background(), "Workflow/drift-candidates")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}
	checkIDs := map[string]bool{}
	for _, c := range got[0].Checks {
		checkIDs[c.CheckID] = true
	}
	for _, want := range []string{reportDriftCheckID, dbReadmeDriftCheckID, scriptedCodeDriftCheckID} {
		if !checkIDs[want] {
			t.Fatalf("candidate checks %v missing expected check_id %q", checkIDs, want)
		}
	}
	if checkIDs[validationSchemaDBDriftCheckID] {
		t.Fatalf("candidate checks unexpectedly include %q; step has no validation_schema.db[] override", validationSchemaDBDriftCheckID)
	}
}

// The DB-rules check only runs when step_config.json itself carries a
// validation_schema.db[] override — a plan.json-only declared schema is out
// of scope for this precompute pass (documented as a known coverage gap).
func TestCollectPlanDriftCandidatesIncludesDBRuleCheckWhenOverridePresent(t *testing.T) {
	singleStepPlan := `{"steps":[{"id":"step-b","type":"regular"}]}`
	stepConfig := `{"steps":[{"id":"step-b","validation_schema":{"db":[{"name":"emails exist","sql":"SELECT id FROM emails","min_rows":0}]}}]}`
	planDriftCandidateWorkspace(t, "Workflow/drift-candidates", singleStepPlan, stepConfig)
	got, err := CollectPlanDriftCandidates(context.Background(), "Workflow/drift-candidates")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}
	found := false
	for _, c := range got[0].Checks {
		if c.CheckID == validationSchemaDBDriftCheckID {
			found = true
		}
	}
	if !found {
		t.Fatalf("candidate checks missing %q despite a validation_schema.db[] override on the step", validationSchemaDBDriftCheckID)
	}
}

func TestCollectPlanDriftCandidatesTrimsWorkspacePath(t *testing.T) {
	docsDir := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsDir)
	got, err := CollectPlanDriftCandidates(context.Background(), "  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("CollectPlanDriftCandidates = %#v, want nil for a blank workspace path", got)
	}
}

// planStepIDsFromPlanJSON is the piece that makes the plan the source of
// truth; test it directly for the nested-routing case, since a plan step's
// sub-agent steps are real steps that also need drift review.
func TestPlanStepIDsFromPlanJSONRecursesIntoRoutingSubAgentSteps(t *testing.T) {
	planJSON := `{"steps":[
  {"id":"step-a","type":"regular"},
  {"id":"step-router","type":"routing","predefined_routes":[
    {"route":"x","sub_agent_step":{"id":"step-nested","type":"regular"}}
  ]}
]}`
	ids, err := planStepIDsFromPlanJSON(planJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"step-a", "step-router", "step-nested"} {
		if !ids[want] {
			t.Fatalf("planStepIDsFromPlanJSON = %v, missing %q", ids, want)
		}
	}
}

func TestPlanStepIDsFromPlanJSONExcludesOrphanSteps(t *testing.T) {
	planJSON := `{"steps":[{"id":"step-a","type":"regular"}],"orphan_steps":[{"id":"step-orphan","type":"regular"}]}`
	ids, err := planStepIDsFromPlanJSON(planJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ids["step-orphan"] {
		t.Fatalf("planStepIDsFromPlanJSON included an orphan step: %v", ids)
	}
	if !ids["step-a"] {
		t.Fatalf("planStepIDsFromPlanJSON missing the live step: %v", ids)
	}
}

func TestPlanStepIDsFromPlanJSONRejectsMalformedSteps(t *testing.T) {
	_, err := planStepIDsFromPlanJSON(`{"steps": "not an array"}`)
	if err == nil {
		t.Fatal("expected an error for a malformed steps field")
	}
	if !strings.Contains(err.Error(), "plan.json") {
		t.Fatalf("error %q does not mention plan.json", err.Error())
	}
}

// StepType is precomputed per candidate (PLAT-259) so the reviewer turn can
// tell routing steps apart from branch/regular without an extra lookup.
func TestCollectPlanDriftCandidatesPopulatesStepTypeFromPlanJSON(t *testing.T) {
	const workspacePath = "Workflow/drift-candidates-types"
	planJSON := `{"steps":[
  {"type":"routing","id":"router-a","title":"Router","routing_question":"Which path?","routes":[
    {"route_id":"r1","route_name":"One","condition":"c","next_step_id":"end"},
    {"route_id":"r2","route_name":"Two","condition":"c","next_step_id":"end"}
  ]},
  {"type":"regular","id":"regular-b","title":"Regular","description":"d","validation_schema":{}}
]}`
	stepConfig := `{"steps":[{"id":"router-a"},{"id":"regular-b"}]}`
	planDriftCandidateWorkspace(t, workspacePath, planJSON, stepConfig)

	got, err := CollectPlanDriftCandidates(context.Background(), workspacePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byID := map[string]PlanDriftCandidate{}
	for _, c := range got {
		byID[c.StepID] = c
	}
	if len(byID) != 2 {
		t.Fatalf("expected 2 candidates, got %d: %#v", len(byID), got)
	}
	if byID["router-a"].StepType != "routing" {
		t.Fatalf("router-a StepType = %q, want %q", byID["router-a"].StepType, "routing")
	}
	if byID["regular-b"].StepType != "regular" {
		t.Fatalf("regular-b StepType = %q, want %q", byID["regular-b"].StepType, "regular")
	}
}

// TestCollectPlanDriftCandidatesPopulatesStepTypeForNestedRoutingStep covers
// the second independent PLAT-259 review's finding: candidate discovery
// (planStepIDsFromPlanJSON) already recurses into a todo_task's
// predefined_routes sub_agent_step, so a nested routing step is a real
// candidate, but the step-type lookup previously only walked top-level
// plan.Steps -- the nested routing step reached the reviewer with an empty
// StepType, silently skipping route_structural_isolation/route_eval_pairing
// for it.
func TestCollectPlanDriftCandidatesPopulatesStepTypeForNestedRoutingStep(t *testing.T) {
	const workspacePath = "Workflow/drift-candidates-nested-routing"
	planJSON := `{"steps":[
  {"type":"todo_task","id":"todo-a","title":"Todo","description":"d","predefined_routes":[
    {"route_id":"nested-router","route_name":"Nested Router","condition":"c","sub_agent_step":{
      "type":"routing","id":"nested-router","title":"Nested Router","routing_question":"Which path?","routes":[
        {"route_id":"r1","route_name":"One","condition":"c","next_step_id":"end"},
        {"route_id":"r2","route_name":"Two","condition":"c","next_step_id":"end"}
      ]
    }}
  ]}
]}`
	stepConfig := `{"steps":[{"id":"todo-a"},{"id":"nested-router"}]}`
	planDriftCandidateWorkspace(t, workspacePath, planJSON, stepConfig)

	got, err := CollectPlanDriftCandidates(context.Background(), workspacePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byID := map[string]PlanDriftCandidate{}
	for _, c := range got {
		byID[c.StepID] = c
	}
	nested, ok := byID["nested-router"]
	if !ok {
		t.Fatalf("expected a candidate for the nested routing step, got %#v", got)
	}
	if nested.StepType != "routing" {
		t.Fatalf("nested-router StepType = %q, want %q", nested.StepType, "routing")
	}
}

// A missing plan.json must not fail the whole scan: candidacy derives from
// the plan, so there is simply nothing to review -- nil candidates, nil
// error (the tolerance half of the old step_config-derived behavior).
func TestCollectPlanDriftCandidatesToleratesMissingPlanJSON(t *testing.T) {
	stepConfig := `{"steps":[{"id":"step-b"}]}`
	planDriftCandidateWorkspace(t, "Workflow/drift-candidates-no-plan", "", stepConfig)

	got, err := CollectPlanDriftCandidates(context.Background(), "Workflow/drift-candidates-no-plan")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no candidates without plan.json, got %d", len(got))
	}
}
