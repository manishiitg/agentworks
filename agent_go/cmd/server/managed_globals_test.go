package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func managedGlobalTestAPI(t *testing.T) *StreamingAPI {
	t.Helper()
	api, _ := sharedSecretsTestAPI(t)
	withManagedVaultPermissions(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","username":"admin","admin":true,"can_create":true,"products":[]},{"id":"a1","username":"owner","can_create":true,"products":[]},{"id":"c3","username":"reader","can_create":false,"products":[]}]}`)
	previous := managedGlobals
	managedGlobals = map[string]string{}
	t.Cleanup(func() { managedGlobals = previous })
	return api
}

func TestManagedGlobalPromotionPermissionsPersistenceAndResolution(t *testing.T) {
	api := managedGlobalTestAPI(t)
	ctx := context.Background()
	const name = "PROMOTED_TOKEN"
	const value = "sensitive-test-value"
	if err := api.upsertSharedWorkflowSecret(ctx, sharedSecretsTestWorkflow, name, value); err != nil {
		t.Fatal(err)
	}
	for _, uid := range []string{"a1", "c3"} {
		rec := httptest.NewRecorder()
		api.handleManageGlobalSecret(rec, sharedSecretsRequest(http.MethodPost, "/api/secrets/global", uid, map[string]string{"name": name, "workspace_path": sharedSecretsTestWorkflow}))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s status %d", uid, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	api.handleManageGlobalSecret(rec, sharedSecretsRequest(http.MethodPost, "/api/secrets/global", "admin", map[string]string{"name": name, "workspace_path": sharedSecretsTestWorkflow}))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), value) {
		t.Fatalf("promotion: %d %s", rec.Code, rec.Body.String())
	}
	source, err := api.ensureSharedWorkflowSecrets(ctx, sharedSecretsTestWorkflow, "admin")
	if err != nil || len(source) != 0 {
		t.Fatalf("source still contains a local copy: %v %v", source, err)
	}
	stored, err := api.chatStore.ListUserSecrets(ctx, managedGlobalSecretsUserID)
	if err != nil || len(stored) != 1 || strings.Contains(stored[0].EncryptedValue, value) {
		t.Fatalf("expected encrypted global persistence")
	}
	managedGlobals = map[string]string{}
	if err := api.loadManagedGlobalSecrets(ctx); err != nil {
		t.Fatal(err)
	}
	selected := api.loadSelectedSecrets(ctx, "a1", sharedSecretsTestWorkflow, []string{name})
	if len(selected) != 1 || selected[0].Value != value {
		t.Fatal("source attachment did not survive promotion/reload")
	}
	names := []string{name}
	merged := api.mergeGlobalSecretsFor(ctx, "admin", nil, &names)
	if len(merged) != 1 || merged[0].Value != value {
		t.Fatal("other workflows cannot resolve selected global")
	}
	rec = httptest.NewRecorder()
	api.handleGetGlobalSecrets(rec, sharedSecretsRequest(http.MethodGet, "/api/secrets/global", "c3", nil))
	if strings.Contains(rec.Body.String(), value) || !strings.Contains(rec.Body.String(), `"managed":true`) {
		t.Fatal("list must expose metadata only")
	}
	if err := api.saveManagedGlobalSecret(ctx, "admin", name, "rotated-value", false); err != nil {
		t.Fatal(err)
	}
	selected = api.loadSelectedSecrets(ctx, "a1", sharedSecretsTestWorkflow, []string{name})
	if len(selected) != 1 || selected[0].Value != "rotated-value" {
		t.Fatal("source did not receive global rotation")
	}
	if err := api.deleteManagedGlobalSecret(ctx, "a1", name); !errors.Is(err, errGlobalAdmin) {
		t.Fatal("owner can delete global")
	}
	if err := api.deleteManagedGlobalSecret(ctx, "admin", name); err != nil {
		t.Fatal(err)
	}
	if err := api.loadManagedGlobalSecrets(ctx); err != nil {
		t.Fatal(err)
	}
	if _, exists := managedGlobals[name]; exists {
		t.Fatal("deleted global survived reload")
	}
}

func TestManagedGlobalConflictsDoNotRemoveSource(t *testing.T) {
	api := managedGlobalTestAPI(t)
	ctx := context.Background()
	if err := api.upsertSharedWorkflowSecret(ctx, sharedSecretsTestWorkflow, "COLLISION", "source"); err != nil {
		t.Fatal(err)
	}
	if err := api.saveManagedGlobalSecret(ctx, "admin", "COLLISION", "original", false); err != nil {
		t.Fatal(err)
	}
	if err := api.promoteWorkflowSecret(ctx, "admin", sharedSecretsTestWorkflow, "COLLISION"); !errors.Is(err, errGlobalConflict) {
		t.Fatalf("conflict: %v", err)
	}
	source, _ := api.ensureSharedWorkflowSecrets(ctx, sharedSecretsTestWorkflow, "admin")
	if len(source) != 1 || managedGlobals["COLLISION"] != "original" {
		t.Fatal("conflict changed a secret")
	}
	previous := globalSecrets
	globalSecrets = append(globalSecrets, globalSecretEntry{Name: "ENV_LOCKED", Value: "environment"})
	t.Cleanup(func() { globalSecrets = previous })
	if err := api.saveManagedGlobalSecret(ctx, "admin", "ENV_LOCKED", "replacement", false); err == nil {
		t.Fatal("environment global overwritten")
	}
	if err := api.deleteManagedGlobalSecret(ctx, "admin", "ENV_LOCKED"); err == nil {
		t.Fatal("environment global deleted")
	}
}

// Shared secrets are Vault secrets, managed in Vault (owner, 2026-10-09:
// "global = vault"); no Builder chat gets a shared-secret tool, admin or not.
func TestBuilderChatsHaveNoSharedSecretTool(t *testing.T) {
	api := managedGlobalTestAPI(t)
	admin := &recordingRegistrar{}
	if err := api.registerSecretManagementTools(admin, "admin", sharedSecretsTestWorkflow, "secrets", false, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, exists := admin.tools["manage_global_secret"]; exists {
		t.Fatal("Builder chat has a shared-secret tool; that belongs to Vault")
	}
}

func TestManagedGlobalRejectsUnreadableCiphertext(t *testing.T) {
	api := managedGlobalTestAPI(t)
	request := sharedSecretsRequest(http.MethodPut, "/api/secrets/global", "admin", map[string]string{"name": "WRONG", "encrypted_value": "invalid"})
	rec := httptest.NewRecorder()
	api.handleManageGlobalSecret(rec, request)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestListSecretsFromAnotherWorkflowNeedsAdmin(t *testing.T) {
	api := managedGlobalTestAPI(t)
	ctx := context.Background()
	const name = "CROSS_WORKFLOW_TOKEN"
	const value = "private-source-value"
	if err := api.upsertSharedWorkflowSecret(ctx, sharedSecretsTestWorkflow, name, value); err != nil {
		t.Fatal(err)
	}
	registrar := &recordingRegistrar{}
	if err := api.registerSecretManagementTools(registrar, "admin", "Workflow/destination", "secrets", false, nil, nil); err != nil {
		t.Fatal(err)
	}
	list := registrar.tools["list_secrets"]
	output, err := list.exec(ctx, map[string]interface{}{"source_workflow_path": sharedSecretsTestWorkflow})
	if err != nil || !strings.Contains(output, name) || strings.Contains(output, value) {
		t.Fatalf("source listing failed or exposed value: %v", err)
	}
	for _, invalid := range []interface{}{"", "Workflow/missing", "Workflow/renewals/../destination", 123} {
		if _, err := list.exec(ctx, map[string]interface{}{"source_workflow_path": invalid}); err == nil {
			t.Fatalf("listed invalid source %v", invalid)
		}
	}
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","username":"admin","can_create":true,"products":[]}]}`)
	if _, err := list.exec(ctx, map[string]interface{}{"source_workflow_path": sharedSecretsTestWorkflow}); !errors.Is(err, errGlobalAdmin) {
		t.Fatal("demoted admin could list another source")
	}
}

func TestManagedGlobalPromotionAcceptsOwnedCrewProject(t *testing.T) {
	api := managedGlobalTestAPI(t)
	ctx := context.Background()
	const publicPath = "Chats/Work/projects/research"
	const runtimePath = "_users/admin/Chats/Work/projects/research"
	const name = "CREW_SHARED_TOKEN"
	const value = "crew-private-value"
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		runtimePath + "/product.json": `{"schema_version":1,"product":"work","id":"research","title":"Research"}`,
	}})
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	if err := api.upsertSharedWorkflowSecret(ctx, runtimePath, name, value); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	api.handleManageGlobalSecret(rec, sharedSecretsRequest(http.MethodPost, "/api/secrets/global", "admin", map[string]string{"name": name, "workspace_path": publicPath}))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), value) {
		t.Fatalf("Crew promotion: %d %s", rec.Code, rec.Body.String())
	}
	names := []string{name}
	resolved := api.mergeGlobalSecretsFor(ctx, "admin", nil, &names)
	if len(resolved) != 1 || resolved[0].Name != name || resolved[0].Value != value {
		t.Fatalf("destination could not resolve Crew-promoted global: %#v", resolved)
	}
}

func TestGlobalRevealIsAdminOnly(t *testing.T) {
	api := managedGlobalTestAPI(t)
	ctx := context.Background()
	if err := api.saveManagedGlobalSecret(ctx, "admin", "MANAGED_TOKEN", "managed-value", false); err != nil {
		t.Fatal(err)
	}
	previousEnv := globalSecrets
	globalSecrets = []globalSecretEntry{{Name: "ENV_TOKEN", Value: "env-value"}}
	t.Cleanup(func() { globalSecrets = previousEnv })

	reveal := func(uid, name string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		api.handleRevealGlobalSecret(rec, sharedSecretsRequest(http.MethodGet, "/api/secrets/global/reveal?name="+name, uid, nil))
		return rec
	}
	if rec := reveal("admin", "MANAGED_TOKEN"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "managed-value") {
		t.Fatalf("managed reveal: %d %s", rec.Code, rec.Body.String())
	}
	if rec := reveal("admin", "ENV_TOKEN"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "env-value") {
		t.Fatalf("env reveal: %d %s", rec.Code, rec.Body.String())
	}
	if rec := reveal("a1", "MANAGED_TOKEN"); rec.Code != http.StatusForbidden {
		t.Fatalf("owner reveal: %d, want 403", rec.Code)
	}
	if rec := reveal("admin", "MISSING"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing reveal: %d, want 404", rec.Code)
	}
	if rec := reveal("admin", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty reveal: %d, want 400", rec.Code)
	}
}

// This persistence fixture supplies explicit test group grants. Registration alone
// does not grant access in production (covered by store and runtime isolation tests).
func withManagedVaultPermissions(t *testing.T) {
	t.Helper()
	token := strings.Repeat("s", 32)
	grants := map[string][]string{
		"admin": {"PROMOTED_TOKEN", "CROSS_WORKFLOW_TOKEN", "CREW_SHARED_TOKEN", "MANAGED_TOKEN", "TOOL_TOKEN"},
		"a1":    {"PROMOTED_TOKEN", "CROSS_WORKFLOW_TOKEN"}, "c3": {"PROMOTED_TOKEN"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/api/admin/runtime/secrets" {
			rows := []map[string]string{}
			for _, n := range grants[r.Header.Get("X-CapLayer-Actor")] {
				rows = append(rows, map[string]string{"name": n})
			}
			json.NewEncoder(w).Encode(map[string]any{"secrets": rows})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/admin/secrets") {
			w.WriteHeader(204)
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CAPLAYER_SERVICE_URL", srv.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", token)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
}
