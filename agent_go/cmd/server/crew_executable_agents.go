package server

import (
	"context"
	"fmt"
	"strings"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectinstructions"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	mcpclient "github.com/manishiitg/mcpagent/mcpclient"
)

type crewExecutableAgentChoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func crewExecutableAgentChoices(ctx context.Context, owner, crewID, root string) ([]crewExecutableAgentChoice, error) {
	tabs, err := crewOwnerChatRecords(ctx, owner, "work", crewID, root)
	if err != nil {
		return nil, err
	}
	choices := []crewExecutableAgentChoice{}
	for _, tab := range tabs {
		choices = append(choices, crewExecutableAgentChoice{ID: tab.Record.ConversationKey, Name: firstNonEmptyTrimmed(tab.Identity.Name, tab.Record.Title, "Main")})
	}
	return choices, nil
}

// Configuration-only reuse: never prepare/restore a product chat turn. The
// shared workflow executor creates its own run-scoped sequence and native session.
func (r *crewStepRunner) ResolveCrewAgentConfiguration(ctx context.Context, workspace, agentID string) (stepworkflow.CrewAgentConfiguration, error) {
	var result stepworkflow.CrewAgentConfiguration
	if r == nil || r.crews == nil || r.crews.registry == nil {
		return result, fmt.Errorf("Crew services are unavailable")
	}
	manifest, found, err := ReadWorkflowManifest(ctx, workspace)
	if err != nil || !found || manifest.crewExecutable == nil {
		return result, fmt.Errorf("this workflow has no owning Crew")
	}
	claims := GetUserFromContext(internalBotRequestContext(ctx, r.userID))
	if claims == nil || claims.UserID != r.userID || workflowAccessForManifest(claims, manifest) == WorkflowAccessNone {
		return result, fmt.Errorf("this Crew is unavailable")
	}
	binding := manifest.crewExecutable
	owner, ok := crewProjectOwnerID(binding.CrewRoot)
	if !ok || directoryUserIsDisabled(&UserClaims{UserID: owner}) {
		return result, fmt.Errorf("the Crew owner's account is unavailable")
	}
	profile, err := r.crews.registry.Resolve("work", 0, owner)
	if err != nil {
		return result, err
	}
	project, err := resolveProductConversationBinding(ctx, owner, profile, binding.CrewID)
	if err != nil || canonicalCrewWorkspaceRoot(project.WorkspacePath) != canonicalCrewWorkspaceRoot(binding.CrewRoot) {
		return result, fmt.Errorf("the owning Crew is unavailable")
	}
	tabs, err := crewOwnerChatRecords(ctx, owner, "work", binding.CrewID, binding.CrewRoot)
	if err != nil {
		return result, err
	}
	for _, tab := range tabs {
		if tab.Record.ConversationKey != strings.TrimSpace(agentID) {
			continue
		}
		model, err := effectiveProfileChatModel(profile, project, tab.Record, tab.Identity)
		if err != nil {
			return result, err
		}
		result.Name = firstNonEmptyTrimmed(tab.Identity.Name, tab.Record.Title, "Main")
		result.Model = &stepworkflow.AgentLLMConfig{Provider: model.Provider, ModelID: model.ModelID, ConnectionID: model.ConnectionID, Options: providerOptionRuntimeOptions(profile.Runtime, model.Provider, model.ModelID)}
		if model.ReasoningEffort != "" {
			if result.Model.Options == nil {
				result.Model.Options = map[string]interface{}{}
			}
			result.Model.Options["reasoning_effort"] = model.ReasoningEffort
		}
		result.Instructions = agentRolePrompt(ctx, tab.OwnerID, "work", tab.Record.ConversationKey, binding.CrewRoot)
		instructions, exists, err := readFileFromWorkspace(ctx, binding.CrewRoot+"/"+projectinstructions.FileName)
		if err != nil {
			return result, err
		}
		if exists {
			result.Instructions += "\n\n" + projectinstructions.Section(instructions)
		}
		parent, err := readCrewProjectManifests(ctx, "work", binding.CrewRoot)
		if err != nil {
			return result, err
		}
		if strings.TrimSpace(parent.Description) != "" {
			result.Instructions += "\n\n## Crew purpose\n" + parent.Description
		}
		result.Instructions += "\n\n" + workflowtypes.ChatInstructionsPrompt(parent.ChatInstructions)
		result.Servers = append([]string{}, runtimeAppSelection(parent.Capabilities.SelectedServers, parent.Capabilities.ChosenVaultAppsOnly)...)
		if len(result.Servers) == 0 {
			result.Servers = []string{mcpclient.NoServers}
		}
		result.Tools = append([]string{}, parent.Capabilities.SelectedTools...)
		result.Skills = append([]string{}, parent.Capabilities.SelectedSkills...)
		return result, nil
	}
	return result, fmt.Errorf("the selected Crew agent is no longer available; choose an existing agent")
}
