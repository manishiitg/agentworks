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
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	agentevents "github.com/manishiitg/mcpagent/events"
)

// PLAT-746-158: whole-chat review must retain private-chat and path access,
// use saved boundaries despite late reference arrival, and start at the oldest
// saved reply once retention removed the first.
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
		api.serveChatFileReview(w, req, crewroot.Roots{}, "", "snapshot_compare", params.Get("snapshot_id"), params.Get("file"), store, func(string) bool { return allow })
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
	// PLAT-746-163: an older reply whose capture failed (unavailable record) must
	// not become the start of the whole-chat comparison.
	cancelled, stop := context.WithCancel(t.Context())
	stop()
	_, _ = store.Begin(cancelled, "broken")
	ref("broken", 0, 1)
	history, err := api.chatFileReviewHistory(t.Context(), store, "chat-a", "")
	if err != nil || history.First != filesnapshots.ID("first") {
		t.Fatalf("late reference reordered start: %v %+v", err, history)
	}
	// PLAT-746: review points use the displayed after-state, not the latest
	// reply at click time; read tokens and foreign chat readers cannot write.
	mark := func(user, method, id string, token *accesstokens.Token) (int, string) {
		req := httptest.NewRequest(method, "/?session_id=chat-a", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user, AccessToken: token}))
		w := httptest.NewRecorder()
		api.serveChatFileReview(w, req, crewroot.Roots{}, "", "snapshot_review", id, "", store, func(string) bool { return true })
		return w.Code, w.Body.String()
	}
	reader := &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}
	runner := &accesstokens.Token{Scopes: []string{"crews:read", "crews:run"}, AllCrews: true}
	if externalTokenAllows(&UserClaims{UserID: "alice", AccessToken: reader}, externalTool{Name: "review_crew_snapshot"}) || !externalTokenAllows(&UserClaims{UserID: "alice", AccessToken: runner}, externalTool{Name: "review_crew_snapshot"}) {
		t.Fatal("review tool must require run scope, not Crew authoring permission")
	}
	for _, tc := range []struct {
		user, method, id string
		token            *accesstokens.Token
		want             int
	}{
		{"bob", "POST", filesnapshots.ID("first"), nil, 404},
		{"alice", "GET", filesnapshots.ID("first"), nil, 405},
		{"alice", "POST", filesnapshots.ID("first"), reader, 403},
		{"alice", "POST", filesnapshots.ID("foreign-chat"), nil, 404},
		{"alice", "POST", filesnapshots.ID("first"), runner, 200},
		{"alice", "POST", filesnapshots.ID("first"), runner, 200},
	} {
		if code, body := mark(tc.user, tc.method, tc.id, tc.token); code != tc.want {
			t.Fatalf("mark %s %s: %d %s", tc.user, tc.method, code, body)
		}
	}
	history, err = api.chatFileReviewHistory(t.Context(), store, "chat-a", "")
	if err != nil || len(history.Reviews) != 1 || history.Baseline != filesnapshots.ID("first") {
		t.Fatalf("durable idempotent review: %v %+v", err, history)
	}
	if code, body := call("alice", params, true); code != 200 || !strings.Contains(body, "-first") || !strings.Contains(body, "+last") || strings.Contains(body, "-zero") {
		t.Fatalf("after-state baseline %d %s", code, body)
	}
	if code, body := mark("alice", "POST", filesnapshots.ID("last"), nil); code != 200 {
		t.Fatalf("mark latest %d %s", code, body)
	}
	if code, body := call("alice", url.Values{"session_id": {"chat-a"}}, true); code != 200 || !strings.Contains(body, `"files":0`) {
		t.Fatalf("review should clear comparison %d %s", code, body)
	}
	params.Set("baseline_snapshot_id", filesnapshots.ID("first"))
	params.Set("through_snapshot_id", filesnapshots.ID("last"))
	if code, body := call("alice", params, true); code != 200 || !strings.Contains(body, "-first") {
		t.Fatalf("displayed comparison drifted %d %s", code, body)
	}
	params.Del("baseline_snapshot_id")
	params.Del("through_snapshot_id")
	params.Set("review_id", filesnapshots.ID("first"))
	if code, body := call("alice", params, true); code != 200 || !strings.Contains(body, "-zero") || !strings.Contains(body, "+first") {
		t.Fatalf("first review history %d %s", code, body)
	}
	params.Set("review_id", filesnapshots.ID("last"))
	if code, body := call("alice", params, true); code != 200 || !strings.Contains(body, "-first") || !strings.Contains(body, "+last") {
		t.Fatalf("later review history %d %s", code, body)
	}
	if code, body := mark("alice", "POST", filesnapshots.ID("first"), nil); code != 409 {
		t.Fatalf("review moved backwards %d %s", code, body)
	}
	params.Del("review_id")
	params.Set("baseline_snapshot_id", "start")
	// Retention removed the chat's first reply: the comparison starts at the
	// oldest reply still saved, not an empty panel (owner, 2026-10-29).
	if err := os.Remove(filepath.Join(store.Private, "file-snapshots", "turns", filesnapshots.ID("first")+".json")); err != nil {
		t.Fatal(err)
	}
	if code, body := call("alice", params, true); code != 200 || !strings.Contains(body, "-first") || !strings.Contains(body, "+last") || !strings.Contains(body, `"available":false`) {
		t.Fatalf("comparison from the oldest saved reply %d %s", code, body)
	}
}

// PLAT-746-158: only a Crew owner with crews:write may undo a reply; the
// refusal comes before any chat or file lookup.
func TestUndoReplyRefusesReadTokenAndNonOwner(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	api, _ := newDurableChatAPI(t, "chat-a", "alice")
	store := filesnapshots.Store{Root: t.TempDir(), Private: t.TempDir()}
	all := func(string) bool { return true }
	call := func(token *accesstokens.Token, user string) int {
		req := httptest.NewRequest("GET", "/?session_id=chat-a", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user, AccessToken: token}))
		w := httptest.NewRecorder()
		api.serveCrewReplyUndo(w, req, "Crew/not-theirs", "", "snapshot_undo_preview", filesnapshots.ID("x"), store, all, all)
		return w.Code
	}
	if code := call(&accesstokens.Token{Scopes: []string{"crews:read", "crews:run"}, AllCrews: true}, "alice"); code != 403 {
		t.Fatalf("run/read token must not undo: %d", code)
	}
	if code := call(&accesstokens.Token{Scopes: []string{"crews:write"}, AllCrews: true}, "bob"); code != 403 {
		t.Fatalf("non-owner must not undo: %d", code)
	}
	if externalTokenAllows(&UserClaims{UserID: "alice", AccessToken: &accesstokens.Token{Scopes: []string{"crews:run"}}}, externalTool{Name: "undo_crew_reply"}) {
		t.Fatal("undo tool must need crews:write")
	}
}

// PLAT-746-158: applying an undo is refused while a turn runs in the chat, but
// only after the owner check (a non-owner never learns the chat is busy); the
// preview stays available.
func TestUndoReplyApplyRefusedWhileTurnRunning(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	api, _ := newDurableChatAPI(t, "chat-a", "alice")
	const root = "Crew/undo-crew"
	crewOwners.mu.Lock()
	crewOwners.entries = map[string]crewOwnerEntry{}
	crewOwners.read = func(_ context.Context, r string) string {
		if r == root {
			return "alice"
		}
		return ""
	}
	crewOwners.mu.Unlock()
	t.Cleanup(func() {
		crewOwners.mu.Lock()
		crewOwners.read = nil
		crewOwners.entries = map[string]crewOwnerEntry{}
		crewOwners.mu.Unlock()
	})
	store := filesnapshots.Store{Root: t.TempDir(), Private: t.TempDir()}
	all := func(string) bool { return true }
	api.setSessionBusy("chat-a", true)
	call := func(user, method, op string) int {
		req := httptest.NewRequest(method, "/?session_id=chat-a", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user, AccessToken: &accesstokens.Token{Scopes: []string{"crews:write"}, AllCrews: true}}))
		w := httptest.NewRecorder()
		api.serveCrewReplyUndo(w, req, root, "", op, filesnapshots.ID("x"), store, all, all)
		return w.Code
	}
	if code := call("bob", "POST", "snapshot_undo"); code != 403 {
		t.Fatalf("non-owner while busy: %d", code)
	}
	if code := call("alice", "POST", "snapshot_undo"); code != 409 {
		t.Fatalf("owner apply while busy: %d", code)
	}
	if code := call("alice", "GET", "snapshot_undo_preview"); code == 409 {
		t.Fatal("preview must stay available while busy")
	}
}
