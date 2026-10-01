package step_based_workflow

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PLAT-259: branch is a new step type, same shape/executor as routing (now
// the "route"/major-fork concept). These tests mirror the existing routing
// coverage, substituting BranchPlanStep/branch, to prove the shared
// routeSwitchStep interface refactor in controller_routing.go/
// controller_routing_deterministic.go genuinely works for branch and not
// just for routing.

func TestValidateBranchStepRejectsDescription(t *testing.T) {
	step := &BranchPlanStep{
		CommonStepFields: CommonStepFields{
			ID:          "branch-by-mode",
			Title:       "Branch by Mode",
			Description: "Decide the route first.",
		},
		BranchQuestion: "Which path should run?",
		Routes:         deterministicRoutingTestRoutes(),
	}

	err := validateBranchStepFieldsTyped(step)
	if err == nil {
		t.Fatal("expected branch description to be rejected")
	}
	if !strings.Contains(err.Error(), "must not set description") {
		t.Fatalf("expected deterministic-only description error, got %v", err)
	}
}

func TestValidateBranchStepRequiresTwoRoutes(t *testing.T) {
	step := &BranchPlanStep{
		CommonStepFields: CommonStepFields{ID: "branch-one-route", Title: "One Route"},
		BranchQuestion:   "Which path?",
		Routes:           []RoutingRoute{{RouteID: "only", RouteName: "Only", NextStepID: "step-only"}},
	}

	err := validateBranchStepFieldsTyped(step)
	if err == nil {
		t.Fatal("expected branch step with < 2 routes to be rejected")
	}
	if !strings.Contains(err.Error(), "at least 2 routes") {
		t.Fatalf("expected minimum-routes error, got %v", err)
	}
}

func TestValidateBranchStepAcceptsWellFormedStep(t *testing.T) {
	step := &BranchPlanStep{
		CommonStepFields: CommonStepFields{ID: "branch-ok", Title: "OK"},
		BranchQuestion:   "Which path?",
		Routes:           deterministicRoutingTestRoutes(),
		DefaultRouteID:   "route-search",
	}

	if err := validateBranchStepFieldsTyped(step); err != nil {
		t.Fatalf("expected well-formed branch step to pass, got %v", err)
	}
}

func TestBranchStepOwnRouteFileCandidatesIgnoresLegacyDescription(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewDefault(),
		nil,
		orchestrator.OrchestratorTypeWorkflow,
		"Workflow/demo",
		0,
		"",
		nil,
		nil,
		false,
		&orchestrator.LLMConfig{},
		1,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator returned error: %v", err)
	}
	hcpo := &StepBasedWorkflowOrchestrator{
		BaseOrchestrator:  base,
		selectedRunFolder: "run-1",
	}
	step := &BranchPlanStep{
		CommonStepFields: CommonStepFields{
			ID:          "branch-by-mode",
			Description: "legacy agent body",
		},
	}

	candidates := hcpo.routingStepOwnRouteFileCandidates(step, 0, "step-1")
	if len(candidates) != 1 {
		t.Fatalf("expected one own route file candidate, got %d: %v", len(candidates), candidates)
	}
	if strings.Contains(candidates[0], "step-1-branch") {
		t.Fatalf("legacy execute path should not be considered, got %q", candidates[0])
	}
}

func TestIsRoutingStepRecognizesBranchStep(t *testing.T) {
	if !isRoutingStep(&BranchPlanStep{}) {
		t.Fatal("isRoutingStep should recognize *BranchPlanStep")
	}
	if !isRoutingStep(&RoutingPlanStep{}) {
		t.Fatal("isRoutingStep should still recognize *RoutingPlanStep")
	}
	if isRoutingStep(&RegularPlanStep{}) {
		t.Fatal("isRoutingStep should not recognize *RegularPlanStep")
	}
}

func TestBranchPlanStepMarshalJSONAlwaysSetsType(t *testing.T) {
	step := &BranchPlanStep{
		CommonStepFields: CommonStepFields{ID: "branch-a", Title: "Branch A"},
		BranchQuestion:   "Which path?",
		Routes:           deterministicRoutingTestRoutes(),
	}

	raw, err := json.Marshal(step)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal into map failed: %v", err)
	}
	if decoded["type"] != "branch" {
		t.Fatalf("decoded type = %v, want %q", decoded["type"], "branch")
	}
	if decoded["branch_question"] != "Which path?" {
		t.Fatalf("decoded branch_question = %v, want %q", decoded["branch_question"], "Which path?")
	}
}

// TestExecuteRoutingStepRunsRealBranchExecution is the second independent
// PLAT-259 review's finding #4: TestBranchStepEndToEndLifecycle exercises
// validation/config/navigation in isolation, but never calls the real
// executeRoutingStep/controller execution path -- so nothing had actually
// proven a *BranchPlanStep survives the real executor, only the pieces
// around it. This drives a *BranchPlanStep through the real
// executeRoutingStep, using the same httptest.NewServer + WorkspaceClient
// mocking pattern as base_orchestrator_workspace_test.go
// (TestReadWorkspaceFileAcceptsExistingEmptyFile): every GET (route-file
// preseed reads) answers "not found" so resolution falls through to
// default_route_id -- the same deterministic path a plain *RoutingPlanStep
// already exercises live -- and every other method answers success (folder
// creation, routing-evaluation.json write), matching the executor's
// fail-open, warn-only handling of those secondary writes.
func TestExecuteRoutingStepRunsRealBranchExecution(t *testing.T) {
	var writtenRoutingEvaluationJSON string
	var writtenRouteSelectionJSON string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet:
			// Every read (route-file preseed candidates) answers "not found",
			// so resolveDeterministicRoutingSelection falls through to
			// default_route_id -- the same path a real *RoutingPlanStep
			// already exercises live when no route_selection.json exists yet.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":false,"error":"not found"}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/folders"):
			w.WriteHeader(http.StatusCreated) // createFolderViaAPI requires 201 or 409
			_, _ = w.Write([]byte(`{"success":true}`))
		case (r.Method == http.MethodPut || r.Method == http.MethodPost) && strings.Contains(r.URL.Path, "routing-evaluation.json"):
			var body struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			writtenRoutingEvaluationJSON = body.Content
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true}`))
		case (r.Method == http.MethodPut || r.Method == http.MethodPost) && strings.Contains(r.URL.Path, "route_selection.json"):
			var body struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			writtenRouteSelectionJSON = body.Content
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true}`))
		}
	}))
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewDefault(),
		nil,
		orchestrator.OrchestratorTypeWorkflow,
		"Workflow/branch-exec-demo",
		0,
		"",
		nil,
		nil,
		false,
		&orchestrator.LLMConfig{},
		1,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator returned error: %v", err)
	}
	base.WorkspaceClient = workspace.NewClient(server.URL)
	base.SetWorkspacePath("Workflow/branch-exec-demo")
	hcpo := &StepBasedWorkflowOrchestrator{
		BaseOrchestrator:  base,
		selectedRunFolder: "run-1",
	}

	branchStep := &BranchPlanStep{
		CommonStepFields: CommonStepFields{ID: "branch-step", Title: "Branch Step"},
		BranchQuestion:   "Which path?",
		Routes:           deterministicRoutingTestRoutes(),
		DefaultRouteID:   "route-search",
	}
	allSteps := []PlanStepInterface{branchStep}

	selectedRouteID, executionResult, err := hcpo.executeRoutingStep(
		context.Background(),
		branchStep,
		0,
		&StepProgress{},
		nil,
		0,
		nil,
		allSteps,
		nil,
	)
	if err != nil {
		t.Fatalf("executeRoutingStep returned error for a well-formed branch step: %v", err)
	}
	if executionResult != "" {
		t.Fatalf("executeRoutingStep executionResult = %q, want empty (deterministic switch never runs an agent)", executionResult)
	}
	if selectedRouteID != "route-search" {
		t.Fatalf("executeRoutingStep selectedRouteID = %q, want default_route_id %q (no route_selection.json present)", selectedRouteID, "route-search")
	}
	if branchStep.SelectedRouteID != "route-search" {
		t.Fatalf("executeRoutingStep did not persist SetSelectedRouteID onto the branch step struct: got %q", branchStep.SelectedRouteID)
	}
	if branchStep.RoutingResponse == nil || branchStep.RoutingResponse.SelectedRouteID != "route-search" {
		t.Fatalf("executeRoutingStep did not persist SetRoutingResponse onto the branch step struct: got %+v", branchStep.RoutingResponse)
	}

	// Close the loop: feed the real executor's output into the navigation
	// helper, exactly as the main execution loop does, and confirm it
	// resolves to the selected route's real next_step_id.
	nextStepID := nextStepIDForSelectedRoute(branchStep, selectedRouteID)
	var wantNextStepID string
	for _, route := range branchStep.Routes {
		if route.RouteID == "route-search" {
			wantNextStepID = route.NextStepID
		}
	}
	if wantNextStepID == "" {
		t.Fatalf("test fixture bug: route-search has no next_step_id in %+v", branchStep.Routes)
	}
	if nextStepID != wantNextStepID {
		t.Fatalf("nextStepIDForSelectedRoute after real execution = %q, want %q", nextStepID, wantNextStepID)
	}

	// The persisted artifact must record the step's real type at execution
	// time -- Execution Logs later prefers this over the current plan.json
	// lookup precisely so a step reclassified after this run doesn't
	// silently relabel this historical run's evidence (P2 finding on the
	// PLAT-259 Execution Logs fix).
	if writtenRoutingEvaluationJSON == "" {
		t.Fatal("expected executeRoutingStep to write routing-evaluation.json, got no write captured")
	}
	var persisted map[string]interface{}
	if err := json.Unmarshal([]byte(writtenRoutingEvaluationJSON), &persisted); err != nil {
		t.Fatalf("routing-evaluation.json is not valid JSON: %v\n%s", err, writtenRoutingEvaluationJSON)
	}
	if got, _ := persisted["step_type"].(string); got != "branch" {
		t.Fatalf("routing-evaluation.json step_type = %q, want %q", got, "branch")
	}

	if writtenRouteSelectionJSON == "" {
		t.Fatal("expected branch to persist its resolved route_selection.json in the run-scoped execution folder")
	}
	var persistedSelection map[string]interface{}
	if err := json.Unmarshal([]byte(writtenRouteSelectionJSON), &persistedSelection); err != nil {
		t.Fatalf("route_selection.json is not valid JSON: %v\n%s", err, writtenRouteSelectionJSON)
	}
	if got, _ := persistedSelection["select_route"].(string); got != "route-search" {
		t.Fatalf("route_selection.json select_route = %q, want %q", got, "route-search")
	}
	if got, _ := persistedSelection["source_kind"].(string); got != "default_route_id" {
		t.Fatalf("route_selection.json source_kind = %q, want %q", got, "default_route_id")
	}
}

// TestBranchStepEndToEndLifecycle is the corrective regression test an
// independent review of PLAT-259 phase A required: adding a branch step
// must survive persistence, reload, config application, and navigation to
// its selected route's target -- not just parse in isolation. The review
// found several *RoutingPlanStep-only switches that silently rejected or
// no-op'd for *BranchPlanStep (canonical plan validation, populateRuntimeFields,
// getAgentConfigs, post-execution navigation, validateNextStepIDReferences);
// this test exercises each of those in sequence against one real branch step.
func TestBranchStepEndToEndLifecycle(t *testing.T) {
	// 1. A branch step plus the two steps its routes point to, and a
	// convergence step both routes eventually reach.
	planJSON := `{"steps":[
		{"type":"branch","id":"branch-step","title":"Branch Step","branch_question":"Which path?","routes":[
			{"route_id":"route-a","route_name":"A","condition":"c","next_step_id":"step-a"},
			{"route_id":"route-b","route_name":"B","condition":"c","next_step_id":"step-b"}
		]},
		{"type":"regular","id":"step-a","title":"Step A","description":"d","validation_schema":{},"next_step_id":"converge"},
		{"type":"regular","id":"step-b","title":"Step B","description":"d","validation_schema":{},"next_step_id":"converge"},
		{"type":"regular","id":"converge","title":"Converge","description":"d","validation_schema":{}}
	]}`

	// 2. Persist + reload: unmarshal exactly as loadPlanFromFile/checkExistingPlan
	// would (custom UnmarshalJSON -> parseStepFromJSON per step).
	var plan PlanningResponse
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		t.Fatalf("failed to unmarshal plan.json: %v", err)
	}
	if len(plan.Steps) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(plan.Steps))
	}
	branchStep, ok := plan.Steps[0].(*BranchPlanStep)
	if !ok {
		t.Fatalf("plan.Steps[0] = %T, want *BranchPlanStep", plan.Steps[0])
	}

	// 3. Canonical plan validation -- this is the review's most severe
	// finding: validateLoadedPlanStepWithOptions previously had no
	// *BranchPlanStep case and returned "unsupported step type" for every
	// branch step, meaning add_branch_step could never actually persist.
	if err := validateLoadedPlanStructure(&plan); err != nil {
		t.Fatalf("validateLoadedPlanStructure rejected a well-formed branch step: %v", err)
	}

	// 4. Round-trip: marshal back out and re-parse, proving persistence is
	// stable, not just the first parse.
	reMarshaled, err := json.Marshal(&plan)
	if err != nil {
		t.Fatalf("failed to re-marshal plan: %v", err)
	}
	var reloaded PlanningResponse
	if err := json.Unmarshal(reMarshaled, &reloaded); err != nil {
		t.Fatalf("failed to re-unmarshal persisted plan: %v", err)
	}
	if err := validateLoadedPlanStructure(&reloaded); err != nil {
		t.Fatalf("reloaded plan failed validation: %v", err)
	}
	reloadedBranch, ok := reloaded.Steps[0].(*BranchPlanStep)
	if !ok || reloadedBranch.BranchQuestion != "Which path?" {
		t.Fatalf("reloaded branch step = %+v, want branch_question preserved", reloaded.Steps[0])
	}

	// 5. Apply step_config.json to the step -- populateRuntimeFields
	// previously had no *BranchPlanStep case and returned "unknown step
	// type", and getAgentConfigs had no case either (silently returned nil).
	stepConfigs := []StepConfig{
		{ID: "branch-step", AgentConfigs: &AgentConfigs{ExecutionTier: "high"}},
	}
	if err := populateRuntimeFields(branchStep, stepConfigs); err != nil {
		t.Fatalf("populateRuntimeFields rejected a branch step: %v", err)
	}
	if got := getAgentConfigs(branchStep); got == nil || got.ExecutionTier != "high" {
		t.Fatalf("getAgentConfigs(branchStep) = %+v, want ExecutionTier=high applied", got)
	}

	// 6. Execute a selected route and verify navigation reaches its target --
	// this is the review's other severe finding: the post-execution
	// navigation lookup only ever type-asserted *RoutingPlanStep, silently
	// leaving nextStepID empty for a branch step (the workflow would not
	// know where to go next after a branch "completed").
	if got := nextStepIDForSelectedRoute(branchStep, "route-a"); got != "step-a" {
		t.Fatalf("nextStepIDForSelectedRoute(route-a) = %q, want step-a", got)
	}
	if got := nextStepIDForSelectedRoute(branchStep, "route-b"); got != "step-b" {
		t.Fatalf("nextStepIDForSelectedRoute(route-b) = %q, want step-b", got)
	}
	if got := nextStepIDForSelectedRoute(branchStep, "no-such-route"); got != "" {
		t.Fatalf("nextStepIDForSelectedRoute(no-such-route) = %q, want empty", got)
	}
}

// TestBranchStepDanglingNextStepIDCaughtByValidation is the counterpart to
// the review's validateNextStepIDReferences finding: a branch route
// pointing at a step that does not exist must be rejected at plan-load
// time, the same as a routing step's dangling route already was.
func TestBranchStepDanglingNextStepIDCaughtByValidation(t *testing.T) {
	planJSON := `{"steps":[
		{"type":"branch","id":"branch-step","title":"Branch Step","branch_question":"Which path?","routes":[
			{"route_id":"route-a","route_name":"A","condition":"c","next_step_id":"does-not-exist"},
			{"route_id":"route-b","route_name":"B","condition":"c","next_step_id":"end"}
		]}
	]}`
	var plan PlanningResponse
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		t.Fatalf("failed to unmarshal plan.json: %v", err)
	}
	err := validateLoadedPlanStructure(&plan)
	if err == nil {
		t.Fatal("expected validateLoadedPlanStructure to reject a branch route pointing at a missing step")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("expected error to name the missing step, got: %v", err)
	}
}

// TestSetStepIdentityAcceptsBranchStep covers a gap the independent review
// called "nested sub-agent identity normalization": setStepIdentity is used
// to stamp a todo_task predefined route's sub_agent_step with the route's ID
// and name, and previously had no *BranchPlanStep case -- a branch step
// nested as a sub_agent_step would hit the "unsupported sub_agent_step type"
// default and error out, unlike an identical *RoutingPlanStep.
func TestSetStepIdentityAcceptsBranchStep(t *testing.T) {
	branchStep := &BranchPlanStep{
		BranchQuestion: "Which path?",
		Routes:         deterministicRoutingTestRoutes(),
	}
	if err := setStepIdentity(branchStep, "route-1", "Route One"); err != nil {
		t.Fatalf("setStepIdentity rejected a branch sub_agent_step: %v", err)
	}
	if branchStep.ID != "route-1" || branchStep.Title != "Route One" {
		t.Fatalf("setStepIdentity did not stamp branch step, got ID=%q Title=%q", branchStep.ID, branchStep.Title)
	}

	routingStep := &RoutingPlanStep{RoutingQuestion: "Which path?", Routes: deterministicRoutingTestRoutes()}
	if err := setStepIdentity(routingStep, "route-2", "Route Two"); err != nil {
		t.Fatalf("setStepIdentity rejected a routing sub_agent_step: %v", err)
	}
}

func TestParseStepFromJSONHandlesBranchType(t *testing.T) {
	raw := `{"type":"branch","id":"branch-a","title":"Branch A","branch_question":"Which path?","routes":[
		{"route_id":"route-search","route_name":"Search","condition":"c","next_step_id":"step-search"},
		{"route_id":"route-save","route_name":"Save","condition":"c","next_step_id":"step-save"}
	]}`

	step, err := parseStepFromJSON(json.RawMessage(raw), 0, "step")
	if err != nil {
		t.Fatalf("parseStepFromJSON returned error: %v", err)
	}
	branchStep, ok := step.(*BranchPlanStep)
	if !ok {
		t.Fatalf("parseStepFromJSON returned %T, want *BranchPlanStep", step)
	}
	if branchStep.StepType() != StepTypeBranch {
		t.Fatalf("StepType() = %q, want %q", branchStep.StepType(), StepTypeBranch)
	}
	if len(branchStep.Routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(branchStep.Routes))
	}
}

// TestCanonicalWorkshopPromptOffersBranchForFixedChoices is the third
// independent PLAT-259 review's finding #1: the canonical planning
// instruction inside interactive_workshop_manager.go (the primary text the
// Builder agent actually reads every workshop turn, distinct from the
// references/*.md deep-dive docs) told the agent to use deterministic
// `routing` for every fixed branch choice and omitted `branch` from its
// step-types list entirely -- so normal plan creation could keep producing
// routing steps for the small decisions PLAT-259 introduced `branch` to
// represent, regardless of how well the reference docs described `branch`.
// Mirrors the existing source-scan pattern in
// TestRunInBackgroundPassesBuilderSkillSnapshotToBothAgentKinds
// (background_skill_inheritance_test.go).
func TestCanonicalWorkshopPromptOffersBranchForFixedChoices(t *testing.T) {
	prompt := executeInteractiveWorkshopPromptForMode(t, "workshop")
	operations, err := guidance.RenderReferenceKindForTest("workflow-chat", "workshop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "references/workflow-chat.md") || !strings.Contains(operations, "references/plan-design.md") || !strings.Contains(operations, "`branch`") {
		t.Fatal("workshop must route step design to the canonical skill and expose branch")
	}
	doc := guidance.RenderSystemDoc("plan-design")
	if !strings.Contains(doc, "small in-flow decision") || !strings.Contains(doc, "major sub-workflow fork") {
		t.Fatal("design skill must distinguish small branches from major workflow routes")
	}
}

// convertRoutingBranchTestRoutes returns two routes that both terminate at
// "end", so a single-step plan fixture stays graph-valid without needing
// real downstream steps.
func convertRoutingBranchTestRoutes() []RoutingRoute {
	return []RoutingRoute{
		{RouteID: "route-a", RouteName: "A", Condition: "c", NextStepID: "end"},
		{RouteID: "route-b", RouteName: "B", Condition: "c", NextStepID: "end"},
	}
}

// TestConvertRoutingBranchStepTypeSchemaPublishesReason prevents the tool's
// public contract from drifting away from requireReason in its executor. The
// mismatch previously made every conversion impossible: omitting reason
// failed in the handler, while supplying it was rejected by schema validation.
func TestConvertRoutingBranchStepTypeSchemaPublishesReason(t *testing.T) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal([]byte(getConvertRoutingBranchStepTypeSchema()), &schema); err != nil {
		t.Fatalf("decode conversion tool schema: %v", err)
	}
	if _, ok := schema.Properties["reason"]; !ok {
		t.Fatal("conversion tool schema does not publish reason, but its executor requires it")
	}
	reasonRequired := false
	for _, field := range schema.Required {
		if field == "reason" {
			reasonRequired = true
			break
		}
	}
	if !reasonRequired {
		t.Fatal("conversion tool schema publishes reason but does not mark it required")
	}
}

// convertRoutingBranchTestPlanFileIO returns readFile/writeFile stubs for
// createConvertRoutingBranchStepTypeExecutor tests, following the same
// lightweight (no HTTP server) pattern as
// TestUpdateRoutingStepCanRepairPreviouslyDanglingGraph in
// planning_graph_integrity_test.go. Also records every path writeFile is
// called with, so a test can assert step_config.json was never touched.
func convertRoutingBranchTestPlanFileIO(t *testing.T, plan *PlanningResponse) (readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error, writtenPaths *[]string, writtenPlan *string) {
	t.Helper()
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	writtenPaths = &[]string{}
	writtenPlan = new(string)
	readFile = func(_ context.Context, path string) (string, error) {
		if strings.HasSuffix(path, "step_config.json") {
			return "[]", nil
		}
		if strings.Contains(path, "evaluation/") {
			return "", errors.New("not found")
		}
		if strings.Contains(path, "changelog") {
			return "", errors.New("not found")
		}
		return string(planJSON), nil
	}
	writeFile = func(_ context.Context, path, content string) error {
		*writtenPaths = append(*writtenPaths, path)
		if strings.HasSuffix(path, "plan.json") {
			*writtenPlan = content
		}
		return nil
	}
	return readFile, writeFile, writtenPaths, writtenPlan
}

// TestConvertRoutingBranchStepTypeFromRoutingToBranch covers the fix for a
// P2 finding on the /migrate-routing-to-branch temporary command: its
// original guidance claimed reusing a step's id via delete-then-recreate
// preserved step_config.json/drift-review history, but delete_plan_steps
// prunes the deleted id's step_config.json row before the id is ever reused,
// so that claim was false. This purpose-built tool instead relabels the
// step's type in place -- the id, and therefore its step_config.json row,
// is never touched at all.
func TestConvertRoutingBranchStepTypeFromRoutingToBranch(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&RoutingPlanStep{
			Type:             StepTypeRouting,
			CommonStepFields: CommonStepFields{ID: "decision-step", Title: "Decision"},
			RoutingQuestion:  "Which path?",
			Routes:           convertRoutingBranchTestRoutes(),
			DefaultRouteID:   "route-a",
		},
	}}
	readFile, writeFile, writtenPaths, writtenPlan := convertRoutingBranchTestPlanFileIO(t, plan)
	executor := createConvertRoutingBranchStepTypeExecutor("workflow", loggerv2.NewNoop(), readFile, writeFile)

	result, err := executor(context.Background(), map[string]interface{}{
		"existing_step_id": "decision-step",
		"target_type":      "branch",
		"reason":           "PLAT-259: reclassify small in-flow decision",
	})
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	if !strings.Contains(result, "Successfully converted step 'decision-step' from routing to branch") {
		t.Fatalf("unexpected result: %s", result)
	}

	var converted PlanningResponse
	if err := json.Unmarshal([]byte(*writtenPlan), &converted); err != nil {
		t.Fatalf("decode persisted plan: %v", err)
	}
	if len(converted.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(converted.Steps))
	}
	branchStep, ok := converted.Steps[0].(*BranchPlanStep)
	if !ok {
		t.Fatalf("converted step = %T, want *BranchPlanStep", converted.Steps[0])
	}
	if branchStep.ID != "decision-step" {
		t.Fatalf("converted step id = %q, want unchanged %q", branchStep.ID, "decision-step")
	}
	if branchStep.BranchQuestion != "Which path?" {
		t.Fatalf("branch_question = %q, want the old routing_question text preserved", branchStep.BranchQuestion)
	}
	if len(branchStep.Routes) != 2 || branchStep.DefaultRouteID != "route-a" {
		t.Fatalf("routes/default_route_id not preserved: %+v", branchStep)
	}

	for _, p := range *writtenPaths {
		if strings.Contains(p, "step_config.json") {
			t.Fatalf("conversion wrote to step_config.json (%s) -- it must never touch step_config, that's the whole point: the id, and its config row, are untouched", p)
		}
	}
}

// TestConvertRoutingBranchStepTypeFromBranchToRouting covers the reverse
// direction.
func TestConvertRoutingBranchStepTypeFromBranchToRouting(t *testing.T) {
	// PLAT-294: a routing step's routes must each start a sub-workflow, so
	// the branch being promoted routes to real steps, not to end. The plan
	// write validates the graph, so those target steps have to exist.
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&BranchPlanStep{
			Type:             StepTypeBranch,
			CommonStepFields: CommonStepFields{ID: "decision-step", Title: "Decision"},
			BranchQuestion:   "Which path?",
			Routes: []RoutingRoute{
				{RouteID: "route-a", RouteName: "A", Condition: "c", NextStepID: "audit-chain"},
				{RouteID: "route-b", RouteName: "B", Condition: "c", NextStepID: "apply-chain"},
			},
			DefaultRouteID: "route-a",
		},
		&RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: "audit-chain", Title: "Audit", Description: "audit sub-workflow"}},
		&RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: "apply-chain", Title: "Apply", Description: "apply sub-workflow"}},
	}}
	readFile, writeFile, _, writtenPlan := convertRoutingBranchTestPlanFileIO(t, plan)
	executor := createConvertRoutingBranchStepTypeExecutor("workflow", loggerv2.NewNoop(), readFile, writeFile)

	_, err := executor(context.Background(), map[string]interface{}{
		"existing_step_id": "decision-step",
		"target_type":      "routing",
		"reason":           "PLAT-259: reclassify as a major sub-workflow fork",
	})
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	var converted PlanningResponse
	if err := json.Unmarshal([]byte(*writtenPlan), &converted); err != nil {
		t.Fatalf("decode persisted plan: %v", err)
	}
	routingStep, ok := converted.Steps[0].(*RoutingPlanStep)
	if !ok {
		t.Fatalf("converted step = %T, want *RoutingPlanStep", converted.Steps[0])
	}
	if routingStep.ID != "decision-step" || routingStep.RoutingQuestion != "Which path?" {
		t.Fatalf("unexpected converted step: %+v", routingStep)
	}
}

// TestConvertRoutingBranchStepTypeRejectsNoOpConversion covers requesting a
// step's already-current type.
func TestConvertRoutingBranchStepTypeRejectsNoOpConversion(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&RoutingPlanStep{
			Type:             StepTypeRouting,
			CommonStepFields: CommonStepFields{ID: "decision-step", Title: "Decision"},
			RoutingQuestion:  "Which path?",
			Routes:           convertRoutingBranchTestRoutes(),
			DefaultRouteID:   "route-a",
		},
	}}
	readFile, writeFile, _, _ := convertRoutingBranchTestPlanFileIO(t, plan)
	executor := createConvertRoutingBranchStepTypeExecutor("workflow", loggerv2.NewNoop(), readFile, writeFile)

	_, err := executor(context.Background(), map[string]interface{}{
		"existing_step_id": "decision-step",
		"target_type":      "routing",
		"reason":           "no-op",
	})
	if err == nil {
		t.Fatal("expected an error converting a routing step to routing")
	}
	if !strings.Contains(err.Error(), "already a routing step") {
		t.Fatalf("unexpected error: %v", err)
	}
}
