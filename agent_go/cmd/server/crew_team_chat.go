package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
func (api *StreamingAPI) registerCrewTeamChatTool(reg definitionToolRegistrar, gate *productToolGate, claims *UserClaims, readOnly bool, sessionID, root, sharedSender string) error {
	if claims == nil || claims.UserID == "" {
		return nil
	}
	gate.Declare("team_sync")
	return reg.RegisterCustomToolWithTimeout("team_sync", "Use your team's shared folder and Sync history. status lists your teams; choose project from that list. recent, clashes, compare (path, left, right), read_revision (path, revision), files and read_file read the shared folder. save_file (path, content, base from files) is a normal save; stale saves keep the previous version in history. clashes lists open clashes (two people changed the same lines of a file) with both versions: current (the file now) and other (the other person's, kept in history), as text for text files up to 2 MiB. resolve (path, clash_id from clashes) keeps current, puts the other back (keep=other), restores keep=revision:N, or saves your merged text (content); each change is a NEW revision every copy downloads, and the clash is closed. restore uses revision and saves it as a NEW revision. leave removes you from a joined Team; agent_visibility (agent_key, in_team) sets one of your own agents In team / Only me, or with all_in_team=true/false your own All my agents in team (Main is always In team). agent_rule (agent_key, when_others_ask and/or can_make_changes) sets what your own agent does when someone else asks it. directory lists company people (find a person by name there, then use their user_id). configure, like the Team tab: people replaces the roster with user_id (anyone in the Team; the owner always stays); folders=[…] adds folders to share (anyone in the Team; a missing folder is made); only the Crew's owner turns the Team on or off (on=true/false; on starts with the team/ folder when nothing is shared), switches share=whole / share=folders, or stops sharing a folder. Whole Crew is everything except chats, memory, app files, node_modules/.git/caches and files that look like passwords or keys. A person added is invited and joins only when they accept; every member reads and writes the shared folder. status with project shows the Team, including what your agents use from the owner (lent). status also lists your open invites; accept or decline one with its project (your own answer, from your own chat). This shares the selected folder with those enabled people; other files stay outside the Team folder. Only your admitted teams are available. Other people's private chats and sign-ins are never transferred. On this Mac a paired project uses its existing server connection.", map[string]any{"type": "object", "properties": map[string]any{
		"action":           map[string]any{"type": "string", "enum": []string{"status", "recent", "clashes", "compare", "read_revision", "resolve", "restore", "files", "read_file", "save_file", "directory", "configure", "accept", "decline", "leave", "agent_visibility", "agent_rule"}},
		"network":          map[string]any{"type": "string", "description": "Company name, if this Mac is connected to several companies and this project has no connection."},
		"agent_key":        map[string]any{"type": "string"},
		"in_team":          map[string]any{"type": "boolean"},
		"when_others_ask":  map[string]any{"type": "string", "maxLength": whenOthersAskMaxRunes, "description": "agent_rule: what your own agent may do when someone else asks it (empty clears it). An instruction, not a lock."},
		"can_make_changes": map[string]any{"type": "boolean", "description": "agent_rule: for an agent on a Mac, false answers others only (the default), true may make changes for them."},
		"on":               map[string]any{"type": "boolean", "description": "configure: the Team switch (the owner only)."},
		"all_in_team":      map[string]any{"type": "boolean", "description": "agent_visibility: your own All my agents in team."},
		"project":          map[string]any{"type": "string", "description": "Team project_id returned by status; defaults to this project's team."},
		"path":             map[string]any{"type": "string"}, "revision": map[string]any{"type": "integer"}, "left": map[string]any{"type": "integer"}, "right": map[string]any{"type": "integer"}, "winning_revision": map[string]any{"type": "integer"}, "clash_id": map[string]any{"type": "string", "description": "resolve: the clash's id from clashes."},
		"share": map[string]any{"type": "string", "enum": []string{"whole", "folders"}}, "folders": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"folder": map[string]any{"type": "string", "description": "One folder; the same as share=folders folders=[folder]."}, "people": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"user_id": map[string]any{"type": "string"}}, "required": []string{"user_id"}}},
		"keep": map[string]any{"type": "string", "description": "resolve: current, other or revision:N."}, "content": map[string]any{"type": "string", "description": "save_file: the file's text. resolve: the merged text to save instead of keep."}, "base": map[string]any{"type": "string", "description": "Last hash from files; empty for a new file."},
	}, "required": []string{"action"}}, func(ctx context.Context, args map[string]any) (string, error) {
		caller := GetUserFromContext(ctx)
		if caller == nil {
			caller = claims
		}
		if caller.UserID != claims.UserID {
			return "", fmt.Errorf("Open Sync in your own project chat to use this team's folder")
		}
		action := externalArg(args, "action")
		writes := action == "resolve" || action == "restore" || action == "save_file" || action == "configure" || action == "leave" || action == "agent_visibility" || action == "agent_rule"
		if writes && readOnly {
			return "", fmt.Errorf("This agent can explain the clash. Ask the owner to allow it to make changes, or resolve it in your own chat")
		}
		manifest, e := readCrewProjectManifests(ctx, crewProfileID, root)
		if e != nil {
			return "", e
		}
		project := externalArg(args, "project")
		if project == "" {
			project = manifest.ID
		}
		if sharedSender != "" && project != manifest.ID {
			return "", fmt.Errorf("This shared agent uses only this project's team. Open your own chat for another team")
		}
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
			if b != nil && project == manifest.ID {
				project = b.PeerProjectID
			}
		}
		if action == "configure" || action == "directory" {
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
		switch action {
		case "status":
			op = ""
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
			key, in := externalArg(args, "agent_key"), args["in_team"]
			if all, ok := args["all_in_team"].(bool); ok && action == "agent_visibility" {
				key, in = crewTeamAllMine, all
			}
			body, _ = json.Marshal(map[string]any{"agent_key": key, "in_team": in})
		case "agent_rule":
			if sharedSender != "" {
				return "", fmt.Errorf("Change your agent's rule in your own chat")
			}
			op, method = "agent-rule", http.MethodPost
			rule := map[string]any{"agent_key": externalArg(args, "agent_key")}
			for _, key := range []string{"when_others_ask", "can_make_changes"} {
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
		if op != "" && op != "invite" {
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
		var data []byte
		if connection != nil {
			req, err := http.NewRequestWithContext(ctx, method, connection.Server+route, bytes.NewReader(body))
			if err != nil {
				return "", err
			}
			req.Header = r.Header.Clone()
			req.Header.Set("Authorization", "Bearer "+connection.Token)
			req.Header.Set("Content-Type", "application/json")
			res, err := desktopHTTPClient().Do(req)
			if err != nil {
				return "", fmt.Errorf("Your company's server is offline. Local files stay on this Mac; try Sync when connected")
			}
			defer res.Body.Close()
			data, err = io.ReadAll(io.LimitReader(res.Body, int64(limit)+1))
			if err != nil {
				return "", err
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
	// What the team shares (owner, 5 Nov): folders=[…] (or one folder="…") adds folders, anyone in the Team;
	// share=whole for the Whole Crew and share=folders with folders=[…] as the whole list are the owner's.
	if folder, ok := args["folder"].(string); ok {
		args["folders"] = []any{folder}
		if folder == "whole" {
			args["share"] = "whole"
		}
	}
	if on, ok := args["on"].(bool); ok {
		team.On = on
	}
	list, _ := args["folders"].([]any)
	folders := []string{}
	for _, item := range list {
		if text, ok := item.(string); ok {
			folders = append(folders, text)
		}
	}
	switch share, _ := args["share"].(string); share {
	case "whole":
		team.Folder = crewTeamFolder{Whole: true}
	case "folders":
		if len(folders) == 0 {
			return "", fmt.Errorf("name the folders to share, such as folders=[\"team\"]")
		}
		team.Folder = crewTeamFolder{Folders: folders}
	case "":
		if len(folders) > 0 && team.Folder.Whole {
			return "", fmt.Errorf("the team shares the Whole Crew already")
		}
		if len(folders) > 0 {
			team.Folder.Folders = append(slices.Clone(team.Folder.Folders), folders...)
		}
	default:
		return "", fmt.Errorf("share is whole or folders")
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
