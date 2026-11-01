package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// File editing uses the same live account/project authority and existing
// authoring consent as Builder. Discovery cannot resolve a project yet;
// dispatch checks its product, role and connection bounds before writing.
func externalFileAuthoringAllowed(claims *UserClaims, manifest *WorkflowManifest) bool {
	if claims == nil {
		return false
	}
	access := userAccessForClaims(claims)
	if access.Disabled || !access.CanEdit {
		return false
	}
	token := claims.AccessToken
	workflowAuthor := token == nil || token.BuilderAccess()
	relayAuthor := workflowAuthor || token != nil && token.RelayBuilderAccess()
	if manifest == nil {
		return workflowAuthor && userAllowedProduct(claims, "agentworks") || relayAuthor && userAllowedProduct(claims, "relays")
	}
	authorized, product := workflowAuthor, "agentworks"
	if manifest.Kind == "relay" {
		authorized, product = relayAuthor, "relays"
	}
	role := workflowAccessForManifest(claims, manifest)
	return authorized && userAllowedProduct(claims, product) && (token == nil || token.AllowsWorkflow(manifest.ID)) && userAllowedWorkflowID(claims, manifest.ID) && (role == WorkflowAccessOwner || role == WorkflowAccessWrite)
}

func (api *StreamingAPI) externalWriteFile(w http.ResponseWriter, r *http.Request, args map[string]any, workflow DiscoveredWorkflow) {
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		externalError(w, 401, "unauthorized", "Sign in first.")
		return
	}
	if workflow.Manifest == nil || !externalFileAuthoringAllowed(claims, workflow.Manifest) {
		externalError(w, 403, "forbidden", "File edits require current project owner/editor access and existing authoring consent for this product.")
		return
	}
	req := wf.WriteRequest{Root: workflow.WorkspacePath, Path: externalArg(args, "path"), Content: externalArg(args, "content"), ContentBase64: externalArg(args, "content_base64"), ExpectedRevision: externalArg(args, "expected_revision"), RequestID: externalArg(args, "request_id"), Actor: claims.UserID, Identity: wf.EditIdentity{UserID: claims.UserID, Username: claims.Username, Source: "public_mcp"}}
	if claims.AccessToken != nil {
		req.Identity.ConnectionID = claims.AccessToken.ID
		req.Guard = claims.AccessToken.FileGuard
		req.Actor += "/" + claims.AccessToken.ID
	}
	_, hasText := args["content"]
	_, hasBinary := args["content_base64"]
	if hasText == hasBinary {
		externalError(w, 400, "invalid_arguments", "Pass content (UTF-8 text) or content_base64 (a binary file), exactly one.")
		return
	}
	p, err := wf.CleanRelative(req.Path)
	if err != nil || wf.ProtectedWrite(p) || !req.Guard.Allows(p, true) {
		externalError(w, 403, "protected_path", "File is protected or outside write grants.")
		return
	}
	externalPostSharedFileWrite(w, r, req)
}

// externalPostSharedFileWrite sends a checked write to the workspace's guarded, revision-checked writer and answers with
// its receipt. A workflow's write_file and a Crew's write_crew_file share it.
func externalPostSharedFileWrite(w http.ResponseWriter, r *http.Request, req wf.WriteRequest) {
	data, err := json.Marshal(req)
	if err != nil {
		externalFailure(w, err)
		return
	}
	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPost, getWorkspaceAPIURL()+"/api/shared-file-write", bytes.NewReader(data))
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
	if response.StatusCode != 200 {
		var failure struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&failure) != nil || failure.Code == "" {
			externalFailure(w, &externalUpstreamError{response.StatusCode, "guarded file write failed"})
			return
		}
		externalError(w, response.StatusCode, failure.Code, failure.Error)
		return
	}
	var receipt wf.WriteReceipt
	if err = json.NewDecoder(io.LimitReader(response.Body, 32<<10)).Decode(&receipt); err != nil {
		externalFailure(w, err)
		return
	}
	externalJSON(w, receipt)
}
