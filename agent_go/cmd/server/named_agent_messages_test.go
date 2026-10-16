package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// Real authenticated HTTP, durable workspace storage and registered agent tools.
// External-to-external delivery never enters a model or uses a native socket.
func TestNamedAgentMailboxOverAuthenticatedHTTP(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "named-agent-fixture-signing-secret")
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := httptest.NewServer(AuthMiddleware(http.HandlerFunc(env.api.handleExternalCall)))
	defer server.Close()
	t.Setenv("PUBLIC_URL", server.URL)
	issue := func(user, name string, scopes []string) (*agentworksclient.Client, accesstokens.Token) {
		t.Helper()
		// Separate native CLI sign-ins are renewable independent connections.
		// Legacy PAT issuance deliberately replaces earlier account PATs.
		device, verification, e := store.CreateCLIDevice(context.Background(), scopes...)
		if e != nil {
			t.Fatal(e)
		}
		if e = store.DecideCLIDevice(context.Background(), verification, &UserClaims{UserID: user, Username: user}, true); e != nil {
			t.Fatal(e)
		}
		grant, raw, _, e := store.PollCLIDevice(context.Background(), device, server.URL+"/api/external/v1")
		if e != nil {
			t.Fatal(e)
		}
		client, e := agentworksclient.New(server.URL, raw)
		if e != nil {
			t.Fatal(e)
		}
		token := mcpOAuthTokenForGrant(grant)
		token.Name = name
		return client, token
	}
	owner, ownerToken := issue("owner", "Reviewer", []string{"crews:run", "workflows:read", "runs:execute"})
	worker, workerToken := issue("owner", "Worker", []string{"crews:run", "workflows:read", "runs:execute"})
	foreign, _ := issue("other", "Foreign", []string{"crews:run"})
	call := func(c *agentworksclient.Client, args map[string]any, wantOK bool) map[string]any {
		t.Helper()
		raw, e := c.Call(context.Background(), "messages", args)
		if !wantOK {
			if e == nil {
				t.Fatalf("accepted unauthorized call: %v", args)
			}
			return nil
		}
		if e != nil {
			t.Fatalf("%v: %v", args, e)
		}
		out := map[string]any{}
		if e = json.Unmarshal(raw, &out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	// A person's sign-in carries the full scope set (owner 2026-10-16), so there is no read-only sign-in to refuse here.
	call(owner, map[string]any{"action": "register", "agent_name": "Reviewer"}, true)
	call(worker, map[string]any{"action": "register", "agent_name": "worker"}, true)
	// A name registered by a CLI sign-in keeps receiving while its chat is closed (no attachment): only names
	// never attached and held by MCP sign-ins are refused (owner 2026-10-16).
	if n := len(call(foreign, map[string]any{"action": "list_agents"}, true)["agents"].([]any)); n != 0 {
		t.Fatal("foreign account saw directory")
	}
	call(foreign, map[string]any{"action": "send", "target": "agent:reviewer", "message": "Foreign"}, false)
	send := map[string]any{"action": "send", "agent_name": "worker", "target": "agent:reviewer", "message": "Ready for review", "submission_id": "review-one"}
	receipt := call(worker, send, true)
	inbox := receipt["inbox_id"].(string)
	call(worker, map[string]any{"action": "read", "agent_name": "reviewer"}, false)
	call(worker, map[string]any{"action": "read", "inbox_id": inbox}, false)
	call(worker, map[string]any{"action": "register", "agent_name": "reviewer"}, false)
	page := call(owner, map[string]any{"action": "read", "agent_name": "reviewer"}, true)
	items := page["messages"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["sender"] != "worker" {
		t.Fatalf("initial contact missing: %v", page)
	}
	if items[0].(map[string]any)["delivery"] != "stored" {
		t.Fatal("external delivery tried to start a model")
	}
	if again := call(worker, send, true); again["message_id"] != receipt["message_id"] {
		t.Fatal("uncertain send duplicated")
	}
	call(owner, map[string]any{"action": "send", "agent_name": "reviewer", "inbox_id": inbox, "message": "Review received"}, true)
	if n := len(call(worker, map[string]any{"action": "read", "agent_name": "worker"}, true)["messages"].([]any)); n != 1 {
		t.Fatal("named reply missing")
	}

	// Internal workflow and Crew chat tools can initiate a contact without
	// an inbox supplied by an external client; project access stays on reads.
	for _, resolve := range []func(context.Context) (triggerLinkCaller, error){workflowTriggerLinkCaller("Workflow/reports"), crewTriggerLinkCaller(linkAlphaPath)} {
		reg := &recordingRegistrar{}
		if e := env.api.registerAgentMessagingTools(reg, "owner", "sender-session", QueryRequest{}, resolve, nil); e != nil {
			t.Fatal(e)
		}
		if _, e := reg.tools["list_message_agents"].exec(context.Background(), map[string]interface{}{}); e != nil {
			t.Fatal(e)
		}
		if _, e := reg.tools["send_message"].exec(context.Background(), map[string]interface{}{"target": "agent:reviewer", "message": "Internal first contact"}); e != nil {
			t.Fatal(e)
		}
	}
	page = call(owner, map[string]any{"action": "read", "agent_name": "reviewer", "after": page["next_cursor"]}, true)
	if len(page["messages"].([]any)) != 2 {
		t.Fatalf("internal contacts missing or cursor confused: %v", page)
	}
	// Durable store reload preserves names and the global mailbox cursor.
	agentMessageMu.Lock()
	saved, e := readAgentMessageStore(context.Background())
	agentMessageMu.Unlock()
	if e != nil || len(saved.Agents) != 2 || saved.NextAgentSequence != 4 {
		t.Fatalf("durable registration/cursor: %+v %v", saved, e)
	}
	call(owner, map[string]any{"action": "register", "agent_name": "reviewer", "allowed_connection_ids": []string{workerToken.ID}}, true)
	call(worker, map[string]any{"action": "read", "agent_name": "reviewer"}, true)
	call(owner, map[string]any{"action": "register", "agent_name": "reviewer"}, true) // preserves allowlist
	call(worker, map[string]any{"action": "read", "agent_name": "reviewer"}, true)
	// Revoking a name's allowed connection also fences already queued native turns.
	workerClaims := &UserClaims{UserID: "owner", AccessToken: &workerToken}
	workerCtx := context.WithValue(context.Background(), UserContextKey, workerClaims)
	registered, err := namedAgentForClaims(workerCtx, workerClaims, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	workflow, err := resolveTriggerTarget(workerCtx, workerClaims, "workflow:Reports")
	if err != nil {
		t.Fatal(err)
	}
	queued := agentConversation{Endpoints: [2]agentMessageEndpoint{{UserID: "owner", Kind: triggerCallerUser, ID: "owner", External: true, NamedAgentID: registered.ID}, targetMessageEndpoint("owner", workflow)}}
	pending := agentMessage{From: 0, SenderConnectionID: workerToken.ID}
	if err := env.api.recheckAgentMessageAdmission(workerCtx, queued, pending); err != nil {
		t.Fatalf("authorized queued sender refused: %v", err)
	}
	call(owner, map[string]any{"action": "register", "agent_name": "reviewer", "allowed_connection_ids": []string{}}, true)
	if err := env.api.recheckAgentMessageAdmission(workerCtx, queued, pending); err == nil {
		t.Fatal("queued turn kept withdrawn named-agent authority")
	}

	call(worker, map[string]any{"action": "read", "agent_name": "reviewer"}, false)
	call(owner, map[string]any{"action": "unregister", "agent_name": "reviewer"}, true)
	call(owner, map[string]any{"action": "register", "agent_name": "reviewer"}, true)
	if n := len(call(owner, map[string]any{"action": "read", "agent_name": "reviewer"}, true)["messages"].([]any)); n != 0 {
		t.Fatal("replacement name inherited old mail")
	}
	call(owner, map[string]any{"action": "read", "agent_name": "reviewer", "inbox_id": inbox}, false)
	// A long poll must recheck the actual durable connection before release.
	claims := &UserClaims{UserID: "owner", AccessToken: &ownerToken}
	ctx := context.WithValue(context.Background(), UserContextKey, claims)
	if err := store.RevokeFamily(ctx, strings.TrimPrefix(ownerToken.ID, "oauth-"), "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.api.readNamedAgentMessages(ctx, claims, map[string]any{"agent_name": "reviewer", "wait_seconds": 1}); err == nil {
		t.Fatal("waiting reader retained revoked connection")
	}
}

// attachRegisteredAgentsForTest gives every registered name an attachment, as `agentworks attach` does:
// unattached names are hidden from lists and refuse messages.
func attachRegisteredAgentsForTest(t *testing.T) {
	t.Helper()
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	store, err := readAgentMessageStore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := range store.Agents {
		if store.Agents[i].Attachment == nil {
			store.Agents[i].Attachment = &namedAgentAttachment{DeliveryContract: 2, ID: "attach-" + store.Agents[i].Name, Provider: "claude", SessionID: "session-" + store.Agents[i].Name}
		}
	}
	if err = saveAgentMessageStore(context.Background(), store); err != nil {
		t.Fatal(err)
	}
}

// Owner decision 2026-10-16 (decisions log): AgentBridge mail needs the CLI. A plain MCP connection is refused
// agent mail, a CLI sign-in and a bound agent connection are not, and talking to a Crew stays open.
func TestAgentMailOverMCPNeedsTheCLI(t *testing.T) {
	env := newTriggerLinkEnv(t)
	code := func(token accesstokens.Token, bearer, body string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", strings.NewReader(body))
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner", AccessToken: &token}))
		rec := httptest.NewRecorder()
		env.api.handleExternalCall(rec, req)
		var out struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.Error.Code
	}
	mcp := accesstokens.Token{ID: "oauth-mcp", Name: "MCP OAuth", UserID: "owner", Scopes: []string{"crews:run"}}
	register := `{"name":"messages","arguments":{"action":"register","agent_name":"helper"}}`
	toAgent := `{"name":"messages","arguments":{"action":"send","target":"agent:helper","message":"hi","submission_id":"s1"}}`
	toCrew := `{"name":"messages","arguments":{"action":"send","crew_id":"nope","message":"hi","submission_id":"s2"}}`
	if got := code(mcp, "mcp-token", register); got != "cli_required" {
		t.Fatalf("MCP register must need the CLI, got %q", got)
	}
	if got := code(mcp, "mcp-token", toAgent); got != "cli_required" {
		t.Fatalf("MCP send to an agent must need the CLI, got %q", got)
	}
	if got := code(mcp, "mcp-token", toCrew); got == "cli_required" {
		t.Fatal("a Crew message over MCP must stay open")
	}
	cli := mcp
	cli.Name = "AgentWorks CLI"
	if got := code(cli, cliOAuthAccessPrefix+"x", register); got == "cli_required" {
		t.Fatal("a CLI sign-in must be allowed to register")
	}
	bound := mcp
	bound.BoundAgentID, bound.BoundAgentName = "agent-1", "helper"
	if got := code(bound, cliOAuthAccessPrefix+"x", `{"name":"messages","arguments":{"action":"list_agents","agent_name":"helper"}}`); got == "cli_required" {
		t.Fatal("a bound agent connection must be allowed")
	}
}
