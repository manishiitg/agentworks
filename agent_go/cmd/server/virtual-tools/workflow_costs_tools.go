package virtualtools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const WorkflowCostsToolCategory = "workflow_costs"

// workflowCostsDescribeSQL lists the cost_events table's own columns. There is
// exactly one table in this database, so action=describe never needs a table
// argument the way query_workflow_db's does.
const workflowCostsDescribeSQL = "SELECT name, type, sql FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name"

func workflowCostsQueryToolDefinition() llmtypes.Tool {
	return llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name:        "query_workflow_costs",
		Description: "Read this workflow's own LLM/tool cost ledger (costs/costs.sqlite) -- the per-run, per-step, per-phase, and per-agent-item cost and token breakdown for exactly this workflow. Pass sql to run one read-only SELECT/WITH/EXPLAIN statement against the cost_events table; action=describe lists its columns. The backend resolves the database to this workflow's own ledger; never pass a path. This is a separate, workflow-scoped ledger from the global human-facing Cost Analysis dashboard -- it only ever contains this workflow's own cost events, and only events recorded since this ledger started being written (PLAT-184). The cost_events table's most useful columns for a cost/model-fitness review are: occurred_at, run_id, execution_id, phase (an execution phase like \"execution_only\"/\"reflection\", or \"item:<id>\" for one agent item), requested_provider, requested_model_id, effective_provider, effective_model_id, prompt_tokens, completion_tokens, reasoning_tokens, cache_read_tokens, cache_write_tokens, total_cost_usd, billing_basis (\"unpriced\" means a call produced zero billable tokens, e.g. a failed provider call -- not a tracking bug), tool_name.",
		Parameters: llmtypes.NewParameters(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":   map[string]any{"type": "string", "enum": []string{"describe", "query"}, "description": "Optional. Omit it and pass sql to run a statement. describe lists the cost_events table definition."},
				"sql":      map[string]any{"type": "string", "description": "One SELECT, read-only WITH, or EXPLAIN statement against cost_events. This is the normal way to use the tool -- for example, group by phase to get per-step/per-item cost and token totals."},
				"params":   map[string]any{"type": "array", "description": "Optional positional values for ? placeholders in sql."},
				"query":    map[string]any{"type": "string", "description": "Compatibility alias for sql. Prefer sql. If both are supplied they must be identical."},
				"max_rows": map[string]any{"type": "integer", "minimum": 1, "maximum": 1000, "description": "Maximum rows to return. Default 500."},
			},
		}),
	}}
}

// CreateWorkflowCostsToolRegistry creates the read-only per-workspace cost
// ledger tool (PLAT-184). It is shared by workflow step execution, Pulse's
// background reviewer sessions, and the interactive Workflow Builder --
// mirroring CreateWorkflowDBToolRegistry, but resolved to costs/costs.sqlite
// instead of db/db.sqlite, and query-only: cost events are only ever written
// by the cost observer itself, never by an agent.
func CreateWorkflowCostsToolRegistry(workspaceURL, userID, fallbackSessionID string) WorkflowDBToolRegistry {
	if strings.TrimSpace(workspaceURL) == "" {
		workspaceURL = getWorkspaceAPIURL()
	}
	client := workspace.NewClient(
		workspaceURL,
		workspace.WithUserID(userID),
		workspace.WithExtraEnv(getMCPExtraEnv(fallbackSessionID)),
	)
	queryExecutor := func(ctx context.Context, args map[string]any) (string, error) {
		costsPath, err := resolveCurrentWorkflowCostsPath(ctx, fallbackSessionID)
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
		if action == "" {
			if querySQL != "" {
				action = "query"
			} else {
				action = "describe"
			}
		}
		switch action {
		case "describe":
			sqlText = workflowCostsDescribeSQL
		case "query":
			sqlText = querySQL
			if sqlText == "" {
				return "", fmt.Errorf("sql (or its query alias) is required for action=query")
			}
		default:
			return "", fmt.Errorf(
				"pass sql to run a read-only statement against cost_events, or action=\"describe\" to list its schema. Received top-level keys %v",
				sortedArgumentKeys(args),
			)
		}
		result, err := client.QueryAuthorizedWorkflowDB(ctx, workspace.QueryWorkflowDBParams{DBPath: costsPath, SQL: sqlText, Params: workflowDBParams(args), MaxRows: maxRows})
		if err != nil {
			if workflowCostsLedgerMissing(err) {
				return "", fmt.Errorf("this workflow has no cost events recorded yet (no LLM/tool call attributable to it has completed since PLAT-184 shipped): %w", err)
			}
			if workflowDBUnrecognizedSigilPattern.MatchString(err.Error()) {
				return "", workflowDBUnquotedBindSigilHint(err)
			}
			return "", workflowDBSchemaHintError(ctx, err, sqlText, func(hintCtx context.Context, hintSQL string) (workspace.QueryWorkflowDBResult, error) {
				return client.QueryAuthorizedWorkflowDB(hintCtx, workspace.QueryWorkflowDBParams{DBPath: costsPath, SQL: hintSQL, MaxRows: workflowDBDescribeRows})
			})
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return "", fmt.Errorf("encode workflow costs query result: %w", err)
		}
		return string(encoded), nil
	}

	tools := []llmtypes.Tool{workflowCostsQueryToolDefinition()}
	return WorkflowDBToolRegistry{
		Tools: tools,
		Executors: map[string]func(context.Context, map[string]any) (string, error){
			"query_workflow_costs": queryExecutor,
		},
		Categories: map[string]string{
			"query_workflow_costs": WorkflowCostsToolCategory,
		},
	}
}

func WorkflowCostsToolNames() map[string]bool {
	return map[string]bool{"query_workflow_costs": true}
}

// workflowCostsLedgerMissing recognizes the workspace API's "file does not
// exist yet" error for a workflow that has never had a cost event recorded
// against its own ledger, so the tool can explain that plainly instead of
// surfacing a raw "database file not found" error.
func workflowCostsLedgerMissing(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "database file not found")
}

func resolveCurrentWorkflowCostsPath(ctx context.Context, fallbackSessionID string) (string, error) {
	sessionID, cfg, err := resolveWorkflowDBSession(ctx, fallbackSessionID)
	if err != nil {
		return "", err
	}
	// Same owning-workflow resolution as query_workflow_db, so attached
	// context never makes the ledger ambiguous and the two tools always agree.
	folder, err := resolveWorkflowWorkspaceFolder(sessionID, cfg)
	if err == nil {
		return workflowCostsPathFromCandidate(folder), nil
	}
	if strings.Contains(err.Error(), "ambiguous") {
		return "", fmt.Errorf("workflow cost ledger context is ambiguous for session %q", sessionID)
	}
	return "", fmt.Errorf("workflow cost ledger context is unavailable for session %q: this tool is only available inside a workflow's own folder", sessionID)
}

// workflowCostsPathFromCandidate mirrors workflowDBPathFromCandidate's
// "Workflow/<name>" extraction, but joins costledger's own relative path
// constant instead of db/db.sqlite -- the two tools resolve two different
// databases under the same workflow folder, from the same session context.
func workflowCostsPathFromCandidate(candidate string) string {
	workspacePath := workflowDBWorkspacePathFromCandidate(candidate)
	if workspacePath == "" {
		return ""
	}
	return workspacePath + "/" + costledger.WorkspaceCostsRelativePath
}
