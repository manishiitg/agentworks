package server

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// The MCP inbox shows a paused agent's question only to people who may answer
// it, and answering it from MCP reaches the paused chat.
func TestExternalNeedsYouListsAndAnswersOnlyYourQuestions(t *testing.T) {
	f := newExternalToolsFixture(t)
	const session = "owner-invoices-chat"
	f.api.activeSessions = map[string]*ActiveSessionInfo{session: {SessionID: session, UserID: "owner", WorkspacePath: "Workflow/invoices"}}
	id := "hf-" + uuid.NewString()
	store := virtualtools.GetHumanFeedbackStore()
	if err := store.CreatePendingRequest(id, "Send 40 reminder emails?", "", session, []string{"Send", "Skip today"}, false, time.Minute); err != nil {
		t.Fatal(err)
	}
	if w := f.call(t, "outsider", "list_needs_you", map[string]any{}); w.Code != 200 || strings.Contains(w.Body.String(), id) {
		t.Fatalf("another user saw the question: %d %s", w.Code, w.Body)
	}
	if w := f.call(t, "outsider", "answer_needs_you", map[string]any{"id": id, "option": "Send"}); w.Code != 404 {
		t.Fatalf("another user answered: %d %s", w.Code, w.Body)
	}
	w := f.call(t, "owner", "list_needs_you", map[string]any{"workflow_id": "invoices"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), id) || !strings.Contains(w.Body.String(), `"type":"question"`) {
		t.Fatalf("owner inbox: %d %s", w.Code, w.Body)
	}
	if w := f.call(t, "owner", "needs_you", map[string]any{"action": "dismiss", "id": id, "reason": "Outdated"}); w.Code != 400 {
		t.Fatalf("dismissed a live question: %d %s", w.Code, w.Body)
	}
	if w := f.call(t, "owner", "answer_needs_you", map[string]any{"id": id, "option": "Send"}); w.Code != 200 {
		t.Fatalf("owner answer: %d %s", w.Code, w.Body)
	}
	if response, ok := store.GetResponse(id); !ok || response != "Send" {
		t.Fatalf("paused chat got %q (%v)", response, ok)
	}
}

// A stale fixed-choice decision can leave Needs you without inventing an
// approval. The new merged action uses the website lifecycle and audit trail.
func TestExternalNeedsYouDecisionDismissalAndCustomAnswer(t *testing.T) {
	f := newExternalToolsFixture(t)
	const workspace = "Workflow/invoices"
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner"})
	for _, id := range []string{"stale-choice", "custom-choice", "fixed-choice"} {
		if _, err := createReportHumanInput(ctx, workspace, ReportHumanInputCreateRequest{
			InputID: id, Source: "pulse", Question: "Which method should be used?",
			Options: []ReportHumanInputOption{{ID: "old-method", Title: "Old method"}}, AllowFreeText: id == "custom-choice",
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []string{"reader", "readonly-owner"} {
		if w := f.call(t, user, "needs_you", map[string]any{"action": "dismiss", "id": "stale-choice", "reason": "Superseded"}); w.Code != 403 {
			t.Fatalf("%s dismissed a decision: %d %s", user, w.Code, w.Body)
		}
	}
	if w := f.call(t, "outsider", "needs_you", map[string]any{"action": "dismiss", "id": "stale-choice", "reason": "Superseded"}); w.Code != 404 {
		t.Fatalf("unrelated product decision exposed: %d %s", w.Code, w.Body)
	}
	if w := f.call(t, "owner", "needs_you", map[string]any{"action": "answer", "id": "fixed-choice", "option": "New method"}); w.Code != 400 {
		t.Fatalf("fixed-choice policy bypassed: %d %s", w.Code, w.Body)
	}
	if w := f.call(t, "owner", "needs_you", map[string]any{"action": "answer", "id": "custom-choice", "option": "New method", "answer": "Use the current instructions."}); w.Code != 200 {
		t.Fatalf("custom answer failed: %d %s", w.Code, w.Body)
	}
	const reason = "Superseded by the current instructions; none of these choices applies."
	if w := f.call(t, "owner", "needs_you", map[string]any{"action": "dismiss", "id": "stale-choice", "reason": " "}); w.Code != 400 {
		t.Fatalf("blank dismissal reason accepted: %d %s", w.Code, w.Body)
	}
	if w := f.call(t, "owner", "needs_you", map[string]any{"action": "dismiss", "id": "stale-choice", "reason": reason}); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"dismissed"`) || strings.Contains(w.Body.String(), "apply_message") {
		t.Fatalf("dismissal did not close the question without applying it: %d %s", w.Code, w.Body)
	}
	if w := f.call(t, "owner", "needs_you", map[string]any{"action": "list"}); w.Code != 200 || strings.Contains(w.Body.String(), "stale-choice") {
		t.Fatalf("dismissed question still pending: %d %s", w.Code, w.Body)
	}
	// The original alias remains callable, and dismissal does not require a
	// duplicate or weaken the separate agent-only withdrawal rules.
	if w := f.call(t, "owner", "answer_needs_you", map[string]any{"id": "fixed-choice", "dismiss": true, "answer": reason}); w.Code != 200 {
		t.Fatalf("legacy dismissal failed: %d %s", w.Code, w.Body)
	}
	normalized, db, err := openReportHumanInputDB(ctx, workspace, false)
	if err != nil || db == nil {
		t.Fatalf("decision store: %v", err)
	}
	defer db.Close()
	stale, err := getReportHumanInputByID(ctx, db, normalized, "stale-choice")
	if err != nil || stale.Status != "dismissed" || stale.SelectedOptionID != "" || stale.AnsweredAt != "" || stale.ConsumedAt != "" {
		t.Fatalf("dismissal fabricated approval or work: %+v %v", stale, err)
	}
	custom, err := getReportHumanInputByID(ctx, db, normalized, "custom-choice")
	if err != nil || custom.Note != "New method\n\nUse the current instructions." || custom.SelectedOptionID != "" || custom.AnsweredBy != "owner" {
		t.Fatalf("custom answer or attribution lost: %+v %v", custom, err)
	}
	var actor, kind, channel, details string
	if err := db.QueryRow(`SELECT actor_id, actor_kind, channel, details FROM report_human_input_events WHERE input_id='stale-choice' AND event_type='dismissed'`).Scan(&actor, &kind, &channel, &details); err != nil {
		t.Fatal(err)
	}
	var audit map[string]string
	if err := json.Unmarshal([]byte(details), &audit); err != nil || actor != "owner" || kind != "human_via_chat" || channel != "mcp" || audit["reason"] != reason {
		t.Fatalf("missing dismissal provenance: %s %s %s %s %v", actor, kind, channel, details, err)
	}
}

func TestExternalNeedsYouIncludesLatestFailedRunWithoutHomeTool(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AGENT_PRODUCTS", "agentworks")
	send := func(status string) {
		_, err := services.NewOrgDashboardConnector().SendUserNotification(t.Context(), "Run result", "", &services.NotificationDestination{WorkspacePath: "Workflow/invoices", Content: &services.NotificationContent{Summary: &services.NotificationSummary{Kind: "run_summary", Title: "Invoice run", Status: status}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	send("failed")
	w := f.call(t, "owner", "list_needs_you", map[string]any{})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"type":"run_review"`) {
		t.Fatal("missing run review", w.Code, w.Body)
	}
	w = f.call(t, "outsider", "list_needs_you", map[string]any{})
	if strings.Contains(w.Body.String(), "Invoice run") {
		t.Fatal("foreign failure leaked", w.Body)
	}
	// PLAT-825-102: the informational projection follows the Goal setting;
	// disabling it creates no decision and changes no product access.
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoice processing","created_by":"owner","access":{"owners":["owner","readonly-owner"],"readers":["reader"]},"capabilities":{"notifications":{"break_alerts":false}}}`)
	w = f.call(t, "owner", "list_needs_you", map[string]any{})
	if strings.Contains(w.Body.String(), `"type":"run_review"`) {
		t.Fatal("disabled break alerts remained visible", w.Body)
	}
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoice processing","created_by":"owner","access":{"owners":["owner","readonly-owner"],"readers":["reader"]},"capabilities":{"notifications":{"break_alerts":true}}}`)
	send("partial")
	w = f.call(t, "owner", "list_needs_you", map[string]any{})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"type":"run_review"`) {
		t.Fatal("missing partial run alert", w.Code, w.Body)
	}
	send("completed")
	w = f.call(t, "owner", "list_needs_you", map[string]any{})
	if strings.Contains(w.Body.String(), `"type":"run_review"`) {
		t.Fatal("old failure survived a later success", w.Body)
	}
}
