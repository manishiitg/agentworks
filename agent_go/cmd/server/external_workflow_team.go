package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
)

func externalWorkflowTeamDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	ids := map[string]any{"type": "array", "items": externalString("Saved ID.")}
	member := map[string]any{"type": "object", "additionalProperties": false,
		"properties": map[string]any{"user_id": externalString("User ID from eligible_members; never a display name."), "approval_ids": ids}, "required": []any{"user_id"}}
	for _, key := range []string{"run", "manage_schedules", "approve", "backup", "publish", "notify"} {
		member["properties"].(map[string]any)[key] = map[string]any{"type": "boolean"}
	}
	route := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"step_id": externalString("Parent step ID from routes."), "route_id": externalString("Stable route ID from routes."),
		"contributor_id": externalString("The one exclusive Contributor editor's user_id.")}, "required": []any{"step_id", "route_id", "contributor_id"}}
	policy := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"leader_id":    externalString("Exactly one Leader; transferring ownership retains the outgoing Leader as a Contributor."),
		"revision":     map[string]any{"type": "integer", "minimum": 0},
		"contributors": map[string]any{"type": "array", "items": member}, "routes": map[string]any{"type": "array", "items": route},
		"read_only_step_ids": ids}, "required": []any{"leader_id", "contributors", "routes"}}
	add("workflow_team", "Manage a workflow's goal team directly through the website backend. get returns policy/revision and assignable routes; eligible_members is available to authorized managers. update replaces the complete team policy: copy get's policy, preserve intended members/grants/read-only steps, and supply expected_revision (0 enables team mode). Only an editable workflow owner may enable; the current Leader/admin manages an enabled team. Assigned routes remain exclusive even against the Leader. Mutations require existing Builder consent and manifest file access. reauthorize_schedule reviews the current saved schedule against current grants and stamps its authorization, preserving timing and enabled state. No Builder model turn is started.", true, true, map[string]any{
		"action": map[string]any{"type": "string", "enum": []any{"get", "update", "reauthorize_schedule"}},
		"team":   policy, "expected_revision": map[string]any{"type": "integer", "minimum": 0, "description": "Current team revision from get; required for mutations."},
		"expected_schedule_revision": externalString("Exact schedule revision from get's schedule_revisions; required only for reauthorize_schedule."),
		"schedule_id":                externalString("Existing schedule ID from manage_schedules action=list; only for reauthorize_schedule.")}, "action")
}

func (api *StreamingAPI) externalWorkflowTeamCall(w http.ResponseWriter, r *http.Request, args map[string]any, workflow DiscoveredWorkflow) {
	action := externalArg(args, "action")
	write := action != "get"
	if workflow.Manifest.Kind == "relay" {
		externalError(w, 400, "wrong_product", "Goal teams belong to workflows.")
		return
	}
	if action == "get" && (args["team"] != nil || args["expected_revision"] != nil || args["schedule_id"] != nil || args["expected_schedule_revision"] != nil) ||
		action == "update" && (args["team"] == nil || args["expected_revision"] == nil || args["schedule_id"] != nil || args["expected_schedule_revision"] != nil) ||
		action == "reauthorize_schedule" && (args["team"] != nil || args["expected_revision"] == nil || externalArg(args, "schedule_id") == "" || externalArg(args, "expected_schedule_revision") == "") {
		externalError(w, 400, "invalid_arguments", "get takes no mutation fields; update needs team and expected_revision; reauthorize_schedule needs schedule_id, expected_revision and expected_schedule_revision.")
		return
	}
	claims := GetUserFromContext(r.Context())
	authorize := func(ctx context.Context) (context.Context, error) {
		live := claims
		if write {
			var err error
			live, err = validateBuilderGrant(ctx, claims, workflow.Manifest.ID, workflow.WorkspacePath)
			if err != nil {
				return nil, errors.New("current workflow authoring authority and Builder consent are required")
			}
		} else if claims.AccessToken != nil {
			var err error
			live, err = activeExternalGrantClaims(ctx, claims.AccessToken.ID)
			if err != nil || live.UserID != claims.UserID || !live.AccessToken.AllowsWorkflow(workflow.Manifest.ID) ||
				(!live.AccessToken.Allows("workflows:read") && !live.AccessToken.Allows("runs:execute")) {
				return nil, errors.New("current workflow read consent is required")
			}
		}
		ctx = context.WithValue(ctx, UserContextKey, live)
		if write && !externalFileGuard(ctx, false).Allows("workflow.json", true) {
			return nil, errors.New("the connection's file guard does not allow team policy changes")
		}
		return ctx, nil
	}
	ctx, err := authorize(r.Context())
	if err != nil {
		externalError(w, 403, "forbidden", err.Error())
		return
	}
	release, err := teamLock(ctx, workflow.WorkspacePath)
	if err != nil {
		externalFailure(w, err)
		return
	}
	defer release()
	ctx = context.WithValue(ctx, teamLockHeldKey{}, workflow.WorkspacePath)
	// A queued call must not retain revoked consent or a replaced workflow.
	ctx, err = authorize(ctx)
	if err != nil {
		externalError(w, 403, "forbidden", err.Error())
		return
	}
	current, found, err := ReadWorkflowManifest(ctx, workflow.WorkspacePath)
	if err != nil || !found || current == nil || current.ID != workflow.Manifest.ID || current.Kind == "relay" {
		externalError(w, 404, "workflow_not_found", "Current workflow binding changed.")
		return
	}
	r = r.WithContext(ctx)
	if action == "reauthorize_schedule" {
		if err := reauthorizeWorkflowTeamSchedule(ctx, workflow.WorkspacePath, current, externalArg(args, "schedule_id"), externalInt(args, "expected_revision", -1), externalArg(args, "expected_schedule_revision")); err != nil {
			externalError(w, 409, "schedule_rejected", err.Error())
			return
		}
		externalJSON(w, map[string]any{"workflow_id": current.ID, "schedule_id": externalArg(args, "schedule_id"), "team_revision": current.Team.Revision, "authorized": true})
		return
	}
	method, body := http.MethodGet, any(nil)
	if action == "update" {
		method, body = http.MethodPut, map[string]any{"workspace_path": workflow.WorkspacePath, "team": args["team"], "expected_revision": args["expected_revision"]}
	}
	status, raw := scheduleHandler(r, api.handleWorkflowTeam, method, "/api/workflow/team", nil, url.Values{"workspace_path": {workflow.WorkspacePath}}, body)
	if status >= 400 {
		externalScheduleRespond(w, status, raw)
		return
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		externalFailure(w, err)
		return
	}
	out["workflow_id"] = current.ID
	if action == "get" {
		revisions := map[string]string{}
		for _, schedule := range current.Schedules {
			revision, err := workflowTeamScheduleRevision(schedule)
			if err != nil {
				externalFailure(w, err)
				return
			}
			revisions[schedule.ID] = revision
		}
		out["schedule_revisions"] = revisions
	}
	// Reuse the UI directory but expose only identifiers/names, and only to
	// managers with current authoring consent and manifest access.
	if action == "get" && out["can_manage"] == true {
		live := GetUserFromContext(ctx)
		if live.AccessToken != nil && live.AccessToken.BuilderAccess() && externalFileGuard(ctx, false).Allows("workflow.json", true) {
			status, raw = scheduleHandler(r, api.handleUserDirectory, http.MethodGet, "/api/users/directory", nil, nil, nil)
			var directory struct {
				Users []workflowAccessUser `json:"users"`
			}
			if status >= 400 || json.Unmarshal(raw, &directory) != nil {
				externalError(w, 503, "users_unavailable", "The member directory could not be read.")
				return
			}
			members := []map[string]string{}
			for _, user := range directory.Users {
				members = append(members, map[string]string{"id": user.ID, "username": user.Username})
			}
			out["eligible_members"] = members
		}
	}
	out["note"] = "Team permission changes require explicit schedule reauthorization. Use manage_schedules to inspect schedules; use reauthorize_schedule with the saved team revision after reviewing them."
	externalJSON(w, out)
}
