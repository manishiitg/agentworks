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
	mux.HandleFunc("/api/external/v1/agents/events", env.api.handleAgentEvents)
	mux.HandleFunc("/api/external/v1/agents/attachment", env.api.handleAgentAttachment)
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
	if err != nil || receipt["state"] != "waiting_offline" {
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
				if m["body"] != nil || m["message"] == "did you get this" {
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
	// Discovery expires after 12 hours, while the stable address and inbox keep
	// working. Neither received mail nor directory reads refresh the recipient.
	ageHelper := func() string {
		t.Helper()
		agentMessageMu.Lock()
		defer agentMessageMu.Unlock()
		st, e := readAgentMessageStore(ctx)
		if e != nil {
			t.Fatal(e)
		}
		id := ""
		for i := range st.Agents {
			if st.Agents[i].UserID == "alice" && st.Agents[i].Name == "helper" {
				st.Agents[i].CreatedAt = time.Now().Add(-24 * time.Hour)
				st.Agents[i].LastOnlineAt = time.Now().Add(-13 * time.Hour)
				id = st.Agents[i].ID
			}
		}
		if id == "" {
			t.Fatal("registered helper missing")
		}
		if e = saveAgentMessageStore(ctx, st); e != nil {
			t.Fatal(e)
		}
		agentLiveEvents.Lock()
		delete(agentLiveEvents.lastRecorded, id) // move the coalescing clock with the aged record
		agentLiveEvents.Unlock()
		return id
	}
	listedHelper := func(client *agentworksclient.Client, expected bool) {
		t.Helper()
		page, e := try(client, map[string]any{"action": "list_agents"})
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, row := range page["agents"].([]any) {
			name := row.(map[string]any)["agent_name"]
			found = found || name == "helper" || name == "alice/helper"
		}
		if found != expected {
			t.Fatalf("helper discovery=%t, want %t", found, expected)
		}
	}
	stableID := ageHelper()
	listedHelper(alice, false)
	listedHelper(carol, false)
	queued, e := try(bob, map[string]any{"action": "send", "agent_name": "scout", "target": "agent:alice/helper", "message": "mail while hidden", "submission_id": "hidden-mail"})
	if e != nil || queued["delivery"] != "stored" || queued["inbox_id"] != receipt["inbox_id"] {
		t.Fatalf("hidden agent lost its original inbox: %v %v", queued, e)
	}
	listedHelper(alice, false)
	listedHelper(carol, false)
	if _, e = try(alice, map[string]any{"action": "read", "agent_name": "helper"}); e != nil {
		t.Fatal(e)
	}
	listedHelper(alice, true) // authenticated mailbox use counts, not inbound mail
	listedHelper(carol, true)
	if _, e = try(alice, map[string]any{"action": "register", "agent_name": "companion"}); e != nil {
		t.Fatal(e)
	}
	ageHelper()
	if _, e = try(alice, map[string]any{"action": "send", "agent_name": "helper", "target": "agent:companion", "message": "mailbox sends while hidden", "submission_id": "hidden-sender"}); e != nil {
		t.Fatal(e)
	}
	listedHelper(carol, true)
	ageHelper()
	attachment, e := alice.AttachAgent(ctx, agentworksclient.AgentAttachmentOptions{AgentName: "helper", Provider: "codex", SessionID: "exact-helper-session"})
	if e != nil || attachment.AgentID != stableID {
		t.Fatalf("reattach changed the stable name: %v %v", attachment, e)
	}
	listedHelper(carol, false) // an attachment alone is not a live listener
	liveCtx, disconnect := context.WithCancel(ctx)
	defer disconnect()
	connected, streamed, finished := make(chan struct{}), make(chan string, 20), make(chan error, 1)
	go func() {
		finished <- alice.StreamAgentMessages(liveCtx, attachment, func(m agentworksclient.AgentLiveMessage) error {
			streamed <- m.MessageID
			return nil
		}, func() { close(connected) })
	}()
	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("exact helper stream did not reconnect")
	}
	listedHelper(alice, true)
	listedHelper(carol, true)
	for {
		select {
		case id := <-streamed:
			if id == queued["message_id"] {
				disconnect()
				<-finished
				goto restored
			}
		case <-time.After(5 * time.Second):
			t.Fatal("reconnected agent did not receive mail stored while hidden")
		}
	}
restored:
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
	// A helper older than the network model prints its own words, so the reply carries them too.
	if out, _, code, _ := env.api.publishAction(ctx, "alice", "share", "helper", true); code != "" || out["state"] != "joined" || out["name"] != "network" {
		t.Fatalf("an old helper must still be told the agent joined: %v %s", out, code)
	}

}
