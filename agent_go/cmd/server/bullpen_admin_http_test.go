package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// An administrator closing an attached chat ends its live stream, fences its old
// lease (a receipt with it is refused), and leaves the owner free to attach again
// with a new lease that the old handle can no longer close.
func TestBullpenAdminCloseEndsStreamAndOwnerCanReattach(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true,"products":[]},{"id":"root","username":"root","admin":true,"products":[]}]}`)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "admin-close-fixture-secret")
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/external/v1/call", env.api.handleExternalCall)
	mux.HandleFunc("/api/external/v1/tools", env.api.handleExternalTools)
	mux.HandleFunc("/api/external/v1/agents/events", env.api.handleAgentEvents)
	mux.HandleFunc("/api/external/v1/agents/attachment", env.api.handleAgentAttachment)
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
	owner, err := agentworksclient.New(srv.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err = owner.Call(ctx, "messages", map[string]any{"action": "register", "agent_name": "helper"}); err != nil {
		t.Fatal(err)
	}
	attachment, err := owner.AttachAgent(ctx, agentworksclient.AgentAttachmentOptions{AgentName: "helper", Provider: "claude", SessionID: "exact-session-one"})
	if err != nil {
		t.Fatal(err)
	}
	streamCtx, stop := context.WithCancel(ctx)
	defer stop()
	ended := make(chan error, 1)
	go func() {
		ended <- owner.StreamAgentMessages(streamCtx, attachment, func(agentworksclient.AgentLiveMessage) error { return nil })
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		agentLiveEvents.Lock()
		active := agentLiveEvents.online[attachment.AgentID]
		online := active != nil && active.attachmentID == attachment.ID
		agentLiveEvents.Unlock()
		if online {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stream never connected")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Reconnect while the prior socket is still open: the same authorized
	// lease must recover immediately, and old cleanup cannot erase presence.
	reconnected := make(chan struct{})
	replacementEnded := make(chan error, 1)
	go func() {
		replacementEnded <- owner.StreamAgentMessages(streamCtx, attachment, func(agentworksclient.AgentLiveMessage) error { return nil }, func() { close(reconnected) })
	}()
	select {
	case <-reconnected:
	case err := <-replacementEnded:
		t.Fatalf("same-lease reconnect refused: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("same-lease reconnect waited for the old socket")
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("replaced stream remained open")
	}
	ended = replacementEnded
	if !namedAgentOnline(namedMessageAgent{ID: attachment.AgentID, Attachment: &namedAgentAttachment{ID: attachment.ID, DeliveryContract: 2}}) {
		t.Fatal("old listener cleanup removed replacement presence")
	}
	wrongLease := attachment
	wrongLease.ID = "attach-not-current"
	err = owner.StreamAgentMessages(streamCtx, wrongLease, func(agentworksclient.AgentLiveMessage) error { return nil })
	var refused *agentworksclient.APIError
	if !errors.As(err, &refused) || refused.Status != http.StatusForbidden {
		t.Fatalf("wrong lease was not refused: %v", err)
	}
	listed, _, code, _ := env.api.bullpenAdminAct(ctx, "root", "list", map[string]any{})
	if code != "" {
		t.Fatalf("list: %s", code)
	}
	handle := ""
	for _, person := range listed["people"].([]map[string]interface{}) {
		for _, agent := range person["agents"].([]map[string]interface{}) {
			if agent["agent_name"] == "helper" {
				handle, _ = agent["attachment_handle"].(string)
			}
		}
	}
	if handle == "" {
		t.Fatalf("no attachment handle listed: %v", listed)
	}
	if out, _, code, msg := env.api.bullpenAdminAct(ctx, "root", "close_attachment", map[string]any{"handle": handle, "reason": "owner unreachable"}); code != "" || out["state"] != "closed" {
		t.Fatalf("close: %v %s %s", out, code, msg)
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the live stream stayed open after the attachment was closed")
	}
	if _, err = owner.Call(ctx, "messages", map[string]any{"action": "ack", "agent_name": "helper", "attachment_id": attachment.ID, "message_id": "msg-none", "delivery": "delivered", "transport": "claude_channel"}); err == nil {
		t.Fatal("the closed lease was still accepted")
	}
	replacement, err := owner.AttachAgent(ctx, agentworksclient.AgentAttachmentOptions{AgentName: "helper", Provider: "claude", SessionID: "exact-session-two"})
	if err != nil {
		t.Fatalf("owner could not attach again: %v", err)
	}
	if replacement.ID == attachment.ID {
		t.Fatal("reattach reused the closed lease")
	}
	if out, _, _, _ := env.api.bullpenAdminAct(ctx, "root", "close_attachment", map[string]any{"handle": handle, "reason": "stale handle"}); out["state"] != "already_closed" {
		t.Fatalf("the old handle closed the new lease: %v", out)
	}
}

// Metadata/audit reads stay usable during a held message mutation. Real browser
// authentication and the existing admin gate still decide who can read them.
func TestBullpenAdminMetadataDoesNotWaitForMessageWriter(t *testing.T) {
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
			t.Fatalf("%s: %d want%d: %s", path, res.StatusCode, want, body)
		}
		if want == http.StatusOK {
			if !strings.Contains(res.Header.Get("Server-Timing"), "store;dur=") || !strings.Contains(res.Header.Get("Server-Timing"), "admin;dur=") {
				t.Fatalf("missing phase timing: %s", res.Header.Get("Server-Timing"))
			}
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
