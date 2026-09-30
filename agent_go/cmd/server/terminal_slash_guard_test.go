package server

import "testing"

// feed sends each chunk in order and returns the decisions.
func feedSlash(g *slashGuard, session string, chunks ...string) []slashDecision {
	var out []slashDecision
	for _, chunk := range chunks {
		decision, _ := g.decide(session, []byte(chunk))
		out = append(out, decision)
	}
	return out
}

func lastDecision(t *testing.T, g *slashGuard, session string, chunks ...string) (slashDecision, int) {
	t.Helper()
	var decision slashDecision
	var erase int
	for _, chunk := range chunks {
		decision, erase = g.decide(session, []byte(chunk))
	}
	return decision, erase
}

func TestSlashGuardAllowsOnlyTheAllowlist(t *testing.T) {
	t.Setenv(terminalSlashCommandsEnv, "")
	g := &slashGuard{lines: map[string]*slashLine{}}
	if d, _ := lastDecision(t, g, "a", "/", "u", "s", "a", "g", "e", "\r"); d != slashForward {
		t.Fatalf("/usage was blocked: %v", d)
	}
	d, erase := lastDecision(t, g, "a", "/", "n", "e", "w", "\r")
	if d != slashCancel || erase != 4 {
		t.Fatalf("/new decision=%v erase=%d, want cancel and 4", d, erase)
	}
	// A partial name of an allowed command is not enough: the CLI's menu would complete it.
	if d, _ := lastDecision(t, g, "a", "/", "u", "s", "\r"); d != slashCancel {
		t.Fatalf("a partial /us was let through: %v", d)
	}
}

func TestSlashGuardBlocksPickingFromTheMenuWithArrowKeys(t *testing.T) {
	t.Setenv(terminalSlashCommandsEnv, "")
	g := &slashGuard{lines: map[string]*slashLine{}}
	if d := feedSlash(g, "a", "/", "\x1b[B", "\x1b[A", "\t"); d[1] != slashDrop || d[2] != slashDrop || d[3] != slashDrop {
		t.Fatalf("menu navigation was forwarded: %v", d)
	}
	// "/" then Enter picks the highlighted item: no name typed, so it is not on the allowlist.
	if d, _ := lastDecision(t, g, "b", "/", "\r"); d != slashCancel {
		t.Fatalf("a bare / plus Enter was let through: %v", d)
	}
}

func TestSlashGuardLeavesOrdinaryTypingAlone(t *testing.T) {
	t.Setenv(terminalSlashCommandsEnv, "")
	g := &slashGuard{lines: map[string]*slashLine{}}
	for _, chunk := range []string{"h", "i", " ", "/", "s", "r", "v", "\r", "\x1b[A", "\x1b[B", "x", "\x7f"} {
		if d, _ := g.decide("a", []byte(chunk)); d != slashForward {
			t.Fatalf("%q was not forwarded: %v", chunk, d)
		}
	}
	// A slash in the middle of a line is part of the text, and Enter sends it.
	if d, _ := lastDecision(t, g, "b", "g", "o", " ", "/", "t", "m", "p", "\r"); d != slashForward {
		t.Fatalf("a slash inside a sentence was blocked: %v", d)
	}
}

func TestSlashGuardBackspaceAndEscapeAbandonTheCommand(t *testing.T) {
	t.Setenv(terminalSlashCommandsEnv, "")
	g := &slashGuard{lines: map[string]*slashLine{}}
	// "/" then backspace leaves an empty line: the next Enter is an ordinary Enter.
	if d, _ := lastDecision(t, g, "a", "/", "\x7f", "\r"); d != slashForward {
		t.Fatalf("Enter after deleting the slash was blocked: %v", d)
	}
	if d, _ := lastDecision(t, g, "b", "/", "n", "\x1b", "\r"); d != slashForward {
		t.Fatalf("Enter after Escape was blocked: %v", d)
	}
}

func TestSlashGuardHandlesPastedCommands(t *testing.T) {
	t.Setenv(terminalSlashCommandsEnv, "")
	g := &slashGuard{lines: map[string]*slashLine{}}
	if d, erase := lastDecision(t, g, "a", "/clear", "\r"); d != slashCancel || erase != 6 {
		t.Fatalf("pasted /clear decision=%v erase=%d", d, erase)
	}
	if d, _ := lastDecision(t, g, "b", "\x1b[200~/usage\x1b[201~", "\r"); d != slashForward {
		t.Fatalf("a bracketed-pasted /usage was blocked: %v", d)
	}
}

func TestSlashGuardPolicyValues(t *testing.T) {
	g := &slashGuard{lines: map[string]*slashLine{}}
	t.Setenv(terminalSlashCommandsEnv, "allow")
	if d, _ := lastDecision(t, g, "a", "/", "n", "e", "w", "\r"); d != slashForward {
		t.Fatalf("allow did not permit /new: %v", d)
	}
	t.Setenv(terminalSlashCommandsEnv, "none")
	if d, _ := lastDecision(t, g, "b", "/", "u", "s", "a", "g", "e", "\r"); d != slashCancel {
		t.Fatalf("none permitted /usage: %v", d)
	}
	t.Setenv(terminalSlashCommandsEnv, "usage, /status")
	if d, _ := lastDecision(t, g, "c", "/", "s", "t", "a", "t", "u", "s", "\r"); d != slashForward {
		t.Fatalf("a listed /status was blocked: %v", d)
	}
}

func TestStripCLIExitKeys(t *testing.T) {
	for _, key := range []byte{0x03, 0x04, 0x1a, 0x1c} {
		if out, stripped := stripCLIExitKeys([]byte{key}); !stripped || len(out) != 0 {
			t.Fatalf("key %#x should be dropped, got %v %v", key, out, stripped)
		}
	}
	if out, stripped := stripCLIExitKeys([]byte("hi\x03there")); !stripped || string(out) != "hithere" {
		t.Fatalf("got %q %v", out, stripped)
	}
	if out, stripped := stripCLIExitKeys([]byte("\x1b")); stripped || string(out) != "\x1b" {
		t.Fatalf("Esc must pass, got %q", out)
	}
	paste := []byte("\x1b[200~a\x03b\x1b[201~")
	if out, stripped := stripCLIExitKeys(paste); stripped || string(out) != string(paste) {
		t.Fatalf("bracketed paste must pass whole")
	}
}
