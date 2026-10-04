package virtualtools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/scriptdb"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const ScriptDBToolCategory = "workflow_script_db"

// ScriptDBToolRegistry mirrors the other Create*ToolRegistry bundles.
type ScriptDBToolRegistry struct {
	Tools      []llmtypes.Tool
	Executors  map[string]func(context.Context, map[string]any) (string, error)
	Categories map[string]string
}

// CreateScriptDBToolRegistry exposes scan_workflow_script_db_usage: the list of
// this workflow's scripts that still open the database themselves (the contract
// 1.0.45 migration converts them to the agentworks_db helper) and the schema
// statements found in scripts. It is read-only; the version stamp runs the same scan.
func CreateScriptDBToolRegistry(fallbackSessionID string) ScriptDBToolRegistry {
	executor := func(ctx context.Context, args map[string]any) (string, error) {
		workspacePath, err := ResolveWorkflowWorkspaceFolder(ctx, fallbackSessionID)
		if err != nil {
			return "", err
		}
		findings, scanned, err := scriptdb.ScanDir(filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(workspacePath), "code"))
		if err != nil {
			return "", fmt.Errorf("scan the scripts under code/: %w", err)
		}
		if findings == nil {
			findings = []scriptdb.Finding{}
		}
		result := map[string]any{
			"workflow":         workspacePath,
			"scripts_scanned":  scanned,
			"needs_conversion": findings,
			"clean":            len(findings) == 0,
		}
		if len(findings) > 0 {
			result["next"] = "Convert each script to the agentworks_db helper; move each schema statement (ddl) into a db/migrations/ file applied with apply_workflow_db_migration. Test files (test_*.py) are not scanned."
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return "", fmt.Errorf("encode result: %w", err)
		}
		return string(encoded), nil
	}
	tool := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name:        "scan_workflow_script_db_usage",
		Description: "List this workflow's scripts under code/ that still open the database themselves (import sqlite3, $DB_PATH, db.sqlite: raw_db) and any schema statement (CREATE/ALTER/DROP) inside a script (ddl). Scripts reach the database through the built-in agentworks_db helper (the managed query/mutate tools); contract 1.0.45 is stamped only when this scan is clean. Read-only; test files are not scanned.",
		Parameters: llmtypes.NewParameters(map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		}),
	}}
	return ScriptDBToolRegistry{
		Tools:      []llmtypes.Tool{tool},
		Executors:  map[string]func(context.Context, map[string]any) (string, error){"scan_workflow_script_db_usage": executor},
		Categories: map[string]string{"scan_workflow_script_db_usage": ScriptDBToolCategory},
	}
}
