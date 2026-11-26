package musecli

import (
	"errors"
	"strings"
	"testing"
)

func TestMuseRewindKeepsTheTurnsBeforeTheEditedMessage(t *testing.T) {
	items := []museHistoryItem{
		{Kind: "userMessage", Text: "(instructions)\n\nfix the login", TurnID: "t1"},
		{Kind: "agentMessage", Text: "done", TurnID: "t1"},
		{Kind: "userShell", Text: "ls"},
		{Kind: "userMessage", Text: "now add tests", TurnID: "t2"},
		{Kind: "toolCall", TurnID: "t2"},
		{Kind: "userMessage", Text: "also run them", TurnID: "t2"}, // a steer into t2
		{Kind: "userMessage", Text: "now add tests", TurnID: "t3"},
		{Kind: "agentMessage", Text: "ok", TurnID: "t3"},
	}
	id := MuseRewindResumeID("s1", "  now add tests ", 1)
	source, cuts, ok := parseMuseRewind(id)
	if !ok || source != "s1" || len(cuts) != 1 || cuts[0].text != "now add tests" || cuts[0].fromEnd != 1 {
		t.Fatalf("parse %q: %q %v %v", id, source, cuts, ok)
	}
	if last, err := museRewindLastTurn(items, cuts); err != nil || last != "t2" {
		t.Fatalf("newest match: got %q, %v", last, err)
	}
	if last, _ := museRewindLastTurn(items, []museRewindCut{{text: "now add tests", fromEnd: 2}}); last != "t1" {
		t.Fatalf("older match: got %q", last)
	}
	if last, _ := museRewindLastTurn(items, []museRewindCut{{text: "also run them", fromEnd: 1}}); last != "t1" {
		t.Fatalf("a steered message goes back to before its turn: got %q", last)
	}

	// Two edits before the next message: the second counts in what the first kept.
	twice := MuseRewindResumeID(MuseRewindResumeID("s1", "now add tests", 1), "now add tests", 1)
	_, cuts, _ = parseMuseRewind(twice)
	if last, err := museRewindLastTurn(items, cuts); err != nil || last != "t1" {
		t.Fatalf("second edit: got %q, %v", last, err)
	}

	if _, err := museRewindLastTurn(items, []museRewindCut{{text: "fix the login", fromEnd: 1}}); !errors.Is(err, errMuseRewindToStart) {
		t.Fatalf("first message: got %v", err)
	}
	if _, err := museRewindLastTurn(items, []museRewindCut{{text: "never sent", fromEnd: 1}}); err == nil {
		t.Fatal("a message Muse never got must not fork")
	}
	if _, _, ok := parseMuseRewind("01a20e2c-08aa-7e51-822e-f3e09d474617"); ok {
		t.Fatal("a plain session id is not a rewind")
	}
	if got := museRewindNeedle(strings.Repeat("é", 800)); len(got) > museRewindNeedleBytes || !strings.HasPrefix(strings.Repeat("é", 800), got) {
		t.Fatalf("needle cut mid-character: %d bytes", len(got))
	}
}
