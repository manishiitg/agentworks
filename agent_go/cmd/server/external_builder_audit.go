package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Edits live in server-owned state. Prepared rows are written before a tool
// mutates anything, so a crash cannot turn a change into an invisible event.
// File rows retain bounded previous text for restoration; revisions and sizes
// remain available when the previous file exceeds the recovery cap.
const (
	externalBuilderAuditContentCap   = 128 << 10
	externalBuilderAuditHistoryLimit = 30
	externalBuilderAuditRetention    = 30 * 24 * time.Hour
)

var externalBuilderAuditPruneState = struct {
	sync.Mutex
	last     map[string]time.Time
	migrated map[string]bool
}{last: map[string]time.Time{}, migrated: map[string]bool{}}

var errExternalBuilderVersionNotRestorable = errors.New("file version exceeds recovery cap")

type externalBuilderEdit struct {
	ID             string    `json:"edit_id"`
	OperationID    string    `json:"operation_id"`
	ViaToken       string    `json:"via_token"`
	WorkflowID     string    `json:"workflow_id"`
	Tool           string    `json:"tool"`
	Path           string    `json:"path"`
	BeforeRevision string    `json:"before_revision,omitempty"`
	AfterRevision  string    `json:"after_revision,omitempty"`
	BeforeExists   bool      `json:"before_exists"`
	Restorable     bool      `json:"restorable"`
	BeforeSize     int64     `json:"before_size"`
	AfterSize      int64     `json:"after_size"`
	Status         string    `json:"status"`
	Detail         string    `json:"detail,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	BeforeContent  string    `json:"-"`
}

func migrateAndPruneExternalBuilderEdits(db *sql.DB) error {
	path, err := mcpOAuthSecret()
	if err != nil {
		return err
	}
	externalBuilderAuditPruneState.Lock()
	defer externalBuilderAuditPruneState.Unlock()
	if !externalBuilderAuditPruneState.migrated[path] {
		if err := migrateExternalBuilderEdits(db); err != nil {
			return err
		}
		externalBuilderAuditPruneState.migrated[path] = true
	}
	now := time.Now().UTC()
	if now.Sub(externalBuilderAuditPruneState.last[path]) < time.Hour {
		return nil
	}
	if _, err = db.Exec(`DELETE FROM external_builder_edits WHERE created_at<?`, now.Add(-externalBuilderAuditRetention).UnixNano()); err != nil {
		return err
	}
	externalBuilderAuditPruneState.last[path] = now
	return nil
}

func migrateExternalBuilderEdits(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`PRAGMA table_info(external_builder_edits)`)
	if err != nil {
		return err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue sql.NullString
		if err = rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	legacy := !columns["before_content_available"]
	for _, col := range []struct{ name, definition string }{
		{"before_content_available", "INTEGER NOT NULL DEFAULT 1"},
		{"before_size", "INTEGER NOT NULL DEFAULT 0"},
		{"after_size", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if !columns[col.name] {
			if _, err = tx.Exec("ALTER TABLE external_builder_edits ADD COLUMN " + col.name + " " + col.definition); err != nil {
				return err
			}
		}
	}
	if legacy {
		_, err = tx.Exec(`UPDATE external_builder_edits SET
 before_size=length(CAST(before_content AS BLOB)), after_size=length(CAST(after_content AS BLOB)),
 before_content_available=CASE WHEN length(CAST(before_content AS BLOB))>? THEN 0 ELSE 1 END,
 before_content=CASE WHEN length(CAST(before_content AS BLOB))>? THEN '' ELSE before_content END,
 after_content=''`, externalBuilderAuditContentCap, externalBuilderAuditContentCap)
		if err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`CREATE INDEX IF NOT EXISTS external_builder_edits_created ON external_builder_edits(created_at)`); err != nil {
		return err
	}
	if _, err = tx.Exec(`CREATE INDEX IF NOT EXISTS external_builder_edits_path ON external_builder_edits(user_id,workflow_id,workspace,path,created_at)`); err != nil {
		return err
	}
	return tx.Commit()
}

func prepareExternalBuilderEdit(ctx context.Context, claims *UserClaims, operationID, workflow, workspace, tool, path, beforeRevision, afterRevision, beforeContent, afterContent string, beforeExists bool) (string, error) {
	if claims == nil || claims.AccessToken == nil || claims.UserID == "" || operationID == "" || workflow == "" || workspace == "" || path == "" {
		return "", errors.New("Builder edit audit requires authenticated operation and target")
	}
	s, err := openExternalBuilderStore()
	if err != nil {
		return "", err
	}
	defer s.Close()
	id := uuid.NewString()
	exists := 0
	if beforeExists {
		exists = 1
	}
	beforeSize, afterSize := len(beforeContent), len(afterContent)
	available := 1
	if beforeSize > externalBuilderAuditContentCap {
		beforeContent = ""
		available = 0
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO external_builder_edits
 (id,operation_id,user_id,grant_id,workflow_id,workspace,tool,path,before_revision,after_revision,before_content,before_exists,before_content_available,before_size,after_size,status,created_at)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'prepared',?)`, id, operationID, claims.UserID, claims.AccessToken.ID, workflow, workspace, tool, path, beforeRevision, afterRevision, beforeContent, exists, available, beforeSize, afterSize, time.Now().UTC().UnixNano())
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM external_builder_edits WHERE user_id=? AND workflow_id=? AND workspace=? AND path=?
 AND id NOT IN (SELECT id FROM external_builder_edits WHERE user_id=? AND workflow_id=? AND workspace=? AND path=? ORDER BY created_at DESC,rowid DESC LIMIT ?)`,
		claims.UserID, workflow, workspace, path, claims.UserID, workflow, workspace, path, externalBuilderAuditHistoryLimit)
	if err != nil {
		return "", err
	}
	return id, tx.Commit()
}

func finishExternalBuilderEdit(id, status, detail string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := openExternalBuilderStore()
	if err != nil {
		return err
	}
	defer s.Close()
	_, err = s.db.ExecContext(ctx, `UPDATE external_builder_edits SET status=?,detail=? WHERE id=? AND status='prepared'`, status, detail, id)
	return err
}

func listExternalBuilderFileEdits(ctx context.Context, claims *UserClaims, workflow, workspace, path string) ([]externalBuilderEdit, error) {
	s, err := openExternalBuilderStore()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	rows, err := s.db.QueryContext(ctx, `SELECT id,operation_id,grant_id,workflow_id,tool,path,before_revision,after_revision,before_exists,before_content_available,before_size,after_size,status,detail,created_at
 FROM external_builder_edits WHERE user_id=? AND workflow_id=? AND workspace=? AND path=?
	 AND tool IN ('write_file','restore_file') AND created_at>=? ORDER BY created_at DESC LIMIT ?`, claims.UserID, workflow, workspace, path, time.Now().Add(-externalBuilderAuditRetention).UnixNano(), externalBuilderAuditHistoryLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	edits := []externalBuilderEdit{}
	for rows.Next() {
		var item externalBuilderEdit
		var grantID string
		var exists, available int
		var created int64
		if err := rows.Scan(&item.ID, &item.OperationID, &grantID, &item.WorkflowID, &item.Tool, &item.Path, &item.BeforeRevision, &item.AfterRevision, &exists, &available, &item.BeforeSize, &item.AfterSize, &item.Status, &item.Detail, &created); err != nil {
			return nil, err
		}
		item.ViaToken = "token:" + grantID
		item.BeforeExists = exists != 0
		item.Restorable = !item.BeforeExists || available != 0
		item.CreatedAt = time.Unix(0, created).UTC()
		edits = append(edits, item)
	}
	return edits, rows.Err()
}

func readExternalBuilderFileEdit(ctx context.Context, claims *UserClaims, workflow, workspace, path, id string) (externalBuilderEdit, error) {
	s, err := openExternalBuilderStore()
	if err != nil {
		return externalBuilderEdit{}, err
	}
	defer s.Close()
	var item externalBuilderEdit
	var exists, available int
	var created int64
	var grantID string
	err = s.db.QueryRowContext(ctx, `SELECT id,operation_id,grant_id,workflow_id,tool,path,before_revision,after_revision,before_content,before_exists,before_content_available,before_size,after_size,status,detail,created_at
 FROM external_builder_edits WHERE id=? AND user_id=? AND workflow_id=? AND workspace=? AND path=?
	 AND tool IN ('write_file','restore_file') AND status='completed' AND created_at>=?`, id, claims.UserID, workflow, workspace, path, time.Now().Add(-externalBuilderAuditRetention).UnixNano()).
		Scan(&item.ID, &item.OperationID, &grantID, &item.WorkflowID, &item.Tool, &item.Path, &item.BeforeRevision, &item.AfterRevision, &item.BeforeContent, &exists, &available, &item.BeforeSize, &item.AfterSize, &item.Status, &item.Detail, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return externalBuilderEdit{}, errors.New("file version not found")
	}
	item.BeforeExists = exists != 0
	item.Restorable = !item.BeforeExists || available != 0
	item.ViaToken = "token:" + grantID
	item.CreatedAt = time.Unix(0, created).UTC()
	if err == nil && !item.Restorable {
		return externalBuilderEdit{}, fmt.Errorf("%w of %d bytes", errExternalBuilderVersionNotRestorable, externalBuilderAuditContentCap)
	}
	return item, err
}

func externalBuilderPlanMutates(name string) bool {
	switch name {
	case "add_step", "manage_group", "manage_step_route", "change_step_type", "maintain_plan", "create_plan", "delete_plan_steps", "update_step", "update_step_config", "update_validation_schema", "update_variable":
		return true
	default:
		return false
	}
}

func auditExternalBuilderPlanTool(name string, run func(context.Context, map[string]interface{}) (string, error)) func(context.Context, map[string]interface{}) (string, error) {
	if !externalBuilderPlanMutates(name) {
		return run
	}
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		claims := GetUserFromContext(ctx)
		if claims == nil || claims.AccessToken == nil || claims.ExternalBuilderOperationID == "" {
			return "", errors.New("Builder plan edit lacks a bound operation")
		}
		op, err := readExternalBuilder(ctx, claims.ExternalBuilderOperationID)
		if err != nil || op.UserID != claims.UserID || op.GrantID != claims.AccessToken.ID {
			return "", errors.New("Builder plan edit binding changed")
		}
		path := "planning/plan.json"
		if step := externalArg(args, "step_id"); step != "" {
			path += "#step=" + step
		}
		id, err := prepareExternalBuilderEdit(ctx, claims, op.ID, op.WorkflowID, op.Workspace, name, path, "", "", "", "", false)
		if err != nil {
			return "", fmt.Errorf("Builder edit audit unavailable: %w", err)
		}
		result, runErr := run(ctx, args)
		status := "completed"
		if runErr != nil {
			status = "failed"
		}
		if err := finishExternalBuilderEdit(id, status, ""); err != nil {
			return result, errors.Join(runErr, fmt.Errorf("Builder edit audit update failed: %w", err))
		}
		return result, runErr
	}
}
