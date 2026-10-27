package server

import (
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Lifecycle and sharing of a workflow, Flow or Crew over MCP, through the
// app's own handlers and their owner/editor checks: rename, duplicate, delete
// (permanent, so the caller repeats the ID) and who has access.
// Crew names and icons stay in update_crew.

func externalProjectDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	names := map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 200}, "description": "User ids, usernames or emails."}
	add("manage_project", "Rename, duplicate, delete or share a workflow, Flow or Crew. Pass workflow_id or crew_id. rename (workflows and Flows; Crews use update_crew): label, description (one short line, shown in Slack and lists), icon. duplicate (workflows and Flows): a copy you own, new_label. delete is permanent with no undo: owner only, and confirm must repeat the workflow_id or crew_id. get_access / set_access: a Goal's or Flow's owners (they build and change it) and readers (the people who can use it); set_access changes only the lists you send, owners only, and the main owner stays. Everyone in the company is the share tool's choice. Crew ownership uses crew_owners.", true, false, map[string]any{
		"workflow_id": externalString("Workflow or Flow ID from list_workflows. Pass this or crew_id."),
		"crew_id":     externalString("Crew ID from list_crews. Pass this or workflow_id."),
		"action":      map[string]any{"type": "string", "enum": []any{"rename", "duplicate", "delete", "get_access", "set_access"}},
		"label":       map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
		"description": map[string]any{"type": "string", "maxLength": 300, "description": "For rename: one short line about what the workflow is for."},
		"icon":        map[string]any{"type": "string", "maxLength": 16},
		"new_label":   map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
		"confirm":     externalString("For delete: the same workflow_id or crew_id."),
		"owners":      names,
		"readers":     names,
	}, "action")
}

var externalProjectFolderCleaner = regexp.MustCompile(`[^a-z0-9]+`)

func (api *StreamingAPI) externalWorkflowProjectCall(w http.ResponseWriter, r *http.Request, args map[string]any, workflow DiscoveredWorkflow) {
	// The connection must have been granted workflow access; a Crew-only
	// connection may not act on the account's workflows.
	if t := GetUserFromContext(r.Context()).AccessToken; t != nil && !t.Allows("workflows:read") && !t.Allows("runs:execute") {
		externalError(w, 403, "insufficient_scope", "This connection was not granted workflow access.")
		return
	}
	action := externalArg(args, "action")
	root := workflow.WorkspacePath
	respond := func(status int, body []byte) { externalScheduleRespond(w, status, body) }
	switch action {
	case "rename":
		req := UpdateWorkflowManifestRequest{WorkspacePath: root}
		if v := externalArg(args, "label"); v != "" {
			req.Label = &v
		}
		if v, ok := args["icon"].(string); ok {
			req.Icon = &v
		}
		if v, ok := args["description"].(string); ok {
			req.Description = &v
		}
		if req.Label == nil && req.Icon == nil && req.Description == nil {
			externalError(w, 400, "invalid_arguments", "rename needs label, description or icon.")
			return
		}
		respond(scheduleHandler(r, requireWorkflowWriteAccess(api.handleUpdateWorkflowManifest), http.MethodPut, "/api/workflows/manifest", nil, nil, req))
	case "duplicate":
		label := externalArg(args, "new_label")
		if label == "" {
			label = workflow.Manifest.Label + " (copy)"
		}
		slug := strings.Trim(externalProjectFolderCleaner.ReplaceAllString(strings.ToLower(label), "-"), "-")
		if slug == "" {
			slug = "workflow-copy"
		}
		if len(slug) > 48 {
			slug = slug[:48]
		}
		target := path.Join(path.Dir(strings.TrimSuffix(root, "/")), slug)
		if _, exists, _ := ReadWorkflowManifest(r.Context(), target); exists {
			externalError(w, 409, "folder_exists", "A workflow folder named "+slug+" exists; pass another new_label.")
			return
		}
		respond(scheduleHandler(r, requireWorkflowCreateAccess(api.handleDuplicateWorkflowManifest), http.MethodPost, "/api/workflows/manifest/duplicate", nil, nil,
			DuplicateWorkflowManifestRequest{SourceWorkspacePath: root, TargetWorkspacePath: target, NewLabel: label}))
	case "delete":
		if externalArg(args, "confirm") != workflow.Manifest.ID {
			externalError(w, 400, "confirm_required", "Deleting is permanent. Repeat the workflow_id in confirm to delete "+workflow.Manifest.ID+".")
			return
		}
		respond(scheduleHandler(r, requireWorkflowWriteAccess(api.handleDeleteWorkflowFolder), http.MethodDelete, "/api/workflows/folder", nil,
			url.Values{"workspace_path": {root}}, DeleteWorkflowFolderRequest{WorkspacePath: root}))
	case "get_access", "set_access":
		// One sharing model (2026-10-27): owners and the people who can use it, through the Access panel's operations.
		claims := GetUserFromContext(r.Context())
		owners, ownersSet := stringListArg(args["owners"])
		readers, readersSet := stringListArg(args["readers"])
		var state map[string]any
		var err error
		if action == "get_access" {
			state, err = api.projectAccessState(r.Context(), claims, root, workflow.Manifest)
		} else if !ownersSet && !readersSet {
			externalError(w, 400, "invalid_arguments", "set_access needs owners, readers (the people who can use it) or both.")
			return
		} else {
			state, err = api.setProjectAccessLists(r.Context(), claims, root, workflow.Manifest, owners, readers, ownersSet, readersSet)
		}
		if err != nil {
			externalError(w, 400, "access_not_changed", err.Error())
			return
		}
		externalJSON(w, state)
	default:
		externalError(w, 400, "invalid_arguments", action+" applies to Crews; for a workflow use rename, duplicate, delete, get_access or set_access.")
	}
}

func (api *StreamingAPI) externalCrewProjectCall(w http.ResponseWriter, r *http.Request, args map[string]any, crewID string) {
	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	action := externalArg(args, "action")
	if t := claims.AccessToken; t != nil {
		scope := "crews:write"
		if action == "get_access" {
			scope = "crews:read"
		}
		if !t.Allows(scope) || !t.AllowsCrew(crewID) {
			externalError(w, 403, "insufficient_scope", "This connection does not allow "+action+" on this Crew.")
			return
		}
	}
	crew, manifest, summary, ok := api.externalCrewResolve(ctx, claims, crewID)
	if !ok {
		externalError(w, 404, "not_found", "Crew not found or not allowed for this connection.")
		return
	}
	if action == "get_access" {
		externalJSON(w, map[string]any{"crew_id": crewID, "owner": summary["owner"], "my_access": map[bool]string{true: "owner", false: "member"}[crew.OwnedByCaller]})
		return
	}
	if !crew.OwnedByCaller {
		externalError(w, 403, "forbidden", "Only the Crew's owner can "+action+" it.")
		return
	}
	respond := func(status int, body []byte) { externalScheduleRespond(w, status, body) }
	vars := map[string]string{"id": manifest.Product, "project_id": manifest.ID}
	switch action {
	case "delete":
		if externalArg(args, "confirm") != crewID {
			externalError(w, 400, "confirm_required", "Deleting is permanent. Repeat the crew_id in confirm to delete "+crewID+".")
			return
		}
		respond(scheduleHandler(r, api.handleDeleteAgentProfileProject, http.MethodDelete, "/api/agent-profiles/"+manifest.Product+"/projects/"+url.PathEscape(manifest.ID), vars, nil, nil))
	case "set_access":
		externalError(w, 409, "use_share_dashboard", "Share this Crew from its dashboard: call share_dashboard, or open the dashboard and press Share.")
	default:
		externalError(w, 400, "invalid_arguments", action+" does not apply to a Crew; rename it with update_crew.")
	}
}
