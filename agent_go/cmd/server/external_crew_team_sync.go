package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"

	"github.com/gorilla/mux"
)

// crew_team_sync: the Team shared folder's clashes over MCP (workspace action=team_clashes / team_resolve), the same
// server handler as the team_sync chat tool and the Sync panel. Any joined member or the owner may use it (members are
// co-owners of the shared folder); the Team's own membership check and the connection's Crew scopes decide.
const externalCrewTeamSyncTool = "crew_team_sync"

func externalCrewTeamSyncDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add(externalCrewTeamSyncTool, "A project Team: fork_from (agent_key=teammate agent ID) copies a Live shared Mac agent from its validated saved source to your own connected Mac; Hidden chats refuse, source unchanged and no logins/files copied. fork_send (agent_key, person=user_id) sends a copy of your own agent's whole conversation to a teammate in this project, with their acceptance; the request is approval. The copy is a new Only-me agent and yours stays unchanged. No files, memory, skills or logins move. forks lists pending copies; fork_accept, fork_decline and fork_cancel use fork_id. way lists how the Team works (the apps and secrets it shares, and the apps each person signs in to themselves); way_add (item app or secret, id) is \"Add to Team\" for one of your own: an app or secret with no login or an API key is copied to the Team once and you keep yours, an app with a personal sign-in is never copied (each person signs in themselves), a secret's value is never shown; way_remove takes one off; every Team member may. agents lists its agents as the Team tab shows them (what each uses, its person's \"When others ask\" rule, what teammates see of its chat); agent_rule (agent_key, in_team, audience, chosen_people, when_others_ask and/or chat_share) sets them for one of your own agents only: in_team is Shared with team; audience is whole_team (default) or chosen, with chosen_people as user IDs from directory. directory lists the people who can be added; people lists the project's people as the People card shows them (Works with me, Dashboards only, with what each gets); with person and level (works, dashboards or none) it adds, moves or removes one person and first returns what they gain or lose: show it and call again with confirm=true. Any builder adds or removes Works with me (they are invited and join when they accept); Dashboards only, and moving someone between the levels, is the owner's, and needs the project on the server. dashboards (kind) lists what this project publishes, the same list and lines as the Share page's Dashboards tab: dashboards, artifacts and motion, who can open each, and the owner's public link on a server project. settings (on, folders) chooses the complete shared-folder list, with at least one folder, the same as the Team tab. Include existing folders to keep them. Only the project's owner turns the Team on or off or removes a folder; joined members may add folders. Open clashes in a project Team's shared folder (two people changed the same lines of a file) and how to resolve them. clashes lists each with both versions: current (the file now) and other (the other person's, kept in history), as text for text files up to 2 MiB. resolve (path, clash_id) keeps current, puts the other back (keep=other), or saves merged text (content) as a new revision every copy downloads, and closes the clash. invites lists the Teams you are in and your open invites; accept or decline (crew_id) answers your own invite, the same as the website: accepting makes you a member. Asks: work_brief shows my_tasks (asks sent to you that you have not finished) and waiting_on_your_person (requests waiting on you). Needs-you cards: work_answer (need, button, optional helper) answers a card the Team panel shows for you, the same as clicking its button (Ask my agent hands it to one of your agents); your own agents ask by sending to target=person:<name> with buttons. Track work and decisions in the project's own issue tracker (GitHub, Jira or files)."+crewChecklistToolHelp, true, false, crewChecklistProperties(map[string]any{
		"crew_id":          externalString("Project ID from workspace action=list, or from team_invites for accept and decline. Not needed for invites."),
		"action":           map[string]any{"type": "string", "enum": []any{"work_brief", "work_answer", "setup_targets", "setup_apply", "setup_kit", "setup_list", "setup_get", "setup_share", "setup_edit", "setup_remove", "setup_checklist", "fork_from", "fork_send", "forks", "fork_accept", "fork_decline", "fork_cancel", "clashes", "resolve", "agents", "agent_rule", "directory", "people", "settings", "dashboards", "invites", "accept", "decline", "way", "way_add", "way_remove"}},
		"fork_id":          externalString("Pending copy ID from forks."),
		"kind":             map[string]any{"type": "string", "enum": []any{"all", "dashboard", "artifact", "motion"}, "description": "dashboards: which kind to list (default all)."},
		"item":             map[string]any{"type": "string", "enum": []any{"app", "secret"}, "description": "way_add, way_remove: what to add to or take off the Team."},
		"id":               externalString("way_add: your own app's connection id (from your Vault) or your secret's name; way_remove: the id from way."),
		"replace":          map[string]any{"type": "boolean", "description": "way_add secret: replace the value already on the Team."},
		"on":               map[string]any{"type": "boolean", "description": "settings: turn the Team on or off (the project's main owner only)."},
		"folders":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "settings: complete chosen-folder list; at least one. Include existing folders to keep them. Members may add, only the owner may remove."},
		"confirm":          map[string]any{"type": "boolean", "description": "people: show the returned gain/loss preview, ask, and call again with confirm=true."},
		"person":           externalString("people: the user_id of one person (find them with directory), or \"everyone\" for everyone on this server (level dashboards or none; the project's main owner only). Without it, people returns the list."),
		"level":            map[string]any{"type": "string", "enum": []any{"works", "dashboards", "none"}, "description": "people: works = Works with me (the Team: files, agents, dashboards; invited until they accept), dashboards = Dashboards only (all this project's dashboards and asking it, no files; the owner's, on a project on the server), none = remove."},
		"create_dashboard": map[string]any{"type": "boolean", "description": "people, level=dashboards: make the project's standard dashboard first when it has none (ask the person first)."},
		"agent_key":        externalString("agent_rule: one of your own agents: its key from agents, or its name."),
		"in_team":          map[string]any{"type": "boolean", "description": "agent_rule: Shared with team (true) or Only me (false) for your own agent."},
		"audience":         map[string]any{"type": "string", "enum": []any{crewTeamAudienceWholeTeam, crewTeamAudienceChosen}, "description": "agent_rule: who your own In-team agent is shared with: whole_team (the default) or chosen (only chosen_people)."},
		"chosen_people":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "agent_rule: the complete chosen list, user IDs of people in this Team (from directory); used with audience=chosen."},
		"when_others_ask":  map[string]any{"type": "string", "maxLength": whenOthersAskMaxRunes, "description": "agent_rule: what your own agent may do when someone else asks it (empty clears it). An instruction, not a lock."},
		"chat_share":       map[string]any{"type": "string", "enum": []any{crewChatShareLive, crewChatShareAgent}, "description": "agent_rule: what teammates see of your own agent's chat: empty for Live (the default: history and live updates), agent for Hidden (the chat is refused; they can still ask the agent)."},
		"path":             externalString("resolve: the clashed file's path in the shared folder."),
		"clash_id":         externalString("resolve: the clash's id from clashes."),
		"keep":             map[string]any{"type": "string", "enum": []any{"current", "other"}, "description": "resolve: keep the file as it is now, or put the other person's version back."},
		"content":          map[string]any{"type": "string", "maxLength": crewTeamMergeMaxBytes, "description": "resolve: merged text to save instead of keep."},
		"need":             externalString("work_answer: the request's id."),
		"button":           externalString("work_answer: the button you choose."),
		"helper":           externalString("work_answer with Ask my agent: which of your agents handles it (id or name)."),
	}, true), "action")
}

func (api *StreamingAPI) externalCrewTeamSyncCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	project, action := externalArg(args, "crew_id"), externalArg(args, "action")
	if action == "accept" {
		var finish func()
		w, r, finish = startCrewTeamJoin(w, r, project)
		defer finish()
	}

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
	if crewChecklistAction(action) {
		api.externalCrewChecklistCall(w, r, project, action, args)
		return
	}
	if slices.Contains(crewWorkActions, action) {
		api.externalCrewWorkCall(w, r, claims, project, args)
		return
	}
	if action == "fork_from" || action == "forks" || action == "fork_send" || action == "fork_accept" || action == "fork_decline" || action == "fork_cancel" {
		out, err := api.crewAgentForkTool(r, claims, project, action, args)
		if err != nil {
			externalError(w, 403, "agent_fork_unavailable", err.Error())
			return
		}
		externalJSON(w, json.RawMessage(out))
		return
	}
	switch action {
	case "accept", "decline":
		body, _ := json.Marshal(map[string]bool{"accept": action == "accept"})
		req := httptest.NewRequest(http.MethodPost, "/api/crew-teams/"+url.PathEscape(project)+"/invite", bytes.NewReader(body))
		api.handleCrewTeam(w, mux.SetURLVars(req.WithContext(context.WithValue(r.Context(), UserContextKey, claims)), map[string]string{"project": project, "op": "invite"}))
		return
	case "agents":
		// On a Mac the Team lives on the server: the Crew's owner reads it there with their saved connection, under
		// the server's own Team checks, instead of looking for a Team record on the Mac.
		if IsLocalMode() {
			if ownerOfCrewProjectID(project) != claims.UserID {
				externalError(w, http.StatusForbidden, "team_forbidden", "Open your own copy of this project to read its Team.")
				return
			}
			_, binding, connection, err := api.desktopCrewChatBinding(r.Context(), project)
			if err != nil {
				externalError(w, http.StatusConflict, "team_unavailable", err.Error())
				return
			}
			var result json.RawMessage
			if err = crewTeamRemoteJSON(r.Context(), claims.UserID, connection, http.MethodGet, "/api/crew-teams/"+url.PathEscape(binding.PeerProjectID)+"/tab", nil, &result); err != nil {
				log.Printf("[CREW_TEAM] project=%s event=local_team_read_failed peer=%s reason=%q", project, binding.PeerProjectID, err)
				externalError(w, http.StatusConflict, "team_unavailable", err.Error())
				return
			}
			externalJSON(w, result)
			return
		}
		req := httptest.NewRequest(http.MethodGet, "/api/crew-teams/"+url.PathEscape(project)+"/tab", nil)
		api.handleCrewTeamTab(w, mux.SetURLVars(req.WithContext(context.WithValue(r.Context(), UserContextKey, claims)), map[string]string{"project": project}))
		return
	case "way", "way_add", "way_remove":
		out, err := api.crewTeamWayChat(r.Context(), claims, project, action, args)
		if err != nil {
			externalError(w, http.StatusBadRequest, "team_way_refused", err.Error())
			return
		}
		externalJSON(w, json.RawMessage(out))
		return
	case "directory":
		// The people who can be added: only a Team member or the owner may look, and only ids and names come back.
		if t, err := readCrewTeamOrNew(project, claims.UserID); err != nil || !crewTeamCanManage(t, claims.UserID) {
			externalError(w, http.StatusForbidden, "team_owner_required", "Join this project's Team to see who can be added.")
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
		// team_sync chat tool (configure): the owner turns the Team on/off or removes folders;
		// joined members may add folders (crewTeamOwnerOnlyChange is the one check).
		team, err := readCrewTeamOrNew(project, claims.UserID)
		if err != nil || !crewTeamCanManage(team, claims.UserID) {
			externalError(w, http.StatusForbidden, "team_owner_required", "Join this project's Team to change its settings.")
			return
		}
		on, hasOn := args["on"].(bool)
		value, hasFolders := args["folders"]
		if !hasOn && !hasFolders {
			externalError(w, http.StatusBadRequest, "invalid_arguments", "settings needs on or folders.")
			return
		}
		if hasOn {
			team.On = on
		}
		if hasFolders {
			list, ok := value.([]any)
			if !ok || len(list) == 0 {
				externalError(w, http.StatusBadRequest, "invalid_arguments", "Choose at least one folder, such as folders=[\"team\"].")
				return
			}
			folders := make([]string, 0, len(list))
			for _, item := range list {
				folder, ok := item.(string)
				if !ok {
					externalError(w, http.StatusBadRequest, "invalid_arguments", "Choose folder names inside this project.")
					return
				}
				folders = append(folders, folder)
			}
			team.Folder = crewTeamFolder{Folders: folders}
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
		for _, key := range []string{"in_team", "audience", "chosen_people", "when_others_ask", "chat_share"} {
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

// externalCrewWorkCall: the Team's asks over MCP, as the person (no agent). A Team-on
// Crew uses the Team under its membership checks (forwarded to the server from a
// Mac); a Team-off Crew shows its owner their own agents' asks.
func (api *StreamingAPI) externalCrewWorkCall(w http.ResponseWriter, r *http.Request, claims *UserClaims, project string, args map[string]any) {
	actor := crewWorkPersonActor(claims.UserID)
	off, err := crewWorkTeamOff(r.Context(), claims.UserID, project)
	if err != nil {
		externalError(w, http.StatusConflict, "work_unavailable", err.Error())
		return
	}
	if off {
		if ownerOfCrewProjectID(project) != claims.UserID {
			externalError(w, http.StatusForbidden, "work_forbidden", "This project's Team is off. Only its owner has asks here.")
			return
		}
		out, err := crewWorkPersonal(r.Context(), project, claims.UserID, actor, args)
		if err != nil {
			externalError(w, 400, "work_refused", err.Error())
			return
		}
		externalJSON(w, json.RawMessage(out))
		return
	}
	in := crewWorkInputFromArgs(args)
	if IsLocalMode() {
		_, binding, connection, err := api.desktopCrewChatBinding(r.Context(), project)
		if err != nil {
			externalError(w, http.StatusConflict, "work_unavailable", err.Error())
			return
		}
		route := "/api/crew-teams/" + url.PathEscape(binding.PeerProjectID) + "/work"
		var body any
		method := http.MethodPost
		if in.Op == "work_brief" {
			method = http.MethodGet
			route += "?" + url.Values{"op": {in.Op}, "chat_key": {actor.ChatKey}}.Encode()
		} else {
			body = struct {
				crewWorkInput
				ChatKey string `json:"chat_key"`
			}{in, actor.ChatKey}
		}
		var out json.RawMessage
		if err := crewTeamRemoteJSON(r.Context(), claims.UserID, connection, method, route, body, &out); err != nil {
			externalError(w, 400, "work_refused", err.Error())
			return
		}
		externalJSON(w, out)
		return
	}
	team, err := readCrewTeam(project)
	if err != nil {
		externalError(w, http.StatusNotFound, "team_unavailable", "This project's Team is not available to you")
		return
	}
	out, err := crewWorkDo(r.Context(), crewWorkScope{Project: project, Team: &team}, claims.UserID, actor, in)
	if err != nil {
		externalError(w, 400, "work_refused", err.Error())
		return
	}
	externalJSON(w, out)
}
