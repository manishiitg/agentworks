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
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	managedGlobalsMu.Lock()
	oldGlobals, oldManaged := globalSecrets, managedGlobals
	globalSecrets = []globalSecretEntry{{Name: "KEY", Value: "dummy-secret"}, {Name: "OTHER", Value: "other-dummy"}}
	managedGlobals = map[string]string{}
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
	if got := api.mergeGlobalSecretsFor(ctx, "alice", nil, nil); len(got) != 0 {
		t.Fatal("nil inherited globals")
	}
	if got := api.mergeGlobalSecretsFor(ctx, "alice", nil, &names); len(got) != 1 || got[0].Value != "dummy-secret" {
		t.Fatal("authorized explicit secret missing")
	}
	if got := api.mergeGlobalSecretsFor(ctx, "bob", nil, &names); len(got) != 0 {
		t.Fatal("cross-user exposure")
	}
	if err := validateVaultSecretSelection(ctx, "bob", nil, &names); err == nil {
		t.Fatal("run not blocked")
	}
	scoped := []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}{{"KEY", "project-value"}}
	if got := api.mergeGlobalSecretsFor(ctx, "alice", scoped, &names); len(got) != 1 || got[0].Value != "project-value" {
		t.Fatal("project precedence lost")
	}
	allowed = false
	if got := api.mergeGlobalSecretsFor(ctx, "alice", nil, &names); len(got) != 0 {
		t.Fatal("revocation ignored")
	}
	srv.Close()
	if got := api.mergeGlobalSecretsFor(ctx, "alice", nil, &names); len(got) != 0 {
		t.Fatal("service failure leaked globals")
	}
	if err := validateVaultSecretSelection(ctx, "alice", nil, &names); err == nil {
		t.Fatal("outage didn't block run")
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

func TestQuerySecretAdmissionLoadsTheExecutionWorkspace(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	withVaultSecretGrant(t, "bob") // No shared-secret grants.
	managedGlobalsMu.Lock()
	oldGlobals, oldManaged := globalSecrets, managedGlobals
	globalSecrets, managedGlobals = nil, map[string]string{}
	managedGlobalsMu.Unlock()
	t.Cleanup(func() {
		managedGlobalsMu.Lock()
		globalSecrets, managedGlobals = oldGlobals, oldManaged
		managedGlobalsMu.Unlock()
	})
	encrypted, err := encryptSecretValueWithAAD("project-test-value", []byte("alice"))
	if err != nil {
		t.Fatal(err)
	}
	w := env.do(t, env.api.handleStoreWorkflowSecret, http.MethodPut, "/api/secrets/workflow/store", "alice", storeSecretRequest{
		Name: "PROJECT_TOKEN", EncryptedValue: encrypted, WorkspacePath: "Workflow/w",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("store project secret: %d", w.Code)
	}
	names := []string{"PROJECT_TOKEN"}
	cases := []struct {
		name, mode, folder, preset string
		allowed                    bool
	}{
		{"phase preset wins over stale browser folder", "workflow_phase", "Workflow/v", "wf-w", true},
		{"headless explicit folder wins", "workflow", "Workflow/w", "wf-v", true},
		{"headless resolves absent folder from preset", "workflow", "", "wf-w", true},
		{"phase falls back when preset missing", "workflow_phase", "Workflow/w", "absent", true},
		{"other preset cannot borrow browser folder secret", "workflow_phase", "Workflow/w", "wf-v", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := QueryRequest{AgentMode: tc.mode, SelectedFolder: tc.folder, PresetQueryID: tc.preset, SelectedGlobalSecrets: &names}
			err := env.api.validateQuerySecretSelection(context.Background(), "bob", req)
			if (err == nil) != tc.allowed {
				t.Fatalf("admission allowed=%v, err=%v", tc.allowed, err)
			}
			if err != nil && !strings.Contains(err.Error(), `Secret "PROJECT_TOKEN" does not exist`) {
				t.Fatalf("missing project secret not named: %v", err)
			}
		})
	}
	// Project precedence also holds when a shared secret has the same name,
	// without admitting a shared-only secret for an ungranted user.
	managedGlobalsMu.Lock()
	globalSecrets = []globalSecretEntry{{Name: "PROJECT_TOKEN", Value: "shared-test-value"}, {Name: "SHARED_ONLY", Value: "shared-only-test-value"}}
	managedGlobalsMu.Unlock()
	req := QueryRequest{AgentMode: "workflow_phase", PresetQueryID: "wf-w", SelectedGlobalSecrets: &names}
	if err := env.api.validateQuerySecretSelection(context.Background(), "bob", req); err != nil {
		t.Fatalf("project precedence rejected: %v", err)
	}
	names = []string{"SHARED_ONLY"}
	if err := env.api.validateQuerySecretSelection(context.Background(), "bob", req); err == nil || !strings.Contains(err.Error(), `do not have access to secret "SHARED_ONLY"`) {
		t.Fatalf("ungranted shared secret not refused: %v", err)
	}
}
