package agentworksclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	remote := func(network string, reads *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := "message-" + network
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"type\":\"connected\",\"attachment_id\":%q}\n\n", r.URL.Query().Get("attachment_id"))
				fmt.Fprintf(w, "data: {\"type\":\"message\",\"attachment_id\":%q,\"message_id\":%q,\"inbox_id\":%q,\"sequence\":1,\"message\":\"Review\",\"sent_at\":%q}\n\n", r.URL.Query().Get("attachment_id"), id, "inbox-"+network, time.Now().UTC().Format(time.RFC3339Nano))
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
			case "validate":
				json.NewEncoder(w).Encode(map[string]any{"message_ids": []string{id}})
			case "read":
				reads.Add(1)
				json.NewEncoder(w).Encode(map[string]any{"messages": []map[string]any{{"message_id": id, "inbox_id": "inbox-" + network, "message": "Review"}}, "next_cursor": 1})
			case "ack":
				json.NewEncoder(w).Encode(map[string]any{"delivery": "processed"})
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
		return &localSession{Attachment: AgentAttachment{ID: "lease-" + network, AgentID: "agent-" + network, AgentName: "receiver", Provider: "codex", SessionID: session}, HostKey: "host", AccountSenders: true, ConfigPath: config, ParentConfigPath: config, Offers: map[string]localOffer{}}
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
		if e == nil && len(out.Networks) == 1 && out.Networks[0].Online && out.Pending == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("joined stream did not connect: %+v %v", out, e)
		}
		time.Sleep(10 * time.Millisecond)
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
}
