package handlers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	"github.com/manishiitg/coding-agent-loop/workspace/utils"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"

	"database/sql/driver"

	"modernc.org/sqlite"
)

// SQLite's REGEXP operator ("X REGEXP Y") has no built-in implementation --
// it is a syntax hook that requires an application-registered regexp(Y, X)
// scalar function (SQLite docs: "X REGEXP Y is equivalent to regexp(Y,X)").
// Without this, any schema-declared query using REGEXP fails uniformly with
// "no such function: REGEXP", even though the SQL itself is otherwise valid
// (PLAT-238). modernc.org/sqlite supports registering one; mattn/go-sqlite3
// would need the same treatment if this driver ever changes. Registration is
// process-global and applies to every connection opened after it runs, so
// this init() covers both openQueryOnlyDB and openMutationDB.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("regexp", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if args[0] == nil || args[1] == nil {
			// NULL/non-text operands: SQLite's own comparison operators return
			// NULL rather than erroring when an operand is NULL.
			return nil, nil
		}
		pattern, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("regexp: pattern argument must be text, got %T", args[0])
		}
		value, ok := args[1].(string)
		if !ok {
			return nil, fmt.Errorf("regexp: value argument must be text, got %T", args[1])
		}
		matched, err := regexp.MatchString(pattern, value)
		if err != nil {
			return nil, fmt.Errorf("regexp: invalid pattern %q: %w", pattern, err)
		}
		return matched, nil
	})
}

// queryTimeout bounds a single read-only query / inspection request.
const queryTimeout = 30 * time.Second

const (
	defaultQueryMaxRows = 10_000
	maximumQueryMaxRows = 50_000
	maximumMutationSQL  = 100_000
	maximumStatements   = 200
	// maximumMutationRows bounds the executions of one mutation call: every
	// statement counts once, a statement with param_sets once per set. The
	// transaction must also finish inside queryTimeout.
	maximumMutationRows = 5_000
	// Managed migrations keep their own, small cap.
	maximumMigrationStatements = 20
	maximumPragmaErrors        = 1_000
)

// Managed migration statements are schema-only DDL, individually allow-listed
// by shape rather than parsed. SQLite itself is still the final authority on
// whether a statement that matches one of these shapes is actually valid.
//
// CREATE/DROP are required to be idempotent (IF NOT EXISTS / IF EXISTS) so
// re-applying a migration file is always a safe no-op. ALTER has no such
// idempotent form in SQLite, so an ALTER that has already been applied fails
// loudly on retry instead of silently doing nothing -- safe, just not a
// no-op. PRAGMA and ATTACH are deliberately never allow-listed here: PRAGMA
// can change database-wide behavior other concurrent readers/writers depend
// on (e.g. journal_mode), and ATTACH opens an arbitrary filesystem path
// outside FolderGuard's authorization entirely.
var (
	managedCreateSQL      = regexp.MustCompile(`(?is)^\s*CREATE\s+(?:TABLE|INDEX)\s+IF\s+NOT\s+EXISTS\b`)
	managedDropSQL        = regexp.MustCompile(`(?is)^\s*DROP\s+(?:TABLE|INDEX)\s+IF\s+EXISTS\b`)
	managedAlterAddColumn = regexp.MustCompile(`(?is)^\s*ALTER\s+TABLE\s+\S.*\s+ADD\s+COLUMN\s+\S.*$`)
	managedAlterRename    = regexp.MustCompile(`(?is)^\s*ALTER\s+TABLE\s+\S.*\s+RENAME\s+(?:TO\s+\S+|COLUMN\s+\S+\s+TO\s+\S+)\s*$`)
	managedAlterDropCol   = regexp.MustCompile(`(?is)^\s*ALTER\s+TABLE\s+\S.*\s+DROP\s+COLUMN\s+\S+\s*$`)
)

// isManagedMigrationStatement reports whether one migration string matches an
// allow-listed schema-change shape.
func isManagedMigrationStatement(statement string) bool {
	return managedCreateSQL.MatchString(statement) ||
		managedDropSQL.MatchString(statement) ||
		managedAlterAddColumn.MatchString(statement) ||
		managedAlterRename.MatchString(statement) ||
		managedAlterDropCol.MatchString(statement)
}

// isDestructiveMigrationStatement reports whether a statement can remove an
// existing table, column, or the name a caller resolves it by. ADD COLUMN is
// excluded: it can only add data, never remove or rename it.
func isDestructiveMigrationStatement(statement string) bool {
	return managedDropSQL.MatchString(statement) ||
		managedAlterRename.MatchString(statement) ||
		managedAlterDropCol.MatchString(statement)
}

// backupDatabaseBeforeDestructiveMigration writes a transactionally
// consistent VACUUM INTO snapshot of fullPath before a destructive migration
// runs. There is no human-approval gate in front of InitializeWorkflowDB, so
// this snapshot is the only recovery path if a DROP/RENAME/DROP COLUMN turns
// out to be wrong.
func backupDatabaseBeforeDestructiveMigration(ctx context.Context, fullPath string) (string, error) {
	backupDir := filepath.Join(filepath.Dir(fullPath), "migrations", ".backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", fmt.Errorf("create migration backup folder: %w", err)
	}
	// Nanosecond epoch, not a second-resolution timestamp: two destructive
	// migrations applied back to back in the same second would otherwise
	// collide on this filename, and VACUUM INTO refuses to overwrite an
	// existing file.
	backupPath := filepath.Join(backupDir, fmt.Sprintf("%d-pre-migration.sqlite", time.Now().UTC().UnixNano()))
	db, err := openMutationDB(fullPath)
	if err != nil {
		return "", fmt.Errorf("open database for pre-migration backup: %w", err)
	}
	defer db.Close()
	quoted := "'" + strings.ReplaceAll(backupPath, "'", "''") + "'"
	if _, err := db.ExecContext(ctx, "VACUUM INTO "+quoted); err != nil {
		return "", fmt.Errorf("snapshot database before destructive migration: %w", err)
	}
	pruneMigrationBackups(backupDir)
	return backupPath, nil
}

// migrationBackupRetentionCount bounds how many pre-migration snapshots
// backupDatabaseBeforeDestructiveMigration retains per workflow. Nothing else
// limits their growth: every retried destructive migration writes another
// complete database snapshot, and once the migration tool is reachable by
// real agent sessions, retries are expected, not exceptional.
const migrationBackupRetentionCount = 20

// pruneMigrationBackups deletes the oldest pre-migration snapshots in
// backupDir beyond migrationBackupRetentionCount, keeping the most recent
// ones by modification time (not filename order, which would break if the
// naming scheme ever changes). Best-effort: a prune failure is logged, not
// returned, since a housekeeping failure must never block an otherwise-
// successful migration that already committed its own snapshot.
func pruneMigrationBackups(backupDir string) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return
	}
	type backupFile struct {
		path    string
		modTime time.Time
	}
	var backups []backupFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "-pre-migration.sqlite") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		backups = append(backups, backupFile{path: filepath.Join(backupDir, entry.Name()), modTime: info.ModTime()})
	}
	if len(backups) <= migrationBackupRetentionCount {
		return
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].modTime.Before(backups[j].modTime) })
	for _, old := range backups[:len(backups)-migrationBackupRetentionCount] {
		if err := os.Remove(old.path); err != nil {
			log.Printf("[WORKFLOW_DB_MIGRATION] failed to prune old backup %q: %v", old.path, err)
		}
	}
}

// schemaMigrationLedgerTableSQL creates the durable audit ledger for every
// applied migration, inside the same transaction as the DDL it records --
// so a ledger row exists if and only if its migration actually committed.
// Not underscore-prefixed: matches this codebase's existing backend-owned
// table naming (run_concerns, pulse_module_state, eval_results, ...), not a
// distinct "system table" convention.
const schemaMigrationLedgerTableSQL = `CREATE TABLE IF NOT EXISTS schema_migration_log (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	migration_file TEXT NOT NULL DEFAULT '',
	statements_hash TEXT NOT NULL,
	destructive INTEGER NOT NULL DEFAULT 0,
	backup_path TEXT NOT NULL DEFAULT '',
	applied_by TEXT NOT NULL DEFAULT '',
	applied_at TEXT NOT NULL
)`

// recordSchemaMigrationLedgerTx records one durable ledger row for a
// migration about to commit. migrations is hashed (not stored verbatim) --
// the applied statements already live in the caller's own db/migrations/
// file when one exists; the ledger's job is proving what ran and when, not
// duplicating the SQL text.
func recordSchemaMigrationLedgerTx(ctx context.Context, tx *sql.Tx, migrationFile string, migrations []string, destructive bool, backupPath, appliedBy string) error {
	if _, err := tx.ExecContext(ctx, schemaMigrationLedgerTableSQL); err != nil {
		return fmt.Errorf("ensure schema migration ledger table: %w", err)
	}
	hash := sha256.Sum256([]byte(strings.Join(migrations, "\n")))
	destructiveFlag := 0
	if destructive {
		destructiveFlag = 1
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migration_log (migration_file, statements_hash, destructive, backup_path, applied_by, applied_at) VALUES (?, ?, ?, ?, ?, ?)`,
		migrationFile, hex.EncodeToString(hash[:]), destructiveFlag, backupPath, appliedBy, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("record schema migration ledger entry: %w", err)
	}
	return nil
}

// dbTablesSampleRows is how many sample rows the inspector returns per table.
const dbTablesSampleRows = 50

// normalizeOwnedPerUserDBPath accepts public per-user paths and the internal
// runtime spelling of the current user's path. Foreign _users trees remain
// inaccessible. Returning the public spelling lets resolveUserPath apply the
// current user scope exactly once.
func normalizeOwnedPerUserDBPath(c *gin.Context, requestedPath string) (string, error) {
	docsDir := viper.GetString("docs-dir")
	clean := utils.SanitizeInputPath(requestedPath, docsDir)
	if clean == utils.UsersDirectory || strings.HasPrefix(clean, utils.UsersDirectory+"/") {
		ownPrefix := filepath.ToSlash(filepath.Join(utils.UsersDirectory, getUserID(c))) + "/"
		if !strings.HasPrefix(clean, ownPrefix) {
			return "", fmt.Errorf("access to another user's database is not allowed")
		}
		clean = strings.TrimPrefix(clean, ownPrefix)
		if !utils.IsPerUserPath(clean) {
			return "", fmt.Errorf("invalid private database path")
		}
	}
	return clean, nil
}

// resolveReadonlyDBPath resolves a workspace-relative SQLite path with the same
// user-isolation rules as document access and rejects cross-user access and
// traversal. Returns the absolute filesystem path.
func resolveReadonlyDBPath(c *gin.Context, requestedPath string) (string, error) {
	docsDir := viper.GetString("docs-dir")
	clean, err := normalizeOwnedPerUserDBPath(c, requestedPath)
	if err != nil {
		return "", err
	}
	fullPath, err := resolveUserPath(c, clean)
	if err != nil {
		return "", err
	}
	if !utils.IsValidFilePath(fullPath, docsDir) {
		return "", fmt.Errorf("path escapes the workspace boundary")
	}
	if info, err := os.Stat(fullPath); err != nil || info.IsDir() {
		return "", fmt.Errorf("database file not found: %s", requestedPath)
	}
	return fullPath, nil
}

// openQueryOnlyDB opens an existing SQLite file read-write-capable so SQLite can
// materialize WAL/SHM sidecars, then makes every pooled connection query-only.
// SQL validation is still required: query_only is defense in depth, not the
// authorization boundary for caller-supplied SQL.
func openQueryOnlyDB(fullPath string) (*sql.DB, error) {
	dsn := sqliteopen.DSN(fullPath) + "&mode=rw&_pragma=query_only(true)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func openMutationDB(fullPath string) (*sql.DB, error) {
	dsn := sqliteopen.DSN(fullPath) + "&mode=rw"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// InitializeWorkflowDB is the generic, trusted database-template primitive.
// It creates only a workspace-relative db.sqlite and optionally accepts a
// bounded set of schema-only migration statements -- idempotent CREATE TABLE/INDEX IF NOT
// EXISTS, idempotent DROP TABLE/INDEX IF EXISTS, and ALTER TABLE RENAME
// TO/RENAME COLUMN/ADD COLUMN/DROP COLUMN. There is no human-approval gate on
// this route, so any destructive statement (DROP, RENAME, DROP COLUMN --
// never ADD COLUMN, which cannot lose data) triggers an automatic VACUUM INTO
// snapshot of the database before the migration runs; its path is returned so
// the caller can recover from it. PRAGMA and ATTACH remain out of scope
// entirely: PRAGMA can change database-wide behavior other concurrent
// readers/writers depend on, and ATTACH opens an arbitrary filesystem path
// outside FolderGuard's authorization. Normal UI reads still use /api/query
// and agent-owned row writes still use the authorized /api/mutate route.
func InitializeWorkflowDB(c *gin.Context) {
	var req models.InitializeDatabaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid request body", Error: err.Error()})
		return
	}
	if len(req.Migrations) > maximumMigrationStatements {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid migrations", Error: fmt.Sprintf("migrations must contain at most %d statements", maximumMigrationStatements)})
		return
	}
	cleanRequest, normalizeErr := normalizeOwnedPerUserDBPath(c, req.DBPath)
	if normalizeErr != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid db_path", Error: normalizeErr.Error()})
		return
	}
	cleanRequest = strings.TrimSpace(filepath.ToSlash(filepath.Clean(filepath.FromSlash(cleanRequest))))
	if !strings.HasSuffix(cleanRequest, "/db/db.sqlite") || strings.HasPrefix(cleanRequest, "../") || cleanRequest == "../db/db.sqlite" {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid db_path", Error: "managed databases must use <workspace>/db/db.sqlite"})
		return
	}
	destructive := false
	for index, migration := range req.Migrations {
		if len(migration) > maximumMutationSQL || !isManagedMigrationStatement(migration) || strings.Contains(migration, ";") {
			c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Migration rejected", Error: fmt.Sprintf("migration %d must be one CREATE TABLE/INDEX IF NOT EXISTS, DROP TABLE/INDEX IF EXISTS, or ALTER TABLE RENAME TO/RENAME COLUMN/ADD COLUMN/DROP COLUMN statement", index+1)})
			return
		}
		if isDestructiveMigrationStatement(migration) {
			destructive = true
		}
	}

	docsDir := viper.GetString("docs-dir")
	fullPath, err := resolveUserPath(c, cleanRequest)
	if err != nil || !utils.IsValidFilePath(fullPath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid db_path", Error: "path escapes the workspace boundary"})
		return
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to create database folder", Error: err.Error()})
		return
	}

	var backupPath string
	if destructive {
		if _, statErr := os.Stat(fullPath); statErr == nil {
			backupPath, err = backupDatabaseBeforeDestructiveMigration(c.Request.Context(), fullPath)
			if err != nil {
				c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to snapshot database before destructive migration", Error: err.Error()})
				return
			}
		}
	}

	db, err := sql.Open("sqlite", sqliteopen.DSN(fullPath))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to open database", Error: err.Error()})
		return
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if len(req.Migrations) == 0 {
		// Opening is lazy in database/sql; Ping forces the valid SQLite image to
		// exist on disk without inventing a user-visible placeholder table.
		if err := db.PingContext(c.Request.Context()); err != nil {
			c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to initialize database", Error: err.Error()})
			return
		}
		log.Printf("[WORKFLOW_DB_INITIALIZE] user=%q db=%q", getUserID(c), cleanRequest)
		c.JSON(http.StatusOK, models.APIResponse[map[string]any]{
			Success: true,
			Message: "Database initialized",
			Data:    map[string]any{"db_path": cleanRequest},
		})
		return
	}
	tx, err := db.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to start migration", Error: err.Error()})
		return
	}
	for index, migration := range req.Migrations {
		if _, err := tx.ExecContext(c.Request.Context(), migration); err != nil {
			_ = tx.Rollback()
			c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Migration failed", Error: fmt.Sprintf("migration %d: %v", index+1, err)})
			return
		}
	}
	if err := recordSchemaMigrationLedgerTx(c.Request.Context(), tx, req.MigrationFile, req.Migrations, destructive, backupPath, getUserID(c)); err != nil {
		_ = tx.Rollback()
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to record migration ledger", Error: err.Error()})
		return
	}
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to commit migration", Error: err.Error()})
		return
	}
	log.Printf("[WORKFLOW_DB_MIGRATION] user=%q db=%q statements=%d destructive=%v backup=%q", getUserID(c), cleanRequest, len(req.Migrations), destructive, backupPath)
	data := map[string]any{"db_path": cleanRequest}
	if backupPath != "" {
		data["backup_path"] = backupPath
	}
	c.JSON(http.StatusOK, models.APIResponse[map[string]any]{Success: true, Message: "Database initialized", Data: data})
}

// scanRows reads all rows of a *sql.Rows into []map[string]interface{}, keyed by
// column name. []byte values are coerced to string so JSON output is readable.
func scanRows(rows *sql.Rows, maxRows int) ([]string, []map[string]interface{}, bool, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, false, err
	}
	out := make([]map[string]interface{}, 0)
	truncated := false
	for rows.Next() {
		if len(out) >= maxRows {
			truncated = true
			break
		}
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, false, err
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
	return cols, out, truncated, rows.Err()
}

var (
	readPragmaPattern      = regexp.MustCompile(`(?is)^pragma\s+(?:main\.)?([a-z_]+)\s*(?:\(([^;]*)\))?\s*;?$`)
	positiveIntegerPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
	sqlIdentifierPattern   = regexp.MustCompile(`(?s)^(?:[A-Za-z_][A-Za-z0-9_]*|"(?:[^"]|"")+"|'(?:[^']|'')+'|\[[^\]]+\]|` + "`(?:[^`]|``)+`" + `)$`)
)

// isSafeReadPragma deliberately recognizes PRAGMA names and their argument
// shapes instead of growing one permissive regular expression. Every entry in
// this allowlist is observational; assignments and unknown pragmas are denied.
func isSafeReadPragma(input string) bool {
	matches := readPragmaPattern.FindStringSubmatch(strings.TrimSpace(input))
	if matches == nil {
		return false
	}
	name := strings.ToLower(matches[1])
	argument := strings.TrimSpace(matches[2])
	hasArgument := strings.Contains(matches[0], "(")

	switch name {
	case "table_info", "table_xinfo", "index_list", "index_info", "index_xinfo", "foreign_key_list":
		return hasArgument && argument != "" && sqlIdentifierPattern.MatchString(argument)
	case "database_list", "journal_mode", "user_version", "schema_version":
		return !hasArgument
	case "integrity_check", "quick_check":
		if !hasArgument {
			return true
		}
		if !positiveIntegerPattern.MatchString(argument) {
			return false
		}
		limit, err := strconv.Atoi(argument)
		return err == nil && limit <= maximumPragmaErrors
	case "foreign_key_check":
		return !hasArgument || sqlIdentifierPattern.MatchString(argument)
	default:
		return false
	}
}

// stripSQLCommentsAndSpace removes leading whitespace and comments so policy is
// applied to the actual first token rather than a model-controlled prefix.
func stripSQLCommentsAndSpace(input string) string {
	remaining := input
	for {
		remaining = strings.TrimSpace(remaining)
		switch {
		case strings.HasPrefix(remaining, "--"):
			if newline := strings.IndexByte(remaining, '\n'); newline >= 0 {
				remaining = remaining[newline+1:]
				continue
			}
			return ""
		case strings.HasPrefix(remaining, "/*"):
			if end := strings.Index(remaining[2:], "*/"); end >= 0 {
				remaining = remaining[end+4:]
				continue
			}
			return remaining
		default:
			return remaining
		}
	}
}

// hasAdditionalSQLStatement rejects stacked SQL while allowing one optional
// trailing semicolon. It understands SQLite strings, identifiers and comments.
func hasAdditionalSQLStatement(input string) bool {
	const (
		plain = iota
		singleQuote
		doubleQuote
		backtickQuote
		bracketQuote
		lineComment
		blockComment
	)
	state := plain
	for i := 0; i < len(input); i++ {
		ch := input[i]
		next := byte(0)
		if i+1 < len(input) {
			next = input[i+1]
		}
		switch state {
		case plain:
			switch {
			case ch == '\'':
				state = singleQuote
			case ch == '"':
				state = doubleQuote
			case ch == '`':
				state = backtickQuote
			case ch == '[':
				state = bracketQuote
			case ch == '-' && next == '-':
				state = lineComment
				i++
			case ch == '/' && next == '*':
				state = blockComment
				i++
			case ch == ';':
				return stripSQLCommentsAndSpace(input[i+1:]) != ""
			}
		case singleQuote:
			if ch == '\'' {
				if next == '\'' {
					i++
				} else {
					state = plain
				}
			}
		case doubleQuote:
			if ch == '"' {
				if next == '"' {
					i++
				} else {
					state = plain
				}
			}
		case backtickQuote:
			if ch == '`' {
				state = plain
			}
		case bracketQuote:
			if ch == ']' {
				state = plain
			}
		case lineComment:
			if ch == '\n' {
				state = plain
			}
		case blockComment:
			if ch == '*' && next == '/' {
				state = plain
				i++
			}
		}
	}
	return false
}

func firstSQLKeyword(input string) string {
	input = stripSQLCommentsAndSpace(input)
	end := 0
	for end < len(input) {
		ch := input[end]
		if ch < 'A' || ch > 'Z' && ch < 'a' || ch > 'z' {
			break
		}
		end++
	}
	return strings.ToUpper(input[:end])
}

type sqlPolicyToken struct {
	word       string
	symbol     byte
	identifier bool
}

// tokenizeSQLForPolicy is a deliberately small lexer for the mutation policy.
// It does not try to validate SQLite syntax; SQLite still does that. Its job is
// only to distinguish CTE structure from quoted text/comments so the policy can
// identify the top-level statement that follows a WITH clause without guessing
// from a substring.
func tokenizeSQLForPolicy(input string) ([]sqlPolicyToken, error) {
	var tokens []sqlPolicyToken
	for i := 0; i < len(input); {
		ch := input[i]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' || ch == '\f':
			i++
		case ch == '-' && i+1 < len(input) && input[i+1] == '-':
			i += 2
			for i < len(input) && input[i] != '\n' {
				i++
			}
		case ch == '/' && i+1 < len(input) && input[i+1] == '*':
			end := strings.Index(input[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("unterminated block comment")
			}
			i += end + 4
		case ch == '\'' || ch == '"' || ch == '`':
			quote := ch
			quotedIdentifier := quote != '\''
			i++
			closed := false
			for i < len(input) {
				if input[i] != quote {
					i++
					continue
				}
				if i+1 < len(input) && input[i+1] == quote {
					i += 2
					continue
				}
				i++
				closed = true
				break
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quoted value")
			}
			tokens = append(tokens, sqlPolicyToken{identifier: quotedIdentifier})
		case ch == '[':
			i++
			closed := false
			for i < len(input) {
				if input[i] == ']' {
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quoted identifier")
			}
			tokens = append(tokens, sqlPolicyToken{identifier: true})
		case ch == '(' || ch == ')' || ch == ',' || ch == ';':
			tokens = append(tokens, sqlPolicyToken{symbol: ch})
			i++
		case ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z':
			start := i
			i++
			for i < len(input) {
				current := input[i]
				if current == '_' || current == '$' || current >= 'A' && current <= 'Z' || current >= 'a' && current <= 'z' || current >= '0' && current <= '9' {
					i++
					continue
				}
				break
			}
			tokens = append(tokens, sqlPolicyToken{word: strings.ToUpper(input[start:i]), identifier: true})
		default:
			tokens = append(tokens, sqlPolicyToken{symbol: ch})
			i++
		}
	}
	return tokens, nil
}

func skipSQLPolicyParentheses(tokens []sqlPolicyToken, start int) (int, error) {
	if start >= len(tokens) || tokens[start].symbol != '(' {
		return start, fmt.Errorf("expected parenthesized CTE query")
	}
	depth := 0
	for i := start; i < len(tokens); i++ {
		switch tokens[i].symbol {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1, nil
			}
			if depth < 0 {
				return i, fmt.Errorf("unbalanced parentheses")
			}
		}
	}
	return len(tokens), fmt.Errorf("unterminated parenthesized CTE query")
}

// statementKeywordAfterWith parses only the SQLite WITH-clause envelope and
// returns the top-level statement keyword after its CTE definitions. This keeps
// the mutation endpoint fail-closed: WITH ... SELECT/CREATE/PRAGMA are not
// authorized merely because a nested or quoted INSERT token exists.
func statementKeywordAfterWith(input string) (string, error) {
	tokens, err := tokenizeSQLForPolicy(input)
	if err != nil {
		return "", err
	}
	if len(tokens) == 0 || tokens[0].word != "WITH" {
		return "", fmt.Errorf("expected WITH")
	}
	index := 1
	if index < len(tokens) && tokens[index].word == "RECURSIVE" {
		index++
	}
	for {
		if index >= len(tokens) || !tokens[index].identifier {
			return "", fmt.Errorf("expected CTE name")
		}
		index++
		if index < len(tokens) && tokens[index].symbol == '(' {
			index, err = skipSQLPolicyParentheses(tokens, index)
			if err != nil {
				return "", err
			}
		}
		if index >= len(tokens) || tokens[index].word != "AS" {
			return "", fmt.Errorf("expected AS after CTE name")
		}
		index++
		if index < len(tokens) && tokens[index].word == "NOT" {
			index++
			if index >= len(tokens) || tokens[index].word != "MATERIALIZED" {
				return "", fmt.Errorf("expected MATERIALIZED after NOT")
			}
			index++
		} else if index < len(tokens) && tokens[index].word == "MATERIALIZED" {
			index++
		}
		index, err = skipSQLPolicyParentheses(tokens, index)
		if err != nil {
			return "", err
		}
		if index < len(tokens) && tokens[index].symbol == ',' {
			index++
			continue
		}
		if index >= len(tokens) || tokens[index].word == "" {
			return "", fmt.Errorf("expected statement after WITH clause")
		}
		return tokens[index].word, nil
	}
}

func validateReadSQL(input string) error {
	trimmed := stripSQLCommentsAndSpace(input)
	if trimmed == "" {
		return fmt.Errorf("sql cannot be empty")
	}
	if len(trimmed) > maximumMutationSQL {
		return fmt.Errorf("sql exceeds %d bytes", maximumMutationSQL)
	}
	if hasAdditionalSQLStatement(trimmed) {
		return fmt.Errorf("exactly one SQL statement is allowed")
	}
	if strings.Contains(strings.ToLower(trimmed), "load_extension") {
		return fmt.Errorf("extension loading is not allowed")
	}
	switch firstSQLKeyword(trimmed) {
	case "SELECT", "WITH", "EXPLAIN":
		return nil
	case "PRAGMA":
		if isSafeReadPragma(trimmed) {
			return nil
		}
		return fmt.Errorf("pragma is not in the read-only allowlist")
	default:
		return fmt.Errorf("only SELECT, read-only WITH/EXPLAIN, and safe schema PRAGMA statements are allowed")
	}
}

func validateMutationSQL(input string) error {
	trimmed := stripSQLCommentsAndSpace(input)
	if trimmed == "" {
		return fmt.Errorf("sql cannot be empty")
	}
	if len(trimmed) > maximumMutationSQL {
		return fmt.Errorf("sql exceeds %d bytes", maximumMutationSQL)
	}
	if hasAdditionalSQLStatement(trimmed) {
		return fmt.Errorf("exactly one SQL statement is allowed")
	}
	keyword := firstSQLKeyword(trimmed)
	if keyword == "WITH" {
		var err error
		keyword, err = statementKeywordAfterWith(trimmed)
		if err != nil {
			return fmt.Errorf("invalid WITH mutation: %w", err)
		}
	}
	switch keyword {
	case "INSERT", "UPDATE", "DELETE":
		return nil
	default:
		return fmt.Errorf("only INSERT, UPDATE, and DELETE, optionally prefixed by WITH, are allowed; schema changes use workflow migrations")
	}
}

// QueryWorkflowDB handles POST /api/query — runs a read-only SQL query against a
// workflow's db/db.sqlite and returns rows as an array of objects.
func QueryWorkflowDB(c *gin.Context) {
	var req models.QueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false, Message: "Invalid request body", Error: err.Error(),
		})
		return
	}
	if strings.TrimSpace(req.SQL) == "" {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false, Message: "sql is required", Error: "sql cannot be empty",
		})
		return
	}
	if err := validateReadSQL(req.SQL); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false, Message: "Query rejected", Error: err.Error(),
		})
		return
	}

	fullPath, err := resolveReadonlyDBPath(c, req.DBPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false, Message: "Invalid db_path", Error: err.Error(),
		})
		return
	}

	db, err := openQueryOnlyDB(fullPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false, Message: "Failed to open database", Error: err.Error(),
		})
		return
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(c.Request.Context(), queryTimeout)
	defer cancel()

	rows, err := db.QueryContext(ctx, req.SQL, req.Params...)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false, Message: "Query failed", Error: err.Error(),
		})
		return
	}
	defer rows.Close()

	maxRows := req.MaxRows
	if maxRows <= 0 {
		maxRows = defaultQueryMaxRows
	}
	if maxRows > maximumQueryMaxRows {
		maxRows = maximumQueryMaxRows
	}
	cols, data, truncated, err := scanRows(rows, maxRows)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false, Message: "Failed to read rows", Error: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, models.APIResponse[models.QueryResponse]{
		Success: true,
		Data:    models.QueryResponse{Columns: cols, Rows: data, Truncated: truncated},
	})
}

// MutateWorkflowDB handles POST /api/mutate. The caller must already hold the
// workspace service token; agent-side tool registration and FolderGuard decide
// whether that trusted session has workflow DB write authority.
func MutateWorkflowDB(c *gin.Context) {
	var req models.MutationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid request body", Error: err.Error()})
		return
	}
	if len(req.Statements) == 0 || len(req.Statements) > maximumStatements {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid statements", Error: fmt.Sprintf("statements must contain 1-%d operations", maximumStatements)})
		return
	}
	executions := 0
	for i, statement := range req.Statements {
		if err := validateMutationSQL(statement.SQL); err != nil {
			c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Mutation rejected", Error: fmt.Sprintf("statement %d: %v", i+1, err)})
			return
		}
		if len(statement.ParamSets) > 0 && len(statement.Params) > 0 {
			c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Mutation rejected", Error: fmt.Sprintf("statement %d: pass params or param_sets, not both", i+1)})
			return
		}
		if len(statement.ParamSets) > 0 {
			executions += len(statement.ParamSets)
		} else {
			executions++
		}
	}
	if executions > maximumMutationRows {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid statements", Error: fmt.Sprintf("a mutation may run at most %d statements and param_sets rows in total (got %d); split it into several calls", maximumMutationRows, executions)})
		return
	}

	fullPath, err := resolveReadonlyDBPath(c, req.DBPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid db_path", Error: err.Error()})
		return
	}
	db, err := openMutationDB(fullPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to open database", Error: err.Error()})
		return
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(c.Request.Context(), queryTimeout)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to start transaction", Error: err.Error()})
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	response := models.MutationResponse{Results: make([]models.MutationStatementResult, 0, len(req.Statements))}
	for i, statement := range req.Statements {
		if len(statement.ParamSets) > 0 {
			receipt, execErr := execParamSets(ctx, tx, statement)
			if execErr != nil {
				c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Mutation failed and was rolled back", Error: fmt.Sprintf("statement %d: %v", i+1, execErr)})
				return
			}
			response.Results = append(response.Results, receipt)
			response.TotalRowsAffected += receipt.RowsAffected
			continue
		}
		result, execErr := tx.ExecContext(ctx, statement.SQL, statement.Params...)
		if execErr != nil {
			c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Mutation failed and was rolled back", Error: fmt.Sprintf("statement %d: %v", i+1, execErr)})
			return
		}
		rowsAffected, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to build mutation receipt", Error: rowsErr.Error()})
			return
		}
		lastInsertID, _ := result.LastInsertId()
		response.Results = append(response.Results, models.MutationStatementResult{RowsAffected: rowsAffected, LastInsertID: lastInsertID})
		response.TotalRowsAffected += rowsAffected
	}
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to commit mutation", Error: err.Error()})
		return
	}
	committed = true
	log.Printf("[WORKFLOW_DB_MUTATION] user=%q db=%q statements=%d rows_affected=%d", getUserID(c), req.DBPath, len(req.Statements), response.TotalRowsAffected)
	c.JSON(http.StatusOK, models.APIResponse[models.MutationResponse]{Success: true, Data: response})
}

// GetWorkflowDBTables handles GET /api/db/tables?db_path=... — lists tables,
// per-table schema, row count and a small sample, for the DatabasePopup viewer.
func GetWorkflowDBTables(c *gin.Context) {
	dbPath := c.Query("db_path")
	if strings.TrimSpace(dbPath) == "" {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false, Message: "db_path is required", Error: "db_path query parameter cannot be empty",
		})
		return
	}

	fullPath, err := resolveReadonlyDBPath(c, dbPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false, Message: "Invalid db_path", Error: err.Error(),
		})
		return
	}

	db, err := openQueryOnlyDB(fullPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false, Message: "Failed to open database", Error: err.Error(),
		})
		return
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(c.Request.Context(), queryTimeout)
	defer cancel()

	tableNames, err := listTableNames(ctx, db)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false, Message: "Failed to list tables", Error: err.Error(),
		})
		return
	}

	tables := make([]models.DBTableInfo, 0, len(tableNames))
	for _, name := range tableNames {
		info := models.DBTableInfo{Name: name}

		if cols, err := tableColumns(ctx, db, name); err == nil {
			info.Columns = cols
		}

		quoted := quoteIdent(name)
		_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quoted).Scan(&info.RowCount)

		if sampleRows, err := db.QueryContext(ctx, "SELECT * FROM "+quoted+" LIMIT ?", dbTablesSampleRows); err == nil {
			if _, data, _, err := scanRows(sampleRows, dbTablesSampleRows); err == nil {
				info.Sample = data
			}
			sampleRows.Close()
		}

		tables = append(tables, info)
	}

	c.JSON(http.StatusOK, models.APIResponse[models.DBTablesResponse]{
		Success: true,
		Data:    models.DBTablesResponse{Tables: tables},
	})
}

// listTableNames returns user table names (excluding sqlite_* internal tables).
func listTableNames(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// tableColumns returns column metadata via PRAGMA table_info.
func tableColumns(ctx context.Context, db *sql.DB, table string) ([]models.DBColumnInfo, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+quoteIdent(table)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []models.DBColumnInfo
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notNull   int
			dfltValue interface{}
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, models.DBColumnInfo{Name: name, Type: ctype, PrimaryKey: pk > 0})
	}
	return cols, rows.Err()
}

// quoteIdent quotes a SQLite identifier (table/column) by doubling embedded
// double-quotes. Names come from sqlite_master (already-created objects), so
// this is defense-in-depth, not untrusted input.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// reportFieldUpdateReservedTables holds every platform-owned table in a
// workflow's db.sqlite that a report's own inline edit action must never
// touch — each already has its own dedicated, validated write path elsewhere
// (report_human_inputs via /report-human-inputs/{id}/answer|dismiss,
// schema_migration_log via the managed-migration route, run_concerns/
// eval_results/pulse_module_state/pulse_module_audit written by Go only, this
// endpoint's own audit log). Keep in sync with the Go-managed table list in
// guidance/templates/system/stores.md.
var reportFieldUpdateReservedTables = map[string]bool{
	"workflow_goal_metrics":     true,
	"pulse_goal_observations":   true,
	"report_human_inputs":       true,
	"report_human_input_events": true,
	"schema_migration_log":      true,
	"run_concerns":              true,
	"eval_results":              true,
	"pulse_module_state":        true,
	"pulse_module_audit":        true,
	"report_field_update_log":   true,
}

// reportFieldUpdateGuardedColumnSuffixes/Names bound what a report's own
// inline single-cell edit can touch, deliberately independent of who or what
// authored the report: a column that identifies the row or another row (the
// primary key, any *_id column) or records when the row was created/touched
// is never a legitimate "approve this email" / "flag this lead" target, so a
// call that names one is rejected before it ever reaches SQL, regardless of
// how the calling report's own JS was generated.
var reportFieldUpdateGuardedColumnNames = map[string]bool{
	"id": true, "created_at": true, "updated_at": true,
}

func isReportFieldUpdateGuardedColumn(name string, primaryKey bool) bool {
	if primaryKey {
		return true
	}
	lower := strings.ToLower(name)
	if reportFieldUpdateGuardedColumnNames[lower] {
		return true
	}
	return strings.HasSuffix(lower, "_id")
}

const maximumReportFieldUpdateValueLength = 20_000

// maximumReportFieldUpdateFields bounds a single form-style write. Not a real
// safety boundary (each field is independently validated the same as a lone
// updateField call) — just a sanity cap against a malformed/runaway caller.
const maximumReportFieldUpdateFields = 50

func validateReportFieldUpdateValue(value interface{}) error {
	switch v := value.(type) {
	case nil, bool, float64:
		return nil
	case string:
		if len(v) > maximumReportFieldUpdateValueLength {
			return fmt.Errorf("value exceeds %d characters", maximumReportFieldUpdateValueLength)
		}
		return nil
	default:
		return fmt.Errorf("value must be a plain string, number, boolean, or null — got %T", value)
	}
}

func validateReportFieldUpdateRowID(rowID interface{}) error {
	switch rowID.(type) {
	case string, float64:
		return nil
	default:
		return fmt.Errorf("row_id must be a string or number")
	}
}

// UpdateReportField handles POST /api/report-field — the write half of an
// interactive HTML report (window.report.updateField/updateFields). It is
// deliberately NOT a general mutation route: the caller never supplies SQL,
// only a table, row id, and a {column: value} map, all validated against the
// database's own live schema before one parameterized UPDATE runs against
// exactly one existing row's named columns, in one transaction — either every
// field in the call is applied, or none are.
func UpdateReportField(c *gin.Context) {
	var req models.ReportFieldUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid request body", Error: err.Error()})
		return
	}
	if err := validateReportFieldUpdateRowID(req.RowID); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid row_id", Error: err.Error()})
		return
	}
	if len(req.Fields) == 0 {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid fields", Error: "fields must contain at least one column"})
		return
	}
	if len(req.Fields) > maximumReportFieldUpdateFields {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid fields", Error: fmt.Sprintf("fields must contain at most %d columns", maximumReportFieldUpdateFields)})
		return
	}
	for column, value := range req.Fields {
		if err := validateReportFieldUpdateValue(value); err != nil {
			c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid value", Error: fmt.Sprintf("field %q: %v", column, err)})
			return
		}
	}
	if reportFieldUpdateReservedTables[strings.ToLower(strings.TrimSpace(req.Table))] {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Table rejected", Error: fmt.Sprintf("table %q is platform-owned and cannot be edited from a report", req.Table)})
		return
	}

	fullPath, err := resolveReadonlyDBPath(c, req.DBPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid db_path", Error: err.Error()})
		return
	}

	db, err := openMutationDB(fullPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to open database", Error: err.Error()})
		return
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(c.Request.Context(), queryTimeout)
	defer cancel()

	tableNames, err := listTableNames(ctx, db)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to list tables", Error: err.Error()})
		return
	}
	tableFound := false
	for _, name := range tableNames {
		if name == req.Table {
			tableFound = true
			break
		}
	}
	if !tableFound {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Table not found", Error: fmt.Sprintf("table %q does not exist in this database", req.Table)})
		return
	}

	columns, err := tableColumns(ctx, db, req.Table)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to read table schema", Error: err.Error()})
		return
	}
	columnsByName := make(map[string]models.DBColumnInfo, len(columns))
	var primaryKeyColumn string
	primaryKeyCount := 0
	for _, col := range columns {
		columnsByName[col.Name] = col
		if col.PrimaryKey {
			primaryKeyCount++
			primaryKeyColumn = col.Name
		}
	}
	if primaryKeyCount != 1 {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Table not editable", Error: fmt.Sprintf("table %q must have exactly one primary key column to target a single row unambiguously (found %d)", req.Table, primaryKeyCount)})
		return
	}
	// Deterministic order: map iteration order is random in Go, and both the
	// SELECT/UPDATE column lists and the audit log must be stable and match
	// each other for a given request.
	targetColumns := make([]string, 0, len(req.Fields))
	for column := range req.Fields {
		col, ok := columnsByName[column]
		if !ok {
			c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Column not found", Error: fmt.Sprintf("column %q does not exist on table %q", column, req.Table)})
			return
		}
		if isReportFieldUpdateGuardedColumn(col.Name, col.PrimaryKey) {
			c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Column rejected", Error: fmt.Sprintf("column %q identifies or timestamps the row and cannot be edited from a report", column)})
			return
		}
		targetColumns = append(targetColumns, col.Name)
	}
	sort.Strings(targetColumns)

	quotedTable := quoteIdent(req.Table)
	quotedPK := quoteIdent(primaryKeyColumn)

	selectCols := make([]string, len(targetColumns))
	for i, name := range targetColumns {
		selectCols[i] = quoteIdent(name)
	}
	selectSQL := "SELECT " + strings.Join(selectCols, ", ") + " FROM " + quotedTable + " WHERE " + quotedPK + " = ?"
	scanTargets := make([]interface{}, len(targetColumns))
	scanValues := make([]interface{}, len(targetColumns))
	for i := range scanValues {
		scanTargets[i] = &scanValues[i]
	}
	if err := db.QueryRowContext(ctx, selectSQL, req.RowID).Scan(scanTargets...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Row not found", Error: fmt.Sprintf("no row in %q with %s=%v", req.Table, primaryKeyColumn, req.RowID)})
			return
		}
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to read current values", Error: err.Error()})
		return
	}
	oldValues := make(map[string]interface{}, len(targetColumns))
	for i, name := range targetColumns {
		v := scanValues[i]
		if b, ok := v.([]byte); ok {
			v = string(b)
		}
		oldValues[name] = v
	}

	setClauses := make([]string, len(targetColumns))
	updateArgs := make([]interface{}, 0, len(targetColumns)+1)
	for i, name := range targetColumns {
		setClauses[i] = quoteIdent(name) + " = ?"
		updateArgs = append(updateArgs, req.Fields[name])
	}
	updateArgs = append(updateArgs, req.RowID)
	updateSQL := "UPDATE " + quotedTable + " SET " + strings.Join(setClauses, ", ") + " WHERE " + quotedPK + " = ?"

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to start transaction", Error: err.Error()})
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, updateSQL, updateArgs...); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Update failed", Error: err.Error()})
		return
	}
	updatedBy := getUserID(c)
	if err := recordReportFieldUpdateLog(ctx, tx, req, updatedBy, targetColumns, oldValues); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to record audit log", Error: err.Error()})
		return
	}
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to commit update", Error: err.Error()})
		return
	}
	committed = true
	log.Printf("[REPORT_FIELD_UPDATE] user=%q db=%q table=%q row_id=%v fields=%v old=%v new=%v",
		updatedBy, req.DBPath, req.Table, req.RowID, targetColumns, oldValues, req.Fields)

	c.JSON(http.StatusOK, models.APIResponse[models.ReportFieldUpdateResponse]{
		Success: true,
		Data: models.ReportFieldUpdateResponse{
			Table: req.Table, RowID: req.RowID,
			OldValues: oldValues, NewValues: req.Fields,
		},
	})
}

const reportFieldUpdateLogTableSQL = `CREATE TABLE IF NOT EXISTS report_field_update_log (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	table_name TEXT NOT NULL,
	row_id TEXT NOT NULL,
	column_name TEXT NOT NULL,
	old_value TEXT,
	new_value TEXT,
	updated_by TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL
)`

// recordReportFieldUpdateLog appends one durable audit row PER changed column
// — old and new value, who, when — so a mistaken or unexpected write is
// traceable and reversible-by-inspection rather than silent. Part of the same
// transaction as the UPDATE itself: a committed field change always has a
// matching audit row, and a rolled-back one never leaves a dangling log entry.
func recordReportFieldUpdateLog(ctx context.Context, tx *sql.Tx, req models.ReportFieldUpdateRequest, updatedBy string, columns []string, oldValues map[string]interface{}) error {
	if _, err := tx.ExecContext(ctx, reportFieldUpdateLogTableSQL); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, column := range columns {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO report_field_update_log (table_name, row_id, column_name, old_value, new_value, updated_by, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			req.Table, fmt.Sprintf("%v", req.RowID), column, fmt.Sprintf("%v", oldValues[column]), fmt.Sprintf("%v", req.Fields[column]), updatedBy, now); err != nil {
			return err
		}
	}
	return nil
}

// execParamSets runs one statement once per parameter set on a prepared
// statement inside the caller's transaction, and returns one receipt for the
// whole statement: the rows affected summed over every set and the last insert
// id. The first failing set aborts it (the caller rolls the transaction back).
func execParamSets(ctx context.Context, tx *sql.Tx, statement models.MutationStatement) (models.MutationStatementResult, error) {
	prepared, err := tx.PrepareContext(ctx, statement.SQL)
	if err != nil {
		return models.MutationStatementResult{}, err
	}
	defer prepared.Close()
	var receipt models.MutationStatementResult
	for row, params := range statement.ParamSets {
		result, err := prepared.ExecContext(ctx, params...)
		if err != nil {
			return models.MutationStatementResult{}, fmt.Errorf("param_sets row %d: %w", row+1, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return models.MutationStatementResult{}, err
		}
		receipt.RowsAffected += affected
		if id, idErr := result.LastInsertId(); idErr == nil {
			receipt.LastInsertID = id
		}
	}
	return receipt, nil
}
