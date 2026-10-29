package server

import (
	"bufio"
	"bytes"
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
		return c, mcpOAuthTokenForGrant(grant).ID, token
	}
	receiver, receiverConnection, receiverToken := issue()
	sender, senderConnection, _ := issue()
	stranger, _, strangerToken := issue()
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
	call(receiver, map[string]any{"action": "register", "agent_name": "reviewer", "purpose": "Review incoming work"})
	attached, err := receiver.AttachAgent(ctx, agentworksclient.AgentAttachmentOptions{AgentName: "reviewer", Purpose: "Review incoming work", Provider: "claude", SessionID: "exact-session-one"})
	if err != nil {
		t.Fatal(err)
	}
	inspect := func(token string) (int, map[string]any) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"action": "inspect", "agent_name": "reviewer"})
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/external/v1/agents/attachment", bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		var out map[string]any
		if e = json.NewDecoder(resp.Body).Decode(&out); e != nil {
			t.Fatal(e)
		}
		return resp.StatusCode, out
	}
	status, metadata := inspect(receiverToken)
	leaseMetadata, _ := metadata["attachment"].(map[string]any)
	if status != http.StatusOK || metadata["agent_id"] != attached.AgentID || len(leaseMetadata) != 1 || leaseMetadata["attachment_id"] != attached.ID {
		t.Fatal("own attachment inspect was missing or exposed excess metadata", metadata)
	}
	if status, _ := inspect(strangerToken); status != http.StatusNotFound {
		t.Fatal("an unapproved connection inspected another connection's name", status)
	}
	root, err := os.MkdirTemp("", "aw-channel-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	config := filepath.Join(root, "connection.json")
	if err := agentworksclient.SaveConfig(config, agentworksclient.Config{Server: srv.URL, Token: receiverToken}); err != nil {
		t.Fatal(err)
	}
	sessions, _ := json.Marshal(map[string]any{"claude:exact-session-one": map[string]any{"attachment": attached, "host_key": "native-host", "account_senders": true, "config_path": config, "parent_config_path": config}})
	if err := os.WriteFile(filepath.Join(root, "sessions.json"), sessions, 0600); err != nil {
		t.Fatal(err)
	}
	helperDone := make(chan error, 1)
	go func() {
		helperDone <- agentworksclient.ServeLocalBridge(ctx, root, agentworksclient.LocalBridgeOptions{HostAlive: func(string, string) bool { return true }})
	}()
	defer func() {
		cancel()
		if err := <-helperDone; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "ping"}); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	bridge, err := agentworksclient.NewPluginMCPServer(ctx, root, "claude", "native-host", true, nil, "exact-session-one")
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
	if !strings.Contains(params["content"].(string), "native push proof") || !strings.Contains(params["content"].(string), "<<<BEGIN "+receipt["message_id"].(string)+">>>") || meta["message_id"] != receipt["message_id"] {
		t.Fatalf("wrong sender/identity: %v", incoming)
	}
	if strings.Contains(params["content"].(string), "Read from the inbox") {
		t.Fatal("pending native delivery inherited a mailbox history label")
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
		for i, delivery := range []string{"delivered", "delivered"} {
			write(map[string]any{"jsonrpc": "2.0", "id": 100 + messageIndex*2 + i, "method": "tools/call", "params": map[string]any{"name": "agentworks", "arguments": map[string]any{"action": "ack", "message_id": id, "delivery": delivery}}})
			response := next()
			res, ok := response["result"].(map[string]any)
			if !ok || res["isError"] == true {
				t.Fatalf("native ack failed: %v", response)
			}
		}
	}
	// An explicit retry from another adapter keeps the first handoff evidence
	// and makes no second durable write or stream wakeup.
	agentMessageMu.Lock()
	beforeRetry, retryErr := readAgentMessageStore(context.Background())
	agentMessageMu.Unlock()
	if retryErr != nil {
		t.Fatal(retryErr)
	}
	beforeJSON, _ := json.Marshal(beforeRetry)
	beforeWake := agentLiveChanged()
	call(receiver, map[string]any{"action": "ack", "agent_name": "reviewer", "attachment_id": meta["attachment_id"], "message_id": receipt["message_id"], "delivery": "delivered", "transport": "mailbox"})
	agentMessageMu.Lock()
	afterRetry, retryErr := readAgentMessageStore(context.Background())
	agentMessageMu.Unlock()
	afterJSON, _ := json.Marshal(afterRetry)
	if retryErr != nil || string(beforeJSON) != string(afterJSON) || beforeWake != agentLiveChanged() {
		t.Fatalf("retry changed the first receipt or rewrote the mailbox: %v", retryErr)
	}
	page = call(receiver, map[string]any{"action": "read", "agent_name": "reviewer"})
	for _, item := range page["messages"].([]any) {
		m := item.(map[string]any)
		if m["message_id"] == receipt["message_id"] && m["delivery"] != "delivered" {
			t.Fatalf("explicit receipt missing: %v", m)
		}
	}
	write(map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{"name": "agentworks", "arguments": map[string]any{"action": "reply", "message_id": receipt["message_id"], "message": "reviewed", "submission_id": "review-reply"}}})
	if response := next(); response["result"].(map[string]any)["isError"] == true {
		t.Fatalf("reply failed: %v", response)
	}
	replies := call(sender, map[string]any{"action": "read", "agent_name": "sender"})["messages"].([]any)
	if len(replies) != 1 || replies[0].(map[string]any)["inbox_id"] != receipt["inbox_id"] {
		t.Fatal("reply split the conversation")
	}
	// A chat rename updates presentation on the same lease. Free text that
	// resembles another address is disambiguated; it never becomes a target.
	lease := agentworksclient.AgentAttachment{AgentName: "reviewer", ID: meta["attachment_id"].(string), DeliveryRoute: "claude_channel", ClientBuild: "current-build"}
	if err := stranger.UpdateAgentLabel(ctx, lease, "Impersonation"); err == nil {
		t.Fatal("another connection changed this chat's label")
	}
	oldLease := lease
	oldLease.ID = "old-attachment"
	if err := receiver.UpdateAgentLabel(ctx, oldLease, "Stale"); err == nil {
		t.Fatal("a stale lease changed the label")
	}
	if err := receiver.UpdateAgentLabel(ctx, lease, "Sender"); err != nil {
		t.Fatal(err)
	}
	// An ordinary metadata request that reports no build cannot erase the
	// last known build and suppress an outdated-helper notice.
	withoutBuild := lease
	withoutBuild.ClientBuild = ""
	if err := receiver.UpdateAgentLabel(ctx, withoutBuild, "Sender"); err != nil {
		t.Fatal(err)
	}
	stored, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundBuild := false
	for _, agent := range stored.Agents {
		if agent.Name == "reviewer" && agent.Attachment != nil {
			foundBuild = agent.Attachment.ClientBuild == "current-build" && agent.Attachment.ID == lease.ID
		}
	}
	if !foundBuild {
		t.Fatal("exact-lease metadata did not refresh the helper build")
	}
	directory := call(receiver, map[string]any{"action": "list_agents", "agent_name": "reviewer"})
	foundLabel := false
	for _, raw := range directory["agents"].([]any) {
		a := raw.(map[string]any)
		if a["agent_name"] == "reviewer" {
			foundLabel = a["label"] == "Sender (reviewer)" && a["target"] == "agent:reviewer" && a["online"] == true && a["presence"] == "Wakes now" && a["last_delivered_at"] != nil
		}
	}
	if !foundLabel {
		t.Fatal("rename changed the identity or hid its real address", directory)
	}
	// An omitted route leaves the observed adapter untouched.
	body, _ := json.Marshal(map[string]any{"action": "label", "agent_name": lease.AgentName, "attachment_id": lease.ID, "label": "Sender"})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/external/v1/agents/attachment", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+receiverToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("omitted-route status: %d", response.StatusCode)
	}
	directory = call(receiver, map[string]any{"action": "list_agents", "agent_name": "reviewer"})
	for _, raw := range directory["agents"].([]any) {
		row := raw.(map[string]any)
		if row["agent_name"] == "reviewer" && (row["presence"] != "Wakes now" || row["delivery_mode"] != "live") {
			t.Fatal("omitted route cleared the adapter", row)
		}
	}
	// Losing an observed adapter clears its old route on the same lease.
	lease.DeliveryRoute = ""
	if err := receiver.UpdateAgentLabel(ctx, lease, "Sender"); err != nil {
		t.Fatal(err)
	}
	directory = call(receiver, map[string]any{"action": "list_agents", "agent_name": "reviewer"})
	for _, raw := range directory["agents"].([]any) {
		row := raw.(map[string]any)
		if row["agent_name"] == "reviewer" && (row["presence"] != "Connected; delivery unknown" || row["delivery_mode"] != "unknown") {
			t.Fatal("missing adapter kept an old route", row)
		}
	}
	// The same open stream can be hook-only. Presence follows the reported
	// adapter, not the TCP connection, and metadata cannot create a receipt.
	lease.DeliveryRoute = "claude_hook"
	if err := receiver.UpdateAgentLabel(ctx, lease, "Sender"); err != nil {
		t.Fatal(err)
	}
	directory = call(receiver, map[string]any{"action": "list_agents", "agent_name": "reviewer"})
	for _, raw := range directory["agents"].([]any) {
		row := raw.(map[string]any)
		if row["agent_name"] == "reviewer" && (row["presence"] != "At next step" || row["delivery_mode"] != "next_step") {
			t.Fatal("hook connection claimed immediate delivery", row)
		}
	}
	lease.DeliveryRoute = "codex_queue"
	if err := receiver.UpdateAgentLabel(ctx, lease, "Sender"); err == nil {
		t.Fatal("accepted another provider's adapter")
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

	if _, err := receiver.AttachAgent(ctx, agentworksclient.AgentAttachmentOptions{AttachmentID: meta["attachment_id"].(string), AgentName: "reviewer", Provider: "claude", SessionID: "exact-session"}); err == nil {
		t.Fatal("old automatic recovery took the name back")
	}
	if replacement["attachment_id"] == meta["attachment_id"] {
		t.Fatal("attachment did not rotate")
	}
	_, err = receiver.Call(context.Background(), "messages", map[string]any{"action": "ack", "agent_name": "reviewer", "attachment_id": meta["attachment_id"], "message_id": receipt["message_id"], "delivery": "delivered", "transport": "claude_channel"})
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
	// Reconnect receives one body-free wake signal; one authorized inbox read
	// catches up approved mail, oldest first, without a polling loop.
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
	select {
	case signal := <-caught:
		if signal.Sequence <= 0 || signal.Message != "" || signal.MessageID != "" || signal.InboxID != "" || signal.Sender != "" {
			t.Fatalf("stream released more than a wake hint: %+v", signal)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect failed")
	}
	body, _ = json.Marshal(map[string]any{"action": "inbox", "agent_name": attachment.AgentName, "attachment_id": attachment.ID, "after": 0, "limit": 20})
	request, _ = http.NewRequestWithContext(catchCtx, http.MethodPost, srv.URL+"/api/external/v1/agents/attachment", strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer "+receiverToken)
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var backlog struct {
		Messages []agentworksclient.AgentLiveMessage `json:"messages"`
	}
	err = json.NewDecoder(response.Body).Decode(&backlog)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || len(backlog.Messages) != 2 || backlog.Messages[0].MessageID != first["message_id"] || backlog.Messages[1].MessageID != second["message_id"] {
		t.Fatalf("authorized inbox did not catch up in order: %+v %v", backlog, err)
	}
	call(sender, map[string]any{"action": "ping_person", "agent_name": "sender", "message_id": first["message_id"]})
	select {
	case ping := <-caught:
		if ping.Kind != "ping" || ping.MessageID == "" || !strings.Contains(ping.Message, "waiting for a reply") || strings.Contains(ping.Message, "offline first") {
			t.Fatalf("explicit person ping lost its notice: %+v", ping)
		}
		call(receiver, map[string]any{"action": "ack", "agent_name": attachment.AgentName, "attachment_id": attachment.ID, "message_id": ping.MessageID, "delivery": "delivered", "transport": "mailbox"})
	case <-time.After(5 * time.Second):
		t.Fatal("person ping was not signalled")
	}
	// Own named-agent mail follows current access after a CLI login replacement.
	if err = store.RevokeFamily(context.Background(), strings.TrimPrefix(senderConnection, "oauth-"), "owner"); err != nil {
		t.Fatal(err)
	}
	validateContext := func(c *agentworksclient.Client, token string, att agentworksclient.AgentAttachment) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"action": "validate", "agent_name": att.AgentName, "attachment_id": att.ID, "message_ids": []string{first["message_id"].(string)}})
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/external/v1/agents/attachment", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		var result map[string]any
		if e = json.NewDecoder(resp.Body).Decode(&result); e != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("reply context status=%d %v", resp.StatusCode, e)
		}
		return result
	}
	replyContext := validateContext(receiver, receiverToken, attachment)
	if ids, ok := replyContext["message_ids"].([]any); !ok || len(ids) != 1 || ids[0] != first["message_id"] {
		t.Fatal("own message was held only because its CLI login was replaced", replyContext)
	}
	if replyContext["inbox_ids"].(map[string]any)[first["message_id"].(string)] != first["inbox_id"] {
		t.Fatal("current recipient lost reply context", replyContext)
	}
	outsider, _, outsiderToken := issue()
	call(outsider, map[string]any{"action": "register", "agent_name": "outside-inbox"})
	outsideLease, e := outsider.AttachAgent(context.Background(), agentworksclient.AgentAttachmentOptions{AgentName: "outside-inbox", Provider: "claude", SessionID: "outside-session"})
	if e != nil {
		t.Fatal(e)
	}
	deniedContext := validateContext(outsider, outsiderToken, outsideLease)
	if len(deniedContext["inbox_ids"].(map[string]any)) != 0 {
		t.Fatal("another agent learned reply context", deniedContext)
	}
	call(receiver, map[string]any{"action": "send", "agent_name": attachment.AgentName, "inbox_id": first["inbox_id"], "message": "new reply after old grant revoked", "submission_id": "current-reply"})
	sender, _, _ = issue()
	call(sender, map[string]any{"action": "register", "agent_name": "sender"})
	call(sender, map[string]any{"action": "unregister", "agent_name": "sender"})
	removedContext := validateContext(receiver, receiverToken, attachment)
	if ids, ok := removedContext["message_ids"].([]any); ok && len(ids) != 0 {
		t.Fatal("removed sender retained delivery access", removedContext)
	}
	body, _ = json.Marshal(map[string]any{"action": "inbox", "agent_name": attachment.AgentName, "attachment_id": attachment.ID, "after": 0, "limit": 20})
	heldRequest, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/external/v1/agents/attachment", strings.NewReader(string(body)))
	heldRequest.Header.Set("Authorization", "Bearer "+receiverToken)
	heldRequest.Header.Set("Content-Type", "application/json")
	heldResponse, err := http.DefaultClient.Do(heldRequest)
	if err != nil {
		t.Fatal(err)
	}
	var heldPage map[string]any
	err = json.NewDecoder(heldResponse.Body).Decode(&heldPage)
	heldResponse.Body.Close()
	if err != nil || heldResponse.StatusCode != 200 || heldPage["held"].(float64) == 0 || heldPage["held_revision"] == "" {
		t.Fatal("removed sender did not remain held", heldPage, err)
	}
	detail := heldPage["held_details"].([]any)[0].(map[string]any)
	if detail["sender"] == "" || detail["reason"] == "" || detail["next"] == "" || detail["message"] != nil {
		t.Fatal("held notice omitted useful metadata or leaked a body", detail)
	}
	call(sender, map[string]any{"action": "register", "agent_name": "sender"})
	if err = store.RevokeFamily(context.Background(), strings.TrimPrefix(receiverConnection, "oauth-"), "owner"); err != nil {
		t.Fatal(err)
	}
	send(sender, "sender", "after revoke")
	select {
	case m := <-caught:
		t.Fatalf("revoked receiver got a signal: %v", m)
	case e := <-ended:
		if e == nil {
			t.Fatal("revoked stream returned success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revoked stream stayed open after write")
	}
	// Retained addresses and their history must not exhaust connection slots.
	before, err := readAgentMessageSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	beforeHistory, err := json.Marshal(before.Conversations)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 101 {
		name := fmt.Sprintf("retained-%03d", i)
		call(sender, map[string]any{"action": "register", "agent_name": name})
		if _, err := sender.CreateAgentConnection(context.Background(), name); err != nil {
			t.Fatalf("connect retained address %d: %v", i, err)
		}
	}
	after, err := readAgentMessageSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	afterHistory, err := json.Marshal(after.Conversations)
	if err != nil || string(beforeHistory) != string(afterHistory) {
		t.Fatal("new connections changed message history or receipts", err)
	}
	for _, previous := range before.Agents {
		found := false
		for _, current := range after.Agents {
			found = found || current.ID == previous.ID && current.Name == previous.Name && current.UserID == previous.UserID
		}
		if !found {
			t.Fatal("new connections discarded a retained address", previous.ID)
		}
	}
}

func TestAgentLabelCollisionsKeepAddresses(t *testing.T) {
	items := []map[string]interface{}{
		{"agent_name": "one", "label": "Shared work"},
		{"agent_name": "person/two", "label": "Shared work"},
		{"agent_name": "three", "label": "agent: TWO"},
	}
	projectAgentLabels(items)
	for i, want := range []string{"Shared work (one)", "Shared work (person/two)", "agent: TWO (three)"} {
		if items[i]["label"] != want {
			t.Fatalf("%v", items)
		}
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

func TestAgentReceiptActivityKeepsOwnerAndReceiptBoundaries(t *testing.T) {
	now := time.Now().UTC()
	s := agentMessageStore{Conversations: []agentConversation{
		{Endpoints: [2]agentMessageEndpoint{{UserID: "sender"}, {UserID: "owner", NamedAgentID: "own"}}, Messages: []agentMessage{
			{From: 0, SentAt: now.Add(time.Hour)}, // storage is not a received receipt
			{From: 0, DeliveredAt: now},
			{From: 0, Kind: agentPingKind, DeliveredAt: now.Add(time.Hour)},
		}},
		{Endpoints: [2]agentMessageEndpoint{{UserID: "sender"}, {UserID: "other", NamedAgentID: "foreign"}}, Messages: []agentMessage{{From: 0, DeliveredAt: now.Add(time.Hour)}}},
	}}
	activity := ownerAgentReceiptActivity(s, "owner")
	if len(activity) != 1 || !activity["own"]["last_delivered_at"].Equal(now) {
		t.Fatal("activity mixed owners, server notices or stored messages", activity)
	}
}

func TestAgentActivityExpiresWithoutInventingIdle(t *testing.T) {
	a := namedMessageAgent{ID: "activity-fixture", Attachment: &namedAgentAttachment{ID: "lease", DeliveryContract: 2, Activity: "busy", ActivityAt: time.Now().Add(-11 * time.Minute)}}
	agentLiveEvents.Lock()
	agentLiveEvents.online[a.ID] = &agentLiveStream{attachmentID: "lease"}
	agentLiveEvents.Unlock()
	defer func() { agentLiveEvents.Lock(); delete(agentLiveEvents.online, a.ID); agentLiveEvents.Unlock() }()
	row := map[string]any{}
	addAgentPresence(row, a)
	if row["activity"] != "Unknown" {
		t.Fatal("stale busy state claimed current activity", row)
	}
	a.Attachment.ActivityAt = time.Now()
	addAgentPresence(row, a)
	if row["activity"] != "Working" {
		t.Fatal("observed busy state missing", row)
	}
	a.Attachment.Activity = "idle"
	addAgentPresence(row, a)
	if row["activity"] != "Idle" {
		t.Fatal("observed idle state missing", row)
	}
	if len([]rune(cleanAgentLabel(strings.Repeat("x", 200)+"\n\u200d"))) > 160 || strings.ContainsAny(cleanAgentLabel("a\n\u200db"), "\n\u200d") {
		t.Fatal("server metadata lost text bounds")
	}
}
