package sessiontransfer

import (
	"os"
	"path/filepath"
	"testing"
)

// PLAT-856 live round 4: a Muse journal gets a session.end record whenever its run closes or is restored, so a copy
// that continued from the same point on both computers differed from the other only by that last line and was wrongly
// called changed on both. A trailing session.end does not count; a different real record still does.
func TestAncestorIgnoresTrailingSessionEnd(t *testing.T) {
	line := func(typ, id string) string {
		return `{"id":"` + id + `","payload_type":"` + typ + `"}` + "\n"
	}
	bundleOf := func(content string) *Bundle {
		dir := t.TempDir()
		path := filepath.Join(dir, "session.jsonl")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := Inspect(path)
		if err != nil {
			t.Fatal(err)
		}
		return &Bundle{Provider: "muse-cli", Format: "muse-journal", SessionID: "s", Root: dir, Files: map[string]File{"session.jsonl": f}}
	}
	shared := line("message", "1") + line("message", "2")
	mac := bundleOf(shared + line("session.end", "mac-end"))
	server := bundleOf(shared + line("session.end", "server-end") + line("message", "3") + line("session.end", "server-end-2"))
	if err := Ancestor(mac, server); err != nil {
		t.Fatalf("a copy that differs only by its closing session.end is an ancestor: %v", err)
	}
	macChanged := bundleOf(shared + line("message", "mac-only") + line("session.end", "mac-end"))
	if err := Ancestor(macChanged, server); err != ErrDiverged {
		t.Fatalf("a real record only on the older copy is a divergence: %v", err)
	}
}
