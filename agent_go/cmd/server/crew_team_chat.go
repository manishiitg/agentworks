package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// The same actions as the Team Files/Sync panels. Shared-agent turns retain the
// owner's file authority and the existing per-message change instructions.
func (api *StreamingAPI) registerCrewTeamChatTool(reg definitionToolRegistrar, gate *productToolGate, claims *UserClaims, readOnly bool, sessionID, root, sharedSender, sourceKey string) error {
	if claims == nil || claims.UserID == "" {
		return nil
	}
	gate.Declare("team_sync")
	return reg.RegisterCustomToolWithTimeout("team_sync", "Work with this Crew's Team. fork_from (agent_key=teammate agent ID) copies a Live shared Mac agent from its validated saved source to your own connected Mac; Hidden chats refuse, source unchanged and no logins/files copied. fork_send (person=user_id) sends a whole-conversation copy of your current agent to a named teammate in this Crew; this request is approval. Their copy starts Only me; yours stays unchanged. forks lists pending copies; fork_accept, fork_decline and fork_cancel use fork_id. No files, memory, skills or logins move. Tasks (this Crew's work): a task is an AgentBridge request thread. work_brief at the start of a session returns my_tasks (request threads you are doing; reply in their inbox_id), team_requests (open Team tasks; take one by replying in its inbox_id with messages status=in_progress) and waiting_on_your_person (requests your person must answer). Start work nobody asked for with messages send target=self and the issue number; hand work to another agent with messages ask (or send with expects_reply=true); a plain send is only a notice. Reply in the task's inbox: status=in_progress, blocked or needs_you, then done naming the PR or commit; decision=true with a short why records a choice others must follow; ref=the parent message_id splits work. Blocked on a person (a review, an approval, a decision)? Send to messages target=person:<name> with one to three buttons such as Approve, Not now, Ask my agent; it pins a Needs-you card in the Team panel, only that person can answer, and their click comes back as the reply in that thread. work_answer (need, button, optional helper = one of your agents) answers a card that is for your person, when they ask you to. When the team uses GitHub, Jira or files, that system stays the source of truth: use its label and link, and update it with your own tools. finding posts one short line from a scheduled run to Team Activity under the schedule's name; only during a scheduled run, and a run with nothing to report posts nothing. These are explicit Team entries visible to teammates, never private chat excerpts: do not include secrets. Use them as your actual current chat; no agent_key is needed. A finding from your own Only-me or unregistered agent is visible only to you. A shared ask can post only publicly as its trusted In-team source. Use your team's shared folder and Sync history. status lists your teams; choose project from that list. recent, clashes, compare (path, left, right), read_revision (path, revision), files (at most 200 entries a call: next_offset in the reply is the offset for the next page; path narrows to one folder) and read_file read the shared folder. save_file (path, content, base from files) is a normal save; stale saves keep the previous version in history. clashes lists open clashes (two people changed the same lines of a file) with both versions: current (the file now) and other (the other person's, kept in history), as text for text files up to 2 MiB. resolve (path, clash_id from clashes) keeps current, puts the other back (keep=other), restores keep=revision:N, or saves your merged text (content); each change is a NEW revision every copy downloads, and the clash is closed. restore uses revision and saves it as a NEW revision. leave removes you from a joined Team; agent_visibility (agent_key, in_team) shares one of your own agents with the Team (in_team=true) or not (false), (new agents are shared; keep at least one shared). agents lists the Team's agents as the Team tab shows them, each with its key (your own ones have can_edit_rule). agent_rule (agent_key, in_team, audience, chosen_people, when_others_ask, can_make_changes and/or chat_share) sets, for your own agent, whether it is shared with the Team, who it is shared with (whole_team, or chosen with chosen_people as user IDs from directory), what it does when someone else asks it and what teammates see of its chat; agent_key is its key from agents, or simply its name. directory lists company people (find a person by name there, then use their user_id). configure, like the Team tab: people replaces the roster with user_id (anyone in the Team; the owner always stays); folders=[…] sets the complete chosen-folder list (at least one; include existing folders to keep them). Anyone in the Team may add folders; a missing folder is made. Only the Crew's owner stops sharing an existing folder or turns the Team on or off (on=true/false; on starts with team/ when nothing is shared). Chats, personal memory, app files, passwords and keys stay private. A person added is invited and joins only when they accept; every member reads and writes the shared folder. way lists how this Team works (the apps and secrets it shares, and the apps each person signs in to themselves); way_add (item app or secret, id) adds one of your own with \"Add to Team\": an app or secret with no login or an API key is copied to the Team once and you keep yours; an app with a personal sign-in is never copied, it is listed as \"each person signs in themselves\"; the secret's value is never shown. On a Mac, a key kept in this Mac's own Vault goes straight from the Mac to the Team's server and never passes through the chat: give only its name (the Team agent can tell you which key names it needs). way_remove takes one off. Every Team member may do both. status with project shows the Team. status also lists your open invites; accept or decline one with its project (your own answer, from your own chat). This shares the selected folder with those enabled people; other files stay outside the Team folder. Only your admitted teams are available. Other people's private chats and sign-ins are never transferred. On this Mac a paired project uses its existing server connection."+crewTaskToolHelp+crewChecklistToolHelp, map[string]any{"type": "object", "properties": crewChecklistProperties(crewTaskProperties(map[string]any{
		"action":              map[string]any{"type": "string", "enum": []string{"fork_from", "fork_send", "forks", "fork_accept", "fork_decline", "fork_cancel", "work_brief", "work_answer", "tasks", "task", "task_update", "features", "feature", "feature_join", "feature_update", "finding", "status", "recent", "clashes", "compare", "read_revision", "resolve", "restore", "files", "read_file", "save_file", "directory", "people", "configure", "accept", "decline", "leave", "agents", "agent_visibility", "agent_rule", "dashboards", "way", "way_add", "way_remove", "receive_online", "setup_targets", "setup_apply", "setup_kit", "setup_list", "setup_get", "setup_share", "setup_edit", "setup_remove", "setup_checklist"}},
		"fork_id":             map[string]any{"type": "string", "description": "Pending copy ID from forks."},
		"line":                map[string]any{"type": "string", "maxLength": 320, "description": "finding: one short line."},
		"need":                map[string]any{"type": "string", "description": "work_answer: the request's id (from work_brief waiting_on_your_person)."},
		"button":              map[string]any{"type": "string", "description": "work_answer: the button your person chose."},
		"comment":             map[string]any{"type": "string", "description": "work_answer: the note your person wrote, sent back with the button (or alone, with no button)."},
		"helper":              map[string]any{"type": "string", "description": "work_answer with Ask my agent: which of your agents handles it (id or name)."},
		"network":             map[string]any{"type": "string", "description": "Company name, if this Mac is connected to several companies and this project has no connection."},
		"agent_key":           map[string]any{"type": "string", "description": "agent_visibility, agent_rule: one of your own agents, by its key from agents (agent_rule also takes its name)."},
		"in_team":             map[string]any{"type": "boolean", "description": "agent_visibility, agent_rule: Shared with team (true) or Only me (false) for your own agent."},
		"audience":            map[string]any{"type": "string", "enum": []string{crewTeamAudienceWholeTeam, crewTeamAudienceChosen}, "description": "agent_rule: who your own In-team agent is shared with: whole_team (the default) or chosen (only chosen_people)."},
		"chosen_people":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "agent_rule: the complete chosen list, user IDs of people in this Team (from directory); used with audience=chosen."},
		"when_others_ask":     map[string]any{"type": "string", "maxLength": whenOthersAskMaxRunes, "description": "agent_rule: what your own agent may do when someone else asks it (empty clears it). An instruction, not a lock."},
		"can_make_changes":    map[string]any{"type": "boolean", "description": "agent_rule: for an agent on a Mac, false answers others only (the default), true may make changes for them."},
		"chat_share":          map[string]any{"type": "string", "enum": []string{crewChatShareLive, crewChatShareAgent}, "description": "agent_rule: what teammates see of your own agent's chat: empty for Live (the default: history and live updates), agent for Hidden (the chat is refused; they can still ask the agent)."},
		"on":                  map[string]any{"type": "boolean", "description": "configure: the Team switch (the owner only)."},
		"person":              map[string]any{"type": "string", "description": "people: the user_id of one person (from directory), or \"everyone\" for everyone on this server (level dashboards or none; the Crew's main owner only); without it, people lists the Crew's people as the People card shows them."},
		"level":               map[string]any{"type": "string", "enum": []string{"works", "dashboards", "none"}, "description": "people: works = Works with me (the Team), dashboards = Dashboards only (the owner's; all this Crew's dashboards and asking it, no files), none = remove. The change first returns what the person gains or loses: show it, ask, then call again with confirm=true."},
		"create_dashboard":    map[string]any{"type": "boolean", "description": "people, level=dashboards: make the Crew's standard dashboard first when it has none (ask the person first)."},
		"kind":                map[string]any{"type": "string", "enum": []string{"all", "dashboard", "artifact", "motion", "automation"}, "description": "dashboards: which kind to list (default all)."},
		"confirm":             map[string]any{"type": "boolean", "description": "people: show the returned gain/loss preview, ask, then confirm the same change."},
		"item":                map[string]any{"type": "string", "enum": []string{"app", "secret"}, "description": "way_add, way_remove: what to add to or take off the Team."},
		"id":                  map[string]any{"type": "string", "description": "way_add: your own app's connection id (from your Vault) or your secret's name; way_remove: the id from way."},
		"source_project_id":   map[string]any{"type": "string", "description": "receive_online: your own current source Crew ID; on this Mac use its local ID."},
		"item_id":             map[string]any{"type": "string", "description": "receive_online: published item ID, or a schedule/Watcher/webhook ID."},
		"item_revision":       map[string]any{"type": "string", "description": "receive_online: published revision or automation revision from its source."},
		"submission_id":       map[string]any{"type": "string"},
		"preview":             map[string]any{"type": "boolean", "description": "receive_online: inspect only, creating no Team home or item."},
		"include_automations": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "receive_online: optionally choose a subset of preview.dependencies.automations; default empty."},
		"replace":             map[string]any{"type": "boolean", "description": "way_add secret: replace the value already on the Team."},
		"project":             map[string]any{"type": "string", "description": "Team project_id returned by status; defaults to this project's team."},
		"path":                map[string]any{"type": "string"}, "revision": map[string]any{"type": "integer"}, "offset": map[string]any{"type": "integer", "description": "files: where the page starts (next_offset from the previous reply)."}, "left": map[string]any{"type": "integer"}, "right": map[string]any{"type": "integer"}, "winning_revision": map[string]any{"type": "integer"}, "clash_id": map[string]any{"type": "string", "description": "resolve: the clash's id from clashes."},
		"folders": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"folder":  map[string]any{"type": "string", "description": "One folder; the same complete selection as folders=[folder]."}, "people": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"user_id": map[string]any{"type": "string"}}, "required": []string{"user_id"}}},
		"keep": map[string]any{"type": "string", "description": "resolve: current, other or revision:N."}, "content": map[string]any{"type": "string", "description": "save_file: the file's text. resolve: the merged text to save instead of keep."}, "base": map[string]any{"type": "string", "description": "Last hash from files; empty for a new file."},
	}), false), "required": []string{"action"}}, func(ctx context.Context, args map[string]any) (string, error) {
		caller := GetUserFromContext(ctx)
		if caller == nil {
			caller = claims
		}
		if caller.UserID != claims.UserID {
			return "", fmt.Errorf("Open Sync in your own project chat to use this team's folder")
		}
		action := externalArg(args, "action")
		writes := action == "resolve" || action == "restore" || action == "save_file" || action == "configure" || action == "leave" || action == "agent_visibility" || action == "agent_rule" || action == "people" && externalArg(args, "person") != ""
		if writes && readOnly {
			return "", fmt.Errorf("This agent can explain the clash. Ask the owner to allow it to make changes, or resolve it in your own chat")
		}
		manifest, e := readCrewProjectManifests(ctx, crewProfileID, root)
		if e != nil {
			return "", e
		}

		if crewChecklistAction(action) {
			if sourceKey != "" && sharedSender == "" {
				return "", fmt.Errorf("Open Team setups from your own agent")
			}
			if out, handled, err := crewChecklistHomeAction(ctx, caller, root, sharedSender, action, args, readOnly, api); handled {
				return out, err
			}
		}
		// Only the Team home's own virtual runtime may operate its source
		// Team setup. It reads/copies Team-owned keys, never the asker's Vault.
		if action == "way" || action == "way_add" || action == "way_remove" {
			if source, ok := teamHomeSource(root); ok && crewTeamHomeOwnsSetup(source, caller.UserID) {
				if sharedSender != "" && (!source.member(sharedSender) || !crewTeamAccountActive(sharedSender)) {
					return "", fmt.Errorf("Join this Team to configure its shared agent")
				}
				if requested := externalArg(args, "project"); requested != "" && requested != manifest.ID && requested != source.ProjectID {
					return "", fmt.Errorf("Use this Team's setup only")
				}
				log.Printf("[TEAM_HOME] team=%s event=setup action=%s person=%s", source.ProjectID, action, firstNonEmptyTrimmed(sharedSender, caller.UserID))
				return api.crewTeamWayChat(ctx, caller, source.ProjectID, action, args)
			}
		}
		project := externalArg(args, "project")
		if project == "" {
			project = manifest.ID
		}
		// #2993: the Team agent works in the Team's shared folder (a handoff lands there). From its home, the
		// folder actions use the source Team; the asking member must still be a joined member of it.
		homeFiles := false
		if action == "files" || action == "read_file" || action == "save_file" {
			if source, ok := teamHomeSource(root); ok && crewTeamHomeOwnsSetup(source, caller.UserID) && (project == manifest.ID || project == source.ProjectID) {
				if sharedSender != "" && (!source.member(sharedSender) || !crewTeamAccountActive(sharedSender)) {
					return "", fmt.Errorf("Join this Team to use its shared folder through the Team agent")
				}
				project, homeFiles = source.ProjectID, true
				log.Printf("[TEAM_HOME] team=%s event=folder action=%s person=%s", source.ProjectID, action, firstNonEmptyTrimmed(sharedSender, caller.UserID))
			}
		}

		if (strings.HasPrefix(action, "work_") || crewTaskOp(action) || crewFeatureOp(action)) && (project == manifest.ID || project == "") {
			off, err := crewWorkTeamOff(ctx, caller.UserID, manifest.ID)
			if err != nil {
				return "", err
			}
			if off {
				if sharedSender != "" || sourceKey != "" {
					return "", fmt.Errorf("This Crew's Team is off. Read your tasks from your own chat")
				}
				key, title, err := crewOwnChatForSession(ctx, caller, manifest.ID, sessionID)
				if err != nil {
					return "", err
				}
				return crewWorkPersonal(ctx, manifest.ID, caller.UserID, crewTeamRegisteredAgent{OwnerID: caller.UserID, ChatKey: key, Name: title}, args)
			}
		}
		if sharedSender != "" && project != manifest.ID && !homeFiles && !strings.HasPrefix(action, "work_") {
			return "", fmt.Errorf("This shared agent uses only this project's team. Open your own chat for another team")
		}
		currentTeamProject := manifest.ID
		var connection *desktopConnection
		if IsLocalMode() {
			b, err := crewroot.ReadSync(manifest.ID)
			if err != nil {
				return "", err
			}
			id := ""
			if b != nil && b.Side == "local" {
				id = b.ConnectionID
			} else {
				connections, err := readDesktopConnections(caller.UserID)
				if err != nil {
					return "", err
				}
				wanted := externalArg(args, "network")
				names := []string{}
				for _, c := range connections {
					label := desktopConnectionLabel(c)
					names = append(names, label)
					if len(connections) == 1 || strings.EqualFold(wanted, label) || strings.EqualFold(wanted, c.Name) {
						id = c.ID
					}
				}
				if id == "" {
					return "", fmt.Errorf("Choose your company from Connected servers first. Available: %s", strings.Join(names, ", "))
				}
			}
			c, err := desktopConnectionFor(caller.UserID, id)
			if err != nil {
				return "", err
			}
			connection = &c
			if b != nil && b.Side == "local" {
				currentTeamProject = b.PeerProjectID
			}
			if b != nil && project == manifest.ID {
				project = b.PeerProjectID
			}
		}
		if (strings.HasPrefix(action, "work_") || action == "finding") && project != currentTeamProject {
			return "", fmt.Errorf("Use the work log as your current agent in this Crew's Team, not another Crew")
		}
		if crewChecklistAction(action) {
			return api.crewChecklistChat(ctx, caller, connection, manifest.ID, project, sessionID, sharedSender, sourceKey, readOnly, action, args)
		}
		if action == "receive_online" {
			if sharedSender != "" || readOnly {
				return "", fmt.Errorf("Add to Team from your own source chat")
			}
			if externalArg(args, "kind") == "automation" {
				args["source_project_id"] = project
			} else if externalArg(args, "source_project_id") == "" {
				args["source_project_id"] = manifest.ID
			}
			return api.teamOnlineChat(ctx, caller, connection, project, args)
		}
		if action == "fork_from" || action == "forks" || strings.HasPrefix(action, "fork_") {
			if sharedSender != "" || sourceKey != "" || readOnly && action != "forks" || project != currentTeamProject {
				return "", fmt.Errorf("Fork from your own chat in this Crew")
			}
			ownArgs := make(map[string]any, len(args)+1)
			for key, value := range args {
				ownArgs[key] = value
			}
			if action == "fork_send" {
				actor, err := api.crewTeamActivityAgentForSession(ctx, caller, connection, manifest.ID, project, sessionID, "")
				if err != nil {
					return "", err
				}
				if actor.ID == "" {
					return "", fmt.Errorf("Open this agent in the Team's Agents page first")
				}
				ownArgs["agent_key"] = actor.ID
			}
			request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/", nil)
			return api.crewAgentForkTool(request, caller, manifest.ID, action, ownArgs)
		}
		if action == "way" || action == "way_add" || action == "way_remove" {
			if sharedSender != "" {
				return "", fmt.Errorf("Change the Team from your own chat")
			}
			if readOnly && action != "way" {
				return "", fmt.Errorf("Change the Team's tools from your own chat")
			}
			if connection != nil {
				return api.crewTeamWayRemote(ctx, caller.UserID, *connection, project, action, args)
			}
			return api.crewTeamWayChat(ctx, caller, project, action, args)
		}
		if action == "configure" || action == "directory" || action == "people" {
			if sharedSender != "" {
				return "", fmt.Errorf("Change the Team from your own chat")
			}
			return api.crewTeamConfigureChat(ctx, caller, connection, manifest.ID, project, action, args)
		}
		op, method := action, http.MethodGet
		q := url.Values{}
		if path := externalArg(args, "path"); path != "" {
			q.Set("path", path)
		}
		for _, key := range []string{"revision", "left", "right"} {
			if _, ok := args[key]; ok {
				q.Set(key, strconv.Itoa(listIntArg(args, key)))
			}
		}
		var body []byte
		if (action == "accept" || action == "decline") && sharedSender != "" {
			return "", fmt.Errorf("Answer a Team invite in your own chat")
		}
		if action == "accept" && IsLocalMode() && connection != nil {
			if readOnly {
				return "", fmt.Errorf("Accept the Team invite in your own chat.")
			}
			// One action: accepting creates the Team's own project, its shared folder in the default folder or the one given.
			method := http.MethodPost
			input := crewTeamJoinChoice{Folder: externalArg(args, "folder")}
			body, _ := json.Marshal(input)
			req := httptest.NewRequest(method, "/api/desktop-connections/"+url.PathEscape(connection.ID)+"/crew-teams/"+url.PathEscape(project)+"/join", bytes.NewReader(body)).WithContext(context.WithValue(ctx, UserContextKey, caller))
			req = mux.SetURLVars(req, map[string]string{"connection": connection.ID, "project": project})
			w := httptest.NewRecorder()
			api.joinCrewTeamOnMac(w, req)
			if w.Code >= 300 {
				return "", fmt.Errorf("%s", laptopActionErrorText(w.Body.String()))
			}
			return w.Body.String(), nil
		}

		// Imported/forked chats may already exist before their first resolve call,
		// so an explicit agent-management request reconciles the current local roster.
		if connection != nil && (action == "agents" || action == "agent_visibility" || action == "agent_rule") {
			api.refreshMacSharedPurposes(ctx, manifest.ID)
		}
		switch action {
		case "work_brief", "work_answer", "tasks", "task", "task_update", "features", "feature", "feature_join", "feature_update":
			actor, err := api.crewTeamActivityAgentForSession(ctx, caller, connection, manifest.ID, project, sessionID, sourceKey)
			if err != nil {
				return "", err
			}
			op = "work"
			if action == "work_brief" {
				q.Set("op", action)
				q.Set("agent_id", actor.ID)
				q.Set("chat_key", actor.ChatKey)
				q.Set("agent", actor.Name)
				if sourceKey != "" {
					q.Set("shared_ask", "1")
				}
				break
			}
			method = http.MethodPost
			in := crewWorkInputFromArgs(args)
			in.Op = action
			body, _ = json.Marshal(struct {
				crewWorkInput
				AgentID   string `json:"agent_id"`
				ChatKey   string `json:"chat_key"`
				Agent     string `json:"agent"`
				SharedAsk bool   `json:"shared_ask"`
			}{in, actor.ID, actor.ChatKey, actor.Name, sourceKey != ""})
		case "finding":
			// Only a schedule's own turn; the schedule name comes from the server's record of that turn, never from the model.
			name, err := crewScheduleTurnName(sessionID, manifest.ID, caller.UserID)
			if err != nil {
				return "", err
			}
			actor, err := api.crewTeamActivityAgentForSession(ctx, caller, connection, manifest.ID, project, sessionID, sourceKey)
			if err != nil {
				return "", err
			}
			if connection == nil {
				if _, err := writeCrewTeamActivityPostAs(ctx, project, caller.UserID, actor, "finding", externalArg(args, "line"), "", name); err != nil {
					return "", fmt.Errorf("Could not save activity. Use one short line from your own agent in this Crew.")
				}
				if !actor.InTeam {
					return "Saved. Only you see it, because this agent isn't shared with the team.", nil
				}
				return "Posted to the Team as a finding from " + name + ".", nil
			}
			// A Mac Crew's Team is on the server: the Mac posts through the same route with its pair header.
			op, method = "activity-posts", http.MethodPost
			body, _ = json.Marshal(map[string]any{"kind": "finding", "agent_id": actor.ID, "chat_key": actor.ChatKey, "agent": actor.Name, "shared_ask": sourceKey != "", "line": externalArg(args, "line"), "schedule": name})
		case "status":
			op = ""
		case "dashboards":
			op = "dashboards-tab" // the Share page's Dashboards tab, the same lines (crew_dashboards_tab.go)
			if kind := externalArg(args, "kind"); kind != "" {
				q.Set("kind", kind)
			}
		case "agents":
			op = "tab" // the Team tab's rows, each agent with its key (crewTeamOwnAgent also takes its name)
		case "recent":
			op = "events"
		case "read_revision":
			op = "history"
		case "read_file":
			op = "file"
		case "save_file":
			op = "file"
			method = http.MethodPut
			body = []byte(externalArg(args, "content"))
			q.Set("base", externalArg(args, "base"))
		case "resolve", "restore":
			op = "resolve"
			method = http.MethodPost
			keep := externalArg(args, "keep")
			if action == "restore" {
				keep = "revision:" + strconv.Itoa(listIntArg(args, "revision"))
			}
			clash := listIntArg(args, "winning_revision")
			if id, err := strconv.Atoi(externalArg(args, "clash_id")); err == nil && id > 0 {
				clash = id
			}
			resolution := map[string]any{"path": externalArg(args, "path"), "keep": keep, "winning_revision": clash, "transfer_id": newCrewTeamTransfer()}
			if content, ok := args["content"].(string); ok && action == "resolve" {
				resolution["content"] = content
			}
			body, _ = json.Marshal(resolution)
		case "leave", "agent_visibility":
			if sharedSender != "" {
				return "", fmt.Errorf("Change your Team access in your own chat")
			}
			op = strings.ReplaceAll(action, "_", "-")
			method = http.MethodPost
			body, _ = json.Marshal(map[string]any{"agent_key": externalArg(args, "agent_key"), "in_team": args["in_team"]})
		case "agent_rule":
			if sharedSender != "" {
				return "", fmt.Errorf("Change your agent's rule in your own chat")
			}
			op, method = "agent-rule", http.MethodPost
			rule := map[string]any{"agent_key": externalArg(args, "agent_key")}
			for _, key := range []string{"in_team", "audience", "chosen_people", "when_others_ask", "can_make_changes", "chat_share"} {
				if value, ok := args[key]; ok {
					rule[key] = value
				}
			}
			body, _ = json.Marshal(rule)
		case "accept", "decline":
			op = "invite"
			method = http.MethodPost
			body, _ = json.Marshal(map[string]bool{"accept": action == "accept"})
		case "clashes":
			q.Set("with_text", "1")
		case "files", "compare":
		default:
			return "", fmt.Errorf("Choose a Sync action listed by this tool")
		}
		route := "/api/crew-teams/" + url.PathEscape(project)
		if op != "" {
			route += "/" + op
		}
		if action == "status" && sharedSender == "" && externalArg(args, "project") == "" {
			route = "/api/crew-teams"
		}
		if op != "" && op != "invite" && op != "tab" && op != "activity-posts" && op != "work" {
			var team crewTeam
			if connection != nil {
				if err := crewTeamRemoteJSON(ctx, caller.UserID, *connection, http.MethodGet, "/api/crew-teams/"+url.PathEscape(project), nil, &team); err != nil {
					return "", err
				}
			} else {
				var err error
				team, err = readCrewTeam(project)
				if err != nil {
					return "", err
				}
			}
			q.Set("folder_id", team.Folder.ID)
		}
		if len(q) > 0 {
			route += "?" + q.Encode()
		}
		r := httptest.NewRequest(method, route, bytes.NewReader(body)).WithContext(context.WithValue(ctx, UserContextKey, caller))
		r.Header.Set("X-Team-Transfer", newCrewTeamTransfer())
		// A clash carries both versions (up to 2 MiB each); other replies stay within 1 MiB.
		limit := 1 << 20
		if action == "clashes" {
			limit = 5 << 20
		}
		readLimit := limit
		if action == "files" {
			readLimit = 32 << 20 // the listing is paged below
		}
		var data []byte
		if connection != nil {
			req, err := http.NewRequestWithContext(ctx, method, connection.Server+route, bytes.NewReader(body))
			if err != nil {
				return "", err
			}
			req.Header = r.Header.Clone()
			req.Header.Set("Authorization", "Bearer "+connection.Token)
			req.Header.Set("Content-Type", "application/json")
			setCrewMacPair(req.Header, caller.UserID, *connection, route)
			res, err := desktopHTTPClient().Do(req)
			if err != nil {
				return "", fmt.Errorf("Your company's server is offline. Local files stay on this Mac; try Sync when connected")
			}
			defer res.Body.Close()
			data, err = io.ReadAll(io.LimitReader(res.Body, int64(readLimit)+1))
			if err != nil {
				return "", err
			}
			if len(data) > readLimit {
				return "", fmt.Errorf("This is too large for chat. Open it in the team's Files panel")
			}
			if res.StatusCode >= 300 {
				return "", fmt.Errorf("%s", laptopActionErrorText(string(data)))
			}
		} else {
			if route == "/api/crew-teams" {
				project = ""
			}
			r = mux.SetURLVars(r, map[string]string{"project": project, "op": op})
			w := httptest.NewRecorder()
			switch op {
			case "leave", "agent-visibility", "agent-rule":
				api.handleCrewTeamMemberAction(w, r)
			case "", "invite":
				api.handleCrewTeam(w, r)
			case "activity-posts":
				api.handleCrewTeamActivityPosts(w, r)
			case "work":
				api.handleCrewWorkLog(w, r)
			case "tab":
				api.handleCrewTeamTab(w, r)
			case "dashboards-tab":
				api.handleCrewTeamDashboardsTab(w, r)
			case "file", "files":
				api.handleCrewTeamFiles(w, r)
			default:
				api.handleCrewTeamHistory(w, r)
			}
			if w.Code >= 300 {
				return "", fmt.Errorf("%s", laptopActionErrorText(w.Body.String()))
			}
			data = w.Body.Bytes()
		}
		if action == "finding" {
			var post crewTeamActivityPost
			if json.Unmarshal(data, &post) == nil && post.Private {
				return "Saved. Only you see it, because this agent isn't shared with the team.", nil
			}
		}
		if action == "files" {
			var err error
			if data, err = crewTeamFilesPage(data, q.Get("path"), listIntArg(args, "offset")); err != nil {
				return "", err
			}
		}
		if len(data) > limit {
			return "", fmt.Errorf("This file is too large for chat. Open it in the team's Files panel")
		}
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return "", fmt.Errorf("This is a binary file. Download it from the team's Files panel")
		}
		if len(data) == 0 {
			return "This revision has no file at that path.", nil
		}
		return strings.TrimSpace(string(data)), nil
	}, 2*time.Minute, "workspace_advanced")
}

func (api *StreamingAPI) crewTeamConfigureChat(ctx context.Context, caller *UserClaims, connection *desktopConnection, localProject, project, action string, args map[string]any) (string, error) {
	request := func(method, op string, body []byte) *http.Request {
		r := httptest.NewRequest(method, "/", bytes.NewReader(body)).WithContext(context.WithValue(ctx, UserContextKey, caller))
		return mux.SetURLVars(r, map[string]string{"project": project, "op": op})
	}
	result := func(w *httptest.ResponseRecorder) (string, error) {
		if w.Code >= 300 {
			return "", fmt.Errorf("%s", laptopActionErrorText(w.Body.String()))
		}
		return w.Body.String(), nil
	}
	if action == "people" {
		// The People list, or one change to a person's level (the confirm flow is the server's).
		route, method, body := "/api/crew-teams/"+url.PathEscape(project)+"/people", http.MethodGet, []byte(nil)
		if externalArg(args, "person") != "" {
			method = http.MethodPost
			in := map[string]any{"person": externalArg(args, "person"), "level": externalArg(args, "level")}
			for _, key := range []string{"confirm", "create_dashboard"} {
				if value, ok := args[key]; ok {
					in[key] = value
				}
			}
			body, _ = json.Marshal(in)
		}
		if connection != nil {
			var out json.RawMessage
			var payload any
			if body != nil {
				payload = json.RawMessage(body)
			}
			if err := crewTeamRemoteJSON(ctx, caller.UserID, *connection, method, route, payload, &out); err != nil {
				return "", err
			}
			return string(out), nil
		}
		w := httptest.NewRecorder()
		api.handleCrewTeamPeople(w, request(method, "", body))
		return result(w)
	}
	if action == "directory" {
		if connection != nil {
			var out json.RawMessage
			if err := crewTeamRemoteJSON(ctx, caller.UserID, *connection, http.MethodGet, "/api/users/directory", nil, &out); err != nil {
				return "", err
			}
			return string(out), nil
		}
		w := httptest.NewRecorder()
		api.handleUserDirectory(w, request(http.MethodGet, "", nil))
		return result(w)
	}
	var team crewTeam
	if connection != nil {
		if err := crewTeamRemoteJSON(ctx, caller.UserID, *connection, http.MethodGet, "/api/crew-teams/"+url.PathEscape(project), nil, &team); err != nil {
			return "", err
		}
	} else {
		w := httptest.NewRecorder()
		api.handleCrewTeam(w, request(http.MethodGet, "", nil))
		if w.Code >= 300 {
			return result(w)
		}
		if err := json.Unmarshal(w.Body.Bytes(), &team); err != nil {
			return "", err
		}
	}
	if people, ok := args["people"]; ok {
		raw, err := json.Marshal(people)
		if err != nil {
			return "", err
		}
		if err = json.Unmarshal(raw, &team.People); err != nil {
			return "", fmt.Errorf("choose people from the directory by user_id")
		}
	}
	// Match the screen's complete folder selection. The shared save handler
	// lets members add folders, but only the owner may remove an existing one.
	if folder, ok := args["folder"].(string); ok {
		args["folders"] = []any{folder}
	}
	if on, ok := args["on"].(bool); ok {
		team.On = on
	}
	if value, provided := args["folders"]; provided {
		list, ok := value.([]any)
		if !ok || len(list) == 0 {
			return "", fmt.Errorf("Choose at least one folder, such as folders=[\"team\"].")
		}
		folders := make([]string, 0, len(list))
		for _, item := range list {
			folder, ok := item.(string)
			if !ok {
				return "", fmt.Errorf("Choose folder names inside this Crew.")
			}
			folders = append(folders, folder)
		}
		team.Folder = crewTeamFolder{Folders: folders}
	}
	raw, _ := json.Marshal(team)
	w := httptest.NewRecorder()
	r := request(http.MethodPut, "", raw)
	// The owner's own Mac goes through its Crew (folder checks, then it applies the record); anyone else's caller
	// changes the server's record directly, with the same server checks.
	if b, _ := crewroot.ReadSync(localProject); connection != nil && b != nil && b.Side == "local" && b.ConnectionID == connection.ID && b.PeerProjectID == project && readCrewTeamState(localProject).MemberOf == "" {
		r = mux.SetURLVars(r, map[string]string{"project": localProject, "connection": connection.ID})
		api.runDesktopCrewTeam(w, r)
	} else if connection != nil {
		var saved json.RawMessage
		if err := crewTeamRemoteJSON(ctx, caller.UserID, *connection, http.MethodPut, "/api/crew-teams/"+url.PathEscape(project), team, &saved); err != nil {
			return "", err
		}
		return string(saved), nil
	} else {
		api.handleCrewTeam(w, r)
	}
	return result(w)
}

// crewTeamFilesPageSize is how many entries one team_sync files call returns.
const crewTeamFilesPageSize = 200

// crewTeamFilesPage cuts the shared folder's listing to one page (offset, crewTeamFilesPageSize entries) with a
// plain note and next_offset, so large shared folders can still be listed from chat. path narrows it to one folder (or file).
func crewTeamFilesPage(data []byte, path string, offset int) ([]byte, error) {
	var list map[string]json.RawMessage
	if json.Unmarshal(data, &list) != nil {
		return data, nil
	}
	var files []map[string]any
	if json.Unmarshal(list["files"], &files) != nil {
		return data, nil
	}
	if path = strings.Trim(path, "/"); path != "" && path != "." {
		files = slices.DeleteFunc(files, func(f map[string]any) bool {
			name, _ := f["path"].(string)
			return name != path && !strings.HasPrefix(name, path+"/")
		})
	}
	offset = min(max(offset, 0), len(files))
	end := min(offset+crewTeamFilesPageSize, len(files))
	list["files"], _ = json.Marshal(files[offset:end])
	list["total"], _ = json.Marshal(len(files))
	if end < len(files) {
		list["next_offset"], _ = json.Marshal(end)
		list["note"], _ = json.Marshal(fmt.Sprintf("Showing %d-%d of %d files. Call files again with offset=%d for the next page, or give path=<folder> to list one folder.", offset+1, end, len(files), end))
	}
	return json.Marshal(list)
}
