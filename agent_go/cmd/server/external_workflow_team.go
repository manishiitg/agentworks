package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
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
	add("workflow_team", "Manage a workflow's goal team directly through the website backend. get returns policy/revision and assignable routes; eligible_members is available to authorized managers. update replaces the complete team policy and, when it adds members or changes the Leader, needs confirm = workflow_id (the first reply lists exactly who would be added or removed and the Leader change; route-only edits and removals need no confirmation): copy get's policy, preserve intended members/grants/read-only steps, and supply expected_revision (0 enables team mode). Only an editable workflow owner may enable; the current Leader/admin manages an enabled team. Assigned routes remain exclusive even against the Leader. Mutations require existing Builder consent and manifest file access. reauthorize_schedule reviews the current saved schedule against current grants and stamps its authorization, preserving timing and enabled state. No Builder model turn is started.", true, true, map[string]any{
		"action": map[string]any{"type": "string", "enum": []any{"get", "update", "reauthorize_schedule"}},
		"team":   policy, "expected_revision": map[string]any{"type": "integer", "minimum": 0, "description": "Current team revision from get; required for mutations."},
		"confirm":                    externalString("Must equal workflow_id when update adds members or changes the Leader; the reply of an unconfirmed attempt lists exactly what would change."),
		"expected_schedule_revision": externalString("Exact schedule revision from get's schedule_revisions; required only for reauthorize_schedule."),
		"schedule_id":                externalString("Existing schedule ID from schedules action=list; only for reauthorize_schedule.")}, "action")
}

func (api *StreamingAPI) externalWorkflowTeamCall(w http.ResponseWriter, r *http.Request, args map[string]any, workflow DiscoveredWorkflow) {
	action := externalArg(args, "action")
	write := action != "get"
	if workflow.Manifest.Kind == "relay" {
		externalError(w, 400, "wrong_product", "Goal teams belong to workflows.")
		return
	}
	if action != "update" && args["confirm"] != nil {
		externalError(w, 400, "invalid_arguments", "confirm applies only to update.")
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
		if write && claims.AccessToken != nil {
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
		if write && claims.AccessToken == nil {
			level, current := workflowAccessForWorkspacePath(ctx, claims, workflow.WorkspacePath)
			if current == nil || current.ID != workflow.Manifest.ID || !userAccessForClaims(claims).CanEdit || !userAllowedProduct(claims, "agentworks") || level != WorkflowAccessOwner && level != WorkflowAccessWrite {
				return nil, errors.New("current workflow Builder access is required")
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
	var changes *workflowTeamChanges
	if action == "update" {
		var requested WorkflowTeam
		raw, _ := json.Marshal(args["team"])
		if json.Unmarshal(raw, &requested) != nil {
			externalError(w, 400, "invalid_arguments", "team is not a valid team policy.")
			return
		}
		changes = diffWorkflowTeam(current.Team, requested)
		if changes.needsConfirmation() && externalArg(args, "confirm") != current.ID {
			// Nothing is changed. The reply says exactly what confirm would apply.
			externalJSON(w, map[string]any{"workflow_id": current.ID, "applied": false, "confirmation_required": true,
				"confirm_with": current.ID, "team_changes": changes.describe(),
				"note": "No change was made. Re-send the same update with confirm set to the workflow_id to apply it. Ask the person first if they did not request these changes."})
			return
		}
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
	if changes != nil {
		out["team_changes"] = changes.describe()
	}
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
		if (live.AccessToken == nil || live.AccessToken.BuilderAccess()) && externalFileGuard(ctx, false).Allows("workflow.json", true) {
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
	out["note"] = "Team permission changes require explicit schedule reauthorization. Use schedules action=list to inspect schedules; use reauthorize_schedule with the saved team revision after reviewing them."
	externalJSON(w, out)
}

// workflowTeamChanges is the difference between the saved team and a requested
// policy, in account ids and server usernames, for confirmation and for the reply.
type workflowTeamChanges struct {
	Added      []string
	Removed    []string
	LeaderFrom string
	LeaderTo   string
}

func diffWorkflowTeam(current *WorkflowTeam, requested WorkflowTeam) *workflowTeamChanges {
	had := map[string]bool{}
	c := &workflowTeamChanges{LeaderTo: requested.LeaderID}
	if current != nil {
		c.LeaderFrom = current.LeaderID
		for _, m := range current.Contributors {
			had[m.UserID] = true
		}
	}
	want := map[string]bool{}
	for _, m := range requested.Contributors {
		want[m.UserID] = true
		if !had[m.UserID] && m.UserID != c.LeaderFrom {
			c.Added = append(c.Added, m.UserID)
		}
	}
	for id := range had {
		if !want[id] && id != requested.LeaderID {
			c.Removed = append(c.Removed, id)
		}
	}
	sort.Strings(c.Added)
	sort.Strings(c.Removed)
	return c
}

// A new Leader, or any member who gains workflow access, needs confirmation.
// Turning the team on for the first time is both.
func (c *workflowTeamChanges) needsConfirmation() bool {
	return len(c.Added) > 0 || c.LeaderFrom != c.LeaderTo
}

func (c *workflowTeamChanges) describe() map[string]any {
	label := shareOwnerLabeler()
	people := func(ids []string) []map[string]string {
		out := []map[string]string{}
		for _, id := range ids {
			if id == "" {
				continue
			}
			out = append(out, map[string]string{"user_id": id, "username": label(id)})
		}
		return out
	}
	out := map[string]any{"added": people(c.Added), "removed": people(c.Removed), "leader_changed": c.LeaderFrom != c.LeaderTo}
	if c.LeaderFrom != c.LeaderTo {
		out["leader"] = map[string]any{"from": people([]string{c.LeaderFrom}), "to": people([]string{c.LeaderTo})}
	}
	return out
}
