package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// #3707: an Edit and resend drops the Team's copy (history and chunks) so removed turns are not readable; answers
// teammates returned stay, a source that does not match is refused, and a repeat (a lost confirmation) is harmless.
func TestCrewOfflineRewindResetDropsCopyKeepsAnswers(t *testing.T) {
	dir := t.TempDir()
	b := crewOfflineBinding{Provider: "codex", Session: "11111111-2222-4333-8444-555555555555", Attachment: "source-attachment"}
	h := crewOfflineHistory{crewOfflineBinding: b, crewOfflineCursor: crewOfflineCursor{Revision: 2, Offset: 40, TailHash: "x"}, Tail: []byte("removed turn"), Batches: []chunkedFile{}}
	if err := fsutil.WriteJSONAtomic(filepath.Join(dir, "history.json"), h, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "chunks"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chunks", "c1"), []byte("removed turn"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := appendCrewOfflineReturn("crew", "agent", dir, b, crewOfflineReturnedAnswer{ID: "a1", At: time.Now().UTC(), Asker: "teammate", Question: "q", Answer: "a"}); err != nil {
		t.Fatal(err)
	}
	other := b
	other.Session = "22222222-3333-4444-8555-666666666666"
	if err := resetCrewOfflineCopy("crew", "agent", dir, other); err == nil {
		t.Fatal("a source that does not match must not drop the copy")
	}
	if _, err := os.Stat(filepath.Join(dir, "history.json")); err != nil {
		t.Fatal("refused reset still removed the copy")
	}
	for range 2 {
		if err := resetCrewOfflineCopy("crew", "agent", dir, b); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"history.json", "chunks"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s survived the reset", name)
		}
	}
	if state, err := readCrewOfflineReturns(dir, b); err != nil || len(state.Pending) != 1 {
		t.Fatal("returned answers must stay", err)
	}
	if fresh, err := readCrewOfflineHistory(dir, b); err != nil || fresh.Offset != 0 || fresh.Revision != 0 {
		t.Fatal("after a reset the copy starts from the beginning", err)
	}
}
