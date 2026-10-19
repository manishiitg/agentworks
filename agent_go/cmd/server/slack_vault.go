package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// My Vault manages the existing encrypted registry. A product stores only a
// selected connection ID; ownership still comes from the original scope.
type slackVaultBot struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	Enabled    bool   `json:"enabled"`
	CanManage  bool   `json:"can_manage"`
	ScopeLabel string `json:"scope_label"`
	Shared     bool   `json:"shared"`
}
type slackVaultScope struct {
	Workspace string `json:"workspace_path"`
	Label     string `json:"label"`
}

func (api *StreamingAPI) slackVaultTarget(r *http.Request, workspace string) (services.SlackTargetRef, bool, error) {
	scope, err := personalConnectionItemScope(r, workspace, false, "Slack", "Slack")
	if err != nil {
		return services.SlackTargetRef{}, false, err
	}
	ref := services.SlackTargetRef{WorkspacePath: scope.Workspace}
	if isProjectWorkspacePath(workspace) {
		raw, found, err := readFileFromWorkspace(r.Context(), scope.Workspace+"/product.json")
		var manifest productProjectManifest
		if err != nil || !found || json.Unmarshal([]byte(raw), &manifest) != nil || manifest.ID == "" {
			return ref, false, fmt.Errorf("Open an existing Crew to choose its Slack bot.")
		}
		ref.ProfileID = manifest.Product
		return ref, true, nil
	}
	manifest, found, err := ReadWorkflowManifest(r.Context(), scope.Workspace)
	if err != nil || !found || manifest == nil {
		return ref, false, fmt.Errorf("Open an existing Goal or Flow to choose Slack settings.")
	}
	return ref, manifest.Kind != "relay", nil
}

func safeSlackVaultBot(r *http.Request, api *StreamingAPI, conn services.SlackConnection) slackVaultBot {
	label := "Company administrators"
	if conn.WorkspacePath != "" {
		label = slackDestinationLabel(r.Context(), conn.WorkspacePath, conn.ProfileID)
	}
	return slackVaultBot{ID: conn.ID, Name: conn.DisplayName, Configured: conn.BotToken != "" && conn.AppToken != "", Enabled: conn.Enabled, CanManage: requireSlackConnectionAccess(r, api, conn) == nil, ScopeLabel: label, Shared: conn.WorkspacePath == ""}
}

func (api *StreamingAPI) slackVaultScopes(r *http.Request) []slackVaultScope {
	out := []slackVaultScope{}
	if workflows, err := DiscoverWorkflowManifests(r.Context()); err == nil {
		for _, item := range workflows {
			if item.Manifest == nil || item.Manifest.Kind == "relay" {
				continue
			}
			if requireSlackTargetOwner(r.Context(), api, services.SlackTargetRef{WorkspacePath: item.WorkspacePath}) == nil {
				out = append(out, slackVaultScope{item.WorkspacePath, "Goal · " + firstNonBlank(item.Manifest.Label, item.Manifest.ID)})
			}
		}
	}
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		return out
	}
	if crews, err := api.externalCrewsVisible(r.Context(), claims, ""); err == nil {
		for _, item := range crews {
			id, _ := item["id"].(string)
			crew, manifest, _, ok := api.externalCrewResolve(r.Context(), claims, id)
			if !ok {
				continue
			}
			path := agentProfileRuntimeWorkspace(crew.OwnerID, crew.Binding.WorkspacePath)
			if requireSlackTargetOwner(r.Context(), api, services.SlackTargetRef{WorkspacePath: path, ProfileID: manifest.Product}) == nil {
				out = append(out, slackVaultScope{path, "Crew · " + manifest.displayTitle()})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

func (api *StreamingAPI) slackVaultBotState(r *http.Request, workspace string) (map[string]any, error) {
	svc, err := ensureSlackService()
	if err != nil {
		return nil, err
	}
	out := map[string]any{"bots": []slackVaultBot{}, "scopes": []slackVaultScope{}, "can_create_company": currentUserIsAdmin(r), "selected_id": ""}
	var ref services.SlackTargetRef
	var settings *SlackTargetSettingsResponse
	if workspace != "" {
		var allowed bool
		ref, allowed, err = api.slackVaultTarget(r, workspace)
		if err != nil {
			return nil, err
		}
		out["allow_bots"] = allowed
		out["target"] = ref
		out["can_select"] = allowed && requireSlackTargetOwner(r.Context(), api, ref) == nil
		if !allowed {
			return out, nil
		}
		query := r.URL.Query()
		query.Set("workspace_path", ref.WorkspacePath)
		query.Set("profile_id", ref.ProfileID)
		status, body := scheduleHandler(r, slackTargetSettingsHandler(api), http.MethodGet, "/api/human-feedback/slack/targets/settings", nil, query, nil)
		if status != 200 {
			return nil, fmt.Errorf("Cannot read Slack bot settings: %s", strings.TrimSpace(string(body)))
		}
		settings = &SlackTargetSettingsResponse{}
		if err = json.Unmarshal(body, settings); err != nil {
			return nil, err
		}
		out["settings"] = settings
		selected := ""
		if ref.ProfileID != "" {
			selected, err = productSlackConnectionID(r.Context(), ref.ProfileID, ref.WorkspacePath)
		} else {
			manifest, _, e := ReadWorkflowManifest(r.Context(), ref.WorkspacePath)
			err = e
			if manifest != nil {
				selected = manifest.Capabilities.SlackConnectionID
			}
		}
		if err != nil {
			return nil, err
		}
		if selected == "" && settings.PlatformBot {
			selected = svc.DefaultConnectionID()
		}
		out["selected_id"] = selected
	} else {
		out["scopes"] = api.slackVaultScopes(r)
	}
	bots := []slackVaultBot{}
	for _, conn := range svc.ListConnections() {
		manageable := requireSlackConnectionAccess(r, api, conn) == nil
		shared := workspace != "" && conn.WorkspacePath == "" && settings != nil && settings.PlatformAvailable && settings.ProductAllowed && conn.ID == svc.DefaultConnectionID()
		selected := out["selected_id"] == conn.ID
		if !manageable && !shared && !selected {
			continue
		}
		bot := safeSlackVaultBot(r, api, conn)
		bots = append(bots, bot)
	}
	sort.Slice(bots, func(i, j int) bool { return bots[i].Name < bots[j].Name })
	out["bots"] = bots
	return out, nil
}

// Both the screen and native/external MCP call this operation. Handler reuse
// preserves the registry's encryption, duplicate-token checks and owner rules.
func (api *StreamingAPI) slackVaultBotOperation(r *http.Request, workspace string, args map[string]any) (any, error) {
	if err := personalAccountManagementScope(r, workspace, false); err != nil {
		return nil, err
	}
	action, _ := args["action"].(string)
	id, _ := args["connection_id"].(string)
	if action == "list_saved" {
		return api.slackVaultBotState(r, workspace)
	}
	svc, err := ensureSlackService()
	if err != nil {
		return nil, err
	}
	if action == "select" {
		if workspace == "" {
			return nil, fmt.Errorf("Open the Crew or Goal's Slack page to choose a bot from My Vault.")
		}
		ref, allowed, err := api.slackVaultTarget(r, workspace)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, fmt.Errorf("Flows support Act in Slack only. Choose a personal account on the Slack page.")
		}
		if err := requireSlackTargetOwner(r.Context(), api, ref); err != nil {
			return nil, err
		}
		// MCP keeps the same item-bound Builder consent as personal account selection.
		if _, err := personalConnectionItemScope(r, workspace, true, "Slack", "Slack"); err != nil {
			return nil, err
		}
		conn, found := svc.GetConnection(id)
		if !found {
			return nil, fmt.Errorf("Choose an existing bot from My Vault.")
		}
		if conn.WorkspacePath != "" {
			if err := requireSlackConnectionAccess(r, api, conn); err != nil {
				return nil, err
			}
		}
		payload := map[string]any{"workspace_path": ref.WorkspacePath, "profile_id": ref.ProfileID, "platform_bot": conn.WorkspacePath == ""}
		if conn.WorkspacePath == "" {
			_, platform, available := slackPlatformBot()
			if !available || platform.ID != id {
				return nil, fmt.Errorf("The company bot is not available. Ask an administrator to check My Vault.")
			}
		} else if !currentSlackRef(services.SlackTargetRef{WorkspacePath: conn.WorkspacePath, ProfileID: conn.ProfileID}).Same(currentSlackRef(ref)) {
			status, body := scheduleHandler(r, putSlackConnectionTargetHandler(api), http.MethodPut, "/api/human-feedback/slack/connections/"+id+"/targets", map[string]string{"id": id}, nil, payload)
			if status >= 400 {
				return nil, fmt.Errorf("%s", strings.TrimSpace(string(body)))
			}
		}
		if conn.WorkspacePath == "" || slackPlatformTargetOn(r.Context(), api, ref) {
			status, body := scheduleHandler(r, slackTargetSettingsHandler(api), http.MethodPut, "/api/human-feedback/slack/targets/settings", nil, nil, payload)
			if status >= 400 {
				return nil, fmt.Errorf("%s", strings.TrimSpace(string(body)))
			}
		}
		if ref.ProfileID != "" {
			err = updateProductSlackConnectionID(r.Context(), ref.ProfileID, ref.WorkspacePath, id)
		} else {
			manifest, _, e := ReadWorkflowManifest(r.Context(), ref.WorkspacePath)
			err = e
			if err == nil && manifest != nil {
				manifest.Capabilities.SlackConnectionID = id
				err = WriteWorkflowManifest(r.Context(), ref.WorkspacePath, manifest)
			}
		}
		if err != nil {
			return nil, err
		}
		registerSlackBotConnectorForOwnedConnections(api, svc)
		return api.slackVaultBotState(r, workspace)
	}
	// Saved credential records keep their existing management scope. A bound MCP
	// client cannot use credential administration to escape its selected item.
	if id != "" {
		conn, found := svc.GetConnection(id)
		if !found {
			return nil, fmt.Errorf("Choose a saved bot in My Vault.")
		}
		if err := requireSlackConnectionAccess(r, api, conn); err != nil {
			return nil, err
		}
		if claims := GetUserFromContext(r.Context()); claims != nil && claims.AccessToken != nil && workspace != "" {
			ref, _, err := api.slackVaultTarget(r, workspace)
			if err != nil {
				return nil, err
			}
			if !currentSlackRef(ref).Same(currentSlackRef(services.SlackTargetRef{WorkspacePath: conn.WorkspacePath, ProfileID: conn.ProfileID})) {
				return nil, fmt.Errorf("This connection is outside the selected item's bot management scope. Open My Vault to manage it.")
			}
		}
	}
	payload := map[string]any{}
	for _, key := range []string{"display_name", "bot_token", "app_token", "enabled"} {
		if value, ok := args[key]; ok {
			payload[key] = value
		}
	}
	var handler http.HandlerFunc
	method := http.MethodPost
	switch action {
	case "save":
		if id == "" {
			if workspace != "" {
				ref, allowed, e := api.slackVaultTarget(r, workspace)
				if e != nil {
					return nil, e
				}
				if !allowed {
					return nil, fmt.Errorf("Choose a Crew or Goal to manage this bot. Flows use personal Slack accounts only.")
				}
				payload["workspace_path"], payload["profile_id"] = ref.WorkspacePath, ref.ProfileID
			}
			handler = createSlackConnectionHandler(api)
		} else {
			handler = updateSlackConnectionHandler(api)
			method = http.MethodPatch
		}
	case "remove":
		if id == "" {
			return nil, fmt.Errorf("Choose a saved bot to remove.")
		}
		handler = deleteSlackConnectionHandler(api)
		method = http.MethodDelete
	case "test":
		if id == "" {
			return nil, fmt.Errorf("Choose a saved bot to test.")
		}
		handler = testSlackConnectionEntryHandler(api)
	default:
		return nil, fmt.Errorf("Choose list_saved, select, save, remove or test.")
	}
	status, body := scheduleHandler(r, handler, method, "/api/human-feedback/slack/connections/"+id, map[string]string{"id": id}, nil, payload)
	if status >= 400 {
		return nil, fmt.Errorf("%s", strings.TrimSpace(string(body)))
	}
	if action == "test" {
		var result any
		if json.Unmarshal(body, &result) == nil {
			return result, nil
		}
	}
	return map[string]any{"updated": true}, nil
}

func slackPlatformTargetOn(ctx context.Context, api *StreamingAPI, ref services.SlackTargetRef) bool {
	set := slackLoadTargetsRegistry(ctx)
	for _, target := range set.Targets {
		if target.Ref().Same(ref) {
			return target.PlatformBot
		}
	}
	return false
}

func (api *StreamingAPI) handleSlackVaultBots(w http.ResponseWriter, r *http.Request) {
	args := map[string]any{"action": "list_saved"}
	workspace := r.URL.Query().Get("workspace_path")
	if r.Method == http.MethodPost {
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&args) != nil {
			writeAgentProfileError(w, 400, "Choose a Slack bot and action.")
			return
		}
		workspace, _ = args["workspace_path"].(string)
	}
	result, err := api.slackVaultBotOperation(r, workspace, args)
	if err != nil {
		writeAgentProfileError(w, 403, err.Error())
		return
	}
	writeAgentProfileJSON(w, 200, result)
}
func (api *StreamingAPI) registerSlackVaultBotTool(reg definitionToolRegistrar, workspace string) error {
	return reg.RegisterCustomTool("manage_slack_bot", "Choose a saved My Vault bot for this Crew or Goal, using the same server action as its Slack page. list_saved shows available bots and the current selection. select uses a listed connection_id without copying credentials. Flows support Act in Slack only; use manage_slack_account there. Bot credentials are managed in My Vault with existing owner/admin permissions. Preserve existing channel routes and personal account permissions.", map[string]any{"type": "object", "additionalProperties": false, "required": []string{"action"}, "properties": map[string]any{
		"action": map[string]any{"type": "string", "enum": []string{"list_saved", "select"}}, "connection_id": map[string]any{"type": "string", "description": "Existing bot ID returned by list_saved."},
	}}, func(ctx context.Context, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action != "list_saved" && action != "select" {
			return "", fmt.Errorf("Choose list_saved or select. Manage credentials in My Vault.")
		}
		r := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
		result, err := api.slackVaultBotOperation(r, workspace, args)
		if err != nil {
			return "", err
		}
		raw, err := json.Marshal(result)
		return string(raw), err
	}, "mcp_server_tools")
}
