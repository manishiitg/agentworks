package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// PLAT-746-988: an over-limit objective must never reach native control; the
// limit is Unicode characters, not bytes. Quoted text/prefixes are not controls.
func TestNativeGoalCommandOwnSessionAndUnicodeBoundary(t *testing.T) {
	for _, query := range []string{"Please use /goal later", "/goalish task"} {
		if _, known, _ := parseNativeGoalCommand(query, "chat"); known {
			t.Fatal("ordinary text became a goal command")
		}
	}
	for _, tc := range []struct {
		query, action string
		bad           bool
	}{{"/goal", "show", false}, {"/goal clear", "clear", false}, {"/goal pause", "pause", false}, {"/goal resume", "resume", false}, {"/goal\nresume", "resume", false}, {"/goal " + strings.Repeat("界", 4000), "set", false}, {"/goal " + strings.Repeat("界", 4001), "set", true}} {
		command, known, err := parseNativeGoalCommand(tc.query, "chat")
		if !known || command.SessionID != "chat" || command.Action != tc.action || (err != nil) != tc.bad {
			t.Fatalf("goal dispatch changed: action=%s known=%t err=%v", command.Action, known, err)
		}
	}
}

// PLAT-746-1037: the goal tool sends the person's own slash command, and only the chat's owner may use it: a teammate's
// or another person's agent is refused before anything is sent.
func TestNativeGoalToolCommandsAndOwnerOnly(t *testing.T) {
	for _, tc := range []struct{ goal, objective, want string }{
		{"set", "  ship the release  ", "/goal ship the release"}, {"show", "", "/goal"}, {"pause", "", "/goal pause"}, {"resume", "", "/goal resume"}, {"clear", "", "/goal clear"},
		{"set", "", ""}, {"set", "clear", ""}, {"stop", "", ""}, {"set", strings.Repeat("a", 4001), ""},
	} {
		if got, refusal := nativeGoalToolQuery(tc.goal, tc.objective); got != tc.want || (tc.want == "") != (refusal != "") {
			t.Fatalf("%s %q: got %q refusal %q", tc.goal, tc.objective, got, refusal)
		}
	}
	chats := []externalCrewChat{{ChatID: "main", OwnerID: "teammate", CanWrite: false, key: "k"}, {ChatID: "mine", OwnerID: "me", CanWrite: true, key: "k2"}}
	for _, chat := range []string{"main", "missing"} {
		req := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "me"}))
		rec := httptest.NewRecorder()
		(&StreamingAPI{}).externalCrewChatGoal(rec, req, map[string]any{"chat_id": chat, "goal": "clear"}, chats, map[string]string{"id": "work"}, "crew")
		if want := map[string]int{"main": http.StatusForbidden, "missing": http.StatusNotFound}[chat]; rec.Code != want {
			t.Fatalf("%s: %d %s", chat, rec.Code, rec.Body.String())
		}
	}
	if nativeGoalOrigin(context.Background(), "set") != "person_set" || nativeGoalOrigin(context.WithValue(context.Background(), nativeGoalToolContextKey{}, "me"), "set") != "tool_set" {
		t.Fatal("a tool-set goal must be logged as tool_set and a typed one as person_set")
	}
}
