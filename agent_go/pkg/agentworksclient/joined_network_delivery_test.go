package agentworksclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// Two real streams feed one exact native chat. A joined-network recovery read
// reaches that network through the MCP tool and keeps receipt routing there.
func TestJoinedNetworksDeliverAndRecoverInTheSameChat(t *testing.T) {
	if err := localIPCSupported(); err != nil {
		t.Skip(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var primaryReads, partnerReads atomic.Int32
	var primarySends, partnerSends atomic.Int32
	var partnerAttaches atomic.Int32
	var duplicateName, refusePartner, failPrimaryDirectory atomic.Bool
	var invalidPrimaryDirectory, primaryReplyFailure atomic.Int32
	incompleteDirectories := []string{`{}`, `null`, `{"agents":null}`}
	remote := func(network string, reads *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := "message-" + network
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"type\":\"connected\",\"attachment_id\":%q}\n\n", r.URL.Query().Get("attachment_id"))
				fmt.Fprintf(w, "data: {\"type\":\"inbox_changed\",\"attachment_id\":%q,\"sequence\":1}\n\n", r.URL.Query().Get("attachment_id"))
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				return
			}
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			args, _ := in["arguments"].(map[string]any)
			if args == nil {
				args = in
			}
			switch args["action"] {
			case "attach":
				if network == "partner" {
					partnerAttaches.Add(1)
				}
				if args["attachment_id"] != "lease-"+network {
					t.Error("joined recovery used another network's attachment fence")
				}
				json.NewEncoder(w).Encode(AgentAttachment{ID: "lease-" + network, AgentID: "agent-" + network, AgentName: "receiver", Provider: "codex", SessionID: "11111111-2222-4333-8444-555555555555"})
			case "list_agents":
				if mode := invalidPrimaryDirectory.Load(); network == "primary" && mode > 0 {
					fmt.Fprint(w, incompleteDirectories[mode-1])
					return
				}
				if network == "primary" && failPrimaryDirectory.Load() {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				agents := []map[string]any{}
				if network == "partner" || duplicateName.Load() {
					agents = append(agents, map[string]any{"id": "agent:reviewer", "agent_name": "reviewer"})
				}
				json.NewEncoder(w).Encode(map[string]any{"agents": agents})
			case "send":
				if inbox, _ := args["inbox_id"].(string); inbox != "" && inbox != "inbox-"+network {
					if network == "primary" && primaryReplyFailure.Load() != 0 {
						w.WriteHeader(http.StatusServiceUnavailable)
						fmt.Fprint(w, `{"error":{"code":"unavailable","message":"uncertain send"}}`)
					} else {
						w.WriteHeader(http.StatusNotFound)
						fmt.Fprint(w, `{"error":{"code":"not_found","message":"inbox unavailable"}}`)
					}
					return
				}
				if args["target"] != "agent:reviewer" && args["inbox_id"] != "inbox-"+network {
					t.Error("send used the wrong target", args["target"])
				}
				if network == "partner" {
					partnerSends.Add(1)
					if refusePartner.Load() {
						w.Header().Set("X-Request-ID", "refused-partner-request")
						w.WriteHeader(http.StatusNotFound)
						fmt.Fprint(w, `{"error":{"code":"not_found","message":"named agent unavailable or access denied"}}`)
						return
					}
				} else {
					primarySends.Add(1)
				}
				json.NewEncoder(w).Encode(map[string]any{"message_id": "new-message-" + network, "accepted": true})
			case "inbox":
				json.NewEncoder(w).Encode(map[string]any{"messages": []map[string]any{{"attachment_id": args["attachment_id"], "message_id": id, "inbox_id": "inbox-" + network, "message": "Review", "sequence": 1}}, "next_cursor": 1, "pending": 1})
			case "detach":
				t.Error("lifecycle offline closed the joined server lease")
				fmt.Fprint(w, `{}`)
			case "validate":
				json.NewEncoder(w).Encode(map[string]any{"message_ids": []string{id}})
			case "read":
				reads.Add(1)
				if inbox, _ := args["inbox_id"].(string); inbox != "" && inbox != "inbox-"+network {
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprint(w, `{"error":{"code":"not_found","message":"inbox unavailable"}}`)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"messages": []map[string]any{{"message_id": id, "inbox_id": "inbox-" + network, "message": "Review"}}, "next_cursor": 1})
			case "ack":
				json.NewEncoder(w).Encode(map[string]any{"delivery": "delivered"})
			default:
				json.NewEncoder(w).Encode(map[string]any{})
			}
		}))
	}
	primary := remote("primary", &primaryReads)
	defer primary.Close()
	partner := remote("partner", &partnerReads)
	defer partner.Close()
	root, err := os.MkdirTemp("", "aw-networks-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	session := "11111111-2222-4333-8444-555555555555"
	makeSession := func(url, network string) *localSession {
		config := filepath.Join(root, network+".json")
		if err := SaveConfig(config, Config{Server: url, Token: "fixture"}); err != nil {
			t.Fatal(err)
		}
		return &localSession{Attachment: AgentAttachment{ID: "lease-" + network, AgentID: "agent-" + network, AgentName: "receiver", Provider: "codex", SessionID: session}, HostKey: "host", AccountSenders: true, ConfigPath: config, ParentConfigPath: config}
	}
	own, extra := makeSession(primary.URL, "primary"), makeSession(partner.URL, "partner")
	extra.Also, extra.Network, extra.ServerClosed = true, "partner", true
	key := localBridgeKey("codex", session)
	if err := atomicLiveJSON(filepath.Join(root, "sessions.json"), map[string]*localSession{key: own, localAlsoKey(key, extra.ParentConfigPath): extra}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- ServeLocalBridge(ctx, root, LocalBridgeOptions{}) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	req := LocalBridgeRequest{Action: "status", Provider: "codex", SessionID: session, HostKey: "host"}
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, e := CallLocalBridge(ctx, root, req)
		if e == nil && len(out.Networks) == 1 && out.Networks[0].Online {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("joined stream did not connect: %+v %v", out, e)
		}
		time.Sleep(10 * time.Millisecond)
	}
	peers, err := CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "peers", Provider: "codex", SessionID: session, HostKey: "host"})
	if err != nil || len(peers.Directories) != 1 || peers.Directories[0].Server != "partner" {
		t.Fatalf("joined network disappeared from peers: %+v %v", peers, err)
	}
	req.Action, req.Transport = "next", "codex_queue"
	batch, err := CallLocalBridge(ctx, root, req)
	if err != nil || len(batch.Messages) != 2 {
		t.Fatalf("both streams did not reach the same chat: %+v %v", batch, err)
	}
	found := map[string]bool{}
	for _, m := range batch.Messages {
		found[m.MessageID] = true
		if m.MessageID == "message-partner" && m.Network != "partner" {
			t.Fatal("joined origin missing")
		}
	}
	if !found["message-primary"] || !found["message-partner"] {
		t.Fatal("missing network message")
	}
	plugin, err := NewPluginMCPServer(ctx, root, "codex", "host", false, nil, session)
	if err != nil {
		t.Fatal(err)
	}
	out, e := plugin.GetTool(PluginToolName).Handler(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"action": "read", "network": "partner", "inbox_id": "inbox-partner"}}})
	if e != nil || out.IsError {
		t.Fatalf("MCP recovery failed: %+v %v", out, e)
	}
	if primaryReads.Load() != 0 || partnerReads.Load() != 1 {
		t.Fatalf("wrong read network: primary=%d partner=%d", primaryReads.Load(), partnerReads.Load())
	}
	var reply LocalBridgeReply
	if err := json.Unmarshal([]byte(out.Content[0].(mcp.TextContent).Text), &reply); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Network string `json:"network"`
	}
	if json.Unmarshal(reply.Result, &result) != nil || result.Network != "partner" {
		t.Fatal("read response did not identify its network")
	}
	// Background recovery must replace a copied primary fence with the joined
	// network's own saved fence before its actual HTTP attachment request.
	if _, err := CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "offline", Provider: "codex", SessionID: session, HostKey: "host", Network: "partner"}); err != nil {
		t.Fatal(err)
	}
	joined := LocalBridgeRequest{Action: "attach", Provider: "codex", SessionID: session, HostKey: "host", Also: true, Automatic: true, ConfigPath: extra.ParentConfigPath, AgentName: extra.Attachment.AgentName, AttachmentID: "stale-primary-lease"}
	if out, err := CallLocalBridge(ctx, root, joined); err != nil || !out.Attached || partnerAttaches.Load() != 1 {
		t.Fatalf("joined automatic recovery failed: %+v %v", out, err)
	}
	// New conversations must reach the same joined directory that lists the
	// agent, including the network argument of the single exposed MCP tool.
	send := func(target, network, submission string) *mcp.CallToolResult {
		t.Helper()
		result, err := plugin.GetTool(PluginToolName).Handler(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"action": "send", "target": target, "network": network, "message": "review request", "submission_id": submission}}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	checkNetwork := func(result *mcp.CallToolResult, network string) {
		t.Helper()
		if result.IsError {
			t.Fatalf("send failed: %+v", result)
		}
		var reply LocalBridgeReply
		if json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &reply) != nil {
			t.Fatal("invalid bridge reply")
		}
		var sent struct {
			Network string `json:"network"`
		}
		if json.Unmarshal(reply.Result, &sent) != nil || sent.Network != network {
			t.Fatalf("send did not identify selected network: %s", reply.Result)
		}
	}
	checkNetwork(send("agent:reviewer", "", "unique-name"), "partner")
	checkNetwork(send("agent:reviewer", "partner", "explicit-network"), "partner")
	if primarySends.Load() != 0 || partnerSends.Load() != 2 {
		t.Fatal("a joined first send went to the primary network")
	}
	duplicateName.Store(true)
	if ambiguous := send("agent:reviewer", "", "ambiguous-name"); !ambiguous.IsError || !strings.Contains(ambiguous.Content[0].(mcp.TextContent).Text, "more than one network") {
		t.Fatal("ambiguous agent was sent to without selecting its network")
	}
	checkNetwork(send("agent:reviewer", primary.URL, "explicit-primary"), primary.URL)
	checkNetwork(send("agent:reviewer@partner", "", "qualified-network"), "partner")
	refusePartner.Store(true)
	refused := send("agent:reviewer", "partner", "refused-no-retry")
	if !refused.IsError || !strings.Contains(refused.Content[0].(mcp.TextContent).Text, "network partner refused") || !strings.Contains(refused.Content[0].(mcp.TextContent).Text, "refused-partner-request") {
		t.Fatal("refusal lost its network or request correlation")
	}
	if unknown := send("agent:reviewer", "not-joined", "unknown-network"); !unknown.IsError {
		t.Fatal("an unjoined network was accepted")
	}
	for i, body := range incompleteDirectories {
		invalidPrimaryDirectory.Store(int32(i + 1))
		invalid := send("agent:reviewer", "", fmt.Sprintf("invalid-directory-%d", i))
		if !invalid.IsError || !strings.Contains(invalid.Content[0].(mcp.TextContent).Text, "Invalid agent directory") || primarySends.Load() != 1 || partnerSends.Load() != 4 {
			t.Fatalf("incomplete directory %s allowed a send", body)
		}
	}
	invalidPrimaryDirectory.Store(0)
	failPrimaryDirectory.Store(true)
	if unavailable := send("agent:reviewer", "", "incomplete-directories"); !unavailable.IsError || !strings.Contains(unavailable.Content[0].(mcp.TextContent).Text, "no message was sent") {
		t.Fatal("an incomplete lookup guessed a destination")
	}
	if primarySends.Load() != 1 || partnerSends.Load() != 4 {
		t.Fatalf("an ambiguous/refused send was replayed on another network: primary=%d partner=%d", primarySends.Load(), partnerSends.Load())
	}
	refusePartner.Store(false)
	// A reply still finds its original server after the helper's read position
	// advances, with no local inbox/body journal and no explicit network hint.
	readsBeforeReply := primaryReads.Load() + partnerReads.Load()
	resultReply, err := plugin.GetTool(PluginToolName).Handler(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"action": "reply", "inbox_id": "inbox-partner", "message": "review completed", "submission_id": "inbox-reply"}}})
	if err != nil || resultReply.IsError || primarySends.Load() != 1 || partnerSends.Load() != 5 {
		t.Fatalf("inbox reply lost its server: %+v %v", resultReply, err)
	}
	primaryReplyFailure.Store(1)
	uncertainReply, err := plugin.GetTool(PluginToolName).Handler(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"action": "reply", "inbox_id": "inbox-partner", "message": "must not replay", "submission_id": "uncertain-inbox-reply"}}})
	if err != nil || !uncertainReply.IsError || primarySends.Load() != 1 || partnerSends.Load() != 5 {
		t.Fatalf("an uncertain reply was sent on another network: %+v %v", uncertainReply, err)
	}
	primaryReplyFailure.Store(0)
	missingReply, err := plugin.GetTool(PluginToolName).Handler(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"action": "reply", "inbox_id": "inbox-missing", "message": "must not send", "submission_id": "missing-inbox-reply"}}})
	if err != nil || !missingReply.IsError || primarySends.Load() != 1 || partnerSends.Load() != 5 {
		t.Fatalf("an unknown inbox accepted a reply: %+v %v", missingReply, err)
	}
	if primaryReads.Load()+partnerReads.Load() != readsBeforeReply {
		t.Fatal("reply routing downloaded historical message bodies")
	}
}
