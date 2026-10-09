package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Lifecycle and sharing of a workflow, Relay or Crew over MCP, through the
// app's own handlers and their owner/editor checks: rename, duplicate, delete
// (permanent, so the caller repeats the ID), who has access, and Crew
// templates after creation. Crew names and icons stay in update_crew.

func externalProjectDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	names := map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 200}, "description": "User ids, usernames or emails."}
	add("manage_project", "Rename, duplicate, delete or share a workflow, Relay or Crew, or install a Crew template after creation. Pass workflow_id or crew_id. rename (workflows and Relays; Crews use update_crew): label, icon. duplicate (workflows and Relays): a copy you own, new_label. delete is permanent with no undo: owner only, and confirm must repeat the workflow_id or crew_id. get_access / set_access: workflows and Relays list owners, editors and readers (set_access changes only the lists you send; owners only); Crews use private true/false (owner only). install_template (Crews): template_id from the Crew Agent catalog; files you edited are kept.", true, false, map[string]any{
		"workflow_id": externalString("Workflow or Relay ID from list_workflows. Pass this or crew_id."),
		"crew_id":     externalString("Crew ID from list_crews. Pass this or workflow_id."),
		"action":      map[string]any{"type": "string", "enum": []any{"rename", "duplicate", "delete", "get_access", "set_access", "install_template"}},
		"label":       map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
		"icon":        map[string]any{"type": "string", "maxLength": 16},
		"new_label":   map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
		"confirm":     externalString("For delete: the same workflow_id or crew_id."),
		"owners":      names,
		"editors":     names,
		"readers":     names,
		"private":     map[string]any{"type": "boolean", "description": "For a Crew's set_access."},
		"template_id": externalString("For install_template."),
	}, "action")
}

var externalProjectFolderCleaner = regexp.MustCompile(`[^a-z0-9]+`)

func (api *StreamingAPI) externalWorkflowProjectCall(w http.ResponseWriter, r *http.Request, args map[string]any, workflow DiscoveredWorkflow) {
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
		if req.Label == nil && req.Icon == nil {
			externalError(w, 400, "invalid_arguments", "rename needs label or icon.")
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
	case "get_access":
		respond(scheduleHandler(r, api.handleGetWorkflowAccess, http.MethodGet, "/api/workflow/access", nil, url.Values{"workspace_path": {root}}, nil))
	case "set_access":
		owners, editors, readers := workflow.Manifest.effectiveOwners(), workflow.Manifest.effectiveEditors(), workflow.Manifest.effectiveReaders()
		changed := false
		for key, target := range map[string]*[]string{"owners": &owners, "editors": &editors, "readers": &readers} {
			if raw, ok := args[key].([]any); ok {
				list := make([]string, 0, len(raw))
				for _, item := range raw {
					list = append(list, strings.TrimSpace(item.(string)))
				}
				*target = list
				changed = true
			}
		}
		if !changed {
			externalError(w, 400, "invalid_arguments", "set_access needs owners, editors or readers.")
			return
		}
		body := map[string]any{"workspace_path": root, "owners": owners, "editors": editors, "readers": readers}
		respond(scheduleHandler(r, api.handleSetWorkflowAccess, http.MethodPut, "/api/workflow/access", nil, nil, body))
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
	crew, _, summary, ok := api.externalCrewResolve(ctx, claims, crewID)
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
	vars := map[string]string{"id": "work", "project_id": crewID}
	switch action {
	case "delete":
		if externalArg(args, "confirm") != crewID {
			externalError(w, 400, "confirm_required", "Deleting is permanent. Repeat the crew_id in confirm to delete "+crewID+".")
			return
		}
		respond(scheduleHandler(r, api.handleDeleteAgentProfileProject, http.MethodDelete, "/api/agent-profiles/work/projects/"+url.PathEscape(crewID), vars, nil, nil))
	case "set_access":
		private, ok := args["private"].(bool)
		if !ok {
			externalError(w, 400, "invalid_arguments", "For a Crew, set_access takes private true or false.")
			return
		}
		respond(scheduleHandler(r, api.handlePutProjectSharing, http.MethodPut, "/api/agent-profiles/work/projects/"+url.PathEscape(crewID)+"/sharing", vars, nil, map[string]any{"private": private}))
	case "install_template":
		template, err := loadCrewAgentTemplate(externalArg(args, "template_id"))
		if err != nil || template == nil {
			externalError(w, 400, "invalid_arguments", "Unknown template_id; see the Crew Agent catalog.")
			return
		}
		root := crew.Binding.WorkspacePath
		// Files the owner already has (edited skills, setup progress) are kept.
		for rel, content := range template.Files {
			target := path.Join(root, rel)
			if _, found, _ := readFileFromWorkspace(ctx, target); found {
				continue
			}
			if err := writeFileToWorkspace(ctx, target, content); err != nil {
				externalError(w, 502, "workspace_unavailable", "Could not write template file "+rel+".")
				return
			}
		}
		if err := updateProductSelectedSkills(ctx, "work", root, func(current []string) []string { return settingsWith(current, template.SelectedSkills...) }); err != nil {
			externalError(w, 502, "workspace_unavailable", err.Error())
			return
		}
		if err := applyCrewAgentTemplate(ctx, root, template); err != nil {
			externalError(w, 502, "workspace_unavailable", err.Error())
			return
		}
		out, _ := json.Marshal(map[string]any{"crew_id": crewID, "installed": template.ID, "skills": template.SelectedSkills})
		respond(http.StatusOK, out)
	default:
		externalError(w, 400, "invalid_arguments", action+" does not apply to a Crew; rename it with update_crew.")
	}
}
