package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

const backgroundAgentLogSchema = `CREATE TABLE IF NOT EXISTS background_agent_log (
	workspace_path TEXT NOT NULL,
	session_id TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL DEFAULT '',
	parent_execution_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	result TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	duration TEXT NOT NULL DEFAULT '',
	started_at TEXT NOT NULL DEFAULT '',
	completed_at TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL,
	PRIMARY KEY (workspace_path, session_id, agent_id)
)`

// BackgroundAgentLogEntry is the durable, queryable record of one background
// agent's lifecycle within one workflow-scoped session.
type BackgroundAgentLogEntry struct {
	WorkspacePath     string `json:"workspace_path"`
	SessionID         string `json:"session_id"`
	AgentID           string `json:"agent_id"`
	Name              string `json:"name"`
	Kind              string `json:"kind,omitempty"`
	ParentExecutionID string `json:"parent_execution_id,omitempty"`
	Status            string `json:"status"`
	Result            string `json:"result,omitempty"`
	Error             string `json:"error,omitempty"`
	Duration          string `json:"duration,omitempty"`
	StartedAt         string `json:"started_at,omitempty"`
	CompletedAt       string `json:"completed_at,omitempty"`
	UpdatedAt         string `json:"updated_at"`
	// TranscriptPath and TranscriptStatus are PLAT-164: a reference to this
	// agent's durable structured transcript (see pkg/orchestrator/events'
	// BackgroundAgentTranscriptPath), and whether writing it actually
	// succeeded. TranscriptStatus is the visible signal requirement 5 asks
	// for — a write failure must show up here rather than this row silently
	// implying a fully auditable transcript exists just because the agent
	// itself completed.
	TranscriptPath   string `json:"transcript_path,omitempty"`
	TranscriptStatus string `json:"transcript_status,omitempty"`
}

// ensureBackgroundAgentLogColumns adds the PLAT-164 transcript-reference
// columns to background_agent_log for databases created before this ticket.
// CREATE TABLE IF NOT EXISTS does not retroactively add columns to an
// existing table, so this mirrors ensureReportHumanInputColumn's
// PRAGMA-table_info-then-ALTER-TABLE pattern.
func ensureBackgroundAgentLogColumns(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(background_agent_log)`)
	if err != nil {
		return err
	}
	cols := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pkIndex int
		var name, colType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pkIndex); err != nil {
			_ = rows.Close()
			return err
		}
		cols[name] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, col := range []string{"transcript_path", "transcript_status"} {
		if cols[col] {
			continue
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE background_agent_log ADD COLUMN %s TEXT NOT NULL DEFAULT ''`, col)); err != nil {
			return err
		}
	}
	return nil
}

// openBackgroundAgentLogWriter owns the shared lifecycle schema. It must not
// depend on the retired review engine to initialize a newly created Goal.
func openBackgroundAgentLogWriter(ctx context.Context, workspacePath string) (*sql.DB, error) {
	_, db, err := openWorkflowStoreDB(ctx, workspacePath, true)
	if err != nil || db == nil {
		return db, err
	}
	if _, err := db.ExecContext(ctx, backgroundAgentLogSchema); err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureBackgroundAgentLogColumns(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// backgroundAgentLogWorkspacePath resolves which workflow's database a
// session's background-agent activity belongs to. A session with no
// workspace (plain chat) has nothing to log against — that is intentional,
// not a gap: this table exists to give workflow-scoped runs the durable
// record workflow steps already get, not to log every chat delegation.
func (api *StreamingAPI) backgroundAgentLogWorkspacePath(sessionID string) string {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	session, ok := api.getActiveSession(sessionID)
	if !ok || session == nil {
		return ""
	}
	return strings.TrimSpace(session.WorkspacePath)
}

// recordBackgroundAgentLogStarted durably records a background agent
// beginning work. Best-effort by design: a write failure is logged and
// swallowed, never returned to the caller — this is an observability record
// of the agent's execution, not a gate on it. Blocking or failing a
// background agent because its own audit log couldn't be written would be a
// strictly worse failure mode than the one this table exists to fix.
func (api *StreamingAPI) recordBackgroundAgentLogStarted(sessionID, agentID, name string, kind orchEvents.ExecutionKind, parentExecutionID string, startedAt time.Time) {
	workspacePath := api.backgroundAgentLogWorkspacePath(sessionID)
	if workspacePath == "" {
		return
	}
	ctx := context.Background()
	db, err := openBackgroundAgentLogWriter(ctx, workspacePath)
	if err != nil || db == nil {
		if err != nil {
			log.Printf("[BG_AGENT_LOG] failed to open db for %s: %v", workspacePath, err)
		}
		return
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.ExecContext(ctx, `INSERT INTO background_agent_log
		(workspace_path, session_id, agent_id, name, kind, parent_execution_id, status, started_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'running', ?, ?)
		ON CONFLICT(workspace_path, session_id, agent_id) DO UPDATE SET
			name=excluded.name, kind=excluded.kind, parent_execution_id=excluded.parent_execution_id,
			status='running', started_at=excluded.started_at, updated_at=excluded.updated_at`,
		workspacePath, sessionID, agentID, name, string(kind), parentExecutionID, startedAt.UTC().Format(time.RFC3339), now,
	); err != nil {
		log.Printf("[BG_AGENT_LOG] failed to record start for session=%s agent=%s: %v", sessionID, agentID, err)
	}
}

// recordBackgroundAgentLogCompleted durably records a background agent's
// outcome. Upserts the same row recordBackgroundAgentLogStarted created, so a
// row that only ever reaches "running" is itself a real signal: that agent
// started and this record never saw it finish.
func (api *StreamingAPI) recordBackgroundAgentLogCompleted(sessionID, agentID, name, kind, status, result, errMsg, duration string, completedAt time.Time) {
	workspacePath := api.backgroundAgentLogWorkspacePath(sessionID)
	if workspacePath == "" {
		return
	}
	ctx := context.Background()
	db, err := openBackgroundAgentLogWriter(ctx, workspacePath)
	if err != nil || db == nil {
		if err != nil {
			log.Printf("[BG_AGENT_LOG] failed to open db for %s: %v", workspacePath, err)
		}
		return
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.ExecContext(ctx, `INSERT INTO background_agent_log
		(workspace_path, session_id, agent_id, name, kind, status, result, error, duration, completed_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_path, session_id, agent_id) DO UPDATE SET
			name=excluded.name, status=excluded.status, result=excluded.result, error=excluded.error,
			duration=excluded.duration, completed_at=excluded.completed_at, updated_at=excluded.updated_at`,
		workspacePath, sessionID, agentID, name, kind, status, result, errMsg, duration, completedAt.UTC().Format(time.RFC3339), now,
	); err != nil {
		log.Printf("[BG_AGENT_LOG] failed to record completion for session=%s agent=%s: %v", sessionID, agentID, err)
	}
}

// recordBackgroundAgentTranscriptPath durably records a background agent's
// transcript reference (PLAT-164 requirement 4) and whether persisting it
// succeeded (requirement 5). Best-effort/swallowed on write failure, same
// rationale as recordBackgroundAgentLogStarted: this is itself an
// observability record, not a gate on the agent it describes. Callers pass
// status="ok" on a successful write or a short "error: ..." string
// otherwise — never leave it implying success by omission.
func (api *StreamingAPI) recordBackgroundAgentTranscriptPath(sessionID, agentID, transcriptPath, status string) {
	workspacePath := api.backgroundAgentLogWorkspacePath(sessionID)
	if workspacePath == "" {
		return
	}
	ctx := context.Background()
	db, err := openBackgroundAgentLogWriter(ctx, workspacePath)
	if err != nil || db == nil {
		if err != nil {
			log.Printf("[BG_AGENT_LOG] failed to open db for %s: %v", workspacePath, err)
		}
		return
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.ExecContext(ctx, `INSERT INTO background_agent_log
		(workspace_path, session_id, agent_id, status, transcript_path, transcript_status, updated_at)
		VALUES (?, ?, ?, 'running', ?, ?, ?)
		ON CONFLICT(workspace_path, session_id, agent_id) DO UPDATE SET
			transcript_path=excluded.transcript_path, transcript_status=excluded.transcript_status, updated_at=excluded.updated_at`,
		workspacePath, sessionID, agentID, transcriptPath, status, now,
	); err != nil {
		log.Printf("[BG_AGENT_LOG] failed to record transcript reference for session=%s agent=%s: %v", sessionID, agentID, err)
	}
}

func backgroundAgentLogForSession(ctx context.Context, workspacePath, sessionID string) ([]BackgroundAgentLogEntry, error) {
	_, db, err := openWorkflowStoreDB(ctx, workspacePath, false)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, nil
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `SELECT workspace_path, session_id, agent_id, name, kind, parent_execution_id,
		status, result, error, duration, started_at, completed_at, updated_at, transcript_path, transcript_status
		FROM background_agent_log WHERE workspace_path = ? AND session_id = ?
		ORDER BY started_at ASC, updated_at ASC`, workspacePath, sessionID)
	if err != nil {
		if isMissingTableError(err) || isMissingColumnError(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()

	var out []BackgroundAgentLogEntry
	for rows.Next() {
		var entry BackgroundAgentLogEntry
		if err := rows.Scan(&entry.WorkspacePath, &entry.SessionID, &entry.AgentID, &entry.Name, &entry.Kind,
			&entry.ParentExecutionID, &entry.Status, &entry.Result, &entry.Error, &entry.Duration,
			&entry.StartedAt, &entry.CompletedAt, &entry.UpdatedAt, &entry.TranscriptPath, &entry.TranscriptStatus); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

func isMissingTableError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

// isMissingColumnError guards backgroundAgentLogForSession's read path,
// which opens with create=false and so does not run
// ensureBackgroundAgentLogColumns. A workspace whose database predates the
// PLAT-164 transcript columns and has had no write since (which would have
// migrated it) should read back as "no rows" rather than error.
func isMissingColumnError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such column")
}
