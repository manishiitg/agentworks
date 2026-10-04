package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

func TestReportRunVaultBridgeUsesLiveViewerAndEndsWithScript(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	allowed := true
	requests := 0
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("X-CapLayer-Actor") != "viewer" || r.Header.Get("X-Vault-Builder") != "" {
			t.Error("report did not retain its ordinary viewer identity")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		rows := []map[string]any{}
		if allowed {
			rows = append(rows, map[string]any{"id": "c-notion", "label": "Notion", "provider": "notion", "tools": []map[string]any{{"name": "notion__fetch-page"}}})
		}
		json.NewEncoder(w).Encode(map[string]any{"servers": rows})
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	api := &StreamingAPI{eventStore: events.NewEventStore(10)}
	const session = "report-run-test"
	api.eventStore.SetSessionOwner(session, "owner") // Must never override the live viewer.
	api.reportRunSessions.Store(session, reportRunScope{workspacePath: "Workflow/report", userID: "viewer"})
	ctx, tool, err := api.vaultBridgeToolName(context.Background(), session, "vault_c_notion", "notion__fetch_page")
	if err != nil || tool != "notion__fetch-page" {
		t.Fatalf("live report bridge: %q %v", tool, err)
	}
	if _, err := api.resolveGovernedMCP(ctx, "viewer", "vault_c_notion"); err != nil {
		t.Fatal(err)
	}
	ctx, tool, err = api.vaultBridgeToolName(context.Background(), session, "Notion", "fetch-page")
	if err != nil || tool != "notion__fetch-page" {
		t.Fatalf("legacy report tool path was lost: %q %v", tool, err)
	}
	if _, err := api.resolveScopedGovernedMCP(ctx, nil, []string{"Notion"}, []string{"Notion:fetch-page"}, "viewer", "Notion", tool); err != nil {
		t.Fatalf("legacy selected tool was lost: %v", err)
	}
	if _, err := api.resolveScopedGovernedMCP(ctx, nil, []string{"Notion"}, []string{"Notion:other-tool"}, "viewer", "Notion", tool); err != nil {
		t.Fatal("authorized Vault tools must be independent of project tool selection")
	}
	allowed = false
	if _, err := api.resolveGovernedMCP(context.Background(), "viewer", "vault_c_notion"); err == nil {
		t.Fatal("viewer revocation was ignored")
	}
	api.reportRunSessions.Delete(session)
	before := requests
	if _, _, err := api.vaultBridgeToolName(context.Background(), session, "vault_c_notion", "notion__fetch_page"); err == nil {
		t.Fatal("ended report inherited the event-store owner")
	}
	if requests != before {
		t.Fatal("ended report reached Vault")
	}
}
