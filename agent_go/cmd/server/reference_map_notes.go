package server

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// wrapExecutorsWithReferenceMapNotes (PLAT-561): when the workflow Builder
// writes a KB note, soul.md, the evaluation plan or a learnings file with
// diff_patch_workspace_file, the response lists the references that file now
// carries that no longer resolve. A report only: the write's outcome is
// unchanged, and files outside the workflow are not checked.
func wrapExecutorsWithReferenceMapNotes(executors map[string]func(context.Context, map[string]interface{}) (string, error), workflowFolder string) map[string]func(context.Context, map[string]interface{}) (string, error) {
	workflowFolder = strings.Trim(filepath.ToSlash(filepath.Clean(strings.TrimSpace(workflowFolder))), "/")
	execute, ok := executors["diff_patch_workspace_file"]
	if !ok || workflowFolder == "" || workflowFolder == "." {
		return executors
	}
	wrapped := make(map[string]func(context.Context, map[string]interface{}) (string, error), len(executors))
	for name, executor := range executors {
		wrapped[name] = executor
	}
	wrapped["diff_patch_workspace_file"] = func(ctx context.Context, args map[string]interface{}) (string, error) {
		targets := workspace.DiffPatchTargetPaths(args)
		out, err := execute(ctx, args)
		if err != nil {
			return out, err
		}
		var rels []string
		for _, target := range targets {
			if rel, ok := referenceMapWorkflowRelative(workflowFolder, target); ok {
				rels = append(rels, rel)
			}
		}
		if len(rels) == 0 {
			return out, nil
		}
		return out + step_based_workflow.ReferenceNotesForFiles(workflowFolder, rels), nil
	}
	return wrapped
}

// referenceMapWorkflowRelative maps a tool path (docs-relative, absolute, or
// relative to the workflow) to a workflow-relative path the map tracks.
func referenceMapWorkflowRelative(workflowFolder, target string) (string, bool) {
	p := filepath.ToSlash(strings.TrimSpace(target))
	for _, prefix := range []string{filepath.ToSlash(fsutil.WorkspaceDocsRoot()), filepath.ToSlash(step_based_workflow.GetPromptDocsRoot()), "/app/workspace-docs"} {
		if prefix != "" && strings.HasPrefix(p, prefix+"/") {
			p = strings.TrimPrefix(p, prefix+"/")
			break
		}
	}
	p = strings.TrimPrefix(p, "./")
	if strings.HasPrefix(p, workflowFolder+"/") {
		p = strings.TrimPrefix(p, workflowFolder+"/")
	} else if ref, _ := workspaceref.Parse(p); filepath.IsAbs(p) || strings.HasPrefix(p, "Workflow/") || ref.HasOwner() {
		return "", false
	}
	return p, step_based_workflow.ReferenceMapTracksFile(p)
}
