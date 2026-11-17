package agentworksclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// #2955: queue uses the same exact native identity and send routing as ask,
// but preserves non-steering intent as data through the helper boundary.
func TestPluginQueuePreservesIntentAndBoundIdentity(t *testing.T) {
	// #3040: normalizing ask preserved inbox_id, but the mailbox HTTP send
	// silently dropped it and let the server reuse an older same-peer thread.
	actions := make(chan any, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Arguments["inbox_id"] != "inbox-current" || body.Arguments["ref"] != "#1" {
			t.Errorf("lost explicit thread or request intent: %v", body.Arguments)
		}
		actions <- body.Arguments["action"]
		_, _ = w.Write([]byte(`{"accepted":true,"inbox_id":"inbox-current"}`))
	}))
	defer server.Close()
	client, err := New(server.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	bridge := &localBridge{}
	session := &localSession{client: client, Attachment: AgentAttachment{ID: "own-attachment", AgentName: "sender"}}
	for _, action := range []string{"send", "ask", "queue"} {
		in, err := messagingRequest(LocalBridgeRequest{Provider: "codex", SessionID: "own-session", HostKey: "own-host", AttachmentID: "own-attachment", Arguments: map[string]any{"action": action, "target": "agent:recipient", "inbox_id": "inbox-current", "message": "new work", "submission_id": "stable", "provider": "forged", "session_id": "other", "status": "in_progress", "ref": "#1"}})
		if err != nil {
			t.Fatal(err)
		}
		if in.Action != "send" || in.Queue != (action == "queue") || in.Ask != (action == "ask") || in.Provider != "codex" || in.SessionID != "own-session" || in.AttachmentID != "own-attachment" {
			t.Fatal("queue routing or native identity changed", in.Action)
		}
		out := bridge.mailboxCall(context.Background(), session, in, LocalBridgeReply{})
		if out.Error != "" {
			t.Fatal(out.Error)
		}
		if got := <-actions; got != action {
			t.Fatalf("sent %v, want %s", got, action)
		}
		// #2965: the Mac helper used to drop status, so every Mac reply closed the task.
		if in.Task["status"] != "in_progress" || in.Task["ref"] != "#1" {
			t.Fatal("task fields were dropped", in.Task)
		}
	}
}
