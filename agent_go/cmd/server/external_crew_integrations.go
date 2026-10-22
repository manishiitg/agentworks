package server

import (
	"context"
	"fmt"
	"sort"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

var crewIntegrationMembers = map[string]bool{
	"get_slack_bot_settings": true, "get_slack_bot_credentials": true,
	"configure_slack_bot": true, "test_slack_bot_connection": true,
	"create_slack_bot_route": true, "update_slack_bot_route_permission": true, "remove_slack_bot_route": true,
	"slack": true, "send_slack_message": true, "google_workspace_cli": true,
	"manage_google_account": true, "manage_slack_account": true, "manage_slack_bot": true, "list_gmail_connections": true, "update_gmail_connection_grants": true,
	"get_agent_inbox": true, "configure_agent_inbox": true,
}

func (api *StreamingAPI) crewIntegrationRegistry(root, session string) (crewCapabilityRegistrar, error) {
	reg := crewCapabilityRegistrar{}
	if err := api.registerAgentInboxTools(reg, root, true); err != nil {
		return nil, err
	}
	if err := api.registerSlackBotTools(reg, session, root, "work", true, true); err != nil {
		return nil, err
	}
	if err := api.registerGmailConnectionManagementTools(reg, session, root); err != nil {
		return nil, err
	}
	executors := virtualtools.CreateHumanToolExecutors()
	executors["send_slack_message"] = api.sendSlackMessageFromTool
	for _, tool := range virtualtools.CreateHumanTools() {
		if tool.Function == nil || (tool.Function.Name != "send_slack_message" && tool.Function.Name != "google_workspace_cli") {
			continue
		}
		schema, err := crewExecutionSchema(tool)
		if err != nil {
			return nil, err
		}
		if err = reg.RegisterCustomTool(tool.Function.Name, tool.Function.Description, schema, executors[tool.Function.Name], "crew_integrations"); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

func crewIntegrationDefinitions() ([]llmtypes.Tool, error) {
	api := &StreamingAPI{}
	reg, err := api.crewIntegrationRegistry("Crew/catalog", "")
	if err != nil {
		return nil, err
	}
	names := []string{}
	for name := range reg {
		if crewIntegrationMembers[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	tools := []llmtypes.Tool{}
	for _, name := range names {
		tool := reg[name]
		tools = append(tools, llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: name, Description: tool.description, Parameters: llmtypes.NewParameters(tool.schema)}})
	}
	return tools, nil
}

// Granted only by the direct transport after it resolves current Crew ownership
// and existing authoring consent. Native tools can recognize an explicit owner
// call without inventing an active chat or dropping token claims.
type externalCrewDirectAuthorityKey struct{}
type externalCrewDirectAuthority struct{ crewID, root, user string }

func (api *StreamingAPI) externalCrewDirectOwner(ctx context.Context, root string) error {
	authority, ok := ctx.Value(externalCrewDirectAuthorityKey{}).(externalCrewDirectAuthority)
	claims := GetUserFromContext(ctx)
	if !ok || claims == nil || authority.root != root || authority.user != claims.UserID {
		return fmt.Errorf("explicit authenticated Crew authoring context required")
	}
	if status, _ := externalCrewAuthorGate(claims); status != 0 {
		return fmt.Errorf("current Crew authoring access required")
	}
	if token := claims.AccessToken; token != nil && (!token.Allows("crews:write") || !token.AllowsCrew(authority.crewID) || token.FileGuard != nil) {
		return fmt.Errorf("current unrestricted Crew authoring consent required")
	}
	crew, _, _, ok := api.externalCrewResolve(ctx, claims, authority.crewID)
	if !ok || !crew.OwnedByCaller || agentProfileRuntimeWorkspace(crew.OwnerID, crew.Binding.WorkspacePath) != root {
		return fmt.Errorf("current Crew ownership required")
	}
	return nil
}

func isExternalCrewDirectCall(ctx context.Context) bool {
	_, ok := ctx.Value(externalCrewDirectAuthorityKey{}).(externalCrewDirectAuthority)
	return ok
}
