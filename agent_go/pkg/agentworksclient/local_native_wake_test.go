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
)

// Real local IPC plus HTTP metadata: a dormant chat needs one shared login
// connection, while stopped, replaced and unrelated records never open.
func TestPendingMailWakesOnlyItsSavedNativeChat(t *testing.T) {
	var streams, opens atomic.Int32
	hints := make(chan AgentLiveMessage, 12)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/external/v1/agents/events" {
			if r.URL.Query().Get("agent_name") != "" {
				t.Error("dormant record opened its own stream")
				return
			}
			streams.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"connected\"}\n\n")
			w.(http.Flusher).Flush()
			for {
				select {
				case <-r.Context().Done():
					return
				case hint := <-hints:
					raw, _ := json.Marshal(map[string]any{"type": "inbox_changed", "agent_id": hint.AgentID, "attachment_id": hint.AttachmentID, "sequence": hint.Sequence})
					fmt.Fprintf(w, "data: %s\n\n", raw)
					w.(http.Flusher).Flush()
				}
			}
		}
		var args map[string]any
		_ = json.NewDecoder(r.Body).Decode(&args)
		if args["action"] == "inspect" {
			name, _ := args["agent_name"].(string)
			_ = json.NewEncoder(w).Encode(map[string]any{"agent_id": "agent-" + name, "attachment": map[string]any{"attachment_id": "lease-" + name}})
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer remote.Close()
	root, err := os.MkdirTemp("", "aw-wake-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	config := filepath.Join(root, "parent.json")
	if err = SaveConfig(config, Config{Server: remote.URL, Token: "fixture-token"}); err != nil {
		t.Fatal(err)
	}
	seed := func(name string) *localSession {
		return &localSession{Attachment: AgentAttachment{ID: "lease-" + name, AgentID: "agent-" + name, AgentName: name, Provider: "codex", SessionID: name}, Offline: true, AccountSenders: true, ConfigPath: config, ParentConfigPath: config, NativeRemote: "desktop+unix:///fixture.sock"}
	}
	saved := map[string]*localSession{}
	for _, name := range []string{"target", "stopped", "moved", "closed"} {
		saved[localBridgeKey("codex", name)] = seed(name)
	}
	saved[localBridgeKey("codex", "closed")].ServerClosed = true
	if err = atomicLiveJSON(filepath.Join(root, "sessions.json"), saved); err != nil {
		t.Fatal(err)
	}
	if err = atomicLiveJSON(filepath.Join(root, "disconnected.json"), map[string]bool{localBridgeKey("codex", "stopped"): true}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	opened := make(chan AgentAttachment, 4)
	go func() {
		done <- ServeLocalBridge(ctx, root, LocalBridgeOptions{NativeWake: func(ctx context.Context, a AgentAttachment, _ string) error {
			// A real own-chat IPC round trip proves no helper lock is held.
			_, err := CallLocalBridge(ctx, root, LocalBridgeRequest{Action: "ping"})
			if err != nil {
				return err
			}
			opens.Add(1)
			opened <- a
			return nil
		}})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for streams.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if streams.Load() != 1 {
		t.Fatal("did not establish exactly one login stream", streams.Load())
	}
	for _, h := range []AgentLiveMessage{{AgentID: "agent-unrelated", AttachmentID: "lease-unrelated", Sequence: 1}, {AgentID: "agent-stopped", AttachmentID: "lease-stopped", Sequence: 2}, {AgentID: "agent-moved", AttachmentID: "replacement-lease", Sequence: 3}, {AgentID: "agent-closed", AttachmentID: "lease-closed", Sequence: 4}, {AgentID: "agent-target", AttachmentID: "lease-target", Sequence: 5}} {
		hints <- h
	}
	select {
	case a := <-opened:
		if a.AgentName != "target" || a.SessionID != "target" {
			t.Fatal("opened a different chat", a)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pending target never opened")
	}
	hints <- AgentLiveMessage{AgentID: "agent-target", AttachmentID: "lease-target", Sequence: 6}
	select {
	case a := <-opened:
		t.Fatal("burst reopened a native chat", a)
	case <-time.After(100 * time.Millisecond):
	}
	if opens.Load() != 1 || streams.Load() != 1 {
		t.Fatalf("opens=%d streams=%d", opens.Load(), streams.Load())
	}
}
