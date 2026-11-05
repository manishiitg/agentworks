package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func managedGlobalTestAPI(t *testing.T) *StreamingAPI {
	t.Helper()
	api, _ := sharedSecretsTestAPI(t)
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	registerWorkflowOwner(sharedSecretsTestWorkflow, "a1")
	withManagedVaultPermissions(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","username":"admin","admin":true,"can_create":true,"products":[]},{"id":"a1","username":"owner","can_create":true,"products":[]},{"id":"c3","username":"reader","can_create":false,"products":[]}]}`)
	previous := managedGlobals
	managedGlobals = map[string]string{}
	t.Cleanup(func() { managedGlobals = previous })
	return api
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
	projectSecrets := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("X-CapLayer-Actor") == "a1" && !strings.HasPrefix(r.URL.Path, "/api/admin/") {
			switch {
			case r.URL.Path == "/api/project-vaults":
				w.Write([]byte(`{"id":"v-123456"}`))
			case r.URL.Path == "/api/vaults/v-123456/secrets":
				var in struct{ Name string }
				json.NewDecoder(r.Body).Decode(&in)
				projectSecrets = append(projectSecrets, in.Name)
				w.Write([]byte(`{}`))
			case r.URL.Path == "/api/vaults/v-123456":
				json.NewEncoder(w).Encode(map[string]any{"secret_names": projectSecrets})
			case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/vaults/v-123456/secrets/"):
				gone := strings.TrimPrefix(r.URL.Path, "/api/vaults/v-123456/secrets/")
				kept := projectSecrets[:0]
				for _, n := range projectSecrets {
					if n != gone {
						kept = append(kept, n)
					}
				}
				projectSecrets = kept
				w.WriteHeader(204)
			default:
				w.WriteHeader(404)
			}
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
