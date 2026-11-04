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
	if !websiteDeviceClaims(claims) {
		return nil
	}
	gate.Declare("team_sync")
	return reg.RegisterCustomToolWithTimeout("team_sync", "Use your team's shared folder and Sync history. status lists your teams; choose project from that list. recent, clashes, compare (path, left, right), read_revision (path, revision), files and read_file read the shared folder. save_file (path, content, base from files) is a normal save; stale saves keep the previous version in history. resolve uses keep=current or revision:N; restore uses revision and saves it as a NEW revision. To resolve with AI: read/compare both revisions, save the merged content, then resolve keep=current. directory lists company people. configure (owner only): people replaces the roster with user_id and can_edit_shared_folder, folder chooses one existing child folder; a person added is invited and joins only when they accept. status also lists your open invites; accept or decline one with its project (your own answer, from your own chat). This shares the selected folder with those enabled people; other files stay outside the Team folder. Only your admitted teams are available. Other people's private chats and sign-ins are never transferred. On this Mac a paired project uses its existing server connection.", map[string]any{"type": "object", "properties": map[string]any{
		"action":  map[string]any{"type": "string", "enum": []string{"status", "recent", "clashes", "compare", "read_revision", "resolve", "restore", "files", "read_file", "save_file", "directory", "configure", "accept", "decline"}},
		"network": map[string]any{"type": "string", "description": "Company name, if this Mac is connected to several companies and this project has no connection."},
		"project": map[string]any{"type": "string", "description": "Team project_id returned by status; defaults to this project's team."},
		"path":    map[string]any{"type": "string"}, "revision": map[string]any{"type": "integer"}, "left": map[string]any{"type": "integer"}, "right": map[string]any{"type": "integer"}, "winning_revision": map[string]any{"type": "integer"},
		"folder": map[string]any{"type": "string"}, "people": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"user_id": map[string]any{"type": "string"}, "can_edit_shared_folder": map[string]any{"type": "boolean"}}, "required": []string{"user_id", "can_edit_shared_folder"}}},
		"keep": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}, "base": map[string]any{"type": "string", "description": "Last hash from files; empty for a new file."},
	}, "required": []string{"action"}}, func(ctx context.Context, args map[string]any) (string, error) {
		caller := GetUserFromContext(ctx)
		if caller == nil {
			caller = claims
		}
		if caller.UserID != claims.UserID || !websiteDeviceClaims(caller) {
			return "", fmt.Errorf("Open Sync in your own project chat to use this team's folder")
		}
		action := externalArg(args, "action")
		writes := action == "resolve" || action == "restore" || action == "save_file" || action == "configure"
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
			if sharedSender != "" || project != manifest.ID && (connection == nil || externalArg(args, "project") != "") {
				return "", fmt.Errorf("Team setup belongs to the owner's own Main chat. Ask the owner to update Access")
			}
			if action == "configure" {
				if _, err := api.crewSyncRoots(ctx, caller, manifest.ID); err != nil {
					return "", err
				}
				if IsLocalMode() {
					if _, err := api.interactiveLaptopOwner(ctx, claims, sessionID, root); err != nil {
						return "", err
					}
				}
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
			return "", fmt.Errorf("choose people from directory and whether each may edit the folder")
		}
	}
	if folder, ok := args["folder"].(string); ok {
		team.Folder = crewTeamFolder{Path: folder}
	}
	raw, _ := json.Marshal(team)
	w := httptest.NewRecorder()
	r := request(http.MethodPut, "", raw)
	if connection != nil {
		r = mux.SetURLVars(r, map[string]string{"project": localProject, "connection": connection.ID})
		api.runDesktopCrewTeam(w, r)
	} else {
		api.handleCrewTeam(w, r)
	}
	return result(w)
}
