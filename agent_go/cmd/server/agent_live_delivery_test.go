package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Real OAuth, durable workspace, SSE and stdio MCP: no model or mailbox poll.
// The channel offer itself cannot claim admission; the receiving agent does.
func TestAgentLiveClaudeChannelOverAuthenticatedHTTP(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "live-agent-fixture-secret")
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
	issue := func() (*agentworksclient.Client, string) {
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
		return c, mcpOAuthTokenForGrant(grant).ID
	}
	receiver, receiverConnection := issue()
	sender, _ := issue()
	stranger, _ := issue()
	call := func(c *agentworksclient.Client, args map[string]any) map[string]any {
		t.Helper()
		raw, e := c.Call(context.Background(), "messages", args)
		if e != nil {
			t.Fatalf("%v: %v", args, e)
		}
		var out map[string]any
		if e = json.Unmarshal(raw, &out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	call(sender, map[string]any{"action": "register", "agent_name": "sender"})
	call(stranger, map[string]any{"action": "register", "agent_name": "stranger"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bridge, err := agentworksclient.NewClaudeChannelServer(ctx, receiver, agentworksclient.ClaudeChannelOptions{AgentName: "reviewer", Purpose: "Review incoming work", SessionID: "exact-session-one", JournalDir: filepath.Join(t.TempDir(), "private-journal"), Log: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	busyStarted := make(chan struct{})
	busyRelease := make(chan struct{})
	bridge.AddTool(mcp.NewTool("fixture_busy"), func(toolCtx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		close(busyStarted)
		select {
		case <-busyRelease:
		case <-toolCtx.Done():
		}
		return mcp.NewToolResultText("finished"), nil
	})
	stdin, input := io.Pipe()
	output, stdout := io.Pipe()
	defer input.Close()
	defer output.Close()
	done := make(chan error, 1)
	go func() { done <- mcpserver.NewStdioServer(bridge).Listen(ctx, stdin, stdout) }()
	wire := make(chan map[string]any, 20)
	go func() {
		defer close(wire)
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), 4<<20)
		for scanner.Scan() {
			var m map[string]any
			if json.Unmarshal(scanner.Bytes(), &m) == nil {
				select {
				case wire <- m:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	write := func(v any) {
		t.Helper()
		raw, _ := json.Marshal(v)
		if _, e := fmt.Fprintf(input, "%s\n", raw); e != nil {
			t.Fatal(e)
		}
	}
	next := func() map[string]any {
		t.Helper()
		select {
		case m, ok := <-wire:
			if !ok {
				t.Fatal("stdio closed")
			}
			return m
		case <-time.After(5 * time.Second):
			agentMessageMu.Lock()
			store, storeErr := readAgentMessageStore(context.Background())
			agentMessageMu.Unlock()
			agentLiveEvents.Lock()
			online := fmt.Sprint(agentLiveEvents.online)
			agentLiveEvents.Unlock()
			t.Fatalf("native channel did not receive without polling; online=%s agents=%+v conversations=%+v storeErr=%v", online, store.Agents, store.Conversations, storeErr)
			return nil
		}
	}
	write(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "fixture", "version": "1"}}})
	init := next()
	result := init["result"].(map[string]any)
	if _, ok := result["capabilities"].(map[string]any)["experimental"].(map[string]any)["claude/channel"]; !ok {
		t.Fatal("native capability absent")
	}
	write(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	send := func(c *agentworksclient.Client, name, text string) map[string]any {
		return call(c, map[string]any{"action": "send", "agent_name": name, "target": "agent:reviewer", "message": text, "submission_id": text})
	}
	write(map[string]any{"jsonrpc": "2.0", "id": 50, "method": "tools/call", "params": map[string]any{"name": "fixture_busy", "arguments": map[string]any{}}})
	select {
	case <-busyStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("tool did not start")
	}
	newPeerReceipt := send(stranger, "stranger", "new account peer")
	newPeerIncoming := next()
	if newPeerIncoming["method"] != "notifications/claude/channel" || newPeerIncoming["params"].(map[string]any)["meta"].(map[string]any)["message_id"] != newPeerReceipt["message_id"] {
		t.Fatal("same-account peer did not push without a sender allow field", newPeerIncoming)
	}
	receipt := send(sender, "sender", "native push proof")
	if receipt["delivery"] != "stored" || !strings.Contains(receipt["next"].(string), "agent_name") {
		t.Fatalf("misleading receipt: %v", receipt)
	}
	incoming := next()
	if incoming["method"] != "notifications/claude/channel" {
		t.Fatalf("wrong event: %v", incoming)
	}
	params := incoming["params"].(map[string]any)
	meta := params["meta"].(map[string]any)
	if params["content"] != "native push proof" || meta["message_id"] != receipt["message_id"] {
		t.Fatalf("wrong sender/identity: %v", incoming)
	}
	close(busyRelease)
	if finished := next(); finished["id"] != float64(50) {
		t.Fatalf("busy tool was interrupted: %v", finished)
	}
	page := call(receiver, map[string]any{"action": "read", "agent_name": "reviewer"})
	for _, item := range page["messages"].([]any) {
		if item.(map[string]any)["delivery"] != "stored" {
			t.Fatal("notification fabricated receipt")
		}
	}
	for messageIndex, id := range []any{newPeerReceipt["message_id"], receipt["message_id"]} {
		for i, delivery := range []string{"admitted", "processed"} {
			write(map[string]any{"jsonrpc": "2.0", "id": 100 + messageIndex*2 + i, "method": "tools/call", "params": map[string]any{"name": "agent_message_ack", "arguments": map[string]any{"message_id": id, "delivery": delivery}}})
			response := next()
			res, ok := response["result"].(map[string]any)
			if !ok || res["isError"] == true {
				t.Fatalf("native ack failed: %v", response)
			}
		}
	}
	page = call(receiver, map[string]any{"action": "read", "agent_name": "reviewer"})
	for _, item := range page["messages"].([]any) {
		m := item.(map[string]any)
		if m["message_id"] == receipt["message_id"] && m["delivery"] != "processed" {
			t.Fatalf("explicit receipt missing: %v", m)
		}
	}
	write(map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{"name": "agent_message_reply", "arguments": map[string]any{"message_id": receipt["message_id"], "message": "reviewed", "submission_id": "review-reply"}}})
	if response := next(); response["result"].(map[string]any)["isError"] == true {
		t.Fatalf("reply failed: %v", response)
	}
	replies := call(sender, map[string]any{"action": "read", "agent_name": "sender"})["messages"].([]any)
	if len(replies) != 1 || replies[0].(map[string]any)["inbox_id"] != receipt["inbox_id"] {
		t.Fatal("reply split the conversation")
	}
	// A simplified person NAME attach cannot displace a live chat, including
	// when the other computer is absent from the local bridge journal.
	if _, err := receiver.AttachAgent(context.Background(), agentworksclient.AgentAttachmentOptions{RejectLiveMove: true, AgentName: "reviewer", Provider: "claude", SessionID: "another-computer-session"}); err == nil || !strings.Contains(err.Error(), "detach it there first") {
		t.Fatal("person attach displaced another computer's live chat", err)
	}
	// Moving a stable address revokes the old session lease immediately.
	replacementAttachment, err := receiver.AttachAgent(context.Background(), agentworksclient.AgentAttachmentOptions{AgentName: "reviewer", Purpose: "New review session", Provider: "claude", SessionID: "exact-session-two"})
	if err != nil {
		t.Fatal(err)
	}
	replacement := map[string]any{}
	replacementJSON, _ := json.Marshal(replacementAttachment)
	if err = json.Unmarshal(replacementJSON, &replacement); err != nil {
		t.Fatal(err)
	}
	if _, err = receiver.Call(context.Background(), "messages", map[string]any{"action": "attach", "agent_name": "reviewer", "purpose": "Agent takeover", "provider": "claude", "native_session_id": "other-session"}); err == nil {
		t.Fatal("agent tool could attach a session")
	}

	if replacement["attachment_id"] == meta["attachment_id"] {
		t.Fatal("attachment did not rotate")
	}
	_, err = receiver.Call(context.Background(), "messages", map[string]any{"action": "ack", "agent_name": "reviewer", "attachment_id": meta["attachment_id"], "message_id": receipt["message_id"], "delivery": "processed", "transport": "claude_channel"})
	if err == nil {
		t.Fatal("old session acknowledged after replacement")
	}
	cancel()
	input.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("channel did not stop")
	}
	// Reconnect catches up stored approved mail, oldest first; acknowledged mail
	// is not replayed. No directory or inbox poll is required by the receiver.
	first := send(sender, "sender", "offline first")
	second := send(sender, "sender", "offline second")
	var attachment agentworksclient.AgentAttachment
	raw, _ := json.Marshal(replacement)
	if err = json.Unmarshal(raw, &attachment); err != nil {
		t.Fatal(err)
	}
	catchCtx, stop := context.WithCancel(context.Background())
	defer stop()
	caught := make(chan agentworksclient.AgentLiveMessage, 3)
	ended := make(chan error, 1)
	go func() {
		ended <- receiver.StreamAgentMessages(catchCtx, attachment, func(m agentworksclient.AgentLiveMessage) error { caught <- m; return nil })
	}()
	for _, expected := range []any{first["message_id"], second["message_id"]} {
		select {
		case m := <-caught:
			if m.MessageID != expected {
				t.Fatalf("out of order or replayed: %v", m)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("reconnect failed")
		}
	}
	if err = store.RevokeFamily(context.Background(), strings.TrimPrefix(receiverConnection, "oauth-"), "owner"); err != nil {
		t.Fatal(err)
	}
	send(sender, "sender", "after revoke")
	select {
	case m := <-caught:
		t.Fatalf("revoked receiver got body: %v", m)
	case e := <-ended:
		if e == nil {
			t.Fatal("revoked stream returned success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revoked stream stayed open after write")
	}
}

// Guest runs retain the owner's storage path, but cannot impersonate that owner
// when all account senders are accepted by an attached local coding agent.
func TestAgentLiveGuestCallerKeepsItsActor(t *testing.T) {
	api := &StreamingAPI{lastQueryRequests: map[string]QueryRequest{"guest-session": {CrewGuestCaller: "guest", PinRunMode: true}}}
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew}, Chat: &codeChat{SessionID: "guest-session"}}
	endpoint := api.agentMessageCallerRole(callerMessageEndpoint("owner", caller))
	if endpoint.UserID != "owner" || endpoint.Guest != "guest" || sameAccountAgentSender(endpoint, "owner") {
		t.Fatal("guest-run Crew widened to owner local-agent access", endpoint)
	}
	endpoint.Guest = ""
	if !sameAccountAgentSender(endpoint, "owner") {
		t.Fatal("owner-run Crew cannot reach its account")
	}
}
