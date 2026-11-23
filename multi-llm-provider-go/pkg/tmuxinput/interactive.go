package tmuxinput

import (
	"bytes"
	"errors"
	"log"
)

// ErrInteractiveDraft is no longer returned: a draft does not block sends (see broker.go). The
// value stays for callers that still compare against it.
var ErrInteractiveDraft = errors.New("finish or clear the terminal draft before sending another message")

type interactiveDraft struct {
	version       uint64
	submitVersion uint64
}

func (b *Broker) ClearInteractiveDraft(sessionID string) {
	b.mu.Lock()
	delete(b.interactiveDrafts, sessionID)
	delete(b.interactiveSubmissions, sessionID)
	b.mu.Unlock()
}

// NoteInteractiveInput runs inside the input transaction, before bytes reach
// tmux. Enter is only an attempt (it can select a slash-menu item). A native
// user record, rather than the key, releases the reservation.
func (b *Broker) NoteInteractiveInput(sessionID string, data []byte) {
	if len(data) == 0 {
		return
	}
	if isTerminalReport(data) {
		return
	}
	if bytes.HasPrefix(data, []byte("\x1b[200~")) && bytes.HasSuffix(data, []byte("\x1b[201~")) {
		b.NoteInteractivePaste(sessionID)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if bytes.Equal(data, []byte{0x03}) {
		delete(b.interactiveDrafts, sessionID)
		return
	}
	draft := b.interactiveDrafts[sessionID]
	if draft == nil {
		draft = &interactiveDraft{}
		b.interactiveDrafts[sessionID] = draft
		logInteractiveDraftStart(sessionID, data)
	}
	draft.version++
	if bytes.ContainsAny(data, "\r\n") {
		draft.submitVersion = draft.version
		b.interactiveSubmissions[sessionID]++
	}
}

// Emulator replies and mouse reports are not edits to the CLI composer.
func isTerminalReport(data []byte) bool {
	if bytes.Equal(data, []byte("\x1b[I")) || bytes.Equal(data, []byte("\x1b[O")) || bytes.HasPrefix(data, []byte("\x1b[M")) {
		return true
	}
	if bytes.HasPrefix(data, []byte("\x1b]")) || bytes.HasPrefix(data, []byte("\x1bP")) {
		return true
	}
	if len(data) < 3 || data[0] != 0x1b || data[1] != '[' {
		return false
	}
	last := data[len(data)-1]
	switch last {
	case 'R', 'c', 'n':
		return true // cursor position, device attributes, status
	case 't', 'x':
		return true // window-size and terminal-parameter reports (\x1b[8;24;80t, \x1b[2;1;1;112;112;1;0x)
	case 'y':
		return len(data) >= 4 && data[len(data)-2] == '$' // mode report (\x1b[?2026;2$y)
	case 'u':
		return data[2] == '?' // kitty keyboard flags reply (\x1b[?0u); a plain \x1b[97u is a key press
	case 'M', 'm':
		return data[2] == '<' // mouse
	}
	return false
}

func (b *Broker) HasInteractiveSubmissions(sessionID string) bool {
	return b.InteractiveSubmissionCount(sessionID) > 0
}

func (b *Broker) InteractiveSubmissionCount(sessionID string) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.interactiveSubmissions[sessionID]
}

// A paste fills the composer. Its embedded newlines are not Enter presses.
func (b *Broker) NoteInteractivePaste(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	draft := b.interactiveDrafts[sessionID]
	if draft == nil {
		draft = &interactiveDraft{}
		b.interactiveDrafts[sessionID] = draft
	}
	draft.version++
}

func (b *Broker) HasInteractiveDraft(sessionID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.interactiveDrafts[sessionID] != nil
}

// ConfirmInteractiveSubmission does not clear a newer draft typed while the
// previous Enter was being accepted. Disconnects also retain drafts: the CLI
// still contains those bytes, even after the browser goes away.
func (b *Broker) ConfirmInteractiveSubmission(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if draft := b.interactiveDrafts[sessionID]; draft != nil && draft.submitVersion == draft.version {
		delete(b.interactiveDrafts, sessionID)
	}
}

// logInteractiveDraftStart records what first reserved a session's composer, so a chat send refused
// with "finish or clear the terminal draft" can be traced to its cause. Control sequences are logged
// as they are (they are terminal traffic, not typing); typed text never is, only its length.
func logInteractiveDraftStart(sessionID string, data []byte) {
	if len(data) > 0 && data[0] == 0x1b {
		shown := data
		if len(shown) > 24 {
			shown = shown[:24]
		}
		log.Printf("[INTERACTIVE_DRAFT] %s: draft started by an escape sequence %q (%d bytes)", sessionID, shown, len(data))
		return
	}
	log.Printf("[INTERACTIVE_DRAFT] %s: draft started by typed input (%d bytes)", sessionID, len(data))
}
