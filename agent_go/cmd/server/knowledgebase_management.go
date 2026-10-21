package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
)

// Forms prepare the same frozen, interactive approval as the access assistant.
// This route never applies a change or accepts credential values. Confirmation
// rechecks current folder roles, administrator rights and the ACL version.
func (api *StreamingAPI) handleKnowledgebaseManagement(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := GetUserFromContext(r.Context())
	if !knowledgebaseProductAllowed(claims) || !knowledgeInteractiveAccess(claims) {
		externalError(w, 403, "FORBIDDEN", "Interactive Brain access required.")
		return
	}
	var args map[string]any
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&args) != nil {
		externalError(w, 400, "INVALID_ARGUMENT", "Invalid management request.")
		return
	}
	if _, present := args["pat"]; present {
		externalError(w, 400, "INVALID_ARGUMENT", "Enter backup credentials only in the secure approval field.")
		return
	}
	action, _ := args["action"].(string)
	switch action {
	case "grant", "revoke", "create_service_account", "disable_service_account", "configure_backup":
	default:
		externalError(w, 400, "INVALID_ARGUMENT", "Unsupported management action.")
		return
	}
	if action == "configure_backup" {
		remote, _ := args["remote_url"].(string)
		u, err := url.Parse(strings.TrimSpace(remote))
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			externalError(w, 400, "INVALID_ARGUMENT", "Use an HTTPS repository URL without credentials, query parameters or a fragment.")
			return
		}
	}
	if err := knowledgebase.ValidateToolArguments(knowledgebase.ToolAccess, args); err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	s, err := knowledgebaseService()
	if err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	if err = knowledgebaseSyncIdentities(r.Context(), s); err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	p := knowledgebasePrincipal(r, claims)
	if action == "configure_backup" || action == "create_service_account" || action == "disable_service_account" {
		if !p.IsAdmin {
			externalError(w, 403, "FORBIDDEN", "An administrator is required.")
			return
		}
	} else {
		folder, _ := args["folder_path"].(string)
		access, e := s.Call(r.Context(), p, "get_knowledgebase_access", map[string]any{"folder_path": folder})
		if e != nil {
			knowledgebaseHTTPError(w, e)
			return
		}
		data, ok := access.(map[string]any)
		if !ok || !strings.EqualFold(fmt.Sprint(data["effective_role"]), "owner") {
			externalError(w, 403, "FORBIDDEN", "A folder owner is required.")
			return
		}
	}
	result, err := knowledgeProposeAccess(r.Context(), claims.UserID, args)
	if err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	var response any
	if json.Unmarshal([]byte(result), &response) != nil {
		externalError(w, 500, "INTERNAL", "Could not prepare the change.")
		return
	}
	knowledgebaseWriteJSON(w, response)
}
