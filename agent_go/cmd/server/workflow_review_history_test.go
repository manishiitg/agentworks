package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// A saved failed/stale review must not intercept the real scheduled Builder
// tool registration. Use an incomplete run request so the real executor proves
// it was reached without starting background work or calling a model. Before
// removal, installWorkflowPhaseTools wrapped this exact tool with AI review.
func TestSavedWorkflowReviewNeverHoldsExecution(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	ws := serveContractGuardManifest(t, WorkflowContractCurrentVersion)
	if err := writeFileToWorkspace(context.Background(), ws+"/variables/variables.json", `{"groups":[{"name":"one","enabled":true},{"name":"two","enabled":true}]}`); err != nil {
		t.Fatal(err)
	}
	planning := filepath.Join(root, ws, "planning")
	if err := os.MkdirAll(planning, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(name, raw string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(planning, name), []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("plan.json", `{"steps":[{"id":"step-a","type":"scripted","description":"Produce a result."}]}`)
	write("step_config.json", `{"steps":[{"id":"step-a","agent_configs":{"drift_review":{"needs_review":true,"contract_version":3,"checks":[]}}}]}`)
	const sessionID = "schedule-cron--saved-review-regression"
	logger := loggerv2.NewNoop()
	session, err := workflow.NewWorkshopChatSession(context.Background(), &workflow.WorkshopConfig{
		Logger: logger, WorkspacePath: ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	api := &StreamingAPI{logger: logger}
	api.workshopChatSessions.Store(sessionID, session)
	draft := &productSurfaceDraft{}
	if err := api.installWorkflowPhaseTools(context.Background(), draft, sessionID,
		"test-user", "workflow-builder", ws, "", map[string]string{"WorkshopMode": "workshop"},
		nil, nil, nil, nil, nil, QueryRequest{TriggeredBy: "cron"}, false); err != nil {
		t.Fatal(err)
	}
	run, ok := draft.tools["run_full_workflow"]
	if !ok {
		t.Fatal("real scheduled Builder run tool was not registered")
	}
	for _, outcome := range []string{"unresolved", "incomplete", "running"} {
		write("workflow_review.json", `{"version":1,"reviewed_revisions":["unchanged-plan"],"latest":{"revision":"unchanged-plan","outcome":"`+outcome+`","unresolved":[{"step_id":"step-a","reason":"The plan changed or a prior drift check remains unresolved"}]}}`)
		out, err := run.exec(context.Background(), map[string]interface{}{})
		if err == nil || !strings.Contains(err.Error(), "group_name is required") {
			t.Fatalf("real run executor intercepted by %s review: %q %v", outcome, out, err)
		}
		if latest := LatestWorkflowReview(ws); latest == nil || latest.Outcome != outcome {
			t.Fatalf("old %s review history was changed: %+v", outcome, latest)
		}
	}
}
