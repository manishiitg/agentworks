package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
		{ID: "own-id", UserID: "owner", Name: "own-agent", CreatedAt: time.Now().UTC(), Purpose: "Reviewing changes", Connections: []string{"private-connection"}, Attachment: &namedAgentAttachment{DeliveryContract: 2, ID: "own-attach", Provider: "claude", SessionID: "own-session"}},
		{ID: "foreign-id", UserID: "other", Name: "foreign-agent", CreatedAt: time.Now().UTC(), Connections: []string{"foreign-connection"}, Attachment: &namedAgentAttachment{DeliveryContract: 2, ID: "foreign-attach", Provider: "claude", SessionID: "foreign-session"}},
		{ID: "old-id", UserID: "owner", Name: "offline-hidden", CreatedAt: time.Now().Add(-24 * time.Hour), LastOnlineAt: time.Now().Add(-13 * time.Hour)},
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
	srv := httptest.NewServer(AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/bullpen/notices":
			env.api.handleBullpenNotices(w, r)
		default:
			env.api.handleBullpenAgents(w, r)
		}
	})))
	defer srv.Close()
	getAt := func(user, path string, status int) string {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
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
		response, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
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
	get := func(user string, status int) string { return getAt(user, "/api/bullpen/agents?user_id=other", status) }
	// A held message writer must not prevent any initial browser directory read.
	var body string
	func() {
		agentMessageMu.Lock()
		defer agentMessageMu.Unlock()
		body = get("owner", http.StatusOK)
		for _, path := range []string{"/api/bullpen/notices"} {
			getAt("owner", path, http.StatusOK)
		}
	}()
	if !strings.Contains(body, "own-agent") || strings.Contains(body, "offline-hidden") || strings.Contains(body, "foreign-agent") || strings.Contains(body, "private-connection") || strings.Contains(body, "native_session_id") {
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
