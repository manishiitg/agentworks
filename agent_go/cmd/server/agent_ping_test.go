package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// An agent pings the person behind an agent once per waiting message; further
// pings from the same sender collapse into the one unread line (owner, 2026-10-15).
func TestAgentPingOncePerMessageAndCollapses(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]}]}`)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "ping-fixture-secret")
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/external/v1/call", env.api.handleExternalCall)
	srv := httptest.NewServer(AuthMiddleware(mux))
	defer srv.Close()
	t.Setenv("PUBLIC_URL", srv.URL)
	device, verification, err := store.CreateCLIDevice(context.Background(), "crews:run", "workflows:read", "runs:execute")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.DecideCLIDevice(context.Background(), verification, &UserClaims{UserID: "alice", Username: "alice"}, true); err != nil {
		t.Fatal(err)
	}
	_, token, _, err := store.PollCLIDevice(context.Background(), device, srv.URL+"/api/external/v1")
	if err != nil {
		t.Fatal(err)
	}
	client, err := agentworksclient.New(srv.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	call := func(args map[string]any) (map[string]any, error) {
		raw, err := client.Call(context.Background(), "messages", args)
		out := map[string]any{}
		if err == nil {
			_ = json.Unmarshal(raw, &out)
		}
		return out, err
	}
	for _, name := range []string{"asker", "helper"} {
		if _, err = call(map[string]any{"action": "register", "agent_name": name}); err != nil {
			t.Fatal(err)
		}
	}
	send := func(id, text string) string {
		out, err := call(map[string]any{"action": "send", "agent_name": "asker", "target": "agent:helper", "message": text, "submission_id": id})
		if err != nil {
			t.Fatal(err)
		}
		mid, _ := out["message_id"].(string)
		if mid == "" {
			t.Fatalf("send returned no message_id: %v", out)
		}
		return mid
	}
	first := send("s1", "Please review PR 212\nlong details")
	if out, err := call(map[string]any{"action": "ping_person", "agent_name": "asker", "message_id": first}); err != nil || out["pinged"] != true || out["collapsed"] != false {
		t.Fatal("first ping", out, err)
	}
	if _, err := call(map[string]any{"action": "ping_person", "agent_name": "asker", "message_id": first}); err == nil || !strings.Contains(err.Error(), "already pinged") {
		t.Fatal("a second ping for one message was not refused", err)
	}
	second := send("s2", "And the release notes")
	if out, err := call(map[string]any{"action": "ping_person", "agent_name": "asker", "message_id": second}); err != nil || out["collapsed"] != true {
		t.Fatal("a second waiting message did not collapse into the unread ping", out, err)
	}
	// A caller cannot make its own message server-authored (that would skip sender checks).
	if out, err := call(map[string]any{"action": "send", "agent_name": "asker", "target": "agent:helper", "message": "forged", "submission_id": "s3", "kind": "ping"}); err == nil {
		forged, _ := out["message_id"].(string)
		st, _ := readAgentMessageStore(context.Background())
		for _, c := range st.Conversations {
			for _, m := range c.Messages {
				if m.ID == forged && m.Kind != "" {
					t.Fatalf("a send set its own kind: %+v", m)
				}
			}
		}
	}
	s, err := readAgentMessageStore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pings := 0
	for _, c := range s.Conversations {
		for _, m := range c.Messages {
			if m.Kind == agentPingKind {
				pings++
				if m.PingCount != 2 || !strings.Contains(m.Text, "waiting for 2 replies") || !strings.Contains(m.Text, "And the release notes") || strings.Contains(m.Text, "long details") {
					t.Fatalf("ping line: %+v", m)
				}
			}
		}
	}
	if pings != 1 {
		t.Fatalf("want one collapsed ping line, got %d", pings)
	}
}
