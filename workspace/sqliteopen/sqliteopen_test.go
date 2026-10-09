package sqliteopen

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestDSNProducesWALModeAndBusyTimeout locks in the fix for a real incident:
// get_pulse_state(view=module) hit persistent SQLITE_BUSY under concurrent
// load because its DB was opened in SQLite's default rollback-journal mode.
// Every caller of DSN must get WAL mode and a positive busy_timeout, since
// both are DSN-embedded pragmas relied on by every connection a pool might
// open, not a one-time runtime PRAGMA that could miss a second connection.
func TestDSNProducesWALModeAndBusyTimeout(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")

	db, err := sql.Open("sqlite", DSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("CREATE TABLE t(id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("create table (forces the file to actually be created): %v", err)
	}

	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want \"wal\"", journalMode)
	}

	var busyTimeoutMs int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeoutMs); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busyTimeoutMs <= 0 {
		t.Fatalf("busy_timeout = %d, want > 0", busyTimeoutMs)
	}
}

// A Windows drive-letter path must be a valid SQLite file address (file:///C:/dir/file), not file:C:%5Cdir%5Cfile.
func TestDSNForADriveLetterPath(t *testing.T) {
	if got := DSN(`C:/Users/me/lock.sqlite`); !strings.HasPrefix(got, "file:///C:/Users/me/lock.sqlite?") {
		t.Fatalf("got %q", got)
	}
	if got := DSN("/var/lib/x/lock.sqlite"); !strings.HasPrefix(got, "file:///var/lib/x/lock.sqlite?") {
		t.Fatalf("got %q", got)
	}
}
