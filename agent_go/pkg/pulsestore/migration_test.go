package pulsestore

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
)

func TestMigratePathBackfillsCompactPulseStoreAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "Workflow", "demo", "db", "db.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db := openMigrationFixture(t, dbPath)
	for _, statement := range []string{
		`CREATE TABLE pulse_module_audit (
			workspace_path TEXT,module TEXT,pulse_run_id TEXT,result TEXT,reason TEXT,
			evidence_json TEXT,recorded_at TEXT)`,
		`CREATE TABLE pulse_module_state (
			workspace_path TEXT,module TEXT,last_pulse_run_id TEXT,last_decision TEXT,last_reason TEXT,
			last_result TEXT,last_result_reason TEXT,next_check_at TEXT,evidence_json TEXT,updated_at TEXT)`,
		`CREATE TABLE run_concerns (
			fingerprint TEXT PRIMARY KEY,issue_id TEXT,step_id TEXT,phase TEXT,text TEXT,
			first_seen_run TEXT,first_seen_at TEXT,last_seen_run TEXT,last_seen_at TEXT,
			status TEXT,resolution_note TEXT)`,
		`CREATE TABLE pulse_finding_details (
			fingerprint TEXT PRIMARY KEY,issue_kind TEXT,detail_json TEXT,source_run_id TEXT,updated_at TEXT)`,
		`CREATE TABLE pulse_finding_events (
			_id INTEGER PRIMARY KEY AUTOINCREMENT,fingerprint TEXT,metadata_json TEXT)`,
		`CREATE TABLE report_human_inputs (
			id TEXT PRIMARY KEY,source TEXT,question TEXT,context TEXT,options_json TEXT,status TEXT,
			selected_option_id TEXT,note TEXT,created_at TEXT,answered_at TEXT,apply_contract_json TEXT)`,
		`INSERT INTO pulse_module_audit VALUES
			('Workflow/demo','strategic_review','run-1','done','Metrics improved','["metric:latency"]','2026-09-17T08:00:00Z')`,
		`INSERT INTO pulse_module_state VALUES
			('Workflow/demo','architecture_review','run-1','skipped','Not due yet','','','2026-10-01T00:00:00Z','[]','2026-09-17T08:00:01Z')`,
		`INSERT INTO run_concerns VALUES
			('abc123','PUL-ABC12345','step-a','review','Latency remains high','run-1','2026-09-17T08:00:00Z','run-1','2026-09-17T08:01:00Z','open',''),
			('def456','PUL-DEF45678','step-b','review','Old false alarm','run-0','2026-09-10T08:00:00Z','run-0','2026-09-10T08:01:00Z','rejected','Not reproducible')`,
		`INSERT INTO pulse_finding_details VALUES
			('abc123','workflow_issue','{"issue_kind":"workflow_issue","evidence":["run-1/metrics.json"],"human_input_id":"decision-1","module":"strategic_review"}','run-1','2026-09-17T08:01:00Z')`,
		`INSERT INTO pulse_finding_events(fingerprint,metadata_json) VALUES
			('abc123','{"human_input_id":"decision-1"}')`,
		`INSERT INTO report_human_inputs VALUES
			('decision-1','strategic_review','Apply the latency change?','Latency is above goal','[{"id":"yes"}]','pending','','','2026-09-17T08:02:00Z','','{"issue_id":"PUL-ABC12345"}')`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("fixture statement failed: %v\n%s", err, statement)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := MigratePath(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || result.Reviews != 2 || result.Issues != 2 || result.Decisions != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, err := os.Stat(result.BackupPath); err != nil {
		t.Fatalf("backup missing: %v", err)
	}

	db = openMigrationFixture(t, dbPath)
	defer db.Close()
	var status, summary, nextCheck string
	if err := db.QueryRow(`SELECT status,summary,next_check_at FROM pulse_reviews
		WHERE pulse_run_id='run-1' AND reviewer_type='architecture_review'`).Scan(&status, &summary, &nextCheck); err != nil {
		t.Fatal(err)
	}
	if status != "skipped" || summary != "Not due yet" || nextCheck != "2026-10-01T00:00:00Z" {
		t.Fatalf("unexpected review: status=%q summary=%q next=%q", status, summary, nextCheck)
	}
	var issueStatus, action, evidence string
	if err := db.QueryRow(`SELECT status,action_taken,evidence_json FROM pulse_issues WHERE id='PUL-DEF45678'`).Scan(&issueStatus, &action, &evidence); err != nil {
		t.Fatal(err)
	}
	if issueStatus != "closed" || action != "Not reproducible" {
		t.Fatalf("unexpected closed issue: status=%q action=%q evidence=%q", issueStatus, action, evidence)
	}
	if err := db.QueryRow(`SELECT evidence_json FROM pulse_issues WHERE id='PUL-ABC12345'`).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if evidence != `["run-1/metrics.json"]` {
		t.Fatalf("unexpected issue evidence: %s", evidence)
	}
	var decisionIssue string
	if err := db.QueryRow(`SELECT issue_id FROM pulse_decisions WHERE id='decision-1'`).Scan(&decisionIssue); err != nil {
		t.Fatal(err)
	}
	if decisionIssue != "PUL-ABC12345" {
		t.Fatalf("decision issue=%q", decisionIssue)
	}

	again, err := MigratePath(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !again.AlreadyCurrent || again.Applied || again.Reviews != 2 || again.Issues != 2 || again.Decisions != 1 {
		t.Fatalf("migration was not idempotent: %+v", again)
	}
}

func TestCompatibilityTriggersKeepCompactRowsCurrent(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "Workflow", "demo", "db", "db.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db := openMigrationFixture(t, dbPath)
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE pulse_module_audit (workspace_path TEXT,module TEXT,pulse_run_id TEXT,result TEXT,reason TEXT,evidence_json TEXT,recorded_at TEXT)`,
		`CREATE TABLE run_concerns (fingerprint TEXT PRIMARY KEY,issue_id TEXT,step_id TEXT,phase TEXT,text TEXT,first_seen_run TEXT,first_seen_at TEXT,last_seen_run TEXT,last_seen_at TEXT,status TEXT,resolution_note TEXT)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Ensure(ctx, db, dbPath); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO pulse_module_audit VALUES
		('Workflow/demo','technical_review','run-2','changed','Fixed invalid output','["test passed"]','2026-09-18T01:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO run_concerns VALUES
		('feed1234','PUL-FEED1234','step-x','review','Broken output','run-2','2026-09-18T01:00:00Z','run-2','2026-09-18T01:00:00Z','open','')`); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM pulse_reviews WHERE pulse_run_id='run-2' AND reviewer_type='technical_review'`).Scan(&status); err != nil || status != "changed" {
		t.Fatalf("projected review status=%q err=%v", status, err)
	}
	if _, err := db.Exec(`UPDATE run_concerns SET status='resolved',resolution_note='Updated the output contract',last_seen_at='2026-09-18T01:10:00Z' WHERE fingerprint='feed1234'`); err != nil {
		t.Fatal(err)
	}
	var action string
	if err := db.QueryRow(`SELECT status,action_taken FROM pulse_issues WHERE id='PUL-FEED1234'`).Scan(&status, &action); err != nil {
		t.Fatal(err)
	}
	if status != "closed" || action != "Updated the output contract" {
		t.Fatalf("projected issue status=%q action=%q", status, action)
	}
}

func TestMigrateWorkspaceDatabasesSkipsUnrelatedDatabase(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	pulsePath := filepath.Join(root, "Workflow", "pulse", "db", "db.sqlite")
	otherPath := filepath.Join(root, "Chats", "Work", "projects", "notes", "db", "db.sqlite")
	leakedTestPath := filepath.Join(root, "var", "folders", "tmp", "TestFixture", "db", "db.sqlite")
	for _, path := range []string{pulsePath, otherPath, leakedTestPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		db := openMigrationFixture(t, path)
		if path == pulsePath || path == leakedTestPath {
			_, _ = db.Exec(`CREATE TABLE run_concerns (fingerprint TEXT PRIMARY KEY,issue_id TEXT,step_id TEXT,phase TEXT,text TEXT,first_seen_run TEXT,first_seen_at TEXT,last_seen_run TEXT,last_seen_at TEXT,status TEXT,resolution_note TEXT)`)
		} else {
			_, _ = db.Exec(`CREATE TABLE notes (id TEXT PRIMARY KEY)`)
		}
		_ = db.Close()
	}
	report, err := MigrateWorkspaceDatabases(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 1 || report.Migrated != 1 || report.Skipped != 0 { // Chats/ (Crew, Code) is not scanned at all
		t.Fatalf("unexpected batch result: %+v", report)
	}
	db := openMigrationFixture(t, otherPath)
	defer db.Close()
	var exists int
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='pulse_reviews')`).Scan(&exists); err != nil || exists != 0 {
		t.Fatalf("unrelated database was modified: exists=%d err=%v", exists, err)
	}
}

func TestMigrationToleratesPartialLegacyTables(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "Workflow", "old", "db", "db.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db := openMigrationFixture(t, dbPath)
	if _, err := db.Exec(`CREATE TABLE pulse_module_audit (module TEXT, pulse_run_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := MigratePath(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || result.Reviews != 0 {
		t.Fatalf("unexpected partial-table migration: %+v", result)
	}
	db = openMigrationFixture(t, dbPath)
	defer db.Close()
	for _, statement := range []string{
		`ALTER TABLE pulse_module_audit ADD COLUMN result TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE pulse_module_audit ADD COLUMN reason TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE pulse_module_audit ADD COLUMN evidence_json TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE pulse_module_audit ADD COLUMN recorded_at TEXT NOT NULL DEFAULT ''`,
		`INSERT INTO pulse_module_audit(module,pulse_run_id,result,reason,evidence_json,recorded_at)
		 VALUES ('strategy_auditor','old-run','done','Recovered after legacy upgrade','[]','2026-09-18')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := RefreshCompatibility(ctx, db); err != nil {
		t.Fatal(err)
	}
	var reviewer string
	if err := db.QueryRow(`SELECT reviewer_type FROM pulse_reviews WHERE pulse_run_id='old-run'`).Scan(&reviewer); err != nil {
		t.Fatal(err)
	}
	if reviewer != "strategic_review" {
		t.Fatalf("legacy reviewer was not normalized: %q", reviewer)
	}
}

func TestGenericPulseDecisionAndFindingEventAreProjected(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "Workflow", "decisions", "db", "db.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db := openMigrationFixture(t, dbPath)
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE run_concerns (fingerprint TEXT PRIMARY KEY,issue_id TEXT,step_id TEXT,phase TEXT,text TEXT,first_seen_run TEXT,first_seen_at TEXT,last_seen_run TEXT,last_seen_at TEXT,status TEXT,resolution_note TEXT)`,
		`CREATE TABLE pulse_finding_events (_id INTEGER PRIMARY KEY AUTOINCREMENT,fingerprint TEXT,metadata_json TEXT)`,
		`CREATE TABLE report_human_inputs (id TEXT PRIMARY KEY,source TEXT,question TEXT,context TEXT,options_json TEXT,status TEXT,selected_option_id TEXT,note TEXT,created_at TEXT,answered_at TEXT,apply_contract_json TEXT)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Ensure(ctx, db, dbPath); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO run_concerns VALUES ('abc','PUL-ABC00001','step','review','Choose an action','run','2026-09-18','run','2026-09-18','open','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO report_human_inputs VALUES ('choice-1','pulse','Choose?','','[]','pending','','','2026-09-18','','{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO pulse_finding_events(fingerprint,metadata_json) VALUES ('abc','{"human_input_id":"choice-1"}')`); err != nil {
		t.Fatal(err)
	}
	var issueID string
	if err := db.QueryRow(`SELECT issue_id FROM pulse_decisions WHERE id='choice-1'`).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	if issueID != "PUL-ABC00001" {
		t.Fatalf("decision issue=%q", issueID)
	}
}

func TestVersionTwoRemovesRetiredReviewerRows(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "Workflow", "canary", "db", "db.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db := openMigrationFixture(t, dbPath)
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO pulse_schema_migrations(version,applied_at,backup_path,legacy_tables_retained)
		VALUES (1,'2026-09-17','','1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO pulse_reviews
		(id,pulse_run_id,reviewer_type,status,summary,evidence_json,next_check_at,created_at,updated_at) VALUES
		('review:r1:artifact_review','r1','artifact_review','done','retired','[]','','2026-09-17','2026-09-17'),
		('review:r1:technical_review','r1','technical_review','done','current','[]','','2026-09-17','2026-09-17')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := MigratePath(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || result.Reviews != 1 {
		t.Fatalf("unexpected v2 result: %+v", result)
	}
	db = openMigrationFixture(t, dbPath)
	defer db.Close()
	var version, retired int
	if err := db.QueryRow(`SELECT MAX(version) FROM pulse_schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM pulse_reviews WHERE reviewer_type='artifact_review'`).Scan(&retired); err != nil {
		t.Fatal(err)
	}
	if version != 2 || retired != 0 {
		t.Fatalf("version=%d retired=%d", version, retired)
	}
}

func openMigrationFixture(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteopen.DSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db
}

// One folder the server cannot open (a build cache, another account's sandbox home) must not stop the whole
// deployment-time pass, and dot folders are never scanned.
func TestMigrateWorkspaceDatabasesSkipsUnreadableAndHiddenFolders(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	pulsePath := filepath.Join(root, "Workflow", "pulse", "db", "db.sqlite")
	hiddenPath := filepath.Join(root, "Crew", "demo", ".sandbox-cache", "home", "db", "db.sqlite")
	releasePath := filepath.Join(root, "Workflow", ".relay_releases", "flow", "v1", "db", "db.sqlite")
	for _, path := range []string{pulsePath, hiddenPath, releasePath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		db := openMigrationFixture(t, path)
		_, _ = db.Exec(`CREATE TABLE run_concerns (fingerprint TEXT PRIMARY KEY,issue_id TEXT,step_id TEXT,phase TEXT,text TEXT,first_seen_run TEXT,first_seen_at TEXT,last_seen_run TEXT,last_seen_at TEXT,status TEXT,resolution_note TEXT)`)
		_ = db.Close()
	}
	locked := filepath.Join(root, "Crew", "demo", "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	report, err := MigrateWorkspaceDatabases(ctx, root)
	if err != nil {
		t.Fatalf("an unreadable folder must not abort the pass: %v", err)
	}
	if report.Scanned != 2 || report.Migrated != 2 {
		t.Fatalf("the workspace and frozen-release databases are migrated, not the one under a hidden folder: %+v", report)
	}
}
