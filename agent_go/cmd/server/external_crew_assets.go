package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Resolve both the link and its download using current Crew visibility and
// connection ceilings. A URL identifies an asset, never an access grant.
func (api *StreamingAPI) externalCrewAssetTarget(w http.ResponseWriter, r *http.Request, id, relative string) (string, string, bool) {
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		externalError(w, 401, "unauthorized", "Sign in to AgentWorks.")
		return "", "", false
	}
	if token := claims.AccessToken; token != nil && (!token.Allows("crews:read") || !token.AllowsCrew(id)) {
		externalError(w, 403, "insufficient_scope", "This connection cannot read this Crew.")
		return "", "", false
	}
	crew, _, _, ok := api.externalCrewResolve(r.Context(), claims, id)
	if !ok {
		externalError(w, 404, "not_found", "Crew not found or not allowed.")
		return "", "", false
	}
	clean, err := wf.CleanRelative(relative)
	if err != nil || clean != relative || clean == "." {
		externalError(w, 400, "invalid_path", "Use a canonical Crew-relative file or folder path.")
		return "", "", false
	}
	if sharedProjectHiddenPath(clean) || wf.Private(clean) || !externalFileGuard(r.Context(), false).Allows(clean, false) {
		externalError(w, 403, "protected_path", "File is private or outside folder grants.")
		return "", "", false
	}
	return agentProfileRuntimeWorkspace(crew.OwnerID, crew.Binding.WorkspacePath), clean, true
}

func (api *StreamingAPI) externalCrewAssetLink(w http.ResponseWriter, r *http.Request, args map[string]any) {
	id := externalArg(args, "crew_id")
	root, relative, ok := api.externalCrewAssetTarget(w, r, id, externalArg(args, "path"))
	if !ok {
		return
	}
	response, err := sharedAssetRequest(r, root, relative, "stat")
	if err != nil {
		externalError(w, 502, "workspace_unavailable", "Asset service unavailable.")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		externalError(w, response.StatusCode, "asset_unavailable", "Asset file is unavailable.")
		return
	}
	var metadata map[string]any
	if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&metadata) != nil || metadata == nil {
		externalError(w, 502, "invalid_response", "Invalid asset metadata.")
		return
	}
	kind, _ := metadata["type"].(string)
	if kind != "file" && kind != "folder" {
		externalError(w, 502, "invalid_response", "Invalid asset type.")
		return
	}
	metadata["crew_id"], metadata["path"] = id, relative
	// Physical legacy roots identify the owner's tree; shared Crew roots
	// already identify the project independently of the viewing account.
	metadata["preview_url"] = sharedAssetPublicURL(getBaseURL(r), kind, path.Join(root, relative))
	authentication := map[string]string{"preview": "Sign in to AgentWorks with current Crew access. The link grants no access."}
	if kind == "file" {
		q := url.Values{"crew_id": {id}, "path": {relative}, "download": {"true"}}
		metadata["download_url"] = strings.TrimRight(getBaseURL(r), "/") + "/api/external/v1/files/content?" + q.Encode()
		authentication["download"] = "Use your signed-in session or CLI/PAT Authorization Bearer header with crews:read; never put credentials in the URL. MCP OAuth is restricted to the MCP endpoint."
	}
	metadata["authentication"] = authentication
	addShareabilityMetadata(metadata, getBaseURL(r))
	externalJSON(w, metadata)
}
