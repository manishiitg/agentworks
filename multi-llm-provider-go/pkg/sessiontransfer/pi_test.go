package sessiontransfer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePiJournal(t *testing.T, store, id, cwd string, extra ...string) string {
	t.Helper()
	body := fmt.Sprintf("{\"type\":\"session\",\"version\":3,\"id\":%q,\"timestamp\":\"2026-11-06T13:17:37.634Z\",\"cwd\":%q}\n", id, cwd) +
		"{\"type\":\"model_change\",\"id\":\"a1\",\"parentId\":null,\"timestamp\":\"2026-11-06T13:17:37.700Z\"}\n" +
		"{\"type\":\"message\",\"id\":\"b2\",\"parentId\":\"a1\",\"timestamp\":\"2026-11-06T13:17:38.000Z\",\"message\":{\"role\":\"user\"}}\n"
	for _, line := range extra {
		body += line + "\n"
	}
	p := filepath.Join(store, "2026-11-06T13-17-37-634Z_"+id+".jsonl")
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Pi sessions move between computers with the destination's cwd in the header (Pi will not resume a session
// recorded for another directory), and the copy stays an ancestor of what the destination continues.
func TestPiNativeRoundTripAndAncestor(t *testing.T) {
	const id = "mlp-pi-0123abcd"
	storeA, storeB, cwdA, cwdB := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	writePiJournal(t, storeA, id, cwdA)
	first, err := ExportJSONL("pi-cli", "rpc", storeA, cwdA, id, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if first.Workspace != cwdA {
		t.Fatalf("workspace %q", first.Workspace)
	}
	if err := ImportJSONL(storeB, cwdB, first); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(storeB, "2026-11-06T13-17-37-634Z_"+id+".jsonl")
	h, err := readPiHeader(moved)
	if err != nil || *h.Cwd == piWorkspacePlaceholder || !strings.HasSuffix(*h.Cwd, filepath.Base(cwdB)) {
		t.Fatalf("destination header cwd not written: %v", err)
	}
	second, err := ExportJSONL("pi-cli", "rpc", storeB, cwdB, id, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if second.Files["session.jsonl"] != first.Files["session.jsonl"] {
		t.Fatal("round trip changed the journal")
	}
	// The destination keeps working (Pi appends); the first copy is its ancestor, a rival history is not.
	f, _ := os.OpenFile(moved, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString("{\"type\":\"message\",\"id\":\"c3\",\"parentId\":\"b2\",\"timestamp\":\"2026-11-06T13:18:00.000Z\"}\n")
	f.Close()
	grown, err := ExportJSONL("pi-cli", "rpc", storeB, cwdB, id, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := Ancestor(first, grown); err != nil {
		t.Fatalf("extension is not an ancestor: %v", err)
	}
	if err := ImportJSONL(storeA, cwdA, grown); err != nil {
		t.Fatalf("hand back over the older copy: %v", err)
	}
	f, _ = os.OpenFile(filepath.Join(storeA, "2026-11-06T13-17-37-634Z_"+id+".jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString("{\"type\":\"message\",\"id\":\"d4\",\"parentId\":\"b2\",\"timestamp\":\"2026-11-06T13:19:00.000Z\"}\n")
	f.Close()
	if err := ImportJSONL(storeA, cwdA, grown); err == nil {
		t.Fatal("diverged copies were overwritten")
	}
}

// A Pi bundle cannot write outside the store, carry another session or an unsupported journal version.
func TestPiNativeRejectsLinksForeignSessionAndOtherVersions(t *testing.T) {
	const id = "mlp-pi-0123abcd"
	store, cwd := t.TempDir(), t.TempDir()
	good := writePiJournal(t, store, id, cwd)
	b, err := ExportJSONL("pi-cli", "rpc", store, cwd, id, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b.Files["../../auth.json"] = b.Files["session.jsonl"]
	if b.Validate() == nil {
		t.Fatal("accepted a path outside the session")
	}
	delete(b.Files, "../../auth.json")
	if _, err := ExportJSONL("pi-cli", "rpc", store, cwd, "mlp-pi-other", t.TempDir()); err == nil {
		t.Fatal("exported a session that is not in the store")
	}
	if _, err := ExportJSONL("pi-cli", "rpc", store, cwd, "../"+id, t.TempDir()); err == nil {
		t.Fatal("accepted a path as a session id")
	}
	// A file named for one session but holding another.
	other := filepath.Join(store, "2026-11-06T13-17-37-634Z_mlp-pi-ffff0000.jsonl")
	raw, _ := os.ReadFile(good)
	if err := os.WriteFile(other, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportJSONL("pi-cli", "rpc", store, cwd, "mlp-pi-ffff0000", t.TempDir()); err == nil {
		t.Fatal("exported a journal whose header names another session")
	}
	// Only header version 3 is read.
	v2 := strings.Replace(string(raw), "\"version\":3", "\"version\":2", 1)
	if err := os.WriteFile(good, []byte(v2), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportJSONL("pi-cli", "rpc", store, cwd, id, t.TempDir()); err == nil {
		t.Fatal("accepted an unsupported Pi journal version")
	}
	// A symlinked journal is refused.
	link := t.TempDir()
	if err := os.Symlink(other, filepath.Join(link, "2026-11-06T13-17-37-634Z_mlp-pi-ffff0000.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportJSONL("pi-cli", "rpc", link, cwd, "mlp-pi-ffff0000", t.TempDir()); err == nil {
		t.Fatal("followed a link")
	}
}
