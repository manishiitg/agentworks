package instructions

import (
	"strings"
	"testing"
)

func lines(n int, change map[int]string) string {
	out := make([]string, n)
	for i := range out {
		out[i] = "rule number " + strings.Repeat("x", i%7) + string(rune('a'+i%26))
		if v, ok := change[i]; ok {
			out[i] = v
		}
	}
	return strings.Join(out, "\n")
}

func TestLinesSendsOnlyChangedLines(t *testing.T) {
	old := lines(400, nil)
	current := lines(400, map[int]string{120: "You are the \"ai-work\" agent in this Crew."})
	diff, ok := Lines(old, current)
	if !ok || !strings.Contains(diff, "+You are the \"ai-work\" agent") || !strings.Contains(diff, "-rule number") || strings.Count(diff, "\n") > 8 {
		t.Fatalf("diff = %q ok=%t", diff, ok)
	}
	if _, ok := Lines(old, "entirely\nnew"); ok {
		t.Fatal("a large change must send the whole set")
	}
}

// The shared rules every app follows: a started session gets nothing extra, a change only its lines, a session
// with no place for instructions the whole set once, and a compaction (also during an undelivered message) the
// difference to what the session started with.
func TestTrackerSendsOnlyWhatASessionLacks(t *testing.T) {
	first := lines(300, nil)
	changed := lines(300, map[int]string{40: "New rule: deploy only through the deploy agent."})

	var started Tracker
	started.Started(first)
	if d, _ := started.Next(first); d.Kind != None {
		t.Fatalf("a started session got %v", d.Kind)
	}
	d, receipt := started.Next(changed)
	if d.Kind != Changes || !strings.Contains(d.Text, "+New rule") || strings.Contains(d.Text, lines(300, nil)[:300]) {
		t.Fatalf("want only the changed lines, got %v %q", d.Kind, d.Text)
	}
	if again, _ := started.Next(changed); again.Kind != Changes {
		t.Fatal("an undelivered change was dropped")
	}
	receipt()
	if after, _ := started.Next(changed); after.Kind != None {
		t.Fatal("a delivered change was sent again")
	}
	started.Compacted()
	if after, _ := started.Next(changed); after.Kind != Changes {
		t.Fatal("a compaction must resend the changes since the start")
	}
	_, lateReceipt := started.Next(changed)
	started.Compacted()
	lateReceipt()
	if after, _ := started.Next(changed); after.Kind != Changes {
		t.Fatal("a message a compaction ran over must not count as delivered")
	}

	var noPlace Tracker
	if d, _ := noPlace.Next(first); d.Kind != Full {
		t.Fatal("a session with no place for instructions must get the whole set first")
	}
	if msg := InMessage(Delivery{Kind: Changes, Text: "x"}, "hi"); !strings.HasPrefix(msg, ChangesStart) || !strings.HasSuffix(msg, MessageStart+"\nhi") {
		t.Fatalf("message = %q", msg)
	}
}
