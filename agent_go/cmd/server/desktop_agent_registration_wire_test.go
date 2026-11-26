package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// #2980: the Mac updates before the server. The old server accepts extra
// registration-envelope fields, but its strict runtime-info decoder rejects
// an unknown nested key. Exercise the actual outbound registration payload.
func TestDesktopAppRegistrationKeepsMacFirstWireContract(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var args map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		var info map[string]json.RawMessage
		if err := json.Unmarshal(args["runtime_info"], &info); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if info["chat_open"] != nil {
			t.Error("new nested key breaks old server decoder")
			w.WriteHeader(400)
			return
		}
		if string(args["chat_open"]) != "true" || string(info["activity"]) != `"Idle"` {
			t.Error("lost current-tab assertion or activity")
			w.WriteHeader(400)
			return
		}
		var name string
		_ = json.Unmarshal(args["agent_name"], &name)
		_ = json.NewEncoder(w).Encode(map[string]string{"agent_id": "agent-fixture", "agent_name": name, "attachment_id": "attachment-fixture"})
	}))
	defer remote.Close()
	record := ProductConversationRecord{ConversationID: "current-tab", ResourceID: "alpha", runtimeInfo: &agentRuntimeInfo{ChatOpen: true, Activity: "Idle"}}
	out, err := registerDesktopAppAgent(t.Context(), "owner", desktopConnection{ID: "fixture", Server: remote.URL}, record)
	if err != nil || out.ID != "agent-fixture" {
		t.Fatalf("Mac-first registration failed: %v %v", out, err)
	}
}
