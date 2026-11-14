package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PLAT-746-977: Settings reuses native connection authority but never adopts
// the request's inherited chat project or a browser-selected Vault.
func TestAccountMCPManagementKeepsProjectAndForeignConnectionsUntouched(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	gateway := newCrewConnectionVaultFixture(t)
	f.api.mcpConfigPath = gateway.catalog
	f.api.logger = loggerv2.NewNoop()
	before := f.read(t, externalCommandCrewRoot+"/workflow.json")
	call := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		request := adminRequest(http.MethodPost, "/api/my-vaults/op", body, &UserClaims{UserID: "owner", Username: "owner"}, nil)
		response := httptest.NewRecorder()
		f.api.handleMyVaultsOp(response, request)
		return response
	}
	created := call(`{"operation":"connect","name":"Example custom","label":"My tools","url":"https://mcp.example.com/mcp","vault_id":"v-999999","workspace_path":"Crew/foreign"}`)
	if created.Code != http.StatusOK || gateway.count() != 1 {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	edited := call(`{"operation":"edit_connection","connection_id":"c-00000001","label":"Renamed tools","vault_id":"v-999999","workspace_path":"Crew/foreign"}`)
	if edited.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", edited.Code, edited.Body.String())
	}
	gateway.mu.Lock()
	label := gateway.connections["c-00000001"]["Label"]
	gateway.mu.Unlock()
	if label != "Renamed tools" {
		t.Fatalf("edit did not persist: %v", label)
	}
	for _, operation := range []string{"edit_connection", "remove_connection", "sign_in", "sync"} {
		refused := call(`{"operation":"` + operation + `","connection_id":"` + crewForeignConnection + `","label":"Must not change"}`)
		if refused.Code == http.StatusOK || !strings.Contains(refused.Body.String(), "connection") {
			t.Fatalf("foreign %s: %d %s", operation, refused.Code, refused.Body.String())
		}
	}
	removed := call(`{"operation":"remove_connection","connection_id":"c-00000001"}`)
	if removed.Code != http.StatusOK || gateway.count() != 0 {
		t.Fatalf("remove: %d %s", removed.Code, removed.Body.String())
	}
	if after := f.read(t, externalCommandCrewRoot+"/workflow.json"); after != before {
		t.Fatal("global management changed Crew selections")
	}
	// The screen link is the authorized OAuth result, not text-parsed instructions,
	// and does not expose internal OAuth state or any saved credential.
	raw, err := mcpConnectionScreenOAuth("c-00000001", &OAuthStartResponse{AuthURL: "https://auth.example.com/authorize", State: "internal-state"}, nil)
	var result map[string]any
	if err != nil || json.Unmarshal([]byte(raw), &result) != nil || result["auth_url"] != "https://auth.example.com/authorize" || strings.Contains(raw, "internal-state") {
		t.Fatalf("OAuth screen result: %v %s", err, raw)
	}
}
