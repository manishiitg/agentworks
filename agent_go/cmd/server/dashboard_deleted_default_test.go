package server

import (
	"context"
	"encoding/json"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

// PLAT-750-50: reads and sync must not recreate a removed starting page.
func TestDeletedDefaultDashboardStaysDeletedOnReadAndSync(t *testing.T) {
	f := newExternalToolsFixture(t)
	withProjectOwnerRegistry(t)
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	ctx := context.Background()
	target, err := f.api.dashboardTarget(ctx, owner, "Workflow/invoices")
	if err != nil {
		t.Fatal(err)
	}
	// This helper is now called only by creation or explicit creation consent.
	if _, _, err = f.api.ensureDefaultDashboard(ctx, owner, target); err != nil {
		t.Fatal(err)
	}
	// Deletion must disable both authenticated and public links, but preserve
	// the saved revision files. Use the same HTTP action as chat and the card.
	code, err := newDashboardPublicCode()
	if err != nil {
		t.Fatal(err)
	}
	if err = saveDashboardPublicPage(dashboardPublicRecord{Code: code, Root: target.Root, Dashboard: defaultDashboardID}, map[string]string{"index.html": "retained public snapshot"}); err != nil {
		t.Fatal(err)
	}
	shortCode := "23456789ab"
	dashboardQuestionRegistryMu.Lock()
	err = saveDashboardQuestionJSON(ctx, dashboardQuestionRegistryPath, dashboardQuestionRegistry{Resources: []dashboardQuestionResource{{ID: "resource-delete", Code: shortCode, Root: target.Root, Dashboard: defaultDashboardID}}})
	dashboardQuestionRegistryMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	reader := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"dashboards:read"}, WorkflowIDs: []string{"invoices"}}}
	denied := httptest.NewRecorder()
	denyPayload, _ := json.Marshal(map[string]string{"action": "delete", "workspace": target.Root, "dashboard_id": defaultDashboardID})
	f.api.handleDashboards(denied, adminRequest(http.MethodPost, "/api/dashboards", string(denyPayload), reader, nil))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("read-only token deleted dashboard: %d %s", denied.Code, denied.Body.String())
	}
	if exists, err := crewHasPublishedDashboardEntry(ctx, target.Root); err != nil || !exists {
		t.Fatalf("live entry missing: %t %v", exists, err)
	}
	deleted := httptest.NewRecorder()
	payload, _ := json.Marshal(map[string]string{"action": "delete", "workspace": target.Root, "dashboard_id": defaultDashboardID})
	f.api.handleDashboards(deleted, adminRequest(http.MethodPost, "/api/dashboards", string(payload), owner, nil))
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body.String())
	}
	gone := httptest.NewRecorder()
	f.api.handleDashboardByCode(gone, mux.SetURLVars(adminRequest(http.MethodGet, "/", "", owner, nil), map[string]string{"code": shortCode}))
	if gone.Code != http.StatusGone || !strings.Contains(gone.Body.String(), "dashboard_removed") {
		t.Fatalf("short link survived: %d %s", gone.Code, gone.Body.String())
	}
	dir, _ := dashboardPublicDir()
	public, err := readDashboardPublicRecord(dir, code)
	if err != nil || !public.Removed || public.Root != "" {
		t.Fatalf("public snapshot retained authority: %+v %v", public, err)
	}

	if exists, err := crewHasPublishedDashboardEntry(ctx, target.Root); err != nil || exists {
		t.Fatalf("deleted entry still opens dashboard: %t %v", exists, err)
	}
	if err = f.api.refreshPublicationDashboard(ctx, owner, target); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	f.api.handleDashboards(response, adminRequest(http.MethodPost, "/api/dashboards", `{"action":"list","workspace":"Workflow/invoices"}`, owner, nil))
	var body struct {
		Dashboards []json.RawMessage `json:"dashboards"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Dashboards) != 0 {
		t.Fatalf("deleted default recreated: %d %s", response.Code, response.Body.String())
	}
	listed, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: target.Root, Action: "list", IncludeDraft: true})
	if err != nil || len(listed.Dashboards) != 0 {
		t.Fatalf("sync recreated definition: %+v %v", listed, err)
	}
}
