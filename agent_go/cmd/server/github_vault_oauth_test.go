package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/gitcred"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PLAT-746-158 B8. Connecting GitHub in your Vault: only the person who started a connection can finish it (a link
// sent to a colleague must not put the colleague's account into the starter's vault), a connection finishes once,
// the token is stored as the person's GITHUB_TOKEN and reaches the owner's all-apps Crews, disconnect revokes at
// GitHub, and no code, token or app secret is ever logged.
func TestGitHubVaultConnectBelongsToTheStarterAndNeverLogsCredentials(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("PUBLIC_URL", "https://agents.example.com")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","role":"creator","products":["mcp-gateway","work"]},{"id":"bob","username":"bob","role":"creator","products":["mcp-gateway","work"]}]}`)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	const token, code, appSecret = "gho_exampletoken0000", "one-time-code-1234", "app-secret-5678"
	revoked := ""
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login/oauth/access_token":
			_ = r.ParseForm()
			if r.Form.Get("client_id") != "Iv1.example" || r.Form.Get("client_secret") != appSecret || r.Form.Get("code") != code {
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "bad_verification_code"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": token, "scope": "repo,read:org,workflow", "token_type": "bearer"})
		case r.URL.Path == "/user" && r.Header.Get("Authorization") == "Bearer "+token:
			_ = json.NewEncoder(w).Encode(map[string]string{"login": "alice-gh"})
		case r.URL.Path == "/user/memberships/orgs":
			_, _ = w.Write([]byte(`[{"organization":{"login":"acme"}},{"organization":{"login":"bigcorp"}},{"organization":{"login":"samlco"}},{"organization":{"login":"slowco"}}]`))
		case r.URL.Path == "/orgs/slowco/repos":
			select { // answers after the check's budget
			case <-time.After(2 * time.Second):
			case <-r.Context().Done():
			}
		case r.URL.Path == "/orgs/acme/repos":
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/orgs/bigcorp/repos":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Although you appear to have the correct authorization credentials, the bigcorp organization has enabled OAuth App access restrictions."}`))
		case r.URL.Path == "/orgs/samlco/repos":
			w.Header().Set("X-GitHub-SSO", "required; url=https://github.com/orgs/samlco/sso?authorization_request=x")
			w.WriteHeader(http.StatusForbidden)
		case r.Method == http.MethodDelete && r.URL.Path == "/applications/Iv1.example/token":
			if user, pass, ok := r.BasicAuth(); ok && user == "Iv1.example" && pass == appSecret {
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				revoked = body["access_token"]
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(github.Close)
	oldAuthorize, oldToken, oldAPI := githubAuthorizeURL, githubTokenURL, githubAPIURL
	githubAuthorizeURL, githubTokenURL, githubAPIURL = github.URL+"/login/oauth/authorize", github.URL+"/login/oauth/access_token", github.URL
	stored := map[string]string{}
	oldStore, oldRemove, oldValue := githubStoreToken, githubRemoveToken, githubTokenValue
	githubStoreToken = func(_ context.Context, _ *StreamingAPI, person, value string) (string, string, error) {
		stored[person] = value
		return "v-1", "VLT_1234__GITHUB_TOKEN", nil
	}
	githubRemoveToken = func(_ context.Context, _ *StreamingAPI, person, _ string) error { delete(stored, person); return nil }
	githubTokenValue = func(*githubAccountRecord) string { return stored["alice"] }
	oldBudget := githubOrgCheckBudget
	githubOrgCheckBudget = 500 * time.Millisecond
	t.Cleanup(func() {
		githubOrgCheckBudget = oldBudget
		githubAuthorizeURL, githubTokenURL, githubAPIURL = oldAuthorize, oldToken, oldAPI
		githubStoreToken, githubRemoveToken, githubTokenValue = oldStore, oldRemove, oldValue
	})
	if err := writeMCPApp(githubAppKey, mcpApp{ClientID: "Iv1.example", ClientSecret: appSecret}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{logger: loggerv2.NewNoop()}
	call := func(method, path, body, user string) *httptest.ResponseRecorder {
		t.Helper()
		return personalRoute(api, (*StreamingAPI).handleGitHubVault, method, path, body, user, nil)
	}

	started := call(http.MethodPost, "/api/vault/github/connect", `{"method":"oauth"}`, "alice")
	var start struct {
		AuthURL string `json:"auth_url"`
	}
	if started.Code != http.StatusOK || json.Unmarshal(started.Body.Bytes(), &start) != nil {
		t.Fatalf("connect = %d %s", started.Code, started.Body.String())
	}
	link, _ := url.Parse(start.AuthURL)
	state := link.Query().Get("state")
	if link.Query().Get("scope") != githubOAuthScopes || link.Query().Get("redirect_uri") != "https://agents.example.com/api/oauth/callback" || state == "" {
		t.Fatalf("authorize link = %s", start.AuthURL)
	}
	back := httptest.NewRecorder()
	api.handleOAuthCallback(back, httptest.NewRequest(http.MethodGet, "/api/oauth/callback?state="+state+"&code="+code, nil))
	if back.Code != http.StatusFound || back.Header().Get("Location") != "/?github_connect="+state {
		t.Fatalf("callback = %d %s", back.Code, back.Header().Get("Location"))
	}
	complete := func(user string) *httptest.ResponseRecorder {
		return call(http.MethodPost, "/api/vault/github/complete", `{"state":"`+state+`"}`, user)
	}
	if other := complete("bob"); other.Code != http.StatusForbidden || !strings.Contains(other.Body.String(), "Sign in as alice") || len(stored) != 0 {
		t.Fatalf("a colleague finished someone else's connection: %d %s %v", other.Code, other.Body.String(), stored)
	}
	if done := complete("alice"); done.Code != http.StatusOK || !strings.Contains(done.Body.String(), "alice-gh") || stored["alice"] != token {
		t.Fatalf("completion = %d %s", done.Code, done.Body.String())
	}
	if again := complete("alice"); again.Code != http.StatusForbidden {
		t.Fatalf("a connection finished twice: %d", again.Code)
	}
	// One app serves every organisation: the reply says where the account works and which orgs still need an owner's
	// approval (restricted OAuth Apps) or single sign-on (SAML); one that GitHub is slow to answer does not hold up the
	// reply beyond the check's budget and is reported as not checked yet.
	if status := call(http.MethodGet, "/api/vault/github", "", "alice"); !strings.Contains(status.Body.String(), `"login":"alice-gh"`) || strings.Contains(status.Body.String(), token) ||
		!strings.Contains(status.Body.String(), "works in: acme, your own repositories") || !strings.Contains(status.Body.String(), "needs approval: bigcorp") || !strings.Contains(status.Body.String(), "needs single sign-on: samlco") ||
		!strings.Contains(status.Body.String(), "not checked yet: slowco") {
		t.Fatalf("status = %s", status.Body.String())
	}
	if status := call(http.MethodGet, "/api/vault/github", "", "bob"); !strings.Contains(status.Body.String(), `"connected":false`) {
		t.Fatalf("another person's status = %s", status.Body.String())
	}

	// Delivery: the owner's Crews that use all their Vault apps get the secret; a chosen-apps Crew does not. A session
	// holding it gets GH_TOKEN and git's credential helper, and nothing without it.
	if githubVaultSecretFor("alice", false) != "VLT_1234__GITHUB_TOKEN" || githubVaultSecretFor("alice", true) != "" || githubVaultSecretFor("bob", false) != "" {
		t.Fatal("GitHub secret delivery does not follow the owner's Vault app setting")
	}
	env := map[string]string{gitcred.SecretEnv: token}
	gitcred.AddGitHub(env)
	without := map[string]string{}
	gitcred.AddGitHub(without)
	if env["GH_TOKEN"] != token || env["GIT_CONFIG_KEY_0"] != "credential.https://github.com.helper" || env["GIT_CONFIG_COUNT"] != "1" || strings.Contains(env["GIT_CONFIG_VALUE_0"], token) || len(without) != 0 {
		t.Fatalf("session GitHub environment = %v", env)
	}

	if gone := call(http.MethodDelete, "/api/vault/github", "", "alice"); gone.Code != http.StatusOK || !strings.Contains(gone.Body.String(), `"revoked_at_github":true`) || revoked != token || len(stored) != 0 {
		t.Fatalf("disconnect = %d %s revoked=%t", gone.Code, gone.Body.String(), revoked == token)
	}
	for _, credential := range []string{token, code, appSecret} {
		if strings.Contains(logs.String(), credential) {
			t.Fatalf("a credential reached the log: %s", logs.String())
		}
	}
}
