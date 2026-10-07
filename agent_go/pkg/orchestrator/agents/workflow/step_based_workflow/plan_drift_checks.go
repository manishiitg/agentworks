package step_based_workflow

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"

	_ "modernc.org/sqlite"
)

// PLAT-258 phase 2. Deterministic plan-drift checks: pure Go, no LLM turn, no
// agent tool call to skip — the same design constraint run_concerns.go already
// documents ("there is no call for an agent to skip"). Each check reads the
// workflow's own current db/reports/index.html and db/db.sqlite directly and
// returns one evidence-required StepDriftCheck. Judgment checks (description
// accuracy, learnings/KB content staleness, DB normalization) are NOT here —
// those need an actual Pulse reviewer turn and are phase 4.

const reportDriftCheckID = "report_query_compatibility"

func planDriftWorkflowDBPath(workspacePath string) string {
	return filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(strings.Trim(strings.TrimSpace(workspacePath), "/")), "db", "db.sqlite")
}

// openPlanDriftQueryOnlyDB opens the workflow's db.sqlite read-only (WAL-capable,
// query_only pragma) — a drift check must never be able to mutate real workflow
// data, no matter what SQL a report or a step's own code embeds.
//
// Builds on the shared sqliteopen.DSN helper (journal_mode=WAL,
// busy_timeout embedded in the DSN, not a one-time runtime PRAGMA) rather
// than a hand-rolled DSN string. This was the one remaining workflow
// db.sqlite opener not yet migrated when report_human_inputs.go's
// SQLITE_BUSY fix and the shared helper itself landed — found live on the
// Dominion deployment 2026-08-31 as the source of a second, separate
// get_pulse_state(view=module) hang (in CheckReportQueryCompatibility) that
// survived that first fix, load-dependent enough that it couldn't be
// force-reproduced in isolation.
func openPlanDriftQueryOnlyDB(dbPath string) (*sql.DB, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, err
	}
	dsn := sqliteopen.DSN(dbPath) + "&mode=rw&_pragma=query_only(true)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// reportQueryPatterns match window.report.query("..."), ('...'), and (`...`)
// separately — one pattern per quote character rather than a single pattern
// with a backreference, since Go's RE2 engine (regexp) does not support
// backreferences at all. Each captures the SQL text between matching
// unescaped quotes of its own kind. Deliberately regexes, not a JS parser —
// report HTML is simple, agent-authored, and this only needs to find literal
// query() call arguments, not evaluate arbitrary expressions.
var reportQueryPatterns = []*regexp.Regexp{
	regexp.MustCompile(`window\.report\.query\(\s*'((?:\\.|[^'\\])*)'`),
	regexp.MustCompile(`window\.report\.query\(\s*"((?:\\.|[^"\\])*)"`),
	regexp.MustCompile("window\\.report\\.query\\(\\s*`((?:\\\\.|[^`\\\\])*)`"),
}

// extractReportQueries pulls every window.report.query("...") SQL string out of
// a report's raw HTML/JS text, deduplicated and in first-seen (source-order)
// position. Queries built by string concatenation or built at runtime (not a
// literal argument) are not extractable this way — a known limitation, not a
// false negative on what IS extractable.
func extractReportQueries(html string) []string {
	type found struct {
		pos int
		sql string
	}
	var all []found
	for _, pattern := range reportQueryPatterns {
		for _, m := range pattern.FindAllStringSubmatchIndex(html, -1) {
			if len(m) < 4 {
				continue
			}
			sqlText := strings.TrimSpace(unescapeJSStringLiteral(html[m[2]:m[3]]))
			if sqlText == "" {
				continue
			}
			all = append(all, found{pos: m[0], sql: sqlText})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].pos < all[j].pos })
	seen := make(map[string]bool, len(all))
	queries := make([]string, 0, len(all))
	for _, f := range all {
		if seen[f.sql] {
			continue
		}
		seen[f.sql] = true
		queries = append(queries, f.sql)
	}
	return queries
}

// unescapeJSStringLiteral handles the handful of escapes that actually show up
// in a hand-authored SQL string embedded in JS source — not a full JS string
// grammar, just enough that a query containing an escaped quote or a newline
// still dry-runs as the same SQL the browser would actually send.
func unescapeJSStringLiteral(s string) string {
	replacer := strings.NewReplacer(
		`\'`, `'`, `\"`, `"`, "\\`", "`",
		`\n`, "\n", `\t`, "\t", `\\`, `\`,
	)
	return replacer.Replace(s)
}

// CheckReportQueryCompatibility dry-runs every window.report.query(...) call
// found in db/reports/index.html against the workflow's live db.sqlite. A step
// changing what it writes (renaming/dropping a column or table) breaks the
// report silently in the UI; this catches it mechanically, no LLM needed.
func CheckReportQueryCompatibility(ctx context.Context, workspacePath string, readFile func(context.Context, string) (string, error)) (StepDriftCheck, error) {
	check := StepDriftCheck{CheckID: reportDriftCheckID}

	reportPath := normalizePathForWorkspaceAPI(filepath.Join("db", "reports", "index.html"), workspacePath)
	html, err := readFile(ctx, reportPath)
	if err != nil || strings.TrimSpace(html) == "" {
		check.Status = "pass"
		check.Evidence = "no db/reports/index.html found for this workflow; nothing to check"
		return check, nil
	}

	queries := extractReportQueries(html)
	if len(queries) == 0 {
		check.Status = "pass"
		check.Evidence = "db/reports/index.html has no window.report.query(...) calls; nothing to check"
		return check, nil
	}

	dbPath := planDriftWorkflowDBPath(workspacePath)
	db, err := openPlanDriftQueryOnlyDB(dbPath)
	if err != nil {
		check.Status = "fail"
		check.Evidence = fmt.Sprintf("report declares %d query(...) call(s) but db/db.sqlite could not be opened: %v", len(queries), err)
		return check, nil
	}
	defer db.Close()

	var failures []string
	for _, q := range queries {
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			preview := q
			if len(preview) > 120 {
				preview = preview[:120] + "..."
			}
			failures = append(failures, fmt.Sprintf("%q -> %v", preview, err))
			continue
		}
		// QueryContext returns an open result set even though this check only
		// needs SQLite to prepare/execute the statement. Leaving it open keeps
		// the read connection alive and can make the deferred db.Close wait
		// until the caller's context expires, which previously made the managed
		// plan-drift review time out on otherwise-valid reports.
		if err := rows.Close(); err != nil {
			preview := q
			if len(preview) > 120 {
				preview = preview[:120] + "..."
			}
			failures = append(failures, fmt.Sprintf("%q -> close result set: %v", preview, err))
		}
	}

	if len(failures) == 0 {
		check.Status = "pass"
		check.Evidence = fmt.Sprintf("all %d report query(...) call(s) ran cleanly against the current db.sqlite schema", len(queries))
		return check, nil
	}
	check.Status = "fail"
	check.Evidence = fmt.Sprintf("%d of %d report query(...) call(s) failed against the current schema: %s", len(failures), len(queries), strings.Join(failures, "; "))
	return check, nil
}

const validationSchemaDBDriftCheckID = "validation_schema_db_rules"

// scanPlanDriftRows converts *sql.Rows into the same []map[string]interface{}
// shape evaluateDBRule (pre_validation_db.go) expects, so drift checks reuse
// the real, already-tested validation semantics instead of a second
// implementation of "what does a row check mean."
func scanPlanDriftRows(rows *sql.Rows) ([]map[string]interface{}, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]interface{}
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]interface{}, len(cols))
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				row[c] = string(b)
			} else {
				row[c] = vals[i]
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// CheckValidationSchemaDBRules dry-runs every ValidationSchema.DB[] rule's SQL
// against the workflow's live db.sqlite (query_only) and applies its MinRows/
// MaxRows/Checks assertions via the real evaluateDBRule — the exact function
// normal pre-validation uses — so a step's own declared DB validation is
// re-checked against CURRENT data, not just at the moment it last ran. A rule
// that used to pass and no longer does means the step's actual output shape
// (or the DB itself) drifted out from under its own validation contract.
func CheckValidationSchemaDBRules(ctx context.Context, workspacePath string, dbRules []DBValidationRule) (StepDriftCheck, error) {
	check := StepDriftCheck{CheckID: validationSchemaDBDriftCheckID}
	if len(dbRules) == 0 {
		check.Status = "pass"
		check.Evidence = "this step's validation_schema declares no db[] rules; nothing to check"
		return check, nil
	}

	dbPath := planDriftWorkflowDBPath(workspacePath)
	db, err := openPlanDriftQueryOnlyDB(dbPath)
	if err != nil {
		check.Status = "fail"
		check.Evidence = fmt.Sprintf("validation_schema declares %d db[] rule(s) but db/db.sqlite could not be opened: %v", len(dbRules), err)
		return check, nil
	}
	defer db.Close()

	var failures []string
	for _, rule := range dbRules {
		label := strings.TrimSpace(rule.Name)
		if label == "" {
			label = truncateForLabel(rule.SQL, 60)
		}
		if strings.TrimSpace(rule.SQL) == "" {
			failures = append(failures, fmt.Sprintf("rule %q has no sql", label))
			continue
		}
		sqlRows, err := db.QueryContext(ctx, rule.SQL)
		if err != nil {
			failures = append(failures, fmt.Sprintf("rule %q: query failed: %v", label, err))
			continue
		}
		rows, err := scanPlanDriftRows(sqlRows)
		sqlRows.Close()
		if err != nil {
			failures = append(failures, fmt.Sprintf("rule %q: failed to read rows: %v", label, err))
			continue
		}
		for _, result := range evaluateDBRule(ctx, rule, rows) {
			if !result.Passed && !result.SchemaError {
				failures = append(failures, fmt.Sprintf("rule %q, check %s: expected %v, got %v (%s)", label, result.Path, result.Expected, result.Actual, result.ErrorMsg))
			}
		}
	}

	if len(failures) == 0 {
		check.Status = "pass"
		check.Evidence = fmt.Sprintf("all %d validation_schema db[] rule(s) still pass against the current db.sqlite", len(dbRules))
		return check, nil
	}
	check.Status = "fail"
	check.Evidence = fmt.Sprintf("%d db[] rule assertion(s) failed against current data: %s", len(failures), strings.Join(failures, "; "))
	return check, nil
}

const validationSchemaFileDriftCheckID = "validation_schema_file_rules"

// CheckValidationSchemaFileRules re-checks a step's ValidationSchema.Files[]
// json_checks against the step's own MOST RECENT real output, using the real
// validateJSONCheck evaluator (pre_validation.go) — the same function normal
// pre-validation calls — so semantics never drift between what a live run
// checks and what this drift check checks. loadJSON resolves a declared
// FileName to its current parsed content; callers (the phase-3 orchestration
// layer) own finding the right run folder, since that resolution has nothing
// to do with what a drift check itself asserts. A field that used to resolve
// and no longer does, or now fails value_type/pattern/etc., means the step's
// actual output shape changed without its validation_schema catching up.
func CheckValidationSchemaFileRules(ctx context.Context, fileRules []FileValidationRule, loadJSON func(ctx context.Context, fileName string) (data interface{}, exists bool, err error)) (StepDriftCheck, error) {
	check := StepDriftCheck{CheckID: validationSchemaFileDriftCheckID}
	if len(fileRules) == 0 {
		check.Status = "pass"
		check.Evidence = "this step's validation_schema declares no files[] rules; nothing to check"
		return check, nil
	}

	var failures []string
	checked := 0
	for _, rule := range fileRules {
		data, exists, err := loadJSON(ctx, rule.FileName)
		if err != nil {
			failures = append(failures, fmt.Sprintf("file %q: failed to load current output: %v", rule.FileName, err))
			continue
		}
		if !exists {
			if rule.MustExist {
				failures = append(failures, fmt.Sprintf("file %q must exist but no recent run produced it", rule.FileName))
			}
			continue
		}
		for _, jsonCheck := range rule.JSONChecks {
			checked++
			result := validateJSONCheck(ctx, jsonCheck, data)
			if !result.Passed && !result.SchemaError {
				failures = append(failures, fmt.Sprintf("file %q, path %s: expected %v, got %v (%s)", rule.FileName, result.Path, result.Expected, result.Actual, result.ErrorMsg))
			}
		}
	}

	if len(failures) == 0 {
		check.Status = "pass"
		check.Evidence = fmt.Sprintf("all %d validation_schema files[] json_checks assertion(s) still pass against the most recent output", checked)
		return check, nil
	}
	check.Status = "fail"
	check.Evidence = fmt.Sprintf("%d files[] assertion(s) failed against the most recent output: %s", len(failures), strings.Join(failures, "; "))
	return check, nil
}

const scriptedCodeDriftCheckID = "scripted_code_db_queries"

// Locate Python sqlite3 .execute calls. The scanner below reads the whole static
// string argument; dynamic expressions and subprocess/sqlite3 CLI calls are not
// covered. The checker reports how many queries it could inspect.
var scriptedCodeExecuteStart = regexp.MustCompile(`\.execute\s*\(\s*`)

// extractScriptedCodeQueries pulls every .execute("SQL"/'SQL'/"""SQL""") call
// out of a scripted step's main.py, deduplicated in first-seen source
// position — same shape as extractReportQueries, deliberately, so both
// extractors are easy to reason about together.
func extractScriptedCodeQueries(code string) []string {
	seen := make(map[string]bool)
	var queries []string
	for _, match := range scriptedCodeExecuteStart.FindAllStringIndex(code, -1) {
		sqlText, ok := scriptedStaticSQLArgument(code, match[1])
		sqlText = strings.TrimSpace(sqlText)
		if !ok || sqlText == "" || seen[sqlText] {
			continue
		}
		seen[sqlText] = true
		queries = append(queries, sqlText)
	}
	return queries
}

// Python implicitly concatenates adjacent literals, including across lines and
// comments inside parentheses. Never EXPLAIN a prefix of a dynamic expression.
func scriptedStaticSQLArgument(code string, pos int) (string, bool) {
	var sql strings.Builder
	for pos < len(code) {
		switch code[pos] {
		case ' ', '\t', '\r', '\n':
			pos++
			continue
		case '#':
			for pos < len(code) && code[pos] != '\n' {
				pos++
			}
			continue
		case ',', ')':
			return sql.String(), true
		}
		if code[pos] != '\'' && code[pos] != '"' {
			return "", false
		}
		delimiter := code[pos : pos+1]
		if strings.HasPrefix(code[pos:], strings.Repeat(delimiter, 3)) {
			delimiter = strings.Repeat(delimiter, 3)
		}
		pos += len(delimiter)
		start := pos
		for pos < len(code) && !strings.HasPrefix(code[pos:], delimiter) {
			if code[pos] == '\\' {
				pos++ // escaped quotes cannot close the literal
			}
			pos++
		}
		if pos >= len(code) {
			return "", false
		}
		sql.WriteString(unescapeJSStringLiteral(code[start:pos]))
		pos += len(delimiter)
	}
	return "", false
}

// countSQLPlaceholders counts standalone `?` bind placeholders in a SQL
// string (naive — does not distinguish a `?` inside a quoted literal, which
// would be unusual for a bind-parameterized query in the first place).
func countSQLPlaceholders(sqlText string) int {
	return strings.Count(sqlText, "?")
}

// CheckScriptedCodeDBQueries dry-runs every extracted query from a scripted
// step's main.py against the live db.sqlite via EXPLAIN (not the query
// itself) with every ?-placeholder bound to NULL — EXPLAIN still requires
// SQLite to resolve every referenced table/column to build the bytecode
// program, so a renamed/dropped table or column is still caught, without
// executing the statement or needing real parameter values (verified: NULL-
// bound EXPLAIN surfaces "no such table"/"no such column" identically to a
// real run, confirmed against a throwaway local database before shipping
// this check). This still never risks a write: EXPLAIN never executes, and
// the connection itself is opened query_only as an additional guard.
func CheckScriptedCodeDBQueries(ctx context.Context, workspacePath, stepID string, readFile func(context.Context, string) (string, error)) (StepDriftCheck, error) {
	check := StepDriftCheck{CheckID: scriptedCodeDriftCheckID}

	codePath := normalizePathForWorkspaceAPI(filepath.Join(savedCodeDirectory(ctx, workspacePath, stepID, readFile), "main.py"), workspacePath)
	code, err := readFile(ctx, codePath)
	if err != nil || strings.TrimSpace(code) == "" {
		check.Status = "pass"
		check.Evidence = "no " + codePath + " found; no saved code to check"
		return check, nil
	}

	queries := extractScriptedCodeQueries(code)
	if len(queries) == 0 {
		check.Status = "pass"
		check.Evidence = "main.py has no .execute(\"...\") calls this extractor recognizes (or it queries db.sqlite some other way, e.g. a sqlite3-CLI subprocess call — not covered by this check); nothing checked"
		return check, nil
	}

	dbPath := planDriftWorkflowDBPath(workspacePath)
	db, err := openPlanDriftQueryOnlyDB(dbPath)
	if err != nil {
		check.Status = "fail"
		check.Evidence = fmt.Sprintf("main.py has %d recognizable query(ies) but db/db.sqlite could not be opened: %v", len(queries), err)
		return check, nil
	}
	defer db.Close()

	var failures []string
	for _, q := range queries {
		placeholderArgs := make([]interface{}, countSQLPlaceholders(q))
		if _, err := db.ExecContext(ctx, "EXPLAIN "+q, placeholderArgs...); err != nil {
			preview := q
			if len(preview) > 120 {
				preview = preview[:120] + "..."
			}
			failures = append(failures, fmt.Sprintf("%q -> %v", preview, err))
		}
	}

	if len(failures) == 0 {
		check.Status = "pass"
		check.Evidence = fmt.Sprintf("all %d recognized main.py query(ies) reference tables/columns that still exist in the current schema", len(queries))
		return check, nil
	}
	check.Status = "fail"
	check.Evidence = fmt.Sprintf("%d of %d main.py query(ies) reference a table/column that no longer exists: %s", len(failures), len(queries), strings.Join(failures, "; "))
	return check, nil
}

const dbReadmeDriftCheckID = "db_readme_contract"

// dbReadmeDDLPatterns extract a documented CREATE TABLE statement out of
// db/README.md. Confirmed against 13 real workflows' READMEs: 12 of 13
// contain a literal CREATE TABLE DDL string somewhere, in one of two real
// conventions — inline backtick-delimited ("- **ddl**: `CREATE TABLE ...`")
// or a fenced ```sql block. The backtick/fence delimiters do the paren-
// balancing work for us (a DDL's own CHECK(...)/nested parens would defeat a
// naive regex trying to match "(...)" directly), so this stays a plain
// delimiter scan rather than a hand-written SQL grammar. The remaining
// prose-only README style (no DDL at all) is a real, acknowledged gap: this
// check reports "no DDL found to verify" rather than a false pass or fail.
var dbReadmeDDLPatterns = []*regexp.Regexp{
	regexp.MustCompile("(?is)`(CREATE TABLE[^`]*)`"),
	regexp.MustCompile("(?is)```sql\\s*(CREATE TABLE.*?)```"),
}

func extractDBReadmeDDLStatements(readme string) []string {
	seen := make(map[string]bool)
	var statements []string
	for _, pattern := range dbReadmeDDLPatterns {
		for _, m := range pattern.FindAllStringSubmatch(readme, -1) {
			if len(m) < 2 {
				continue
			}
			ddl := strings.TrimSpace(m[1])
			ddl = strings.TrimSuffix(ddl, ";")
			if ddl == "" || seen[ddl] {
				continue
			}
			seen[ddl] = true
			statements = append(statements, ddl)
		}
	}
	return statements
}

// ddlDeclaredColumns runs one CREATE TABLE statement against a throwaway
// in-memory SQLite database and reads back its column list via PRAGMA
// table_info — using SQLite's own real parser to learn what the DDL declares
// rather than a hand-written column-list parser (a DDL's own CHECK/DEFAULT/
// FOREIGN KEY clauses make regex column extraction unreliable). Returns the
// declared table name and its column names.
func ddlDeclaredColumns(ctx context.Context, ddl string) (tableName string, columns []string, err error) {
	scratch, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return "", nil, err
	}
	defer scratch.Close()
	// A :memory: database is private to the connection that opened it, and
	// database/sql pools connections -- without pinning to exactly one, the
	// CREATE TABLE below and the PRAGMA read after it can silently land on
	// two different, unrelated in-memory databases, leaving columns empty.
	scratch.SetMaxOpenConns(1)
	if _, err := scratch.ExecContext(ctx, ddl); err != nil {
		return "", nil, fmt.Errorf("documented DDL does not parse as valid SQL: %w", err)
	}
	// rows must be fully closed before the next query: the connection pool is
	// pinned to exactly one connection above, so an open statement here would
	// otherwise block the PRAGMA query below forever waiting for the same
	// connection.
	rows, err := scratch.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		return "", nil, err
	}
	if rows.Next() {
		if err := rows.Scan(&tableName); err != nil {
			rows.Close()
			return "", nil, err
		}
	}
	if err := rows.Close(); err != nil {
		return "", nil, err
	}
	if tableName == "" {
		return "", nil, fmt.Errorf("DDL did not create a table")
	}
	colRows, err := scratch.QueryContext(ctx, "PRAGMA table_info("+quotePlanDriftIdent(tableName)+")")
	if err != nil {
		return "", nil, err
	}
	defer colRows.Close()
	for colRows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt interface{}
		if err := colRows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return "", nil, err
		}
		columns = append(columns, name)
	}
	return tableName, columns, colRows.Err()
}

func quotePlanDriftIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func liveTableColumns(ctx context.Context, db *sql.DB, tableName string) ([]string, bool, error) {
	var exists int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tableName).Scan(&exists); err != nil {
		return nil, false, err
	}
	if exists == 0 {
		return nil, false, nil
	}
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+quotePlanDriftIdent(tableName)+")")
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return nil, false, err
		}
		cols = append(cols, name)
	}
	return cols, true, rows.Err()
}

func stringSetDiff(a, b []string) []string {
	inB := make(map[string]bool, len(b))
	for _, v := range b {
		inB[v] = true
	}
	var diff []string
	for _, v := range a {
		if !inB[v] {
			diff = append(diff, v)
		}
	}
	sort.Strings(diff)
	return diff
}

// CheckDBReadmeContract extracts every documented CREATE TABLE statement from
// db/README.md and compares its declared column list against the live
// db.sqlite schema (both sides read via SQLite's own parser/PRAGMA, not a
// hand-written comparator). A step that renames/drops/adds a column without
// updating the README is drift the same way a report or a validation_schema
// rule breaking is — the README just documents the contract instead of
// consuming it.
func CheckDBReadmeContract(ctx context.Context, workspacePath string, readFile func(context.Context, string) (string, error)) (StepDriftCheck, error) {
	check := StepDriftCheck{CheckID: dbReadmeDriftCheckID}

	readmePath := normalizePathForWorkspaceAPI(filepath.Join("db", "README.md"), workspacePath)
	readme, err := readFile(ctx, readmePath)
	if err != nil || strings.TrimSpace(readme) == "" {
		check.Status = "pass"
		check.Evidence = "no db/README.md found for this workflow; nothing to check"
		return check, nil
	}

	statements := extractDBReadmeDDLStatements(readme)
	if len(statements) == 0 {
		check.Status = "pass"
		check.Evidence = "db/README.md contains no CREATE TABLE DDL this extractor recognizes (it may be prose-only) — no schema contract to verify"
		return check, nil
	}

	dbPath := planDriftWorkflowDBPath(workspacePath)
	db, err := openPlanDriftQueryOnlyDB(dbPath)
	if err != nil {
		check.Status = "fail"
		check.Evidence = fmt.Sprintf("db/README.md documents %d table(s) but db/db.sqlite could not be opened: %v", len(statements), err)
		return check, nil
	}
	defer db.Close()

	var failures []string
	checked := 0
	for _, ddl := range statements {
		tableName, declaredCols, err := ddlDeclaredColumns(ctx, ddl)
		if err != nil {
			failures = append(failures, fmt.Sprintf("unparseable documented DDL: %v", err))
			continue
		}
		checked++
		liveCols, exists, err := liveTableColumns(ctx, db, tableName)
		if err != nil {
			failures = append(failures, fmt.Sprintf("table %q: failed to read live schema: %v", tableName, err))
			continue
		}
		if !exists {
			failures = append(failures, fmt.Sprintf("table %q is documented in db/README.md but does not exist in db.sqlite", tableName))
			continue
		}
		missing := stringSetDiff(declaredCols, liveCols)
		extra := stringSetDiff(liveCols, declaredCols)
		if len(missing) > 0 {
			failures = append(failures, fmt.Sprintf("table %q: README declares column(s) %s that no longer exist live", tableName, strings.Join(missing, ", ")))
		}
		if len(extra) > 0 {
			failures = append(failures, fmt.Sprintf("table %q: live schema has undocumented column(s) %s", tableName, strings.Join(extra, ", ")))
		}
	}

	if len(failures) == 0 {
		check.Status = "pass"
		check.Evidence = fmt.Sprintf("all %d documented table(s) in db/README.md match the live db.sqlite schema", checked)
		return check, nil
	}
	check.Status = "fail"
	check.Evidence = fmt.Sprintf("%d documented table(s) checked, contract drift found: %s", checked, strings.Join(failures, "; "))
	return check, nil
}

const orphanedTablesDriftCheckID = "orphaned_tables"

// planDriftReservedTables mirrors the reserved-table denylist in
// workspace/handlers/query.go's UpdateReportField (reportFieldUpdateReservedTables)
// -- a different Go module, so it can't be imported directly, but the two
// lists must stay in sync: these are the platform-managed tables that are
// never orphaned by definition, since nothing workflow-authored is expected
// to reference them directly.
var planDriftReservedTables = map[string]bool{
	"report_human_inputs":       true,
	"report_human_input_events": true,
	"schema_migration_log":      true,
	"run_concerns":              true,
	// eval_results is retired: reserved as read-only history.
	"eval_results": true,

	"pulse_module_state":      true,
	"pulse_module_audit":      true,
	"report_field_update_log": true,
	// The Goal Lead's recommendations and decision log (PLAT-697).
	"pulse_recommendations": true,
	// The Goal Lead's conversation, its Pulse-tab log and its QA requests
	// (PLAT-697 phase 4).
	"goal_lead_conversation": true,
	"goal_lead_messages":     true,
	"goal_lead_qa_requests":  true,
}

// sqlTableReferencePattern extracts table names referenced by FROM/JOIN/
// UPDATE/INTO clauses. Deliberately a heuristic, not a SQL parser -- the same
// tradeoff as the query extractors above -- so it can under-detect (a
// reference hidden behind a CTE alias, a dynamically-built table name) but
// should not over-detect: every match is a real keyword immediately followed
// by a real identifier.
var sqlTableReferencePattern = regexp.MustCompile(`(?i)\b(?:FROM|JOIN|UPDATE|INTO)\s+"?(\w+)"?`)

func extractSQLTableReferences(sqlText string) []string {
	var names []string
	for _, m := range sqlTableReferencePattern.FindAllStringSubmatch(sqlText, -1) {
		if len(m) >= 2 {
			names = append(names, m[1])
		}
	}
	return names
}

// CheckOrphanedTables flags a live db.sqlite table with zero references
// across every source this package's other checks already extract from:
// report queries, every step's validation_schema db[] rules, every scripted
// step's main.py queries, and db/README.md's own documented table names.
// Deliberately takes those as pre-aggregated inputs rather than assembling
// them itself: doing so needs every step's validation_schema and scripted
// code, which means parsing the whole plan.json step-type union — that
// aggregation is phase-3 orchestration-layer work (it needs to iterate all
// steps anyway to schedule their own checks), not something this pure check
// should duplicate. A table with no references anywhere in these known
// sources is a real orphan CANDIDATE — the check names its limitations in
// its own evidence rather than asserting certainty a heuristic scan cannot
// have, since only a portion of a workflow's total references are scanned by
// the sources listed above (e.g. free-text mentions in an evaluation step's
// Description are not covered — see PLAT-258 investigation notes).
func CheckOrphanedTables(ctx context.Context, workspacePath string, allDBRuleSQL, allScriptedCodeQueries, allReportQueries, readmeDeclaredTables []string) (StepDriftCheck, error) {
	check := StepDriftCheck{CheckID: orphanedTablesDriftCheckID}

	dbPath := planDriftWorkflowDBPath(workspacePath)
	db, err := openPlanDriftQueryOnlyDB(dbPath)
	if err != nil {
		check.Status = "fail"
		check.Evidence = fmt.Sprintf("db/db.sqlite could not be opened: %v", err)
		return check, nil
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		check.Status = "fail"
		check.Evidence = fmt.Sprintf("failed to list live tables: %v", err)
		return check, nil
	}
	var liveTables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			check.Status = "fail"
			check.Evidence = fmt.Sprintf("failed to read live table list: %v", err)
			return check, nil
		}
		liveTables = append(liveTables, name)
	}
	if err := rows.Close(); err != nil {
		check.Status = "fail"
		check.Evidence = fmt.Sprintf("failed to read live table list: %v", err)
		return check, nil
	}

	referenced := make(map[string]bool)
	for _, sqlText := range allDBRuleSQL {
		for _, name := range extractSQLTableReferences(sqlText) {
			referenced[name] = true
		}
	}
	for _, sqlText := range allScriptedCodeQueries {
		for _, name := range extractSQLTableReferences(sqlText) {
			referenced[name] = true
		}
	}
	for _, sqlText := range allReportQueries {
		for _, name := range extractSQLTableReferences(sqlText) {
			referenced[name] = true
		}
	}
	for _, name := range readmeDeclaredTables {
		referenced[name] = true
	}

	var orphans []string
	for _, table := range liveTables {
		if planDriftReservedTables[strings.ToLower(table)] || referenced[table] {
			continue
		}
		orphans = append(orphans, table)
	}
	sort.Strings(orphans)

	if len(orphans) == 0 {
		check.Status = "pass"
		check.Evidence = fmt.Sprintf("all %d live table(s) (excluding platform-reserved ones) have at least one known reference", len(liveTables))
		return check, nil
	}
	check.Status = "fail"
	check.Evidence = fmt.Sprintf(
		"%d of %d live table(s) have zero references across report queries, validation_schema db[] rules, scripted main.py queries, and db/README.md: %s — candidates for cleanup via apply_workflow_db_migration (which auto-snapshots before any destructive change), not a raw DROP; verify manually before removing, since this is a heuristic scan of known sources, not a full-plan reference audit",
		len(orphans), len(liveTables), strings.Join(orphans, ", "))
	return check, nil
}
