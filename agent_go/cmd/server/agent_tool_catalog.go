package server

import (
	"slices"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
)

func agentSourceAction(name string) (platformtools.Action, bool) {
	for _, action := range platformtools.AgentActions {
		if action.Member == name && action.Source != "" {
			return action, true
		}
	}
	return platformtools.Action{}, false
}

func agentConsentMember(name string) string {
	if action, ok := agentSourceAction(name); ok {
		return action.Source
	}
	return name
}

// Restore only a catalog-owned selector, after validating the narrow member.
// This identity is also used by the existing guidance and consent classifiers.
func agentSourceCall(tool externalTool, args map[string]any) (externalTool, map[string]any) {
	action, ok := agentSourceAction(tool.Name)
	if !ok {
		return tool, args
	}
	resolved := map[string]any{}
	for key, value := range args {
		resolved[key] = value
	}
	resolved[action.SourceParameter()] = action.SourceAction
	tool.Name = action.Source
	return tool, resolved
}

func agentSourceMutates(source, operation string, original bool) bool {
	if knowledgebase.IsMCPTool(source) {
		return knowledgebase.ToolActionMutates(source, operation)
	}
	if isExternalVaultTool(source) {
		return !vaultOperationReads(source, map[string]any{"operation": operation})
	}
	switch source {
	case brainAdminTool:
		return brainAdminWriteActions[operation]
	case "crew_skills", "crew_mcp":
		return !crewCapabilityRead(operation)
	case externalCrewCommandsTool:
		return operation != "list"
	case "manage_crew_chats":
		return operation != "list" && operation != "read" && operation != "tools"
	case "builder_tabs":
		return operation != "list" && operation != "read"
	}
	return original
}

// Keep the original service/tool admission and derive only its actual enum.
func agentExternalMembers(catalog []externalTool) ([]externalTool, error) {
	result := []externalTool{}
	for _, tool := range catalog {
		for _, action := range platformtools.AgentActions {
			if action.Source != tool.Name || !platformtools.OffersSourceAction(tool.InputSchema, action) {
				continue
			}
			schema, err := platformtools.MemberSchema(tool.InputSchema, action)
			if err != nil {
				return nil, err
			}
			member := tool
			member.Name, member.Description, member.InputSchema = action.Member, action.Description, schema
			member.mutates = agentSourceMutates(tool.Name, action.SourceAction, tool.mutates)
			result = append(result, member)
		}
		result = append(result, tool)
	}
	return result, nil
}

// Existing mixed tools used to narrow their action enum for a reader. The
// flattened catalog must apply that same source rule to each offered action.
func agentSourceConsentAllowed(claims *UserClaims, tool externalTool) bool {
	action, ok := agentSourceAction(tool.Name)
	if !ok {
		return true
	}
	args := map[string]any{action.SourceParameter(): action.SourceAction}
	if action.Source == brainAdminTool && brainAdminWriteActions[action.SourceAction] && claims != nil && claims.AccessToken != nil && !claims.AccessToken.Allows("knowledgebase:write") {
		return false
	}
	if knowledgebase.IsMCPTool(action.Source) && !knowledgebaseConnectionAllowsAction(claims, action.Source, args) {
		return false
	}
	if isExternalVaultTool(action.Source) && vaultClaimsLevel(claims) == vaultRead && !vaultOperationReads(action.Source, args) {
		return false
	}
	if claims == nil || claims.AccessToken == nil {
		return true
	}
	switch action.Source {
	case "crew_skills", "crew_mcp", externalCrewCommandsTool:
		scope := "crews:write"
		if crewCapabilityRead(action.SourceAction) {
			scope = "crews:read"
		}
		return claims.AccessToken.Allows(scope)
	case "manage_crew_chats":
		if action.SourceAction == "tool" {
			return slices.ContainsFunc([]string{"crews:read", "crews:write", "crews:run"}, claims.AccessToken.Allows)
		}
		return claims.AccessToken.Allows(crewChatActionScope(action.SourceAction, ""))
	}
	return true
}
