package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Edits live in server-owned state. Prepared rows are written before a tool
// mutates anything, so a crash cannot turn a change into an invisible event.
// File rows retain the previous and resulting text for exact restoration.
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
	Status         string    `json:"status"`
	Detail         string    `json:"detail,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	BeforeContent  string    `json:"-"`
	AfterContent   string    `json:"-"`
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
	_, err = s.db.ExecContext(ctx, `INSERT INTO external_builder_edits
 (id,operation_id,user_id,grant_id,workflow_id,workspace,tool,path,before_revision,after_revision,before_content,after_content,before_exists,status,created_at)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,'prepared',?)`, id, operationID, claims.UserID, claims.AccessToken.ID, workflow, workspace, tool, path, beforeRevision, afterRevision, beforeContent, afterContent, exists, time.Now().UTC().UnixNano())
	return id, err
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
	rows, err := s.db.QueryContext(ctx, `SELECT id,operation_id,grant_id,workflow_id,tool,path,before_revision,after_revision,before_exists,status,detail,created_at
 FROM external_builder_edits WHERE user_id=? AND workflow_id=? AND workspace=? AND path=?
 AND tool IN ('write_file','restore_file') ORDER BY created_at DESC LIMIT 100`, claims.UserID, workflow, workspace, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	edits := []externalBuilderEdit{}
	for rows.Next() {
		var item externalBuilderEdit
		var grantID string
		var exists int
		var created int64
		if err := rows.Scan(&item.ID, &item.OperationID, &grantID, &item.WorkflowID, &item.Tool, &item.Path, &item.BeforeRevision, &item.AfterRevision, &exists, &item.Status, &item.Detail, &created); err != nil {
			return nil, err
		}
		item.ViaToken = "token:" + grantID
		item.BeforeExists = exists != 0
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
	var exists int
	var created int64
	var grantID string
	err = s.db.QueryRowContext(ctx, `SELECT id,operation_id,grant_id,workflow_id,tool,path,before_revision,after_revision,before_content,after_content,before_exists,status,detail,created_at
 FROM external_builder_edits WHERE id=? AND user_id=? AND workflow_id=? AND workspace=? AND path=?
 AND tool IN ('write_file','restore_file') AND status='completed'`, id, claims.UserID, workflow, workspace, path).
		Scan(&item.ID, &item.OperationID, &grantID, &item.WorkflowID, &item.Tool, &item.Path, &item.BeforeRevision, &item.AfterRevision, &item.BeforeContent, &item.AfterContent, &exists, &item.Status, &item.Detail, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return externalBuilderEdit{}, errors.New("file version not found")
	}
	item.BeforeExists = exists != 0
	item.ViaToken = "token:" + grantID
	item.CreatedAt = time.Unix(0, created).UTC()
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
