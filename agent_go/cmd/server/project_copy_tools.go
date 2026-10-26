package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strings"

	"github.com/google/uuid"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

const projectCopyMaxFiles = 64
const projectCopyMaxBytes = 16 << 20

func projectCopyDefinition() externalTool {
	return externalTool{Name: "copy_project_files", Description: "Copy a file or folder from a project you can read into one you own or edit. Private chats, secret stores and symlinks are excluded. Existing files are not replaced. At most 64 files, 2 MiB each and 16 MiB total; choose a smaller folder if it exceeds these limits.", mutates: true, InputSchema: map[string]any{
		"type": "object", "additionalProperties": false, "required": []any{"source_project", "source_path", "target_project", "target_path"},
		"properties": map[string]any{
			"source_project": externalString("Workflow/Flow ID, or work:<Crew ID>, from your accessible project list."),
			"source_path":    externalString("File or folder path relative to the source project."),
			"target_project": externalString("Destination Workflow/Flow ID, or work:<Crew ID>, that you own or edit."),
			"target_path":    externalString("New destination file or folder path relative to the target project."),
		},
	}}
}

func projectCopyAvailable(c *UserClaims) bool {
	if c == nil || c.UserID == "" {
		return false
	}
	a := userAccessForClaims(c)
	if a.Disabled || !a.CanEdit {
		return false
	}
	if c.AccessToken == nil {
		return true
	}
	t := c.AccessToken
	return (t.Allows("files:read") || t.Allows("crews:read")) && (t.BuilderAccess() || t.RelayBuilderAccess() || t.Allows("crews:write"))
}

// Resolve both sides independently. A visible source never lends its owner's
// write access, and a target grant never makes an inaccessible source readable.
func (api *StreamingAPI) projectCopyRoot(ctx context.Context, project string, write bool) (string, error) {
	c := GetUserFromContext(ctx)
	if c == nil {
		return "", fmt.Errorf("Sign in to copy files")
	}
	if strings.HasPrefix(project, crewProfileID+":") {
		id := strings.TrimPrefix(project, crewProfileID+":")
		if !externalTokenAllows(c, externalTool{Name: "read_crew_file"}) {
			return "", fmt.Errorf("This connection cannot read that Crew")
		}
		crew, _, _, ok := api.externalCrewResolve(ctx, c, id)
		if !ok {
			return "", fmt.Errorf("Source or destination Crew is unavailable")
		}
		if write {
			status, _ := externalCrewAuthorGate(c)
			if !crew.OwnedByCaller || status != 0 || c.AccessToken != nil && !c.AccessToken.Allows("crews:write") {
				return "", fmt.Errorf("Only the Crew owner with current write access can copy into it")
			}
		}
		return agentProfileRuntimeWorkspace(crew.OwnerID, crew.Binding.WorkspacePath), nil
	}
	root, m, err := findWorkflowManifestByID(ctx, project)
	if err != nil || m == nil || !userAllowedWorkflowID(c, project) || c.AccessToken != nil && !c.AccessToken.AllowsWorkflow(project) {
		return "", fmt.Errorf("Source or destination project is unavailable")
	}
	role := workflowAccessForManifest(c, m)
	if role != WorkflowAccessOwner && role != WorkflowAccessWrite && role != WorkflowAccessRead {
		return "", fmt.Errorf("Source or destination project is unavailable")
	}
	product := "agentworks"
	if m.Kind == "relay" {
		product = "relays"
	}
	if !userAllowedProduct(c, product) || !externalTokenAllows(c, externalTool{Name: "read_file"}) {
		return "", fmt.Errorf("This connection cannot read that project")
	}
	if write && !externalFileAuthoringAllowed(c, m) {
		return "", fmt.Errorf("Copying into this project needs current owner/editor access and Builder consent")
	}
	return root, nil
}

type projectCopyFile struct {
	destination string
	file        wf.File
}

func (api *StreamingAPI) registerProjectCopyTool(reg definitionToolRegistrar, gate *productToolGate, userID string, caller *UserClaims) error {
	if !projectCopyAvailable(caller) {
		return nil
	}
	def := projectCopyDefinition()
	if gate != nil {
		gate.Declare(def.Name)
	}
	return reg.RegisterCustomTool(def.Name, def.Description, def.InputSchema, func(ctx context.Context, args map[string]any) (string, error) {
		if caller == nil || caller.UserID != userID {
			return "", fmt.Errorf("Use your own chat to copy project files")
		}
		if current := GetUserFromContext(ctx); current != nil && current.UserID != userID {
			return "", fmt.Errorf("Use your own chat to copy project files")
		}
		r := httptest.NewRequest(http.MethodPost, "/api/external/call", nil).WithContext(context.WithValue(ctx, UserContextKey, caller))
		w := httptest.NewRecorder()
		api.externalCopyProjectFiles(w, r, args)
		if w.Code >= 400 {
			return "", fmt.Errorf("%s", strings.TrimSpace(w.Body.String()))
		}
		return w.Body.String(), nil
	}, "workspace_advanced")
}

func (api *StreamingAPI) externalCopyProjectFiles(w http.ResponseWriter, r *http.Request, args map[string]any) {
	if c := GetUserFromContext(r.Context()); c != nil && c.AccessToken != nil {
		live, err := activeExternalGrantClaims(r.Context(), c.AccessToken.ID)
		if err != nil || live.UserID != c.UserID {
			externalError(w, 403, "grant_unavailable", "This connection is no longer active. Sign in again.")
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, live))
	}
	if !projectCopyAvailable(GetUserFromContext(r.Context())) {
		externalError(w, 403, "forbidden", "Use a Builder connection with read access to the source and write access to your destination.")
		return
	}
	if err := validateProjectArguments(projectCopyDefinition().InputSchema, args); err != nil {
		externalError(w, 400, "invalid_arguments", err.Error())
		return
	}
	source, err := api.projectCopyRoot(r.Context(), externalArg(args, "source_project"), false)
	if err != nil {
		externalError(w, 403, "source_unavailable", err.Error())
		return
	}
	target, err := api.projectCopyRoot(r.Context(), externalArg(args, "target_project"), true)
	if err != nil {
		externalError(w, 403, "target_unavailable", err.Error())
		return
	}
	p, err := wf.CleanRelative(externalArg(args, "source_path"))
	if err != nil {
		externalError(w, 400, "invalid_path", err.Error())
		return
	}
	dest, err := wf.CleanRelative(externalArg(args, "target_path"))
	if err != nil || dest == "." {
		externalError(w, 400, "invalid_path", "Choose a destination file or folder inside the project.")
		return
	}
	if source == target && (dest == p || strings.HasPrefix(dest, p+"/")) {
		externalError(w, 400, "invalid_path", "Choose a destination outside the source folder.")
		return
	}
	guard := externalFileGuard(r.Context(), externalIsCrewRoot(target))
	if wf.ProtectedWrite(dest) || !guard.Allows(dest, true) {
		externalError(w, 403, "protected_path", "Destination is protected or outside write grants.")
		return
	}
	listing, err := externalFileRequest(r.Context(), wf.Request{Root: source, Operation: "list", Path: p, Depth: 8, Limit: 200})
	if err != nil {
		externalFailure(w, err)
		return
	}
	if listing.Truncated || listing.DepthLimited || listing.NextOffset != 0 {
		externalError(w, 400, "copy_limit", "Choose a smaller source folder (at most 64 files and 8 levels).")
		return
	}
	files := []projectCopyFile{}
	directories := []string{}
	// File listings include their selected file; directory listings omit the
	// selected directory itself. Private descendants are excluded by that reader.
	isFile := len(listing.Entries) == 1 && listing.Entries[0].Path == p && listing.Entries[0].Type == "file"
	if !isFile {
		directories = append(directories, dest)
	}
	var total int64
	for _, entry := range listing.Entries {
		rel := strings.TrimPrefix(strings.TrimPrefix(entry.Path, p), "/")
		if p == "." {
			rel = entry.Path
		}
		to := dest
		if rel != "" {
			to = path.Join(dest, rel)
		}
		clean, e := wf.CleanRelative(to)
		if e != nil || clean != to || wf.ProtectedWrite(to) || !guard.Allows(to, true) {
			externalError(w, 403, "protected_path", "A destination path is protected or outside write grants.")
			return
		}
		if reason := publishedWriteRefusal(r.Context(), target+"/"+to); reason != "" {
			externalError(w, 403, "published_source", reason)
			return
		}
		if entry.Type == "folder" {
			directories = append(directories, to)
			continue
		}
		if len(files) >= projectCopyMaxFiles || entry.Size > wf.MaxFileBytes || total+entry.Size > projectCopyMaxBytes {
			externalError(w, 400, "copy_limit", "Choose fewer or smaller files (64 files, 2 MiB each, 16 MiB total).")
			return
		}
		content, e := externalFileRequest(r.Context(), wf.Request{Root: source, Operation: "read", Path: entry.Path})
		if e != nil {
			externalFailure(w, e)
			return
		}
		if !content.Exists || content.Size > wf.MaxFileBytes {
			externalError(w, 400, "copy_limit", "A source file is missing or larger than 2 MiB.")
			return
		}
		current, e := externalFileRequest(r.Context(), wf.Request{Root: target, Operation: "read", Path: to})
		if e != nil {
			externalFailure(w, e)
			return
		}
		if current.Exists {
			externalError(w, 409, "destination_exists", "A destination file already exists. Choose a new path.")
			return
		}
		total += content.Size
		files = append(files, projectCopyFile{to, content.File})
	}
	if total > projectCopyMaxBytes {
		externalError(w, 400, "copy_limit", "Choose files totaling at most 16 MiB.")
		return
	}
	for _, dir := range directories {
		if err = projectCopyDirectory(r.Context(), target, dir, guard); err != nil {
			externalFailure(w, err)
			return
		}
	}
	paths := []string{}
	for _, item := range files {
		c := GetUserFromContext(r.Context())
		req := wf.WriteRequest{Root: target, Path: item.destination, ExpectedRevision: wf.MissingRevision, RequestID: uuid.NewString(), Actor: c.UserID, Guard: guard, Identity: wf.EditIdentity{UserID: c.UserID, Username: c.Username, Source: "project_copy"}}
		if c.AccessToken != nil {
			req.Identity.ConnectionID = c.AccessToken.ID
			req.Actor += "/" + c.AccessToken.ID
		}
		if item.file.Encoding == "base64" {
			if _, err = base64.StdEncoding.DecodeString(item.file.Content); err != nil {
				externalFailure(w, err)
				return
			}
			req.ContentBase64 = item.file.Content
		} else {
			req.Content = item.file.Content
		}
		result := httptest.NewRecorder()
		externalPostSharedFileWrite(result, r, req)
		if result.Code != 200 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(result.Code)
			_, _ = w.Write(result.Body.Bytes())
			return
		}
		paths = append(paths, item.destination)
	}
	externalJSON(w, map[string]any{"copied": true, "files": paths, "file_count": len(files), "bytes": total, "source_project": args["source_project"], "target_project": args["target_project"]})
}

func projectCopyDirectory(ctx context.Context, root, dir string, guard *wf.FolderGuard) error {
	if dir == "." {
		return nil
	}
	if err := projectCopyDirectory(ctx, root, path.Dir(dir), guard); err != nil {
		return err
	}
	if !guard.Allows(dir, true) && guard.AllowsTraversal(dir) {
		listing, err := externalFileRequest(ctx, wf.Request{Root: root, Path: dir, Operation: "list", Depth: 1, Limit: 1})
		if err == nil && !(len(listing.Entries) == 1 && listing.Entries[0].Path == dir && listing.Entries[0].Type == "file") {
			return nil
		}
	}
	if wf.ProtectedWrite(dir) || !guard.Allows(dir, true) {
		return fmt.Errorf("Destination folder is protected or outside write grants")
	}
	data, _ := json.Marshal(wf.LifecycleRequest{Root: root, Path: dir, Action: "mkdir", ExpectedRevision: wf.MissingRevision, Guard: guard})
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, getWorkspaceAPIURL()+"/api/shared-file-lifecycle", bytes.NewReader(data))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Workspace-Token", os.Getenv("WORKSPACE_API_TOKEN"))
	response, err := workspaceHTTPClient.Do(r)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == 200 {
		return nil
	}
	if response.StatusCode == 409 {
		listing, e := externalFileRequest(ctx, wf.Request{Root: root, Path: dir, Operation: "list", Depth: 1, Limit: 1})
		if e == nil && !(len(listing.Entries) == 1 && listing.Entries[0].Path == dir && listing.Entries[0].Type == "file") {
			return nil
		}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 16384))
	return fmt.Errorf("Destination folder could not be created (status %d)", response.StatusCode)
}
