package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"

	"github.com/gorilla/mux"
)

// crew_team_sync: the Team shared folder's clashes over MCP (workspace action=team_clashes / team_resolve), the same
// server handler as the team_sync chat tool and the Sync panel. Any joined member or the owner may use it (members are
// co-owners of the shared folder); the Team's own membership check and the connection's Crew scopes decide.
const externalCrewTeamSyncTool = "crew_team_sync"

func externalCrewTeamSyncDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add(externalCrewTeamSyncTool, "A Crew Team: agents lists its agents as the Team tab shows them (what each uses, its person's \"When others ask\" rule, Answer only / Make changes, what teammates see of its chat); agent_rule (agent_key, when_others_ask, can_make_changes and/or chat_share) sets them for one of your own agents only. directory lists the people who can be added; people lists the Crew's people as the People card shows them (Works with me, Dashboards only, with what each gets); with person and level (works, dashboards or none) it adds, moves or removes one person and first returns what they gain or lose: show it and call again with confirm=true. Any builder adds or removes Works with me (they are invited and join when they accept); Dashboards only, and moving someone between the levels, is the owner's, and needs the Crew on the server. dashboards (kind) lists what this Crew publishes, the same list and lines as the Share page's Dashboards tab: dashboards, artifacts and motion, who can open each, and the owner's public link on a server Crew. settings (on, share=whole or folders, folders, confirm) turns the Team on or off and chooses what it shares, the same as the Team tab: on, whole/folders and removing a folder are the Crew's main owner's; a joined member may add folders. Switching between Folders and the Whole Crew first returns what changes; show it to the person and call again with confirm=true. Open clashes in a Crew Team's shared folder (two people changed the same lines of a file) and how to resolve them. clashes lists each with both versions: current (the file now) and other (the other person's, kept in history), as text for text files up to 2 MiB. resolve (path, clash_id) keeps current, puts the other back (keep=other), or saves merged text (content) as a new revision every copy downloads, and closes the clash. invites lists the Teams you are in and your open invites; accept or decline (crew_id) answers your own invite, the same as the website: accepting makes you a member.", true, false, map[string]any{
		"crew_id":          externalString("Crew ID from workspace action=list, or from team_invites for accept and decline. Not needed for invites."),
		"action":           map[string]any{"type": "string", "enum": []any{"clashes", "resolve", "agents", "agent_rule", "directory", "people", "settings", "dashboards", "invites", "accept", "decline"}},
		"kind":             map[string]any{"type": "string", "enum": []any{"all", "dashboard", "artifact", "motion"}, "description": "dashboards: which kind to list (default all)."},
		"on":               map[string]any{"type": "boolean", "description": "settings: turn the Team on or off (the Crew's main owner only)."},
		"share":            map[string]any{"type": "string", "enum": []any{"whole", "folders"}, "description": "settings: share the Whole Crew, or only the folders named in folders (the main owner only)."},
		"folders":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "settings: folders to share. With share=folders they are the whole list; alone they are added to it."},
		"confirm":          map[string]any{"type": "boolean", "description": "people, settings: the change first returns what the person gains or loses (or what switching Folders and the Whole Crew changes); show it, ask, and call again with confirm=true."},
		"person":           externalString("people: the user_id of one person (find them with directory), or \"everyone\" for everyone on this server (level dashboards or none; the Crew's main owner only). Without it, people returns the list."),
		"level":            map[string]any{"type": "string", "enum": []any{"works", "dashboards", "none"}, "description": "people: works = Works with me (the Team: files, agents, dashboards; invited until they accept), dashboards = Dashboards only (all this Crew's dashboards and asking it, no files; the owner's, on a Crew on the server), none = remove."},
		"create_dashboard": map[string]any{"type": "boolean", "description": "people, level=dashboards: make the Crew's standard dashboard first when it has none (ask the person first)."},
		"agent_key":        externalString("agent_rule: one of your own agents: its key from agents, or its name."),
		"when_others_ask":  map[string]any{"type": "string", "maxLength": whenOthersAskMaxRunes, "description": "agent_rule: what your own agent may do when someone else asks it (empty clears it). An instruction, not a lock."},
		"can_make_changes": map[string]any{"type": "boolean", "description": "agent_rule: for an agent on a Mac, false answers others only (default), true may make changes for them."},
		"chat_share":       map[string]any{"type": "string", "enum": []any{crewChatShareLive, crewChatShareHistory, crewChatShareAgent}, "description": "agent_rule: what teammates see of your own agent's chat: empty for Live chat (the default: history and live updates), history for Chat history (the saved conversation, updated when a turn ends, no live view), agent for Just the agent (the chat is refused; they can still ask the agent)."},
		"path":             externalString("resolve: the clashed file's path in the shared folder."),
		"clash_id":         externalString("resolve: the clash's id from clashes."),
		"keep":             map[string]any{"type": "string", "enum": []any{"current", "other"}, "description": "resolve: keep the file as it is now, or put the other person's version back."},
		"content":          map[string]any{"type": "string", "maxLength": crewTeamMergeMaxBytes, "description": "resolve: merged text to save instead of keep."},
	}, "action")
}

func (api *StreamingAPI) externalCrewTeamSyncCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	project, action := externalArg(args, "crew_id"), externalArg(args, "action")
	// PLAT-746-463: an invited person lists and answers their own invites over MCP,
	// through the website's own routes and checks; no dashboard is involved.
	if action == "invites" {
		api.listCrewTeams(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, claims)))
		return
	}
	if project == "" {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "crew_id is required for "+action+".")
		return
	}
	switch action {
	case "accept", "decline":
		body, _ := json.Marshal(map[string]bool{"accept": action == "accept"})
		req := httptest.NewRequest(http.MethodPost, "/api/crew-teams/"+url.PathEscape(project)+"/invite", bytes.NewReader(body))
		api.handleCrewTeam(w, mux.SetURLVars(req.WithContext(context.WithValue(r.Context(), UserContextKey, claims)), map[string]string{"project": project, "op": "invite"}))
		return
	case "agents":
		req := httptest.NewRequest(http.MethodGet, "/api/crew-teams/"+url.PathEscape(project)+"/tab", nil)
		api.handleCrewTeamTab(w, mux.SetURLVars(req.WithContext(context.WithValue(r.Context(), UserContextKey, claims)), map[string]string{"project": project}))
		return
	case "directory":
		// The people who can be added: only a Team member or the owner may look, and only ids and names come back.
		if t, err := readCrewTeamOrNew(project, claims.UserID); err != nil || !crewTeamCanManage(t, claims.UserID) {
			externalError(w, http.StatusForbidden, "team_owner_required", "Join this Crew's Team to see who can be added.")
			return
		}
		status, raw := scheduleHandler(r, api.handleUserDirectory, http.MethodGet, "/api/users/directory", nil, nil, nil)
		var directory struct {
			Users []workflowAccessUser `json:"users"`
		}
		if status >= 400 || json.Unmarshal(raw, &directory) != nil {
			externalError(w, http.StatusServiceUnavailable, "users_unavailable", "The member directory could not be read.")
			return
		}
		people := []map[string]string{}
		for _, user := range directory.Users {
			people = append(people, map[string]string{"user_id": user.ID, "name": user.Username})
		}
		externalJSON(w, map[string]any{"people": people})
		return
	case "people":
		// The Crew's People list: no person returns the list as the People card shows it; person and level (works,
		// dashboards or none to remove) change one person, first returning what they gain or lose (confirm=true applies).
		// The Team tab's own route, with its checks: any builder adds or removes Works with me; dashboards only is the owner's.
		vars := map[string]string{"project": project}
		ctx := context.WithValue(r.Context(), UserContextKey, claims)
		if externalArg(args, "person") == "" {
			api.handleCrewPeopleView(w, mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/crew-teams/"+url.PathEscape(project)+"/people", nil).WithContext(ctx), vars))
			return
		}
		in := map[string]any{"person": externalArg(args, "person"), "level": externalArg(args, "level")}
		for _, key := range []string{"confirm", "create_dashboard"} {
			if value, ok := args[key]; ok {
				in[key] = value
			}
		}
		body, _ := json.Marshal(in)
		api.handleCrewTeamPeople(w, mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/api/crew-teams/"+url.PathEscape(project)+"/people", bytes.NewReader(body)).WithContext(ctx), vars))
		return
	case "settings":
		// The Team tab's switch and shared folders, the same saved record and checks as the website, the Mac app and the
		// team_sync chat tool (configure): the Crew's main owner alone turns the Team on or off or switches Folders and
		// Whole Crew, a joined member may add folders (crewTeamOwnerOnlyChange is the one check).
		team, err := readCrewTeamOrNew(project, claims.UserID)
		if err != nil || !crewTeamCanManage(team, claims.UserID) {
			externalError(w, http.StatusForbidden, "team_owner_required", "Join this Crew's Team to change its settings.")
			return
		}
		on, hasOn := args["on"].(bool)
		share := externalArg(args, "share")
		var folders []string
		list, _ := args["folders"].([]any)
		for _, item := range list {
			if text, ok := item.(string); ok {
				folders = append(folders, text)
			}
		}
		if !hasOn && share == "" && len(folders) == 0 {
			externalError(w, http.StatusBadRequest, "invalid_arguments", "settings needs on, share or folders.")
			return
		}
		wasWhole := team.Folder.Whole
		if hasOn {
			team.On = on
		}
		switch share {
		case "whole":
			team.Folder = crewTeamFolder{Whole: true}
		case "folders":
			if len(folders) == 0 {
				externalError(w, http.StatusBadRequest, "invalid_arguments", "Name the folders to share, such as folders=[\"team\"].")
				return
			}
			team.Folder = crewTeamFolder{Folders: folders}
		case "":
			if len(folders) > 0 && team.Folder.Whole {
				externalError(w, http.StatusBadRequest, "invalid_arguments", "The Team shares the Whole Crew already.")
				return
			}
			team.Folder.Folders = append(slices.Clone(team.Folder.Folders), folders...)
		default:
			externalError(w, http.StatusBadRequest, "invalid_arguments", "share is whole or folders.")
			return
		}
		if confirm, _ := args["confirm"].(bool); team.Folder.Whole != wasWhole && !confirm {
			query := url.Values{"whole": {map[bool]string{true: "1", false: "0"}[team.Folder.Whole]}}
			if !team.Folder.Whole {
				query["folder"] = team.Folder.Folders
			}
			req := httptest.NewRequest(http.MethodGet, "/api/crew-teams/"+url.PathEscape(project)+"/folder-change?"+query.Encode(), nil)
			rec := httptest.NewRecorder()
			api.handleCrewTeamFolderChange(rec, mux.SetURLVars(req.WithContext(context.WithValue(r.Context(), UserContextKey, claims)), map[string]string{"project": project}))
			if rec.Code >= 300 {
				w.WriteHeader(rec.Code)
				_, _ = w.Write(rec.Body.Bytes())
				return
			}
			externalJSON(w, map[string]any{"confirm_needed": true, "preview": json.RawMessage(rec.Body.Bytes()), "next": "Nothing changed yet. Show the person this summary in plain words and ask. Switch only when they agree: call settings again with the same arguments and confirm=true."})
			return
		}
		body, _ := json.Marshal(team)
		req := httptest.NewRequest(http.MethodPut, "/api/crew-teams/"+url.PathEscape(project), bytes.NewReader(body))
		api.handleCrewTeam(w, mux.SetURLVars(req.WithContext(context.WithValue(r.Context(), UserContextKey, claims)), map[string]string{"project": project}))
		return
	case "dashboards":
		// The Share page's Dashboards tab, with the same server-written lines (crew_dashboards_tab.go).
		req := httptest.NewRequest(http.MethodGet, "/api/crew-teams/"+url.PathEscape(project)+"/dashboards-tab?kind="+url.QueryEscape(externalArg(args, "kind")), nil)
		api.handleCrewTeamDashboardsTab(w, mux.SetURLVars(req.WithContext(context.WithValue(r.Context(), UserContextKey, claims)), map[string]string{"project": project}))
		return
	case "agent_rule":
		in := map[string]any{"agent_key": externalArg(args, "agent_key")}
		for _, key := range []string{"when_others_ask", "can_make_changes", "chat_share"} {
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
		externalError(w, http.StatusBadRequest, "invalid_arguments", "action must be agents, agent_rule, directory, people, settings, dashboards, clashes or resolve.")
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
