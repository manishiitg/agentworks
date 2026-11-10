package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Existing GitHub OAuth/PAT tests now exercise an explicit, caller-owned project.
func githubProjectFixture(t *testing.T) *mockWorkspaceAPI {
	t.Helper()
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	files := map[string]string{}
	for _, person := range []string{"alice", "bob"} {
		folder := "github-" + person
		root := registerTestCrew(t, folder, person, true)
		raw, _ := json.Marshal(map[string]any{"schema_version": 1, "product": "work", "id": "crew-" + folder, "title": folder})
		files[root+"/product.json"] = string(raw)
	}
	mock := &mockWorkspaceAPI{files: files}
	workspace := httptest.NewServer(mock)
	t.Cleanup(workspace.Close)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	return mock
}

func githubTestRecord(t *testing.T, person string) *githubAccountRecord {
	t.Helper()
	rec, err := projectGitHubConnection(context.Background(), person, "Crew/github-"+person)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// PLAT-746: selecting/disconnecting GitHub in one owned Crew must not replace
// another Crew's identity or overwrite its reusable encrypted credential.
func TestGitHubProjectSelectionDoesNotChangeOtherProject(t *testing.T) {
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "1")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","role":"creator","products":["work","mcp-gateway"]},{"id":"bob","username":"bob","role":"creator","products":["work","mcp-gateway"]}]}`)
	mock := githubProjectFixture(t)
	second := registerTestCrew(t, "github-second", "alice", true)
	mock.files[second+"/product.json"] = `{"schema_version":1,"product":"work","id":"crew-github-second","title":"Second"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			login := "account-one"
			if r.Header.Get("Authorization") == "Bearer fixture-two" {
				login = "account-two"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"login": login})
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	oldAPI, oldStore, oldValue := githubAPIURL, githubStoreToken, githubTokenValue
	githubAPIURL = server.URL
	stored := map[string]string{}
	githubStoreToken = func(ctx context.Context, _ *StreamingAPI, _, value string) (string, string, error) {
		name, _ := ctx.Value(githubConnectionSecretKey{}).(string)
		if !strings.HasPrefix(name, "GITHUB_") {
			t.Fatal("missing isolated credential name")
		}
		full := "VLT_fixture__" + name
		stored[full] = value
		return "fixture", full, nil
	}
	githubTokenValue = func(rec *githubAccountRecord) string { return stored[rec.SecretName] }
	t.Cleanup(func() { githubAPIURL, githubStoreToken, githubTokenValue = oldAPI, oldStore, oldValue })
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "alice", Username: "alice"})
	firstCtx, err := githubProjectContext(ctx, "alice", "Crew/github-alice", true)
	if err != nil {
		t.Fatal(err)
	}
	secondCtx, err := githubProjectContext(ctx, "alice", second, true)
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	one, err := api.saveGitHubAccount(firstCtx, "alice", "fixture-one", "", "pat", "")
	if err != nil {
		t.Fatal(err)
	}
	two, err := api.saveGitHubAccount(secondCtx, "alice", "fixture-two", "", "pat", "")
	if err != nil {
		t.Fatal(err)
	}
	if one.ID == two.ID || one.SecretName == two.SecretName || len(stored) != 2 {
		t.Fatal("connections overwrote each other's credentials")
	}
	for _, tc := range []struct{ root, id, secret string }{{"Crew/github-alice", one.ID, one.SecretName}, {second, two.ID, two.SecretName}} {
		rec, err := projectGitHubConnection(ctx, "alice", tc.root)
		if err != nil || rec == nil || rec.ID != tc.id {
			t.Fatalf("project selected wrong account: %+v %v", rec, err)
		}
		names, err := productSelectedGlobalSecrets(ctx, crewProfileID, tc.root)
		if err != nil || names == nil || len(*names) != 1 || (*names)[0] != tc.secret {
			t.Fatalf("project delivered wrong credential names: %v %v", names, err)
		}
	}
	if _, err := githubProjectContext(ctx, "alice", "Crew/github-bob", true); err == nil {
		t.Fatal("another person's project admitted account selection")
	}
	if readGitHubConnection("bob", one.ID) != nil {
		t.Fatal("another person inherited saved credentials")
	}
	if err := selectProjectGitHubConnection(secondCtx, "alice", one.ID); err != nil {
		t.Fatal(err)
	}
	if err := updateProductSelectedGlobalSecrets(secondCtx, crewProfileID, second, func(names []string) []string { return append(names, one.SecretName) }); err != nil {
		t.Fatal(err)
	}
	if _, err := api.disconnectGitHub(secondCtx, "alice"); err != nil {
		t.Fatal(err)
	}
	if rec, err := projectGitHubConnection(ctx, "alice", second); err != nil || rec != nil {
		t.Fatal("project disconnect did not clear its selection")
	}
	if names, err := productSelectedGlobalSecrets(ctx, crewProfileID, second); err != nil || names == nil || len(*names) != 0 {
		t.Fatal("disconnect retained a materialized GitHub credential", names, err)
	}
	if rec := githubTestRecord(t, "alice"); rec == nil || rec.ID != one.ID || len(stored) != 2 {
		t.Fatal("disconnect affected other project or saved connections")
	}
	delete(stored, one.SecretName)
	if _, err := projectGitHubConnection(ctx, "alice", "Crew/github-alice"); err == nil {
		t.Fatal("missing selected credential silently fell back")
	}
}
