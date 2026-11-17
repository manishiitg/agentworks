package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// The actual MCP call reaches only the resolver's native session, not an ID or
// path in the model's arguments. Only the local helper boundary is replaced.
func TestCrewJoinMCPExactSession(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("Unix helper transport")
			}
			root := t.TempDir()
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(root, "bridge.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Chmod(socket, 0600); err != nil {
				t.Fatal(err)
			}
			calls := make(chan agentworksclient.LocalBridgeRequest, 2)
			boundary := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var in agentworksclient.LocalBridgeRequest
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					http.Error(w, "invalid", 400)
					return
				}
				calls <- in
				_ = json.NewEncoder(w).Encode(agentworksclient.LocalBridgeReply{Attached: true, Result: json.RawMessage(`{"copy":{"path":"/accepted/team","folders":["team"]},"outcome":"join"}`)})
			})}
			go func() { _ = boundary.Serve(listener) }()
			defer boundary.Close()
			s := server.NewMCPServer("team-fixture", "1", server.WithToolCapabilities(false))
			resolved := true
			addTerminalTeamTools(s, root, provider, "exact-host", func(context.Context, mcp.CallToolRequest) (string, error) {
				if !resolved {
					return "", fmt.Errorf("native identity unavailable")
				}
				return "exact-native-session", nil
			})
			client, err := mcpclient.NewInProcessClient(s)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if err = client.Start(ctx); err != nil {
				t.Fatal(err)
			}
			init := mcp.InitializeRequest{}
			init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
			init.Params.ClientInfo = mcp.Implementation{Name: "fixture", Version: "1"}
			if _, err = client.Initialize(ctx, init); err != nil {
				t.Fatal(err)
			}
			listed, err := client.ListTools(ctx, mcp.ListToolsRequest{})
			if err != nil || len(listed.Tools) != 1 || listed.Tools[0].Name != "agentworks" {
				t.Fatalf("Team action bypassed the combined plugin tool: %v %v", listed, err)
			}
			request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "agentworks", Arguments: map[string]any{"action": "join", "project": "example", "session": "forged", "path": "/outside", "attachment_id": "forged"}}}
			result, err := client.CallTool(ctx, request)
			if err != nil || result == nil || result.IsError {
				t.Fatalf("join MCP refused: %v %v", result, err)
			}
			in := <-calls
			if in.Action != "crew_join" || in.Provider != provider || in.SessionID != "exact-native-session" || in.HostKey != "exact-host" || in.Arguments["project"] != "example" || in.Arguments["team_action"] != nil || in.Arguments["path"] != nil || in.Arguments["session"] != nil || in.Arguments["attachment_id"] != nil {
				t.Fatalf("model changed native identity/path: %#v", in)
			}
			if len(result.Content) != 1 {
				t.Fatal("missing join confirmation")
			}
			text, ok := result.Content[0].(mcp.TextContent)
			if !ok || !strings.Contains(text.Text, "/accepted/team") {
				t.Fatal("server-returned working copy missing")
			}
			resolved = false
			result, err = client.CallTool(ctx, request)
			if err != nil || result == nil || !result.IsError {
				t.Fatal("missing native identity was not refused")
			}
			select {
			case <-calls:
				t.Fatal("unresolved native session reached the helper")
			default:
			}
		})
	}

}
