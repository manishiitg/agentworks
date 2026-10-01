package server

import (
	"context"
	"encoding/json"
	"github.com/gorilla/mux"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderConnectionsLifecycleAndAuthorization(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AUTH_SECRET", "account-test-only-secret")
	t.Setenv("LLM_CONFIG_LOCKED", "")
	t.Setenv("ALLOW_PERSONAL_PROVIDER_CONNECTIONS", "")
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "codex-cli,muse-cli")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]},{"id":"bob","username":"bob","can_create":true,"products":[]}]}`)
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	api := &StreamingAPI{}
	create := func(user, name, key string) ProviderConnection {
		w := httptest.NewRecorder()
		api.handleProviderConnections(w, sharedSecretsRequest("POST", "/", user, map[string]string{"provider": "codex-cli", "display_name": name, "credential": key}))
		if w.Code != 201 {
			t.Fatalf("create status %d: %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), key) {
			t.Fatal("credential exposed")
		}
		var result ProviderConnection
		if json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal("invalid metadata")
		}
		return result
	}
	a := create("alice", "Account A", "private-key-A")
	b := create("alice", "Account B", "private-key-B")
	if a.ID == b.ID {
		t.Fatal("duplicate account ID")
	}
	mock.mu.Lock()
	stored := mock.files[providerConnectionsPath]
	mock.mu.Unlock()
	if stored == "" || strings.Contains(stored, "private-key") {
		t.Fatal("registry was not encrypted")
	}
	keysA, err := api.connectionAPIKeys(context.Background(), providerAccountScope{Principal: "alice"}, "codex-cli", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	keysB, err := api.connectionAPIKeys(context.Background(), providerAccountScope{Principal: "alice"}, "codex-cli", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if *keysA.CodexCLI != "private-key-A" || *keysB.CodexCLI != "private-key-B" {
		t.Fatal("wrong credential resolved")
	}
	if keysA.RuntimeEnvironment["HOME"] == keysB.RuntimeEnvironment["HOME"] {
		t.Fatal("accounts share a runtime home")
	}
	config, err := os.ReadFile(filepath.Join(keysB.RuntimeEnvironment["CODEX_HOME"], "config.toml"))
	if err != nil || !strings.Contains(string(config), `cli_auth_credentials_store = "file"`) {
		t.Fatal("Codex account does not use file credentials")
	}
	for _, attempt := range []struct{ user, provider, id string }{{"bob", "codex-cli", b.ID}, {"", "codex-cli", b.ID}, {"alice", "muse-cli", b.ID}, {"alice", "codex-cli", "missing"}, {"alice", "codex-cli", "global:muse-cli"}} {
		if _, err := api.connectionAPIKeys(context.Background(), providerAccountScope{Principal: attempt.user}, attempt.provider, attempt.id); err == nil {
			t.Fatalf("unauthorized binding accepted: %+v", attempt)
		}
	}
	w := httptest.NewRecorder()
	api.handleProviderConnections(w, sharedSecretsRequest("GET", "/", "bob", nil))
	if strings.Contains(w.Body.String(), a.ID) || strings.Contains(w.Body.String(), b.ID) {
		t.Fatal("another user's accounts exposed")
	}
	req := mux.SetURLVars(sharedSecretsRequest("DELETE", "/", "bob", nil), map[string]string{"connectionID": b.ID})
	w = httptest.NewRecorder()
	api.handleProviderConnection(w, req)
	if w.Code != 404 {
		t.Fatal("other user deleted account")
	}
	req = mux.SetURLVars(sharedSecretsRequest("PATCH", "/", "alice", map[string]string{"display_name": "Renamed B", "credential": "rotated-B"}), map[string]string{"connectionID": b.ID})
	w = httptest.NewRecorder()
	api.handleProviderConnection(w, req)
	if w.Code != 204 {
		t.Fatalf("rotate status %d", w.Code)
	}
	rotated, err := api.connectionAPIKeys(context.Background(), providerAccountScope{Principal: "alice"}, "codex-cli", b.ID)
	if err != nil || *rotated.CodexCLI != "rotated-B" || rotated.RuntimeEnvironment["HOME"] != keysB.RuntimeEnvironment["HOME"] {
		t.Fatal("rotation changed identity or failed to update credential")
	}
	req = mux.SetURLVars(sharedSecretsRequest("DELETE", "/", "alice", nil), map[string]string{"connectionID": b.ID})
	w = httptest.NewRecorder()
	api.handleProviderConnection(w, req)
	if w.Code != 204 {
		t.Fatalf("delete status %d", w.Code)
	}
	if _, err := api.connectionAPIKeys(context.Background(), providerAccountScope{Principal: "alice"}, "codex-cli", b.ID); err == nil {
		t.Fatal("deleted account fell back to global credentials")
	}
	t.Setenv("LLM_CONFIG_LOCKED", "codex-cli")
	if _, err := api.connectionAPIKeys(context.Background(), providerAccountScope{Principal: "alice"}, "codex-cli", a.ID); err == nil {
		t.Fatal("administrator lock bypassed")
	}
	t.Setenv("ALLOW_PERSONAL_PROVIDER_CONNECTIONS", "true")
	if _, err := api.connectionAPIKeys(context.Background(), providerAccountScope{Principal: "alice"}, "codex-cli", a.ID); err != nil {
		t.Fatal("explicit personal-account opt-in ignored")
	}
}

func TestProviderConnectionSetupEnvironmentIsAccountScoped(t *testing.T) {
	t.Setenv("CODEX_API_KEY", "global-codex-key")
	t.Setenv("META_API_KEY", "global-muse-key")
	keys, err := connectionCredentialKeys(storedProviderConnection{ProviderConnection: ProviderConnection{Provider: "codex-cli", AuthMethod: "cli_login"}})
	if err != nil {
		t.Fatal(err)
	}
	keys.RuntimeEnvironment = map[string]string{"HOME": "/account-B/home", "CODEX_HOME": "/account-B/codex"}
	env := strings.Join(providerConnectionSetupEnvironment("test", keys), "\n")
	if strings.Contains(env, "global-codex-key") || strings.Contains(env, "global-muse-key") || !strings.Contains(env, "CODEX_HOME=/account-B/codex") {
		t.Fatal("setup inherited a global identity or lost account paths")
	}
}

// A person who added their own account for a provider uses it by default in their Crew or Code;
// another person's account, another provider's, or one known not signed in is never picked.
func TestOwnDefaultProviderAccountID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AUTH_SECRET", "account-test-only-secret")
	t.Setenv("LLM_CONFIG_LOCKED", "")
	t.Setenv("ALLOW_PERSONAL_PROVIDER_CONNECTIONS", "")
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "codex-cli,muse-cli")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]},{"id":"bob","username":"bob","can_create":true,"products":[]}]}`)
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	api := &StreamingAPI{}
	create := func(user, provider, name string) ProviderConnection {
		w := httptest.NewRecorder()
		api.handleProviderConnections(w, sharedSecretsRequest("POST", "/", user, map[string]string{"provider": provider, "display_name": name, "credential": "k-" + name}))
		if w.Code != 201 {
			t.Fatalf("create status %d: %s", w.Code, w.Body.String())
		}
		var result ProviderConnection
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return result
	}
	ctx := context.Background()
	if got := ownDefaultProviderAccountID(ctx, "alice", "codex-cli"); got != "" {
		t.Fatalf("no account yet, got %q", got)
	}
	bobs := create("bob", "codex-cli", "bob")
	create("alice", "muse-cli", "alice-muse")
	if got := ownDefaultProviderAccountID(ctx, "alice", "codex-cli"); got != "" {
		t.Fatalf("picked someone else's or another provider's account: %q", got)
	}
	mine := create("alice", "codex-cli", "alice-codex")
	if got := ownDefaultProviderAccountID(ctx, "alice", "codex-cli"); got != mine.ID {
		t.Fatalf("own account not picked: %q want %q", got, mine.ID)
	}
	if got := ownDefaultProviderAccountID(ctx, "bob", "codex-cli"); got != bobs.ID {
		t.Fatalf("bob gets his own: %q", got)
	}
	rememberProviderAccountStatus(mine.ID, providerAccountStatus{State: "signed_out"})
	if configured, ok := cachedProviderAccountConfigured(mine.ID); ok && configured != nil && !*configured {
		if got := ownDefaultProviderAccountID(ctx, "alice", "codex-cli"); got != "" {
			t.Fatalf("picked an account known not signed in: %q", got)
		}
	}
}
