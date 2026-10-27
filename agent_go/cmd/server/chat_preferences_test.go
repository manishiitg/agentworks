package server

import (
	"net/http"
	"testing"
)

// A display choice applies only to the authenticated caller. The same MCP
// handler backs Models/Builder chat; there is no target-user selector.
func TestChatContextDisplayBelongsToTheCaller(t *testing.T) {
	f := newExternalToolsFixture(t)
	if body := externalTestBody(t, f.call(t, "owner", "settings", map[string]any{"action": "chat"}), http.StatusOK); body["show_context_indicator"] != false {
		t.Fatal("context is not off by default")
	}
	if body := externalTestBody(t, f.call(t, "owner", "settings", map[string]any{"action": "chat", "show_context_indicator": true}), http.StatusOK); body["show_context_indicator"] != true {
		t.Fatal("choice was not saved")
	}
	if body := externalTestBody(t, f.call(t, "owner", "settings", map[string]any{"action": "chat"}), http.StatusOK); body["show_context_indicator"] != true {
		t.Fatal("saved choice was lost")
	}
	if body := externalTestBody(t, f.call(t, "reader", "settings", map[string]any{"action": "chat"}), http.StatusOK); body["show_context_indicator"] != false {
		t.Fatal("another person inherited the choice")
	}
	externalTestBody(t, f.call(t, "owner", "settings", map[string]any{"action": "chat", "user_id": "reader", "show_context_indicator": false}), http.StatusBadRequest)
	externalTestBody(t, f.call(t, "", "settings", map[string]any{"action": "chat"}), http.StatusUnauthorized)
}
