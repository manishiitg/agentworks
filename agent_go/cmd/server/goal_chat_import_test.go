package server

import (
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PLAT-746-265: JSON-only Goal history disappeared from the Crew screen;
// importing it must preserve the person boundary and never duplicate a journal.
func TestGoalChatImportPreservesOwnerAndExistingJournal(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, "events.sqlite")
	archive := filepath.Join(root, "chat.json")
	if err := os.WriteFile(archive, []byte(`{"session_id":"saved","user_id":"owner","updated_at":"2026-01-01T00:00:00Z","conversation_history":[{"Role":"human","Parts":[{"Text":"Original question"}]},{"Role":"ai","Parts":[{"Text":"Original answer"}]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	item := goalChatImport{Session: "saved", Owner: "other", Archive: archive}
	if _, err := importGoalChatArchives(db, []goalChatImport{item}); err == nil {
		t.Fatal("accepted another person's archive")
	}
	item.Owner = "owner"
	if n, err := importGoalChatArchives(db, []goalChatImport{item}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if n, err := importGoalChatArchives(db, []goalChatImport{item}); err != nil || n != 0 {
		t.Fatal("reimport duplicated history", n, err)
	}
	item.Owner = "other"
	if _, err := importGoalChatArchives(db, []goalChatImport{item}); err == nil {
		t.Fatal("accepted another person's journal")
	}
	journal, err := events.OpenSQLiteEventJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	page, err := journal.ReadPage("saved", events.DurableEventPageOptions{Limit: 100})
	raw, _ := json.Marshal(page)
	if err != nil || !strings.Contains(string(raw), "Original question") || !strings.Contains(string(raw), "Original answer") {
		t.Fatal("dialogue missing", string(raw), err)
	}
	// An unfinished archive must not become a permanently running Crew chat.
	if err := os.WriteFile(archive, []byte(`{"session_id":"unfinished","user_id":"owner","updated_at":"2026-01-01T00:00:00Z","conversation_history":[{"Role":"human","Parts":[{"Text":"Unanswered question"}]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := importGoalChatArchives(db, []goalChatImport{{Session: "unfinished", Owner: "owner", Archive: archive}}); err != nil {
		t.Fatal(err)
	}
	unfinished, err := journal.ReadPage("unfinished", events.DurableEventPageOptions{Limit: 100})
	if err != nil || len(unfinished.Events) != 2 || unfinished.Events[1].Type != "context_cancelled" {
		t.Fatal("unfinished imported turn must be stopped", unfinished, err)
	}
}
