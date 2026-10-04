package models

// QueryRequest represents a read-only SQL query against a workflow's SQLite DB.
type QueryRequest struct {
	// DBPath is a workspace-relative path to the SQLite file,
	// e.g. "Workflow/social-media/db/db.sqlite".
	DBPath string `json:"db_path" binding:"required"`
	// SQL is the read-only query to run. The server validates the statement and
	// uses a WAL-capable connection with query_only enabled.
	SQL string `json:"sql" binding:"required"`
	// Params binds positional values for ? placeholders.
	Params []interface{} `json:"params,omitempty"`
	// MaxRows bounds rows returned to the caller. Zero uses the server default.
	MaxRows int `json:"max_rows,omitempty"`
}

// QueryResponse is the result of a read-only SQL query: rows are returned as an
// array of objects keyed by column name so report widget renderers consume them
// the same way they consumed parsed JSON arrays.
type QueryResponse struct {
	Columns []string                 `json:"columns"`
	Rows    []map[string]interface{} `json:"rows"`
	// Truncated is true when additional rows existed beyond MaxRows.
	Truncated bool `json:"truncated,omitempty"`
}

// MutationStatement is one permitted DML operation in an atomic workflow DB
// mutation. Schema changes use the workflow migration path, not this API.
type MutationStatement struct {
	SQL    string        `json:"sql" binding:"required"`
	Params []interface{} `json:"params,omitempty"`
	// ParamSets runs SQL once per entry (an executemany), inside the same
	// transaction. It excludes Params: a statement has one or the other. The
	// receipt for the statement sums the rows affected over every set.
	ParamSets [][]interface{} `json:"param_sets,omitempty"`
}

// MutationRequest executes all statements in one transaction against an
// existing workflow database.
type MutationRequest struct {
	DBPath     string              `json:"db_path" binding:"required"`
	Statements []MutationStatement `json:"statements" binding:"required"`
}

// MutationStatementResult reports the deterministic receipt for one statement.
type MutationStatementResult struct {
	RowsAffected int64 `json:"rows_affected"`
	LastInsertID int64 `json:"last_insert_id,omitempty"`
}

// MutationResponse is returned only after the complete transaction commits.
type MutationResponse struct {
	Results           []MutationStatementResult `json:"results"`
	TotalRowsAffected int64                     `json:"total_rows_affected"`
}

// InitializeDatabaseRequest creates a missing managed SQLite database and
// applies idempotent schema migrations. The route is protected by the
// workspace service token and is intended for trusted platform initializers,
// never arbitrary agent SQL.
type InitializeDatabaseRequest struct {
	DBPath     string   `json:"db_path" binding:"required"`
	// An empty list is the platform initializer's explicit request to create a
	// valid, empty managed SQLite database before the first product task runs.
	Migrations []string `json:"migrations"`
	// MigrationFile is the optional source filename this batch of statements
	// came from (e.g. "2026-08-06-action-outcome-measurement.sql"), recorded
	// in the durable _schema_migration_log ledger for audit. A caller that
	// applies a fixed, compiled-in migration list (a product runtime, not a
	// file-based caller) may leave this empty.
	MigrationFile string `json:"migration_file,omitempty"`
}

// WorkflowDatabaseBackupSnapshotRequest asks the trusted workspace service to
// materialize a consistent backup image of one workflow database. The caller
// supplies only the canonical managed DB path; the service owns the snapshot
// destination so this route cannot become an arbitrary file-copy primitive.
type WorkflowDatabaseBackupSnapshotRequest struct {
	DBPath string `json:"db_path" binding:"required"`
}

type WorkflowDatabaseBackupSnapshotResult struct {
	SourceDBPath string `json:"source_db_path"`
	SnapshotPath string `json:"snapshot_path"`
	ChecksumPath string `json:"checksum_path"`
	SHA256       string `json:"sha256"`
	SizeBytes    int64  `json:"size_bytes"`
	CreatedAt    string `json:"created_at"`
	Integrity    string `json:"integrity"`
}

// DBTablesResponse describes the tables in a workflow's SQLite DB, for the
// read-only DatabasePopup inspector.
type DBTablesResponse struct {
	Tables []DBTableInfo `json:"tables"`
}

type DBTableInfo struct {
	Name     string                   `json:"name"`
	Columns  []DBColumnInfo           `json:"columns"`
	RowCount int64                    `json:"row_count"`
	Sample   []map[string]interface{} `json:"sample"`
}

type DBColumnInfo struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	PrimaryKey bool   `json:"primary_key"`
}

// ReportFieldUpdateRequest is a narrow, structured write for an HTML report's
// window.report.updateField/updateFields — the browser-facing counterpart to
// the agent-only /api/mutate. Unlike /api/mutate, the caller never supplies
// SQL: table/column names come from Fields' own keys and are validated
// against the live schema, and the row is matched on that table's own
// single-column primary key — so this can only ever change one already-
// existing row's named columns in one transaction, never structure, never an
// arbitrary WHERE. Fields covers both single-field (one key) and form-style
// multi-field (several keys) writes with the same request shape.
type ReportFieldUpdateRequest struct {
	DBPath string                 `json:"db_path" binding:"required"`
	Table  string                 `json:"table" binding:"required"`
	RowID  interface{}            `json:"row_id" binding:"required"`
	Fields map[string]interface{} `json:"fields" binding:"required"`
}

// ReportFieldUpdateResponse returns both value sets, keyed by the same column
// names as the request, so the report can update its own UI optimistically
// without a full re-query, and confirm exactly what changed.
type ReportFieldUpdateResponse struct {
	Table     string                 `json:"table"`
	RowID     interface{}            `json:"row_id"`
	OldValues map[string]interface{} `json:"old_values"`
	NewValues map[string]interface{} `json:"new_values"`
}
