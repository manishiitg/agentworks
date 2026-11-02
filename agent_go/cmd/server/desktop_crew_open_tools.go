package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"
)

func (api *StreamingAPI) registerOpenServerProjectTool(reg definitionToolRegistrar, gate *productToolGate, claims *UserClaims, sessionID, root string) error {
	gate.Declare("open_server_project")
	return reg.RegisterCustomToolWithTimeout("open_server_project", "Open one of your existing connected-server projects on this Mac, keeping its identity, files and main conversation through Project sync. Use action=list to find your connected servers; provide server to list its projects. Then action=inspect or open with server and project (exact name or ID). Optional folder must be empty; default is under ~/AgentWorks. Uses only your own saved server sign-in and the server rechecks the project's actual owner. Opening copies completed work but does not start an agent or move editing; your next local message uses the normal handoff. Server secrets and logins are not copied; the result names connections to set up on the Mac. Available only in your local interactive Builder chat; a server chat can open the Project sync panel in the Mac app.", map[string]any{
		"type": "object", "properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"list", "inspect", "open"}},
			"server": map[string]any{"type": "string"}, "project": map[string]any{"type": "string"}, "folder": map[string]any{"type": "string"},
		}, "required": []string{"action"},
	}, func(ctx context.Context, args map[string]any) (string, error) {
		caller, err := api.interactiveLaptopOwner(ctx, claims, sessionID, root)
		if err != nil {
			return "", err
		}
		desktopConnectionsMu.Lock()
		connections, err := readDesktopConnections(caller.UserID)
		desktopConnectionsMu.Unlock()
		if err != nil {
			return "", err
		}
		action, server := externalArg(args, "action"), externalArg(args, "server")
		if action == "list" && server == "" {
			rows := []map[string]string{}
			for _, item := range connections {
				rows = append(rows, map[string]string{"id": item.ID, "name": item.Name, "server": item.Server})
			}
			raw, _ := json.Marshal(map[string]any{"servers": rows})
			return string(raw), nil
		}
		connectionID := ""
		for _, item := range connections {
			if server == item.ID || strings.EqualFold(server, item.Name) || server == item.Server {
				if connectionID != "" {
					return "", fmt.Errorf("more than one saved server matches; choose its exact ID from list")
				}
				connectionID = item.ID
			}
		}
		if connectionID == "" {
			return "", fmt.Errorf("choose a saved server from action=list; connect it in the Mac app first if needed")
		}
		connection, err := desktopConnectionFor(caller.UserID, connectionID)
		if err != nil {
			return "", err
		}
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, connection.Server+"/api/workspaces", strings.NewReader(`{"action":"list"}`))
		request.Header.Set("Authorization", "Bearer "+connection.Token)
		request.Header.Set("Content-Type", "application/json")
		response, err := desktopHTTPClient().Do(request)
		if err != nil {
			return "", fmt.Errorf("this server is unavailable; retry from the Mac app")
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return "", fmt.Errorf("could not list this server's projects; reconnect in the Mac app")
		}
		var inventory struct {
			Projects []desktopProject `json:"workspaces"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&inventory) != nil {
			return "", fmt.Errorf("the server's project list could not be read")
		}
		if action == "list" {
			raw, _ := json.Marshal(map[string]any{"projects": inventory.Projects})
			return string(raw), nil
		}
		if action != "inspect" && action != "open" {
			return "", fmt.Errorf("choose list, inspect or open")
		}
		project, selected := externalArg(args, "project"), ""
		for _, item := range inventory.Projects {
			if item.ID == project || item.Name == project {
				if selected != "" {
					return "", fmt.Errorf("more than one project matches; choose its exact ID from list")
				}
				selected = item.ID
			}
		}
		if selected == "" {
			return "", fmt.Errorf("choose an existing project from action=list for this server")
		}
		local := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(context.WithValue(ctx, UserContextKey, caller))
		out, err := api.openServerCrewOnMac(local, connectionID, selected, externalArg(args, "folder"), action == "inspect")
		if err != nil {
			return "", err
		}
		raw, _ := json.Marshal(out)
		return string(raw), nil
	}, 5*time.Minute, "workspace_advanced")
}
