package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

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
	f.write(t, externalCommandCrewRoot+"/workflow.json", `{"id":"team","created_by":"owner","capabilities":{"selected_servers":["vault_lent"]}}`)
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
	saved, err := projectSelectedServers(ctx, externalCommandCrewRoot)
	if err != nil || !slices.Equal(saved, []string{"NO_SERVERS"}) {
		t.Fatalf("last untick became implicit all: %v, %v", saved, err)
	}
	args["selected"] = true
	if _, err := f.api.myVaultsOperation(ctx, "other", args); err == nil {
		t.Fatal("could select an item outside the caller's current panel inventory")
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
	if vaultSharingLabel(1) != "Only you" || vaultSharingLabel(2) != "You + 1 person" {
		t.Fatal("sharing count changed")
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
