package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Real signed-in HTTP and the durable directory. URL parameters cannot choose
// another account; the product page never returns reader grants or session IDs.
func TestBullpenAccountDirectoryOverAuthenticatedHTTP(t *testing.T) {
	env := newTriggerLinkEnv(t)
	t.Setenv("AUTH_SECRET", "bullpen-fixture-signing-secret-20261012")
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "agentworks,bullpen")
	ctx := context.Background()
	if err := saveAgentMessageStore(ctx, agentMessageStore{Agents: []namedMessageAgent{
		{ID: "own-id", UserID: "owner", Name: "own-agent", Purpose: "Reviewing changes", Connections: []string{"private-connection"}},
		{ID: "foreign-id", UserID: "other", Name: "foreign-agent", Connections: []string{"foreign-connection"}},
	}}); err != nil {
		t.Fatal(err)
	}
	// Real durable accounts are required by the current browser sign-in gate.
	if err := saveUserDirectory(&userDirectory{Users: []UserRecord{
		{ID: "owner", Username: "owner", Role: UserRoleCreator},
		{ID: "other", Username: "other", Role: UserRoleCreator},
		{ID: "denied", Username: "denied", Role: UserRoleCreator, Products: []string{"agentworks"}},
	}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(invalidateUserDirectoryCache)
	srv := httptest.NewServer(AuthMiddleware(http.HandlerFunc(env.api.handleBullpenAgents)))
	defer srv.Close()
	get := func(user string, status int) string {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/bullpen/agents?user_id=other", nil)
		if err != nil {
			t.Fatal(err)
		}
		if user != "" {
			token, err := GenerateJWT(user, user, "")
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var body json.RawMessage
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != status {
			t.Fatalf("%s: status %d want %d: %s", user, response.StatusCode, status, body)
		}
		if status == http.StatusOK && response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("account directory must not be cached")
		}
		return string(body)
	}
	body := get("owner", http.StatusOK)
	if !strings.Contains(body, "own-agent") || strings.Contains(body, "foreign-agent") || strings.Contains(body, "private-connection") || strings.Contains(body, "native_session_id") {
		t.Fatalf("directory ownership or metadata leak: %s", body)
	}
	if body = get("other", http.StatusOK); !strings.Contains(body, "foreign-agent") || strings.Contains(body, "own-agent") {
		t.Fatalf("foreign directory crossed account: %s", body)
	}
	get("", http.StatusUnauthorized)
	get("denied", http.StatusForbidden)
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "agentworks")
	get("owner", http.StatusForbidden)
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "")
	get("owner", http.StatusForbidden)
}
