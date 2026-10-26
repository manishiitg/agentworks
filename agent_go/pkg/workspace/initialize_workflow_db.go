package workspace

import (
	"context"
	"encoding/json"
	"fmt"
)

type InitializeWorkflowDBParams struct {
	DBPath     string   `json:"db_path"`
	Migrations []string `json:"migrations"`
	// MigrationFile is the optional source filename this batch came from,
	// recorded in the durable schema_migration_log ledger for audit. A
	// caller applying a fixed, compiled-in migration list (a product
	// runtime, not a file-based caller) may leave this empty.
	MigrationFile string `json:"migration_file,omitempty"`
}

// InitializeWorkflowDBResult reports the backup snapshot path when the
// applied migrations included a destructive statement (DROP, RENAME, DROP
// COLUMN). Empty when every statement was purely additive/idempotent-create,
// since nothing was at risk and no snapshot was taken.
type InitializeWorkflowDBResult struct {
	BackupPath string `json:"backup_path,omitempty"`
}

// InitializeWorkflowDB invokes the token-protected generic managed-database
// initializer. Product runtimes call this directly with a fixed, compiled-in
// migration list once per workspace. It is
// also the backing call for the narrow apply_workflow_db_migration agent
// tool (virtual-tools/workflow_db_tools.go), which restricts callers to a
// workflow's own pre-authored db/migrations/*.sql file and a read-write
// trusted session -- agents never reach this client method with inline SQL.
func (c *Client) InitializeWorkflowDB(ctx context.Context, params InitializeWorkflowDBParams) (InitializeWorkflowDBResult, error) {
	if params.DBPath == "" {
		return InitializeWorkflowDBResult{}, fmt.Errorf("db_path is required")
	}
	body, err := c.request(ctx, "POST", "/api/db/initialize", params)
	if err != nil {
		return InitializeWorkflowDBResult{}, err
	}
	var response APIResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return InitializeWorkflowDBResult{}, fmt.Errorf("decode database initialization response: %w", err)
	}
	if !response.Success {
		return InitializeWorkflowDBResult{}, fmt.Errorf("database initialization failed: %s", response.Error)
	}
	var result InitializeWorkflowDBResult
	if len(response.Data) > 0 {
		if err := json.Unmarshal(response.Data, &result); err != nil {
			return InitializeWorkflowDBResult{}, fmt.Errorf("decode database initialization result: %w", err)
		}
	}
	noteWorkflowDBWrite(params.DBPath)
	return result, nil
}
