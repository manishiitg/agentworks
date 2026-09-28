package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/google/uuid"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// File edits from this origin deliberately use a small managed surface, rather
// than shell commands or the generic diff endpoint. These read helpers already
// reject symlinks and private runtime files, locally and on remote deployments.
func (api *StreamingAPI) registerExternalBuilderWorkspaceTools(registrar definitionToolRegistrar, workspace string, claims *UserClaims) error {
	if claims == nil || claims.ExternalBuilderOperationID == "" {
		return nil
	}
	for _, name := range []string{"read_file", "list_files", "search_files", "write_file"} {
		tool := name
		props := map[string]interface{}{"path": map[string]interface{}{"type": "string", "description": "Path relative to this workflow, for example code/task.py. Other workflows, private runtime storage, and databases are inaccessible."}}
		required := []string{"path"}
		description := "Read a bounded workflow file and its revision."
		switch tool {
		case "list_files", "search_files":
			description = "List or search this workflow's public files. Use next_offset to page."
			props["offset"] = map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 10000}
			props["limit"] = map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 200}
			props["depth"] = map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 8}
			if tool == "search_files" {
				props["query"] = map[string]interface{}{"type": "string"}
				required = append(required, "query")
			}
		case "write_file":
			description = "Create or replace a workflow source file using the revision returned by read_file (or missing for a new file). Stale revisions fail: reread and reconcile before retrying. Plans use typed plan tools. Requires the local workspace mount."
			props["content"] = map[string]interface{}{"type": "string", "maxLength": wf.MaxFileBytes}
			props["expected_revision"] = map[string]interface{}{"type": "string", "minLength": 1}
			required = append(required, "content", "expected_revision")
		}
		schema := map[string]interface{}{"type": "object", "properties": props, "required": required, "additionalProperties": false}
		err := registrar.RegisterCustomTool(tool, description, schema, func(ctx context.Context, args map[string]interface{}) (string, error) {
			// The common tool binder revalidates the persisted operation grant. Keep
			// the filesystem scope captured from its authorized workflow, never args.
			p, err := externalBuilderFilePath(externalArg(args, "path"), tool == "write_file")
			if err != nil {
				return "", err
			}
			var result wf.Result
			if tool == "write_file" {
				result, err = externalBuilderWriteFile(ctx, workspace, p, externalArg(args, "content"), externalArg(args, "expected_revision"))
			} else {
				operation := map[string]string{"read_file": "read", "list_files": "list", "search_files": "search"}[tool]
				result, err = externalFileRequest(ctx, wf.Request{Root: workspace, Path: p, Operation: operation, Query: externalArg(args, "query"), Offset: externalInt(args, "offset", 0), Limit: externalInt(args, "limit", 100), Depth: externalInt(args, "depth", 4)})
				if err == nil && operation != "read" {
					visible := result.Entries[:0]
					for _, entry := range result.Entries {
						if _, check := externalBuilderFilePath(entry.Path, false); check == nil {
							visible = append(visible, entry)
						}
					}
					result.Entries = visible
				}
			}
			if err != nil {
				return "", err
			}
			encoded, err := json.Marshal(result)
			return string(encoded), err
		}, "workspace_tools")
		if err != nil {
			return err
		}
	}
	return nil
}

func externalBuilderFilePath(input string, write bool) (string, error) {
	p, err := wf.CleanRelative(input)
	if err != nil {
		return "", err
	}
	if wf.Private(p) || strings.EqualFold(strings.Split(p, "/")[0], "db") {
		return "", errors.New("private workflow path")
	}
	if write && (p == "." || strings.EqualFold(p, "workflow.json") || strings.EqualFold(strings.Split(p, "/")[0], "planning")) {
		return "", errors.New("this file requires a typed workflow tool")
	}
	return p, nil
}

var externalBuilderFileWriteMu sync.Mutex

// Revision validation and replacement are serialized for MCP file edits. The
// normal Builder single-runner boundary prevents parallel Builder turns; a
// browser's independent direct file editor should reread after a conflict.
func externalBuilderWriteFile(ctx context.Context, workspace, p, content, expected string) (wf.Result, error) {
	if err := ctx.Err(); err != nil {
		return wf.Result{}, err
	}
	var err error
	if p, err = externalBuilderFilePath(p, true); err != nil {
		return wf.Result{}, err
	}
	if expected == "" {
		return wf.Result{}, errors.New("expected_revision is required")
	}
	if len(content) > wf.MaxFileBytes {
		return wf.Result{}, errors.New("file exceeds size limit")
	}
	rootPath, err := wf.CleanRelative(workspace)
	if err != nil || !strings.HasPrefix(rootPath, "Workflow/") || len(strings.Split(rootPath, "/")) != 2 {
		return wf.Result{}, errors.New("invalid workflow root")
	}
	externalBuilderFileWriteMu.Lock()
	defer externalBuilderFileWriteMu.Unlock()
	base, err := os.OpenRoot(getWorkspaceDocsAbsPath())
	if err != nil {
		return wf.Result{}, fmt.Errorf("external Builder file writes require the local workspace mount: %w", err)
	}
	defer base.Close()
	if err = externalNoSymlinks(base, rootPath); err != nil {
		return wf.Result{}, err
	}
	root, err := base.OpenRoot(rootPath)
	if err != nil {
		return wf.Result{}, err
	}
	defer root.Close()
	if err = externalNoSymlinks(root, p); err != nil {
		return wf.Result{}, err
	}
	before, err := externalScopedFile(root, p)
	if err != nil {
		return wf.Result{}, err
	}
	if before.Revision != expected {
		return wf.Result{}, errors.New("file revision conflict; reread and reconcile the latest contents")
	}
	if err = root.MkdirAll(path.Dir(p), 0755); err != nil {
		return wf.Result{}, err
	}
	tmp := path.Join(path.Dir(p), ".builder-"+uuid.NewString())
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return wf.Result{}, err
	}
	defer root.Remove(tmp)
	if _, err = f.WriteString(content); err != nil {
		f.Close()
		return wf.Result{}, err
	}
	if err = f.Close(); err != nil {
		return wf.Result{}, err
	}
	// Check again after staging, so direct-editor changes during staging are
	// detected rather than silently replacing the version used by the model.
	current, err := externalScopedFile(root, p)
	if err != nil {
		return wf.Result{}, err
	}
	if current.Revision != expected {
		return wf.Result{}, errors.New("file revision conflict; reread and reconcile the latest contents")
	}
	if err = ctx.Err(); err != nil {
		return wf.Result{}, err
	}
	if err = root.Rename(tmp, p); err != nil {
		return wf.Result{}, err
	}
	return wf.Result{File: wf.File{Path: p, Exists: true, Content: content, Encoding: "utf-8", Revision: wf.Revision([]byte(content)), Size: int64(len(content))}}, nil
}
