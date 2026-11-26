package server

import (
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strings"
)

// crewScriptFilesProxyWrite saves a Crew workflow's code file edited in the project's Files viewer (#4151) through
// the workflow's own writer, so script.json is checked and the workflow list refreshes, the same as every other
// source edit. It answers in the workspace service's document shape and returns true when it handled the request;
// anything that is not a save of a workflow's editable code file goes on to the workspace service unchanged.
func crewScriptFilesProxyWrite(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPut {
		return false
	}
	rel := workspaceProxyRelativePath(r)
	if !strings.HasPrefix(rel, "api/documents/") {
		return false
	}
	documentPath := path.Clean(strings.TrimPrefix(rel, "api/documents/"))
	parts := strings.SplitN(documentPath, "/", 4)
	if len(parts) != 4 || parts[0] != "Workflow" || parts[2] != "code" || !crewScriptEditableFile(parts[3]) {
		return false
	}
	binding, err := findCrewExecutable(parts[0] + "/" + parts[1])
	if err != nil || binding == nil || binding.Kind != "script" {
		return false
	}
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.AccessToken != nil {
		return false
	}
	if access := userAccessForClaims(claims); access.Disabled || !access.CanEdit || !userAllowedProduct(claims, "work") || !crewProjectOwnedByCaller(claims.UserID, binding.CrewRoot) {
		writeJSONError(w, http.StatusForbidden, "Only this project's Builder can edit its workflows. Ask in the project's chat instead.")
		return true
	}
	if refuseCrewOwnerOnline(w, r, crewProjectIDOfRoot(binding.CrewRoot), claims.UserID) {
		return true
	}
	var body struct {
		Content    *string `json:"content"`
		CreateOnly bool    `json:"create_only"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&body); err != nil || body.Content == nil {
		writeJSONError(w, http.StatusBadRequest, "Send the file's new content.")
		return true
	}
	if body.CreateOnly {
		writeJSONError(w, http.StatusBadRequest, "Add new workflow files from the project's chat.")
		return true
	}
	if err := writeCrewScriptFile(r.Context(), binding, parts[3], *body.Content); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return true
	}
	publishPlanChanged(documentPath)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "Document updated successfully", "data": map[string]string{"filepath": documentPath}})
	return true
}
