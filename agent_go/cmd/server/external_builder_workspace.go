package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

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
	release, lockErr := wf.LockWorkspace(ctx, filepath.Join(getWorkspaceDocsAbsPath(), filepath.FromSlash(rootPath)))
	if lockErr != nil {
		return wf.Result{}, lockErr
	}
	defer release()
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
	if before.Exists && before.Encoding != "utf-8" {
		return wf.Result{}, errors.New("Builder can only edit UTF-8 source files")
	}
	mode := os.FileMode(0644)
	if before.Exists {
		info, statErr := root.Stat(p)
		if statErr != nil {
			return wf.Result{}, statErr
		}
		mode = info.Mode().Perm()
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
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return wf.Result{}, err
	}
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
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.AccessToken == nil || claims.ExternalBuilderOperationID == "" {
		return wf.Result{}, errors.New("Builder file edit lacks a bound operation")
	}
	versionID, err := prepareExternalBuilderEdit(ctx, claims, claims.ExternalBuilderOperationID,
		strings.TrimPrefix(rootPath, "Workflow/"), rootPath, "write_file", p,
		before.Revision, wf.Revision([]byte(content)), before.Content, content, before.Exists)
	if err != nil {
		return wf.Result{}, fmt.Errorf("Builder edit audit unavailable: %w", err)
	}
	if err = root.Rename(tmp, p); err != nil {
		_ = finishExternalBuilderEdit(versionID, "failed", "replacement failed")
		return wf.Result{}, err
	}
	if err = finishExternalBuilderEdit(versionID, "completed", ""); err != nil {
		return wf.Result{}, fmt.Errorf("file was written but audit confirmation failed: %w", err)
	}
	return wf.Result{File: wf.File{Path: p, Exists: true, Content: content, Encoding: "utf-8", Revision: wf.Revision([]byte(content)), Size: int64(len(content))}}, nil
}

func (api *StreamingAPI) externalBuilderFileCall(w http.ResponseWriter, r *http.Request, name string, args map[string]interface{}, workflow DiscoveredWorkflow) {
	p, err := externalBuilderFilePath(externalArg(args, "path"), true)
	if err != nil || p == "." {
		externalError(w, 400, "invalid_path", "A public workflow source file path is required")
		return
	}
	claims := GetUserFromContext(r.Context())
	if name == "builder_file_history" {
		edits, err := listExternalBuilderFileEdits(r.Context(), claims, workflow.Manifest.ID, workflow.WorkspacePath, p)
		if err != nil {
			externalError(w, 503, "audit_unavailable", err.Error())
			return
		}
		externalJSON(w, map[string]any{"edits": edits})
		return
	}
	version, err := readExternalBuilderFileEdit(r.Context(), claims, workflow.Manifest.ID, workflow.WorkspacePath, p, externalArg(args, "edit_id"))
	if err != nil {
		if errors.Is(err, errExternalBuilderVersionNotRestorable) {
			externalError(w, 409, "restore_unavailable", err.Error())
			return
		}
		externalError(w, 404, "edit_not_found", "Completed Builder file edit not found")
		return
	}
	expected := externalArg(args, "expected_revision")
	if expected == "" {
		externalError(w, 400, "invalid_revision", "Current expected_revision is required")
		return
	}
	copy := *claims
	copy.ExternalBuilderOperationID = "restore-" + uuid.NewString()
	ctx := context.WithValue(r.Context(), UserContextKey, &copy)
	var result wf.Result
	if version.BeforeExists {
		result, err = externalBuilderWriteFile(ctx, workflow.WorkspacePath, p, version.BeforeContent, expected)
	} else {
		result, err = externalBuilderRemoveFile(ctx, workflow.WorkspacePath, p, expected)
	}
	if err != nil {
		externalError(w, 409, "restore_failed", err.Error())
		return
	}
	externalJSON(w, map[string]any{"restored": result, "from_edit_id": version.ID})
}

func externalBuilderRemoveFile(ctx context.Context, workspace, p, expected string) (wf.Result, error) {
	if err := ctx.Err(); err != nil {
		return wf.Result{}, err
	}
	if expected == "" {
		return wf.Result{}, errors.New("expected_revision is required")
	}
	rootPath, err := wf.CleanRelative(workspace)
	if err != nil || !strings.HasPrefix(rootPath, "Workflow/") || len(strings.Split(rootPath, "/")) != 2 {
		return wf.Result{}, errors.New("invalid workflow root")
	}
	if p, err = externalBuilderFilePath(p, true); err != nil {
		return wf.Result{}, err
	}
	release, lockErr := wf.LockWorkspace(ctx, filepath.Join(getWorkspaceDocsAbsPath(), filepath.FromSlash(rootPath)))
	if lockErr != nil {
		return wf.Result{}, lockErr
	}
	defer release()
	base, err := os.OpenRoot(getWorkspaceDocsAbsPath())
	if err != nil {
		return wf.Result{}, err
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
	before, err := externalScopedFile(root, p)
	if err != nil {
		return wf.Result{}, err
	}
	if !before.Exists || before.Revision != expected {
		return wf.Result{}, errors.New("file revision conflict; reread before restoring")
	}
	if before.Encoding != "utf-8" {
		return wf.Result{}, errors.New("Builder can only restore UTF-8 source files")
	}
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.AccessToken == nil || claims.ExternalBuilderOperationID == "" {
		return wf.Result{}, errors.New("Builder restore lacks a bound grant")
	}
	id, err := prepareExternalBuilderEdit(ctx, claims, claims.ExternalBuilderOperationID,
		strings.TrimPrefix(rootPath, "Workflow/"), rootPath, "restore_file", p,
		before.Revision, wf.MissingRevision, before.Content, "", true)
	if err != nil {
		return wf.Result{}, fmt.Errorf("Builder edit audit unavailable: %w", err)
	}
	if err = root.Remove(p); err != nil {
		_ = finishExternalBuilderEdit(id, "failed", "remove failed")
		return wf.Result{}, err
	}
	if err = finishExternalBuilderEdit(id, "completed", ""); err != nil {
		return wf.Result{}, fmt.Errorf("file was removed but audit confirmation failed: %w", err)
	}
	return wf.Result{File: wf.File{Path: p, Revision: wf.MissingRevision}}, nil
}
