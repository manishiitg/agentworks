package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// The merged Vault panel must retain the company accounts the project's
// Google tools already expose, without turning private accounts into shared ones.
func TestProjectVaultShowsCompanyGoogleWithoutPrivateAccounts(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("CAPLAYER_SERVICE_URL", "")
	t.Setenv("CAPLAYER_SERVICE_TOKEN", "")
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	f, _ := newExternalCrewCommandFixture(t)
	config := services.GmailConfig{DefaultConnectionID: "company", Connections: []services.GmailConnection{
		{ID: "company", DisplayName: "Company Google", Email: "team@example.com", Enabled: true, Status: services.GmailConnectionConnected},
		{ID: "disabled", DisplayName: "Disabled company account", Status: services.GmailConnectionConnected},
		{ID: "personal", DisplayName: "Another person's Google", Email: "private@example.com", Personal: true, OwnerID: "other", Enabled: true},
		{ID: "private", DisplayName: "Private project Google", ScopeWorkspace: "Crew/other-project", OwnerID: "other", Enabled: true},
	}}
	raw, _ := json.Marshal(config)
	f.write(t, "config/gmail-config.json", string(raw))
	previous := services.GetGmailService()
	t.Cleanup(func() { services.SetGmailService(previous) })
	if _, err := services.InitGmailService(); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner"})
	r := httptest.NewRequest(http.MethodPost, "/api/my-vaults/op", strings.NewReader(`{"operation":"project","workspace_path":"Crew/test-team"}`)).WithContext(ctx)
	w := httptest.NewRecorder()
	f.api.handleMyVaultsOp(w, r)
	var response struct {
		Result string `json:"result"`
	}
	var inventory projectVaultInventory
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil || json.Unmarshal([]byte(response.Result), &inventory) != nil {
		t.Fatalf("Vault response: %d %s", w.Code, w.Body.String())
	}
	var accounts []projectVaultItem
	for _, group := range inventory.Vaults {
		for _, item := range group.Items {
			if group.ID == "company" && item.Source == "Company Google" {
				accounts = append(accounts, item)
			}
		}
	}
	if len(accounts) != 2 || accounts[0].ID != "company" || accounts[0].Status != "connected" || accounts[0].AccountEmail != "team@example.com" || !accounts[0].Selected || accounts[0].CanSelect || accounts[1].Status != "needs_attention" {
		t.Fatalf("company accounts omitted or changed authority: %+v", accounts)
	}
	if strings.Contains(response.Result, "Another person's Google") || strings.Contains(response.Result, "Private project Google") || strings.Contains(response.Result, "private@example.com") {
		t.Fatal("Vault leaked a private Google account")
	}
	if _, err := f.api.selectProjectVaultItem(ctx, "owner", externalCommandCrewRoot, "google", "company", false); err == nil {
		t.Fatal("company row changed a personal-account selection")
	}
	// MCP settings exposes the same names-only inventory as the website.
	w = httptest.NewRecorder()
	f.api.externalCrewSettingsCall(w, httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx), "get_settings", map[string]any{"crew_id": "team"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Company Google") || strings.Contains(w.Body.String(), "Another person's Google") {
		t.Fatalf("MCP inventory differs: %d %s", w.Code, w.Body.String())
	}
}

// Actual workspace + HTTP/native settings writers: one unchecked app must not
// turn every app back on, and a co-owner must not see unrelated owner accounts.
func TestProjectVaultSelectionsUseCanonicalCrewSettings(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	runContext := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "other"})
	if _, err := f.api.projectVaultInventory(runContext, "other", externalCommandCrewRoot); err == nil {
		t.Fatal("Run share opened Builder Vault inventory")
	}
	if _, err := defaultProjectOwners().AddCoOwner("work", "test-team", "other"); err != nil {
		t.Fatal(err)
	}
	// Through the workspace API, which routes a Crew's record to its private folder (PLAT-856-383), as the product does.
	if err := writeFileToWorkspace(t.Context(), externalCommandCrewRoot+"/workflow.json", `{"id":"team","created_by":"owner","capabilities":{"selected_servers":["vault_lent"],"chosen_vault_apps_only":true}}`); err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/admin/runtime/servers":
			if r.Header.Get("X-CapLayer-Actor") == "owner" {
				w.Write([]byte(`{"servers":[{"id":"lent","label":"Project app","provider":"notion"},{"id":"private","label":"Private account","provider":"notion"}],"groups":[],"secrets":[]}`))
			} else {
				w.Write([]byte(`{"servers":[],"groups":[],"secrets":[]}`))
			}
		case "/api/vaults":
			w.Write([]byte(`{"vaults":[]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "other"})
	inventory, err := f.api.projectVaultInventory(ctx, "other", externalCommandCrewRoot)
	if err != nil {
		t.Fatal(err)
	}
	lenderFound := false
	for _, group := range inventory.Vaults {
		for _, item := range group.Items {
			if item.Source == "the project's owner" {
				lenderFound = true
				if item.AccountEmail != "" {
					t.Fatal("project owner's personal account email added to lender inventory")
				}
			}
		}
	}
	if !lenderFound {
		t.Fatal("test did not exercise the project owner's app inventory")
	}
	raw, _ := json.Marshal(inventory)
	if !strings.Contains(string(raw), "Project app") || strings.Contains(string(raw), "Private account") || strings.Contains(string(raw), "device_owner_id") {
		t.Fatalf("wrong names-only project inventory: %s", raw)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/my-vaults/op", strings.NewReader(`{"operation":"project","workspace_path":"Crew/test-team"}`)).WithContext(ctx)
	response := httptest.NewRecorder()
	f.api.handleMyVaultsOp(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Project app") || strings.Contains(response.Body.String(), "Private account") {
		t.Fatalf("panel HTTP differs: %d %s", response.Code, response.Body)
	}
	// The same operation is used by the panel endpoint and Builder's native tool.
	args := map[string]any{"operation": "project_select", "workspace_path": externalCommandCrewRoot, "kind": "mcp", "item_id": "lent", "selected": false}
	if _, err := f.api.myVaultsOperation(ctx, "other", args); err != nil {
		t.Fatal(err)
	}
	// Unticking the last app means chosen apps only with none chosen, never back to every Vault app (PLAT-856-394).
	saved, chosenOnly, err := projectAppSelection(ctx, externalCommandCrewRoot)
	if err != nil || len(saved) != 0 || !chosenOnly {
		t.Fatalf("last untick became implicit all: %v, %v, %v", saved, chosenOnly, err)
	}
	args["selected"] = true
	if _, err := f.api.myVaultsOperation(ctx, "other", args); err == nil {
		t.Fatal("could select an item outside the caller's current panel inventory")
	}
	if _, err := f.api.myVaultsOperation(ctx, "other", map[string]any{"operation": "project_select", "workspace_path": externalCommandCrewRoot, "kind": "all_vault_apps", "item_id": "", "selected": true}); err != nil {
		t.Fatal(err)
	}
	if _, chosenOnly, _ = projectAppSelection(ctx, externalCommandCrewRoot); chosenOnly {
		t.Fatal("Use all my apps automatically did not switch back on")
	}
	// Project ticks cannot write into a project the caller only uses.
	if _, err := f.api.projectVaultInventory(context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "stranger"}), "stranger", externalCommandCrewRoot); err == nil {
		t.Fatal("unrelated user opened project Vault")
	}
}

func TestVaultComputerNamesStayPersonalWhenOffline(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	rememberVaultComputer("owner", "laptop-one")
	rememberVaultComputer("owner", "laptop-one")
	rememberVaultComputer("other", "other-laptop")
	if got := knownVaultComputers("owner"); !slices.Equal(got, []string{"laptop-one"}) {
		t.Fatal(got)
	}
}

// Goal inventory is the same operation and uses the Goal's current Builder
// authority, never a dashboard share or an execution session's authority.
func TestProjectVaultGoalBuilderAccess(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("CAPLAYER_SERVICE_URL", "")
	t.Setenv("CAPLAYER_SERVICE_TOKEN", "")
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	for _, person := range []string{"owner", "reader"} {
		ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: person})
		_, err := f.api.projectVaultInventory(ctx, person, "Workflow/invoices")
		if (err == nil) != (person == "owner") {
			t.Fatalf("Goal inventory as %s: %v", person, err)
		}
	}
}
