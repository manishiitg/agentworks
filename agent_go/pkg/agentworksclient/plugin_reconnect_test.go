package agentworksclient

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

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestCodexMessagingRestartsStoppedBridge(t *testing.T) {
	if err := localIPCSupported(); err != nil {
		t.Skip(err)
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/agents/events") {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		var body struct {
			Action  string `json:"action"`
			Name    string `json:"agent_name"`
			Session string `json:"native_session_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Action == "attach" {
			_ = json.NewEncoder(w).Encode(AgentAttachment{ID: "renewed", AgentID: "same-agent", AgentName: body.Name, Provider: "codex", SessionID: body.Session})
			return
		}
		_, _ = w.Write([]byte(`{"agents":[],"message_ids":[]}`))
	}))
	defer remote.Close()
	root, err := os.MkdirTemp("", "aw-helper")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	config := filepath.Join(root, "parent.json")
	if err = SaveConfig(config, Config{Server: remote.URL, Token: "fixture-token"}); err != nil {
		t.Fatal(err)
	}
	session, closed := uuid.NewString(), uuid.NewString()
	saved := map[string]*localSession{}
	for _, id := range []string{session, closed} {
		saved[localBridgeKey("codex", id)] = &localSession{Attachment: AgentAttachment{ID: "saved-lease", AgentID: "same-agent", AgentName: "saved-chat", Provider: "codex", SessionID: id}, HostKey: "prior-process", AccountSenders: true, Offline: true, ServerClosed: id == closed, ConfigPath: config, ParentConfigPath: config, Offers: map[string]localOffer{}}
	}
	// The closed chat is a different agent, not another holder of saved-chat.
	saved[localBridgeKey("codex", closed)].Attachment.ID = "closed-lease"
	saved[localBridgeKey("codex", closed)].Attachment.AgentID = "closed-agent"
	saved[localBridgeKey("codex", closed)].Attachment.AgentName = "closed-chat"
	if err = atomicLiveJSON(filepath.Join(root, "sessions.json"), saved); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var helpers []chan error
	defer func() {
		cancel()
		for _, done := range helpers {
			if done != nil {
				<-done
			}
		}
	}()
	starts := 0
	start := func(wait context.Context) error {
		starts++
		done := make(chan error, 1)
		helpers = append(helpers, done)
		go func() {
			done <- ServeLocalBridge(ctx, root, LocalBridgeOptions{HostAlive: func(string, string) bool { return true }})
		}()
		for {
			if _, e := CallLocalBridge(wait, root, LocalBridgeRequest{Action: "ping"}); e == nil {
				return nil
			}
			select {
			case <-wait.Done():
				return wait.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	call := func(id string) (LocalBridgeReply, bool) {
		plugin, e := NewPluginMCPServer(ctx, root, "codex", "current-process", false, start, id)
		if e != nil {
			t.Fatal(e)
		}
		result, e := plugin.ListTools()[PluginToolName].Handler(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"action": "peers"}}})
		if e != nil {
			t.Fatal(e)
		}
		var out LocalBridgeReply
		if result.IsError {
			t.Logf("messaging refusal: %v", result.Content)
		}
		if !result.IsError {
			if e = json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &out); e != nil {
				t.Fatal(e)
			}
		}
		return out, result.IsError
	}
	for _, id := range []string{uuid.NewString(), closed} {
		if _, failed := call(id); !failed || starts != 0 {
			t.Fatal("an unconnected or server-closed chat launched the helper")
		}
	}
	for round := 0; round < 2; round++ {
		if _, e := CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "peers", Provider: "codex", SessionID: session, HostKey: "current-process"}); e == nil {
			t.Fatal("fixture did not reproduce a stopped helper")
		}
		out, failed := call(session)
		if failed || !out.Attached || out.Attachment.AgentID != "same-agent" || out.Attachment.AgentName != "saved-chat" || starts != round+1 {
			t.Fatalf("saved chat did not recover after helper stop: %+v, starts=%d", out, starts)
		}
		// Another discovery process can see that the chat is connected, but
		// cannot use that metadata query to change the saved action fence.
		other := LocalBridgeRequest{Action: "connection", Provider: "codex", SessionID: session, HostKey: "another-process"}
		if state, e := CallLocalBridge(ctx, root, other); e != nil || !state.Attached {
			t.Fatalf("read-only connection discovery refused: %+v %v", state, e)
		}
		other.Action = "peers"
		if denied, e := CallLocalBridge(ctx, root, other); e != nil || denied.Attached || denied.Attachment.ID != "" || len(denied.Result) != 0 {
			t.Fatalf("connection discovery changed the saved provider fence: %+v %v", denied, e)
		}
		if round == 1 {
			_, err = CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "detach", Provider: "codex", SessionID: session, HostKey: "current-process"})
			if err != nil {
				t.Fatal(err)
			}
		}
		_, _ = CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "shutdown", ShutdownReason: "stop"})
		select {
		case e := <-helpers[len(helpers)-1]:
			helpers[len(helpers)-1] = nil
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("helper %d did not stop", round)
		}
	}
	if _, failed := call(session); !failed || starts != 2 {
		t.Fatal("explicit detach was revived after helper shutdown")
	}
}
