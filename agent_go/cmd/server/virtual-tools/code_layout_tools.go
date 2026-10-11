package virtualtools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const CodeLayoutToolCategory = "workflow_code_layout"

// CodeLayoutToolRegistry mirrors the shape of the other Create*ToolRegistry
// bundles in this package (see WorkflowDBToolRegistry).
type CodeLayoutToolRegistry struct {
	Tools      []llmtypes.Tool
	Executors  map[string]func(context.Context, map[string]any) (string, error)
	Categories map[string]string
}

// CreateCodeLayoutToolRegistry exposes the one supported way to switch an
// existing workflow between the legacy learnings/<step-id>/main.py layout and
// the code/<step-id>/main.py layout (PLAT-298, code-authoring.md's
// "Deliberate migration to code/"). setVersion is the privileged manifest
// write this package cannot perform itself -- it lives in cmd/server (see
// server.SetWorkflowCodeLayoutVersion), which this package cannot import
// without creating an import cycle, since cmd/server already imports this
// package to register its other tools.
func CreateCodeLayoutToolRegistry(fallbackSessionID string, setVersion func(ctx context.Context, workspacePath string, version int) error) CodeLayoutToolRegistry {
	executor := func(ctx context.Context, args map[string]any) (string, error) {
		raw, ok := args["code_layout_version"]
		if !ok {
			return "", fmt.Errorf("code_layout_version (0 or 1) is required")
		}
		versionFloat, ok := raw.(float64)
		if !ok {
			return "", fmt.Errorf("code_layout_version must be the integer 0 or 1")
		}
		version := int(versionFloat)
		if version != 0 && version != 1 {
			return "", fmt.Errorf("code_layout_version must be 0 or 1, got %d", version)
		}
		workspacePath, err := ResolveWorkflowWorkspaceFolder(ctx, fallbackSessionID)
		if err != nil {
			return "", err
		}
		if err := setVersion(ctx, workspacePath, version); err != nil {
			return "", err
		}
		encoded, err := json.Marshal(map[string]any{
			"code_layout_version": version,
			"note":                "the runtime resolves canonical step source from this version on its very next execution; nothing under learnings/ was deleted, so calling this again with the previous value is a safe, instant rollback",
		})
		if err != nil {
			return "", fmt.Errorf("encode result: %w", err)
		}
		return string(encoded), nil
	}

	tool := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name:        "set_code_layout_version",
		Description: "The ONLY supported way to switch this workflow's persisted code_layout_version between 0 (legacy learnings/<step-id>/main.py) and 1 (code/<step-id>/main.py). The ordinary workflow-configuration update tool silently preserves the existing value and ignores any change to it -- this tool is the deliberate, separate override for an authorized migration. Read code-authoring.md's \"Deliberate migration to code/\" section before calling this. Before calling with code_layout_version=1: stage a complete code/<step-id>/main.py (and its script_metadata.json) yourself, for every regular-type step in the plan, using ordinary file tools -- this tool does not move, copy, or verify any script itself, and the runtime starts resolving canonical source from code/ for every such step immediately on the next execution, with no execution-copy fallback for anything left only in learnings/. Nothing under learnings/ is ever deleted by this call, so calling it again with the previous value is a safe, instant rollback if something is wrong after the switch.",
		Parameters: llmtypes.NewParameters(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code_layout_version": map[string]any{
					"type":        "integer",
					"enum":        []int{0, 1},
					"description": "1 to switch this workflow to the code/ layout, 0 to roll back to the legacy learnings/ layout.",
				},
			},
			"required": []string{"code_layout_version"},
		}),
	}}

	return CodeLayoutToolRegistry{
		Tools: []llmtypes.Tool{tool},
		Executors: map[string]func(context.Context, map[string]any) (string, error){
			"set_code_layout_version": executor,
		},
		Categories: map[string]string{
			"set_code_layout_version": CodeLayoutToolCategory,
		},
	}
}
