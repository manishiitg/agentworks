package server

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func TestStaticExportKeepsAuthoringAndExecuteConsent(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AGENTWORKS_MCP_BUILDER_ENABLED", "true")
	for _, scopes := range [][]string{{"dashboards:read"}, {"dashboards:read", "builder:chat", "files:read", "workflows:read"}} {
		caller := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: scopes, WorkflowIDs: []string{"invoices"}}}
		if dashboardScopeAllowed(caller, "export_static") {
			t.Fatal("export acquired missing execution/authoring consent")
		}
	}
	caller := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"dashboards:read", "builder:chat", "files:read", "workflows:read", "runs:execute"}, WorkflowIDs: []string{"invoices"}}}
	if !dashboardScopeAllowed(caller, "export_static") {
		t.Fatal("existing Builder execution consent was refused")
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/dashboards", nil).WithContext(context.WithValue(context.Background(), UserContextKey, caller))
	f.api.dashboardStaticExport(response, request, dashboardTarget{Root: "Workflow/invoices", Kind: "workflow", ID: "invoices", Edit: false}, dashboardExportOptions{})
	if response.Code != 403 {
		t.Fatalf("Run target exported: %d", response.Code)
	}
}
