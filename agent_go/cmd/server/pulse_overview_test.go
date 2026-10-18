package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPulseOverviewKeepsSavedWorkWhenOffWithoutReviewTables(t *testing.T) {
	env := newCrewFunctionEnv(t)
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	const ws = "Workflow/example"
	ctx := context.Background()
	manifest := NewWorkflowManifest("Example")
	manifest.CreatedBy = "owner"
	manifest.Access = &WorkflowAccess{Owners: []string{"owner"}}
	manifest.Pulse = &WorkflowPulseConfig{Enabled: false, OutwardActions: "ask"}
	raw, _ := json.Marshal(manifest)
	env.mock.mu.Lock()
	env.mock.files[manifestPath(ws)] = string(raw)
	env.mock.mu.Unlock()
	item, err := recordPulseGoalWork(ctx, ws, PulseGoalWorkItem{Title: "Repair collection", Status: "done", ActionTaken: "Corrected collection; effect still unknown"}, "recorded-cycle")
	if err != nil {
		t.Fatal(err)
	}
	_, db, err := openPulseStoreDB(ctx, ws, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := ensurePulseGoalCheckSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO pulse_goal_checks(check_id,checked_at,pulse_run_id,status,key_number,summary,action_taken,decision_id,alarms_json,pause_fingerprint) VALUES('GC-saved',?,'recorded-cycle','not_measured','','Waiting for a meaningful measurement.','','','[]','')`, now); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/api/workflow/pulse-overview?workspace_path="+ws, nil)
	response := httptest.NewRecorder()
	env.api.handleGetPulseOverview(response, request)
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var page struct {
		LastChange *PulseGoalWorkItem `json:"last_change"`
		NextPulse  any                `json:"next_pulse"`
		Goal       *GoalStatusView    `json:"goal_status"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.LastChange == nil || page.LastChange.ID != item.ID || page.NextPulse != nil || page.Goal == nil || page.Goal.LatestCheck == nil || page.Goal.LatestCheck.Status != "not_measured" {
		t.Fatalf("saved OFF projection=%s", response.Body.String())
	}
	var retired int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('pulse_reviews','pulse_issues','pulse_decisions','pulse_module_state','pulse_module_audit','pulse_review_recovery','pulse_run_mode')`).Scan(&retired); err != nil {
		t.Fatal(err)
	}
	if retired != 0 {
		t.Fatalf("current Pulse created %d retired review tables", retired)
	}
	// Existing databases may still execute the retired mirror triggers.
	if _, err := db.Exec(`CREATE TABLE pulse_decisions(id TEXT); INSERT INTO pulse_decisions VALUES('saved-history'); CREATE TRIGGER pulse_compact_decision_update AFTER UPDATE ON report_human_inputs BEGIN SELECT 1; END; CREATE TRIGGER business_rule AFTER UPDATE ON report_human_inputs BEGIN SELECT 1; END`); err != nil {
		t.Fatal(err)
	}
	// Startup must retire triggers before its schedule-only filter; this
	// OFF workflow has no schedules and must not acquire schedule state.
	if len(manifest.Schedules) != 0 {
		t.Fatal("fixture must remain unscheduled")
	}
	for _, workflow := range (&SchedulerService{}).discoverWorkflows(ctx) {
		if workflow.WorkspacePath == ws {
			t.Fatal("trigger cleanup admitted an unscheduled workflow to the scheduler")
		}
	}
	var remaining, scheduleState int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name='pulse_compact_decision_update'`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("startup left %d retired triggers: %v", remaining, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name LIKE 'schedule_%'`).Scan(&scheduleState); err != nil || scheduleState != 0 {
		t.Fatalf("cleanup created schedule state: %d %v", scheduleState, err)
	}
	var saved, other int
	if err := db.QueryRow(`SELECT count(*) FROM pulse_decisions WHERE id='saved-history'`).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name='business_rule'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if saved != 1 || other != 1 {
		t.Fatal("retirement changed saved history or a workflow trigger")
	}
	// Old owner decisions and audit-only history remain readable without rebuilding review tables.
	if _, err := createReportHumanInput(ctx, ws, ReportHumanInputCreateRequest{InputID: "retained-owner-choice", Source: "strategic_review", Question: "Keep the written goal?"}); err != nil {
		t.Fatal(err)
	}
	decisions, err := listReportHumanInputs(ctx, ws, "pending", "")
	if err != nil || len(decisions) != 1 || decisions[0].ID != "retained-owner-choice" {
		t.Fatalf("retained decisions=%v err=%v", decisions, err)
	}
	if _, err := db.Exec(`CREATE TABLE pulse_module_audit(workspace_path TEXT,module TEXT,pulse_run_id TEXT,reason TEXT,evidence_json TEXT,result TEXT,recorded_at TEXT); INSERT INTO pulse_module_audit VALUES('Workflow/example','technical_review','old-cycle','Previous repair evidence','[]','done','2026-10-01T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	notes, err := loadPulseReviewNotes(ctx, ws, "", "", 3)
	if err != nil || len(notes) != 1 || notes[0].Conclusion != "Previous repair evidence" {
		t.Fatalf("history=%v err=%v", notes, err)
	}
	var created int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='pulse_review_notes'`).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatal("reading old history created reviewer tables")
	}

}

func TestAfterRunHousekeepingUsesRunChat(t *testing.T) {
	steps := afterRunHousekeepingSteps("run-1", true, true)
	if len(steps) != 1 || !strings.Contains(steps[0].query, "run chat") || strings.Contains(steps[0].query, "record_pulse_result") {
		t.Fatalf("housekeeping=%v", steps)
	}
	tools, execs, _ := createPulseTools()
	for _, tool := range tools {
		if tool.Function.Name == "record_pulse_result" || tool.Function.Name == "record_pulse_worklist" || tool.Function.Name == "record_pulse_qa_request" {
			t.Fatalf("retired tool still registered: %s", tool.Function.Name)
		}
	}
	if _, ok := execs["record_pulse_goal_check"]; !ok {
		t.Fatal("current goal check tool missing")
	}

}
