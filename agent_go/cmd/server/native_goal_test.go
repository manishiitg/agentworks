package server

import (
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
	}{{"/goal", "show", false}, {"/goal clear", "clear", false}, {"/goal pause", "pause", false}, {"/goal resume", "resume", false}, {"/goal " + strings.Repeat("界", 4000), "set", false}, {"/goal " + strings.Repeat("界", 4001), "set", true}} {
		command, known, err := parseNativeGoalCommand(tc.query, "chat")
		if !known || command.SessionID != "chat" || command.Action != tc.action || (err != nil) != tc.bad {
			t.Fatalf("goal dispatch changed: action=%s known=%t err=%v", command.Action, known, err)
		}
	}
}
