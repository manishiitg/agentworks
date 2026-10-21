package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func externalCrewFilesDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("crew_files", "Use action=patch to edit a Crew text file, mkdir to create a folder, move to move a file/folder without overwriting, or delete to remove a file/empty folder. Only the owner with crews:write and current edit access can change files. Both move paths and every descendant must satisfy file guards; manifests, chats and databases stay protected. Only patch accepts diff and request_id: pass expected_revision from files read and a stable request_id for patch retries. Omit diff and request_id for mkdir, move and delete. File delete/move require expected_revision; mkdir requires expected_revision=missing; folder move/delete must omit expected_revision. Move/delete have no Crew history undo. Lifecycle operations return applied status; inspect paths before retrying them.", true, false, map[string]any{
		"crew_id": externalString("Crew ID."), "action": map[string]any{"type": "string", "enum": []any{"patch", "mkdir", "move", "delete"}},
		"path": externalString("Crew-relative path."), "destination": externalString("Crew-relative destination for move; parent must exist."), "diff": externalString("Patch only: diff accepted by the platform patch parser."), "expected_revision": externalString("Revision returned by files read, or missing for a new folder; omit for existing folders."), "request_id": externalString("Patch only: stable request ID for a retry; omit for mkdir, move and delete."),
	}, "crew_id", "action", "path")
}
func (api *StreamingAPI) externalCrewFilesCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	id := externalArg(args, "crew_id")
	if status, message := externalCrewAuthorGate(claims); status != 0 {
		externalError(w, status, "forbidden", message)
		return
	}
	if token := claims.AccessToken; token != nil && (!token.Allows("crews:write") || !token.AllowsCrew(id)) {
		externalError(w, 403, "insufficient_scope", "This connection cannot edit this Crew.")
		return
	}
	crew, _, _, ok := api.externalCrewResolve(r.Context(), claims, id)
	if !ok {
		externalError(w, 404, "not_found", "Crew not found or not allowed.")
		return
	}
	if !crew.OwnedByCaller {
		externalError(w, 403, "forbidden", "Only the Crew owner can change its files.")
		return
	}
	root := agentProfileRuntimeWorkspace(crew.OwnerID, crew.Binding.WorkspacePath)
	if reason := publishedWriteRefusal(r.Context(), root+"/"+externalArg(args, "path")); reason != "" {
		externalError(w, 403, "published_source", reason)
		return
	}
	if destination := externalArg(args, "destination"); destination != "" {
		if reason := publishedWriteRefusal(r.Context(), root+"/"+destination); reason != "" {
			externalError(w, 403, "published_source", reason)
			return
		}
	}
	p, err := wf.CleanRelative(externalArg(args, "path"))
	guard := externalFileGuard(r.Context(), true)
	if err != nil || wf.ProtectedWrite(p) || !guard.Allows(p, true) {
		externalError(w, 403, "protected_path", "Path is protected or outside write grants.")
		return
	}
	if crewEditLockRefuse(w, crew, claims, p) {
		return
	}
	action := externalArg(args, "action")
	if action == "patch" {
		if externalArg(args, "diff") == "" || externalArg(args, "expected_revision") == "" || externalArg(args, "request_id") == "" || args["destination"] != nil {
			externalError(w, 400, "invalid_arguments", "Patch requires diff, expected_revision and request_id, without destination.")
			return
		}
		req := wf.WriteRequest{Root: root, Path: p, Diff: externalArg(args, "diff"), ExpectedRevision: externalArg(args, "expected_revision"), RequestID: externalArg(args, "request_id"), Guard: guard, Actor: claims.UserID, Identity: wf.EditIdentity{UserID: claims.UserID, Username: claims.Username, Source: "public_mcp"}}
		if claims.AccessToken != nil {
			req.Actor += "/" + claims.AccessToken.ID
			req.Identity.ConnectionID = claims.AccessToken.ID
		}
		externalPostSharedFileWrite(w, r, req)
		return
	}
	if args["diff"] != nil || args["request_id"] != nil {
		externalError(w, 400, "invalid_arguments", "diff and request_id are patch-only arguments.")
		return
	}
	req := wf.LifecycleRequest{Root: root, Path: p, Action: action, Destination: externalArg(args, "destination"), ExpectedRevision: externalArg(args, "expected_revision"), Guard: guard}
	if action == "move" && req.Destination == "" || action != "move" && req.Destination != "" {
		externalError(w, 400, "invalid_arguments", "Only move requires destination.")
		return
	}
	data, _ := json.Marshal(req)
	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPost, getWorkspaceAPIURL()+"/api/shared-file-lifecycle", bytes.NewReader(data))
	if err != nil {
		externalFailure(w, err)
		return
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("X-Workspace-Token", os.Getenv("WORKSPACE_API_TOKEN"))
	response, err := workspaceHTTPClient.Do(upstream)
	if err != nil {
		externalFailure(w, err)
		return
	}
	defer response.Body.Close()
	var value any
	if err = json.NewDecoder(io.LimitReader(response.Body, 32<<10)).Decode(&value); err != nil {
		externalFailure(w, err)
		return
	}
	if response.StatusCode != 200 {
		externalError(w, response.StatusCode, "lifecycle_rejected", "File operation rejected: "+string(mustCrewJSON(value)))
		return
	}
	externalJSON(w, value)
}
func mustCrewJSON(value any) []byte { data, _ := json.Marshal(value); return data }
