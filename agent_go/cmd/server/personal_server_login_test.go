package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

// Moving a Mac's Codex login to the server carries only a ChatGPT sign-in, in the model proxy's own format; an API-key
// login has nothing to renew and is refused (PLAT-856-441).
func TestCodexProxyLoginConversion(t *testing.T) {
	if _, _, err := codexProxyLogin([]byte(`{"OPENAI_API_KEY":"sk-example","tokens":null}`)); err == nil {
		t.Fatal("an API-key login was accepted")
	}
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"person@example.com"}`))
	idToken := "e30." + claims + ".sig"
	stored, email, err := codexProxyLogin([]byte(`{"tokens":{"id_token":"` + idToken + `","access_token":"e30.e30.sig","refresh_token":"rt","account_id":"acct"}}`))
	if err != nil || email != "person@example.com" {
		t.Fatalf("email %q, err %v", email, err)
	}
	var saved map[string]string
	if json.Unmarshal(stored, &saved) != nil || saved["type"] != "codex" || saved["refresh_token"] != "rt" || saved["email"] != "person@example.com" {
		t.Fatalf("saved login %v", saved)
	}
}
