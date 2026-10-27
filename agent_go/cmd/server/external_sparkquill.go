package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/sparkquillproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const sparkquillMCPInstructions = `You are connected to SparkQuill, the family's learning workspace. Use get_api_spec to discover tools and their schemas, then call_tool. Paths are relative to this family's workspace, never absolute or another user's path. Read materials and learning context before building. create_learning_activity and update_learning_activity delegate to Quill in a separate conversation and return a job_id: poll get_activity_status. Finished activities can be previewed with get_file_link; assign_activity hands a finished activity to the child. Direct file edits require sparkquill:write, expected_revision and a stable request_id; read first and reuse request_id only for identical retries. Upload binary files as base64 (up to 2 MiB decoded per file). Internal state, PINs, secrets and progress records cannot be edited as files. Creation does not automatically hand an activity to the child. Only tools in this connection's catalog are available.`

var sparkquillScopes = []string{"sparkquill:read", "sparkquill:write", "sparkquill:run"}

func sparkquillOnlyDeployment() bool {
	return isSingleProductServerDeployment() && productEnabled("sparkquill")
}
func hasSparkQuillScopes(scopes []string) bool {
	for _, s := range scopes {
		if strings.HasPrefix(s, "sparkquill:") {
			return true
		}
	}
	return false
}
func mcpOAuthDefaults() []string {
	if sparkquillOnlyDeployment() {
		return slices.Clone(sparkquillScopes)
	}
	return slices.Clone(mcpOAuthDefaultScopes)
}
func mcpOAuthSupportedScopes() []string {
	if sparkquillOnlyDeployment() {
		return slices.Clone(sparkquillScopes)
	}
	return slices.Clone(mcpOAuthScopes)
}
func sparkquillExternalConnection(c *UserClaims) bool {
	return sparkquillOnlyDeployment() || c != nil && c.AccessToken != nil && c.AccessToken.SparkQuillAccess()
}
func sparkquillMCPAllowed(c *UserClaims) bool {
	if c == nil {
		return false
	}
	if IsMultiUserMode() {
		d, err := readUserDirectoryFile()
		if err != nil || d.byID(c.UserID) == nil || d.byID(c.UserID).Disabled {
			return false
		}
	}
	return c != nil && c.UserID != "" && c.Scope == "" && c.Provider != "bot_route" && productEnabled("sparkquill") && userAllowedProduct(c, "sparkquill") && !directoryUserIsDisabled(c)
}
func sparkquillGrantAllowed(c *UserClaims, scopes []string) bool {
	if !sparkquillMCPAllowed(c) || !slices.Contains(scopes, "sparkquill:read") {
		return false
	}
	for _, s := range scopes {
		if !slices.Contains(sparkquillScopes, s) {
			return false
		}
	}
	return (!slices.Contains(scopes, "sparkquill:write") && !slices.Contains(scopes, "sparkquill:run")) || userAccessForClaims(c).CanEdit
}
func sparkquillOAuthScopesFor(c *UserClaims, scopes []string) []string {
	out := []string{}
	if !sparkquillMCPAllowed(c) {
		return out
	}
	for _, s := range scopes {
		if slices.Contains(sparkquillScopes, s) && (s == "sparkquill:read" || userAccessForClaims(c).CanEdit) {
			out = append(out, s)
		}
	}
	return out
}
func sparkquillExternalToolAllowed(c *UserClaims, t externalTool) bool {
	if !sparkquillMCPAllowed(c) {
		return false
	}
	if c.AccessToken == nil {
		return !t.mutates && !t.executes
	} // Writes always need explicit external consent.
	scopes := c.AccessToken.Scopes
	if !sparkquillGrantAllowed(c, scopes) {
		return false
	}
	if t.executes {
		return c.AccessToken.Allows("sparkquill:run") && c.AccessToken.FileGuard == nil
	}
	return !t.mutates || c.AccessToken.Allows("sparkquill:write")
}
func externalToolsForClaims(c *UserClaims) ([]externalTool, error) {
	if sparkquillExternalConnection(c) {
		return sparkquillExternalTools()
	}
	return externalTools()
}

var sparkquillCatalogOnce sync.Once
var sparkquillCatalog []externalTool
var sparkquillCatalogErr error

func sparkquillExternalTools() ([]externalTool, error) {
	sparkquillCatalogOnce.Do(func() {
		add := func(name, description string, write, run bool, props map[string]any, required ...string) {
			if props == nil {
				props = map[string]any{}
			}
			req := []string{}
			req = append(req, required...)
			sparkquillCatalog = append(sparkquillCatalog, externalTool{Name: name, Description: description, sparkquill: true, mutates: write, executes: run, InputSchema: map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false}})
		}
		page := func() map[string]any {
			return map[string]any{"path": map[string]any{"type": "string", "description": "Family-relative directory; omit for root."}, "limit": externalInteger(1, 200), "offset": externalInteger(0, 10000), "depth": externalInteger(1, 8), "glob": externalString("Optional relative glob, e.g. **/*.html.")}
		}
		add("get_learning_context", "Read the child's saved name, grade, curriculum and language, plus available learning-profile notes. PINs and account credentials are excluded.", false, false, nil)
		add("get_learning_progress", "Read the family's saved progress report and list activity attempt evidence. Reports describe saved evidence, not a new assessment.", false, false, nil)
		add("list_materials", "Browse uploaded learning materials and their metadata. Paths returned can be used with read_material.", false, false, page())
		add("read_material", "Read an uploaded material or its extracted-text .meta.json. Binary content is base64 (up to 2 MiB).", false, false, map[string]any{"path": externalString("Path under materials/ or inbox/ from list_materials/list_files.")}, "path")
		add("list_activities", "List finalized learning activities, their metadata, item paths and preview links.", false, false, map[string]any{"limit": externalInteger(1, 200), "offset": externalInteger(0, 10000), "query": externalString("Filter title, subject and topic.")})
		add("list_files", "Browse the family's learning content. Private state and symbolic links are excluded.", false, false, page())
		add("read_file", "Read a family content file with its current revision, up to 2 MiB. Binary content is base64.", false, false, map[string]any{"path": externalString("Family-relative content path.")}, "path")
		add("get_file_link", "Return an authenticated SparkQuill preview URL for an existing content file. Recipients must sign in to this family account.", false, false, map[string]any{"path": externalString("Family-relative content file path.")}, "path")
		content := func() map[string]any {
			return map[string]any{"path": externalString("File under activities/, materials/ or inbox/. Internal settings and attempts are protected."), "content": map[string]any{"type": "string", "maxLength": wf.MaxFileBytes, "description": "UTF-8 content; empty content is allowed."}, "request_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}
		}
		add("create_file", "Create a new UTF-8 content file; refuses to overwrite an existing file. Reuse request_id only for identical retries.", true, false, content(), "path", "content", "request_id")
		p := content()
		p["expected_revision"] = externalString("Current revision from read_file. Read and reconcile on conflict.")
		add("update_file", "Replace an existing UTF-8 file using its current revision. Does not create missing files.", true, false, p, "path", "content", "expected_revision", "request_id")
		p = content()
		delete(p, "content")
		p["content_base64"] = map[string]any{"type": "string", "maxLength": 2796204, "description": "Base64 file content, up to 2 MiB decoded."}
		p["expected_revision"] = externalString("Current revision, or missing to create. Protects against overwriting concurrent changes.")
		add("upload_file", "Upload an image, PDF, audio, video or other learning asset with revision checks (2 MiB decoded limit).", true, false, p, "path", "content_base64", "expected_revision", "request_id")
		p = map[string]any{"brief": map[string]any{"type": "string", "minLength": 1, "maxLength": 20000}, "format": map[string]any{"type": "string", "enum": []string{"game", "quiz", "book", "video", "study-material"}}, "request_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}
		add("create_learning_activity", "Ask Quill to create a complete game, quiz, illustrated book, explainer video or study material using the saved parent model/account. Uses the parent's normal SparkQuill capabilities and budget. Returns job_id to poll; does not assign to the child.", false, true, p, "brief", "format", "request_id")
		add("update_learning_activity", "Ask Quill to revise an existing activity. Runs separately from the parent's live chat. Returns job_id to poll.", false, true, map[string]any{"dir": externalString("Activity directory from list_activities."), "brief": map[string]any{"type": "string", "minLength": 1, "maxLength": 20000}, "request_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}}, "dir", "brief", "request_id")
		add("get_activity_status", "Poll a creation/update job from this connection. Returns its exact final response, error and activity preview links.", false, false, map[string]any{"job_id": externalString("Job ID returned by create_learning_activity/update_learning_activity.")}, "job_id")
		add("finalize_activity", "Finalize content you built directly into a child-ready activity with the normal SparkQuill renderer and manifest. Read the returned render report before assigning.", true, false, map[string]any{"dir": externalString("Existing activities/<slug> folder."), "title": externalString("Human title."), "subject": externalString("School subject."), "topic": externalString("Learning topic."), "goal": externalString("Learning objective."), "persona": externalString("Tutor tone."), "items": map[string]any{"type": "array", "items": externalString("Bare filename inside the activity folder."), "maxItems": 100}}, "dir", "title")
		add("assign_activity", "Make a finalized activity the child's current activity. Does not start a child chat or change the PIN.", true, false, map[string]any{"dir": externalString("Activity directory from list_activities.")}, "dir")
		manifest, err := sparkquillproduct.SparkQuillManifest()
		if err != nil {
			sparkquillCatalogErr = err
			return
		}
		admitted := manifest.Chat["run"].ExternalTools
		if len(admitted) != len(sparkquillCatalog) {
			sparkquillCatalogErr = fmt.Errorf("SparkQuill external tool definitions do not match product.yaml")
			return
		}
		for i := range sparkquillCatalog {
			t := &sparkquillCatalog[i]
			if !slices.Contains(admitted, t.Name) {
				sparkquillCatalogErr = fmt.Errorf("SparkQuill tool %s is not admitted", t.Name)
				return
			}
			wire, marshalErr := json.Marshal(t.InputSchema)
			if marshalErr != nil {
				sparkquillCatalogErr = marshalErr
				return
			}
			if err := json.Unmarshal(wire, &t.InputSchema); err != nil {
				sparkquillCatalogErr = err
				return
			}
			compiler := jsonschema.NewCompiler()
			uri := "urn:sparkquill:" + t.Name
			if err := compiler.AddResource(uri, t.InputSchema); err != nil {
				sparkquillCatalogErr = err
				return
			}
			t.validator, err = compiler.Compile(uri)
			if err != nil {
				sparkquillCatalogErr = err
				return
			}
		}
	})
	return sparkquillCatalog, sparkquillCatalogErr
}

type sparkquillFileGuardKey struct{}

func sparkquillFamilyRoot(c *UserClaims) string {
	return agentProfileRuntimeWorkspace(c.UserID, "Chats/SparkQuill")
}
func sparkquillRootAllowed(ctx context.Context, root string) bool {
	c := GetUserFromContext(ctx)
	return sparkquillMCPAllowed(c) && root == sparkquillFamilyRoot(c) && sparkquillExternalConnection(c)
}
func sparkquillContentContext(r *http.Request) context.Context {
	guard := &wf.FolderGuard{ReadPaths: []string{"activities", "materials", "inbox", "reports", "memory"}, WritePaths: []string{"activities", "materials", "inbox"}}
	if c := GetUserFromContext(r.Context()); c.AccessToken != nil && c.AccessToken.FileGuard != nil {
		supplied := c.AccessToken.FileGuard
		guard.ReadPaths = nil
		guard.WritePaths = nil
		guard.ReadOnlyPaths = nil
		for _, prefix := range []string{"activities", "materials", "inbox", "reports", "memory"} {
			for _, paths := range [][]string{supplied.ReadPaths, supplied.WritePaths, supplied.ReadOnlyPaths} {
				for _, p := range paths {
					if p == "." || p == prefix || strings.HasPrefix(prefix, p+"/") {
						guard.ReadPaths = append(guard.ReadPaths, prefix)
					} else if strings.HasPrefix(p, prefix+"/") {
						guard.ReadPaths = append(guard.ReadPaths, p)
					}
				}
			}
		}
		for _, prefix := range []string{"activities", "materials", "inbox"} {
			for _, p := range supplied.WritePaths {
				if p == "." || p == prefix || strings.HasPrefix(prefix, p+"/") {
					guard.WritePaths = append(guard.WritePaths, prefix)
				} else if strings.HasPrefix(p, prefix+"/") {
					guard.WritePaths = append(guard.WritePaths, p)
				}
			}
		}
		guard.BlockedPaths = slices.Clone(supplied.BlockedPaths)
		guard.ReadOnlyPaths = slices.Clone(supplied.ReadOnlyPaths)
		guard.BlockedWritePaths = slices.Clone(supplied.BlockedWritePaths)
	}
	return context.WithValue(r.Context(), sparkquillFileGuardKey{}, guard)
}
func sparkquillProtectedPath(p string, write bool) bool {
	if wf.Private(p) {
		return true
	}
	for _, part := range strings.Split(strings.ToLower(p), "/") {
		switch part {
		case "family.json", "current-activity.json", "pinned-pages.json", "product.json", "chat_history", "schedule-runs.json", "functions.json", "credentials", "sessions", "secrets":
			return true
		}
		if write && (part == "attempts" || part == "activity.json" || part == "child-profile.json" || strings.HasSuffix(part, ".meta.json")) {
			return true
		}
	}
	return write && wf.ProtectedWrite(p)
}
func externalFilePathPrivate(ctx context.Context, crew bool, p string) bool {
	if _, ok := ctx.Value(sparkquillFileGuardKey{}).(*wf.FolderGuard); ok && sparkquillProtectedPath(p, false) {
		return true
	}
	return externalPathPrivate(crew, p)
}
func sparkquillPreviewURL(r *http.Request, p string) string {
	return strings.TrimRight(getBaseURL(r), "/") + "/sparkquill?file=" + url.QueryEscape(p)
}
func sparkquillRead(ctx context.Context, c *UserClaims, p string) (wf.File, error) {
	result, err := externalFileRequest(ctx, wf.Request{Root: sparkquillFamilyRoot(c), Operation: "read", Path: p})
	return result.File, err
}
func sparkquillActivityDir(raw string) (string, error) {
	p, err := wf.CleanRelative(raw)
	parts := strings.Split(p, "/")
	if err != nil || len(parts) != 2 || parts[0] != "activities" || parts[1] == "." || sparkquillProtectedPath(p, false) {
		return "", fmt.Errorf("dir must identify one activities/<slug> folder")
	}
	return p, nil
}

func (api *StreamingAPI) externalSparkQuillCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) {
	c := GetUserFromContext(r.Context())
	ctx := sparkquillContentContext(r)
	r = r.WithContext(ctx)
	root := sparkquillFamilyRoot(c)
	fail := func(err error) { externalFailure(w, err) }
	switch name {
	case "get_learning_context":
		state, err := sparkquillLoadFamily(ctx, c)
		if err != nil {
			fail(err)
			return
		}
		notes, err := sparkquillRead(ctx, c, "memory/child-profile.json")
		if err != nil {
			fail(err)
			return
		}
		externalJSON(w, map[string]any{"child": state.Child, "parent_label": state.ParentLabel, "learning_profile": notes, "workspace": "SparkQuill", "formats": []string{"game", "quiz", "book", "video", "study-material"}})
	case "list_files", "list_materials":
		p := externalArg(args, "path")
		if name == "list_materials" {
			if p == "" {
				p = "materials"
			}
			if p != "materials" && p != "inbox" && !strings.HasPrefix(p, "materials/") && !strings.HasPrefix(p, "inbox/") {
				externalError(w, 403, "protected_path", "Materials must be under materials/ or inbox/.")
				return
			}
		}
		result, err := externalFileRequest(ctx, wf.Request{Root: root, Operation: "list", Path: p, Limit: externalInt(args, "limit", 100), Offset: externalInt(args, "offset", 0), Depth: externalInt(args, "depth", 4), Glob: externalArg(args, "glob")})
		if err != nil {
			if !sparkquillMissingDirectory(err) {
				fail(err)
				return
			}
			result = wf.Result{Note: "No files in this directory yet."}
		}
		externalJSON(w, result)
	case "read_file", "read_material", "get_file_link":
		p, err := wf.CleanRelative(externalArg(args, "path"))
		if err != nil || sparkquillProtectedPath(p, false) {
			externalError(w, 403, "protected_path", "File is protected or outside SparkQuill.")
			return
		}
		if name == "read_material" && !strings.HasPrefix(p, "materials/") && !strings.HasPrefix(p, "inbox/") {
			externalError(w, 403, "protected_path", "Material path must be under materials/ or inbox/.")
			return
		}
		if name == "get_file_link" {
			sparkquillFileLink(w, r, p)
			return
		}
		file, err := sparkquillRead(ctx, c, p)
		if err != nil {
			fail(err)
			return
		}
		externalJSON(w, file)
	case "create_file", "update_file", "upload_file":
		p, err := wf.CleanRelative(externalArg(args, "path"))
		if err != nil || sparkquillProtectedPath(p, true) {
			externalError(w, 403, "protected_path", "Internal settings and progress records are protected.")
			return
		}
		if name == "create_file" {
			args["expected_revision"] = wf.MissingRevision
		}
		if name == "update_file" && externalArg(args, "expected_revision") == wf.MissingRevision {
			externalError(w, 400, "invalid_arguments", "Use create_file to create a missing file.")
			return
		}
		if name == "upload_file" {
			delete(args, "content")
		}
		api.externalWriteFile(w, r, args, DiscoveredWorkflow{WorkspacePath: root})
	case "list_activities":
		rows, err := sparkquillActivities(ctx, c, r, externalArg(args, "query"))
		if err != nil {
			fail(err)
			return
		}
		start := min(externalInt(args, "offset", 0), len(rows))
		end := min(start+externalInt(args, "limit", 100), len(rows))
		externalJSON(w, map[string]any{"activities": rows[start:end], "total": len(rows), "next_offset": end, "has_more": end < len(rows)})
	case "get_learning_progress":
		report, err := sparkquillRead(ctx, c, "reports/progress.html")
		if err != nil {
			fail(err)
			return
		}
		evidence, err := externalFileRequest(ctx, wf.Request{Root: root, Operation: "list", Path: "activities", Glob: "**/attempts/**", Depth: 8, Limit: 200})
		if err != nil {
			if !sparkquillMissingDirectory(err) {
				fail(err)
				return
			}
			evidence = wf.Result{Note: "No activity attempts yet."}
		}
		preview := ""
		if report.Exists {
			preview = sparkquillPreviewURL(r, "reports/progress.html")
		}
		externalJSON(w, map[string]any{"report": report, "preview_url": preview, "attempt_files": evidence})
	case "create_learning_activity", "update_learning_activity":
		api.externalSparkQuillStart(w, r, name, args)
	case "get_activity_status":
		api.externalSparkQuillStatus(w, r, args)
	case "finalize_activity":
		if c.AccessToken.FileGuard != nil {
			externalError(w, 403, "insufficient_scope", "Finalizing activities requires an unrestricted SparkQuill write grant.")
			return
		}
		dir, err := sparkquillActivityDir(externalArg(args, "dir"))
		if err != nil {
			externalError(w, 400, "invalid_arguments", err.Error())
			return
		}
		args["dir"] = dir
		for _, metadata := range []string{"activity.json", "product.json"} {
			if _, err := sparkquillInternalFile(ctx, c, path.Join(dir, metadata)); err != nil {
				fail(err)
				return
			}
		}
		for _, item := range externalSparkQuillItems(args) {
			if path.Base(item) != item || strings.ContainsAny(item, "\\:") || sparkquillProtectedPath(path.Join(dir, item), true) {
				externalError(w, 400, "invalid_arguments", "Items must be ordinary bare filenames inside the activity.")
				return
			}
			file, err := sparkquillRead(ctx, c, path.Join(dir, item))
			if err != nil {
				fail(err)
				return
			}
			if !file.Exists {
				externalError(w, 404, "not_found", "Write every item before finalizing.")
				return
			}
			if strings.HasSuffix(strings.ToLower(item), sparkquillproduct.FragmentSuffix) {
				rendered := item[:len(item)-len(sparkquillproduct.FragmentSuffix)] + ".html"
				if _, err := sparkquillRead(ctx, c, path.Join(dir, rendered)); err != nil {
					fail(err)
					return
				}
			}
		}
		if api.agentProfiles == nil {
			externalError(w, 503, "unavailable", "SparkQuill is not ready.")
			return
		}
		tool, err := api.agentProfiles.BuildTool(agentprofiles.ToolBinding{ID: "sparkquill.create-learning-activity"}, agentprofiles.ToolRuntimeContext{UserID: c.UserID, WorkspacePath: root, Product: "sparkquill"})
		if err != nil {
			fail(err)
			return
		}
		result, err := tool.Execute(ctx, args)
		if err != nil {
			externalError(w, 422, "invalid_activity", err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(result))
	case "assign_activity":
		if c.AccessToken.FileGuard != nil {
			externalError(w, 403, "insufficient_scope", "Assigning activities requires an unrestricted SparkQuill write grant.")
			return
		}
		dir, err := sparkquillActivityDir(externalArg(args, "dir"))
		if err != nil {
			externalError(w, 400, "invalid_arguments", err.Error())
			return
		}
		manifest, err := sparkquillRead(ctx, c, path.Join(dir, "activity.json"))
		if err != nil {
			fail(err)
			return
		}
		if !manifest.Exists {
			externalError(w, 404, "not_found", "Finalize the activity before assigning it.")
			return
		}
		var m sparkquillproduct.ActivityManifest
		if json.Unmarshal([]byte(manifest.Content), &m) != nil || m.Title == "" {
			externalError(w, 422, "invalid_activity", "Activity manifest is invalid.")
			return
		}
		project, err := sparkquillInternalFile(ctx, c, path.Join(dir, "product.json"))
		if err != nil {
			fail(err)
			return
		}
		var saved struct {
			Product string `json:"product"`
			ID      string `json:"id"`
		}
		if !project.Exists || json.Unmarshal([]byte(project.Content), &saved) != nil || saved.Product != sparkquillproduct.ChildProfileID || saved.ID != path.Base(dir) {
			externalError(w, 422, "invalid_activity", "Finalize the activity before assigning it.")
			return
		}
		if _, err := sparkquillInternalFile(ctx, c, sparkquillproduct.CurrentActivityFile); err != nil {
			fail(err)
			return
		}
		data, _ := json.Marshal(map[string]string{"dir": dir})
		client := workspace.NewClient(getWorkspaceAPIURL(), workspace.WithUserID(c.UserID))
		_, err = client.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: path.Join(root, sparkquillproduct.CurrentActivityFile), Content: string(data)})
		if err != nil {
			fail(err)
			return
		}
		externalJSON(w, map[string]any{"assigned": true, "dir": dir, "title": m.Title})
	default:
		externalError(w, 404, "unknown_tool", "Tool is not exposed by SparkQuill.")
	}
}

func sparkquillLoadFamily(ctx context.Context, c *UserClaims) (sparkquillproduct.FamilyState, error) {
	// Read internal state only through this typed adapter; never return raw family.json.
	file, err := sparkquillInternalFile(ctx, c, sparkquillproduct.FamilyFile)
	var state sparkquillproduct.FamilyState
	if err == nil && file.Exists {
		err = json.Unmarshal([]byte(file.Content), &state)
	}
	return state, err
}
func sparkquillActivities(ctx context.Context, c *UserClaims, r *http.Request, query string) ([]map[string]any, error) {
	rows := []map[string]any{}
	entries := []wf.Entry{}
	for offset := 0; offset <= 10000; {
		result, err := externalFileRequest(ctx, wf.Request{Root: sparkquillFamilyRoot(c), Operation: "list", Path: "activities", Glob: "*/activity.json", Depth: 2, Limit: 200, Offset: offset})
		if err != nil {
			if sparkquillMissingDirectory(err) {
				return rows, nil
			}
			return rows, err
		}
		entries = append(entries, result.Entries...)
		if result.NextOffset <= offset {
			break
		}
		offset = result.NextOffset
	}
	for _, e := range entries {
		if e.Type != "file" {
			continue
		}
		file, err := sparkquillRead(ctx, c, e.Path)
		if err != nil {
			return rows, err
		}
		var m sparkquillproduct.ActivityManifest
		if json.Unmarshal([]byte(file.Content), &m) != nil {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(m.Title+" "+m.Subject+" "+m.Topic), strings.ToLower(query)) {
			continue
		}
		dir := path.Dir(e.Path)
		links := []map[string]string{}
		for _, item := range m.Items {
			p := path.Join(dir, item)
			if path.Base(item) != item || sparkquillProtectedPath(p, false) {
				continue
			}
			links = append(links, map[string]string{"path": p, "preview_url": sparkquillPreviewURL(r, p)})
		}
		rows = append(rows, map[string]any{"dir": dir, "activity": m, "files": links})
	}
	return rows, nil
}

func sparkquillInternalFile(ctx context.Context, c *UserClaims, p string) (wf.File, error) {
	rootPath := sparkquillFamilyRoot(c)
	base, err := os.OpenRoot(getWorkspaceDocsAbsPath())
	if errors.Is(err, fs.ErrNotExist) {
		return externalRemoteReadFile(ctx, rootPath, p)
	}
	if err != nil {
		return wf.File{}, err
	}
	defer base.Close()
	if err := externalNoSymlinks(base, rootPath); err != nil {
		return wf.File{}, err
	}
	root, err := base.OpenRoot(rootPath)
	if errors.Is(err, fs.ErrNotExist) {
		return externalRemoteReadFile(ctx, rootPath, p)
	}
	if err != nil {
		return wf.File{}, err
	}
	defer root.Close()
	return externalScopedFile(root, p)
}

func sparkquillFileLink(w http.ResponseWriter, r *http.Request, p string) {
	guard := externalFileGuard(r.Context(), false)
	if !guard.Allows(p, false) {
		externalError(w, 403, "protected_path", "File is outside folder grants.")
		return
	}
	response, err := externalRemoteAssetCall(r.Context(), sparkquillFamilyRoot(GetUserFromContext(r.Context())), p, "stat")
	if err != nil {
		externalFailure(w, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		externalError(w, response.StatusCode, "not_found", "File is not available.")
		return
	}
	var metadata map[string]any
	if err := json.NewDecoder(response.Body).Decode(&metadata); err != nil {
		externalFailure(w, err)
		return
	}
	if metadata["type"] != "file" {
		externalError(w, 400, "invalid_path", "Select a file to preview.")
		return
	}
	externalJSON(w, map[string]any{"path": p, "preview_url": sparkquillPreviewURL(r, p), "size": metadata["size"], "requires_sign_in": true})
}

func externalSparkQuillItems(args map[string]any) []string {
	items := []string{}
	if raw, ok := args["items"].([]any); ok {
		for _, v := range raw {
			if name, ok := v.(string); ok {
				items = append(items, name)
			}
		}
	}
	return items
}

func sparkquillMissingDirectory(err error) bool {
	var upstream *externalUpstreamError
	return errors.As(err, &upstream) && upstream.status == http.StatusNotFound
}
