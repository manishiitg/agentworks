package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVaultSecretsRuntimeIsExplicitAuthorizedAndFailClosed(t *testing.T) {
	// PLAT-856-217: hiding the Vault screen must not disable selected runtime secrets.
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "agentworks")
	t.Setenv("AGENT_PRODUCTS", "agentworks")
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	managedGlobalsMu.Lock()
	oldGlobals, oldManaged := globalSecrets, managedGlobals
	globalSecrets = nil
	managedGlobals = map[string]string{"KEY": "dummy-secret", "OTHER": "other-dummy"}
	managedGlobalsMu.Unlock()
	defer func() {
		managedGlobalsMu.Lock()
		globalSecrets, managedGlobals = oldGlobals, oldManaged
		managedGlobalsMu.Unlock()
	}()
	allowed := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("s", 32) || r.Header.Get("X-CapLayer-Actor") == "" {
			t.Error("host identity absent")
		}
		if r.URL.Path == "/api/vaults/v-fixture" {
			if !allowed || r.Header.Get("X-CapLayer-Actor") != "alice" || r.Header.Get("X-Vault-Platform-User") != "1" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path != "/api/admin/runtime/secrets" {
			t.Error("wrong runtime route")
		}
		rows := []map[string]string{}
		if allowed && r.Header.Get("X-CapLayer-Actor") == "alice" {
			rows = append(rows, map[string]string{"name": "KEY"})
		}
		json.NewEncoder(w).Encode(map[string]any{"secrets": rows})
	}))
	defer srv.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", srv.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	api := &StreamingAPI{}
	names := []string{"KEY"}
	ctx := context.Background()
	if _, _, err := capLayerServiceConfig(); err == nil {
		t.Fatal("runtime configuration enabled the hidden Vault browser surface")
	}
	if _, err := vaultPersonRequest(ctx, "alice", http.MethodGet, "/api/vaults/v-fixture", nil); err != nil {
		t.Fatal("project/person runtime request blocked by the product switch:", err)
	}
	if _, err := vaultPersonRequest(ctx, "bob", http.MethodGet, "/api/vaults/v-fixture", nil); err == nil {
		t.Fatal("project/person service refusal ignored")
	}
	if got := api.mergeGlobalSecretsFor(ctx, "alice", nil); len(got) != 0 {
		t.Fatal("nil inherited globals")
	}
	if got := api.mergeGlobalSecretsFor(ctx, "alice", &names); len(got) != 1 || got[0].Value != "dummy-secret" {
		t.Fatal("authorized explicit secret missing")
	}
	if got := api.mergeGlobalSecretsFor(ctx, "bob", &names); len(got) != 0 {
		t.Fatal("cross-user exposure")
	}
	if err := validateVaultSecretSelection(ctx, "bob", &names); err == nil {
		t.Fatal("run not blocked")
	}
	allowed = false
	if got := api.mergeGlobalSecretsFor(ctx, "alice", &names); len(got) != 0 {
		t.Fatal("revocation ignored")
	}
	srv.Close()
	if got := api.mergeGlobalSecretsFor(ctx, "alice", &names); len(got) != 0 {
		t.Fatal("service failure leaked globals")
	}
	if err := validateVaultSecretSelection(ctx, "alice", &names); err == nil {
		t.Fatal("outage didn't block run")
	}
}

func TestDisabledVaultCannotExposeStoredGlobals(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "")
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("CAPLAYER_SERVICE_URL", "http://127.0.0.1:1")
	managedGlobalsMu.Lock()
	oldGlobals, oldManaged := globalSecrets, managedGlobals
	globalSecrets = nil
	managedGlobals = map[string]string{"SHARED": "dummy"}
	managedGlobalsMu.Unlock()
	t.Cleanup(func() {
		managedGlobalsMu.Lock()
		globalSecrets, managedGlobals = oldGlobals, oldManaged
		managedGlobalsMu.Unlock()
	})
	ctx := context.Background()
	if rows, err := permittedGlobalSecrets(ctx, "default"); err != nil || len(rows) != 0 {
		t.Fatalf("disabled Vault exposed shared names: %v %v", rows, err)
	}
	names := []string{"SHARED"}
	api := &StreamingAPI{}
	if rows := api.mergeGlobalSecretsFor(ctx, "default", &names); len(rows) != 0 {
		t.Fatal("disabled Vault injected stored values")
	}
	if err := validateVaultSecretSelection(ctx, "default", &names); err == nil {
		t.Fatal("stale selected Vault value authorized while disabled")
	}
}

// Older runtime tests now model an explicit live group grant, rather than implicit global access.
func withVaultSecretGrant(t *testing.T, userID string, names ...string) {
	t.Helper()
	t.Setenv("MULTI_USER_MODE", "false")
	token := strings.Repeat("s", 32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("X-CapLayer-Actor") != userID || r.URL.Path != "/api/admin/runtime/secrets" {
			w.WriteHeader(403)
			return
		}
		rows := []map[string]string{}
		for _, name := range names {
			rows = append(rows, map[string]string{"name": name})
		}
		json.NewEncoder(w).Encode(map[string]any{"secrets": rows})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CAPLAYER_SERVICE_URL", srv.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", token)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
}

// A project-selected secret is lent by the Crew owner. Personal Vault grants
// must not make the same project work in one owner's chat and fail in another.
func TestCrewTeamMembersShareSelectedSecretsInChatAndMCP(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	joinCrewTeam(t, "test-team", "other")
	withVaultSecretGrant(t, "owner", "CREW_TOKEN", "NOT_SELECTED")
	t.Setenv("MULTI_USER_MODE", "true")
	managedGlobalsMu.Lock()
	oldGlobals, oldManaged := globalSecrets, managedGlobals
	globalSecrets = nil
	managedGlobals = map[string]string{"CREW_TOKEN": "test-only-value", "NOT_SELECTED": "not-for-this-project"}
	managedGlobalsMu.Unlock()
	t.Cleanup(func() {
		managedGlobalsMu.Lock()
		globalSecrets, managedGlobals = oldGlobals, oldManaged
		managedGlobalsMu.Unlock()
	})
	f.write(t, externalCommandCrewState+"/workflow.json", `{"id":"team","label":"Test Team","created_by":"owner","capabilities":{"selected_global_secret_names":["CREW_TOKEN"]}}`)
	names := []string{"CREW_TOKEN"}
	for _, person := range []string{"owner", "other"} {
		ctx := internalBotRequestContext(t.Context(), person)
		req := QueryRequest{SelectedFolder: externalCommandCrewRoot, SelectedGlobalSecrets: &names}
		if err := f.api.validateQuerySecretSelection(ctx, person, req); err != nil {
			t.Errorf("Builder admission for %s: %v", person, err)
		}
		env := f.api.crewSelectedSecretEnv(ctx, "work", externalCommandCrewRoot, externalCommandCrewRoot, person)
		if env["SECRET_CREW_TOKEN"] != "test-only-value" || len(env) != 1 {
			t.Errorf("MCP selected secret for %s: missing or includes unselected names", person)
		}
	}
	// This does not change the co-owner's personal Vault grants.
	if err := validateVaultSecretSelection(t.Context(), "other", &names); err == nil {
		t.Fatal("project access widened personal Vault grants")
	}
	// A Team's joined member builds in the Crew the same way, in the agent's own turn setup too (owner, 5 Nov: the owner's
	// tools are lent to the Team); an invited person and a Run person keep their own grants.
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work"]},{"id":"other","role":"editor","products":["work"]},{"id":"member","role":"editor","products":["work"]},{"id":"invited","role":"editor","products":["work"]}]}`)
	if err := writeCrewTeam(crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "member"}, {UserID: "invited", Invited: true}}}); err != nil {
		t.Fatal(err)
	}
	if got := f.api.crewSecretsPerson("member", externalCommandCrewRoot, "test"); got != "owner" {
		t.Fatalf("a joined Team member's turn is admitted as %q, want the owner", got)
	}
	if err := validateVaultSecretSelection(t.Context(), f.api.crewSecretsPerson("member", externalCommandCrewRoot, "test"), &names); err != nil {
		t.Fatalf("a joined Team member's turn was refused the owner's selected secret: %v", err)
	}
	if got := f.api.crewSecretsPerson("invited", externalCommandCrewRoot, "test"); got != "invited" {
		t.Fatalf("an invited person was lent the owner's secrets: %q", got)
	}
}
