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

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/mark3labs/mcp-go/mcp"
)

// The production OAuth, workspace store, Unix helper and MCP handler reproduce
// server-owned hook delivery, retry after an unconfirmed receipt, and an exact reply.
func TestAgentMailboxRecoveryReplyAndReceipt(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "receipt-fixture-secret")
	oauth, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer oauth.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/external/v1/call", env.api.handleExternalCall)
	mux.HandleFunc("/api/external/v1/agents/attachment", env.api.handleAgentAttachment)
	mux.HandleFunc("/api/external/v1/agents/events", env.api.handleAgentEvents)
	remote := httptest.NewServer(AuthMiddleware(mux))
	defer remote.Close()
	t.Setenv("PUBLIC_URL", remote.URL)
	issue := func(user string) (*agentworksclient.Client, string) {
		t.Helper()
		device, verification, e := oauth.CreateCLIDevice(context.Background(), "crews:run", "workflows:read", "runs:execute")
		if e != nil {
			t.Fatal(e)
		}
		if e = oauth.DecideCLIDevice(context.Background(), verification, &UserClaims{UserID: user, Username: user}, true); e != nil {
			t.Fatal(e)
		}
		_, token, _, e := oauth.PollCLIDevice(context.Background(), device, remote.URL+"/api/external/v1")
		if e != nil {
			t.Fatal(e)
		}
		client, e := agentworksclient.New(remote.URL, token)
		if e != nil {
			t.Fatal(e)
		}
		config := filepath.Join(t.TempDir(), "login.json")
		if e = agentworksclient.SaveConfig(config, agentworksclient.Config{Server: remote.URL, Token: token}); e != nil {
			t.Fatal(e)
		}
		return client, config
	}
	parent, config := issue("owner")
	sender, _ := issue("owner")
	call := func(client *agentworksclient.Client, args map[string]any) map[string]any {
		t.Helper()
		raw, e := client.Call(context.Background(), "messages", args)
		if e != nil {
			t.Fatal(e)
		}
		var value map[string]any
		if e = json.Unmarshal(raw, &value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	senderID := call(sender, map[string]any{"action": "register", "agent_name": "sender"})["agent_id"].(string)
	root, err := os.MkdirTemp("", "aw-receipt-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agentworksclient.ServeLocalBridge(ctx, root, agentworksclient.LocalBridgeOptions{}) }()
	defer func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(5 * time.Second):
			t.Error("helper did not stop")
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, e := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "ping"}); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	session := "11111111-2222-4333-8444-555555555555"
	base := agentworksclient.LocalBridgeRequest{Provider: "codex", SessionID: session, HostKey: "exact-process", Action: "attach", AgentName: "receiver", ConfigPath: config}
	attached, err := agentworksclient.CallLocalBridge(ctx, root, base)
	if err != nil || !attached.Attached {
		t.Fatalf("attach: %+v %v", attached, err)
	}
	plugin, err := agentworksclient.NewPluginMCPServer(ctx, root, "codex", base.HostKey, false, nil, session)
	if err != nil {
		t.Fatal(err)
	}
	tool := func(args map[string]any, wantError bool) agentworksclient.LocalBridgeReply {
		t.Helper()
		result, e := plugin.GetTool("agentworks").Handler(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}})
		if e != nil || result.IsError != wantError {
			t.Fatalf("MCP %v: %v %+v", args, e, result)
		}
		var value agentworksclient.LocalBridgeReply
		if !wantError {
			if e = json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &value); e != nil {
				t.Fatal(e)
			}
		}
		return value
	}
	sent := call(sender, map[string]any{"action": "send", "agent_name": "sender", "target": "agent:receiver", "message": "Please review", "submission_id": "receipt-review"})
	id, inbox := sent["message_id"].(string), sent["inbox_id"].(string)
	hook := base
	hook.Action, hook.Transport = "drain", "codex_hook"
	for attempt := 0; attempt < 2; attempt++ {
		batch, e := agentworksclient.CallLocalBridge(ctx, root, hook)
		if e != nil || len(batch.Messages) != 1 || batch.Messages[0].MessageID != id || batch.Messages[0].Message != "Please review" {
			t.Fatalf("server-owned pending hook %d: %+v %v", attempt, batch, e)
		}
	}
	localState, e := os.ReadFile(filepath.Join(root, "sessions.json"))
	if e != nil || strings.Contains(string(localState), "Please review") || strings.Contains(string(localState), `"offers"`) || strings.Contains(string(localState), `"retired"`) {
		t.Fatalf("helper retained a second mailbox: %v", e)
	}
	_, foreignConfig := issue("other")
	foreignLogin, e := agentworksclient.LoadConfig(foreignConfig)
	if e != nil {
		t.Fatal(e)
	}
	foreignRequest, e := http.NewRequest(http.MethodPost, remote.URL+"/api/external/v1/agents/attachment", strings.NewReader(`{"action":"inbox","agent_name":"receiver","attachment_id":"`+attached.Attachment.ID+`"}`))
	if e != nil {
		t.Fatal(e)
	}
	foreignRequest.Header.Set("Authorization", "Bearer "+foreignLogin.Token)
	foreignResponse, e := http.DefaultClient.Do(foreignRequest)
	if e != nil {
		t.Fatal(e)
	}
	foreignResponse.Body.Close()
	if foreignResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign account read pending inbox: %d", foreignResponse.StatusCode)
	}
	page := tool(map[string]any{"action": "read", "inbox_id": inbox}, false)
	var recovered struct {
		Messages []struct {
			ID string `json:"message_id"`
		} `json:"messages"`
	}
	if json.Unmarshal(page.Result, &recovered) != nil || len(recovered.Messages) != 1 || recovered.Messages[0].ID != id {
		t.Fatal("read did not hand over the message")
	}
	saved := func() agentMessage {
		t.Helper()
		s, e := readAgentMessageStore(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		for _, c := range s.Conversations {
			for _, m := range c.Messages {
				if m.ID == id {
					return m
				}
			}
		}
		t.Fatal("stored message missing")
		return agentMessage{}
	}
	if saved().Delivery != "stored" {
		t.Fatal("a read fabricated admission")
	}
	ack := map[string]any{"action": "ack", "message_id": id, "delivery": "delivered"}
	tool(ack, false)
	first := saved()
	if first.Delivery != "delivered" || first.DeliveredAt.IsZero() || first.LiveTransport != "mailbox" {
		t.Fatalf("single processed ack: %+v", first)
	}
	tool(ack, false)
	ack["delivery"] = "delivered"
	tool(ack, false)
	last := saved()
	if last.Delivery != "delivered" || !last.DeliveredAt.Equal(first.DeliveredAt) {
		t.Fatal("retry changed the receipt or its times")
	}
	if batch, e := agentworksclient.CallLocalBridge(ctx, root, hook); e != nil || len(batch.Messages) != 0 {
		t.Fatalf("delivered message reappeared in hook: %+v %v", batch, e)
	}
	reply := tool(map[string]any{"action": "reply", "message_id": id, "message": "Reviewed", "submission_id": "review-answer"}, false)
	var answer map[string]any
	if json.Unmarshal(reply.Result, &answer) != nil || answer["inbox_id"] != inbox {
		t.Fatal("reply lost its original conversation")
	}
	// A known outgoing message ID is not permission to reply as its recipient.
	tool(map[string]any{"action": "reply", "message_id": answer["message_id"], "message": "Wrong direction", "submission_id": "bad-reply"}, true)
	other := base
	other.SessionID = "22222222-2222-4333-8444-555555555555"
	other.HostKey, other.AgentName = "other-process", "other-agent"
	if _, e := agentworksclient.CallLocalBridge(ctx, root, other); e != nil {
		t.Fatal(e)
	}
	other.Action, other.MessageID, other.Message, other.SubmissionID = "reply", id, "Take over", "other-agent-reply"
	if _, e := agentworksclient.CallLocalBridge(ctx, root, other); e == nil {
		t.Fatal("another agent replied by message ID")
	}
	// A messaging-only child can answer the exact hosted chat that opened its
	// inbox, without gaining product tools. Exercise submission AND queued
	// delivery through the real HTTP server/helper; replace only the model.
	for _, profile := range workproduct.BuiltinAgentProfiles() {
		profile.Product = workproduct.ProfileID
		if e := env.svc.registry.RegisterProfile(profile); e != nil {
			t.Fatal(e)
		}
	}
	const hostedRoot = "Crew/example"
	const hostedKey = "example:chat:abcdef12"
	const hostedSession = "product-33333333-2222-4333-8444-555555555555"
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: workproduct.ProfileID, Folder: "example", OwnerID: "owner", ProjectID: "example", Shared: true}); err != nil {
		t.Fatal(err)
	}
	record := ProductConversationRecord{ConversationID: "conversation-44444444-2222-4333-8444-555555555555", ProfileID: workproduct.ProfileID, ConversationKey: hostedKey, SessionID: hostedSession, WorkspacePath: hostedRoot, ResourceID: "example"}
	registry, _ := json.Marshal(productConversationRegistryDocument{Version: 1, Entries: map[string]ProductConversationRecord{string(workproduct.ProfileID) + "/" + hostedKey: record}})
	env.mock.mu.Lock()
	env.mock.files[hostedRoot+"/product.json"] = `{"schema_version":1,"product":"work","id":"example","owner_id":"owner","title":"Example","session_id":"project-main"}`
	env.mock.files[productConversationRegistryPath("owner")] = string(registry)
	env.mock.mu.Unlock()
	reg := &recordingRegistrar{}
	if e := env.api.registerAgentMessagingTools(reg, "owner", hostedSession, QueryRequest{}, crewTriggerLinkCaller(hostedRoot), func(string) {}); e != nil {
		t.Fatal(e)
	}
	hostedReceipt, err := reg.tools["send_message"].exec(internalBotRequestContext(ctx, "owner"), map[string]interface{}{"target": "agent:receiver", "message": "Please answer here", "submission_id": "hosted-review"})
	if err != nil {
		t.Fatal(err)
	}
	var hosted map[string]any
	if err = json.Unmarshal([]byte(hostedReceipt), &hosted); err != nil {
		t.Fatal(err)
	}
	hostedID, hostedInbox := hosted["message_id"].(string), hosted["inbox_id"].(string)
	tool(map[string]any{"action": "read", "inbox_id": hostedInbox}, false)
	tool(map[string]any{"action": "ack", "message_id": hostedID, "delivery": "delivered"}, false)
	oldTurn := agentMessageTurn
	agentMessageTurn = func(_ *StreamingAPI, _ context.Context, request map[string]interface{}, session, _ string) (internalSessionTurnResult, error) {
		if session != hostedSession || request["agent_message_inbox"] != hostedInbox && request["agent_message_inbox"] != "inbox-different_chat" {
			t.Errorf("reply reached another chat: session=%s inbox=%v", session, request["agent_message_inbox"])
		}
		return internalSessionTurnResult{}, nil
	}
	defer func() { agentMessageTurn = oldTurn }()
	response := tool(map[string]any{"action": "reply", "message_id": hostedID, "message": "Answered", "submission_id": "hosted-answer"}, false)
	var replyReceipt map[string]any
	if json.Unmarshal(response.Result, &replyReceipt) != nil || replyReceipt["inbox_id"] != hostedInbox {
		t.Fatal("hosted reply lost the original inbox")
	}
	var original agentConversation
	deadline = time.Now().Add(5 * time.Second)
	for {
		agentMessageMu.Lock()
		store, e := readAgentMessageStore(ctx)
		agentMessageMu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
		for _, conversation := range store.Conversations {
			if conversation.ID == hostedInbox {
				original = conversation
			}
		}
		if len(original.Messages) == 2 && original.Messages[1].Delivery == "delivered" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("hosted reply did not pass live admission: %+v", original.Messages)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, scenario := range []string{"agent_opened", "different_chat", "another_person", "ping_only", "not_recipient"} {
		conversation := original
		conversation.ID = "inbox-" + scenario
		incoming := original.Messages[0]
		incoming.ID = "msg-" + scenario
		conversation.Messages = []agentMessage{incoming}
		switch scenario {
		case "agent_opened":
			conversation.Endpoints[0], conversation.Endpoints[1] = conversation.Endpoints[1], conversation.Endpoints[0]
			conversation.Messages[0].From = 1
		case "different_chat":
			conversation.Endpoints[0].ChatKey = "session:" + hostedSession
		case "another_person":
			conversation.Endpoints[0].UserID = "other"
		case "ping_only":
			// Real person pings live in the target's private notification inbox.
			conversation.ID = agentStatusConversationPrefix + attached.Attachment.AgentID
			conversation.Endpoints = [2]agentMessageEndpoint{original.Endpoints[1], original.Endpoints[1]}
			conversation.Messages[0].Kind = agentPingKind
		case "not_recipient":
			conversation.Endpoints[1].NamedAgentID = senderID
		}
		agentMessageMu.Lock()
		store, e := readAgentMessageStore(ctx)
		if e == nil {
			store.Conversations = append(store.Conversations, conversation)
			e = saveAgentMessageStore(ctx, store)
		}
		agentMessageMu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
		args := map[string]any{"action": "reply", "message_id": incoming.ID, "message": "Reply", "submission_id": scenario}
		if scenario == "different_chat" {
			// PLAT-856-412, 23 Oct decision: "A new reply uses current inbox
			// participation and current send/share authority."
			response := tool(args, false)
			var receipt map[string]any
			if json.Unmarshal(response.Result, &receipt) != nil || receipt["inbox_id"] != conversation.ID {
				t.Fatal("current participant's reply lost its inbox")
			}
		} else {
			tool(args, true)
		}
	}
	// Rotating the receiver lease makes the old MCP child's receipts fail.
	if _, e := parent.AttachAgent(ctx, agentworksclient.AgentAttachmentOptions{AgentName: "receiver", Provider: "codex", SessionID: "replacement-session"}); e != nil {
		t.Fatal(e)
	}
	tool(map[string]any{"action": "read", "inbox_id": inbox}, true)
	tool(map[string]any{"action": "ack", "message_id": id, "delivery": "delivered"}, true)
}

// A Windows hook chat is online but cannot wake itself. Stored receipts must
// describe the observed route without inventing admission from the stream.
func TestStoredAgentReceiptFollowsRoute(t *testing.T) {
	a := namedMessageAgent{ID: "route-receipt-fixture", Name: "worker", Attachment: &namedAgentAttachment{ID: "route-lease", DeliveryContract: 2}}
	agentLiveEvents.Lock()
	agentLiveEvents.online[a.ID] = &agentLiveStream{attachmentID: a.Attachment.ID}
	agentLiveEvents.Unlock()
	t.Cleanup(func() { agentLiveEvents.Lock(); delete(agentLiveEvents.online, a.ID); agentLiveEvents.Unlock() })
	for _, tc := range []struct{ route, text string }{
		{"codex_hook", "Stored; worker gets it at its next step."},
		{"", "Stored; worker gets it when it next checks."},
		{"inbox", "Stored; worker gets it when it next checks."},
		{"codex_desktop", "Sent to worker's open chat; waiting for worker to pick it up."},
	} {
		a.Attachment.DeliveryRoute = tc.route
		state, text := receiptState(agentMessage{Delivery: "stored"}, &a, true)
		if state != "sent" || text != tc.text {
			t.Fatalf("route %q: %s %s", tc.route, state, text)
		}
		if state, _ = receiptState(agentMessage{Delivery: "delivered"}, &a, true); state != "delivered" {
			t.Fatal("route changed a real receipt")
		}
	}
}
