package agentworksclient

import (
	"context"
	"encoding/json"
	"github.com/mark3labs/mcp-go/mcp"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// The same MCP child forwards a changed helper contract without freezing an
// action or receipt enum, dropping new fields, or accepting a caller's identity.
func TestPluginExposesOneToolWithActions(t *testing.T) {
	if err := localIPCSupported(); err != nil {
		t.Skip(err)
	}
	root, err := os.MkdirTemp("", "aw-mcp-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	listener, err := net.Listen("unix", filepath.Join(root, "bridge.sock"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(filepath.Join(root, "bridge.sock"), 0600); err != nil {
		t.Fatal(err)
	}
	received := make(chan LocalBridgeRequest, 2)
	helper := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request LocalBridgeRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		received <- request
		json.NewEncoder(w).Encode(LocalBridgeReply{Attached: true})
	})}
	go helper.Serve(listener)
	defer helper.Close()
	session := "11111111-2222-4333-8444-555555555555"
	s, err := NewPluginMCPServer(context.Background(), root, "codex", "native-host", false, nil, session)
	if err != nil {
		t.Fatal(err)
	}
	tools := s.ListTools()
	if len(tools) != 1 || tools[PluginToolName] == nil {
		t.Fatal("expected one tool")
	}
	tool := tools[PluginToolName]
	for _, property := range []string{"action", "delivery"} {
		schema := tool.Tool.InputSchema.Properties[property].(map[string]any)
		if _, frozen := schema["enum"]; frozen {
			t.Fatalf("%s freezes the helper contract", property)
		}
	}
	for _, args := range []map[string]any{
		{"action": "ack", "message_id": "m1", "delivery": "delivered"},
		{"action": "new_helper_action", "new_field": map[string]any{"value": "kept"}, "provider": "other", "session_id": "other", "host_key": "other"},
	} {
		out, err := tool.Handler(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}})
		if err != nil || out.IsError {
			t.Fatalf("helper call failed: %+v %v", out, err)
		}
		forwarded := <-received
		if forwarded.Action != "message" || forwarded.Provider != "codex" || forwarded.SessionID != session || forwarded.HostKey != "native-host" {
			t.Fatalf("unbound helper request: %+v", forwarded)
		}
		got, _ := json.Marshal(forwarded.Arguments)
		want, _ := json.Marshal(args)
		if string(got) != string(want) {
			t.Fatal("messaging JSON changed")
		}
	}
}

func TestMessagingArgumentsKeepNativeIdentityAndFilterIntent(t *testing.T) {
	bound := LocalBridgeRequest{Provider: "codex", SessionID: "native-chat", HostKey: "native-host", AttachmentID: "native-lease", Arguments: map[string]any{"action": "ack", "delivery": "delivered", "message_id": "m1", "session_id": "other", "host_key": "other", "attachment_id": "other", "config_path": "/other", "transport": "codex_hook"}}
	got, err := messagingRequest(bound)
	if err != nil || got.Provider != bound.Provider || got.SessionID != bound.SessionID || got.HostKey != bound.HostKey || got.AttachmentID != bound.AttachmentID || got.ConfigPath != "" || got.Transport != "" {
		t.Fatalf("message arguments changed native authority: %+v %v", got, err)
	}
	bound.Arguments = map[string]any{"action": "clear", "delivered_only": "true"}
	if _, err := messagingRequest(bound); err == nil {
		t.Fatal("bad filter became an unfiltered clear")
	}
	bound.Arguments = map[string]any{"action": "attach"}
	if _, err := messagingRequest(bound); err == nil {
		t.Fatal("messaging selected a connection action")
	}
}
