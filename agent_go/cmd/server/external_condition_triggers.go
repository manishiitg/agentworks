package server

import (
	"context"
	"net/http"
	"strings"
)

// Watchers are caller-owned automation. Their creation uses the same execution
// scopes as a direct call from that source; script reads never reveal another
// person's caller folder.
func externalWatchersAllowed(claims *UserClaims) bool {
	if claims == nil {
		return false
	}
	return claims.AccessToken == nil || externalCodeRunAllowed(claims) || claims.AccessToken.Allows("crews:run") || claims.AccessToken.Allows("runs:execute")
}

func externalWatcherDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	props := conditionToolSchema()["properties"].(map[string]interface{})
	props["project_id"] = externalString("Your own Code project ID (code:run).")
	props["crew_id"] = externalString("Associated Crew ID (crews:run); calls a published function.")
	props["workflow_id"] = externalString("Associated workflow or Flow ID (runs:execute); workflow route/trigger or published Flow entry.")
	add("manage_condition_triggers", conditionToolDescription, true, false, props, "action")
}

func (api *StreamingAPI) externalWatcherCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if !externalWatchersAllowed(claims) || api.productSchedules == nil {
		externalError(w, http.StatusForbidden, "insufficient_scope", "Watchers require the source's execution scope.")
		return
	}
	project, crew, workflow := externalArg(args, "project_id"), externalArg(args, "crew_id"), externalArg(args, "workflow_id")
	count := 0
	for _, id := range []string{project, crew, workflow} {
		if id != "" {
			count++
		}
	}
	if count != 1 {
		externalError(w, 400, "invalid_arguments", "Pass exactly one of project_id, crew_id or workflow_id.")
		return
	}
	profile := "code"
	ctx := r.Context()
	switch {
	case project != "":
		if !externalCodeRunAllowed(claims) {
			externalError(w, 403, "insufficient_scope", "Code watchers require code:run.")
			return
		}
		if _, _, ok := api.externalCodeRunOwnProject(ctx, claims.UserID, project); !ok {
			externalError(w, 404, "not_found", "Code project unavailable.")
			return
		}
	case crew != "":
		if claims.AccessToken != nil && !claims.AccessToken.Allows("crews:run") {
			externalError(w, 403, "insufficient_scope", "Crew watchers require crews:run.")
			return
		}
		if _, _, _, ok := api.externalCrewResolve(ctx, claims, crew); !ok {
			externalError(w, 404, "not_found", "Crew unavailable.")
			return
		}
		profile, project = "work", crew
	case workflow != "":
		if claims.AccessToken != nil && (!claims.AccessToken.Allows("runs:execute") || !claims.AccessToken.AllowsWorkflow(workflow)) {
			externalError(w, 403, "insufficient_scope", "Workflow and Flow watchers require runs:execute for that workflow.")
			return
		}
		profile, project = "agentworks", workflow
		items, err := DiscoverWorkflowManifests(ctx)
		if err != nil {
			externalError(w, 502, "workspace_unavailable", "Cannot discover workflows.")
			return
		}
		found := false
		for _, item := range filterWorkflowManifestsForUser(claims, items) {
			if item.Manifest != nil && item.Manifest.ID == workflow {
				found = true
				if item.Manifest.Kind == "relay" {
					profile = "relays"
				}
				break
			}
		}
		if !found {
			externalError(w, 404, "not_found", "Workflow unavailable.")
			return
		}
	}
	out, err := api.productSchedules.conditionService().manage(internalBotRequestContext(ctx, claims.UserID), claims.UserID, profile, project, args)
	if err != nil {
		externalError(w, 400, "condition_rejected", err.Error())
		return
	}
	externalJSON(w, out)
}

func (api *StreamingAPI) registerWorkflowWatcherTools(reg definitionToolRegistrar, owner, workspace string) error {
	if api.productSchedules == nil {
		return nil
	}
	manifest, found, err := ReadWorkflowManifest(context.Background(), workspace)
	if err != nil || !found {
		return err
	}
	profile := "agentworks"
	if strings.EqualFold(manifest.Kind, "relay") {
		profile = "relays"
	}
	return api.registerConditionTools(reg, owner, profile, workspace, "")
}
