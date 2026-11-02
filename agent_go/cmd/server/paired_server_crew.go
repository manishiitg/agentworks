package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// A Crew on the Mac that is paired with a server Crew uses that Crew's Vault
// apps where they live. List and call go to the server's own Crew MCP actions
// as the signed-in person, so the server's selection and Vault checks decide.
// Sign-ins and secret values stay on the server. The Mac's connection token is
// added here, in this process; it never reaches the agent's env, files or MCP
// config (PLAT-856-493).
type pairedServerCrew struct {
	conn   desktopConnection
	crewID string
}

// pairedServerCrewFor returns the server Crew paired with this local Crew. Only
// the Crew's own owner gets one, matching who may pair it.
func pairedServerCrewFor(userID, workspacePath string) (pairedServerCrew, bool, error) {
	if !IsLocalMode() || userID == "" || workspacePath == "" {
		return pairedServerCrew{}, false, nil
	}
	roots, found, err := crewStorageRoots(workspacePath)
	if err != nil || !found {
		return pairedServerCrew{}, false, err
	}
	return pairedServerCrewForRoots(userID, roots)
}

func pairedServerCrewForRoots(userID string, roots crewroot.Roots) (pairedServerCrew, bool, error) {
	if !IsLocalMode() || roots.Owner != userID {
		return pairedServerCrew{}, false, nil
	}
	b, err := crewroot.ReadSync(roots.ID)
	if err != nil || b == nil || b.Side != "local" || b.ConnectionID == "" || b.PeerProjectID == "" {
		return pairedServerCrew{}, false, err
	}
	conn, err := desktopConnectionFor(userID, b.ConnectionID)
	if err != nil {
		return pairedServerCrew{}, false, fmt.Errorf("this Crew's server sign-in is missing; sign in to %s again", b.PeerServer)
	}
	if strings.TrimRight(conn.Server, "/") != strings.TrimRight(b.PeerServer, "/") {
		return pairedServerCrew{}, false, fmt.Errorf("this Crew's server sign-in does not match its server")
	}
	return pairedServerCrew{conn: conn, crewID: b.PeerProjectID}, true, nil
}

// mcp calls the server's Crew mcp tool for the paired Crew.
func (p pairedServerCrew) mcp(ctx context.Context, args map[string]any) (map[string]any, error) {
	return p.call(ctx, "mcp", args)
}

// call runs one of the server's Crew tools (mcp, settings) for the paired Crew as the signed-in person.
func (p pairedServerCrew) call(ctx context.Context, tool string, args map[string]any) (map[string]any, error) {
	args["crew_id"] = p.crewID
	payload, err := json.Marshal(map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.conn.Server, "/")+"/api/external/v1/call", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.conn.Token)
	client := desktopHTTPClient()
	client.Timeout = 55 * time.Second
	started := time.Now()
	res, err := client.Do(req)
	if err != nil {
		log.Printf("[CREW_REMOTE_MCP] crew=%s server=%s action=%v app=%v tool=%v -> unreachable (%s)", p.crewID, p.conn.Name, args["action"], args["server"], args["tool"], time.Since(started).Round(time.Millisecond))
		return nil, fmt.Errorf("%s could not be reached; try again", p.conn.Name)
	}
	defer res.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&body)
	log.Printf("[CREW_REMOTE_MCP] crew=%s server=%s action=%v app=%v tool=%v -> %d (%s)", p.crewID, p.conn.Name, args["action"], args["server"], args["tool"], res.StatusCode, time.Since(started).Round(time.Millisecond))
	if res.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("your sign-in to %s has expired; sign in again from Settings", p.conn.Name)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s refused: %s", p.conn.Name, externalErrorMessage(body, res.Status))
	}
	return body, nil
}

// usableApps is the server Crew's own app selection: nil means all its Vault apps.
func (p pairedServerCrew) usableApps(ctx context.Context) (map[string]bool, map[string]any, error) {
	settings, err := p.call(ctx, "settings", map[string]any{"action": "get"})
	if err != nil {
		return nil, nil, err
	}
	if all, _ := settings["use_all_vault_apps"].(bool); all {
		return nil, settings, nil
	}
	selected := map[string]bool{}
	servers, _ := settings["mcp_servers"].([]any)
	for _, name := range servers {
		if text, ok := name.(string); ok {
			selected[text] = true
		}
	}
	return selected, settings, nil
}

// registerPairedServerAppTools adds the server Crew's apps to a paired Crew's
// chat. Unpaired Crews get nothing.
func (api *StreamingAPI) registerPairedServerAppTools(registrar definitionToolRegistrar, gate *productToolGate, userID, workspacePath string) error {
	if _, paired, err := pairedServerCrewFor(userID, workspacePath); err != nil || !paired {
		return nil
	}
	current := func() (pairedServerCrew, error) {
		p, paired, err := pairedServerCrewFor(userID, workspacePath)
		if err == nil && !paired {
			err = fmt.Errorf("this Crew is no longer linked to a server")
		}
		return p, err
	}
	out := func(value any) (string, error) {
		encoded, err := json.MarshalIndent(value, "", "  ")
		return string(encoded), err
	}
	tools := []struct {
		name, description string
		parameters        map[string]interface{}
		execute           func(context.Context, map[string]interface{}) (string, error)
	}{
		{"list_server_apps", "List the apps (Gmail, Slack, Notion and other Vault connections) of the server Crew this Crew is linked to. They run on that server with its sign-ins. Pass app to see that app's tools and their parameters; page with offset.", map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"app":    map[string]interface{}{"type": "string", "description": "App name from the list, e.g. vault_c-1234."},
				"offset": map[string]interface{}{"type": "integer", "minimum": 0},
			}},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				p, err := current()
				if err != nil {
					return "", err
				}
				request := map[string]any{"action": "diagnostics"}
				app := strings.TrimSpace(stringToolArg(args, "app"))
				if app != "" {
					offset, _ := args["offset"].(float64)
					request["name"], request["include_tools"], request["limit"], request["offset"] = app, true, 100, int(offset)
				}
				body, err := p.mcp(ctx, request)
				if err != nil {
					return "", err
				}
				if app == "" {
					// Only apps this server Crew has selected can be called, so only those are listed.
					usable, _, err := p.usableApps(ctx)
					if err != nil {
						return "", err
					}
					apps := []map[string]any{}
					connections, _ := body["connections"].(map[string]any)
					items, _ := connections["items"].([]any)
					for _, item := range items {
						if c, ok := item.(map[string]any); ok && (usable == nil || usable[fmt.Sprint(c["name"])]) {
							apps = append(apps, map[string]any{"app": c["name"], "label": c["label"], "kind": c["catalog"], "connected": c["connected"], "tools": c["cached_tool_count"]})
						}
					}
					return out(map[string]any{"server": p.conn.Name, "apps": apps})
				}
				connection, _ := body["connection"].(map[string]any)
				list, _ := connection["tools"].(map[string]any)
				items, _ := list["items"].([]any)
				tools := []map[string]any{}
				for _, item := range items {
					if t, ok := item.(map[string]any); ok {
						// The server's own tool name, prefix included: its call action expects exactly that.
						tools = append(tools, map[string]any{"tool": t["name"], "description": t["description"], "parameters": t["parameters"]})
					}
				}
				return out(map[string]any{"server": p.conn.Name, "app": app, "tools": tools, "total": list["total"], "next_offset": list["next_offset"], "has_more": list["has_more"]})
			}},
		{"call_server_app", "Call a tool of one of the linked server Crew's apps (see list_server_apps). It runs on that server as you, with its sign-ins and checks; nothing is copied to this computer.", map[string]interface{}{
			"type": "object", "required": []string{"app", "tool", "arguments"}, "properties": map[string]interface{}{
				"app":       map[string]interface{}{"type": "string", "description": "App name from list_server_apps."},
				"tool":      map[string]interface{}{"type": "string", "description": "Tool name from list_server_apps with app."},
				"arguments": map[string]interface{}{"type": "object", "description": "The tool's arguments; {} for none."},
			}},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				p, err := current()
				if err != nil {
					return "", err
				}
				app, tool := strings.TrimSpace(stringToolArg(args, "app")), strings.TrimSpace(stringToolArg(args, "tool"))
				arguments, ok := args["arguments"].(map[string]interface{})
				if app == "" || tool == "" || !ok {
					return "", fmt.Errorf("pass app, tool and an arguments object; see list_server_apps")
				}
				body, err := p.mcp(ctx, map[string]any{"action": "call", "server": app, "tool": tool, "args": arguments})
				if err != nil {
					return "", err
				}
				return out(map[string]any{"ran_on": p.conn.Name + " (server)", "result": body["result"]})
			}},
	}
	for _, tool := range tools {
		gate.Declare(tool.name)
		if err := registrar.RegisterCustomTool(tool.name, tool.description, tool.parameters, tool.execute, "code_chat_tools"); err != nil {
			return err
		}
	}
	return nil
}

// handleCloudApps answers the Cloud page: the apps, secret names and accounts the
// paired server Crew uses. Names and status only; values never leave the server.
func (api *StreamingAPI) handleCloudApps(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	roots, err := api.crewSyncRoots(r.Context(), claims, mux.Vars(r)["project"])
	if err != nil {
		externalError(w, 403, "unavailable", err.Error())
		return
	}
	p, paired, err := pairedServerCrewForRoots(claims.UserID, roots)
	if err != nil || !paired {
		externalJSON(w, map[string]any{"apps": []any{}, "secrets": []any{}, "accounts": []any{}})
		return
	}
	usable, settings, err := p.usableApps(r.Context())
	if err != nil {
		externalError(w, 502, "server_unavailable", err.Error())
		return
	}
	apps, secrets, accounts := []map[string]any{}, []string{}, []map[string]any{}
	seen := map[string]bool{}
	vault, _ := settings["vault"].(map[string]any)
	groups, _ := vault["vaults"].([]any)
	for _, group := range groups {
		g, _ := group.(map[string]any)
		items, _ := g["items"].([]any)
		for _, raw := range items {
			item, _ := raw.(map[string]any)
			kind, _ := item["kind"].(string)
			name := fmt.Sprint(item["name"])
			selected, _ := item["selected"].(bool)
			switch kind {
			case "mcp":
				if usable == nil || usable["vault_"+fmt.Sprint(item["id"])] {
					apps = append(apps, map[string]any{"name": name, "status": item["status"]})
				}
			case "project_secret", "shared_secret":
				if selected && !seen[name] {
					seen[name] = true
					secrets = append(secrets, name)
				}
			case "google", "slack":
				if selected {
					accounts = append(accounts, map[string]any{"name": name, "kind": kind})
				}
			}
		}
	}
	if stored, ok := settings["secrets"].(map[string]any); ok {
		names, _ := stored["stored"].([]any)
		for _, name := range names {
			if text := fmt.Sprint(name); !seen[text] {
				seen[text] = true
				secrets = append(secrets, text)
			}
		}
	}
	externalJSON(w, map[string]any{"apps": apps, "secrets": secrets, "accounts": accounts})
}
