package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	issue := func(users ...string) (*agentworksclient.Client, string, string) {
		user := "owner"
		if len(users) > 0 {
			user = users[0]
		}
		t.Helper()
		device, verification, e := store.CreateCLIDevice(context.Background(), "crews:run", "workflows:read", "runs:execute")
		if e != nil {
			t.Fatal(e)
		}
		if e = store.DecideCLIDevice(context.Background(), verification, &UserClaims{UserID: user, Username: user}, true); e != nil {
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
	var bridgeOptions agentworksclient.LocalBridgeOptions
	start := func() (context.CancelFunc, <-chan error) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- agentworksclient.ServeLocalBridge(ctx, root, bridgeOptions) }()
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
	if out := local(attach); !out.Attached || out.Attachment.SessionID != base.SessionID {
		t.Fatal("wrong exact session")
	}
	// One parent can manage both names, but a derived child cannot impersonate
	// the other agent or fetch the parent's general tool catalog.
	call(receiver, map[string]any{"action": "register", "agent_name": "other-agent"})
	files, e := filepath.Glob(filepath.Join(root, "connections", "*.json"))
	if e != nil || len(files) != 1 {
		t.Fatal("missing private child connection")
	}
	cfg, e := agentworksclient.LoadConfig(files[0])
	if e != nil {
		t.Fatal(e)
	}
	child, e := agentworksclient.New(cfg.Server, cfg.Token)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = child.Call(context.Background(), "messages", map[string]any{"action": "read", "agent_name": "other-agent"}); e == nil {
		t.Fatal("child read another agent")
	}
	if _, e = child.Tools(context.Background()); e == nil {
		t.Fatal("child obtained full catalog")
	}
	grant, e := store.Authenticate(context.Background(), cfg.Token)
	if e != nil || grant.BoundAgentName != "hook-reviewer" || grant.BoundAgentID == "" {
		t.Fatal("child lacks stable identity binding")
	}
	refreshCfg, e := receiver.CreateAgentConnection(context.Background(), "other-agent")
	if e != nil {
		t.Fatal(e)
	}
	if refreshed, _, _, e := store.Refresh(context.Background(), refreshCfg.RefreshToken, cliOAuthClientID, srv.URL+"/api/external/v1"); e != nil || refreshed.BoundAgentName != "other-agent" {
		t.Fatal("child refresh lost binding", e)
	}
	if out, e := peers.Handler(context.Background(), mcp.CallToolRequest{}); e != nil || out.IsError {
		t.Fatalf("attached directory failed: %+v %v", out, e)
	}
	// A desktop MCP child is shared, so only host metadata chooses the chat.
	codexPlugin, e := agentworksclient.NewPluginMCPServer(context.Background(), root, "codex", "desktop-parent", false)
	if e != nil {
		t.Fatal(e)
	}
	codexPeers := codexPlugin.GetTool("agent_message_peers")
	if out, e := codexPeers.Handler(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"session_id": "11111111-2222-4333-8444-555555555555"}}}); e != nil || !out.IsError {
		t.Fatal("model arguments selected an MCP thread")
	}
	threads := []string{"11111111-2222-4333-8444-555555555555", "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"}
	names := []string{"codex-one", "codex-two"}
	for i, session := range threads {
		local(agentworksclient.LocalBridgeRequest{Action: "attach", Provider: "codex", SessionID: session, HostKey: "desktop-parent", ConfigPath: path, AgentName: names[i], Purpose: "Review"})
	}
	for i, session := range threads {
		req := mcp.CallToolRequest{Params: mcp.CallToolParams{Meta: &mcp.Meta{AdditionalFields: map[string]any{"x-codex-turn-metadata": map[string]any{"thread_id": session}}}}}
		out, e := codexPeers.Handler(context.Background(), req)
		if e != nil || out.IsError {
			t.Fatal("host-bound peers failed", out, e)
		}
		var bound agentworksclient.LocalBridgeReply
		if e := json.Unmarshal([]byte(out.Content[0].(mcp.TextContent).Text), &bound); e != nil || bound.Attachment.AgentName != names[i] {
			t.Fatal("shared MCP child selected another chat", bound.Attachment, e)
		}
	}
	for _, session := range threads {
		local(agentworksclient.LocalBridgeRequest{Action: "detach", Provider: "codex", SessionID: session, HostKey: "desktop-parent"})
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
	if !offer.IncludeGuidance {
		t.Fatal("first delivery omitted session guidance")
	}
	if out := local(drain); len(out.Messages) != 0 {
		t.Fatal("duplicate offer within bridge")
	}
	stop(cancel, done)
	cancel, done = start()
	defer cancel()
	second := send("new offer after restart")
	if out := wait(second["message_id"].(string)); out.IncludeGuidance {
		t.Fatal("bridge restart repeated session guidance before 24 hours")
	} // Unacknowledged first message must NOT be reoffered.
	// Advance the durable timestamp while stopped, then exercise the real
	// server/SSE/socket path again without a day-long test wait.
	stop(cancel, done)
	statePath := filepath.Join(root, "sessions.json")
	stateBytes, e := os.ReadFile(statePath)
	if e != nil {
		t.Fatal(e)
	}
	var state map[string]map[string]any
	if e = json.Unmarshal(stateBytes, &state); e != nil {
		t.Fatal(e)
	}
	for _, session := range state {
		session["last_guidance_at"] = time.Now().Add(-25 * time.Hour).UTC()
	}
	stateBytes, e = json.Marshal(state)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(statePath, stateBytes, 0600); e != nil {
		t.Fatal(e)
	}
	cancel, done = start()
	defer cancel()
	reminder := send("offer after guidance expires")
	if out := wait(reminder["message_id"].(string)); !out.IncludeGuidance {
		t.Fatal("24-hour guidance refresh missing")
	}
	afterReminder := send("offer immediately after reminder")
	if out := wait(afterReminder["message_id"].(string)); out.IncludeGuidance {
		t.Fatal("guidance repeated immediately after refresh")
	}
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
	// New agents in this account are accepted without changing the attachment.
	call(freshSender, map[string]any{"action": "register", "agent_name": "new-peer"})
	newPeer := call(freshSender, map[string]any{"action": "send", "agent_name": "new-peer", "target": "agent:hook-reviewer", "message": "new account peer", "submission_id": "new-peer-proof"})
	wait(newPeer["message_id"].(string))
	read := base
	read.Action = "read"
	if !strings.Contains(string(local(read).Result), "new account peer") {
		t.Fatal("new account peer absent from explicit recovery")
	}
	peerReply := base
	peerReply.Action, peerReply.InboxID = "reply", newPeer["inbox_id"].(string)
	peerReply.Message, peerReply.SubmissionID = "received", "new-peer-reply"
	local(peerReply)
	otherAccount, _, _ := issue("other")
	call(otherAccount, map[string]any{"action": "register", "agent_name": "outsider"})
	if _, e := otherAccount.Call(context.Background(), "messages", map[string]any{"action": "send", "agent_name": "outsider", "target": "agent:hook-reviewer", "message": "outside account", "submission_id": "outside-account-proof"}); e == nil {
		t.Fatal("sender-policy removal crossed the account boundary")
	}
	cleanup := base
	cleanup.Action = "cleanup"
	local(cleanup)
	recovered := reply
	recovered.MessageID, recovered.InboxID = "", offer.Messages[0].InboxID
	recovered.Message, recovered.SubmissionID = "recovered original inbox", "recovered-reply"
	local(recovered)
	responses = call(sender, map[string]any{"action": "read", "agent_name": "sender"})["messages"].([]any)
	if len(responses) != 2 || responses[1].(map[string]any)["inbox_id"] != recovered.InboxID {
		t.Fatal("compacted reply lost its original inbox")
	}
	stop(cancel, done)
	// Simulate a saved pre-upgrade restricted attachment: never widen it silently.
	savedPath := filepath.Join(root, "sessions.json")
	rawState, e := os.ReadFile(savedPath)
	if e != nil {
		t.Fatal(e)
	}
	var saved map[string]map[string]any
	if e = json.Unmarshal(rawState, &saved); e != nil {
		t.Fatal(e)
	}
	delete(saved["claude:"+base.SessionID], "account_senders")
	rawState, _ = json.Marshal(saved)
	if e = os.WriteFile(savedPath, rawState, 0600); e != nil {
		t.Fatal(e)
	}
	cancel, done = start()
	defer cancel()
	migrationStatus := base
	migrationStatus.Action = "status"
	if out := local(migrationStatus); out.Attached || !out.Offline || !strings.Contains(out.LastError, "policy changed") {
		t.Fatal("old sender permissions widened without person confirmation", out)
	}
	if out := local(drain); len(out.Messages) != 0 {
		t.Fatal("cleanup replayed an ambiguous offer")
	}
	offline := base
	offline.Action = "offline"
	local(offline)
	statusOffline := base
	statusOffline.Action = "status"
	if out := local(statusOffline); out.Attached || !out.Offline || out.Attachment.AgentName != "hook-reviewer" {
		t.Fatal("session end lost identity")
	}
	if out := local(drain); len(out.Messages) != 0 {
		t.Fatal("offline session received a turn")
	}
	missingHost := base
	missingHost.Action, missingHost.HostKey = "resume", ""
	if _, e := agentworksclient.CallLocalBridge(context.Background(), root, missingHost); e == nil {
		t.Fatal("resume accepted no provider process key")
	}
	resume := base
	resume.Action = "resume"
	if out := local(resume); !out.Attached || out.Attachment.SessionID != base.SessionID {
		t.Fatal("person-confirmed exact resume failed")
	}
	channelOffer := send("channel offer on new attachment")
	drain.Transport = "claude_channel"
	if out := wait(channelOffer["message_id"].(string)); out.IncludeGuidance {
		t.Fatal("channel advanced guidance it does not display")
	}
	drain.Transport = "claude_hook"
	resumed := send("offer on a new attachment")
	if out := wait(resumed["message_id"].(string)); !out.IncludeGuidance {
		t.Fatal("new attachment inherited old command guidance")
	}
	wrongProcess := base
	wrongProcess.Action, wrongProcess.HostKey = "status", "another-provider-process"
	if local(wrongProcess).Attached {
		t.Fatal("another process selected this session")
	}
	move := attach
	move.SessionID, move.HostKey = "moved-exact-session", "moved-exact-process"
	if !local(move).Attached || !local(attach).Attached {
		t.Fatal("stable identity move failed")
	}
	files, e = filepath.Glob(filepath.Join(root, "connections", "*.json"))
	if e != nil || len(files) != 1 {
		t.Fatal("moving retained identities leaked child credentials")
	}
	// A dead child is renewed exactly once from the currently approved parent.
	cfg, e = agentworksclient.LoadConfig(files[0])
	if e != nil {
		t.Fatal(e)
	}
	if e = store.RevokeFamily(context.Background(), grant.FamilyID, "owner"); e != nil {
		t.Fatal(e)
	}
	if !local(attach).Attached {
		t.Fatal("dead child was not recovered from approved parent")
	}
	files, e = filepath.Glob(filepath.Join(root, "connections", "*.json"))
	if e != nil || len(files) != 1 {
		t.Fatal("stale child configuration survived replacement")
	}
	cfg, e = agentworksclient.LoadConfig(files[0])
	if e != nil {
		t.Fatal(e)
	}
	// Exercise failure classification through real OAuth/SSE/local delivery.
	stop(cancel, done)
	nativeOffers := make(chan agentworksclient.LocalBridgeReply, 4)
	var firstNative atomic.Bool
	nativeNotices := make(chan string, 4)
	bridgeOptions.Notify = func(_ agentworksclient.AgentAttachment, notice string) { nativeNotices <- notice }
	bridgeOptions.NativeOffer = func(_ context.Context, _ string, out agentworksclient.LocalBridgeReply) error {
		nativeOffers <- out
		if !firstNative.Swap(true) {
			return errors.Join(agentworksclient.ErrNativeOfferRejected, errors.New("unavailable native endpoint"))
		}
		return agentworksclient.ErrNativeOfferUnconfirmed
	}
	cancel, done = start()
	defer cancel()
	nativeAttach := attach
	nativeAttach.NativeRemote = "claude+session:///example"
	local(nativeAttach)
	for i, text := range []string{"definitely failed native offer", "native offer with refreshed guidance", "unconfirmed offer retains guidance cadence"} {
		send(text)
		select {
		case delivered := <-nativeOffers:
			if delivered.IncludeGuidance != (i < 2) {
				t.Fatal("native failure or ambiguity lost guidance cadence", i)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("native stream never offered message")
		}
		if i == 0 {
			select {
			case notice := <-nativeNotices:
				if !strings.Contains(notice, "not delivered") || !strings.Contains(notice, "read the mailbox") || strings.Contains(notice, "Open the chat") {
					t.Fatal("failed native offer gave misleading recovery", notice)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("failed offer produced no recovery notice")
			}
		}
	}
	stop(cancel, done)
	bridgeOptions = agentworksclient.LocalBridgeOptions{}
	cancel, done = start()
	defer cancel()
	local(attach)
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
	if out := local(drain); len(out.Messages) != 0 || out.IncludeGuidance {
		t.Fatal("revoked sender body or guidance released")
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
	if _, _, _, e := store.Refresh(context.Background(), cfg.RefreshToken, cliOAuthClientID, srv.URL+"/api/external/v1"); e == nil {
		t.Fatal("child refreshed after parent revocation")
	}
}
