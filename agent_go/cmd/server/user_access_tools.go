package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// Reuse the UI's mutation handlers so persistence, user resolution and last-owner
// protections have one implementation. This captures only their JSON response.
type accessToolResponse struct {
	header http.Header
	status int
	bytes.Buffer
}

func (r *accessToolResponse) Header() http.Header    { return r.header }
func (r *accessToolResponse) WriteHeader(status int) { r.status = status }
func (r *accessToolResponse) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = 200
	}
	return r.Buffer.Write(p)
}

func (api *StreamingAPI) registerUserAccessTools(reg definitionToolRegistrar, userID, activeWorkspace string, policy workflowChatPolicy) error {
	if !policy.allows("user_management") {
		return nil
	}
	str := func(description string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": description}
	}
	return reg.RegisterCustomTool("manage_user_access", "Builder-only user and workflow access management. Inspect before changing. Goal/Flow owners and admins may inspect or set its owners and the people who can use it, and list the sharing directory. Only admins may list full accounts, update users (including a person-supplied password) or delete an account while keeping its files. Accounts are added by DevOps on the server, not here. Every invocation checks current caller permissions and errors when unauthorized. Use source workspace_path to resolve cross-workflow sharing; never widen access without the user's request. Returns account metadata only, never passwords or hashes.", map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"properties": map[string]interface{}{
			"action":         map[string]interface{}{"type": "string", "enum": []string{"get_workflow_access", "set_workflow_access", "list_users", "update_user", "delete_user"}},
			"workspace_path": str("Workflow/<folder>; defaults to the active workflow for access and owner directory operations."),
			"owners":         map[string]interface{}{"type": "array", "items": str("User ID, username or email; complete owner list (they build and change it). Omit to keep.")},
			"readers":        map[string]interface{}{"type": "array", "items": str("User ID, username or email; complete list of people who can use it. Omit to keep.")},
			"user_id":        str("Existing account ID for update_user/delete_user."),
			"username":       str("Account username (update_user may rename)."),
			"email":          str("Account email."), "password": str("User-provided account password only; never echo it."),
			"role":  map[string]interface{}{"type": "string", "enum": []string{"admin", "creator", "editor", "viewer"}, "description": "Standardized account role; wins over admin/can_create/can_edit."},
			"admin": map[string]interface{}{"type": "boolean"}, "can_create": map[string]interface{}{"type": "boolean"}, "can_edit": map[string]interface{}{"type": "boolean"}, "disabled": map[string]interface{}{"type": "boolean"}, "vault_reader": map[string]interface{}{"type": "boolean", "description": "May read Vault (connections, tools, groups, access, audit, SQL queries) without being an admin; changes nothing. Vault is available to everyone; this grants audit access, not secret values."}, "crew_reviewer": map[string]interface{}{"type": "boolean", "description": "May review every Crew project's cost, chats and files, read-only and audited."},
			"products": map[string]interface{}{"type": "array", "items": str("Assignable product ID from list_users. AgentBridge, Brain and Vault are available to everyone where installed, regardless of this list.")},
		}, "required": []string{"action"},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		claims := &UserClaims{UserID: userID}
		access := userAccessForClaims(claims)
		if access.Disabled {
			return "", fmt.Errorf("Access denied: account is disabled")
		}
		ctx = context.WithValue(ctx, UserContextKey, claims)
		action, _ := args["action"].(string)
		path := activeWorkspace
		if raw, ok := args["workspace_path"]; ok {
			var valid bool
			path, valid = raw.(string)
			if !valid || strings.TrimSpace(path) == "" {
				return "", fmt.Errorf("workspace_path must be a workflow path")
			}
		}
		workflowAction := action == "get_workflow_access" || action == "set_workflow_access"
		if workflowAction || action == "list_users" && !access.Admin {
			paths, err := authorizeWorkflowContextPaths(ctx, []string{path})
			if err != nil {
				return "", fmt.Errorf("Access denied: source workflow is unavailable")
			}
			path = paths[0]
			level, _ := workflowAccessForWorkspacePath(ctx, claims, path)
			if !access.Admin && level != WorkflowAccessOwner {
				return "", fmt.Errorf("Access denied: workflow owner or admin required")
			}
		} else if !access.Admin {
			return "", fmt.Errorf("Access denied: admin required for account management")
		}
		var handler http.HandlerFunc
		method, target := http.MethodGet, "/api/users/directory"
		payload := map[string]interface{}{}
		switch action {
		case "get_workflow_access", "set_workflow_access":
			_, manifest := workflowAccessForWorkspacePath(ctx, claims, path)
			if manifest == nil {
				return "", fmt.Errorf("no Goal or Flow at %s", path)
			}
			owners, ownersSet := stringListArg(args["owners"])
			readers, readersSet := stringListArg(args["readers"])
			var state map[string]any
			var err error
			if action == "get_workflow_access" {
				state, err = api.projectAccessState(ctx, claims, path, manifest)
			} else if !ownersSet && !readersSet {
				return "", fmt.Errorf("give owners, readers or both")
			} else {
				state, err = api.setProjectAccessLists(ctx, claims, path, manifest, owners, readers, ownersSet, readersSet)
			}
			if err != nil {
				return "", err
			}
			out, _ := json.Marshal(state)
			return string(out), nil
		case "list_users":
			handler = api.handleUserDirectory
			if access.Admin {
				handler = api.handleAdminListUsers
			}
		case "update_user":
			handler = api.handleAdminUpdateUser
			method = http.MethodPut
			target = "/api/admin/users"
			for _, key := range []string{"username", "email", "password", "role", "admin", "can_create", "can_edit", "disabled", "crew_reviewer", "vault_reader", "products"} {
				if v, ok := args[key]; ok {
					payload[key] = v
				}
			}
		case "delete_user":
			handler = api.handleAdminDeleteUser
			method = http.MethodDelete
			target = "/api/admin/users"
		default:
			return "", fmt.Errorf("Unknown user management action")
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("Invalid user management arguments")
		}
		req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(data))
		if err != nil {
			return "", err
		}
		if action == "update_user" || action == "delete_user" {
			id, _ := args["user_id"].(string)
			if strings.TrimSpace(id) == "" {
				return "", fmt.Errorf("user_id required")
			}
			req = mux.SetURLVars(req, map[string]string{"id": id})
		}
		rec := &accessToolResponse{header: make(http.Header)}
		handler(rec, req)
		if rec.status >= 400 {
			return "", fmt.Errorf("User/access operation failed (%d): %s", rec.status, strings.TrimSpace(rec.String()))
		}
		return rec.String(), nil
	}, "user_management")
}
