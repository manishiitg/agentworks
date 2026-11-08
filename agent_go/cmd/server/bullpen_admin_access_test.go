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

func toJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// Access rules of the Bullpen admin actions: the admin list never leaks a native
// session or lease id, a change needs a reason, a live name cannot be removed,
// changes are rate limited per admin, and a non-administrator cannot see the tool.
func TestBullpenAdminActionAccessRules(t *testing.T) {
	newTriggerLinkEnv(t) // a workspace API for the message store
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]},{"id":"root","username":"root","admin":true,"products":[]}]}`)
	api := &StreamingAPI{}
	ctx := context.Background()
	live := namedMessageAgent{ID: "agent-live", UserID: "alice", Name: "helper", Attachment: &namedAgentAttachment{DeliveryContract: 2, ID: "attach-1", Provider: "claude", SessionID: "native-session-secret"}}
	if err := saveAgentMessageStore(ctx, agentMessageStore{Agents: []namedMessageAgent{live}}); err != nil {
		t.Fatal(err)
	}
	listed, _, code, _ := api.bullpenAdminAct(ctx, "root", "list", map[string]any{})
	if code != "" || strings.Contains(strings.ToLower(toJSON(listed)), "native-session-secret") || strings.Contains(toJSON(listed), "attach-1") {
		t.Fatalf("list failed or leaked a session or lease id: %s", toJSON(listed))
	}
	handle := attachmentHandle(live.ID, live.Attachment.ID)
	if _, _, code, _ = api.bullpenAdminAct(ctx, "root", "close_attachment", map[string]any{"handle": handle}); code != "reason_required" {
		t.Fatalf("change without a reason was allowed: %q", code)
	}
	agentLiveEvents.Lock()
	agentLiveEvents.online["agent-live"] = &agentLiveStream{attachmentID: "attach-1"}
	agentLiveEvents.Unlock()
	defer func() { agentLiveEvents.Lock(); delete(agentLiveEvents.online, "agent-live"); agentLiveEvents.Unlock() }()
	if _, _, code, _ := api.bullpenAdminAct(ctx, "root", "remove_agent", map[string]any{"agent_id": "agent-live", "reason": "should refuse"}); code != "agent_live" {
		t.Fatalf("live name was removed: %q", code)
	}
	now := time.Now()
	for i := 0; i < bullpenAdminRateMax; i++ {
		bullpenAdminRateOK("flood", now)
	}
	if _, status, code, _ := api.bullpenAdminAct(ctx, "flood", "remove_agent", map[string]any{"agent_id": "none", "reason": "flooding"}); code != "rate_limited" || status != 429 {
		t.Fatalf("rate limit not applied: %d %q", status, code)
	}
	if bullpenAdminToolAllowed(&UserClaims{UserID: "alice"}) {
		t.Fatal("a non-administrator can see the admin tool")
	}
}

// Only an administrator reads admin metadata over HTTP; the owner of the agents
// is refused, anonymous is 401, and no session id, connection or token usage is
// exposed. The reads also stay usable while a message mutation is held.
func TestBullpenAdminHTTPAccessAndMetadata(t *testing.T) {
	env := newTriggerLinkEnv(t)
	t.Setenv("AUTH_SECRET", "admin-snapshot-fixture-secret")
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "agentworks,bullpen")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"operator","username":"operator","admin":true}]}`)
	if err := saveAgentMessageStore(context.Background(), agentMessageStore{Agents: []namedMessageAgent{{ID: "agent", UserID: "owner", Name: "helper", Connections: []string{"private-connection"}, Attachment: &namedAgentAttachment{ID: "lease", Provider: "codex", SessionID: "private-session"}}}}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(AuthMiddleware(http.HandlerFunc(env.api.handleBullpenAdmin)))
	defer srv.Close()
	get := func(user, path string, want int) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if user != "" {
			token, e := GenerateJWT(user, user, "")
			if e != nil {
				t.Fatal(e)
			}
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var body json.RawMessage
		if err = json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != want {
			t.Fatalf("%s: %d want %d: %s", path, res.StatusCode, want, body)
		}
		if want == http.StatusOK {
			for _, forbidden := range []string{"private-session", "private-connection", "input_tokens"} {
				if strings.Contains(string(body), forbidden) {
					t.Fatalf("metadata exposed %s", forbidden)
				}
			}
		}
	}
	func() {
		agentMessageMu.Lock()
		defer agentMessageMu.Unlock()
		for _, path := range []string{"/api/bullpen/admin/agents", "/api/bullpen/admin/audit"} {
			get("operator", path, http.StatusOK)
		}
	}()
	get("owner", "/api/bullpen/admin/agents", http.StatusForbidden)
	get("", "/api/bullpen/admin/audit", http.StatusUnauthorized)
}
