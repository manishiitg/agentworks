package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"

	"github.com/gorilla/mux"
)

// crew_team_sync: the Team shared folder's clashes over MCP (workspace action=team_clashes / team_resolve), the same
// server handler as the team_sync chat tool and the Sync panel. Any joined member or the owner may use it (members are
// co-owners of the shared folder); the Team's own membership check and the connection's Crew scopes decide.
const externalCrewTeamSyncTool = "crew_team_sync"

func externalCrewTeamSyncDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add(externalCrewTeamSyncTool, "A Crew Team: agents lists its agents as the Team tab shows them (what each uses, its person's \"When others ask\" rule, Answer only / Make changes); agent_rule (agent_key, when_others_ask and/or can_make_changes) sets them for one of your own agents only. Open clashes in a Crew Team's shared folder (two people changed the same lines of a file) and how to resolve them. clashes lists each with both versions: current (the file now) and other (the other person's, kept in history), as text for text files up to 2 MiB. resolve (path, clash_id) keeps current, puts the other back (keep=other), or saves merged text (content) as a new revision every copy downloads, and closes the clash.", true, false, map[string]any{
		"crew_id":          externalString("Crew ID from workspace action=list."),
		"action":           map[string]any{"type": "string", "enum": []any{"clashes", "resolve", "agents", "agent_rule"}},
		"agent_key":        externalString("agent_rule: one of your own agents: its key from agents, or its name."),
		"when_others_ask":  map[string]any{"type": "string", "maxLength": whenOthersAskMaxRunes, "description": "agent_rule: what your own agent may do when someone else asks it (empty clears it). An instruction, not a lock."},
		"can_make_changes": map[string]any{"type": "boolean", "description": "agent_rule: for an agent on a Mac, false answers others only (default), true may make changes for them."},
		"path":             externalString("resolve: the clashed file's path in the shared folder."),
		"clash_id":         externalString("resolve: the clash's id from clashes."),
		"keep":             map[string]any{"type": "string", "enum": []any{"current", "other"}, "description": "resolve: keep the file as it is now, or put the other person's version back."},
		"content":          map[string]any{"type": "string", "maxLength": crewTeamMergeMaxBytes, "description": "resolve: merged text to save instead of keep."},
	}, "crew_id", "action")
}

func (api *StreamingAPI) externalCrewTeamSyncCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	project, action := externalArg(args, "crew_id"), externalArg(args, "action")
	switch action {
	case "agents":
		req := httptest.NewRequest(http.MethodGet, "/api/crew-teams/"+url.PathEscape(project)+"/tab", nil)
		api.handleCrewTeamTab(w, mux.SetURLVars(req.WithContext(context.WithValue(r.Context(), UserContextKey, claims)), map[string]string{"project": project}))
		return
	case "agent_rule":
		in := map[string]any{"agent_key": externalArg(args, "agent_key")}
		for _, key := range []string{"when_others_ask", "can_make_changes"} {
			if value, ok := args[key]; ok {
				in[key] = value
			}
		}
		body, _ := json.Marshal(in)
		req := httptest.NewRequest(http.MethodPost, "/api/crew-teams/"+url.PathEscape(project)+"/agent-rule", bytes.NewReader(body))
		api.handleCrewTeamMemberAction(w, mux.SetURLVars(req.WithContext(context.WithValue(r.Context(), UserContextKey, claims)), map[string]string{"project": project, "op": "agent-rule"}))
		return
	case "clashes", "resolve":
	default:
		externalError(w, http.StatusBadRequest, "invalid_arguments", "action must be agents, agent_rule, clashes or resolve.")
		return
	}
	team, err := readCrewTeam(project)
	if err != nil {
		// The same refusal as any team the caller is not in, so a private Crew's Team is not revealed.
		externalError(w, http.StatusForbidden, "team_folder_forbidden", "This team is not shared with you; ask its owner to add you.")
		return
	}
	q := url.Values{"folder_id": {team.Folder.ID}}
	method, op := http.MethodGet, "clashes"
	var body []byte
	if action == "clashes" {
		q.Set("with_text", "1")
	} else {
		method, op = http.MethodPost, "resolve"
		in := map[string]any{"path": externalArg(args, "path"), "keep": externalArg(args, "keep"), "transfer_id": newCrewTeamTransfer()}
		if id, err := strconv.ParseUint(externalArg(args, "clash_id"), 10, 64); err == nil {
			in["winning_revision"] = id
		}
		if content, ok := args["content"].(string); ok {
			in["content"] = content
		}
		body, _ = json.Marshal(in)
	}
	req := httptest.NewRequest(method, "/api/crew-teams/"+url.PathEscape(project)+"/"+op+"?"+q.Encode(), bytes.NewReader(body))
	req = mux.SetURLVars(req.WithContext(context.WithValue(r.Context(), UserContextKey, claims)), map[string]string{"project": project, "op": op})
	api.handleCrewTeamHistory(w, req)
}
