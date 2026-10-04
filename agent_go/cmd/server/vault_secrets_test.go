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
