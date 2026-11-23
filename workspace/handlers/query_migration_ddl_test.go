package handlers

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
)

// TestInitializeWorkflowDBAcceptsIdempotentDropTable proves DROP TABLE IF
// EXISTS is accepted, actually drops the table, and is a safe no-op on retry.
func TestInitializeWorkflowDBAcceptsIdempotentDropTable(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	create := models.InitializeDatabaseRequest{DBPath: rel, Migrations: []string{
		"CREATE TABLE IF NOT EXISTS scratch (id TEXT PRIMARY KEY)",
	}}
	if recorder := postWorkflowDBTest(t, router, "/api/db/initialize", create); recorder.Code != http.StatusOK {
		t.Fatalf("create failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	drop := models.InitializeDatabaseRequest{DBPath: rel, Migrations: []string{
		"DROP TABLE IF EXISTS scratch",
	}}
	for attempt := 0; attempt < 2; attempt++ {
		recorder := postWorkflowDBTest(t, router, "/api/db/initialize", drop)
		if recorder.Code != http.StatusOK {
			t.Fatalf("drop attempt %d failed: status=%d body=%s", attempt+1, recorder.Code, recorder.Body.String())
		}
	}

	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='scratch'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("table still present after DROP TABLE IF EXISTS")
	}
}

// TestInitializeWorkflowDBDestructiveMigrationSnapshotsFirst proves a
// destructive migration (here, DROP COLUMN) writes a pre-migration backup
// that still has the dropped data, since there is no approval gate on this
// route to catch a mistake before it runs.
func TestInitializeWorkflowDBDestructiveMigrationSnapshotsFirst(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	create := models.InitializeDatabaseRequest{DBPath: rel, Migrations: []string{
		"CREATE TABLE IF NOT EXISTS widgets (id TEXT PRIMARY KEY, retiring_field TEXT)",
	}}
	if recorder := postWorkflowDBTest(t, router, "/api/db/initialize", create); recorder.Code != http.StatusOK {
		t.Fatalf("create failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	seedDB, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seedDB.Exec("INSERT INTO widgets(id, retiring_field) VALUES ('w1', 'irreplaceable')"); err != nil {
		seedDB.Close()
		t.Fatal(err)
	}
	seedDB.Close()

	dropColumn := models.InitializeDatabaseRequest{DBPath: rel, Migrations: []string{
		"ALTER TABLE widgets DROP COLUMN retiring_field",
	}}
	recorder := postWorkflowDBTest(t, router, "/api/db/initialize", dropColumn)
	if recorder.Code != http.StatusOK {
		t.Fatalf("drop column failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response models.APIResponse[map[string]any]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	backupPath, _ := response.Data["backup_path"].(string)
	if backupPath == "" {
		t.Fatalf("destructive migration did not report a backup_path: %s", recorder.Body.String())
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup file missing on disk: %v", err)
	}

	backupDB, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer backupDB.Close()
	var recovered string
	if err := backupDB.QueryRow("SELECT retiring_field FROM widgets WHERE id='w1'").Scan(&recovered); err != nil {
		t.Fatalf("dropped column not recoverable from backup: %v", err)
	}
	if recovered != "irreplaceable" {
		t.Fatalf("backup has wrong value %q", recovered)
	}

	liveDB, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	defer liveDB.Close()
	var columnCount int
	rows, err := liveDB.Query("PRAGMA table_info(widgets)")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		columnCount++
	}
	rows.Close()
	if columnCount != 1 {
		t.Fatalf("live table still has the dropped column: %d columns", columnCount)
	}
}

// TestInitializeWorkflowDBAdditiveAlterDoesNotSnapshot proves ADD COLUMN,
// which can only add data, never triggers the destructive-migration backup
// path -- keeping the common additive case cheap.
func TestInitializeWorkflowDBAdditiveAlterDoesNotSnapshot(t *testing.T) {
	rel, _, router := setupWorkflowDBTest(t)
	create := models.InitializeDatabaseRequest{DBPath: rel, Migrations: []string{
		"CREATE TABLE IF NOT EXISTS widgets (id TEXT PRIMARY KEY)",
	}}
	if recorder := postWorkflowDBTest(t, router, "/api/db/initialize", create); recorder.Code != http.StatusOK {
		t.Fatalf("create failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	addColumn := models.InitializeDatabaseRequest{DBPath: rel, Migrations: []string{
		"ALTER TABLE widgets ADD COLUMN new_field TEXT",
	}}
	recorder := postWorkflowDBTest(t, router, "/api/db/initialize", addColumn)
	if recorder.Code != http.StatusOK {
		t.Fatalf("add column failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response models.APIResponse[map[string]any]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if backupPath, ok := response.Data["backup_path"]; ok {
		t.Fatalf("additive ALTER TABLE ADD COLUMN triggered a snapshot: %v", backupPath)
	}
}

// TestInitializeWorkflowDBAcceptsRenameForms proves ALTER TABLE ... RENAME TO
// and ALTER TABLE ... RENAME COLUMN are accepted and take effect.
func TestInitializeWorkflowDBAcceptsRenameForms(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	create := models.InitializeDatabaseRequest{DBPath: rel, Migrations: []string{
		"CREATE TABLE IF NOT EXISTS old_name (id TEXT PRIMARY KEY, old_col TEXT)",
	}}
	if recorder := postWorkflowDBTest(t, router, "/api/db/initialize", create); recorder.Code != http.StatusOK {
		t.Fatalf("create failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	renameColumn := models.InitializeDatabaseRequest{DBPath: rel, Migrations: []string{
		"ALTER TABLE old_name RENAME COLUMN old_col TO new_col",
	}}
	if recorder := postWorkflowDBTest(t, router, "/api/db/initialize", renameColumn); recorder.Code != http.StatusOK {
		t.Fatalf("rename column failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	renameTable := models.InitializeDatabaseRequest{DBPath: rel, Migrations: []string{
		"ALTER TABLE old_name RENAME TO new_name",
	}}
	if recorder := postWorkflowDBTest(t, router, "/api/db/initialize", renameTable); recorder.Code != http.StatusOK {
		t.Fatalf("rename table failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var col string
	if err := db.QueryRow("SELECT new_col FROM new_name LIMIT 1").Scan(&col); err != nil && err != sql.ErrNoRows {
		t.Fatalf("renamed table/column not queryable: %v", err)
	}
}

// TestInitializeWorkflowDBStillRejectsPragmaAndAttach proves the two
// deliberately excluded statement kinds remain blocked even though DROP and
// ALTER are now allowed: PRAGMA can change database-wide behavior other
// concurrent readers/writers depend on, and ATTACH opens an arbitrary
// filesystem path outside FolderGuard's authorization.
func TestInitializeWorkflowDBStillRejectsPragmaAndAttach(t *testing.T) {
	rel, _, router := setupWorkflowDBTest(t)
	for _, migration := range []string{
		"PRAGMA journal_mode=DELETE",
		"PRAGMA foreign_keys=OFF",
		"ATTACH DATABASE '/etc/passwd' AS evil",
		"DROP TABLE widgets",               // still rejected without IF EXISTS
		"RENAME TABLE widgets TO widgets2", // not SQLite's ALTER syntax
	} {
		recorder := postWorkflowDBTest(t, router, "/api/db/initialize", models.InitializeDatabaseRequest{DBPath: rel, Migrations: []string{migration}})
		if recorder.Code == http.StatusOK {
			t.Fatalf("migration %q was accepted, want rejected", migration)
		}
	}
}

// TestBackupDatabaseBeforeDestructiveMigrationSkipsMissingFile proves the
// backup helper is never called against a database that does not exist yet
// (InitializeWorkflowDB only backs up when os.Stat succeeds), so a first-run
// destructive migration against a brand-new database is not itself an error.
func TestInitializeWorkflowDBDestructiveMigrationOnFreshDatabaseNeedsNoBackup(t *testing.T) {
	rel, _, router := setupWorkflowDBTest(t)
	// DROP TABLE IF EXISTS on a table (and database) that has never existed
	// is a valid no-op; it must not fail merely because there was nothing to
	// snapshot yet.
	drop := models.InitializeDatabaseRequest{DBPath: rel, Migrations: []string{
		"DROP TABLE IF EXISTS never_existed",
	}}
	recorder := postWorkflowDBTest(t, router, "/api/db/initialize", drop)
	if recorder.Code != http.StatusOK {
		t.Fatalf("drop-if-exists on fresh database failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

// TestInitializeWorkflowDBRecordsDurableLedgerEntry is a code review finding:
// the migration route claimed to be an "auditable route" but recorded only a
// process log line, not anything durable. Every applied migration must now
// leave a schema_migration_log row recording its source filename, a content
// hash (not the raw SQL, which already lives in the caller's own
// db/migrations/ file when one exists), whether it was destructive, its
// backup path, and who applied it.
func TestInitializeWorkflowDBRecordsDurableLedgerEntry(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	migrations := []string{"CREATE TABLE IF NOT EXISTS widgets (id TEXT PRIMARY KEY)"}
	request := models.InitializeDatabaseRequest{DBPath: rel, Migrations: migrations, MigrationFile: "2026-08-06-widgets.sql"}
	recorder := postWorkflowDBTest(t, router, "/api/db/initialize", request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("create failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var migrationFile, statementsHash, backupPath, appliedBy, appliedAt string
	var destructive int
	if err := db.QueryRow(`SELECT migration_file, statements_hash, destructive, backup_path, applied_by, applied_at FROM schema_migration_log ORDER BY id DESC LIMIT 1`).
		Scan(&migrationFile, &statementsHash, &destructive, &backupPath, &appliedBy, &appliedAt); err != nil {
		t.Fatalf("ledger row missing: %v", err)
	}
	if migrationFile != "2026-08-06-widgets.sql" {
		t.Fatalf("migration_file = %q", migrationFile)
	}
	wantHash := sha256.Sum256([]byte(strings.Join(migrations, "\n")))
	if statementsHash != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("statements_hash = %q, want a real content hash", statementsHash)
	}
	if destructive != 0 {
		t.Fatalf("destructive = %d, want 0 for a CREATE-only migration", destructive)
	}
	if backupPath != "" {
		t.Fatalf("backup_path = %q, want empty for a non-destructive migration", backupPath)
	}
	if appliedBy == "" {
		t.Fatalf("applied_by is empty")
	}
	if _, err := time.Parse(time.RFC3339Nano, appliedAt); err != nil {
		t.Fatalf("applied_at = %q is not a valid timestamp: %v", appliedAt, err)
	}
}

// TestInitializeWorkflowDBLedgerRecordsBackupPathForDestructiveMigrations
// proves the ledger and the backup mechanism are linked: a destructive
// migration's ledger row names the exact snapshot it can be recovered from.
func TestInitializeWorkflowDBLedgerRecordsBackupPathForDestructiveMigrations(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	create := models.InitializeDatabaseRequest{DBPath: rel, Migrations: []string{
		"CREATE TABLE IF NOT EXISTS widgets (id TEXT PRIMARY KEY)",
	}}
	if recorder := postWorkflowDBTest(t, router, "/api/db/initialize", create); recorder.Code != http.StatusOK {
		t.Fatalf("create failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	drop := models.InitializeDatabaseRequest{DBPath: rel, Migrations: []string{
		"DROP TABLE IF EXISTS widgets",
	}}
	if recorder := postWorkflowDBTest(t, router, "/api/db/initialize", drop); recorder.Code != http.StatusOK {
		t.Fatalf("drop failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var destructive int
	var backupPath string
	if err := db.QueryRow(`SELECT destructive, backup_path FROM schema_migration_log ORDER BY id DESC LIMIT 1`).
		Scan(&destructive, &backupPath); err != nil {
		t.Fatalf("ledger row missing: %v", err)
	}
	if destructive != 1 {
		t.Fatalf("destructive = %d, want 1", destructive)
	}
	if backupPath == "" {
		t.Fatalf("backup_path is empty for a destructive migration")
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("ledger's backup_path does not exist on disk: %v", err)
	}
}

// TestInitializeWorkflowDBLedgerEntryRolledBackWithFailedMigration proves the
// ledger is atomic with the migration it records: a migration batch that
// fails partway through must leave neither a schema change nor a ledger row,
// not a ledger row claiming a migration that never actually committed.
func TestInitializeWorkflowDBLedgerEntryRolledBackWithFailedMigration(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	// ADD COLUMN twice in the same request: the second application fails
	// (SQLite ALTER has no idempotent form), so the whole transaction must
	// roll back -- including any ledger row -- not partially commit.
	create := models.InitializeDatabaseRequest{DBPath: rel, Migrations: []string{
		"CREATE TABLE IF NOT EXISTS widgets (id TEXT PRIMARY KEY)",
	}}
	if recorder := postWorkflowDBTest(t, router, "/api/db/initialize", create); recorder.Code != http.StatusOK {
		t.Fatalf("create failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	addColumn := models.InitializeDatabaseRequest{DBPath: rel, Migrations: []string{
		"ALTER TABLE widgets ADD COLUMN new_field TEXT",
	}}
	if recorder := postWorkflowDBTest(t, router, "/api/db/initialize", addColumn); recorder.Code != http.StatusOK {
		t.Fatalf("first add column failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var ledgerCountBefore int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migration_log`).Scan(&ledgerCountBefore); err != nil {
		t.Fatal(err)
	}

	// Same statement again: SQLite rejects a duplicate column, so this must fail.
	recorder := postWorkflowDBTest(t, router, "/api/db/initialize", addColumn)
	if recorder.Code == http.StatusOK {
		t.Fatalf("duplicate ADD COLUMN unexpectedly succeeded")
	}

	var ledgerCountAfter int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migration_log`).Scan(&ledgerCountAfter); err != nil {
		t.Fatal(err)
	}
	if ledgerCountAfter != ledgerCountBefore {
		t.Fatalf("ledger count changed from %d to %d despite the migration failing", ledgerCountBefore, ledgerCountAfter)
	}
}

// TestPruneMigrationBackupsKeepsOnlyTheMostRecent proves destructive-migration
// snapshots do not grow without bound: nothing else limited their count, and
// every retried destructive migration writes another complete database copy.
func TestPruneMigrationBackupsKeepsOnlyTheMostRecent(t *testing.T) {
	backupDir := t.TempDir()
	total := migrationBackupRetentionCount + 5
	var names []string
	for i := 0; i < total; i++ {
		name := filepath.Join(backupDir, formatBackupTestName(i))
		if err := os.WriteFile(name, []byte("fake snapshot"), 0o600); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
		// Distinct, increasing modification times so "most recent" is
		// unambiguous regardless of filesystem timestamp resolution.
		modTime := time.Now().Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(name, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}

	pruneMigrationBackups(backupDir)

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != migrationBackupRetentionCount {
		t.Fatalf("backups remaining = %d, want %d", len(entries), migrationBackupRetentionCount)
	}
	// The oldest (lowest index) backups must be the ones removed.
	if _, err := os.Stat(names[0]); err == nil {
		t.Fatalf("oldest backup %q was not pruned", names[0])
	}
	if _, err := os.Stat(names[total-1]); err != nil {
		t.Fatalf("newest backup %q was pruned: %v", names[total-1], err)
	}
}

func formatBackupTestName(index int) string {
	return fmt.Sprintf("backup-%04d-pre-migration.sqlite", index)
}
