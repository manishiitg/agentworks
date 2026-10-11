package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

// Drive the UI answer handler, HTTP completion handler, registered agent tool
// and real SQLite history. A failed preflight must leave the original answer
// usable; completing it requires the applying agent's checked result receipt.
func TestApprovalCompletionKeepsBlockedAnswerAndRecordsCheckedResult(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	ctx := context.Background()
	workspace := "Workflow/approval-completion"
	const id = "draft-1"
	if _, err := createReportHumanInput(ctx, workspace, ReportHumanInputCreateRequest{
		InputID: id, Question: "Approve this exact draft?", Context: "Draft revision 7 only.",
		Options: []ReportHumanInputOption{{ID: "approve", Title: "Approve"}},
	}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	answer := httptest.NewRequest(http.MethodPost, "/answer", strings.NewReader(`{"workspace_path":"`+workspace+`","selected_option_id":"approve"}`))
	answer = mux.SetURLVars(answer, map[string]string{"input_id": id})
	w := httptest.NewRecorder()
	api.handleAnswerReportHumanInput(w, answer)
	if w.Code != http.StatusOK {
		t.Fatalf("answer: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Input        ReportHumanInput `json:"input"`
		ApplyMessage string           `json:"apply_message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Input.Status != "answered" || response.Input.ConsumedAt != "" || !strings.Contains(response.ApplyMessage, "outcome_evidence") {
		t.Fatalf("UI approval did not preserve answer and checked-completion handoff: %+v", response)
	}
	_, executors, _ := createReportHumanInputTools()
	complete := executors["mark_human_input_consumed"].(func(context.Context, map[string]interface{}) (string, error))
	args := map[string]interface{}{"workspace_path": workspace, "input_id": id, "outcome_summary": "Starting the approved action."}
	if _, err := complete(ctx, args); err == nil || !strings.Contains(err.Error(), "outcome_evidence is required") {
		t.Fatalf("agent tool accepted a start receipt: %v", err)
	}
	blocked := httptest.NewRequest(http.MethodPost, "/consume", strings.NewReader(`{"workspace_path":"`+workspace+`","outcome_summary":"Browser disconnected before action"}`))
	blocked = mux.SetURLVars(blocked, map[string]string{"input_id": id})
	w = httptest.NewRecorder()
	api.handleConsumeReportHumanInput(w, blocked)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "outcome_evidence is required") {
		t.Fatalf("HTTP completion accepted missing checked evidence: %d %s", w.Code, w.Body.String())
	}
	inputs, err := listReportHumanInputs(ctx, workspace, "answered", "")
	if err != nil || len(inputs) != 1 || inputs[0].ID != id || inputs[0].ConsumedAt != "" || inputs[0].OutcomeSummary != "" {
		t.Fatalf("blocked attempt consumed or replaced approval: %+v %v", inputs, err)
	}
	// This is a synthetic observed result, not a real external publication.
	args["outcome_summary"] = "Applied the approved draft."
	args["outcome_evidence"] = "Checked https://example.com/posts/draft-1: revision 7 is live."
	result, err := complete(ctx, args)
	if err != nil || !strings.Contains(result, "revision 7 is live") {
		t.Fatalf("checked completion failed: %s %v", result, err)
	}
	if _, err := complete(ctx, args); err == nil {
		t.Fatal("completion was recorded twice")
	}
	_, db, err := openReportHumanInputDB(ctx, workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	input, err := getReportHumanInputByID(ctx, db, workspace, id)
	if err != nil || input.Status != "consumed" || input.SelectedOptionID != "approve" || !strings.Contains(input.OutcomeSummary, "revision 7 is live") {
		t.Fatalf("completion history lost answer or result receipt: %+v %v", input, err)
	}
	var count int
	var details string
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(details), '') FROM report_human_input_events WHERE input_id=? AND event_type='consumed'`, id).Scan(&count, &details); err != nil {
		t.Fatal(err)
	}
	if count != 1 || !strings.Contains(details, "revision 7 is live") {
		t.Fatalf("completion audit = %d %q; want one checked receipt", count, details)
	}
}
