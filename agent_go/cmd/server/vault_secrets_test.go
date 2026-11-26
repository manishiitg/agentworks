package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVaultSecretsRuntimeIsExplicitAuthorizedAndFailClosed(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
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
			rows = append(rows, map[string]string{"name": "KEY"}, map[string]string{"name": "OTHER"})
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
	// PLAT-746-803 / PR1938: older workflow selections can contain two saved
	// GitHub connections. Neither may win the shared GITHUB_TOKEN alias.
	for id, name := range map[string]string{"0123456789abcdef0123456789abcdef": "KEY", "abcdef0123456789abcdef0123456789": "OTHER"} {
		if err := writeGitHubRecord("alice", githubAccountRecord{ID: id, Login: "fixture-account", SecretName: name}); err != nil {
			t.Fatal(err)
		}
	}
	if got := api.mergeGlobalSecretsFor(ctx, "alice", &names); len(got) != 1 || got[0].Name != "GITHUB_TOKEN" || got[0].Value != "dummy-secret" {
		t.Fatal("single selected GitHub connection did not get the standard alias")
	}
	both := []string{"KEY", "OTHER"}
	var logOutput bytes.Buffer
	previousLog := log.Writer()
	log.SetOutput(&logOutput)
	got := api.mergeGlobalSecretsFor(ctx, "alice", &both)
	log.SetOutput(previousLog)
	values := map[string]string{}
	for _, value := range got {
		values[value.Name] = value.Value
	}
	if len(values) != 2 || values["KEY"] != "dummy-secret" || values["OTHER"] != "other-dummy" || values["GITHUB_TOKEN"] != "" {
		t.Fatal("ambiguous GitHub credentials were aliased or lost")
	}
	if !strings.Contains(logOutput.String(), "multiple GitHub connections selected; none mapped to GITHUB_TOKEN") || strings.Contains(logOutput.String(), "dummy-secret") || strings.Contains(logOutput.String(), "other-dummy") {
		t.Fatal("ambiguous account warning absent or exposed a secret")
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

// In a Crew everyone uses their own Vault, the owner included (owner 19 Nov, GitHub #3269): a chosen secret from the
// owner's personal vault means each person's own secret with that name, and a teammate never gets the owner's value.
func TestCrewChosenSecretIsEachPersonsOwn(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	joinCrewTeam(t, "test-team", "other")
	ownerSecret, _ := vaultSecretName("v-aaaa1111", "CREW_TOKEN")
	otherSecret, _ := vaultSecretName("v-bbbb2222", "CREW_TOKEN")
	withVaultSecretGrants(t, map[string][]string{"owner": {ownerSecret}, "other": {otherSecret}})
	t.Setenv("MULTI_USER_MODE", "true")
	managedGlobalsMu.Lock()
	oldGlobals, oldManaged := globalSecrets, managedGlobals
	globalSecrets = nil
	managedGlobals = map[string]string{ownerSecret: "owner-only-value", otherSecret: "teammate-value"}
	managedGlobalsMu.Unlock()
	t.Cleanup(func() {
		managedGlobalsMu.Lock()
		globalSecrets, managedGlobals = oldGlobals, oldManaged
		managedGlobalsMu.Unlock()
	})
	f.write(t, externalCommandCrewSettings+"/workflow.json", `{"id":"team","label":"Test Team","created_by":"owner","capabilities":{"selected_global_secret_names":["`+ownerSecret+`"]}}`)
	names := []string{ownerSecret}
	for person, want := range map[string]string{"owner": "owner-only-value", "other": "teammate-value"} {
		ctx := internalBotRequestContext(t.Context(), person)
		req := QueryRequest{SelectedFolder: externalCommandCrewRoot, SelectedGlobalSecrets: &names}
		if err := f.api.validateQuerySecretSelection(ctx, person, req); err != nil {
			t.Errorf("admission for %s: %v", person, err)
		}
		env := f.api.crewSelectedSecretEnv(ctx, "work", externalCommandCrewRoot, externalCommandCrewRoot, person)
		if env["SECRET_CREW_TOKEN"] != want || len(env) != 1 {
			t.Errorf("%s got the wrong CREW_TOKEN (or extra secrets)", person)
		}
	}
	// A teammate with no secret of that name still chats (the secret is skipped), and is never lent the owner's.
	withVaultSecretGrants(t, map[string][]string{"owner": {ownerSecret}})
	t.Setenv("MULTI_USER_MODE", "true")
	otherCtx := internalBotRequestContext(t.Context(), "other")
	req := QueryRequest{SelectedFolder: externalCommandCrewRoot, SelectedGlobalSecrets: &names}
	if err := f.api.validateQuerySecretSelection(otherCtx, "other", req); err != nil {
		t.Fatalf("a missing Crew secret refused the turn: %v", err)
	}
	// ...and the agent is told which one is missing, so it can say so plainly.
	if note := crewSkippedSecretsNote(otherCtx, "other", externalCommandCrewRoot, &names); !strings.Contains(note, "CREW_TOKEN") {
		t.Fatal("the agent was not told about the skipped secret")
	}
	if note := crewSkippedSecretsNote(internalBotRequestContext(t.Context(), "owner"), "owner", externalCommandCrewRoot, &names); note != "" {
		t.Fatal("a person with the secret got a missing-secret note")
	}
	if env := f.api.crewSelectedSecretEnv(otherCtx, "work", externalCommandCrewRoot, externalCommandCrewRoot, "other"); len(env) != 0 {
		t.Fatal("a person without the secret got a value")
	}
}

// withVaultSecretGrants grants each person their own secret names; anyone else has none.
func withVaultSecretGrants(t *testing.T, grants map[string][]string) {
	t.Helper()
	t.Setenv("MULTI_USER_MODE", "false")
	token := strings.Repeat("s", 32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token || r.URL.Path != "/api/admin/runtime/secrets" {
			w.WriteHeader(403)
			return
		}
		rows := []map[string]string{}
		for _, name := range grants[r.Header.Get("X-CapLayer-Actor")] {
			rows = append(rows, map[string]string{"name": name})
		}
		json.NewEncoder(w).Encode(map[string]any{"secrets": rows})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CAPLAYER_SERVICE_URL", srv.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", token)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
}
