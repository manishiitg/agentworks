package virtualtools

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const WorkflowDBToolCategory = "workflow_db"

const workflowDBAccessEnv = "WORKFLOW_DB_ACCESS"

var safeWorkflowDBTableName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// safeMigrationFileName allows only a bare filename (no "/", so no traversal
// out of the workflow's own db/migrations/ folder is representable at all).
var safeMigrationFileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}\.sql$`)

// These mirror the workspace service's own allow-list (workspace/handlers/
// query.go: isManagedMigrationStatement/isDestructiveMigrationStatement) as
// separate per-shape regexes, not one combined pattern -- CREATE/DROP only
// anchor a prefix (real DDL has more after "IF NOT/IF EXISTS"), while the
// ALTER forms anchor the whole statement, so a shared trailing "$" would
// silently break the CREATE/DROP branches. Kept as a separate copy because
// agent_go and workspace are different Go modules; this copy exists only to
// fail closed with a clear per-statement error before the HTTP round trip,
// not as the authorization boundary -- the workspace service re-validates
// every statement server-side regardless. PRAGMA and ATTACH are deliberately
// never part of this set: PRAGMA can change database-wide behavior other
// concurrent readers/writers depend on, and ATTACH opens an arbitrary
// filesystem path outside FolderGuard's authorization entirely.
var (
	managedWorkflowDBCreateSQL      = regexp.MustCompile(`(?is)^\s*CREATE\s+(?:TABLE|INDEX)\s+IF\s+NOT\s+EXISTS\b`)
	managedWorkflowDBDropSQL        = regexp.MustCompile(`(?is)^\s*DROP\s+(?:TABLE|INDEX)\s+IF\s+EXISTS\b`)
	managedWorkflowDBAlterAddColumn = regexp.MustCompile(`(?is)^\s*ALTER\s+TABLE\s+\S.*\s+ADD\s+COLUMN\s+\S.*$`)
	managedWorkflowDBAlterRename    = regexp.MustCompile(`(?is)^\s*ALTER\s+TABLE\s+\S.*\s+RENAME\s+(?:TO\s+\S+|COLUMN\s+\S+\s+TO\s+\S+)\s*$`)
	managedWorkflowDBAlterDropCol   = regexp.MustCompile(`(?is)^\s*ALTER\s+TABLE\s+\S.*\s+DROP\s+COLUMN\s+\S+\s*$`)
)

// isManagedWorkflowDBMigrationStatement reports whether one migration string
// matches an allow-listed schema-change shape, exactly mirroring the
// workspace service's own gate.
func isManagedWorkflowDBMigrationStatement(statement string) bool {
	return managedWorkflowDBCreateSQL.MatchString(statement) ||
		managedWorkflowDBDropSQL.MatchString(statement) ||
		managedWorkflowDBAlterAddColumn.MatchString(statement) ||
		managedWorkflowDBAlterRename.MatchString(statement) ||
		managedWorkflowDBAlterDropCol.MatchString(statement)
}

// workflowDBMigrationEnvelopeStatement recognizes the BEGIN/COMMIT wrapper a
// migration file conventionally uses for its own transactional intent (see
// db/migrations/*.sql). InitializeWorkflowDB already applies every migration
// in the file inside one transaction, so these statements are dropped rather
// than sent -- the backend's single-statement validator would reject them.
var workflowDBMigrationEnvelopeStatement = regexp.MustCompile(`(?is)^(BEGIN(\s+(IMMEDIATE|EXCLUSIVE|DEFERRED))?(\s+TRANSACTION)?|COMMIT(\s+TRANSACTION)?|END(\s+TRANSACTION)?)$`)

// workflowDBDescribeAllSQL lists every user table and view. It is the schema
// source for action=describe and for the schema hint attached to a failed query,
// so both answer from the same statement.
const workflowDBDescribeAllSQL = "SELECT name, type, sql FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY type, name"

// workflowDBIntegrityCheckSQL is exposed only through the named integrity_check
// action. Agents cannot use it as a path to arbitrary PRAGMA execution, while
// database-health reviewers can run the exact deterministic check their
// contract requires through the same guarded, query-only connection.
const workflowDBIntegrityCheckSQL = "PRAGMA integrity_check"

// workflowDBDescribeRows bounds the follow-up describe. Schema rows are one per
// column or one per table, so this is far above any real workflow database.
const workflowDBDescribeRows = 500

// workflowDBSchemaHintBudget caps the schema text appended to a failed query, in
// bytes, so a wide table cannot bury the SQLite error that the caller has to
// read. Names past the budget are replaced by a "(+N more)" count, and the
// caller can still get the full list with action=describe.
const workflowDBSchemaHintBudget = 1000

// workflowDBDescribeTableSQL renders the column-listing PRAGMA. Callers must
// have checked the name against safeWorkflowDBTableName first.
func workflowDBDescribeTableSQL(table string) string {
	return `PRAGMA table_info("` + strings.ReplaceAll(table, `"`, `""`) + `")`
}

type WorkflowDBToolRegistry struct {
	Tools      []llmtypes.Tool
	Executors  map[string]func(context.Context, map[string]any) (string, error)
	Categories map[string]string
}

func workflowDBQueryToolDefinition() llmtypes.Tool {
	return llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name:        "query_workflow_db",
		Description: "Read the current workflow SQLite database. Pass sql to run one statement; query is accepted as a compatibility alias. It opens read-only and cannot mutate. Use action=describe to inspect an unfamiliar table, or action=integrity_check for the guarded SQLite integrity check. The backend resolves the database; never pass a path. Single-statement, row-bounded, WAL-aware. Response: direct/native and MCP tool calls return JSON containing columns and rows. Raw shell HTTP calls to $MCP_CUSTOM/query_workflow_db return the transport envelope {success,result,error}; first require success=true, then JSON-decode the result string once to obtain the same columns/rows payload. Do not repeat a query to discover its shape or treat a failed envelope as an empty result.",
		Parameters: llmtypes.NewParameters(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":   map[string]any{"type": "string", "enum": []string{"describe", "query", "integrity_check"}, "description": "Optional. Omit it and pass sql to run a statement. Use describe to list schemas or columns; integrity_check runs the fixed guarded SQLite integrity check."},
				"table":    map[string]any{"type": "string", "description": "Optional table name for action=describe. Omit to list all table/view definitions."},
				"sql":      map[string]any{"type": "string", "description": "One SELECT, read-only WITH/EXPLAIN, or allowlisted read-only PRAGMA statement. Supported integrity checks include PRAGMA integrity_check, quick_check[(N)], and foreign_key_check[(table)]. This is the normal way to use the tool. Through the shell HTTP bridge, put SQL in a variable and JSON-encode it with jq -n --arg sql \"$sql\" '{sql:$sql}'; never inline SQL containing single quotes inside an outer single-quoted JSON literal."},
				"params":   map[string]any{"type": "array", "description": "Optional positional values for ? placeholders in sql."},
				"query":    map[string]any{"type": "string", "description": "Compatibility alias for sql. Prefer sql. If both are supplied they must be identical."},
				"max_rows": map[string]any{"type": "integer", "minimum": 1, "maximum": 1000, "description": "Maximum rows to return for action=query. Default 500."},
			},
		}),
	}}
}

func workflowDBMutateToolDefinition() llmtypes.Tool {
	return llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name:        "mutate_workflow_db",
		Description: "Mutate rows in the current workflow SQLite database. Needs trusted DB write authority. Pass sql (and optional params) for one statement, same shape as query_workflow_db; pass statements with 1-20 entries for an all-or-nothing batch. Prefer INSERT ... ON CONFLICT DO UPDATE (upsert by the table's declared primary key) over DELETE or a wholesale overwrite — a table is shared across groups/runs, and deleting/replacing rows can clobber another writer's data. Check db/README.md for the table's primary key and upsert rule before writing; a genuine DELETE is for rows this step itself owns and is retiring, not routine updates. Schema changes (new tables/indexes) are not accepted here -- write the migration to db/migrations/ with ordinary file tools, then call apply_workflow_db_migration.",
		Parameters: llmtypes.NewParameters(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"statements": map[string]any{
					"type": "array", "minItems": 1, "maxItems": 20,
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"sql":    map[string]any{"type": "string", "description": "One INSERT, UPDATE, or DELETE statement, optionally prefixed by WITH CTEs. Use ? placeholders for values."},
							"params": map[string]any{"type": "array", "description": "Optional positional values for ? placeholders."},
						},
						"required": []string{"sql"},
					},
				},
				"sql":    map[string]any{"type": "string", "description": "One INSERT, UPDATE, or DELETE statement for the single-statement form. Same shape as query_workflow_db. Use ? placeholders for values. Through the shell HTTP bridge, keep SQL in a variable and JSON-encode it with jq -n --arg; never inline quoted SQL inside a single-quoted JSON literal."},
				"params": map[string]any{"type": "array", "description": "Optional positional values for the ? placeholders in sql."},
			},
		}),
	}}
}

func workflowDBApplyMigrationToolDefinition() llmtypes.Tool {
	return llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name:        "apply_workflow_db_migration",
		Description: "Apply a schema migration already written to this workflow's db/migrations/<file>.sql to the live workflow database. Needs trusted DB write authority, same as mutate_workflow_db. Pass only the filename (no path) of a file already saved under db/migrations/; this tool never accepts inline SQL. The file may contain CREATE TABLE/INDEX IF NOT EXISTS, DROP TABLE/INDEX IF EXISTS, and ALTER TABLE RENAME TO / RENAME COLUMN / ADD COLUMN / DROP COLUMN statements (an optional BEGIN/COMMIT transaction wrapper around them is fine and is stripped automatically). Every statement runs in one all-or-nothing transaction. CREATE/DROP are idempotent -- re-applying an already-applied migration is a safe no-op; a repeated ALTER instead fails loudly, which is also safe. Any DROP, RENAME, or DROP COLUMN automatically snapshots the database first (VACUUM INTO) since there is no approval gate on this route; the response's backup_path is the recovery point if the migration turns out wrong. PRAGMA and ATTACH are never accepted, in any file. This tool cannot mutate rows -- write the migration file first with ordinary file tools, following the db/migrations/YYYY-MM-DD-slug.sql convention, then call this tool to apply it.",
		Parameters: llmtypes.NewParameters(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"migration_file": map[string]any{"type": "string", "description": "Bare filename of the migration under db/migrations/, e.g. \"2026-08-06-action-outcome-measurement.sql\". No path separators."},
			},
			"required": []string{"migration_file"},
		}),
	}}
}

func workflowDBBackupSnapshotToolDefinition() llmtypes.Tool {
	return llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name:        "create_workflow_database_snapshot",
		Description: "Create the current workflow's managed SQLite backup image. This is an operational Builder/Pulse backup tool, not a query or mutation tool. It accepts no path or SQL: the backend resolves db/db.sqlite, includes committed WAL rows, runs integrity_check, and atomically writes backup/database/db.sqlite plus its stable checksum sidecar. Use the returned snapshot_path and checksum_path for Git/object backup; never stage or copy the protected live db/db.sqlite directly.",
		Parameters: llmtypes.NewParameters(map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		}),
	}}
}

// splitSQLStatements splits a SQL script into individual statements on
// top-level semicolons, tracking quoted text and comments so a semicolon
// inside a string literal or comment never creates a spurious split.
func splitSQLStatements(script string) []string {
	var statements []string
	var current strings.Builder
	const (
		plain = iota
		single
		double
		backtick
		lineComment
		blockComment
	)
	state := plain
	runes := []rune(script)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		var next rune
		if i+1 < len(runes) {
			next = runes[i+1]
		}
		switch state {
		case plain:
			switch {
			case ch == '\'':
				state = single
			case ch == '"':
				state = double
			case ch == '`':
				state = backtick
			case ch == '-' && next == '-':
				state = lineComment
			case ch == '/' && next == '*':
				state = blockComment
			case ch == ';':
				statements = append(statements, current.String())
				current.Reset()
				continue
			}
		case single:
			if ch == '\'' {
				state = plain
			}
		case double:
			if ch == '"' {
				state = plain
			}
		case backtick:
			if ch == '`' {
				state = plain
			}
		case lineComment:
			if ch == '\n' {
				state = plain
			}
		case blockComment:
			if ch == '*' && next == '/' {
				state = plain
			}
		}
		current.WriteRune(ch)
	}
	if strings.TrimSpace(current.String()) != "" {
		statements = append(statements, current.String())
	}
	return statements
}

// stripLeadingSQLComments removes leading `-- line` and `/* block */`
// comments (and the whitespace around them) so a naturally-commented
// migration statement -- e.g. "-- Add outcome table\nCREATE TABLE IF NOT
// EXISTS ..." -- is recognized by the anchored `^\s*CREATE...` style shape
// checks in this file and in the workspace service's own validator, neither
// of which treats "--" as insignificant. Mirrors
// workspace/handlers/query.go's stripSQLCommentsAndSpace (a separate Go
// module, so duplicated rather than imported); kept only for its comment
// -> statement boundary, not as a general SQL lexer.
func stripLeadingSQLComments(input string) string {
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

// parseManagedMigrationStatements turns a migration file's contents into the
// flat []string InitializeWorkflowDB expects: transaction-envelope statements
// (BEGIN/COMMIT) removed, everything else required to already be an
// idempotent CREATE TABLE/INDEX IF NOT EXISTS statement. It fails closed and
// names the offending statement rather than forwarding something the
// workspace service would reject anyway with a less specific error.
func parseManagedMigrationStatements(script string) ([]string, error) {
	var statements []string
	for _, raw := range splitSQLStatements(script) {
		trimmed := stripLeadingSQLComments(raw)
		if trimmed == "" {
			continue
		}
		if workflowDBMigrationEnvelopeStatement.MatchString(trimmed) {
			continue
		}
		if !isManagedWorkflowDBMigrationStatement(trimmed) {
			preview := trimmed
			if len(preview) > 200 {
				preview = preview[:200] + "..."
			}
			return nil, fmt.Errorf("migration statement is not an allowed schema-change shape (CREATE TABLE/INDEX IF NOT EXISTS, DROP TABLE/INDEX IF EXISTS, or ALTER TABLE RENAME TO/RENAME COLUMN/ADD COLUMN/DROP COLUMN): %s", preview)
		}
		statements = append(statements, trimmed)
	}
	if len(statements) == 0 {
		return nil, fmt.Errorf("migration file contains no CREATE TABLE/INDEX IF NOT EXISTS statements")
	}
	return statements, nil
}

// CreateWorkflowDBToolRegistry creates one implementation shared by Builder,
// managed background agents and workflow execution steps. Availability is
// narrowed later from trusted role/db_access configuration.
// errWorkflowDBWriteGrantMissing is the refusal for a write from a session
// without the read-write DB grant. It says whose problem this is: the grant is
// set by the platform, so a caller cannot fix it (and must not try to work
// around it), only report it.
func errWorkflowDBWriteGrantMissing(op, sessionID, access string) error {
	return fmt.Errorf("workflow database %s denied for session %q: explicit db_access=read-write is required (effective value %q). "+
		"This is a platform permission problem, not something the caller can fix or work around: this session was not granted "+
		"database write access. Stop and report it as a platform issue (session %q, workflow database write grant missing)",
		op, sessionID, access, sessionID)
}

func CreateWorkflowDBToolRegistry(workspaceURL, userID, fallbackSessionID string) WorkflowDBToolRegistry {
	if strings.TrimSpace(workspaceURL) == "" {
		workspaceURL = getWorkspaceAPIURL()
	}
	client := workspace.NewClient(
		workspaceURL,
		workspace.WithUserID(userID),
		workspace.WithExtraEnv(getMCPExtraEnv(fallbackSessionID)),
	)
	queryExecutor := func(ctx context.Context, args map[string]any) (string, error) {
		dbPath, err := resolveCurrentWorkflowDBPath(ctx, fallbackSessionID)
		if err != nil {
			return "", err
		}
		action, _ := args["action"].(string)
		action = strings.TrimSpace(strings.ToLower(action))
		querySQL, err := workflowDBReadSQLArgument(args)
		if err != nil {
			return "", err
		}
		var sqlText string
		maxRows := 500
		if raw, ok := args["max_rows"].(float64); ok && raw > 0 {
			maxRows = int(raw)
		} else if raw, ok := args["max_rows"].(int); ok && raw > 0 {
			maxRows = raw
		}
		if maxRows > 1000 {
			maxRows = 1000
		}
		// Raw SQL is the contract, exactly as on mutate_workflow_db; the only
		// difference between the two tools is that this one opens the database with
		// query_only(true). `action` is an optional convenience, not a required
		// preamble — requiring it produced "action must be describe or query" and
		// "sql is required for action=query" failures from callers that had already
		// supplied valid SQL.
		if action == "" {
			if querySQL != "" {
				action = "query"
			} else if _, hasTable := args["table"]; hasTable {
				action = "describe"
			}
		}
		switch action {
		case "describe":
			table, _ := args["table"].(string)
			table = strings.TrimSpace(table)
			if table == "" {
				sqlText = workflowDBDescribeAllSQL
			} else {
				if !safeWorkflowDBTableName.MatchString(table) {
					return "", fmt.Errorf("table must contain only letters, digits, underscore, dot, or hyphen")
				}
				sqlText = workflowDBDescribeTableSQL(table)
			}
		case "query":
			sqlText = querySQL
			if sqlText == "" {
				return "", fmt.Errorf("sql (or its query alias) is required for action=query")
			}
		case "integrity_check":
			if querySQL != "" {
				return "", fmt.Errorf("action=integrity_check does not accept sql or query; it runs the fixed guarded statement %q", workflowDBIntegrityCheckSQL)
			}
			sqlText = workflowDBIntegrityCheckSQL
		default:
			return "", fmt.Errorf(
				"pass sql to run a read-only statement, action=\"describe\" (with optional table) to list schemas, or action=\"integrity_check\". Received top-level keys %v",
				sortedArgumentKeys(args),
			)
		}
		result, err := client.QueryAuthorizedWorkflowDB(ctx, workspace.QueryWorkflowDBParams{DBPath: dbPath, SQL: sqlText, Params: workflowDBParams(args), MaxRows: maxRows})
		if err != nil {
			if workflowDBUnrecognizedSigilPattern.MatchString(err.Error()) {
				return "", workflowDBUnquotedBindSigilHint(err)
			}
			// A bare "no such column: input_id" tells the caller only that its guess
			// was wrong, so it guesses again — one overnight run spent 18 tool calls
			// inventing column names and finished on `x`. Answer with the real
			// schema, read back over this same query_only(true) path.
			return "", workflowDBSchemaHintError(ctx, err, sqlText, func(hintCtx context.Context, hintSQL string) (workspace.QueryWorkflowDBResult, error) {
				return client.QueryAuthorizedWorkflowDB(hintCtx, workspace.QueryWorkflowDBParams{DBPath: dbPath, SQL: hintSQL, MaxRows: workflowDBDescribeRows})
			})
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return "", fmt.Errorf("encode workflow DB query result: %w", err)
		}
		return string(encoded), nil
	}

	mutationExecutor := func(ctx context.Context, args map[string]any) (string, error) {
		sessionID, cfg, err := resolveWorkflowDBSession(ctx, fallbackSessionID)
		if err != nil {
			return "", err
		}
		if access := strings.TrimSpace(cfg.Env[workflowDBAccessEnv]); access != "read-write" {
			return "", errWorkflowDBWriteGrantMissing("mutation", sessionID, access)
		}
		dbPath, err := resolveWorkflowDBPathFromConfig(sessionID, cfg)
		if err != nil {
			return "", err
		}
		encodedArgs, err := json.Marshal(args)
		if err != nil {
			return "", fmt.Errorf("encode mutation arguments: %w", err)
		}
		var payload struct {
			Statements []workspace.WorkflowDBMutationStatement `json:"statements"`
		}
		if err := json.Unmarshal(encodedArgs, &payload); err != nil {
			return "", fmt.Errorf("invalid mutation arguments: %w", err)
		}
		// Single-statement form: sql at the top level, identical in shape to
		// query_workflow_db. The two tools now differ only in how the database is
		// opened — query_only(true) for reads, read-write here. Requiring a
		// `statements` wrapper for one UPDATE made callers that had just used
		// query_workflow_db reach for sql=, set=, and upsert= instead.
		if len(payload.Statements) == 0 {
			if raw, _ := args["sql"].(string); strings.TrimSpace(raw) != "" {
				statement := workspace.WorkflowDBMutationStatement{SQL: raw}
				if rawParams, ok := args["params"].([]interface{}); ok {
					statement.Params = rawParams
				}
				payload.Statements = []workspace.WorkflowDBMutationStatement{statement}
			}
		}
		if len(payload.Statements) == 0 {
			// A bare "statements must contain at least one mutation" told the caller
			// nothing about the shape it wanted, so agents guessed: sql=, set=, and
			// upsert= at top level produced 10 failures in a single run. Show the
			// contract and what actually arrived.
			return "", fmt.Errorf(
				"no mutation supplied. Received top-level keys %v. "+
					"Pass sql for one statement, exactly like query_workflow_db: "+
					`{"sql":"UPDATE t SET c = ? WHERE id = ?","params":["value",1]}. `+
					"For an all-or-nothing batch pass statements with 1-20 entries of the same shape. There is no `set` or `upsert` argument",
				sortedArgumentKeys(args),
			)
		}
		result, err := client.MutateAuthorizedWorkflowDB(ctx, workspace.MutateWorkflowDBParams{DBPath: dbPath, Statements: payload.Statements})
		if err != nil {
			return "", workflowDBUnquotedBindSigilHint(err)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return "", fmt.Errorf("encode workflow DB mutation result: %w", err)
		}
		return string(encoded), nil
	}

	migrationExecutor := func(ctx context.Context, args map[string]any) (string, error) {
		sessionID, cfg, err := resolveWorkflowDBSession(ctx, fallbackSessionID)
		if err != nil {
			return "", err
		}
		if access := strings.TrimSpace(cfg.Env[workflowDBAccessEnv]); access != "read-write" {
			return "", errWorkflowDBWriteGrantMissing("migration", sessionID, access)
		}
		filename := strings.TrimSpace(fmt.Sprint(args["migration_file"]))
		if filename == "" || filename == "<nil>" {
			return "", fmt.Errorf("migration_file is required: the bare filename of a .sql file already saved under db/migrations/")
		}
		if !safeMigrationFileName.MatchString(filename) {
			return "", fmt.Errorf("migration_file must be a bare filename ending in .sql, with no path separators: got %q", filename)
		}
		migrationPath, err := resolveWorkflowMigrationFilePath(sessionID, cfg, filename)
		if err != nil {
			return "", err
		}
		dbPath, err := resolveWorkflowDBPathFromConfig(sessionID, cfg)
		if err != nil {
			return "", err
		}
		file, err := client.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: migrationPath})
		if err != nil {
			return "", fmt.Errorf("read migration file %q: %w", migrationPath, err)
		}
		statements, err := parseManagedMigrationStatements(file.Content)
		if err != nil {
			return "", fmt.Errorf("migration file %q: %w", migrationPath, err)
		}
		result, err := client.InitializeWorkflowDB(ctx, workspace.InitializeWorkflowDBParams{DBPath: dbPath, Migrations: statements, MigrationFile: filename})
		if err != nil {
			return "", fmt.Errorf("apply migration %q: %w", migrationPath, err)
		}
		response := map[string]any{
			"applied_file":       migrationPath,
			"statements_applied": len(statements),
		}
		if result.BackupPath != "" {
			response["backup_path"] = result.BackupPath
			response["note"] = "a destructive statement (DROP/RENAME/DROP COLUMN) was applied; backup_path is a pre-migration snapshot to recover from if this turns out wrong"
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			return "", fmt.Errorf("encode migration result: %w", err)
		}
		return string(encoded), nil
	}

	backupSnapshotExecutor := func(ctx context.Context, _ map[string]any) (string, error) {
		sessionID, cfg, err := resolveWorkflowDBSession(ctx, fallbackSessionID)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(cfg.Env["STEP_OUTPUT_DIR"]) != "" || strings.TrimSpace(cfg.Env["RUNLOOP_STEP_ID"]) != "" {
			return "", fmt.Errorf("workflow database backup snapshots are restricted to the parent Builder/Pulse session, not workflow steps")
		}
		access := strings.TrimSpace(cfg.Env[workflowDBAccessEnv])
		if access != "read" && access != "read-write" {
			return "", fmt.Errorf("workflow database backup snapshot denied for session %q: explicit db_access=read or read-write is required (effective value %q)", sessionID, access)
		}
		dbPath, err := resolveWorkflowDBPathFromConfig(sessionID, cfg)
		if err != nil {
			return "", err
		}
		result, err := client.CreateWorkflowDatabaseBackupSnapshot(ctx, workspace.CreateWorkflowDatabaseBackupSnapshotParams{DBPath: dbPath})
		if err != nil {
			return "", err
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return "", fmt.Errorf("encode workflow database backup snapshot result: %w", err)
		}
		return string(encoded), nil
	}

	tools := []llmtypes.Tool{workflowDBQueryToolDefinition(), workflowDBMutateToolDefinition(), workflowDBApplyMigrationToolDefinition(), workflowDBBackupSnapshotToolDefinition()}
	return WorkflowDBToolRegistry{
		Tools: tools,
		Executors: map[string]func(context.Context, map[string]any) (string, error){
			"query_workflow_db":                 queryExecutor,
			"mutate_workflow_db":                mutationExecutor,
			"apply_workflow_db_migration":       migrationExecutor,
			"create_workflow_database_snapshot": backupSnapshotExecutor,
		},
		Categories: map[string]string{
			"query_workflow_db":                 WorkflowDBToolCategory,
			"mutate_workflow_db":                WorkflowDBToolCategory,
			"apply_workflow_db_migration":       WorkflowDBToolCategory,
			"create_workflow_database_snapshot": WorkflowDBToolCategory,
		},
	}
}

// workflowDBReadSQLArgument normalizes the read tool's canonical sql argument
// and its compatibility alias. query_workflow_db used to reject {"query":"…"},
// even though that is the most natural argument name for the tool; callers then
// spent extra turns discovering the schema before retrying with sql. Both names
// reach the same query-only execution path, while conflicting inputs fail closed.
func workflowDBReadSQLArgument(args map[string]any) (string, error) {
	sqlText, _ := args["sql"].(string)
	queryText, _ := args["query"].(string)
	sqlText = strings.TrimSpace(sqlText)
	queryText = strings.TrimSpace(queryText)
	if sqlText != "" && queryText != "" && sqlText != queryText {
		return "", fmt.Errorf("sql and query were both supplied with different values; pass only sql or make them identical")
	}
	if sqlText != "" {
		return sqlText, nil
	}
	return queryText, nil
}

func workflowDBParams(args map[string]any) []interface{} {
	params, _ := args["params"].([]interface{})
	return params
}

func WorkflowDBToolNames() map[string]bool {
	return map[string]bool{"query_workflow_db": true, "mutate_workflow_db": true, "apply_workflow_db_migration": true, "create_workflow_database_snapshot": true}
}

func resolveCurrentWorkflowDBPath(ctx context.Context, fallbackSessionID string) (string, error) {
	sessionID, cfg, err := resolveWorkflowDBSession(ctx, fallbackSessionID)
	if err != nil {
		return "", err
	}
	return resolveWorkflowDBPathFromConfig(sessionID, cfg)
}

func resolveWorkflowDBSession(ctx context.Context, fallbackSessionID string) (string, *common.SessionShellConfig, error) {
	sessionID := strings.TrimSpace(fallbackSessionID)
	if ctx != nil {
		if current, ok := ctx.Value(common.ChatSessionIDKey).(string); ok && strings.TrimSpace(current) != "" {
			sessionID = strings.TrimSpace(current)
		}
	}
	if sessionID == "" {
		return "", nil, fmt.Errorf("workflow database context is unavailable: missing trusted session")
	}
	cfg := common.GetSessionShellConfig(sessionID)
	if cfg == nil {
		return "", nil, fmt.Errorf("workflow database context is unavailable for session %q", sessionID)
	}
	return sessionID, cfg, nil
}

// ResolveWorkflowWorkspaceFolder resolves the owning managed-data workspace
// for a trusted session, reusing the exact DB_PATH/read/write-path resolution
// resolveWorkflowWorkspaceFolder already does for the workflow-database
// tools. Exported so a sibling registry constructor in this package (see
// code_layout_tools.go) can address workflow.json without duplicating this
// resolution logic.
func ResolveWorkflowWorkspaceFolder(ctx context.Context, fallbackSessionID string) (string, error) {
	sessionID, cfg, err := resolveWorkflowDBSession(ctx, fallbackSessionID)
	if err != nil {
		return "", err
	}
	return resolveWorkflowWorkspaceFolder(sessionID, cfg)
}

func resolveWorkflowDBPathFromConfig(sessionID string, cfg *common.SessionShellConfig) (string, error) {
	folder, err := resolveWorkflowWorkspaceFolder(sessionID, cfg)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join(folder, "db", "db.sqlite")), nil
}

// resolveWorkflowMigrationFilePath resolves a bare migration filename (already
// validated against safeMigrationFileName by the caller) to that workflow's
// own db/migrations/<filename>, scoped by the same session context as the
// live database path. The model never supplies a directory.
func resolveWorkflowMigrationFilePath(sessionID string, cfg *common.SessionShellConfig, filename string) (string, error) {
	folder, err := resolveWorkflowWorkspaceFolder(sessionID, cfg)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join(folder, "db", "migrations", filename)), nil
}

// resolveWorkflowWorkspaceFolder finds the owning workflow or Work project for
// the trusted session, from DB_PATH or, failing that, its read/write paths and
// working dir. Shared by every resolver that needs to address a location
// inside that workflow's own db/ tree from the session context alone.
func resolveWorkflowWorkspaceFolder(sessionID string, cfg *common.SessionShellConfig) (string, error) {
	if folder := workflowDBWorkspacePathFromCandidate(cfg.Env["DB_PATH"]); folder != "" {
		return folder, nil
	}
	// Attached context is read-only: other workflows or Crew projects linked as
	// context grant their whole root, and knowledgebase_sources grant another
	// workflow's knowledgebase/. Neither says which workflow owns this session,
	// yet counting them made every DB query in such a session "ambiguous". So
	// the session's own writable roots and working dir decide first, read grants
	// only when those name nothing, and knowledgebase grants last.
	// The working dir is the session's own project, so it decides first: since
	// attached Crews are writable (server-wide Crew sharing), WritePaths alone
	// can name several Crew projects.
	own := []string{cfg.WorkingDir}
	home := append(append([]string{}, cfg.WritePaths...), cfg.WorkingDir)
	all := append(append([]string{}, cfg.ReadPaths...), home...)
	for _, tier := range []struct {
		candidates []string
		kb         bool
	}{{own, false}, {home, false}, {all, false}, {all, true}} {
		seen := map[string]bool{}
		var matches []string
		for _, candidate := range tier.candidates {
			folder := workflowDBWorkspacePathFromCandidate(candidate)
			if folder == "" || seen[folder] || isKnowledgebaseGrant(candidate, folder) != tier.kb {
				continue
			}
			seen[folder] = true
			matches = append(matches, folder)
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return "", fmt.Errorf("workflow database context is ambiguous for session %q", sessionID)
		}
	}
	return "", fmt.Errorf("workflow database context is unavailable for session %q", sessionID)
}

// workflowDBWorkspacePathFromCandidate extracts the owning workflow or Work
// project from trusted session configuration. Work paths are returned in their
// public user-relative form so the workspace API applies the authenticated
// user's private-tree scope exactly once.
// Shared by every per-workflow database resolver -- db/db.sqlite here, and
// costs/costs.sqlite in workflow_costs_tools.go -- so the two tools agree on
// exactly which workflow folder they're scoped to, from the same session.
func workflowDBWorkspacePathFromCandidate(candidate string) string {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(candidate)))
	if clean == "." || clean == "" {
		return ""
	}
	parts := strings.Split(strings.Trim(clean, "/"), "/")
	for i := 0; i+3 < len(parts); i++ {
		if parts[i] == "Chats" && (parts[i+1] == "Work" || parts[i+1] == "Code") && parts[i+2] == "projects" && strings.TrimSpace(parts[i+3]) != "" {
			return filepath.ToSlash(filepath.Join("Chats", parts[i+1], "projects", parts[i+3]))
		}
	}
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "Workflow" && strings.TrimSpace(parts[i+1]) != "" {
			if parts[i+1] == ".relay_releases" {
				return workflowtypes.RelayReleaseWorkspace(strings.Join(parts[i:], "/"))
			}

			return filepath.ToSlash(filepath.Join("Workflow", parts[i+1]))
		}
	}
	return ""
}

// isKnowledgebaseGrant reports whether candidate points at or inside the
// knowledgebase/ folder of the workflow folder it resolved to.
func isKnowledgebaseGrant(candidate, folder string) bool {
	clean := "/" + strings.Trim(filepath.ToSlash(filepath.Clean(strings.TrimSpace(candidate))), "/") + "/"
	return strings.Contains(clean, "/"+folder+"/knowledgebase/")
}

// workflowDBSchemaDescriber runs one read-only statement against the database
// the failed query already resolved to.
type workflowDBSchemaDescriber func(ctx context.Context, sqlText string) (workspace.QueryWorkflowDBResult, error)

// workflowDBSchemaHintError appends the real schema to a "no such column" or
// "no such table" failure so the caller can correct itself instead of guessing.
// The original error is always preserved and wrapped; if the follow-up describe
// itself fails, or the database has nothing to report, the original error is
// returned unchanged. Diagnostics must never turn one failure into two.
func workflowDBSchemaHintError(ctx context.Context, queryErr error, sqlText string, describe workflowDBSchemaDescriber) error {
	kind := workflowDBMissingSchemaKind(queryErr)
	if kind == "" || describe == nil {
		return queryErr
	}
	if kind == "column" {
		// Only name a table the SQL actually names. A guessed table presented as
		// fact would be a second wrong answer on top of the first.
		if table := workflowDBTableFromSQL(sqlText); table != "" {
			described, err := describe(ctx, workflowDBDescribeTableSQL(table))
			if err == nil {
				if columns := workflowDBNamedValues(described.Rows); len(columns) > 0 {
					return fmt.Errorf("%w. Table `%s` has columns: %s. Use these exact names", queryErr, table, workflowDBJoinWithinBudget(columns))
				}
			}
		}
	}
	described, err := describe(ctx, workflowDBDescribeAllSQL)
	if err != nil {
		return queryErr
	}
	names := workflowDBNamedValues(described.Rows)
	if len(names) == 0 {
		return queryErr
	}
	const nextStep = `Call query_workflow_db with action="describe" and table="<name>" to list its columns`
	if kind == "column" {
		return fmt.Errorf("%w. The table that column belongs to could not be identified from this SQL. Tables and views in this database: %s. %s", queryErr, workflowDBJoinWithinBudget(names), nextStep)
	}
	return fmt.Errorf("%w. Tables and views in this database: %s. %s", queryErr, workflowDBJoinWithinBudget(names), nextStep)
}

// workflowDBUnquotedBindSigilHint recognizes SQLite's "unrecognized token"
// error for a bare $ or @, and explains the actual mistake instead of leaving
// the caller to reverse-engineer a parser error.
//
// SQLite reserves $, @, :, and ? to introduce a bind parameter ($name, @name,
// :name) directly in SQL text. A string literal that was written unquoted and
// happens to start with $ or @ is not a parameter reference at all, but
// SQLite cannot tell that -- it tries to lex a parameter name, the next
// character breaks that (most often "." from a JSON path, or a closing
// paren), and it reports the sigil itself as the unrecognized token.
//
// The dominant instance (19 of 22 occurrences the day this was diagnosed) is
// json_extract(col, $.field) instead of json_extract(col, '$.field') -- the
// path argument to every json_* function must be a quoted string, and SQLite
// gives no hint that the fix is quoting rather than a different path. A
// second instance is a literal like @ or $ passed unquoted to a string
// function, e.g. ltrim(handle, @) meant to strip a leading "@". A generic "no
// such function" or "syntax error" would at least suggest what to search for;
// "unrecognized token: \"$\"" does not, and 22 identical failures on one
// workflow in one day is 22 tool calls that told the caller nothing about
// what to change.
func workflowDBUnquotedBindSigilHint(err error) error {
	if err == nil {
		return nil
	}
	match := workflowDBUnrecognizedSigilPattern.FindStringSubmatch(err.Error())
	if match == nil {
		return err
	}
	sigil := match[1]
	return fmt.Errorf(
		"%w. SQLite reports the bare %s itself as the unrecognized token because %s introduces a bind parameter (%sname) in SQL text -- it is not being read as a string. "+
			"If this is a JSON path argument to json_extract/json_set/json_remove/json_insert/json_replace/json_patch/json_type/json_valid, quote it: json_extract(col, '%s.field'), not json_extract(col, %s.field). "+
			"If this is meant to be a literal character or string, quote it the same way: '%s', not bare %s.",
		err, sigil, sigil, sigil, sigil, sigil, sigil, sigil,
	)
}

// workflowDBUnrecognizedSigilPattern matches SQLite's own rendering of this
// specific failure, e.g. `unrecognized token: "$"` or `unrecognized token: @`
// (quoting varies by how far the error text has been JSON-re-encoded on its
// way back through the HTTP envelope).
var workflowDBUnrecognizedSigilPattern = regexp.MustCompile(`(?i)unrecognized token:\s*\\*"?(\$|@)\\*"?`)

// workflowDBMissingSchemaKind classifies a failed query as naming a column that
// does not exist, a table that does not exist, or neither. The workspace service
// returns SQLite's message inside its JSON envelope, so this matches on the
// SQLite text wherever it sits in the error.
func workflowDBMissingSchemaKind(err error) string {
	if err == nil {
		return ""
	}
	lowered := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lowered, "no such column"):
		return "column"
	case strings.Contains(lowered, "no such table"):
		return "table"
	default:
		return ""
	}
}

var workflowDBTableReferencePattern = regexp.MustCompile("(?is)\\b(?:from|join)\\s+([A-Za-z_][A-Za-z0-9_$]*|\"[^\"]+\"|`[^`]+`|\\[[^\\]]+\\])")

// workflowDBTableFromSQL returns the single table a statement reads from, or ""
// when the statement names none, names several, or names one this code cannot
// read confidently. Confidence is the point: the caller is told the columns of a
// named table, so naming the wrong table would be worse than naming none.
func workflowDBTableFromSQL(sqlText string) string {
	stripped := workflowDBStripSQLNoise(sqlText)
	matches := workflowDBTableReferencePattern.FindAllStringSubmatchIndex(stripped, -1)
	seen := map[string]bool{}
	found := ""
	for _, match := range matches {
		name := workflowDBUnquoteIdentifier(stripped[match[2]:match[3]])
		// A name this code cannot quote safely is not a name it may report.
		if name == "" || !safeWorkflowDBTableName.MatchString(name) {
			return ""
		}
		// `FROM a, b` puts two tables in scope without a second FROM/JOIN keyword.
		if strings.EqualFold(stripped[match[0]:match[0]+4], "from") && workflowDBFromListContinues(stripped, match[3]) {
			return ""
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		if found != "" {
			return ""
		}
		found = name
	}
	return found
}

// workflowDBFromClauseBoundary marks the words that end a FROM item list, so a
// comma found after one of them belongs to some later clause rather than to the
// table list.
var workflowDBFromClauseBoundary = map[string]bool{
	"WHERE": true, "GROUP": true, "ORDER": true, "HAVING": true, "LIMIT": true,
	"OFFSET": true, "WINDOW": true, "UNION": true, "INTERSECT": true, "EXCEPT": true,
	"JOIN": true, "INNER": true, "LEFT": true, "RIGHT": true, "FULL": true,
	"CROSS": true, "NATURAL": true, "ON": true, "USING": true, "RETURNING": true,
	"SELECT": true, "VALUES": true,
}

// workflowDBFromListContinues reports whether the FROM item that ends at offset
// is followed by another comma-separated table in the same clause.
func workflowDBFromListContinues(stripped string, offset int) bool {
	depth := 0
	for i := offset; i < len(stripped); {
		ch := stripped[i]
		switch {
		case ch == '(':
			depth++
			i++
		case ch == ')':
			if depth == 0 {
				return false
			}
			depth--
			i++
		case ch == ',':
			if depth == 0 {
				return true
			}
			i++
		case ch == '"' || ch == '`' || ch == '[':
			closing := ch
			if ch == '[' {
				closing = ']'
			}
			i++
			for i < len(stripped) && stripped[i] != closing {
				i++
			}
			i++
		case ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z':
			start := i
			for i < len(stripped) {
				current := stripped[i]
				if current == '_' || current == '$' || current >= 'A' && current <= 'Z' || current >= 'a' && current <= 'z' || current >= '0' && current <= '9' {
					i++
					continue
				}
				break
			}
			if depth == 0 && workflowDBFromClauseBoundary[strings.ToUpper(stripped[start:i])] {
				return false
			}
		default:
			i++
		}
	}
	return false
}

// workflowDBStripSQLNoise blanks string literals and comments so a table scan
// cannot be fooled by the word "from" inside quoted text.
func workflowDBStripSQLNoise(input string) string {
	var out strings.Builder
	for i := 0; i < len(input); {
		switch {
		case input[i] == '\'':
			i++
			for i < len(input) {
				if input[i] != '\'' {
					i++
					continue
				}
				if i+1 < len(input) && input[i+1] == '\'' {
					i += 2
					continue
				}
				i++
				break
			}
			out.WriteByte(' ')
		case input[i] == '-' && i+1 < len(input) && input[i+1] == '-':
			for i < len(input) && input[i] != '\n' {
				i++
			}
			out.WriteByte(' ')
		case input[i] == '/' && i+1 < len(input) && input[i+1] == '*':
			i += 2
			for i+1 < len(input) && !(input[i] == '*' && input[i+1] == '/') {
				i++
			}
			if i+1 < len(input) {
				i += 2
			} else {
				i = len(input)
			}
			out.WriteByte(' ')
		default:
			out.WriteByte(input[i])
			i++
		}
	}
	return out.String()
}

// workflowDBUnquoteIdentifier removes SQLite identifier quoting.
func workflowDBUnquoteIdentifier(identifier string) string {
	identifier = strings.TrimSpace(identifier)
	if len(identifier) < 2 {
		return identifier
	}
	first, last := identifier[0], identifier[len(identifier)-1]
	switch {
	case first == '"' && last == '"':
		return strings.ReplaceAll(identifier[1:len(identifier)-1], `""`, `"`)
	case first == '`' && last == '`':
		return strings.ReplaceAll(identifier[1:len(identifier)-1], "``", "`")
	case first == '[' && last == ']':
		return identifier[1 : len(identifier)-1]
	}
	return identifier
}

// workflowDBNamedValues pulls the name column out of PRAGMA table_info or
// sqlite_master rows.
func workflowDBNamedValues(rows []map[string]interface{}) []string {
	values := make([]string, 0, len(rows))
	for _, row := range rows {
		raw, ok := row["name"]
		if !ok || raw == nil {
			continue
		}
		if name := strings.TrimSpace(fmt.Sprint(raw)); name != "" {
			values = append(values, name)
		}
	}
	return values
}

// workflowDBJoinWithinBudget joins names until workflowDBSchemaHintBudget bytes
// are used, then reports how many were left out rather than cutting a name in
// half.
func workflowDBJoinWithinBudget(values []string) string {
	var joined strings.Builder
	for index, value := range values {
		addition := len(value)
		if index > 0 {
			addition += len(", ")
		}
		if joined.Len()+addition > workflowDBSchemaHintBudget {
			fmt.Fprintf(&joined, " ... (+%d more)", len(values)-index)
			break
		}
		if index > 0 {
			joined.WriteString(", ")
		}
		joined.WriteString(value)
	}
	return joined.String()
}

// sortedArgumentKeys lists the top-level argument names a caller supplied, so a
// contract error can show what arrived next to what was required.
func sortedArgumentKeys(args map[string]any) []string {
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
