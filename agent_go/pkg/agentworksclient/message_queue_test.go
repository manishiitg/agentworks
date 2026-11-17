package agentworksclient

import "testing"

// #2955: queue uses the same exact native identity and send routing as ask,
// but preserves non-steering intent as data through the helper boundary.
func TestPluginQueuePreservesIntentAndBoundIdentity(t *testing.T) {
	for _, action := range []string{"send", "ask", "queue"} {
		in, err := messagingRequest(LocalBridgeRequest{Provider: "codex", SessionID: "own-session", HostKey: "own-host", AttachmentID: "own-attachment", Arguments: map[string]any{"action": action, "target": "agent:recipient", "message": "new work", "submission_id": "stable", "provider": "forged", "session_id": "other", "status": "in_progress", "ref": "#1"}})
		if err != nil {
			t.Fatal(err)
		}
		if in.Action != "send" || in.Queue != (action == "queue") || in.Ask != (action == "ask") || in.Provider != "codex" || in.SessionID != "own-session" || in.AttachmentID != "own-attachment" {
			t.Fatal("queue routing or native identity changed", in.Action)
		}
		// #2965: the Mac helper used to drop status, so every Mac reply closed the task.
		if in.Task["status"] != "in_progress" || in.Task["ref"] != "#1" {
			t.Fatal("task fields were dropped", in.Task)
		}
	}
}
