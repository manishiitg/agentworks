package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sharingTestVault(t *testing.T, failGrants bool) *int {
	t.Helper()
	writes := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-CapLayer-Actor") != "admin" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("s", 32) {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.URL.Path == "/api/admin/groups":
			json.NewEncoder(w).Encode(map[string]any{"groups": []map[string]string{{"ID": "platform", "Name": "Platform"}, {"ID": "finance", "Name": "Finance"}}})
		case strings.HasSuffix(r.URL.Path, "/grants"):
			*writes++
			if failGrants {
				w.WriteHeader(503)
				return
			}
			var in struct {
				GroupIDs []string `json:"group_ids"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			if len(in.GroupIDs) != 2 || in.GroupIDs[0] != "platform" || in.GroupIDs[1] != "finance" {
				t.Error("wrong recipients")
			}
			w.WriteHeader(204)
		case r.URL.Path == "/api/admin/secrets":
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CAPLAYER_SERVICE_URL", srv.URL)
	return writes
}

func TestVaultSecretSharingKeepsSource(t *testing.T) {
	api := managedGlobalTestAPI(t)
	grants := sharingTestVault(t, false)
	ctx := context.Background()
	if err := api.upsertSharedWorkflowSecret(ctx, sharedSecretsTestWorkflow, "PROJECT_KEY", "private-sharing-value"); err != nil {
		t.Fatal(err)
	}
	before, _ := api.ensureSharedWorkflowSecrets(ctx, sharedSecretsTestWorkflow, "admin")
	if err := api.shareWorkflowSecretToVault(ctx, "admin", sharedSecretsTestWorkflow, "PROJECT_KEY", "TEAM_KEY", []string{"platform", "finance"}); err != nil {
		t.Fatalf("sharing: %v", err)
	}
	after, _ := api.ensureSharedWorkflowSecrets(ctx, sharedSecretsTestWorkflow, "admin")
	if len(after) != 1 || before[0].EncryptedValue != after[0].EncryptedValue {
		t.Fatal("source changed")
	}
	stored, err := api.chatStore.ListUserSecrets(ctx, managedGlobalSecretsUserID)
	if err != nil || len(stored) != 1 || stored[0].Name != "TEAM_KEY" || strings.Contains(stored[0].EncryptedValue, "private-sharing-value") {
		t.Fatal("not encrypted")
	}
	if value, err := decryptSecretValueWithAAD(stored[0].EncryptedValue, managedGlobalAAD("TEAM_KEY")); err != nil || value != "private-sharing-value" {
		t.Fatal("copy cannot decrypt")
	}
	if *grants != 1 {
		t.Fatal("missing grants")
	}
}

func TestVaultSecretSharingRejectsUnauthorizedInvalidAndConflictingRequests(t *testing.T) {
	api := managedGlobalTestAPI(t)
	writes := sharingTestVault(t, false)
	ctx := context.Background()
	api.upsertSharedWorkflowSecret(ctx, sharedSecretsTestWorkflow, "PROJECT_KEY", "source")
	for _, tc := range []struct {
		actor, path, name string
		groups            []string
	}{
		{"a1", sharedSecretsTestWorkflow, "TEAM_KEY", []string{"platform"}},
		{"admin", sharedSecretsTestWorkflow, "TEAM_KEY", nil},
		{"admin", sharedSecretsTestWorkflow, "TEAM_KEY", []string{"missing"}},
		{"admin", "Workflow/missing", "TEAM_KEY", []string{"platform"}},
		{"admin", sharedSecretsTestWorkflow, "bad/name", []string{"platform"}},
	} {
		if err := api.shareWorkflowSecretToVault(ctx, tc.actor, tc.path, "PROJECT_KEY", tc.name, tc.groups); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	if len(managedGlobals) != 0 || *writes != 0 {
		t.Fatal("invalid request wrote data")
	}
	api.saveManagedGlobalSecret(ctx, "admin", "TEAM_KEY", "existing", true)
	rec := httptest.NewRecorder()
	api.handleShareVaultSecret(rec, sharedSecretsRequest(http.MethodPost, "/api/secrets/vault/share", "admin", map[string]any{"workspace_path": sharedSecretsTestWorkflow, "name": "PROJECT_KEY", "vault_name": "TEAM_KEY", "group_ids": []string{"platform", "finance"}}))
	if rec.Code != 409 || managedGlobals["TEAM_KEY"] != "existing" || *writes != 0 {
		t.Fatal("conflict changed value or grants")
	}
}

func TestVaultSecretSharingReportsPartialPermissionFailure(t *testing.T) {
	api := managedGlobalTestAPI(t)
	sharingTestVault(t, true)
	api.upsertSharedWorkflowSecret(context.Background(), sharedSecretsTestWorkflow, "PROJECT_KEY", "source")
	err := api.shareWorkflowSecretToVault(context.Background(), "admin", sharedSecretsTestWorkflow, "PROJECT_KEY", "TEAM_KEY", []string{"platform", "finance"})
	if err == nil || !strings.Contains(err.Error(), "copied to Vault") {
		t.Fatalf("hidden partial failure: %v", err)
	}
	source, _ := api.ensureSharedWorkflowSecrets(context.Background(), sharedSecretsTestWorkflow, "admin")
	if len(source) != 1 || managedGlobals["TEAM_KEY"] != "source" {
		t.Fatal("source or copy lost")
	}
}
