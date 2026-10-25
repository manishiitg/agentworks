package server

import (
	"net/http"
	"testing"
)

// A display choice applies only to the authenticated caller. The same MCP
// handler backs Models/Builder chat; there is no target-user selector.
func TestChatContextDisplayBelongsToTheCaller(t *testing.T) {
	f := newExternalToolsFixture(t)
	if body := externalTestBody(t, f.call(t, "owner", "chat_preferences", map[string]any{}), http.StatusOK); body["show_context_indicator"] != true {
		t.Fatal("context is not on by default")
	}
	if body := externalTestBody(t, f.call(t, "owner", "chat_preferences", map[string]any{"show_context_indicator": false}), http.StatusOK); body["show_context_indicator"] != false {
		t.Fatal("choice was not saved")
	}
	if body := externalTestBody(t, f.call(t, "owner", "chat_preferences", map[string]any{}), http.StatusOK); body["show_context_indicator"] != false {
		t.Fatal("saved choice was lost")
	}
	if body := externalTestBody(t, f.call(t, "reader", "chat_preferences", map[string]any{}), http.StatusOK); body["show_context_indicator"] != true {
		t.Fatal("another person inherited the choice")
	}
	externalTestBody(t, f.call(t, "owner", "chat_preferences", map[string]any{"user_id": "reader", "show_context_indicator": false}), http.StatusBadRequest)
	externalTestBody(t, f.call(t, "", "chat_preferences", map[string]any{}), http.StatusUnauthorized)
}
