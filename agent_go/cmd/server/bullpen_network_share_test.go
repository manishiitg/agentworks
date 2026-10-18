package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// An agent is connected (private to its person) or published to the network. A
// published agent can be messaged by anyone on the server, unpublishing ends
// access at once, an administrator can unpublish any agent, and the retired
// groups turn into published agents.
func TestPublishAgentToTheNetworkBetweenAccounts(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]},{"id":"bob","username":"bob","can_create":true,"products":[]},{"id":"carol","username":"carol","can_create":true,"products":[]},{"id":"root","username":"root","admin":true,"products":[]}]}`)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "publish-fixture-secret")
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "agentworks,bullpen")
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/external/v1/call", env.api.handleExternalCall)
	mux.HandleFunc("/api/external/v1/tools", env.api.handleExternalTools)
	mux.HandleFunc("/api/external/v1/agents/sharing", env.api.handleAgentSharing)
	srv := httptest.NewServer(AuthMiddleware(mux))
	defer srv.Close()
	t.Setenv("PUBLIC_URL", srv.URL)
	connect := func(user string) *agentworksclient.Client {
		t.Helper()
		device, verification, e := store.CreateCLIDevice(context.Background(), "crews:run", "workflows:read", "runs:execute")
		if e != nil {
			t.Fatal(e)
		}
		if e = store.DecideCLIDevice(context.Background(), verification, &UserClaims{UserID: user, Username: user}, true); e != nil {
			t.Fatal(e)
		}
		_, token, _, e := store.PollCLIDevice(context.Background(), device, srv.URL+"/api/external/v1")
		if e != nil {
			t.Fatal(e)
		}
		c, e := agentworksclient.New(srv.URL, token)
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	try := func(c *agentworksclient.Client, args map[string]any) (map[string]any, error) {
		raw, e := c.Call(context.Background(), "messages", args)
		if e != nil {
			return nil, e
		}
		var out map[string]any
		if e = json.Unmarshal(raw, &out); e != nil {
			t.Fatal(e)
		}
		return out, nil
	}
	alice, bob, carol := connect("alice"), connect("bob"), connect("carol")
	for client, name := range map[*agentworksclient.Client]string{alice: "helper", bob: "scout", carol: "snoop"} {
		if _, err = try(client, map[string]any{"action": "register", "agent_name": name}); err != nil {
			t.Fatal(err)
		}
	}
	dm := func(c *agentworksclient.Client, from, text string) error {
		_, e := try(c, map[string]any{"action": "send", "agent_name": from, "target": "agent:alice/helper", "message": text, "submission_id": text})
		return e
	}
	ctx := context.Background()
	if e := dm(bob, "scout", "before publishing"); e == nil || strings.Contains(e.Error(), "publish") {
		t.Fatalf("a private agent must refuse neutrally: %v", e)
	}
	if _, err = bob.ShareAgent(ctx, "share", "helper"); err == nil {
		t.Fatal("a person published someone else's agent")
	}
	if _, err = alice.ShareAgent(ctx, "share", "helper"); err != nil {
		t.Fatalf("publishing: %v", err)
	}
	if _, err = alice.ShareAgent(ctx, "share", "helper"); err != nil {
		t.Fatalf("publishing twice must be harmless: %v", err)
	}
	if err = dm(bob, "scout", "hello published agent"); err != nil {
		t.Fatalf("anyone on the server could not message a published agent: %v", err)
	}
	got, _ := try(alice, map[string]any{"action": "read", "agent_name": "helper"})
	first := got["messages"].([]any)[0].(map[string]any)
	if first["sender"] != "bob/scout" || first["untrusted"] != true || first["from_person"] != "bob" {
		t.Fatalf("a message from another person must be marked as theirs: %v", first)
	}
	// The sender sees where its message stands: waiting (the receiver is offline),
	// then delivered, then handled, without bodies.
	receipt, err := try(bob, map[string]any{"action": "send", "agent_name": "scout", "target": "agent:alice/helper", "message": "did you get this", "submission_id": "receipt-check"})
	if err != nil || receipt["state"] != "waiting_offline" || !strings.Contains(receipt["state_text"].(string), "offline") {
		t.Fatalf("the send result must say the receiver is offline: %v %v", receipt, err)
	}
	setDelivery := func(messageID, delivery string) {
		agentMessageMu.Lock()
		defer agentMessageMu.Unlock()
		st, _ := readAgentMessageStore(ctx)
		for ci := range st.Conversations {
			for mi := range st.Conversations[ci].Messages {
				if m := &st.Conversations[ci].Messages[mi]; m.ID == messageID {
					m.Delivery = delivery
					stampReceipt(m, delivery, time.Now().UTC())
				}
			}
		}
		_ = saveAgentMessageStore(ctx, st)
	}
	stateOf := func(messageID string) string {
		page, _ := try(bob, map[string]any{"action": "read", "agent_name": "scout"})
		sent, _ := page["sent"].([]any)
		for _, item := range sent {
			if m := item.(map[string]any); m["message_id"] == messageID {
				if m["body"] != nil || m["message"] != nil {
					t.Fatal("a sent receipt must not carry the message body")
				}
				return m["state"].(string)
			}
		}
		return "missing"
	}
	id := receipt["message_id"].(string)
	if got := stateOf(id); got != "waiting_offline" {
		t.Fatalf("sent receipt before delivery: %s", got)
	}
	setDelivery(id, "admitted")
	if got := stateOf(id); got != "delivered" {
		t.Fatalf("sent receipt after admission: %s", got)
	}
	setDelivery(id, "processed")
	if got := stateOf(id); got != "handled" {
		t.Fatalf("sent receipt after processing: %s", got)
	}
	listed, _ := try(carol, map[string]any{"action": "list_agents"})
	found := false
	for _, a := range listed["agents"].([]any) {
		if m := a.(map[string]any); m["agent_name"] == "alice/helper" && m["scope"] == "everyone" {
			found = true
		}
	}
	if !found {
		t.Fatalf("list_agents must show a published agent to everyone: %v", listed)
	}
	// Unpublishing ends access at once, in both directions.
	if _, err = alice.ShareAgent(ctx, "unshare", "helper"); err != nil {
		t.Fatal(err)
	}
	if dm(bob, "scout", "after unpublishing") == nil {
		t.Fatal("an unpublished agent could still be messaged")
	}
	// An administrator can unpublish any agent.
	if _, err = alice.ShareAgent(ctx, "share", "helper"); err != nil {
		t.Fatal(err)
	}
	listing, _, code, _ := env.api.bullpenAdminAct(ctx, "root", "list", map[string]any{})
	agentID, published := "", false
	for _, person := range listing["people"].([]map[string]interface{}) {
		for _, agent := range person["agents"].([]map[string]interface{}) {
			if agent["agent_name"] == "helper" {
				agentID, _ = agent["agent_id"].(string)
				published, _ = agent["published"].(bool)
			}
		}
	}
	if code != "" || agentID == "" || !published {
		t.Fatalf("the admin list must show the agent as published: %v %s", listing, code)
	}
	if out, _, code, msg := env.api.bullpenAdminAct(ctx, "root", "unshare_agent", map[string]any{"agent_id": agentID, "reason": "test of admin unshare"}); code != "" || out["state"] != "unshared" {
		t.Fatalf("admin unshare: %v %s %s", out, code, msg)
	}
	// The retired groups: a member becomes published, a ghost member is dropped, and the groups are gone.
	agentMessageMu.Lock()
	s, _ := readAgentMessageStore(ctx)
	var scoutID string
	for _, a := range s.Agents {
		if a.Name == "scout" {
			scoutID = a.ID
		}
	}
	s.Groups = append(s.Groups, agentGroup{ID: "group-old", Members: []groupMember{{AgentID: scoutID, UserID: "bob"}, {AgentID: "agent-gone", UserID: "bob"}}})
	_ = saveAgentMessageStore(ctx, s)
	agentMessageMu.Unlock()
	env.api.migrateGroupsToPublished(ctx)
	if s, _ = readAgentMessageSnapshot(ctx); len(s.Groups) != 0 || len(s.serverSharesOf(scoutID, time.Now().UTC())) != 1 {
		t.Fatalf("a group member must become published and the groups must go: groups=%d", len(s.Groups))
	}
	if _, err = try(carol, map[string]any{"action": "send", "agent_name": "snoop", "target": "agent:bob/scout", "message": "hello migrated agent", "submission_id": "migrated"}); err != nil {
		t.Fatalf("a migrated agent could not be messaged: %v", err)
	}
}
