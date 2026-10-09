package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func (api *StreamingAPI) externalWriteFile(w http.ResponseWriter, r *http.Request, args map[string]any, workflow DiscoveredWorkflow) {
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		externalError(w, 401, "unauthorized", "Sign in first.")
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
