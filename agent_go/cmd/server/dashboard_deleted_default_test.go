package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if _, err = dashboardWorkspaceRequest(ctx, dashboards.Request{Root: target.Root, Action: "delete", ID: defaultDashboardID, IncludeDraft: true}); err != nil {
		t.Fatal(err)
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
