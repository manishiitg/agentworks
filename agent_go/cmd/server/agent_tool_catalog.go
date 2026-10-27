package server

import (
	"slices"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
)

func agentCatalogActions() []platformtools.Action {
	return append(slices.Clone(platformtools.AgentActions), platformtools.ExperienceActions...)
}

func agentSourceAction(name string) (platformtools.Action, bool) {
	for _, action := range agentCatalogActions() {
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
	case "manage_schedules":
		return operation != "list" && operation != "runs"
	case "manage_triggers":
		return operation != "list" && operation != "runs" && operation != "status"
	case "manage_condition_triggers":
		return operation != "list" && operation != "history" && operation != "read_script"
	case "crew_skills", "crew_mcp":
		return !crewCapabilityRead(operation)
	case externalCrewCommandsTool:
		return operation != "list"
	case "manage_crew_chats":
		return operation != "list" && operation != "read" && operation != "tools" && operation != "project_context" && operation != "native_list"
	case "builder_tabs":
		return operation != "list" && operation != "read"
	}
	return original
}

// Keep the original service/tool admission and derive only its actual enum.
func agentExternalMembers(catalog []externalTool) ([]externalTool, error) {
	var err error
	catalog, err = crewIntegrationExternalMembers(catalog)
	if err != nil {
		return nil, err
	}
	result := []externalTool{}
	for _, tool := range catalog {
		for _, action := range agentCatalogActions() {
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
		// A source that already owns a public family is captured by its fixed
		// members above. Keep one public family entry, not a same-name raw tool.
		selfSource := slices.ContainsFunc(agentCatalogActions(), func(action platformtools.Action) bool {
			return action.Source == tool.Name && action.Family == tool.Name
		})
		if !selfSource {
			result = append(result, tool)
		}
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
	case "manage_schedules", "manage_triggers":
		t := claims.AccessToken
		switch action.SourceAction {
		case "list", "runs", "status":
			return t.Allows("crews:read") || t.Allows("workflows:read") || t.Allows("runs:execute")
		case "run_now", "stop", "test":
			return t.Allows("crews:run") || t.Allows("runs:execute")
		default:
			return t.Allows("crews:write") || t.BuilderAccess() || t.RelayBuilderAccess()
		}
	case "manage_condition_triggers":
		return externalWatchersAllowed(claims)
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

// Capture direct integration members only after the original product group
// has been admitted. Product declarations remain the source of availability.
func crewIntegrationExternalMembers(catalog []externalTool) ([]externalTool, error) {
	result := []externalTool{}
	for _, source := range catalog {
		if source.Name == "crew_integrations" {
			definitions, err := crewIntegrationDefinitions()
			if err != nil {
				return nil, err
			}
			for _, definition := range definitions {
				if definition.Function == nil || !platformtools.OffersAction(source.InputSchema, definition.Function.Name) {
					continue
				}
				schema, err := crewExecutionSchema(definition)
				if err != nil {
					return nil, err
				}
				props, err := externalSchemaProperties(definition.Function.Name, schema)
				if err != nil {
					return nil, err
				}
				props["crew_id"] = externalString("Crew ID from workspace action=list.")
				required, _ := schema["required"].([]any)
				schema["required"] = append([]any{"crew_id"}, required...)
				member := source
				member.Name, member.Description, member.InputSchema = definition.Function.Name, definition.Function.Description, schema
				member.hidden, member.internalOnly, member.mutates = true, true, true
				result = append(result, member)
			}
			source.hidden, source.internalOnly = true, true
		}
		result = append(result, source)
	}
	return result, nil
}
