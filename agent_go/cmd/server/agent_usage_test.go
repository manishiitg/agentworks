package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentusage"
)

// Real browser/CLI authentication, a shared agent, durable usage storage,
// idempotent hourly reads, off and deletion fencing. No native session or
// person's installed collector is touched by this fixture.
func TestAgentUsageAuthenticatedLifecycle(t *testing.T) {
	env := newTriggerLinkEnv(t)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "usage-fixture-signing-secret-20261012")
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "agentworks,bullpen")
	if err := saveUserDirectory(&userDirectory{Users: []UserRecord{{ID: "owner", Username: "owner", Role: UserRoleCreator}, {ID: "other", Username: "other", Role: UserRoleCreator}}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(invalidateUserDirectoryCache)
	oauth, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer oauth.Close()
	srv := httptest.NewServer(AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/external/v1/agents/usage":
			env.api.handleAgentUsageReport(w, r)
		case "/api/bullpen/agents/usage":
			env.api.handleBullpenAgentUsage(w, r)
		case "/api/bullpen/usage":
			env.api.handleBullpenUsage(w, r)
		default:
			env.api.handleBullpenAgents(w, r)
		}
	})))
	defer srv.Close()
	t.Setenv("PUBLIC_URL", srv.URL)
	device, verification, err := oauth.CreateCLIDevice(context.Background(), "crews:run")
	if err != nil {
		t.Fatal(err)
	}
	if err = oauth.DecideCLIDevice(context.Background(), verification, &UserClaims{UserID: "owner", Username: "owner"}, true); err != nil {
		t.Fatal(err)
	}
	grant, cli, _, err := oauth.PollCLIDevice(context.Background(), device, srv.URL+"/api/external/v1")
	if err != nil {
		t.Fatal(err)
	}
	token := mcpOAuthTokenForGrant(grant)
	if err = saveAgentMessageStore(context.Background(), agentMessageStore{
		Agents: []namedMessageAgent{{ID: "own-agent", UserID: "owner", Name: "reviewer", Connections: []string{token.ID}, Attachment: &namedAgentAttachment{ID: "lease", Provider: "codex", SessionID: "private-native-id", ConnectionID: token.ID}}},
		Shares: []agentShare{{ID: "share", OwnerUserID: "owner", AgentID: "own-agent", SubjectUserID: "other", Capability: "chat", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}},
	}); err != nil {
		t.Fatal(err)
	}
	owner, _ := GenerateJWT("owner", "owner", "")
	other, _ := GenerateJWT("other", "other", "")
	call := func(method, path, auth string, data any, want int) []byte {
		t.Helper()
		b, _ := json.Marshal(data)
		req, e := http.NewRequest(method, srv.URL+path, bytes.NewReader(b))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Authorization", "Bearer "+auth)
		req.Header.Set("Content-Type", "application/json")
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var body json.RawMessage
		if e = json.NewDecoder(res.Body).Decode(&body); e != nil {
			t.Fatal(e)
		}
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d want%d: %s", method, path, res.StatusCode, want, body)
		}
		return body
	}
	x := agentusage.Reading{ID: uuid.NewString(), AgentID: "own-agent", AttachmentID: "lease", Generation: uuid.NewString(), Sequence: 1, At: time.Now().Add(-2 * time.Hour), Baseline: true, Native: agentusage.Native{Counters: agentusage.Counters{Input: 100, Output: 10, Cached: 70}, Source: "Codex session total", Model: "model"}}
	report := func(action string, want int) {
		call("POST", "/api/external/v1/agents/usage", cli, map[string]any{"action": action, "reading": x, "available": action == "sample"}, want)
	}
	call("POST", "/api/external/v1/agents/usage", owner, map[string]any{"action": "sample", "reading": x, "available": true}, 403)
	report("sample", 200)
	report("sample", 200)
	x.ID = uuid.NewString()
	x.Sequence = 2
	x.At = time.Now().Add(-time.Hour)
	x.Baseline = false
	x.Native.Counters = agentusage.Counters{Input: 120, Output: 15, Cached: 90}
	report("sample", 200)
	body := call("GET", "/api/bullpen/usage", owner, nil, 200)
	var page struct {
		Readings []agentusage.Reading `json:"readings"`
	}
	_ = json.Unmarshal(body, &page)
	if len(page.Readings) != 2 || page.Readings[0].Delta.Total() != 25 || !page.Readings[1].Baseline || page.Readings[1].Delta.Total() != 0 {
		t.Fatalf("hourly native differences/baseline: %s", body)
	}
	if b := call("GET", "/api/bullpen/agents?timezone=Asia/Kolkata", owner, nil, 200); strings.Contains(string(b), "input_tokens") || strings.Contains(string(b), "private-native-id") {
		t.Fatalf("directory must not wait for or expose usage: %s", b)
	}
	if b := call("GET", "/api/bullpen/agents/usage?timezone=Asia/Kolkata", owner, nil, 200); !strings.Contains(string(b), "Codex session total") || strings.Contains(string(b), "private-native-id") {
		t.Fatalf("owner-only current usage: %s", b)
	}
	call("GET", "/api/bullpen/agents/usage", cli, nil, 403)
	if b := call("GET", "/api/bullpen/agents/usage?user_id=owner", other, nil, 200); strings.Contains(string(b), "input_tokens") || strings.Contains(string(b), "own-agent") {
		t.Fatal("share recipient received owner current usage")
	}
	if b := call("GET", "/api/bullpen/usage?user_id=owner", other, nil, 200); strings.Contains(string(b), "own-agent") {
		t.Fatal("share recipient read owner usage")
	}
	if b := call("GET", "/api/bullpen/agents?user_id=owner", other, nil, 200); strings.Contains(string(b), "input_tokens") {
		t.Fatal("share recipient received usage field")
	}
	call("DELETE", "/api/bullpen/usage", cli, nil, 403)
	x.Native.Input = 121
	report("sample", 409)
	x.Native.Input = 120
	report("off", 200)
	x.ID = uuid.NewString()
	x.Sequence = 3
	x.At = time.Now()
	report("sample", 409)
	call("DELETE", "/api/bullpen/usage", owner, nil, 200)
	if b := call("GET", "/api/bullpen/agents/usage", owner, nil, 200); strings.Contains(string(b), "Codex session total") {
		t.Fatal("delete retained current totals/model")
	}
	x.Generation = uuid.NewString()
	x.ID = uuid.NewString()
	x.Sequence = 1
	x.Baseline = true
	x.At = time.Now().Add(-time.Minute)
	report("sample", 409)
	x.At = time.Now()
	report("sample", 200)
	db, e := openAgentUsageDB()
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	_, e = db.Exec("INSERT INTO agent_usage_readings(owner,agent,reading_id,at,input_delta,output_delta,cached_delta,body) VALUES(?,?,?,?,0,0,0,?)", "owner", "own-agent", uuid.NewString(), time.Now().Add(-91*24*time.Hour).UnixNano(), `{}`)
	if e != nil {
		t.Fatal(e)
	}
	body = call("GET", "/api/bullpen/usage", owner, nil, 200)
	_ = json.Unmarshal(body, &page)
	if len(page.Readings) != 1 || !page.Readings[0].Baseline {
		t.Fatalf("deletion/retention resurrected history: %s", body)
	}
}
