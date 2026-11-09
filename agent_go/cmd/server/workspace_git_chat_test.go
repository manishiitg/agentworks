package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"

	"testing"
	"time"

	chatevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	mcpevents "github.com/manishiitg/mcpagent/events"
)

func TestWorkspaceGitChatUsageRequiresPrivateChatAccess(t *testing.T) {
	_, _ = gitTestEnv(t)
	api, store := newDurableChatAPI(t, "chat-private", "bob")
	store.AddEvent("chat-private", chatevents.Event{ID: "tool", Type: "tool_call_start", Timestamp: time.Now(), Data: &mcpevents.AgentEvent{Type: mcpevents.ToolCallStart, Data: &mcpevents.ToolCallStartEvent{ToolParams: mcpevents.ToolParams{Arguments: `{"path":"app/a.txt"}`}}}})
	request := func(user, session string) (int, map[string]any) {
		query := url.Values{"workspace_path": {gitTestProject}, "session_id": {session}, "op": {"status"}}
		req := sharedSecretsRequest(http.MethodGet, "/api/workspace-git?"+query.Encode(), user, nil)
		w := httptest.NewRecorder()
		requireWorkflowReadAccessCallerRelative(api.handleWorkspaceGit)(w, req)
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body
	}
	// Alice owns the files, but that alone cannot reveal Bob's private tool usage.
	if code, body := request("alice", "chat-private"); code != http.StatusOK || body["repos"] == nil || body["repos"].([]any)[0].(map[string]any)["used_in_chat"] != nil {
		t.Fatalf("private chat metadata exposed: %d %v", code, body)
	}
	_, plain := request("alice", "")
	for _, session := range []string{"chat-private", "missing-chat"} {
		code, body := request("alice", session)
		want, _ := json.Marshal(plain)
		got, _ := json.Marshal(body)
		if code != http.StatusOK || string(got) != string(want) {
			t.Fatalf("optional inaccessible chat differs: %d %v", code, body)
		}
	}
	// Same file owner with their own durable chat is allowed.
	allowed, allowedStore := newDurableChatAPI(t, "chat-private", "alice")
	allowedStore.AddEvent("chat-private", chatevents.Event{ID: "tool-allowed", Type: "tool_call_start", Timestamp: time.Now(), Data: &mcpevents.AgentEvent{Type: mcpevents.ToolCallStart, Data: &mcpevents.ToolCallStartEvent{ToolParams: mcpevents.ToolParams{Arguments: `{"path":"app/a.txt"}`}}}})
	api = allowed
	if code, body := request("alice", "chat-private"); code != http.StatusOK || body["repos"] == nil || body["repos"].([]any)[0].(map[string]any)["used_in_chat"] != true {
		t.Fatalf("owner status = %d %v", code, body)
	}
}
