package tmuxinput

import (
	"context"
	"testing"
)

func TestInteractiveDraftDoesNotBlockASend(t *testing.T) {
	b := NewBroker()
	b.NoteInteractiveInput("pane", []byte("/model"))
	called := false
	_, err := b.Do(t.Context(), Request{SessionID: "pane", BypassReadiness: true}, func(context.Context) error { called = true; return nil })
	if err != nil || !called {
		t.Fatalf("a draft blocked the send: called=%v err=%v", called, err)
	}
	// Native navigation/interrupts still work while the composer is occupied.
	_, err = b.Do(t.Context(), Request{SessionID: "pane", BypassReadiness: true, InteractiveInput: true}, func(context.Context) error { b.NoteInteractiveInput("pane", []byte("\x1b[A")); return nil })
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Do(t.Context(), Request{SessionID: "pane", Priority: PriorityInterrupt}, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("stop blocked by draft: %v", err)
	}
	b.NoteInteractiveInput("pane", []byte("\r"))
	if !b.HasInteractiveDraft("pane") {
		t.Fatal("Enter alone is not proof of accepted input")
	}
	b.NoteInteractiveInput("pane", []byte("next draft"))
	b.ConfirmInteractiveSubmission("pane")
	if !b.HasInteractiveDraft("pane") {
		t.Fatal("accepting older Enter cleared a newer draft")
	}
	b.NoteInteractiveInput("pane", []byte{0x15})
	if !b.HasInteractiveDraft("pane") {
		t.Fatal("Ctrl+U can leave a suffix after the cursor; it is not proof of an empty composer")
	}
	b.NoteInteractiveInput("pane", []byte{0x03})
	if b.HasInteractiveDraft("pane") {
		t.Fatal("Ctrl+C did not clear the reservation")
	}
}

func TestInteractivePasteAndReportsAreNotSubmissions(t *testing.T) {
	b := NewBroker()
	for _, report := range []string{"\x1b[1;3R", "\x1b[?1;2c", "\x1b[<0;1;1M", "\x1b[I", "\x1b[O", "\x1b[M !!", "\x1b]10;rgb:ffff/ffff/ffff\x1b\\", "\x1b[?0u", "\x1b[?2026;2$y", "\x1b[8;24;80t", "\x1b[2;1;1;112;112;1;0x"} {
		b.NoteInteractiveInput("pane", []byte(report))
	}
	if b.HasInteractiveDraft("pane") {
		t.Fatal("terminal report reserved the composer")
	}
	b.NoteInteractivePaste("pane")
	if b.HasInteractiveSubmissions("pane") {
		t.Fatal("multiline paste counted as Enter")
	}
	b.NoteInteractiveInput("pane", []byte("\r"))
	b.ConfirmInteractiveSubmission("pane")
	if b.HasInteractiveDraft("pane") || b.InteractiveSubmissionCount("pane") != 1 {
		t.Fatal("accepted native input did not release its draft")
	}
	b.ClearInteractiveDraft("pane")
	if b.HasInteractiveSubmissions("pane") {
		t.Fatal("dead process retained submission state")
	}
}

// Keys that look like reports are still input: arrows, function keys and a kitty key press.
func TestInteractiveKeysAreNotMistakenForReports(t *testing.T) {
	for _, key := range []string{"\x1b[A", "\x1b[15~", "\x1b[97u", "\x1b[1;5D", "hi"} {
		b := NewBroker()
		b.NoteInteractiveInput("pane", []byte(key))
		if !b.HasInteractiveDraft("pane") {
			t.Fatalf("%q was treated as a terminal report", key)
		}
	}
}
