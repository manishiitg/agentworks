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
// 1.0.45 migration converts them to the agentworks_db helper). SQL validity is
// decided by the managed tools at execution. The version stamp runs this same scan.
func CreateScriptDBToolRegistry(fallbackSessionID string) ScriptDBToolRegistry {
	executor := func(ctx context.Context, args map[string]any) (string, error) {
		workspacePath, err := ResolveWorkflowWorkspaceFolder(ctx, fallbackSessionID)
		if err != nil {
			return "", err
		}
		codePath, err := fsutil.WorkspacePath(fsutil.WorkspaceDocsRoot(), filepath.ToSlash(filepath.Join(workspacePath, "code")))
		if err != nil {
			return "", err
		}
		findings, scanned, err := scriptdb.ScanDir(codePath)
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
			result["next"] = "Convert each script to the agentworks_db helper. Use execute or transaction for CREATE TABLE/INDEX IF NOT EXISTS; ALTER and DROP are refused by the server. Test files (test_*.py) are not scanned."
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return "", fmt.Errorf("encode result: %w", err)
		}
		return string(encoded), nil
	}
	tool := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name:        "scan_workflow_script_db_usage",
		Description: "List this workflow's scripts under code/ that still open the database themselves ($DB_PATH, db.sqlite: raw_db). Scripts reach the database through the built-in agentworks_db helper (the managed query/mutate tools); contract 1.0.45 is stamped only when this scan is clean. Read-only; test files are not scanned.",
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
