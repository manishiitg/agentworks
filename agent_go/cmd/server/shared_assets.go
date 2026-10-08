package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// A link identifies a file; it never grants access. In particular uid is not
// an authorization credential and cannot select someone else's personal files.
func authorizedSharedAsset(w http.ResponseWriter, r *http.Request, full string) (root, relative string, ok bool) {
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		externalError(w, 401, "unauthorized", "Sign in to view this asset.")
		return
	}
	clean, err := wf.CleanRelative(full)
	if err != nil || clean == "." || clean != full {
		externalError(w, 400, "invalid_path", "Use a canonical workspace-relative path.")
		return
	}
	parts := strings.Split(clean, "/")
	if parts[0] == "Workflow" {
		if len(parts) < 2 {
			externalError(w, 403, "forbidden", "Share one workflow folder at a time.")
			return
		}
		root = strings.Join(parts[:2], "/")
		relative = strings.TrimPrefix(strings.TrimPrefix(clean, root), "/")
		if relative == "" {
			relative = "."
		}
		manifest, exists, e := ReadWorkflowManifest(r.Context(), root)
		if e != nil {
			externalError(w, 502, "workspace_unavailable", "Cannot verify workflow access.")
			return
		}
		if !exists || workflowAccessForManifest(claims, manifest) == WorkflowAccessNone {
			externalError(w, 403, "forbidden", "You do not have access to this workflow.")
			return
		}
		if claims.AccessToken != nil && (!claims.AccessToken.Allows("files:read") || !claims.AccessToken.AllowsWorkflow(manifest.ID)) {
			externalError(w, 403, "insufficient_scope", "This token does not allow this asset.")
			return
		}
		if claims.AccessToken != nil && !claims.AccessToken.FileGuard.Allows(relative, false) {
			externalError(w, 403, "protected_path", "File is outside folder grants.")
			return
		}
	} else {
		uid := publicWorkspaceUserID(r)
		if requested := r.URL.Query().Get("uid"); IsMultiUserMode() && requested != "" && requested != uid {
			externalError(w, 403, "forbidden", "Another user's personal files cannot be opened through this link.")
			return
		}
		if ref := workspaceref.MustParse(clean); ref.HasOwner() || ref.IsUsersRoot() {
			if ref.Logical() == "" || ref.Owner() != uid {
				externalError(w, 403, "forbidden", "Personal file access denied.")
				return
			}
			parts = strings.Split(ref.Logical(), "/")
		}
		switch parts[0] {
		case "Chats", "Downloads":
			root = workspaceref.PhysicalPathOf(uid, parts[0])
		case "skills", "knowledgebase", "learnings":
			root = parts[0]
		default:
			externalError(w, 403, "forbidden", "This workspace folder is not shareable.")
			return
		}
		relative = strings.Join(parts[1:], "/")
		if relative == "" {
			relative = "."
		}
	}
	if wf.Private(relative) {
		externalError(w, 403, "protected_path", "Private workspace files are not shareable.")
		return
	}
	return root, relative, true
}

func sharedAssetRequest(r *http.Request, root, p, operation string) (*http.Response, error) {
	body, _ := json.Marshal(map[string]string{"root": root, "path": p, "operation": operation})
	req, err := http.NewRequestWithContext(r.Context(), "POST", getWorkspaceAPIURL()+"/api/shared-assets", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Workspace-Token", os.Getenv("WORKSPACE_API_TOKEN"))
	for _, h := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	req.Header.Set("X-Asset-Method", r.Method)
	return workspaceHTTPClient.Do(req)
}
func sharedAssetURL(r *http.Request, full string) string {
	return sharedAssetPublicURL(getBaseURL(r), "file", full)
}
func serveSharedAsset(w http.ResponseWriter, r *http.Request, root, p, operation string) {
	response, err := sharedAssetRequest(r, root, p, operation)
	if err != nil {
		externalError(w, 502, "workspace_unavailable", "Asset service unavailable.")
		return
	}
	defer response.Body.Close()
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// Arbitrary HTML/SVG documents must not execute with the app's origin.
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data: blob:")
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"} {
		if v := response.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	if operation == "read" || operation == "archive" {
		disposition := "inline"
		name := path.Base(p)
		if operation == "archive" {
			name = "files.zip"
			disposition = "attachment"
		}
		if r.URL.Query().Get("download") == "true" {
			disposition = "attachment"
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	}
	w.WriteHeader(response.StatusCode)
	if r.Method != "HEAD" {
		io.Copy(w, response.Body)
	}
}
func (api *StreamingAPI) servePublicAsset(w http.ResponseWriter, r *http.Request, operation string) {
	encoded := r.URL.Query().Get("path")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		decoded, err = base64.RawURLEncoding.DecodeString(encoded)
	}
	if err != nil || len(decoded) == 0 {
		externalError(w, 400, "invalid_path", "Invalid file link.")
		return
	}
	full := string(decoded)
	crewRoot, crewPath, crewRootView, handled, crewOK := api.crewReaderSharedAsset(w, r, full)
	if handled && !crewOK {
		return
	}
	root, p := crewRoot, crewPath
	if !handled {
		var ok bool
		if root, p, ok = authorizedSharedAsset(w, r, full); !ok {
			return
		}
	}
	if crewRootView && operation == "archive" {
		// A crew-root archive would include the owner's private areas.
		externalError(w, 403, "protected_path", "Download a folder inside this crew instead.")
		return
	}
	if operation != "list" {
		serveSharedAsset(w, r, root, p, operation)
		return
	}
	response, err := sharedAssetRequest(r, root, p, operation)
	if err != nil {
		externalError(w, 502, "workspace_unavailable", "Asset service unavailable.")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		externalError(w, response.StatusCode, "asset_unavailable", "Cannot list this folder.")
		return
	}
	var listing struct {
		Data []map[string]any `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&listing) != nil {
		externalError(w, 502, "invalid_response", "Invalid asset listing.")
		return
	}
	if crewRootView {
		visible := listing.Data[:0]
		for _, entry := range listing.Data {
			// Entries are relative to root; name them relative to the crew root.
			if name, _ := entry["filepath"].(string); crewRootListingVisible(strings.TrimPrefix(strings.TrimPrefix(name, p), "/")) {
				visible = append(visible, entry)
			}
		}
		listing.Data = visible
	}
	// Preserve the existing SharedFolder response shape and full display paths.
	displayRoot := strings.TrimSuffix(strings.TrimSuffix(full, p), "/")
	if p == "." {
		displayRoot = full
	}
	for _, entry := range listing.Data {
		if name, ok := entry["filepath"].(string); ok {
			entry["filepath"] = path.Join(displayRoot, name)
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	externalJSON(w, map[string]any{"success": true, "data": sharedAssetTree(listing.Data, full)})
}

func (api *StreamingAPI) externalAssetLink(w http.ResponseWriter, r *http.Request, workflow DiscoveredWorkflow, p string) {
	root, relative, ok := authorizedSharedAsset(w, r, path.Join(workflow.WorkspacePath, p))
	if !ok {
		return
	}
	// Do not normalize untrusted traversal into an authorized path.
	if clean, e := wf.CleanRelative(p); e != nil || clean != p {
		externalError(w, 400, "invalid_path", "Use a canonical workflow-relative file path.")
		return
	}
	response, err := sharedAssetRequest(r, root, relative, "stat")
	if err != nil {
		externalError(w, 502, "workspace_unavailable", "Asset service unavailable.")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		externalError(w, response.StatusCode, "asset_unavailable", "Asset file is unavailable.")
		return
	}
	var metadata map[string]any
	if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&metadata) != nil {
		externalError(w, 502, "invalid_response", "Invalid asset metadata.")
		return
	}
	metadata["workflow_id"] = workflow.Manifest.ID
	metadata["path"] = relative
	kind, _ := metadata["type"].(string)
	if kind == "folder" {
		metadata["preview_url"] = sharedAssetPublicURL(getBaseURL(r), kind, path.Join(root, relative))
		metadata["authentication"] = map[string]string{"preview": "Sign in to AgentWorks with workflow access."}
	} else {
		metadata["preview_url"] = sharedAssetURL(r, path.Join(root, relative))
		q := url.Values{"workflow_id": {workflow.Manifest.ID}, "path": {relative}, "download": {"true"}}
		metadata["download_url"] = strings.TrimRight(getBaseURL(r), "/") + "/api/external/v1/files/content?" + q.Encode()
		metadata["authentication"] = map[string]string{"preview": "Sign in to AgentWorks with workflow access.", "download": "Send your PAT in the Authorization Bearer header; never put it in the URL."}
	}
	externalJSON(w, metadata)
}
func (api *StreamingAPI) handleExternalAssetContent(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		externalError(w, 401, "unauthorized", "Sign in to AgentWorks.")
		return
	}
	if claims.AccessToken != nil && !claims.AccessToken.Allows("files:read") {
		externalError(w, 403, "insufficient_scope", "File read permission is required.")
		return
	}
	workflows, err := DiscoverWorkflowManifests(r.Context())
	if err != nil {
		externalError(w, 502, "workspace_unavailable", "Cannot verify workflow access.")
		return
	}
	var chosen *DiscoveredWorkflow
	for _, workflow := range filterWorkflowManifestsForUser(claims, workflows) {
		if workflow.Manifest != nil && workflow.Manifest.ID == r.URL.Query().Get("workflow_id") {
			if chosen != nil {
				externalError(w, 409, "ambiguous_workflow", "Duplicate workflow IDs.")
				return
			}
			v := workflow
			chosen = &v
		}
	}
	if chosen == nil {
		externalError(w, 404, "not_found", "Workflow unavailable.")
		return
	}
	p := r.URL.Query().Get("path")
	clean, err := wf.CleanRelative(p)
	if err != nil || clean != p || p == "." {
		externalError(w, 400, "invalid_path", "Use a workflow-relative file path.")
		return
	}
	if claims.AccessToken != nil && !claims.AccessToken.FileGuard.Allows(p, false) {
		externalError(w, 403, "protected_path", "File is outside folder grants.")
		return
	}
	root, relative, ok := authorizedSharedAsset(w, r, fmt.Sprintf("%s/%s", chosen.WorkspacePath, p))
	if !ok {
		return
	}
	serveSharedAsset(w, r, root, relative, "read")
}

func sharedAssetTree(items []map[string]any, root string) []map[string]any {
	result := []map[string]any{}
	folders := map[string]map[string]any{}
	for _, entry := range items {
		name, _ := entry["filepath"].(string)
		if entry["type"] == "folder" {
			entry["children"] = []map[string]any{}
			folders[name] = entry
		}
		parent := path.Dir(name)
		if folder, ok := folders[parent]; ok && parent != root {
			folder["children"] = append(folder["children"].([]map[string]any), entry)
		} else {
			result = append(result, entry)
		}
	}
	return result
}
