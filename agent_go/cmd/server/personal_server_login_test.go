package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// The placeholder is the only guard on the public relay route (PLAT-856-441 review): a wrong key is refused, the
// account's own key gets through, and after Sign out the old key is refused at once.
func TestPersonalModelProxyPlaceholderGuard(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	ctx := context.Background()
	placeholder, err := newServerLoginPlaceholder()
	if err != nil {
		t.Fatal(err)
	}
	record := storedProviderConnection{ProviderConnection: ProviderConnection{ID: "acct-server-login", Provider: "codex-cli", DisplayName: "My ChatGPT", OwnerUserID: "alice", Scope: "user", AuthMethod: providerAuthServerLogin, UpdatedAt: time.Now().UTC()}, Credential: placeholder}
	providerConnectionsMu.Lock()
	err = saveProviderConnections(ctx, []storedProviderConnection{record})
	providerConnectionsMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	relay := func(key string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/personal-model-proxy/acct-server-login/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		req = mux.SetURLVars(req, map[string]string{"account": record.ID, "path": "models"})
		w := httptest.NewRecorder()
		env.api.handlePersonalModelProxy(w, req)
		return w.Code
	}
	if code := relay("sk-agentworks-wrong"); code != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d, want 401", code)
	}
	// Past the guard; no proxy program in tests, so the relay reports it did not start.
	if code := relay(placeholder); code == http.StatusUnauthorized {
		t.Fatalf("own key refused")
	}
	if w := env.do(t, env.api.handleProviderAccountSignOut, http.MethodPost, "/api/provider-connections/"+record.ID+"/sign-out", "alice", nil, map[string]string{"connectionID": record.ID}); w.Code != http.StatusOK {
		t.Fatalf("sign out: %d %s", w.Code, w.Body.String())
	}
	if code := relay(placeholder); code != http.StatusUnauthorized {
		t.Fatalf("old key after sign-out: %d, want 401", code)
	}
}
