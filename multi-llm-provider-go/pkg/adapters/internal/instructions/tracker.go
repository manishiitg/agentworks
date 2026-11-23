// Package instructions is the one way every coding agent session is kept on
// the current AgentWorks instructions, whatever app runs it.
//
// The rules, the same for every app:
//   - A session that starts (or resumes) takes the current instructions where
//     its app keeps them outside the conversation: Claude's system prompt,
//     Codex's thread settings. An app with no such place (Muse) has none.
//   - Each message then carries only what the session lacks: nothing when it
//     holds the current instructions, the changed lines when they changed, the
//     whole set the first time or for a large change.
//   - A compaction may drop what messages carried, so the session is back to
//     what it started with; the next message carries the difference.
//
// An app reports Started and Compacted and asks Next before each message;
// it decides only where the text goes (the message, a turn setting).
package instructions

import (
	"strings"
	"sync"
)

// Kind of text a message carries for the instructions.
type Kind int

const (
	// None: the session already holds the current instructions.
	None Kind = iota
	// Changes: only the changed lines (a diff), with Note.
	Changes
	// Full: the whole set.
	Full
)

// Delivery is what the next message carries; For is the set it brings the
// session to.
type Delivery struct {
	Kind Kind
	Text string
	For  string
}

// Markers around instructions at the top of a message (Claude, Muse). The
// context pane counts them to size what they take.
const (
	FullStart    = "[Current AgentWorks Instructions]"
	ChangesStart = "[AgentWorks Instructions Changed]"
	MessageStart = "[Current User Message]"
)

// Tracker follows what one session holds. The zero value is a session that
// holds nothing yet.
type Tracker struct {
	mu    sync.Mutex
	base  string // what the session started with, outside the conversation
	held  string // base plus what messages carried since
	epoch int    // bumps on start and compaction; a receipt from before is void
}

// Started records a session that started or resumed with base outside the
// conversation ("" when its app has no such place).
func (t *Tracker) Started(base string) {
	t.mu.Lock()
	t.base, t.held = base, base
	t.epoch++
	t.mu.Unlock()
}

// Compacted records a compaction: what messages carried may be gone.
func (t *Tracker) Compacted() {
	t.mu.Lock()
	t.held = t.base
	t.epoch++
	t.mu.Unlock()
}

// Holds reports whether the session holds current.
func (t *Tracker) Holds(current string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.held == current
}

// Next is what the next message carries so the session follows current, and
// the receipt to call once that message was delivered.
func (t *Tracker) Next(current string) (Delivery, func()) {
	t.mu.Lock()
	held, epoch := t.held, t.epoch
	t.mu.Unlock()
	if held == current || strings.TrimSpace(current) == "" {
		return Delivery{Kind: None, For: current}, func() {}
	}
	d := Delivery{Kind: Full, Text: current, For: current}
	if held != "" {
		if diff, ok := Lines(held, current); ok {
			d = Delivery{Kind: Changes, Text: Note + "\n\n" + diff, For: current}
		}
	}
	return d, func() {
		t.mu.Lock()
		if t.epoch == epoch {
			t.held = current
		}
		t.mu.Unlock()
	}
}

// InMessage puts a delivery at the top of a message.
func InMessage(d Delivery, message string) string {
	switch d.Kind {
	case Changes:
		return ChangesStart + "\n" + d.Text + "\n\n" + MessageStart + "\n" + message
	case Full:
		return FullStart + "\n" + d.Text + "\n\n" + MessageStart + "\n" + message
	}
	return message
}
