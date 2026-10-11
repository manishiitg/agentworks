package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/mark3labs/mcp-go/mcp"
)

// Real CLI OAuth, durable server store, persistent SSE and private Unix socket.
// Pins the crash/restart decision and cached-body authorization boundary.
func TestAgentLocalBridgeRestartAndAdmission(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "bridge-fixture-secret")
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/external/v1/call", env.api.handleExternalCall)
	mux.HandleFunc("/api/external/v1/agents/events", env.api.handleAgentEvents)
	mux.HandleFunc("/api/external/v1/agents/attachment", env.api.handleAgentAttachment)
	srv := httptest.NewServer(AuthMiddleware(mux))
	defer srv.Close()
	t.Setenv("PUBLIC_URL", srv.URL)
	issue := func() (*agentworksclient.Client, string, string) {
		t.Helper()
		device, verification, e := store.CreateCLIDevice(context.Background(), "crews:run", "workflows:read", "runs:execute")
		if e != nil {
			t.Fatal(e)
		}
		if e = store.DecideCLIDevice(context.Background(), verification, &UserClaims{UserID: "owner", Username: "owner"}, true); e != nil {
			t.Fatal(e)
		}
		grant, token, _, e := store.PollCLIDevice(context.Background(), device, srv.URL+"/api/external/v1")
		if e != nil {
			t.Fatal(e)
		}
		c, e := agentworksclient.New(srv.URL, token)
		if e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(t.TempDir(), "connection.json")
		if e = agentworksclient.SaveConfig(path, agentworksclient.Config{Server: srv.URL, Token: token}); e != nil {
			t.Fatal(e)
		}
		return c, path, mcpOAuthTokenForGrant(grant).ID
	}
	receiver, path, receiverID := issue()
	sender, _, senderID := issue()
	freshSender, _, _ := issue()
	call := func(c *agentworksclient.Client, args map[string]any) map[string]any {
		t.Helper()
		raw, e := c.Call(context.Background(), "messages", args)
		if e != nil {
			t.Fatal(e)
		}
		out := map[string]any{}
		if e = json.Unmarshal(raw, &out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	call(sender, map[string]any{"action": "register", "agent_name": "sender"})
	call(freshSender, map[string]any{"action": "register", "agent_name": "fresh-sender"})
	// Short path avoids the operating system Unix socket filename limit.
	root, err := os.MkdirTemp("", "aw-bridge-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	start := func() (context.CancelFunc, <-chan error) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- agentworksclient.ServeLocalBridge(ctx, root) }()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, e := agentworksclient.CallLocalBridge(context.Background(), root, agentworksclient.LocalBridgeRequest{Action: "ping"}); e == nil {
				return cancel, done
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
		t.Fatal("local bridge did not start")
		return nil, nil
	}
	stop := func(cancel context.CancelFunc, done <-chan error) {
		t.Helper()
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("bridge did not stop")
		}
	}
	cancel, done := start()
	defer cancel()
	base := agentworksclient.LocalBridgeRequest{Provider: "claude", SessionID: "exact-hook-session", HostKey: "exact-parent-process"}
	plugin, err := agentworksclient.NewPluginMCPServer(context.Background(), root, "claude", base.HostKey, false)
	if err != nil {
		t.Fatal(err)
	}
	if plugin.GetTool("list_workflows") != nil || plugin.GetTool("messages") != nil || plugin.GetTool("attach_agent") != nil {
		t.Fatal("messaging plugin exposed broad tools or attachment")
	}
	peers := plugin.GetTool("agent_message_peers")
	if out, e := peers.Handler(context.Background(), mcp.CallToolRequest{}); e != nil || !out.IsError {
		t.Fatal("unattached plugin used the default CLI login")
	}
	local := func(in agentworksclient.LocalBridgeRequest) agentworksclient.LocalBridgeReply {
		t.Helper()
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		out, e := agentworksclient.CallLocalBridge(ctx, root, in)
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	attach := base
	attach.Action = "attach"
	attach.ConfigPath = path
	attach.AgentName = "hook-reviewer"
	attach.Purpose = "Review work"
	attach.AllowedSenders = []string{"sender", "fresh-sender"}
	if out := local(attach); !out.Attached || out.Attachment.SessionID != base.SessionID {
		t.Fatal("wrong exact session")
	}
	if out, e := peers.Handler(context.Background(), mcp.CallToolRequest{}); e != nil || out.IsError {
		t.Fatalf("attached directory failed: %+v %v", out, e)
	}
	send := func(text string) map[string]any {
		return call(sender, map[string]any{"action": "send", "agent_name": "sender", "target": "agent:hook-reviewer", "message": text, "submission_id": text})
	}
	drain := base
	drain.Action = "drain"
	drain.Transport = "claude_hook"
	wait := func(expected string) agentworksclient.LocalBridgeReply {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			out := local(drain)
			if len(out.Messages) > 0 {
				if len(out.Messages) != 1 || out.Messages[0].MessageID != expected {
					t.Fatalf("duplicate or wrong message: %+v", out)
				}
				return out
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("hook never received pushed message")
		return agentworksclient.LocalBridgeReply{}
	}
	first := send("offer before crash")
	offer := wait(first["message_id"].(string))
	if out := local(drain); len(out.Messages) != 0 {
		t.Fatal("duplicate offer within bridge")
	}
	stop(cancel, done)
	cancel, done = start()
	defer cancel()
	second := send("new offer after restart")
	_ = wait(second["message_id"].(string)) // Unacknowledged first message must NOT be reoffered.
	page := call(receiver, map[string]any{"action": "read", "agent_name": "hook-reviewer"})
	for _, v := range page["messages"].([]any) {
		if v.(map[string]any)["delivery"] != "stored" {
			t.Fatal("local write fabricated admission")
		}
	}
	ack := base
	ack.Action = "ack"
	ack.MessageID = first["message_id"].(string)
	for _, delivery := range []string{"admitted", "processed"} {
		ack.Delivery = delivery
		_ = local(ack)
	}
	reply := base
	reply.Action = "reply"
	reply.MessageID = first["message_id"].(string)
	reply.Message = "reviewed"
	reply.SubmissionID = "exact-reply"
	_ = local(reply)
	responses := call(sender, map[string]any{"action": "read", "agent_name": "sender"})["messages"].([]any)
	if len(responses) != 1 || responses[0].(map[string]any)["inbox_id"] != offer.Messages[0].InboxID {
		t.Fatal("reply lost conversation")
	}
	wrong := drain
	wrong.SessionID = "another-session"
	wrong.HostKey = "another-host"
	if local(wrong).Attached {
		t.Fatal("another session read attached state")
	}
	// Revoke after the body was cached but before a hook could release it.
	_ = send("cached before sender revoke")
	status := base
	status.Action = "status"
	deadline := time.Now().Add(5 * time.Second)
	for local(status).Pending == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if local(status).Pending == 0 {
		t.Fatal("pending body never cached")
	}
	if e := store.RevokeFamily(context.Background(), strings.TrimPrefix(senderID, "oauth-"), "owner"); e != nil {
		t.Fatal(e)
	}
	if out := local(drain); len(out.Messages) != 0 {
		t.Fatal("revoked sender cached body released")
	}
	call(freshSender, map[string]any{"action": "send", "agent_name": "fresh-sender", "target": "agent:hook-reviewer", "message": "pending for receiver", "submission_id": "receiver-revoke"})
	deadline = time.Now().Add(5 * time.Second)
	for local(status).Pending == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if local(status).Pending == 0 {
		t.Fatal("receiver pending body never cached")
	}
	if e := store.RevokeFamily(context.Background(), strings.TrimPrefix(receiverID, "oauth-"), "owner"); e != nil {
		t.Fatal(e)
	}
	ctx, stopCall := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCall()
	if _, e := agentworksclient.CallLocalBridge(ctx, root, drain); e == nil {
		t.Fatal("revoked receiver cached body released")
	}
	stop(cancel, done)
}
