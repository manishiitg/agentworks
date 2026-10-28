package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	agentevents "github.com/manishiitg/mcpagent/events"
)

// PLAT-746-158: whole-chat review must retain private-chat and path access,
// use saved boundaries despite late reference arrival, and refuse a lost start.
func TestChatFileReviewKeepsPrivateHistoryAndSavedLineAnchors(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	api, events := newDurableChatAPI(t, "chat-a", "alice")
	store := filesnapshots.Store{Root: t.TempDir(), Private: t.TempDir()}
	path := filepath.Join(store.Root, "main.txt")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("zero\n")
	for _, turn := range []string{"first", "last"} {
		if _, err := store.Begin(t.Context(), turn); err != nil {
			t.Fatal(err)
		}
		write(turn + "\n")
		if turn == "first" {
			for i := 0; i < 105; i++ {
				if err := os.WriteFile(filepath.Join(store.Root, fmt.Sprintf("file-%03d.txt", i)), []byte("new\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
		if _, err := store.Finish(t.Context(), turn); err != nil {
			t.Fatal(err)
		}
	}
	ref := func(id string, start, end int64) {
		events.AddEvent("chat-a", storeevents.Event{ID: "ref-" + id, Type: "reply_file_changes", Timestamp: time.Now(), Data: agentevents.NewAgentEvent(storeevents.NewGenericEventData("reply_file_changes", map[string]any{"snapshot_id": filesnapshots.ID(id), "workspace_path": "", "start_sequence": start, "end_sequence": end}))})
	}
	// The first capture's reference arrives last, but its saved start is zero.
	ref("last", 2, 4)
	ref("first", 0, 2)
	call := func(user string, params url.Values, allow bool) (int, string) {
		req := httptest.NewRequest("GET", "/?"+params.Encode(), nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user}))
		w := httptest.NewRecorder()
		api.serveChatFileReview(w, req, "", "", "snapshot_compare", params.Get("snapshot_id"), params.Get("file"), store, func(string) bool { return allow })
		return w.Code, w.Body.String()
	}
	pageParams := url.Values{"session_id": {"chat-a"}}
	_, body := call("alice", pageParams, true)
	var page struct {
		Next       int                      `json:"next_offset"`
		Total      int                      `json:"total_changes"`
		Comparison filesnapshots.Comparison `json:"comparison"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil || page.Next != 100 || len(page.Comparison.Changes) != 100 || page.Total != 106 {
		t.Fatalf("bounded file page: %v %s", err, body)
	}
	pageParams.Set("offset", "100")
	pageParams.Set("through_snapshot_id", filesnapshots.ID("last"))
	_, body = call("alice", pageParams, true)
	if err := json.Unmarshal([]byte(body), &page); err != nil || page.Next != 0 || len(page.Comparison.Changes) != 6 {
		t.Fatalf("remaining files: %v %s", err, body)
	}
	params := url.Values{"session_id": {"chat-a"}, "file": {"main.txt"}}
	if code, body := call("alice", params, true); code != 200 || !strings.Contains(body, "+last") || !strings.Contains(body, "-zero") {
		t.Fatalf("saved net comparison %d %s", code, body)
	}
	if code, body := call("bob", params, true); code != 404 || strings.Contains(body, "main.txt") {
		t.Fatalf("private chat exposed %d %s", code, body)
	}
	if _, body := call("alice", params, false); strings.Contains(body, "+last") {
		t.Fatal("revoked file exposed")
	}
	params.Set("snapshot_id", filesnapshots.ID("foreign-chat"))
	if code, _ := call("alice", params, true); code != 404 {
		t.Fatalf("foreign snapshot: %d", code)
	}
	params.Del("snapshot_id")
	params.Set("side", "after")
	params.Set("line", "1")
	write("working tree changed\n")
	code, body := call("alice", params, true)
	var anchor map[string]any
	_ = json.Unmarshal([]byte(body), &anchor)
	if code != 200 || !strings.HasSuffix(anchor["context"].(string), "\nlast") {
		t.Fatalf("anchor read current file %d %s", code, body)
	}
	params.Set("line", "99")
	if code, _ := call("alice", params, true); code != 400 {
		t.Fatalf("invalid line accepted: %d", code)
	}
	params.Del("line")
	params.Del("side")
	// A late reference must still identify the original before-boundary.
	history, err := api.chatFileReviewHistory(t.Context(), "chat-a", "")
	if err != nil || history.First != filesnapshots.ID("first") {
		t.Fatalf("late reference reordered start: %v %+v", err, history)
	}
	// Removing the real first record simulates retention; never substitute a later one.
	if err := os.Remove(filepath.Join(store.Private, "file-snapshots", "turns", filesnapshots.ID("first")+".json")); err != nil {
		t.Fatal(err)
	}
	if _, body := call("alice", params, true); strings.Contains(body, "+last") {
		t.Fatalf("missing first capture fabricated: %s", body)
	}
}
