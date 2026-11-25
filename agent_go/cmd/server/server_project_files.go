package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// A Mac project's agent reads and writes the files of a project on a server the
// person is signed in to (#3663). It calls that server's own project file tools
// (the public workspace and files action families) as the signed-in person, so
// the server's owner/co-owner/shared checks decide.
// The saved connection's token is added here, in this process; it never reaches
// the agent's env, files or MCP config. Only the Mac project's own owner gets the
// tool: a teammate's turn on this Mac never uses the owner's server sign-in.

const serverProjectFilesToolName = "server_project_files"

// callServerTool runs one of a server's external tools as the person signed in through conn.
func callServerTool(ctx context.Context, owner string, conn desktopConnection, tool string, args map[string]any) (map[string]any, error) {
	payload, err := json.Marshal(map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(conn.Server, "/")+"/api/external/v1/call", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+conn.Token)
	client := desktopHTTPClient()
	client.Timeout = 55 * time.Second
	started := time.Now()
	res, err := client.Do(req)
	if err != nil {
		log.Printf("[SERVER_PROJECT_CALL] server=%s tool=%s crew=%v -> unreachable (%s)", conn.Name, tool, args["crew_id"], time.Since(started).Round(time.Millisecond))
		return nil, fmt.Errorf("%s could not be reached; try again", conn.Name)
	}
	defer res.Body.Close()
	if renewed := res.Header.Get("X-Renewed-Token"); owner != "" && renewed != "" {
		if err := saveRenewedDesktopToken(owner, conn, renewed); err != nil {
			log.Printf("[SERVER_PROJECT_CALL] server=%s keep renewed sign-in: %v", conn.Name, err)
		}
	}
	var body map[string]any
	_ = json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&body)
	log.Printf("[SERVER_PROJECT_CALL] server=%s tool=%s action=%v crew=%v path=%v -> %d (%s)", conn.Name, tool, args["action"], args["crew_id"], args["path"], res.StatusCode, time.Since(started).Round(time.Millisecond))
	if res.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("your sign-in to %s has expired; sign in again from Settings", conn.Name)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s refused: %s", conn.Name, externalErrorMessage(body, res.Status))
	}
	return body, nil
}

func desktopConnectionsOf(owner string) ([]desktopConnection, error) {
	desktopConnectionsMu.Lock()
	defer desktopConnectionsMu.Unlock()
	return readDesktopConnections(owner)
}

// pickServerConnection finds the saved server by its name, address or ID; with one saved server, it is the default.
func pickServerConnection(conns []desktopConnection, server string) (desktopConnection, error) {
	if len(conns) == 0 {
		return desktopConnection{}, fmt.Errorf("this Mac is not signed in to a server; sign in from Settings, then try again")
	}
	server = strings.TrimSpace(server)
	if server == "" {
		if len(conns) == 1 {
			return conns[0], nil
		}
		return desktopConnection{}, fmt.Errorf("this Mac is signed in to several servers (%s); pass server", serverConnectionNames(conns))
	}
	want := strings.ToLower(strings.TrimRight(server, "/"))
	for _, c := range conns {
		host := ""
		if u, err := url.Parse(c.Server); err == nil {
			host = strings.ToLower(u.Host)
		}
		if strings.EqualFold(c.Name, server) || c.ID == server || strings.ToLower(strings.TrimRight(c.Server, "/")) == want || (host != "" && host == want) {
			return c, nil
		}
	}
	return desktopConnection{}, fmt.Errorf("no saved server called %q; this Mac is signed in to: %s", server, serverConnectionNames(conns))
}

func serverConnectionNames(conns []desktopConnection) string {
	names := make([]string, 0, len(conns))
	for _, c := range conns {
		names = append(names, c.Name)
	}
	return strings.Join(names, ", ")
}

// registerServerProjectFilesTool adds server_project_files to a Mac project's chat for its own owner.
func (api *StreamingAPI) registerServerProjectFilesTool(registrar definitionToolRegistrar, gate *productToolGate, userID, workspacePath string) error {
	if !IsLocalMode() || userID == "" || workspacePath == "" {
		return nil
	}
	roots, found, err := crewStorageRoots(workspacePath)
	if err != nil || !found || roots.Owner != userID {
		return nil
	}
	forward := []string{"path", "depth", "glob", "offset", "limit", "query", "match_case", "whole_word", "regex", "content", "content_base64", "expected_revision", "request_id"}
	execute := func(ctx context.Context, args map[string]interface{}) (string, error) {
		conns, err := desktopConnectionsOf(userID)
		if err != nil {
			return "", fmt.Errorf("cannot read this Mac's server sign-ins: %v", err)
		}
		action := strings.TrimSpace(stringToolArg(args, "action"))
		server := stringToolArg(args, "server")
		out := func(value any) (string, error) {
			encoded, err := json.MarshalIndent(value, "", "  ")
			return string(encoded), err
		}
		if action == "projects" {
			targets := conns
			if strings.TrimSpace(server) != "" {
				c, err := pickServerConnection(conns, server)
				if err != nil {
					return "", err
				}
				targets = []desktopConnection{c}
			}
			if len(targets) == 0 {
				return "", fmt.Errorf("this Mac is not signed in to a server; sign in from Settings, then try again")
			}
			servers := []map[string]any{}
			for _, c := range targets {
				entry := map[string]any{"server": c.Name}
				list := map[string]any{"action": "list"}
				if q := strings.TrimSpace(stringToolArg(args, "query")); q != "" {
					list["query"] = q // servers refuse an empty query
				}
				body, err := callServerTool(ctx, userID, c, "workspace", list)
				if err != nil {
					entry["error"] = err.Error()
				} else {
					projects := []map[string]any{}
					items, _ := body["workspaces"].([]any)
					for _, item := range items {
						if p, ok := item.(map[string]any); ok {
							projects = append(projects, map[string]any{"project": p["id"], "name": p["name"], "owner": p["owner_name"], "yours": p["owned_by_caller"], "mode": p["mode"]})
						}
					}
					entry["projects"] = projects
				}
				servers = append(servers, entry)
			}
			return out(map[string]any{"servers": servers})
		}
		if action == "create_project" {
			c, err := pickServerConnection(conns, server)
			if err != nil {
				return "", err
			}
			return api.createOnlineProjectFromMac(ctx, userID, roots.ID, c, stringToolArg(args, "name"), stringToolArg(args, "purpose"))
		}
		if action != "list" && action != "search" && action != "read" && action != "write" {
			return "", fmt.Errorf("action must be projects, create_project, list, search, read or write")
		}
		project := strings.TrimSpace(stringToolArg(args, "project"))
		if project == "" {
			return "", fmt.Errorf("pass project: its ID from action=projects")
		}
		c, err := pickServerConnection(conns, server)
		if err != nil {
			return "", err
		}
		// The server's public files family picks its project file member from crew_id.
		request := map[string]any{"action": action, "crew_id": project}
		for _, key := range forward {
			if value, ok := args[key]; ok && value != nil {
				request[key] = value
			}
		}
		body, err := callServerTool(ctx, userID, c, "files", request)
		if err != nil {
			return "", err
		}
		body["server"] = c.Name
		return out(body)
	}
	parameters := map[string]interface{}{
		"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{
			"action":            map[string]interface{}{"type": "string", "enum": []string{"projects", "create_project", "list", "search", "read", "write"}, "description": "projects lists the server projects you can open; create_project makes a new online project for this project's Team (name, purpose); list, search, read and write work on one project's files."},
			"name":              map[string]interface{}{"type": "string", "description": "create_project: the new online project's name, named for its job."},
			"purpose":           map[string]interface{}{"type": "string", "description": "create_project: one or two sentences on what it does; becomes its Main's purpose."},
			"server":            map[string]interface{}{"type": "string", "description": "Saved server name; optional when this Mac is signed in to one server."},
			"project":           map[string]interface{}{"type": "string", "description": "Project ID from action=projects (list, search, read, write)."},
			"path":              map[string]interface{}{"type": "string", "description": "Project-relative file path (read, write) or folder to start from (list, search)."},
			"glob":              map[string]interface{}{"type": "string", "description": "Optional name pattern for list and search, e.g. **/*.md."},
			"depth":             map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 8},
			"query":             map[string]interface{}{"type": "string", "description": "search: text to find inside files. projects: filter by name."},
			"match_case":        map[string]interface{}{"type": "boolean"},
			"whole_word":        map[string]interface{}{"type": "boolean"},
			"regex":             map[string]interface{}{"type": "boolean"},
			"offset":            map[string]interface{}{"type": "integer", "minimum": 0},
			"limit":             map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 200},
			"content":           map[string]interface{}{"type": "string", "description": "write: UTF-8 text, at most 2 MiB."},
			"content_base64":    map[string]interface{}{"type": "string", "description": "write: a binary file as base64, at most 11 MiB."},
			"expected_revision": map[string]interface{}{"type": "string", "description": "write: refuse if the file changed since this revision (from read)."},
			"request_id":        map[string]interface{}{"type": "string", "description": "write: reuse only to retry the identical write."},
		}}
	description := "Read and write the files of a project on a server this Mac is signed in to, as you. action=projects lists the server projects you can open; then list, search, read or write one project's files by its ID. The server decides access: its owner and co-owners may write any editable file, people it is shared with only under shared/<their id>/. Private areas (chats, databases, hidden folders) stay closed. create_project makes a new project on the server, owned by you, with this project's Team in its Users, so the Team's people and agents can use it and its agents run 24/7; then hand it the job with messages (its Main)."
	gate.Declare(serverProjectFilesToolName)
	return registrar.RegisterCustomTool(serverProjectFilesToolName, description, parameters, execute, "code_chat_tools")
}

// createOnlineProjectFromMac makes a new project on the server for this Mac project's Team (owner, 26 Nov, #4087): the
// person owns it, its Main gets the purpose, and the Team goes into its Users, all through the server's own tools as
// the signed-in person, so the server's checks decide. Nothing is copied from the Mac.
func (api *StreamingAPI) createOnlineProjectFromMac(ctx context.Context, userID, macProject string, c desktopConnection, name, purpose string) (string, error) {
	name, purpose = strings.TrimSpace(name), strings.TrimSpace(purpose)
	if name == "" {
		return "", fmt.Errorf("pass name: what the new online project is called, named for its job")
	}
	spec := map[string]any{"name": name}
	if purpose != "" {
		spec["description"] = purpose
	}
	key := "mac-online-" + macProject + "-" + strings.ToLower(strings.Join(strings.Fields(name), "-"))
	if len(key) > 128 {
		key = key[:128]
	}
	created, err := callServerTool(ctx, userID, c, "workspace", map[string]any{"action": "create", "kind": "crew", "spec": spec, "idempotency_key": key})
	if err != nil {
		return "", err
	}
	id, _ := created["id"].(string)
	if id == "" {
		id, _ = created["crew_id"].(string)
	}
	if id == "" {
		return "", fmt.Errorf("%s did not say which project it created; check its Projects page", c.Name)
	}
	madeName, _ := created["name"].(string)
	out := map[string]any{"server": c.Name, "project": id, "name": firstNonEmptyTrimmed(madeName, name), "owner": "you"}
	if _, err := callServerTool(ctx, userID, c, "crew_owners", map[string]any{"crew_id": id, "action": "team_add", "team_id": macProject}); err != nil {
		out["team"] = "not added: " + err.Error()
		out["note"] = "The project was made, but this project's Team is not in its Users yet. Turn the Team on (Team panel), then add it on the server: the project's Access page, Users."
	} else {
		out["team"] = "in its Users"
		out["note"] = "Everyone in this project's Team can use it now. Hand it the job with messages ask to its Main (team_sync agents lists it); it builds and test-runs it there. Keys it needs are added to that project's own vault."
	}
	log.Printf("[SERVER_PROJECT_CALL] server=%s event=online_project_created mac_project=%s project=%s team_added=%t", c.Name, macProject, id, out["team"] == "in its Users")
	return func() (string, error) { b, err := json.MarshalIndent(out, "", "  "); return string(b), err }()
}
