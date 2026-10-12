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

// An agent shared with everyone on the server through its owner's CLI login: every
// other person's agent can message it, a blocked person cannot, unsharing ends the
// conversation, and an administrator can make it private again.
func TestShareWithEveryoneThroughTheOwnersCLILogin(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]},{"id":"bob","username":"bob","can_create":true,"products":[]},{"id":"carol","username":"carol","can_create":true,"products":[]},{"id":"root","username":"root","admin":true,"products":[]}]}`)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "network-share-fixture-secret")
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
	send := func(c *agentworksclient.Client, name, text string) error {
		_, e := try(c, map[string]any{"action": "send", "agent_name": name, "target": "agent:alice/helper", "message": text, "submission_id": text})
		return e
	}
	if send(bob, "scout", "before sharing") == nil {
		t.Fatal("a private agent accepted a message from another person")
	}
	if _, err = bob.ShareAgent(context.Background(), "share", "helper"); err == nil {
		t.Fatal("a person shared someone else's agent")
	}
	if _, err = alice.ShareAgent(context.Background(), "share", "helper"); err != nil {
		t.Fatalf("owner could not share: %v", err)
	}
	if _, err = alice.ShareAgent(context.Background(), "share", "helper"); err != nil {
		t.Fatalf("sharing twice must be harmless: %v", err)
	}
	if err = send(bob, "scout", "hello everyone"); err != nil {
		t.Fatalf("shared agent refused a signed-in person: %v", err)
	}
	if err = send(carol, "snoop", "hello from carol"); err != nil {
		t.Fatalf("shared agent refused a second person: %v", err)
	}
	seen, err := try(alice, map[string]any{"action": "read", "agent_name": "helper"})
	if err != nil || len(seen["messages"].([]any)) != 2 {
		t.Fatalf("owner should have both messages: %v %v", seen, err)
	}
	listed, _ := try(bob, map[string]any{"action": "list_agents"})
	foundShared := false
	for _, a := range listed["agents"].([]any) {
		if m := a.(map[string]any); m["agent_name"] == "alice/helper" && m["scope"] == "everyone" {
			foundShared = true
		}
	}
	if !foundShared {
		t.Fatalf("the shared agent is not in another person's list: %v", listed)
	}
	own, _ := try(alice, map[string]any{"action": "list_agents"})
	if raw, _ := json.Marshal(own); !strings.Contains(string(raw), `"shared_with_everyone":true`) {
		t.Fatalf("the owner's list does not mark the agent as shared: %s", raw)
	}
	// The owner blocks carol: carol's messages stop, bob's do not.
	agentMessageMu.Lock()
	s, _ := readAgentMessageStore(context.Background())
	s.Blocks = append(s.Blocks, agentShareBlock{OwnerUserID: "alice", BlockedUserID: "carol", At: time.Now().UTC()})
	_ = saveAgentMessageStore(context.Background(), s)
	agentMessageMu.Unlock()
	if send(carol, "snoop", "after the block") == nil {
		t.Fatal("a blocked person still reached the shared agent")
	}
	if err = send(bob, "scout", "bob still fine"); err != nil {
		t.Fatalf("blocking one person stopped another: %v", err)
	}
	// An administrator can make it private again; the owner is told.
	listing, _, code, _ := env.api.bullpenAdminAct(context.Background(), "root", "list", map[string]any{})
	agentID := ""
	for _, person := range listing["people"].([]map[string]interface{}) {
		for _, agent := range person["agents"].([]map[string]interface{}) {
			if agent["agent_name"] == "helper" && agent["shared_with_everyone"] == true {
				agentID, _ = agent["agent_id"].(string)
			}
		}
	}
	if code != "" || agentID == "" {
		t.Fatalf("admin list does not show the shared agent: %v %s", listing, code)
	}
	if out, _, code, msg := env.api.bullpenAdminAct(context.Background(), "root", "unshare_agent", map[string]any{"agent_id": agentID, "reason": "test of admin unshare"}); code != "" || out["state"] != "unshared" {
		t.Fatalf("admin unshare: %v %s %s", out, code, msg)
	}
	if send(bob, "scout", "after admin unshare") == nil {
		t.Fatal("the agent stayed reachable after an administrator made it private")
	}
	// Share again, then the owner unshares: open conversations stop too.
	if _, err = alice.ShareAgent(context.Background(), "share", "helper"); err != nil {
		t.Fatal(err)
	}
	if err = send(bob, "scout", "shared again"); err != nil {
		t.Fatal(err)
	}
	if _, err = alice.ShareAgent(context.Background(), "unshare", "helper"); err != nil {
		t.Fatal(err)
	}
	if send(bob, "scout", "after owner unshare") == nil {
		t.Fatal("the agent stayed reachable after its owner made it private")
	}
}
