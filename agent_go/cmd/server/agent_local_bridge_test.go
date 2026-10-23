package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	var slowMuseValidate atomic.Bool
	var failNativeValidate atomic.Bool
	nativeValidateFailed := make(chan struct{}, 1)
	validateStarted, validateRelease := make(chan struct{}, 1), make(chan struct{})
	mux.HandleFunc("/api/external/v1/agents/attachment", func(w http.ResponseWriter, r *http.Request) {
		if failNativeValidate.Load() {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			var in struct {
				Action string   `json:"action"`
				Name   string   `json:"agent_name"`
				IDs    []string `json:"message_ids"`
			}
			_ = json.Unmarshal(body, &in)
			if in.Action == "validate" && in.Name == "hook-reviewer" && len(in.IDs) > 0 && failNativeValidate.Swap(false) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"Temporary connection failure"}}`))
				nativeValidateFailed <- struct{}{}
				return
			}
		}
		if slowMuseValidate.Load() {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			var in struct {
				Action string `json:"action"`
				Name   string `json:"agent_name"`
			}
			_ = json.Unmarshal(body, &in)
			if in.Action == "validate" && in.Name == "muse-review" {
				select {
				case validateStarted <- struct{}{}:
				default:
				}
				select {
				case <-validateRelease:
				case <-r.Context().Done():
					return
				}
			}
		}
		env.api.handleAgentAttachment(w, r)
	})
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
	attachRegisteredAgentsForTest(t)
	call(freshSender, map[string]any{"action": "register", "agent_name": "fresh-sender"})
	// Short path avoids the operating system Unix socket filename limit.
	cache, err := os.MkdirTemp("", "aw-cache-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cache)
	t.Setenv("HOME", cache)
	t.Setenv("XDG_CACHE_HOME", cache)
	root, err := agentworksclient.DefaultLocalBridgeRoot()
	if err != nil {
		t.Fatal(err)
	}
	// A chat whose native-route lookup failed is looked at again after a wait (5 s in production). The route phase
	// below makes a route appear and expects the very next hook call, microseconds after the last lookup, to find
	// it, so the test turns the wait off (one nanosecond).
	bridgeOptions := agentworksclient.LocalBridgeOptions{RouteRetry: time.Nanosecond}
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
	// A non-interactive coding-agent tool call attaches only its own native
	// UUID, using the existing login and no installed provider plugin. The ps
	// fixture supplies process routing metadata; HTTP/OAuth/IPC stay real.
	bin := filepath.Join(cache, "agentworks-cli")
	build := exec.Command("go", "build", "-o", bin, "./cmd/agentworks")
	build.Dir = "../.."
	if output, e := build.CombinedOutput(); e != nil {
		t.Fatalf("CLI fixture build: %v: %s", e, output)
	}
	metadata := filepath.Join(cache, "metadata")
	if e := os.Mkdir(metadata, 0700); e != nil {
		t.Fatal(e)
	}
	ps := "#!/bin/sh\ncase \"$*\" in *ppid=*) printf '%s\\n' '1 codex /usr/bin/codex Mon Oct 12 01:00:00 2026';; *) printf '%s\\n' 'codex Mon Oct 12 01:00:00 2026';; esac\n"
	if e := os.WriteFile(filepath.Join(metadata, "ps"), []byte(ps), 0700); e != nil {
		t.Fatal(e)
	}
	cliEnv := func(wantOK bool, extraEnv []string, args ...string) string {
		t.Helper()
		command := exec.Command(bin, append([]string{"--config", path}, args...)...)
		command.Env = append(os.Environ(), "PATH="+metadata+string(os.PathListSeparator)+os.Getenv("PATH"), "CODEX_THREAD_ID=11111111-2222-4333-8444-555555555555", "CODEX_SESSION_ID=11111111-2222-4333-8444-555555555555")
		command.Env = append(command.Env, extraEnv...)
		output, e := command.CombinedOutput()
		if (e == nil) != wantOK {
			t.Fatalf("CLI %v: %v: %s", args, e, output)
		}
		return string(output)
	}
	cli := func(wantOK bool, args ...string) string { return cliEnv(wantOK, nil, args...) }
	if output := cli(true, "attach", "own-cli"); !strings.Contains(output, "No plugin was installed") {
		t.Fatalf("own attach required plugin or picker: %s", output)
	}
	cli(true, "attach", "own-cli")
	// The agent itself may move its own chat to another saved login and back
	// (owner choice for an open system): the second account gains the agent, the
	// first loses it, and the chat keeps its name.
	moved, movedPath, _ := issue("other")
	movedConfig, err := agentworksclient.LoadConfig(movedPath)
	if err != nil {
		t.Fatal(err)
	}
	ownerConfig, err := agentworksclient.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	hasAgent := func(c *agentworksclient.Client) bool {
		raw, e := c.Call(context.Background(), "messages", map[string]any{"action": "list_agents"})
		if e != nil {
			t.Fatal(e)
		}
		return strings.Contains(string(raw), `"agent_name":"own-cli"`)
	}
	for name, cfg := range map[string]agentworksclient.Config{"moved": movedConfig, "back": ownerConfig} {
		loginFile, e := agentworksclient.LoginPath(name)
		if e != nil {
			t.Fatal(e)
		}
		if e = agentworksclient.SaveConfig(loginFile, cfg); e != nil {
			t.Fatal(e)
		}
	}
	cli(true, "attach", "--on", "moved")
	if !hasAgent(moved) {
		t.Fatal("the moved chat's agent is not on the second login")
	}
	cli(true, "attach", "--on", "back")
	cli(true, "attach", "own-cli")
	cli(false, "attach", "another-chat", "--provider", "claude", "--session", "22222222-2222-4333-8444-555555555555")
	longPurpose := strings.Repeat("Responsibilities and current scope. ", 5) + strings.Repeat("🤝", 100)
	if output := cli(true, "bridge", "message", "purpose", "--provider", "codex", "--purpose", longPurpose+"\u202e\x1b"); !strings.Contains(output, strings.TrimSpace(longPurpose)) || strings.Contains(output, "\u202e") || strings.Contains(output, "\\u001b") {
		t.Fatalf("full purpose was lost: %s", output)
	}
	cli(false, "bridge", "message", "purpose", "--provider", "codex", "--purpose", strings.Repeat("x", 513))
	if output := cli(true, "agent"); !strings.Contains(output, "agent:own-cli") || !strings.Contains(output, "agent:sender") {
		t.Fatalf("directory alias lost authorized agents: %s", output)
	}
	cliEnv(false, []string{"CODEX_THREAD_ID=22222222-2222-4333-8444-555555555555"}, "detach")
	// A separate existing person lease must not be renamed or detached by
	// spoofing both same-user routing hints, even under a shared Codex host.
	person := agentworksclient.LocalBridgeRequest{Action: "attach", Provider: "codex", SessionID: "22222222-2222-4333-8444-555555555555", HostKey: "person-process", AgentName: "person-created", ConfigPath: path}
	if _, e := agentworksclient.CallLocalBridge(context.Background(), root, person); e != nil {
		t.Fatal(e)
	}
	spoof := []string{"CODEX_THREAD_ID=" + person.SessionID, "CODEX_SESSION_ID=" + person.SessionID}
	cliEnv(false, spoof, "attach", "takeover")
	cliEnv(false, spoof, "detach")
	person.Action = "detach_person"
	if _, e := agentworksclient.CallLocalBridge(context.Background(), root, person); e != nil {
		t.Fatal(e)
	}
	cli(true, "detach")
	base := agentworksclient.LocalBridgeRequest{Provider: "claude", SessionID: "exact-hook-session", HostKey: "exact-parent-process"}
	plugin, err := agentworksclient.NewPluginMCPServer(context.Background(), root, "claude", base.HostKey, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plugin.GetTool("list_workflows") != nil || plugin.GetTool("messages") != nil || plugin.GetTool("attach_agent") != nil {
		t.Fatal("messaging plugin exposed broad tools or attachment")
	}
	peers := plugin.GetTool("agentworks")
	if out, e := peers.Handler(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"action": "peers"}}}); e != nil || !out.IsError {
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
	attach.Purpose = longPurpose + "\u202e\x1b"
	if out := local(attach); !out.Attached || out.Attachment.SessionID != base.SessionID || out.Attachment.Purpose != longPurpose {
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
	for _, action := range []string{"count", "delete", "clear"} {
		if _, e = child.Call(context.Background(), "messages", map[string]any{"action": action, "agent_name": "other-agent", "message_id": "missing"}); e == nil {
			t.Fatal("bound child managed another agent's inbox", action)
		}
	}
	if _, e = child.Tools(context.Background()); e == nil {
		t.Fatal("child obtained full catalog")
	}
	grant, e := store.Authenticate(context.Background(), cfg.Token)
	if e != nil || grant.BoundAgentName != "hook-reviewer" || grant.BoundAgentID == "" || grant.ClientID != cliOAuthClientID || mcpOAuthTokenForGrant(grant).OAuthClientID != cliOAuthClientID {
		t.Fatal("child lacks stable identity binding")
	}
	refreshCfg, e := receiver.CreateAgentConnection(context.Background(), "other-agent")
	if e != nil {
		t.Fatal(e)
	}
	if refreshed, _, _, e := store.Refresh(context.Background(), refreshCfg.RefreshToken, cliOAuthClientID, srv.URL+"/api/external/v1"); e != nil || refreshed.BoundAgentName != "other-agent" {
		t.Fatal("child refresh lost binding", e)
	}
	if out, e := peers.Handler(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"action": "peers"}}}); e != nil || out.IsError {
		t.Fatalf("attached directory failed: %+v %v", out, e)
	}
	// Purpose updates use only this exact child's existing identity, without attachment authority.
	purposeTool := plugin.GetTool("agentworks")
	if result, e := purposeTool.Handler(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"action": "set_purpose", "purpose": "Review workflow communication"}}}); e != nil || result.IsError {
		t.Fatalf("own purpose update failed: %+v %v", result, e)
	}
	if result, e := purposeTool.Handler(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"action": "set_purpose", "purpose": "Change again"}}}); e != nil || !result.IsError {
		t.Fatal("purpose changes were not rate limited")
	}
	if _, e := child.Call(context.Background(), "messages", map[string]any{"action": "update_purpose", "agent_name": "hook-reviewer", "attachment_id": "retired-lease", "purpose": "Old session"}); e == nil {
		t.Fatal("stale attachment updated purpose")
	}
	if _, e := child.Call(context.Background(), "messages", map[string]any{"action": "update_purpose", "agent_name": "other-agent", "purpose": "Take over"}); e == nil {
		t.Fatal("child changed another agent purpose")
	}
	if _, e := child.Call(context.Background(), "messages", map[string]any{"action": "register", "agent_name": "hook-reviewer", "purpose": "Register"}); e == nil {
		t.Fatal("purpose child acquired registration authority")
	}
	// A desktop MCP child is shared, so only host metadata chooses the chat.
	codexPlugin, e := agentworksclient.NewPluginMCPServer(context.Background(), root, "codex", "desktop-parent", false, nil)
	if e != nil {
		t.Fatal(e)
	}
	codexPeers := codexPlugin.GetTool("agentworks")
	if out, e := codexPeers.Handler(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"action": "peers", "session_id": "11111111-2222-4333-8444-555555555555"}}}); e != nil || !out.IsError {
		t.Fatal("model arguments selected an MCP thread")
	}
	threads := []string{"11111111-2222-4333-8444-555555555555", "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"}
	names := []string{"codex-one", "codex-two"}
	for i, session := range threads {
		local(agentworksclient.LocalBridgeRequest{Action: "attach", Provider: "codex", SessionID: session, HostKey: "desktop-parent", ConfigPath: path, AgentName: names[i], Purpose: "Review"})
	}
	for i, session := range threads {
		req := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"action": "peers"}, Meta: &mcp.Meta{AdditionalFields: map[string]any{"x-codex-turn-metadata": map[string]any{"thread_id": session}}}}}
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
	// A bare name that is no Crew, workflow or agent says how to address an agent.
	if _, e := sender.Call(context.Background(), "messages", map[string]any{"action": "send", "agent_name": "sender", "target": "no-such-name", "message": "x", "submission_id": "bare-unknown"}); e == nil || !strings.Contains(e.Error(), "agent:NAME") {
		t.Fatalf("an unknown bare target must say to use agent:NAME: %v", e)
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
	for _, delivery := range []string{"delivered", "delivered"} {
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
	// The CLI's shutdown path retains identities/leases for update and stop/start.
	leaseBeforeStop := local(agentworksclient.LocalBridgeRequest{Action: "status", Provider: base.Provider, SessionID: base.SessionID, HostKey: base.HostKey}).Attachment.ID
	_, _ = agentworksclient.CallLocalBridge(context.Background(), root, agentworksclient.LocalBridgeRequest{Action: "shutdown"})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CLI shutdown did not finish")
	}
	cancel()
	cancel, done = start()
	if got := local(agentworksclient.LocalBridgeRequest{Action: "status", Provider: base.Provider, SessionID: base.SessionID, HostKey: base.HostKey}); !got.Attached || got.Attachment.ID != leaseBeforeStop {
		t.Fatal("CLI restart lost the approved attachment", got)
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
	offlineMessage := send("stored while offline")
	resume := base
	resume.Action = "attach"
	resume.AgentName = ""
	resume.ConfigPath = "/missing/default/login"
	resume.ReuseIdentity = true
	if out := local(resume); !out.Attached || out.Attachment.SessionID != base.SessionID {
		t.Fatal("person-confirmed exact resume failed")
	}
	// A renewed lease starts with no memory of earlier offers, so messages handed over earlier but never acknowledged
	// come again with the new one (the hook acknowledges admitted once it has written a message out, which stops this
	// in practice). What matters: the offline message arrives, and everything arrives under the renewed lease.
	sawOffline := false
	for deadline := time.Now().Add(5 * time.Second); !sawOffline; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("hook never received the message stored while offline")
		}
		for _, m := range local(drain).Messages {
			sawOffline = sawOffline || m.MessageID == offlineMessage["message_id"].(string)
			if m.AttachmentID != local(statusOffline).Attachment.ID {
				t.Fatal("offline catch-up did not use the renewed lease", m)
			}
		}
	}
	firstLease := local(statusOffline).Attachment.ID
	if got := local(resume); got.Attachment.ID != firstLease {
		t.Fatal("repeated person attach rotated an active lease")
	}
	setup := resume
	setup.Setup = true
	if !local(setup).Attached {
		t.Fatal("person attach failed")
	}
	setupContext := local(drain)
	if setupContext.SetupContext == "" || !strings.Contains(setupContext.SetupContext, "action=set_purpose") || !strings.Contains(setupContext.SetupContext, "hook-reviewer") {
		t.Fatal("attach setup omitted purpose/roster", setupContext)
	}
	if got := local(drain); got.SetupContext != "" {
		t.Fatal("setup repeated without another person attach")
	}
	channelOffer := send("channel offer on new attachment")
	drain.Transport = "claude_channel"
	if out := wait(channelOffer["message_id"].(string)); out.IncludeGuidance {
		t.Fatal("channel advanced guidance it does not display")
	}
	drain.Transport = "claude_hook"
	resumed := send("offer on a new attachment")
	if out := wait(resumed["message_id"].(string)); out.IncludeGuidance {
		t.Fatal("reconnect repeated command guidance within 24 hours")
	}
	wrongProcess := base
	wrongProcess.Action, wrongProcess.HostKey = "status", "another-provider-process"
	if local(wrongProcess).Attached {
		t.Fatal("another process selected this session")
	}
	move := attach
	move.SessionID, move.HostKey = "moved-exact-session", "moved-exact-process"
	if _, err := agentworksclient.CallLocalBridge(context.Background(), root, move); err == nil || !strings.Contains(err.Error(), "detach it there first") {
		t.Fatal("live chat name was silently displaced", err)
	}
	local(offline)
	if !local(move).Attached {
		t.Fatal("explicit move from offline identity failed")
	}
	moveOffline := move
	moveOffline.Action = "offline"
	local(moveOffline)
	if !local(attach).Attached {
		t.Fatal("stable identity did not return from offline session")
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
	// A missing registration may be retried once only before any content write,
	// and only after resolving a route for the already-approved process/session.
	routes := make(chan string, 4)
	bridgeOptions.NativeRoute = func(provider, session, host, remote string) string {
		if provider != base.Provider || session != base.SessionID || host != base.HostKey {
			return ""
		}
		return "claude+session:///replacement"
	}
	bridgeOptions.NativeOffer = func(_ context.Context, remote string, _ agentworksclient.LocalBridgeReply) error {
		routes <- remote
		if remote != "claude+session:///replacement" {
			return agentworksclient.ErrNativeOfferRejected
		}
		return agentworksclient.ErrNativeOfferUnconfirmed
	}
	cancel, done = start()
	defer cancel()
	local(nativeAttach)
	routeStatus := base
	routeStatus.Action = "status"
	if local(routeStatus).NativeRouteStatus != "stale" {
		t.Fatal("stale native route reported healthy")
	}
	send("registration changed before content write")
	for _, expected := range []string{nativeAttach.NativeRemote, "claude+session:///replacement"} {
		select {
		case route := <-routes:
			if route != expected {
				t.Fatal("wrong native retry route", route)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("definite pre-write failure was not repaired")
		}
	}
	if local(routeStatus).NativeRouteStatus != "ok" {
		t.Fatal("replacement native route not persisted")
	}
	select {
	case notice := <-nativeNotices:
		t.Fatal("repaired route emitted a fallback notification", notice)
	default:
	}
	approvedLease := local(routeStatus).Attachment.ID
	stop(cancel, done)
	// An older approved lease can lack its native route. Recovery changes
	// transport metadata only, without a new attachment or replaying offers.
	clearRoute := func() {
		t.Helper()
		file := filepath.Join(root, "sessions.json")
		data, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		var saved map[string]map[string]any
		if e = json.Unmarshal(data, &saved); e != nil {
			t.Fatal(e)
		}
		delete(saved[base.Provider+":"+base.SessionID], "native_remote")
		data, e = json.Marshal(saved)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(file, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	clearRoute()
	var routeReady atomic.Bool
	bridgeOptions.HostAlive = func(provider, host string) bool { return provider == base.Provider && host == base.HostKey }
	bridgeOptions.NativeRoute = func(provider, session, host, remote string) string {
		if !routeReady.Load() || provider != base.Provider || session != base.SessionID || host != base.HostKey {
			return ""
		}
		return "claude+session:///replacement"
	}
	bridgeOptions.NativeOffer = func(_ context.Context, _ string, out agentworksclient.LocalBridgeReply) error {
		nativeOffers <- out
		return agentworksclient.ErrNativeOfferUnconfirmed
	}
	cancel, done = start()
	defer cancel()
	if local(routeStatus).NativeDelivery {
		t.Fatal("unavailable route reported native delivery")
	}
	pendingNative := send("stored before approved native route recovery")
	deadline := time.Now().Add(5 * time.Second)
	for local(routeStatus).Pending == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if local(routeStatus).Pending == 0 {
		t.Fatal("missing-route message was not retained")
	}
	routeReady.Store(true)
	wrongHost := routeStatus
	wrongHost.HostKey = "different-provider-process"
	if local(wrongHost).Attached {
		t.Fatal("another host recovered the approved route")
	}
	if out := local(drain); !out.NativeDelivery || out.Attachment.ID != approvedLease || len(out.Messages) != 0 {
		t.Fatal("hook recovery changed lease or released native content through hooks", out)
	}
	select {
	case out := <-nativeOffers:
		if out.Attachment.ID != approvedLease || len(out.Messages) != 1 || out.Messages[0].MessageID != pendingNative["message_id"] {
			t.Fatal("recovered native route lost pending body or replayed earlier offers", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovered route did not deliver retained message")
	}
	// A transient authorization-service failure happens BEFORE a native offer.
	// The already-connected stream and existing housekeeping must restart its
	// consumer, without a new lease or replaying an earlier ambiguous offer.
	failNativeValidate.Store(true)
	afterFailure := send("native delivery resumes after pre-offer validation failure")
	select {
	case <-nativeValidateFailed:
	case <-time.After(5 * time.Second):
		t.Fatal("native validation failure was not exercised")
	}
	select {
	case out := <-nativeOffers:
		if out.Attachment.ID != approvedLease || len(out.Messages) != 1 || out.Messages[0].MessageID != afterFailure["message_id"] {
			t.Fatal("native validation recovery changed identity or replayed an ambiguous offer", out)
		}
	case <-time.After(35 * time.Second):
		t.Fatal("connected stream did not restart its native consumer after validation recovered")
	}
	// A person attach on that exact native lease starts setup immediately,
	// without waiting for a peer message; restarting cannot repeat it.
	setupNative := nativeAttach
	setupNative.NativeRemote = "claude+session:///replacement"
	setupNative.Setup = true
	if out := local(setupNative); out.Attachment.ID != approvedLease {
		t.Fatal("setup renewed an already-live lease")
	}
	select {
	case out := <-nativeOffers:
		if out.SetupContext == "" || len(out.Messages) != 0 || !strings.Contains(out.SetupContext, "action=set_purpose") {
			t.Fatal("Claude native setup did not start without a peer message", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native setup never offered")
	}
	stop(cancel, done)
	clearRoute()
	cancel, done = start()
	defer cancel()
	if out := local(routeStatus); !out.NativeDelivery || out.Attachment.ID != approvedLease {
		t.Fatal("restart did not recover existing approved lease")
	}
	select {
	case out := <-nativeOffers:
		t.Fatal("restart replayed setup or an ambiguous offer", out)
	case <-time.After(100 * time.Millisecond):
	}
	// A reconnect digest can cover more than the validator's 20-ID request
	// limit. Cache the real backlog without a native consumer, then restore it.
	stop(cancel, done)
	bridgeOptions.NativeOffer = nil
	oldIDs := map[string]bool{}
	for i := 0; i < 25; i++ {
		receipt := send("older backlog " + strconv.Itoa(i))
		oldIDs[receipt["message_id"].(string)] = true
	}
	agentMessageMu.Lock()
	backlog, e := readAgentMessageStore(context.Background())
	if e == nil {
		for ci := range backlog.Conversations {
			for mi := range backlog.Conversations[ci].Messages {
				m := &backlog.Conversations[ci].Messages[mi]
				if oldIDs[m.ID] {
					m.SentAt = time.Now().Add(-2 * time.Hour)
				}
			}
		}
		e = saveAgentMessageStore(context.Background(), backlog)
	}
	agentMessageMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	freshAfterBacklog := send("fresh body beside the old digest")
	expiredBacklog := send("old pending delivery keeps its history")
	missingBacklog := send("removed server message must leave the local queue")
	deletedBacklog := send("inbox cleanup must suppress a cached native offer")
	cancel, done = start()
	defer cancel()
	deadline = time.Now().Add(5 * time.Second)
	for local(routeStatus).Pending < 29 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if local(routeStatus).Pending != 29 {
		t.Fatal("backlog was not cached before native recovery")
	}
	preview := local(agentworksclient.LocalBridgeRequest{Action: "count", Provider: base.Provider, SessionID: base.SessionID, HostKey: base.HostKey, MessageID: deletedBacklog["message_id"].(string)})
	if !strings.Contains(string(preview.Result), `"matched":1`) {
		t.Fatal("local bridge did not forward count filters", preview)
	}
	stop(cancel, done)
	// Recipient cleanup must invalidate an already cached native body on restart.
	// Earlier phases revoke and replace the first child. Use the current
	// saved connection, just as the bridge does after reconnect.
	files, e = filepath.Glob(filepath.Join(root, "connections", "*.json"))
	if e != nil || len(files) != 1 {
		t.Fatal("missing current child for inbox cleanup", e)
	}
	cfg, e = agentworksclient.LoadConfig(files[0])
	if e != nil {
		t.Fatal(e)
	}
	cleanupChild, e := agentworksclient.New(cfg.Server, cfg.Token)
	if e != nil {
		t.Fatal(e)
	}
	if out := call(cleanupChild, map[string]any{"action": "delete", "agent_name": "hook-reviewer", "message_id": deletedBacklog["message_id"]}); out["removed"] != float64(1) {
		t.Fatal("bound child did not delete its own incoming message", out)
	}
	// Cross the cutoff after the body is cached locally, so the real server
	// validator must drain it without offering it or inventing a receipt.
	agentMessageMu.Lock()
	backlog, e = readAgentMessageStore(context.Background())
	if e == nil {
		for ci := range backlog.Conversations {
			kept := backlog.Conversations[ci].Messages[:0]
			for mi := range backlog.Conversations[ci].Messages {
				m := backlog.Conversations[ci].Messages[mi]
				if m.ID == missingBacklog["message_id"] {
					continue
				}
				if m.ID == expiredBacklog["message_id"] {
					m.SentAt = time.Now().Add(-25 * time.Hour)
				}
				kept = append(kept, m)
			}
			backlog.Conversations[ci].Messages = kept
		}
		e = saveAgentMessageStore(context.Background(), backlog)
	}
	agentMessageMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	env.api.resumeAgentMessages()
	bridgeOptions.NativeOffer = func(_ context.Context, _ string, out agentworksclient.LocalBridgeReply) error {
		nativeOffers <- out
		return agentworksclient.ErrNativeOfferUnconfirmed
	}
	cancel, done = start()
	defer cancel()
	select {
	case out := <-nativeOffers:
		if out.Attachment.ID != approvedLease || len(out.Messages) != 2 || out.Messages[0].InboxID != "agentworks-digest" || !strings.Contains(out.Messages[0].Message, "25 older unacknowledged messages") || out.Messages[1].MessageID != freshAfterBacklog["message_id"] {
			t.Fatal("large digest lost validation, identity or the fresh body", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("backlog larger than the server validation cap blocked native delivery")
	}
	if out := local(routeStatus); out.Pending != 0 {
		t.Fatal("expired cached body remained pending", out)
	}
	agentMessageMu.Lock()
	backlog, e = readAgentMessageStore(context.Background())
	agentMessageMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	retainedExpired := false
	for _, c := range backlog.Conversations {
		for _, m := range c.Messages {
			if m.ID == expiredBacklog["message_id"] {
				retainedExpired = m.Delivery == "expired" && m.Text == "old pending delivery keeps its history" && m.DeliveredAt.IsZero()
			}
		}
	}
	if !retainedExpired {
		t.Fatal("clearing old delivery lost its history or fabricated acknowledgment")
	}
	offlineNative := base
	offlineNative.Action = "offline"
	local(offlineNative)
	stop(cancel, done)
	clearRoute()
	cancel, done = start()
	defer cancel()
	if out := local(routeStatus); !out.Offline || out.NativeDelivery {
		t.Fatal("native recovery revived an offline identity")
	}
	stop(cancel, done)
	// A legacy empty-host record stays fenced, with a durable once-only notice.
	statePath = filepath.Join(root, "sessions.json")
	stateBody, e := os.ReadFile(statePath)
	if e != nil {
		t.Fatal(e)
	}
	saved = nil
	if e = json.Unmarshal(stateBody, &saved); e != nil {
		t.Fatal(e)
	}
	legacy := saved[base.Provider+":"+base.SessionID]
	legacy["host_key"] = ""
	legacy["offline"] = false
	delete(legacy, "missing_host_notice")
	delete(legacy, "offline_explicit")
	stateBody, e = json.Marshal(saved)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(statePath, stateBody, 0600); e != nil {
		t.Fatal(e)
	}
	legacyNotices := make(chan string, 4)
	bridgeOptions = agentworksclient.LocalBridgeOptions{Notify: func(_ agentworksclient.AgentAttachment, text string) { legacyNotices <- text }}
	cancel, done = start()
	if out := local(routeStatus); !out.Offline || out.NativeDelivery || !strings.Contains(out.LastError, "no verified process binding") {
		t.Fatal("legacy attachment revived or hid reattach diagnostic", out)
	}
	select {
	case text := <-legacyNotices:
		if !strings.Contains(text, "agentworks attach") {
			t.Fatal("legacy notice lacks recovery", text)
		}
	case <-time.After(time.Second):
		t.Fatal("legacy upgrade gave no notice")
	}
	stop(cancel, done)
	cancel, done = start()
	select {
	case text := <-legacyNotices:
		t.Fatal("restart repeated legacy notice", text)
	case <-time.After(100 * time.Millisecond):
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

	// Muse: real OAuth/SSE + persistent private Unix watch. A wake is metadata,
	// not a body offer or receipt. Restart never replays an ambiguous wake.
	_, musePath, _ := issue()
	museBase := agentworksclient.LocalBridgeRequest{Provider: "muse", SessionID: "11111111-2222-4333-8444-555555555555", HostKey: "muse-birth-key"}
	museAttach := museBase
	museAttach.Action, museAttach.AgentName, museAttach.ConfigPath = "attach", "muse-review", musePath
	museLease := local(museAttach).Attachment.ID
	museBase.AttachmentID = museLease
	museContext, museCancel := context.WithCancel(context.Background())
	defer museCancel()
	musePlugin, e := agentworksclient.NewMusePluginMCPServer(museContext, root, museBase.HostKey, func() (string, error) { return museBase.SessionID, nil })
	if e != nil {
		t.Fatal(e)
	}
	tool := func(name string, args map[string]any, wantError bool) *mcp.CallToolResult {
		t.Helper()
		callArgs := map[string]any{"action": name}
		for k, v := range args {
			callArgs[k] = v
		}
		result, e := musePlugin.GetTool("agentworks").Handler(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: callArgs}})
		if e != nil || result.IsError != wantError {
			t.Fatalf("Muse %s: %+v %v", name, result, e)
		}
		return result
	}
	config := tool("monitor", nil, false).Content[0].(mcp.TextContent).Text
	if !strings.Contains(config, "--session") || !strings.Contains(config, museLease) || !strings.Contains(config, `"wake_delay_ms":0`) {
		t.Fatal("unpinned Monitor config", config)
	}
	watch := func(request agentworksclient.LocalBridgeRequest) (context.CancelFunc, <-chan error, <-chan agentworksclient.MuseWatchHint) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		hints := make(chan agentworksclient.MuseWatchHint, 4)
		go func() {
			done <- agentworksclient.WatchMuseBridge(ctx, root, request, func(h agentworksclient.MuseWatchHint) error { hints <- h; return nil })
		}()
		return cancel, done, hints
	}
	waitActive := func(active bool) {
		t.Helper()
		check := museBase
		check.Action = "status"
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if local(check).MonitorActive == active {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("Muse Monitor activity did not change", active)
	}
	slowMuseValidate.Store(true)
	watchCancel, watchDone, hints := watch(museBase)
	defer watchCancel()
	select {
	case <-validateStarted:
	case <-time.After(5 * time.Second):
		close(validateRelease)
		t.Fatal("watch validation did not start")
	}
	responsive := make(chan struct{}, 1)
	go func() {
		check := base
		check.Action = "status"
		_, _ = agentworksclient.CallLocalBridge(context.Background(), root, check)
		responsive <- struct{}{}
	}()
	select {
	case <-responsive:
	case <-time.After(time.Second):
		close(validateRelease)
		t.Fatal("slow Muse validation stalled another attached provider")
	}
	slowMuseValidate.Store(false)
	close(validateRelease)
	waitActive(true)
	renewMuse := museBase
	renewMuse.Action = "attach"
	if out := local(renewMuse); !out.MonitorActive || !out.NativeDelivery || out.Attachment.ID != museLease {
		t.Fatal("bare Muse reconnect lost its active Monitor or lease", out)
	}
	if active := tool("monitor", nil, false).Content[0].(mcp.TextContent).Text; !strings.Contains(active, `"monitor_active":true`) || strings.Contains(active, `"command"`) {
		t.Fatal("duplicate Monitor startup suggested", active)
	}
	for _, bad := range []agentworksclient.LocalBridgeRequest{
		{Provider: "muse", SessionID: museBase.SessionID, HostKey: "wrong-host", AttachmentID: museLease},
		{Provider: "muse", SessionID: museBase.SessionID, HostKey: museBase.HostKey, AttachmentID: "old-lease"},
		museBase, // museSecond reader of the same exact lease
	} {
		c, d, _ := watch(bad)
		select {
		case err := <-d:
			if err == nil {
				t.Fatal("invalid/duplicate watch admitted")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("invalid watch did not fail")
		}
		c()
	}
	museSend := func(body string) map[string]any {
		return call(freshSender, map[string]any{"action": "send", "agent_name": "fresh-sender", "target": "agent:muse-review", "message": body, "submission_id": body})
	}
	museFirst := museSend("Muse body stays private until receive")
	select {
	case hint := <-hints:
		raw, _ := json.Marshal(hint)
		if hint.MessageID != museFirst["message_id"] || hint.Source != "agentworks-muse-watch" || hint.Sender != "fresh-sender" || strings.Contains(string(raw), "body stays") {
			t.Fatal("wrong or overbroad Muse hint", hint)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SSE did not wake the idle Unix watch")
	}
	tool("ack", map[string]any{"message_id": museFirst["message_id"], "delivery": "delivered"}, true)
	watchCancel()
	<-watchDone
	waitActive(false)
	stop(cancel, done)
	cancel, done = start()
	defer cancel()
	watchCancel, watchDone, hints = watch(museBase)
	defer watchCancel()
	waitActive(true)
	select {
	case hint := <-hints:
		if hint.Kind != "pending" || hint.Pending != 1 || hint.MessageID != "" || hint.InboxID != "" || hint.Sender != "" {
			t.Fatal("invalid aggregate catch-up hint", hint)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("restart left unread bodies silent")
	}
	select {
	case hint := <-hints:
		t.Fatal("restart replayed an individual ambiguous hint", hint)
	case <-time.After(100 * time.Millisecond):
	}
	museSecond := museSend("New message wakes the same renewed stream")
	select {
	case hint := <-hints:
		if hint.MessageID != museSecond["message_id"] {
			t.Fatal("old hint replayed", hint)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("restarted watch did not receive new SSE offer")
	}
	received := tool("receive", nil, false)
	var bodies agentworksclient.LocalBridgeReply
	if e = json.Unmarshal([]byte(received.Content[0].(mcp.TextContent).Text), &bodies); e != nil || len(bodies.Messages) != 2 || bodies.Messages[0].MessageID != museFirst["message_id"] {
		t.Fatal("Muse bodies unavailable or misrouted", bodies, e)
	}
	tool("ack", map[string]any{"message_id": museFirst["message_id"], "delivery": "delivered"}, false)
	tool("reply", map[string]any{"message_id": museFirst["message_id"], "message": "Received through Muse Monitor", "submission_id": "muse-monitor-reply"}, false)
	tool("ack", map[string]any{"message_id": museFirst["message_id"], "delivery": "delivered"}, false)
	responses = call(freshSender, map[string]any{"action": "read", "agent_name": "fresh-sender", "inbox_id": museFirst["inbox_id"]})["messages"].([]any)
	if len(responses) != 1 || responses[0].(map[string]any)["message"] != "Received through Muse Monitor" || responses[0].(map[string]any)["inbox_id"] != museFirst["inbox_id"] {
		t.Fatal("Muse reply lost its original inbox", responses)
	}
	changed := museBase
	changed.SessionID = "11111111-2222-4333-8444-555555555556"
	changed.Action = "status"
	changed.MuseCurrent = true
	if local(changed).Attached {
		t.Fatal("chat switch attached another session")
	}
	select {
	case <-watchDone:
	case <-time.After(5 * time.Second):
		t.Fatal("chat switch did not stop old Monitor")
	}
	waitActive(false)
	c, d, _ := watch(museBase)
	select {
	case err := <-d:
		if err == nil {
			t.Fatal("old session watch resumed after switch")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old-session refusal blocked")
	}
	c()
	changed = museBase
	changed.Action = "status"
	changed.MuseCurrent = true
	local(changed)
	watchCancel, watchDone, _ = watch(museBase)
	defer watchCancel()
	waitActive(true)
	// The approved MCP brokers a metadata pipe for a sandboxed native reader.
	watchCancel()
	<-watchDone
	waitActive(false)
	config = tool("monitor", nil, false).Content[0].(mcp.TextContent).Text
	var options struct {
		Monitor struct {
			Command string `json:"command"`
		} `json:"monitor"`
	}
	if e = json.Unmarshal([]byte(config), &options); e != nil {
		t.Fatal(e)
	}
	parts := strings.Split(options.Monitor.Command, " --subscription-file '")
	if len(parts) != 2 {
		t.Fatal("missing private subscription", config)
	}
	subscription := strings.TrimSuffix(parts[1], "'")
	pipeCtx, pipeCancel := context.WithCancel(context.Background())
	pipeDone := make(chan error, 1)
	pipeHints := make(chan agentworksclient.MuseWatchHint, 4)
	go func() {
		pipeDone <- agentworksclient.ReadMuseMonitorPipe(pipeCtx, subscription, museBase.SessionID, museLease, func(h agentworksclient.MuseWatchHint) error { pipeHints <- h; return nil })
	}()
	waitActive(true)
	if e = agentworksclient.ReadMuseMonitorPipe(context.Background(), subscription, museBase.SessionID, museLease, func(agentworksclient.MuseWatchHint) error { return nil }); e == nil {
		t.Fatal("duplicate FIFO reader accepted")
	}
	third := museSend("Private FIFO carries no body")
	select {
	case h := <-pipeHints:
		if h.MessageID != third["message_id"] {
			t.Fatal("wrong FIFO metadata", h)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("approved broker did not forward SSE hint to FIFO")
	}
	pipeCancel()
	select {
	case <-pipeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("FIFO reader cancellation stuck")
	}
	waitActive(false) // kernel lock release, no new message or liveness polling
	// A crashed broker's complete UUID set is cleaned only with its lifetime
	// lock free. The helper must retain the newly staged live subscription.
	stale := filepath.Join(filepath.Dir(subscription), "11111111-2222-4333-8444-555555555557.json")
	record := agentworksclient.MuseMonitorSubscription{Source: "agentworks-muse-watch", SessionID: museBase.SessionID, AttachmentID: museLease, HostKey: museBase.HostKey, DataHome: t.TempDir()}
	encoded, _ := json.Marshal(record)
	for _, suffix := range []string{"", ".broker", ".lock", ".fifo"} {
		if e = os.WriteFile(stale+suffix, encoded, 0600); e != nil {
			t.Fatal(e)
		}
	}
	config = tool("monitor", nil, false).Content[0].(mcp.TextContent).Text
	for _, suffix := range []string{"", ".broker", ".lock", ".fifo"} {
		if _, e = os.Lstat(stale + suffix); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("stale Muse subscription retained", suffix, e)
		}
	}
	if again := tool("monitor", nil, false).Content[0].(mcp.TextContent).Text; again != config {
		t.Fatal("live staged broker was removed or duplicated", again, config)
	}
	museCancel()
	watchCancel, watchDone, _ = watch(museBase)
	defer watchCancel()
	waitActive(true)
	closeMuse := museBase
	closeMuse.Action = "detach"
	local(closeMuse)
	select {
	case <-watchDone:
	case <-time.After(5 * time.Second):
		t.Fatal("detach did not close Monitor")
	}

	// Revoke after the body was cached but before a hook could release it.
	_ = send("cached before sender revoke")
	status := base
	status.Action = "status"
	deadline = time.Now().Add(5 * time.Second)
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
