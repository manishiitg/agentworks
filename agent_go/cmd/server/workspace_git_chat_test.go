package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	chatevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	mcpevents "github.com/manishiitg/mcpagent/events"
)

func TestWorkspaceGitChatUsageUsesToolPathsNotDirtyFiles(t *testing.T) {
	base := t.TempDir()
	for _, dir := range []string{"api", "web space", "unrelated"} {
		if err := os.MkdirAll(filepath.Join(base, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(base, "unrelated", "escape")); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{eventStore: chatevents.NewEventStore(100)}
	t.Cleanup(api.eventStore.Stop)
	add := func(id, arguments string) {
		api.eventStore.AddEvent("chat", chatevents.Event{ID: id, Type: "tool_call_start", Timestamp: time.Now(), Data: &mcpevents.AgentEvent{Type: mcpevents.ToolCallStart, Data: &mcpevents.ToolCallStartEvent{ToolName: "Bash", ToolParams: mcpevents.ToolParams{Arguments: arguments}}}})
	}
	add("a", `{"command":"cd api && npm test"}`)
	add("b", `{"command":"git -C 'web space' status"}`)
	add("c", `{"file_path":"unrelated/escape/private.txt"}`)
	add("d", `{"command":"echo 'cd unrelated'"}`)
	repos := []workspaceGitRepo{{Root: "api"}, {Root: "web space"}, {Root: "unrelated", Files: []workspaceGitFile{{Path: "dirty.txt"}}}}
	api.markWorkspaceGitChatUsage("chat", base, repos)
	if !repos[0].UsedInChat || !repos[1].UsedInChat || repos[2].UsedInChat {
		t.Fatalf("tool usage = %+v", repos)
	}
	// A subsequent tool read adds a worktree; committing/resetting dirty files
	// does not erase navigation evidence or attribute other chats' edits.
	add("e", `{"workdir":"unrelated","file_path":"new.txt"}`)
	for i := range repos {
		repos[i].UsedInChat = false
		repos[i].Files = nil
	}
	api.markWorkspaceGitChatUsage("chat", base, repos)
	for _, repo := range repos {
		if !repo.UsedInChat {
			t.Fatalf("incremental usage lost: %+v", repos)
		}
	}
	other := []workspaceGitRepo{{Root: "api"}}
	api.markWorkspaceGitChatUsage("different-chat", base, other)
	if other[0].UsedInChat {
		t.Fatal("another chat inherited usage")
	}
}

func TestWorkspaceGitChatUsageRequiresPrivateChatAccess(t *testing.T) {
	_, _ = gitTestEnv(t)
	api, store := newDurableChatAPI(t, "chat-private", "bob")
	store.AddEvent("chat-private", chatevents.Event{ID: "tool", Type: "tool_call_start", Timestamp: time.Now(), Data: &mcpevents.AgentEvent{Type: mcpevents.ToolCallStart, Data: &mcpevents.ToolCallStartEvent{ToolParams: mcpevents.ToolParams{Arguments: `{"path":"app/a.txt"}`}}}})
	request := func(user string) (int, map[string]any) {
		query := url.Values{"workspace_path": {gitTestProject}, "session_id": {"chat-private"}, "op": {"status"}}
		req := sharedSecretsRequest(http.MethodGet, "/api/workspace-git?"+query.Encode(), user, nil)
		w := httptest.NewRecorder()
		requireWorkflowReadAccessCallerRelative(api.handleWorkspaceGit)(w, req)
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body
	}
	// Alice owns the files, but that alone cannot reveal Bob's private tool usage.
	if code, _ := request("alice"); code != http.StatusNotFound {
		t.Fatalf("private chat status = %d", code)
	}
	// Same file owner with their own durable chat is allowed.
	allowed, allowedStore := newDurableChatAPI(t, "chat-private", "alice")
	allowedStore.AddEvent("chat-private", chatevents.Event{ID: "tool-allowed", Type: "tool_call_start", Timestamp: time.Now(), Data: &mcpevents.AgentEvent{Type: mcpevents.ToolCallStart, Data: &mcpevents.ToolCallStartEvent{ToolParams: mcpevents.ToolParams{Arguments: `{"path":"app/a.txt"}`}}}})
	api = allowed
	if code, body := request("alice"); code != http.StatusOK || body["repos"] == nil || body["repos"].([]any)[0].(map[string]any)["used_in_chat"] != true {
		t.Fatalf("owner status = %d %v", code, body)
	}
}
