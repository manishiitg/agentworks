package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func withSharedSecretsForTest(t *testing.T, env []globalSecretEntry, managed map[string]string) {
	t.Helper()
	managedGlobalsMu.Lock()
	oldGlobals, oldManaged := globalSecrets, managedGlobals
	globalSecrets, managedGlobals = env, managed
	managedGlobalsMu.Unlock()
	t.Cleanup(func() {
		managedGlobalsMu.Lock()
		globalSecrets, managedGlobals = oldGlobals, oldManaged
		managedGlobalsMu.Unlock()
	})
}

// Environment-defined and managed shared secrets are registered at backend
// start without an administrator opening Vault > Secrets, as names only.
func TestStartupRegistersEnvironmentAndManagedSecretNamesWithoutValues(t *testing.T) {
	withSharedSecretsForTest(t, []globalSecretEntry{{Name: "ENV_ONE", Value: "env-secret-value-111"}}, map[string]string{"MANAGED_ONE": "managed-secret-value-222"})
	var mu sync.Mutex
	var bodies []string
	got := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/api/admin/secrets" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("s", 32) {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		mu.Lock()
		bodies = append(bodies, string(data)+" "+r.Header.Get("X-CapLayer-Actor"))
		mu.Unlock()
		w.WriteHeader(204)
		got <- struct{}{}
	}))
	defer srv.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", srv.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(io.Discard)
	startVaultSecretRegistration()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("startup did not register shared secrets")
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("expected one registration: %v", bodies)
	}
	var in struct {
		Secrets []map[string]any `json:"secrets"`
	}
	payload := strings.TrimSuffix(bodies[0], " "+vaultSecretRegistrationActor)
	if err := json.Unmarshal([]byte(payload), &in); err != nil || len(in.Secrets) != 2 {
		t.Fatal(bodies[0], err)
	}
	for _, row := range in.Secrets {
		if len(row) != 2 || row["name"] == nil || row["managed"] == nil {
			t.Fatalf("row carries more than name and managed: %v", row)
		}
	}
	for _, forbidden := range []string{"env-secret-value-111", "managed-secret-value-222"} {
		if strings.Contains(bodies[0], forbidden) || strings.Contains(logs.String(), forbidden) {
			t.Fatal("secret value leaked to Vault request or log")
		}
	}
	if !strings.Contains(bodies[0], "ENV_ONE") || !strings.Contains(bodies[0], "MANAGED_ONE") || !strings.HasSuffix(bodies[0], vaultSecretRegistrationActor) {
		t.Fatal(bodies[0])
	}
}

// Without CAPLAYER_SERVICE_URL every function behaves as before Vault existed
// and never reports "Vault is not configured" or "permissions unavailable".
func TestWithoutVaultSharedSecretsBehaveAsBeforeVault(t *testing.T) {
	t.Setenv("CAPLAYER_SERVICE_URL", "")
	t.Setenv("CAPLAYER_SERVICE_TOKEN", "")
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	t.Setenv("MULTI_USER_MODE", "false")
	withSharedSecretsForTest(t, []globalSecretEntry{{Name: "ENV_ONE", Value: "env-value"}}, map[string]string{"MANAGED_ONE": "managed-value"})
	ctx := context.Background()
	api := &StreamingAPI{}
	both := []string{"ENV_ONE", "MANAGED_ONE"}
	missing := []string{"NOPE"}
	project := []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}{{"ENV_ONE", "project-value"}}
	empty := []string{}

	if vaultConfigured() {
		t.Fatal("Vault reported as configured")
	}
	if err := syncVaultSecretMetadata(ctx, "admin"); err != nil {
		t.Fatal("sync:", err)
	}
	if err := revokeVaultSecret(ctx, "admin", "MANAGED_ONE"); err != nil {
		t.Fatal("revoke:", err)
	}
	if rows, err := permittedGlobalSecrets(ctx, "anyone"); err != nil || len(rows) != 2 {
		t.Fatal("permitted:", rows, err)
	}
	if rows := visibleGlobalSecrets(ctx, "anyone"); len(rows) != 2 {
		t.Fatal("visible:", rows)
	}
	for name, tc := range map[string]struct {
		scoped    any
		selection *[]string
		want      int
	}{
		"nil selection keeps the current behaviour (no inherited globals)": {nil, nil, 0},
		"empty selection":             {nil, &empty, 0},
		"selected globals are merged": {nil, &both, 2},
		"unknown name is skipped":     {nil, &missing, 0},
	} {
		got := api.mergeGlobalSecretsFor(ctx, "anyone", nil, tc.selection)
		if len(got) != tc.want {
			t.Errorf("%s: got %d secrets, want %d", name, len(got), tc.want)
		}
	}
	if got := api.mergeGlobalSecretsFor(ctx, "anyone", project, &both); len(got) != 2 || got[0].Value != "project-value" {
		t.Errorf("project secret must win over a shared secret of the same name: %v", got)
	}
	if err := validateVaultSecretSelection(ctx, "anyone", nil, &both); err != nil {
		t.Error("admission refused with no Vault:", err)
	}
	if err := validateVaultSecretSelection(ctx, "anyone", nil, nil); err != nil {
		t.Error(err)
	}
	if err := validateVaultSecretSelection(ctx, "anyone", nil, &missing); err == nil || strings.Contains(err.Error(), "Vault") {
		t.Error("a missing secret must still be reported as missing, never as a Vault problem:", err)
	}
	if inv, err := vaultAccessFor(ctx, "anyone"); err != nil || len(inv.Servers) != 0 || len(inv.Secrets) != 0 {
		t.Error("vaultAccessFor:", inv, err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/secrets/global", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "anyone"}))
	api.handleGetGlobalSecrets(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "ENV_ONE") || !strings.Contains(rec.Body.String(), "MANAGED_ONE") || strings.Contains(rec.Body.String(), "env-value") {
		t.Errorf("GET /api/secrets/global: %d %s", rec.Code, rec.Body.String())
	}
}

// Saving and deleting a managed secret succeeds with no Vault; the Vault calls are skipped.
func TestWithoutVaultManagedSecretSaveAndDeleteSucceed(t *testing.T) {
	api := managedGlobalTestAPI(t)
	t.Setenv("CAPLAYER_SERVICE_URL", "")
	ctx := context.Background()
	if err := api.saveManagedGlobalSecret(ctx, "admin", "MANAGED_TOKEN", "managed-value", true); err != nil {
		t.Fatal("save:", err)
	}
	if rows, err := permittedGlobalSecrets(ctx, "a1"); err != nil || len(rows) != 1 {
		t.Fatal("saved secret is not usable without Vault:", rows, err)
	}
	if err := api.deleteManagedGlobalSecret(ctx, "admin", "MANAGED_TOKEN"); err != nil {
		t.Fatal("delete:", err)
	}
}

// A configured but unusable Vault stays fail-closed.
func TestConfiguredButUnreachableVaultStillFailsClosed(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("CAPLAYER_SERVICE_URL", "http://127.0.0.1:1")
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	withSharedSecretsForTest(t, []globalSecretEntry{{Name: "ENV_ONE", Value: "env-value"}}, map[string]string{})
	names := []string{"ENV_ONE"}
	if err := validateVaultSecretSelection(context.Background(), "alice", nil, &names); err == nil {
		t.Fatal("unreachable Vault admitted the run")
	}
}
