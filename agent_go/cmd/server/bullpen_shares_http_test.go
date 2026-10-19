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

// Two real accounts over the authenticated external API: a grant lets the guest's
// agent message the owner's agent and get replies, a third account cannot, and
// revoking ends the conversation and suppresses what was never admitted.
func TestSharedAgentMessagingBetweenTwoAccounts(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	withMemoryUserDirectory(t, `{"users":[
		{"id":"alice","username":"alice","can_create":true,"products":[]},
		{"id":"bob","username":"bob","can_create":true,"products":[]},
		{"id":"carol","username":"carol","can_create":true,"products":[]}]}`)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "shared-agent-fixture-secret")
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/external/v1/call", env.api.handleExternalCall)
	mux.HandleFunc("/api/external/v1/tools", env.api.handleExternalTools)
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
	call := func(c *agentworksclient.Client, args map[string]any) map[string]any {
		t.Helper()
		out, e := try(c, args)
		if e != nil {
			t.Fatalf("%v: %v", args, e)
		}
		return out
	}
	alice, bob, carol := connect("alice"), connect("bob"), connect("carol")
	call(alice, map[string]any{"action": "register", "agent_name": "helper"})
	call(bob, map[string]any{"action": "register", "agent_name": "scout"})
	call(carol, map[string]any{"action": "register", "agent_name": "snoop"})
	send := func(c *agentworksclient.Client, name, target, text string) (map[string]any, error) {
		return try(c, map[string]any{"action": "send", "agent_name": name, "target": target, "message": text, "submission_id": text})
	}

	if _, err = send(bob, "scout", "agent:alice/helper", "before any grant"); err == nil {
		t.Fatal("message reached an agent that was never shared")
	}
	share, status, code, msg := env.api.createShare(context.Background(), "alice", "helper", "bob", time.Hour)
	if code != "" {
		t.Fatalf("create share: %d %s %s", status, code, msg)
	}
	if _, err = send(carol, "snoop", "agent:alice/helper", "uninvited"); err == nil {
		t.Fatal("a third account used someone else's grant")
	}
	first, err := send(bob, "scout", "agent:alice/helper", "hello from bob")
	if err != nil {
		t.Fatalf("granted send refused: %v", err)
	}
	seen := call(alice, map[string]any{"action": "read", "agent_name": "helper"})["messages"].([]any)
	if len(seen) != 1 {
		t.Fatalf("owner did not receive exactly the granted message: %v", seen)
	}
	got := seen[0].(map[string]any)
	if got["sender"] != "bob/scout" || got["from_person"] != "bob" || got["untrusted"] != true {
		t.Fatalf("receiving agent is not told this is another person's agent: %v", got)
	}
	if _, err = try(alice, map[string]any{"action": "send", "agent_name": "helper", "inbox_id": first["inbox_id"], "message": "hi bob", "submission_id": "reply-1"}); err != nil {
		t.Fatalf("owner reply refused: %v", err)
	}
	back := call(bob, map[string]any{"action": "read", "agent_name": "scout"})["messages"].([]any)
	if len(back) != 1 || back[0].(map[string]any)["sender"] != "alice/helper" {
		t.Fatalf("guest did not get the owner's labelled reply: %v", back)
	}
	listed := call(bob, map[string]any{"action": "list_agents"})["agents"].([]any)
	foundShared := false
	for _, a := range listed {
		if a.(map[string]any)["agent_name"] == "alice/helper" {
			foundShared = true
		}
	}
	for _, a := range call(carol, map[string]any{"action": "list_agents"})["agents"].([]any) {
		if strings.Contains(a.(map[string]any)["agent_name"].(string), "alice") {
			t.Fatal("an unshared account sees the owner's agent")
		}
	}
	if !foundShared {
		t.Fatal("guest does not see the shared agent in its list")
	}

	queued, err := send(bob, "scout", "agent:alice/helper", "never admitted")
	if err != nil || queued["delivery"] != "stored" {
		t.Fatalf("queued send: %v %v", queued, err)
	}
	if queued["inbox_id"] != first["inbox_id"] {
		t.Fatal("same granted agent pair split the conversation")
	}
	if status, code, msg := env.api.revokeShare(context.Background(), "alice", share["share_id"].(string)); code != "" {
		t.Fatalf("revoke: %d %s %s", status, code, msg)
	}
	if _, err = send(bob, "scout", "agent:alice/helper", "after revoke"); err == nil {
		t.Fatal("new send worked after revoke")
	}
	if _, err = try(bob, map[string]any{"action": "send", "agent_name": "scout", "inbox_id": first["inbox_id"], "message": "old inbox", "submission_id": "old-inbox"}); err == nil {
		t.Fatal("old inbox still worked after revoke")
	}
	if _, err = try(alice, map[string]any{"action": "send", "agent_name": "helper", "inbox_id": first["inbox_id"], "message": "late", "submission_id": "late-reply"}); err == nil {
		t.Fatal("owner reply worked after revoke")
	}
	if left := call(alice, map[string]any{"action": "read", "agent_name": "helper"})["messages"].([]any); len(left) != 0 {
		t.Fatalf("revoked share still delivers to the owner: %v", left)
	}
	agentMessageMu.Lock()
	final, readErr := readAgentMessageStore(context.Background())
	agentMessageMu.Unlock()
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, c := range final.Conversations {
		for _, m := range c.Messages {
			if m.Text == "never admitted" && m.Delivery != "suppressed" {
				t.Fatalf("pending message of a revoked share is %q, want suppressed", m.Delivery)
			}
		}
	}
	if _, _, code, msg := env.api.createShare(context.Background(), "alice", "helper", "bob", time.Hour); code != "" {
		t.Fatal(msg)
	}
	regranted, err := send(bob, "scout", "agent:alice/helper", "new grant")
	if err != nil || regranted["inbox_id"] == first["inbox_id"] {
		t.Fatalf("new grant reused revoked conversation: %v %v", regranted, err)
	}
}
