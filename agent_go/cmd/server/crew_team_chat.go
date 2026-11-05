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
	return reg.RegisterCustomToolWithTimeout("team_sync", "Use your team's shared folder and Sync history. status lists your teams; choose project from that list. recent, clashes, compare (path, left, right), read_revision (path, revision), files and read_file read the shared folder. save_file (path, content, base from files) is a normal save; stale saves keep the previous version in history. resolve uses keep=current or revision:N; restore uses revision and saves it as a NEW revision. To resolve with AI: read/compare both revisions, save the merged content, then resolve keep=current. leave removes you from a joined Team; agent_visibility (agent_key, in_team) changes only your own agent. directory lists company people. configure (the owner or any joined member, like the Team tab): on=true/false is the Team switch (on starts with All agents in team and, with nothing shared, the team/ folder); all_in_team=true/false; people replaces the roster with user_id (the owner always stays); share=whole shares the Whole Crew (everything except chats, memory, app files, node_modules/.git/caches and files that look like passwords or keys) and share=folders with folders=[…] shares one or more existing folders of this Crew (switching back stops syncing the rest; teammates' copies stay); a person added is invited and joins only when they accept, and every member reads and writes the shared folder. status also lists your open invites; accept or decline one with its project (your own answer, from your own chat). This shares the selected folder with those enabled people; other files stay outside the Team folder. Only your admitted teams are available. Other people's private chats and sign-ins are never transferred. On this Mac a paired project uses its existing server connection.", map[string]any{"type": "object", "properties": map[string]any{
		"action":      map[string]any{"type": "string", "enum": []string{"status", "recent", "clashes", "compare", "read_revision", "resolve", "restore", "files", "read_file", "save_file", "directory", "configure", "accept", "decline", "leave", "agent_visibility"}},
		"network":     map[string]any{"type": "string", "description": "Company name, if this Mac is connected to several companies and this project has no connection."},
		"agent_key":   map[string]any{"type": "string"},
		"in_team":     map[string]any{"type": "boolean"},
		"on":          map[string]any{"type": "boolean", "description": "configure: the Team switch."},
		"all_in_team": map[string]any{"type": "boolean", "description": "configure: All agents in team."},
		"project":     map[string]any{"type": "string", "description": "Team project_id returned by status; defaults to this project's team."},
		"path":        map[string]any{"type": "string"}, "revision": map[string]any{"type": "integer"}, "left": map[string]any{"type": "integer"}, "right": map[string]any{"type": "integer"}, "winning_revision": map[string]any{"type": "integer"},
		"share": map[string]any{"type": "string", "enum": []string{"whole", "folders"}}, "folders": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"folder": map[string]any{"type": "string", "description": "One folder; the same as share=folders folders=[folder]."}, "people": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"user_id": map[string]any{"type": "string"}}, "required": []string{"user_id"}}},
		"keep": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}, "base": map[string]any{"type": "string", "description": "Last hash from files; empty for a new file."},
	}, "required": []string{"action"}}, func(ctx context.Context, args map[string]any) (string, error) {
		caller := GetUserFromContext(ctx)
		if caller == nil {
			caller = claims
		}
		if caller.UserID != claims.UserID {
			return "", fmt.Errorf("Open Sync in your own project chat to use this team's folder")
		}
		action := externalArg(args, "action")
		writes := action == "resolve" || action == "restore" || action == "save_file" || action == "configure" || action == "leave" || action == "agent_visibility"
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
			body, _ = json.Marshal(map[string]any{"path": externalArg(args, "path"), "keep": keep, "winning_revision": listIntArg(args, "winning_revision"), "transfer_id": newCrewTeamTransfer()})
		case "leave", "agent_visibility":
			if sharedSender != "" {
				return "", fmt.Errorf("Change your Team access in your own chat")
			}
			op = strings.ReplaceAll(action, "_", "-")
			method = http.MethodPost
			body, _ = json.Marshal(map[string]any{"agent_key": externalArg(args, "agent_key"), "in_team": args["in_team"]})
		case "accept", "decline":
			op = "invite"
			method = http.MethodPost
			body, _ = json.Marshal(map[string]bool{"accept": action == "accept"})
		case "files", "clashes", "compare":
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
			data, err = io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
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
			case "leave", "agent-visibility":
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
		if len(data) > 1<<20 {
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
	// What the team shares (owner, 5 Nov): share=whole for the Whole Crew, or share=folders with folders=[…]; a single
	// folder="…" is one folder.
	if folder, ok := args["folder"].(string); ok {
		args["share"], args["folders"] = "folders", []any{folder}
		if folder == "whole" {
			args["share"] = "whole"
		}
	}
	if on, ok := args["on"].(bool); ok {
		team.On = on
	}
	if all, ok := args["all_in_team"].(bool); ok {
		team.AgentsOnlyMe = !all
	}
	switch share, _ := args["share"].(string); share {
	case "whole":
		team.Folder = crewTeamFolder{Whole: true}
	case "folders":
		list, _ := args["folders"].([]any)
		folders := []string{}
		for _, item := range list {
			if text, ok := item.(string); ok {
				folders = append(folders, text)
			}
		}
		if len(folders) == 0 {
			return "", fmt.Errorf("name the folders to share, such as folders=[\"team\"]")
		}
		team.Folder = crewTeamFolder{Folders: folders}
	case "":
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
