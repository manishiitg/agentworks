package step_based_workflow

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestExtractReportQueriesHandlesAllQuoteStyles(t *testing.T) {
	html := "" +
		"<script>\n" +
		"const rows1 = await window.report.query('SELECT id FROM emails');\n" +
		"const rows2 = await window.report.query(\"SELECT status FROM emails\");\n" +
		"const rows3 = await window.report.query(`SELECT note FROM emails`);\n" +
		"</script>"
	got := extractReportQueries(html)
	want := []string{"SELECT id FROM emails", "SELECT status FROM emails", "SELECT note FROM emails"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractReportQueries = %v, want %v", got, want)
	}
}

func TestExtractReportQueriesDedupsAndPreservesOrder(t *testing.T) {
	html := "" +
		"window.report.query('SELECT b FROM t');\n" +
		"window.report.query('SELECT a FROM t');\n" +
		"window.report.query('SELECT b FROM t');\n"
	got := extractReportQueries(html)
	want := []string{"SELECT b FROM t", "SELECT a FROM t"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractReportQueries = %v, want %v (dedup should keep first occurrence position)", got, want)
	}
}

func TestExtractReportQueriesHandlesEscapedQuotes(t *testing.T) {
	html := `window.report.query('SELECT * FROM t WHERE name = \'x\'')`
	got := extractReportQueries(html)
	want := []string{`SELECT * FROM t WHERE name = 'x'`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractReportQueries = %v, want %v", got, want)
	}
}

func TestExtractReportQueriesReturnsEmptyForNoCalls(t *testing.T) {
	got := extractReportQueries("<html><body>no report calls here</body></html>")
	if len(got) != 0 {
		t.Fatalf("extractReportQueries = %v, want empty", got)
	}
}

func setupPlanDriftDBTest(t *testing.T, workspacePath string) string {
	t.Helper()
	docsDir := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsDir)
	dbDir := filepath.Join(docsDir, workspacePath, "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("mkdir db dir: %v", err)
	}
	return filepath.Join(dbDir, "db.sqlite")
}

func TestCheckReportQueryCompatibilityPassesWhenSchemaMatches(t *testing.T) {
	ctx := context.Background()
	dbPath := setupPlanDriftDBTest(t, "Workflow/drift-test")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE emails(id INTEGER PRIMARY KEY, status TEXT)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	readFile := func(_ context.Context, path string) (string, error) {
		if path == "Workflow/drift-test/db/reports/index.html" {
			return `<script>window.report.query('SELECT id, status FROM emails')</script>`, nil
		}
		return "", os.ErrNotExist
	}

	check, err := CheckReportQueryCompatibility(ctx, "Workflow/drift-test", readFile)
	if err != nil {
		t.Fatalf("CheckReportQueryCompatibility returned error: %v", err)
	}
	if check.Status != "pass" {
		t.Fatalf("status = %q, want pass; evidence=%s", check.Status, check.Evidence)
	}
	if check.CheckID != reportDriftCheckID {
		t.Fatalf("check_id = %q, want %q", check.CheckID, reportDriftCheckID)
	}
	if check.Evidence == "" {
		t.Fatal("evidence must not be empty even on pass")
	}
}

func TestCheckReportQueryCompatibilityReleasesSuccessfulQueryRows(t *testing.T) {
	ctx := context.Background()
	dbPath := setupPlanDriftDBTest(t, "Workflow/drift-test")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE emails(id INTEGER PRIMARY KEY, status TEXT); INSERT INTO emails(status) VALUES ('pending')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	readFile := func(_ context.Context, path string) (string, error) {
		if path == "Workflow/drift-test/db/reports/index.html" {
			return `<script>
				window.report.query('SELECT id FROM emails');
				window.report.query('SELECT status FROM emails');
			</script>`, nil
		}
		return "", os.ErrNotExist
	}
	if _, err := CheckReportQueryCompatibility(ctx, "Workflow/drift-test", readFile); err != nil {
		t.Fatalf("CheckReportQueryCompatibility returned error: %v", err)
	}

	// A successful compatibility read must release every result set. The
	// managed dependency audit writes its Pulse receipt immediately after this
	// scan; an abandoned read transaction used to make that follow-up wait for
	// the request timeout.
	writeDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writeDB.Close()
	if _, err := writeDB.Exec(`PRAGMA busy_timeout = 100`); err != nil {
		t.Fatal(err)
	}
	if _, err := writeDB.Exec(`INSERT INTO emails(status) VALUES ('sent')`); err != nil {
		t.Fatalf("report compatibility check retained a database read lock: %v", err)
	}
}

func TestCheckReportQueryCompatibilityFailsWhenColumnDropped(t *testing.T) {
	ctx := context.Background()
	dbPath := setupPlanDriftDBTest(t, "Workflow/drift-test")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Simulates a step that used to write a "status" column and no longer does.
	if _, err := db.Exec(`CREATE TABLE emails(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	readFile := func(_ context.Context, path string) (string, error) {
		if path == "Workflow/drift-test/db/reports/index.html" {
			return `<script>window.report.query('SELECT id, status FROM emails')</script>`, nil
		}
		return "", os.ErrNotExist
	}

	check, err := CheckReportQueryCompatibility(ctx, "Workflow/drift-test", readFile)
	if err != nil {
		t.Fatalf("CheckReportQueryCompatibility returned error: %v", err)
	}
	if check.Status != "fail" {
		t.Fatalf("status = %q, want fail (report query references a dropped column)", check.Status)
	}
	if check.Evidence == "" {
		t.Fatal("evidence must not be empty on fail")
	}
}

func TestCheckReportQueryCompatibilityPassesWhenNoReportExists(t *testing.T) {
	ctx := context.Background()
	setupPlanDriftDBTest(t, "Workflow/drift-test")

	readFile := func(_ context.Context, _ string) (string, error) {
		return "", os.ErrNotExist
	}

	check, err := CheckReportQueryCompatibility(ctx, "Workflow/drift-test", readFile)
	if err != nil {
		t.Fatalf("CheckReportQueryCompatibility returned error: %v", err)
	}
	if check.Status != "pass" {
		t.Fatalf("status = %q, want pass (no report to check)", check.Status)
	}
	if check.Evidence == "" {
		t.Fatal("evidence must explain why there was nothing to check")
	}
}

func TestCheckReportQueryCompatibilityDoesNotMutateDB(t *testing.T) {
	ctx := context.Background()
	dbPath := setupPlanDriftDBTest(t, "Workflow/drift-test")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE emails(id INTEGER PRIMARY KEY, status TEXT); INSERT INTO emails(id, status) VALUES (1, 'pending')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	// A malicious/buggy report embedding a mutation must never actually run —
	// the check opens the DB query_only, so this must fail to execute, not
	// silently mutate real data.
	readFile := func(_ context.Context, path string) (string, error) {
		if path == "Workflow/drift-test/db/reports/index.html" {
			return `<script>window.report.query("UPDATE emails SET status='hacked'")</script>`, nil
		}
		return "", os.ErrNotExist
	}

	if _, err := CheckReportQueryCompatibility(ctx, "Workflow/drift-test", readFile); err != nil {
		t.Fatalf("CheckReportQueryCompatibility returned error: %v", err)
	}

	check, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var status string
	if err := check.QueryRow(`SELECT status FROM emails WHERE id=1`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("status = %q, want unchanged %q — query_only guard did not hold", status, "pending")
	}
}

// TestCheckReportQueryCompatibilityHandlesMultipleQueriesWithoutBlocking
// covers a real bug found live on the Dominion deployment 2026-08-31:
// db.QueryContext's returned *sql.Rows was discarded without Close() on a
// successful query. openPlanDriftQueryOnlyDB caps the connection pool at
// exactly 1 (SetMaxOpenConns(1)), so the first successful query in the loop
// permanently held the pool's only connection — every query after it then
// blocked forever in database/sql's own connection-acquisition code,
// unblockable by anything except the caller's context expiring. A report
// with a single query (the shape every other test here uses) can never
// exercise this: the bug only shows once a second query needs a connection
// after the first one already succeeded. Bounded by a 5s context so a
// regression fails loudly instead of hanging the test suite.
func TestCheckReportQueryCompatibilityHandlesMultipleQueriesWithoutBlocking(t *testing.T) {
	dbPath := setupPlanDriftDBTest(t, "Workflow/drift-multi-query")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE emails(id INTEGER PRIMARY KEY, status TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE trades(id INTEGER PRIMARY KEY, symbol TEXT)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	readFile := func(_ context.Context, path string) (string, error) {
		if path == "Workflow/drift-multi-query/db/reports/index.html" {
			return `<script>
window.report.query('SELECT id, status FROM emails')
window.report.query('SELECT id, symbol FROM trades')
window.report.query('SELECT id, status FROM emails WHERE id > 0')
</script>`, nil
		}
		return "", os.ErrNotExist
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	check, err := CheckReportQueryCompatibility(ctx, "Workflow/drift-multi-query", readFile)
	if err != nil {
		t.Fatalf("CheckReportQueryCompatibility returned error: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("hit the 5s test timeout instead of completing on its own — a query after the first successful one is blocked waiting for a connection the pool never releases; evidence=%s", check.Evidence)
	}
	if check.Status != "pass" {
		t.Fatalf("status = %q, want pass (all 3 queries against matching schema); evidence=%s", check.Status, check.Evidence)
	}
}

func TestCheckValidationSchemaDBRulesPassesWhenAssertionsHold(t *testing.T) {
	ctx := context.Background()
	dbPath := setupPlanDriftDBTest(t, "Workflow/drift-test")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE emails(id INTEGER PRIMARY KEY, status TEXT); INSERT INTO emails VALUES (1, 'sent')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	rules := []DBValidationRule{{
		Name: "at least one sent email",
		SQL:  "SELECT status FROM emails WHERE status='sent'",
		Checks: []JSONValidationCheck{
			{Path: "$.status", MustExist: true, ValueType: "string"},
		},
	}}

	check, err := CheckValidationSchemaDBRules(ctx, "Workflow/drift-test", rules)
	if err != nil {
		t.Fatalf("CheckValidationSchemaDBRules returned error: %v", err)
	}
	if check.Status != "pass" {
		t.Fatalf("status = %q, want pass; evidence=%s", check.Status, check.Evidence)
	}
	if check.CheckID != validationSchemaDBDriftCheckID {
		t.Fatalf("check_id = %q, want %q", check.CheckID, validationSchemaDBDriftCheckID)
	}
}

func TestCheckValidationSchemaDBRulesFailsWhenColumnRenamed(t *testing.T) {
	ctx := context.Background()
	dbPath := setupPlanDriftDBTest(t, "Workflow/drift-test")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Simulates a step that renamed "status" to "delivery_status".
	if _, err := db.Exec(`CREATE TABLE emails(id INTEGER PRIMARY KEY, delivery_status TEXT); INSERT INTO emails VALUES (1, 'sent')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	rules := []DBValidationRule{{
		Name: "at least one sent email",
		SQL:  "SELECT status FROM emails WHERE status='sent'",
	}}

	check, err := CheckValidationSchemaDBRules(ctx, "Workflow/drift-test", rules)
	if err != nil {
		t.Fatalf("CheckValidationSchemaDBRules returned error: %v", err)
	}
	if check.Status != "fail" {
		t.Fatalf("status = %q, want fail (rule references a renamed column)", check.Status)
	}
}

func TestCheckValidationSchemaDBRulesFailsWhenRowCountAssertionBreaks(t *testing.T) {
	ctx := context.Background()
	dbPath := setupPlanDriftDBTest(t, "Workflow/drift-test")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Table exists and query runs fine, but now returns zero matching rows —
	// the step stopped writing status='sent' rows even though the schema
	// itself is intact.
	if _, err := db.Exec(`CREATE TABLE emails(id INTEGER PRIMARY KEY, status TEXT); INSERT INTO emails VALUES (1, 'draft')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	minRows := 1
	rules := []DBValidationRule{{
		Name:    "at least one sent email",
		SQL:     "SELECT status FROM emails WHERE status='sent'",
		MinRows: &minRows,
	}}

	check, err := CheckValidationSchemaDBRules(ctx, "Workflow/drift-test", rules)
	if err != nil {
		t.Fatalf("CheckValidationSchemaDBRules returned error: %v", err)
	}
	if check.Status != "fail" {
		t.Fatalf("status = %q, want fail (min_rows assertion no longer holds)", check.Status)
	}
}

func TestCheckValidationSchemaDBRulesPassesWhenNoRulesDeclared(t *testing.T) {
	ctx := context.Background()
	setupPlanDriftDBTest(t, "Workflow/drift-test")

	check, err := CheckValidationSchemaDBRules(ctx, "Workflow/drift-test", nil)
	if err != nil {
		t.Fatalf("CheckValidationSchemaDBRules returned error: %v", err)
	}
	if check.Status != "pass" {
		t.Fatalf("status = %q, want pass (no rules to check)", check.Status)
	}
	if check.Evidence == "" {
		t.Fatal("evidence must explain why there was nothing to check")
	}
}

func TestCheckValidationSchemaFileRulesPassesWhenFieldsResolve(t *testing.T) {
	ctx := context.Background()
	rules := []FileValidationRule{{
		FileName:  "extracted_data.json",
		MustExist: true,
		JSONChecks: []JSONValidationCheck{
			{Path: "$.status", MustExist: true, ValueType: "string"},
		},
	}}
	loadJSON := func(_ context.Context, fileName string) (interface{}, bool, error) {
		if fileName != "extracted_data.json" {
			return nil, false, nil
		}
		return map[string]interface{}{"status": "done"}, true, nil
	}

	check, err := CheckValidationSchemaFileRules(ctx, rules, loadJSON)
	if err != nil {
		t.Fatalf("CheckValidationSchemaFileRules returned error: %v", err)
	}
	if check.Status != "pass" {
		t.Fatalf("status = %q, want pass; evidence=%s", check.Status, check.Evidence)
	}
	if check.CheckID != validationSchemaFileDriftCheckID {
		t.Fatalf("check_id = %q, want %q", check.CheckID, validationSchemaFileDriftCheckID)
	}
}

func TestCheckValidationSchemaFileRulesFailsWhenFieldRemoved(t *testing.T) {
	ctx := context.Background()
	// The step's own output used to have "status"; a later edit renamed the
	// field to "outcome" without updating validation_schema.
	rules := []FileValidationRule{{
		FileName:  "extracted_data.json",
		MustExist: true,
		JSONChecks: []JSONValidationCheck{
			{Path: "$.status", MustExist: true, ValueType: "string"},
		},
	}}
	loadJSON := func(_ context.Context, _ string) (interface{}, bool, error) {
		return map[string]interface{}{"outcome": "done"}, true, nil
	}

	check, err := CheckValidationSchemaFileRules(ctx, rules, loadJSON)
	if err != nil {
		t.Fatalf("CheckValidationSchemaFileRules returned error: %v", err)
	}
	if check.Status != "fail" {
		t.Fatalf("status = %q, want fail (declared field no longer exists in real output)", check.Status)
	}
}

func TestCheckValidationSchemaFileRulesFailsWhenMustExistFileMissing(t *testing.T) {
	ctx := context.Background()
	rules := []FileValidationRule{{FileName: "results.json", MustExist: true}}
	loadJSON := func(_ context.Context, _ string) (interface{}, bool, error) {
		return nil, false, nil
	}

	check, err := CheckValidationSchemaFileRules(ctx, rules, loadJSON)
	if err != nil {
		t.Fatalf("CheckValidationSchemaFileRules returned error: %v", err)
	}
	if check.Status != "fail" {
		t.Fatalf("status = %q, want fail (must_exist file missing from recent output)", check.Status)
	}
}

func TestCheckValidationSchemaFileRulesPassesWhenNoRulesDeclared(t *testing.T) {
	ctx := context.Background()
	check, err := CheckValidationSchemaFileRules(ctx, nil, func(context.Context, string) (interface{}, bool, error) {
		t.Fatal("loadJSON should not be called when there are no file rules")
		return nil, false, nil
	})
	if err != nil {
		t.Fatalf("CheckValidationSchemaFileRules returned error: %v", err)
	}
	if check.Status != "pass" {
		t.Fatalf("status = %q, want pass (no rules to check)", check.Status)
	}
}

func TestExtractScriptedCodeQueriesHandlesRealWorldShapes(t *testing.T) {
	code := "" +
		"import sqlite3, os\n" +
		"conn = sqlite3.connect(os.environ['DB_PATH'])\n" +
		"cur = conn.cursor()\n" +
		"cur.execute(\"SELECT id FROM leads WHERE status = ?\", (status,))\n" +
		"cur.execute('''\n" +
		"    UPDATE leads SET touched = 1 WHERE id = ?\n" +
		"''', (lead_id,))\n"
	got := extractScriptedCodeQueries(code)
	want := []string{
		"SELECT id FROM leads WHERE status = ?",
		"UPDATE leads SET touched = 1 WHERE id = ?",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractScriptedCodeQueries = %v, want %v", got, want)
	}
}

func TestExtractScriptedCodeQueriesAdjacentLiterals(t *testing.T) {
	code := `conn.execute(
    "INSERT INTO leads " # comment between adjacent literals
    '(id, status) '
    """VALUES (?, ?)""", (id, status))
conn.execute("SELECT id " "FROM leads")
conn.execute("SELECT id FROM leads")
conn.execute("SELECT " + columns)
conn.execute("SELECT %s" % columns)
conn.execute("SELECT {}".format(columns))
conn.execute(f"SELECT {columns}")
conn.execute("unterminated)
`
	want := []string{"INSERT INTO leads (id, status) VALUES (?, ?)", "SELECT id FROM leads"}
	if got := extractScriptedCodeQueries(code); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestCheckScriptedCodeDBQueriesPassesWhenSchemaMatches(t *testing.T) {
	ctx := context.Background()
	dbPath := setupPlanDriftDBTest(t, "Workflow/drift-test")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE leads(id INTEGER PRIMARY KEY, status TEXT)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	readFile := func(_ context.Context, path string) (string, error) {
		if path == "Workflow/drift-test/learnings/step-log/main.py" {
			return `cur.execute("SELECT id "
                "FROM leads "
                "WHERE status = ?", (status,))
                cur.execute("INSERT INTO leads "
                "(id, status) VALUES (?, ?)", (id, status))`, nil
		}
		return "", os.ErrNotExist
	}

	check, err := CheckScriptedCodeDBQueries(ctx, "Workflow/drift-test", "step-log", readFile)
	if err != nil {
		t.Fatalf("CheckScriptedCodeDBQueries returned error: %v", err)
	}
	if check.Status != "pass" {
		t.Fatalf("status = %q, want pass; evidence=%s", check.Status, check.Evidence)
	}
	if check.CheckID != scriptedCodeDriftCheckID {
		t.Fatalf("check_id = %q, want %q", check.CheckID, scriptedCodeDriftCheckID)
	}
}

func TestCheckScriptedCodeDBQueriesFailsWhenTableRenamed(t *testing.T) {
	ctx := context.Background()
	dbPath := setupPlanDriftDBTest(t, "Workflow/drift-test")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Simulates a later step renaming "leads" to "prospects" without
	// updating this scripted step's own query.
	if _, err := db.Exec(`CREATE TABLE prospects(id INTEGER PRIMARY KEY, status TEXT)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	readFile := func(_ context.Context, path string) (string, error) {
		if path == "Workflow/drift-test/learnings/step-log/main.py" {
			return `cur.execute("SELECT id FROM leads WHERE status = ?", (status,))`, nil
		}
		return "", os.ErrNotExist
	}

	check, err := CheckScriptedCodeDBQueries(ctx, "Workflow/drift-test", "step-log", readFile)
	if err != nil {
		t.Fatalf("CheckScriptedCodeDBQueries returned error: %v", err)
	}
	if check.Status != "fail" {
		t.Fatalf("status = %q, want fail (query references a renamed table)", check.Status)
	}
}

func TestCheckScriptedCodeDBQueriesPassesWhenStepNotScripted(t *testing.T) {
	ctx := context.Background()
	setupPlanDriftDBTest(t, "Workflow/drift-test")

	readFile := func(_ context.Context, _ string) (string, error) {
		return "", os.ErrNotExist
	}

	check, err := CheckScriptedCodeDBQueries(ctx, "Workflow/drift-test", "step-agentic", readFile)
	if err != nil {
		t.Fatalf("CheckScriptedCodeDBQueries returned error: %v", err)
	}
	if check.Status != "pass" {
		t.Fatalf("status = %q, want pass (no main.py — not a scripted step)", check.Status)
	}
	if check.Evidence == "" {
		t.Fatal("evidence must explain why there was nothing to check")
	}
}

func TestCheckScriptedCodeDBQueriesDoesNotMutateDB(t *testing.T) {
	ctx := context.Background()
	dbPath := setupPlanDriftDBTest(t, "Workflow/drift-test")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE leads(id INTEGER PRIMARY KEY, status TEXT); INSERT INTO leads VALUES (1, 'new')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	readFile := func(_ context.Context, path string) (string, error) {
		if path == "Workflow/drift-test/learnings/step-log/main.py" {
			return `cur.execute("UPDATE leads SET status = 'hacked' WHERE id = ?", (lead_id,))`, nil
		}
		return "", os.ErrNotExist
	}

	if _, err := CheckScriptedCodeDBQueries(ctx, "Workflow/drift-test", "step-log", readFile); err != nil {
		t.Fatalf("CheckScriptedCodeDBQueries returned error: %v", err)
	}

	check, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var status string
	if err := check.QueryRow(`SELECT status FROM leads WHERE id=1`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "new" {
		t.Fatalf("status = %q, want unchanged %q — EXPLAIN-only guard did not hold", status, "new")
	}
}

func TestExtractDBReadmeDDLStatementsHandlesBothRealConventions(t *testing.T) {
	readme := "" +
		"## table: campaign_runs\n" +
		"- **ddl**: `CREATE TABLE campaign_runs (batch_id TEXT PRIMARY KEY, status TEXT CHECK (status IN ('a','b')))`\n" +
		"- **writers**: step-x\n\n" +
		"## `prospects`\n" +
		"- **create_table**:\n" +
		"```sql\n" +
		"CREATE TABLE prospects (\n" +
		"  prospect_id TEXT PRIMARY KEY,\n" +
		"  batch_id TEXT NOT NULL\n" +
		")\n" +
		"```\n"
	got := extractDBReadmeDDLStatements(readme)
	if len(got) != 2 {
		t.Fatalf("extractDBReadmeDDLStatements found %d statement(s), want 2: %v", len(got), got)
	}
	if !strings.Contains(got[0], "campaign_runs") || !strings.Contains(got[1], "prospects") {
		t.Fatalf("extracted statements = %v, want one per table in source order", got)
	}
}

func TestDDLDeclaredColumnsParsesRealDDL(t *testing.T) {
	ctx := context.Background()
	name, cols, err := ddlDeclaredColumns(ctx, `CREATE TABLE campaign_runs (batch_id TEXT PRIMARY KEY, status TEXT CHECK (status IN ('a','b')), notes TEXT)`)
	if err != nil {
		t.Fatalf("ddlDeclaredColumns returned error: %v", err)
	}
	if name != "campaign_runs" {
		t.Fatalf("table name = %q, want campaign_runs", name)
	}
	want := []string{"batch_id", "status", "notes"}
	if !reflect.DeepEqual(cols, want) {
		t.Fatalf("columns = %v, want %v", cols, want)
	}
}

func TestCheckDBReadmeContractPassesWhenSchemaMatches(t *testing.T) {
	ctx := context.Background()
	dbPath := setupPlanDriftDBTest(t, "Workflow/drift-test")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE leads(id INTEGER PRIMARY KEY, status TEXT)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	readFile := func(_ context.Context, path string) (string, error) {
		if path == "Workflow/drift-test/db/README.md" {
			return "## table: leads\n- **ddl**: `CREATE TABLE leads(id INTEGER PRIMARY KEY, status TEXT)`\n", nil
		}
		return "", os.ErrNotExist
	}

	check, err := CheckDBReadmeContract(ctx, "Workflow/drift-test", readFile)
	if err != nil {
		t.Fatalf("CheckDBReadmeContract returned error: %v", err)
	}
	if check.Status != "pass" {
		t.Fatalf("status = %q, want pass; evidence=%s", check.Status, check.Evidence)
	}
	if check.CheckID != dbReadmeDriftCheckID {
		t.Fatalf("check_id = %q, want %q", check.CheckID, dbReadmeDriftCheckID)
	}
}

func TestCheckDBReadmeContractFailsWhenColumnDropped(t *testing.T) {
	ctx := context.Background()
	dbPath := setupPlanDriftDBTest(t, "Workflow/drift-test")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// A step dropped the "status" column without updating db/README.md.
	if _, err := db.Exec(`CREATE TABLE leads(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	readFile := func(_ context.Context, path string) (string, error) {
		if path == "Workflow/drift-test/db/README.md" {
			return "## table: leads\n- **ddl**: `CREATE TABLE leads(id INTEGER PRIMARY KEY, status TEXT)`\n", nil
		}
		return "", os.ErrNotExist
	}

	check, err := CheckDBReadmeContract(ctx, "Workflow/drift-test", readFile)
	if err != nil {
		t.Fatalf("CheckDBReadmeContract returned error: %v", err)
	}
	if check.Status != "fail" {
		t.Fatalf("status = %q, want fail (documented column no longer exists live)", check.Status)
	}
}

func TestCheckDBReadmeContractFailsWhenTableDropped(t *testing.T) {
	ctx := context.Background()
	setupPlanDriftDBTest(t, "Workflow/drift-test")
	// db.sqlite exists but the documented table was never created (or was dropped).
	dbPath := planDriftWorkflowDBPath("Workflow/drift-test")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE unrelated(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	readFile := func(_ context.Context, path string) (string, error) {
		if path == "Workflow/drift-test/db/README.md" {
			return "## table: leads\n- **ddl**: `CREATE TABLE leads(id INTEGER PRIMARY KEY, status TEXT)`\n", nil
		}
		return "", os.ErrNotExist
	}

	check, err := CheckDBReadmeContract(ctx, "Workflow/drift-test", readFile)
	if err != nil {
		t.Fatalf("CheckDBReadmeContract returned error: %v", err)
	}
	if check.Status != "fail" {
		t.Fatalf("status = %q, want fail (documented table missing entirely)", check.Status)
	}
}

func TestCheckDBReadmeContractPassesWhenProseOnly(t *testing.T) {
	ctx := context.Background()
	setupPlanDriftDBTest(t, "Workflow/drift-test")

	readFile := func(_ context.Context, path string) (string, error) {
		if path == "Workflow/drift-test/db/README.md" {
			return "### `leads`\nPrimary key: `id`. Upsert rule: insert one row per lead.\n", nil
		}
		return "", os.ErrNotExist
	}

	check, err := CheckDBReadmeContract(ctx, "Workflow/drift-test", readFile)
	if err != nil {
		t.Fatalf("CheckDBReadmeContract returned error: %v", err)
	}
	if check.Status != "pass" {
		t.Fatalf("status = %q, want pass (prose-only README, no DDL to verify — not a false failure)", check.Status)
	}
	if check.Evidence == "" {
		t.Fatal("evidence must explain why nothing was verified")
	}
}

func TestCheckDBReadmeContractPassesWhenNoReadmeExists(t *testing.T) {
	ctx := context.Background()
	setupPlanDriftDBTest(t, "Workflow/drift-test")

	readFile := func(_ context.Context, _ string) (string, error) {
		return "", os.ErrNotExist
	}

	check, err := CheckDBReadmeContract(ctx, "Workflow/drift-test", readFile)
	if err != nil {
		t.Fatalf("CheckDBReadmeContract returned error: %v", err)
	}
	if check.Status != "pass" {
		t.Fatalf("status = %q, want pass (no README to check)", check.Status)
	}
}

func TestExtractSQLTableReferencesFindsAllClauseKinds(t *testing.T) {
	got := extractSQLTableReferences(`SELECT a.x FROM leads a JOIN campaigns c ON a.c=c.id; UPDATE leads SET x=1; INSERT INTO audit_log VALUES (1)`)
	want := []string{"leads", "campaigns", "leads", "audit_log"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractSQLTableReferences = %v, want %v", got, want)
	}
}

func TestCheckOrphanedTablesPassesWhenAllTablesReferenced(t *testing.T) {
	ctx := context.Background()
	dbPath := setupPlanDriftDBTest(t, "Workflow/drift-test")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE leads(id INTEGER PRIMARY KEY); CREATE TABLE campaigns(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	check, err := CheckOrphanedTables(ctx, "Workflow/drift-test",
		[]string{"SELECT * FROM leads"},     // db rule SQL
		[]string{"SELECT * FROM campaigns"}, // scripted code queries
		nil,                                 // report queries
		nil,                                 // readme declared tables
	)
	if err != nil {
		t.Fatalf("CheckOrphanedTables returned error: %v", err)
	}
	if check.Status != "pass" {
		t.Fatalf("status = %q, want pass; evidence=%s", check.Status, check.Evidence)
	}
	if check.CheckID != orphanedTablesDriftCheckID {
		t.Fatalf("check_id = %q, want %q", check.CheckID, orphanedTablesDriftCheckID)
	}
}

func TestCheckOrphanedTablesFlagsUnreferencedTable(t *testing.T) {
	ctx := context.Background()
	dbPath := setupPlanDriftDBTest(t, "Workflow/drift-test")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// "legacy_imports" used to be written by a step that was since removed
	// or rewritten; nothing references it anymore.
	if _, err := db.Exec(`CREATE TABLE leads(id INTEGER PRIMARY KEY); CREATE TABLE legacy_imports(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	check, err := CheckOrphanedTables(ctx, "Workflow/drift-test",
		[]string{"SELECT * FROM leads"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("CheckOrphanedTables returned error: %v", err)
	}
	if check.Status != "fail" {
		t.Fatalf("status = %q, want fail (legacy_imports has zero references)", check.Status)
	}
	if !strings.Contains(check.Evidence, "legacy_imports") {
		t.Fatalf("evidence must name the orphaned table: %s", check.Evidence)
	}
}

func TestCheckOrphanedTablesNeverFlagsReservedTables(t *testing.T) {
	ctx := context.Background()
	dbPath := setupPlanDriftDBTest(t, "Workflow/drift-test")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE run_concerns(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	check, err := CheckOrphanedTables(ctx, "Workflow/drift-test", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("CheckOrphanedTables returned error: %v", err)
	}
	if check.Status != "pass" {
		t.Fatalf("status = %q, want pass (run_concerns is platform-reserved, never flagged): %s", check.Status, check.Evidence)
	}
}

func TestCheckOrphanedTablesRecognizesReportAndReadmeReferences(t *testing.T) {
	ctx := context.Background()
	dbPath := setupPlanDriftDBTest(t, "Workflow/drift-test")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE from_report(id INTEGER PRIMARY KEY); CREATE TABLE from_readme(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	check, err := CheckOrphanedTables(ctx, "Workflow/drift-test",
		nil, nil,
		[]string{"SELECT * FROM from_report"},
		[]string{"from_readme"},
	)
	if err != nil {
		t.Fatalf("CheckOrphanedTables returned error: %v", err)
	}
	if check.Status != "pass" {
		t.Fatalf("status = %q, want pass; evidence=%s", check.Status, check.Evidence)
	}
}

// The report checker used to execute parameterized SQL with zero arguments, and
// scripted checks counted literal question marks as parameters. Prepare against
// real SQLite instead, preserving complete adjacent literals and real failures.
func TestPlanDriftSchemaChecksPrepareParameterizedSQL(t *testing.T) {
	workspace := "Workflow/parameterized-report"
	dbPath := setupPlanDriftDBTest(t, workspace)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE messages(id INTEGER PRIMARY KEY, body TEXT); INSERT INTO messages VALUES (1,'original')"); err != nil {
		t.Fatal(err)
	}
	report := `window.report.query("SELECT body FROM messages WHERE id=? AND (?='' OR body=?)",[id,body||'',body||'']);
window.report.query("SELECT '?' AS question, body FROM messages WHERE id=?3 -- ? is a comment",[null,null,id]);
window.report.query("SELECT body FROM messages WHERE id=:id",{id});`
	code := `conn.execute(
  "UPDATE messages " # adjacent literals must stay complete
  "SET body=:body "
  """WHERE id=?2 AND '?'='?'""", params)
conn.execute("SELECT " + dynamic_columns)`
	readFile := func(_ context.Context, path string) (string, error) {
		switch path {
		case workspace + "/db/reports/index.html":
			return report, nil
		case workspace + "/code/work/main.py":
			return code, nil
		case workspace + "/workflow.json":
			return `{"code_layout_version":1}`, nil
		}
		return "", os.ErrNotExist
	}
	for _, broken := range []bool{false, true} {
		if broken {
			report = strings.ReplaceAll(report, "SELECT body", "SELECT absent_column")
			code = strings.Replace(code, "SET body", "SET absent_column", 1)
		}
		reportCheck, err := CheckReportQueryCompatibility(t.Context(), workspace, readFile)
		if err != nil {
			t.Fatal(err)
		}
		scriptCheck, err := CheckScriptedCodeDBQueries(t.Context(), workspace, "work", readFile)
		if err != nil {
			t.Fatal(err)
		}
		for _, check := range []StepDriftCheck{reportCheck, scriptCheck} {
			want := "pass"
			if broken {
				want = "fail"
			}
			if check.Status != want {
				t.Fatalf("%s: %s; %s", check.CheckID, check.Status, check.Evidence)
			}
			if broken && !strings.Contains(check.Evidence, "absent_column") {
				t.Fatalf("lost real schema error: %s", check.Evidence)
			}
		}
	}
	var body string
	if err := db.QueryRow("SELECT body FROM messages WHERE id=1").Scan(&body); err != nil || body != "original" {
		t.Fatalf("schema check executed saved SQL: %q / %v", body, err)
	}
}
