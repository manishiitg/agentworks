package server

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// A moved Crew's cost history follows it; another Crew's rows and a second run are left alone.
func TestRenameCostLedgerKeysRefilesOnlyTheMovedCrew(t *testing.T) {
	docs := t.TempDir()
	if err := os.MkdirAll(filepath.Join(docs, "_system"), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(docs, "_system", "costs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE cost_events (event_id TEXT PRIMARY KEY, workflow_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	old, other := "_users/u1/Chats/Work/projects/yami-1", "_users/u1/Chats/Work/projects/other-2"
	for i, k := range []string{old, old, "Crew/yami-1", other} {
		if _, err := db.Exec(`INSERT INTO cost_events VALUES (?, ?)`, string(rune('a'+i)), k); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := renameCostLedgerKeys(docs, old, "Crew/yami-1"); err != nil || n != 2 {
		t.Fatalf("first run: %d %v, want 2 rows", n, err)
	}
	if n, _ := renameCostLedgerKeys(docs, old, "Crew/yami-1"); n != 0 {
		t.Fatalf("second run renamed %d rows, want 0", n)
	}
	var moved, kept int
	_ = db.QueryRow(`SELECT COUNT(*) FROM cost_events WHERE workflow_id='Crew/yami-1'`).Scan(&moved)
	_ = db.QueryRow(`SELECT COUNT(*) FROM cost_events WHERE workflow_id=?`, other).Scan(&kept)
	if moved != 3 || kept != 1 {
		t.Fatalf("moved=%d kept=%d, want 3 and 1", moved, kept)
	}
	if n, err := renameCostLedgerKeys(t.TempDir(), old, "Crew/yami-1"); err != nil || n != 0 {
		t.Fatalf("no ledger must be a no-op: %d %v", n, err)
	}
}
