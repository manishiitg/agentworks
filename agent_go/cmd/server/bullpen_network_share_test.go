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

// Sharing an agent means joining a group. Members of a group see each other,
// message each other directly or the whole group, outsiders cannot, leaving ends
// access, and an administrator can take an agent out of every group.
func TestShareIntoAGroupBetweenAccounts(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]},{"id":"bob","username":"bob","can_create":true,"products":[]},{"id":"carol","username":"carol","can_create":true,"products":[]},{"id":"root","username":"root","admin":true,"products":[]}]}`)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "group-share-fixture-secret")
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
	toGroup := func(c *agentworksclient.Client, from, group, text string) (map[string]any, error) {
		return try(c, map[string]any{"action": "send", "agent_name": from, "target": "group:" + group, "message": text, "submission_id": text})
	}
	ctx := context.Background()
	if e := dm(bob, "scout", "before any group"); e == nil || strings.Contains(e.Error(), "group") {
		t.Fatalf("a private agent must refuse neutrally and not mention groups: %v", e)
	}
	if _, err = bob.ShareAgent(ctx, "share", "scout", "docs", ""); err == nil {
		t.Fatal("joined a group that does not exist without giving it a purpose")
	}
	if _, err = alice.ShareAgent(ctx, "share", "helper", "docs", "Questions about the docs"); err != nil {
		t.Fatalf("creating a group by sharing into it: %v", err)
	}
	if _, err = alice.ShareAgent(ctx, "share", "helper", "docs", ""); err != nil {
		t.Fatalf("sharing twice must be harmless: %v", err)
	}
	if _, err = bob.ShareAgent(ctx, "share", "helper", "docs", ""); err == nil {
		t.Fatal("a person put someone else's agent into a group")
	}
	// Anyone can see the group before joining it.
	seen, err := carol.ShareAgent(ctx, "group", "", "docs", "")
	if err != nil || !strings.Contains(string(seen), `"alice/helper"`) || !strings.Contains(string(seen), `"member_count":1`) {
		t.Fatalf("an outsider cannot see the group's members: %s %v", seen, err)
	}
	if e := dm(bob, "scout", "not a member yet"); e == nil || !strings.Contains(e.Error(), "/agentworks share docs") {
		t.Fatalf("a non-member must be told which group to join: %v", e)
	}
	if _, err = try(bob, map[string]any{"action": "join_group", "agent_name": "scout", "group": "docs"}); err != nil {
		t.Fatalf("join through the messages tool: %v", err)
	}
	if err = dm(bob, "scout", "hello member"); err != nil {
		t.Fatalf("members could not message each other: %v", err)
	}
	got, _ := try(alice, map[string]any{"action": "read", "agent_name": "helper"})
	first := got["messages"].([]any)[0].(map[string]any)
	if first["sender"] != "bob/scout" || first["untrusted"] != true || first["from_person"] != "bob" {
		t.Fatalf("a message from a group member must be marked as another person's: %v", first)
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
	if _, err = toGroup(carol, "snoop", "docs", "outsider to the group"); err == nil {
		t.Fatal("a non-member sent to the group")
	}
	if _, err = try(carol, map[string]any{"action": "join_group", "agent_name": "snoop", "group": "docs"}); err != nil {
		t.Fatal(err)
	}
	result, err := toGroup(bob, "scout", "docs", "to everyone in docs")
	if err != nil || len(result["sent_to"].([]any)) != 2 {
		t.Fatalf("group message must reach both other members: %v %v", result, err)
	}
	carolGot, _ := try(carol, map[string]any{"action": "read", "agent_name": "snoop"})
	var sawGroup bool
	for _, item := range carolGot["messages"].([]any) {
		if m := item.(map[string]any); m["group"] == "docs" && m["sender"] == "bob/scout" {
			sawGroup = true
		}
	}
	if !sawGroup {
		t.Fatalf("the group message did not reach another member with its group name: %v", carolGot)
	}
	listed, _ := try(bob, map[string]any{"action": "list_agents"})
	found := false
	for _, a := range listed["agents"].([]any) {
		if m := a.(map[string]any); m["agent_name"] == "alice/helper" && m["scope"] == "group" {
			found = true
		}
	}
	if !found || len(listed["groups"].([]any)) != 1 {
		t.Fatalf("list_agents must show group co-members and the groups: %v", listed)
	}
	// Leaving ends access at once.
	if _, err = bob.ShareAgent(ctx, "unshare", "scout", "docs", ""); err != nil {
		t.Fatal(err)
	}
	if dm(bob, "scout", "after leaving") == nil {
		t.Fatal("a member who left could still message the group's agents")
	}
	if _, err = try(alice, map[string]any{"action": "send", "agent_name": "helper", "target": "agent:bob/scout", "message": "reply after leaving", "submission_id": "reply-after-leaving"}); err == nil {
		t.Fatal("a group member could still message someone who left")
	}
	// An administrator takes an agent out of every group.
	listing, _, code, _ := env.api.bullpenAdminAct(ctx, "root", "list", map[string]any{})
	agentID := ""
	for _, person := range listing["people"].([]map[string]interface{}) {
		for _, agent := range person["agents"].([]map[string]interface{}) {
			if agent["agent_name"] == "helper" {
				agentID, _ = agent["agent_id"].(string)
			}
		}
	}
	if code != "" || agentID == "" || len(listing["groups"].([]map[string]interface{})) != 1 {
		t.Fatalf("the admin list must show agents and groups: %v %s", listing, code)
	}
	if out, _, code, msg := env.api.bullpenAdminAct(ctx, "root", "unshare_agent", map[string]any{"agent_id": agentID, "reason": "test of admin unshare"}); code != "" || out["state"] != "unshared" {
		t.Fatalf("admin unshare: %v %s %s", out, code, msg)
	}
	// Earlier everyone-shares move into the default group, so nothing breaks.
	agentMessageMu.Lock()
	s, _ := readAgentMessageStore(ctx)
	for _, a := range s.Agents {
		if a.Name == "scout" {
			s.Shares = append(s.Shares, agentShare{ID: "share-old", OwnerUserID: a.UserID, AgentID: a.ID, SubjectUserID: shareScopeServer, Capability: shareCapabilityChat, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(serverShareLifetime), RevocationVersion: 1})
		}
	}
	_ = saveAgentMessageStore(ctx, s)
	agentMessageMu.Unlock()
	// An orphaned old grant (its agent is gone) and a ghost member are dropped, not shown.
	agentMessageMu.Lock()
	s, _ = readAgentMessageStore(ctx)
	s.Shares = append(s.Shares, agentShare{ID: "share-orphan", OwnerUserID: "bob", AgentID: "agent-gone", SubjectUserID: shareScopeServer, Capability: shareCapabilityChat, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(serverShareLifetime), RevocationVersion: 1})
	s.Groups = append(s.Groups, agentGroup{ID: "group-ghost", Name: "ghosts", Purpose: "left behind", Members: []groupMember{{AgentID: "agent-gone", UserID: "bob"}}})
	_ = saveAgentMessageStore(ctx, s)
	agentMessageMu.Unlock()
	env.api.migrateEveryoneShares(ctx)
	if ghosts, e := carol.ShareAgent(ctx, "groups", "", "", ""); e != nil || strings.Contains(string(ghosts), `"ghosts"`) || strings.Contains(string(ghosts), "agent-gone") {
		t.Fatalf("a group of ghost members must be gone: %s %v", ghosts, e)
	}
	general, err := carol.ShareAgent(ctx, "group", "", defaultGroupName, "")
	if err != nil || !strings.Contains(string(general), `"bob/scout"`) {
		t.Fatalf("an old everyone-share must become a member of the default group: %s %v", general, err)
	}
}
