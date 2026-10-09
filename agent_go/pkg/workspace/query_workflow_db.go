package workspace

import (
	"context"
	"encoding/json"
	"fmt"
)

// QueryWorkflowDBParams holds a read-only SQL query against a workflow's SQLite DB.
type QueryWorkflowDBParams struct {
	// DBPath is the workspace-relative path to the SQLite file, e.g.
	// "Workflow/<name>/db/db.sqlite".
	DBPath string `json:"db_path"`
	// SQL is the read-only query to run.
	SQL string `json:"sql"`
	// Params binds positional values for ? placeholders, exactly like mutations.
	Params []interface{} `json:"params,omitempty"`
	// PrepareOnly validates schema and syntax without executing or requiring bindings.
	PrepareOnly bool `json:"prepare_only,omitempty"`
	// MaxRows bounds returned rows. Zero uses the workspace service default.
	MaxRows int `json:"max_rows,omitempty"`
}

type QueryWorkflowDBResult struct {
	Columns   []string                 `json:"columns"`
	Rows      []map[string]interface{} `json:"rows"`
	Truncated bool                     `json:"truncated,omitempty"`
	// NextOffset is the offset to pass to read the next page when Truncated.
	NextOffset int `json:"next_offset,omitempty"`
}

// queryAPIResponse mirrors the workspace /api/query envelope.
type queryAPIResponse struct {
	Success bool                  `json:"success"`
	Message string                `json:"message,omitempty"`
	Data    QueryWorkflowDBResult `json:"data,omitempty"`
	Error   string                `json:"error,omitempty"`
}

// QueryWorkflowDB runs a read-only SQL query against a workflow's db/db.sqlite via
// the workspace POST /api/query endpoint and returns the result rows as objects
// keyed by column name. The server opens a WAL-capable, query-only connection.
func (c *Client) QueryWorkflowDB(ctx context.Context, params QueryWorkflowDBParams) ([]map[string]interface{}, error) {
	// Read operation against the db file — validate the path against the read guard.
	if err := c.ValidatePathWithContext(ctx, params.DBPath, false); err != nil {
		return nil, err
	}
	result, err := c.queryWorkflowDB(ctx, params)
	if err != nil {
		return nil, err
	}
	return result.Rows, nil
}

// QueryAuthorizedWorkflowDB is used only by a server-registered database tool
// after trusted tool exposure has already authorized the current workflow role.
// It deliberately bypasses raw-file FolderGuard validation because managed
// agents hard-deny db.sqlite* while the backend tool remains their safe access
// path.
func (c *Client) QueryAuthorizedWorkflowDB(ctx context.Context, params QueryWorkflowDBParams) (QueryWorkflowDBResult, error) {
	return c.queryWorkflowDB(ctx, params)
}

func (c *Client) queryWorkflowDB(ctx context.Context, params QueryWorkflowDBParams) (QueryWorkflowDBResult, error) {

	respBody, err := c.request(ctx, "POST", "/api/query", params)
	if err != nil {
		return QueryWorkflowDBResult{}, err
	}

	var apiResp queryAPIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return QueryWorkflowDBResult{}, fmt.Errorf("failed to parse query response: %w", err)
	}
	if !apiResp.Success {
		if apiResp.Error != "" {
			return QueryWorkflowDBResult{}, fmt.Errorf("query failed: %s", apiResp.Error)
		}
		return QueryWorkflowDBResult{}, fmt.Errorf("query failed: %s", apiResp.Message)
	}
	return apiResp.Data, nil
}

type WorkflowDBMutationStatement struct {
	SQL    string        `json:"sql"`
	Params []interface{} `json:"params,omitempty"`
	// ParamSets runs SQL once per entry inside the same transaction (excludes Params).
	ParamSets [][]interface{} `json:"param_sets,omitempty"`
}

type MutateWorkflowDBParams struct {
	DBPath     string                        `json:"db_path"`
	Statements []WorkflowDBMutationStatement `json:"statements"`
}

type WorkflowDBMutationStatementResult struct {
	RowsAffected int64 `json:"rows_affected"`
	LastInsertID int64 `json:"last_insert_id,omitempty"`
}

type MutateWorkflowDBResult struct {
	Results           []WorkflowDBMutationStatementResult `json:"results"`
	TotalRowsAffected int64                               `json:"total_rows_affected"`
}

type mutationAPIResponse struct {
	Success bool                   `json:"success"`
	Message string                 `json:"message,omitempty"`
	Data    MutateWorkflowDBResult `json:"data,omitempty"`
	Error   string                 `json:"error,omitempty"`
}

// MutateAuthorizedWorkflowDB is used only by the conditionally registered
// mutation tool. The trusted registration is the capability check; the
// workspace service independently requires its internal API token.
func (c *Client) MutateAuthorizedWorkflowDB(ctx context.Context, params MutateWorkflowDBParams) (MutateWorkflowDBResult, error) {
	respBody, err := c.request(ctx, "POST", "/api/mutate", params)
	if err != nil {
		return MutateWorkflowDBResult{}, err
	}
	var apiResp mutationAPIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return MutateWorkflowDBResult{}, fmt.Errorf("failed to parse mutation response: %w", err)
	}
	if !apiResp.Success {
		if apiResp.Error != "" {
			return MutateWorkflowDBResult{}, fmt.Errorf("mutation failed: %s", apiResp.Error)
		}
		return MutateWorkflowDBResult{}, fmt.Errorf("mutation failed: %s", apiResp.Message)
	}
	noteWorkflowDBWrite(params.DBPath)
	return apiResp.Data, nil
}
