package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// PLAT-746-191: every method saves exactly the caller's selected identity;
// tokens never reach a response/log, and hosted gh cannot read the operator.
func TestGitHubVaultPATAndCLIUseTheSameOwnerRecord(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "1")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","role":"creator","products":["mcp-gateway","work"]},{"id":"bob","username":"bob","role":"creator","products":["mcp-gateway","work"]}]}`)
	const pat, cliToken = "github_pat_fixture", "gho_fixture"
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			login := "alice-pat"
			if r.Header.Get("Authorization") == "Bearer "+cliToken {
				login = "alice-cli"
			}
			if r.Header.Get("Authorization") == "Bearer rejected-fixture" {
				w.WriteHeader(401)
				_, _ = w.Write([]byte(`{"message":"rejected-fixture"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"login": login})
		default:
			w.WriteHeader(403) // no scopes/org access must not prevent connecting
		}
	}))
	t.Cleanup(github.Close)
	oldAPI, oldStore, oldRemove, oldValue, oldCLI := githubAPIURL, githubStoreToken, githubRemoveToken, githubTokenValue, githubReadCLIToken
	githubAPIURL = github.URL
	stored := map[string]string{}
	githubStoreToken = func(_ context.Context, _ *StreamingAPI, person, value string) (string, string, error) {
		stored[person] = value
		return "v-1", "VLT_1234__GITHUB_TOKEN", nil
	}
	githubRemoveToken = func(_ context.Context, _ *StreamingAPI, person, _ string) error { delete(stored, person); return nil }
	githubTokenValue = func(*githubAccountRecord) string { return stored["alice"] }
	cliReads := 0
	githubReadCLIToken = func(context.Context) (string, error) { cliReads++; return cliToken, nil }
	t.Cleanup(func() {
		githubAPIURL = oldAPI
		githubStoreToken = oldStore
		githubRemoveToken = oldRemove
		githubTokenValue = oldValue
		githubReadCLIToken = oldCLI
	})
	api := &StreamingAPI{}
	call := func(method, body string) *httptest.ResponseRecorder {
		return personalRoute(api, (*StreamingAPI).handleGitHubVault, method, "/api/vault/github/connect", body, "alice", nil)
	}
	// A missing OAuth app cannot block a PAT. It is not stored for a colleague.
	result := call(http.MethodPost, `{"method":"pat","token":"`+pat+`"}`)
	if result.Code != 200 || stored["alice"] != pat || readGitHubRecord("bob") != nil {
		t.Fatalf("PAT connection failed: %d %s", result.Code, result.Body.String())
	}
	if rec := readGitHubRecord("alice"); rec == nil || rec.Source != "pat" || rec.Login != "alice-pat" || githubVaultSecretFor("alice", true) != "" {
		t.Fatal("PAT did not use the existing owner record/grant model")
	}
	for _, settings := range []struct{ mode, multi, local, owner string }{{"server", "false", "true", "alice"}, {"local", "true", "true", "alice"}, {"local", "false", "true", "bob"}} {
		t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", settings.mode)
		t.Setenv("MULTI_USER_MODE", settings.multi)
		t.Setenv("LOCAL_MODE", settings.local)
		t.Setenv("DEFAULT_USER_ID", settings.owner)
		if denied := call(http.MethodPost, `{"method":"gh"}`); denied.Code == 200 {
			t.Fatal("used another person's OS login")
		}
	}
	if cliReads != 0 {
		t.Fatal("read gh credentials before caller eligibility")
	}
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("DEFAULT_USER_ID", "alice")
	result = call(http.MethodPost, `{"method":"gh"}`)
	if result.Code != 200 || cliReads != 1 || stored["alice"] != cliToken {
		t.Fatalf("local gh import failed: %d %s", result.Code, result.Body.String())
	}
	if rec := readGitHubRecord("alice"); rec == nil || rec.Source != "gh" || rec.Login != "alice-cli" {
		t.Fatal("CLI identity did not replace the selected record")
	}
	gone := call(http.MethodDelete, "")
	if gone.Code != 200 || readGitHubRecord("alice") != nil || len(stored) != 0 || !strings.Contains(gone.Body.String(), "gh login is unchanged") {
		t.Fatalf("disconnect changed native identity: %d %s", gone.Code, gone.Body.String())
	}
	githubReadCLIToken = func(context.Context) (string, error) { return "", errors.New(cliToken) }
	failed := call(http.MethodPost, `{"method":"gh"}`)
	rejected := call(http.MethodPost, `{"method":"pat","token":"rejected-fixture"}`)
	for _, value := range []string{pat, cliToken, "rejected-fixture"} {
		if strings.Contains(result.Body.String()+gone.Body.String()+failed.Body.String()+rejected.Body.String()+logs.String(), value) {
			t.Fatal("credential exposed in reply or log")
		}
	}
}

func TestGitHubCLIEnvironmentDoesNotBorrowInjectedTokens(t *testing.T) {
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_DEBUG"} {
		t.Setenv(key, "fixture-secret")
	}
	for _, value := range githubCLIEnvironment() {
		if strings.Contains(value, "fixture-secret") {
			t.Fatal("inherited GitHub credential/debug environment reached gh")
		}
	}
}
