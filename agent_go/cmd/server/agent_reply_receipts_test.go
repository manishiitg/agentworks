package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/mark3labs/mcp-go/mcp"
)

// The production OAuth, workspace store, Unix helper and MCP handler reproduce
// mailbox recovery without a native offer, then a reply after local cleanup.
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
	call(sender, map[string]any{"action": "register", "agent_name": "sender"})
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
	tool(map[string]any{"action": "ack", "message_id": id, "delivery": "processed"}, true)
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
	ack := map[string]any{"action": "ack", "message_id": id, "delivery": "processed"}
	tool(ack, false)
	first := saved()
	if first.Delivery != "processed" || first.AdmittedAt.IsZero() || first.ProcessedAt.IsZero() || first.LiveTransport != "mailbox" {
		t.Fatalf("single processed ack: %+v", first)
	}
	tool(ack, false)
	ack["delivery"] = "admitted"
	tool(ack, false)
	last := saved()
	if last.Delivery != "processed" || !last.AdmittedAt.Equal(first.AdmittedAt) || !last.ProcessedAt.Equal(first.ProcessedAt) {
		t.Fatal("retry changed the receipt or its times")
	}
	cleanup := base
	cleanup.Action = "cleanup"
	if _, e := agentworksclient.CallLocalBridge(ctx, root, cleanup); e != nil {
		t.Fatal(e)
	}
	reply := tool(map[string]any{"action": "reply", "message_id": id, "message": "Reviewed", "submission_id": "review-answer"}, false)
	var answer map[string]any
	if json.Unmarshal(reply.Result, &answer) != nil || answer["inbox_id"] != inbox {
		t.Fatal("reply after cleanup lost its original conversation")
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
	for _, profile := range codeproduct.BuiltinAgentProfiles() {
		profile.Product = codeproduct.ProfileID
		if e := env.svc.registry.RegisterProfile(profile); e != nil {
			t.Fatal(e)
		}
	}
	const hostedRoot = "_users/owner/Chats/Code/projects/example"
	const hostedKey = "example:chat:abcdef12"
	const hostedSession = "product-33333333-2222-4333-8444-555555555555"
	record := ProductConversationRecord{ProfileID: codeproduct.ProfileID, ConversationKey: hostedKey, SessionID: hostedSession, WorkspacePath: hostedRoot, ResourceID: "example"}
	registry, _ := json.Marshal(productConversationRegistryDocument{Version: 1, Entries: map[string]ProductConversationRecord{"code/" + hostedKey: record}})
	env.mock.mu.Lock()
	env.mock.files[hostedRoot+"/product.json"] = `{"schema_version":1,"product":"code","id":"example","owner_id":"owner","title":"Example","session_id":"project-main"}`
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
	tool(map[string]any{"action": "ack", "message_id": hostedID, "delivery": "processed"}, false)
	oldTurn := agentMessageTurn
	agentMessageTurn = func(_ *StreamingAPI, _ context.Context, request map[string]interface{}, session, _ string) (internalSessionTurnResult, error) {
		if session != hostedSession || request["agent_message_inbox"] != hostedInbox {
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
	for _, refusal := range []string{"agent_opened", "different_chat", "another_person", "status_only"} {
		conversation := original
		conversation.ID = "inbox-" + refusal
		incoming := original.Messages[0]
		incoming.ID = "msg-" + refusal
		conversation.Messages = []agentMessage{incoming}
		switch refusal {
		case "agent_opened":
			conversation.Endpoints[0], conversation.Endpoints[1] = conversation.Endpoints[1], conversation.Endpoints[0]
			conversation.Messages[0].From = 1
		case "different_chat":
			conversation.Endpoints[0].ChatKey = "session:" + hostedSession
		case "another_person":
			conversation.Endpoints[0].UserID = "other"
		case "status_only":
			conversation.Messages[0].Kind = "status"
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
		tool(map[string]any{"action": "reply", "message_id": incoming.ID, "message": "Must refuse", "submission_id": refusal}, true)
	}
	// Rotating the receiver lease makes the old MCP child's receipts fail.
	if _, e := parent.AttachAgent(ctx, agentworksclient.AgentAttachmentOptions{AgentName: "receiver", Provider: "codex", SessionID: "replacement-session"}); e != nil {
		t.Fatal(e)
	}
	tool(map[string]any{"action": "read", "inbox_id": inbox}, true)
	tool(map[string]any{"action": "ack", "message_id": id, "delivery": "processed"}, true)
}
