package server

import (
	"context"
	"encoding/json"
	"fmt"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	agent "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentwrapper"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// registerWorkBackgroundTools reuses AgentWorks' established asynchronous
// delegation registry and AUTO-NOTIFICATION lifecycle, but exposes the Work
// vocabulary and none of the workflow plan/schedule surface.
func (api *StreamingAPI) registerWorkBackgroundTools(
	llmAgent *agent.LLMAgentWrapper,
	profile *resolvedAgentProfile,
	parentReq QueryRequest,
	sessionID string,
	userID string,
) error {
	if profile == nil || !isProjectProfileID(profile.Definition.ID) || !agentprofiles.HasFeature(profile.Definition, "background-work") {
		return nil
	}
	if !isActiveWorkProjectWorkspace(userID, parentReq.SelectedFolder) {
		return nil
	}
	if llmAgent == nil || llmAgent.GetUnderlyingAgent() == nil {
		return fmt.Errorf("Work background tools require an active agent")
	}

	tierConfig := resolveDelegationTierConfig(parentReq.DelegationTierConfig)
	definitions := virtualtools.CreateDelegationTools(tierConfig, true)
	executors := virtualtools.CreateDelegationToolExecutors()
	category := virtualtools.GetDelegationToolCategory()
	backgroundDelegate := func(ctx context.Context, name, instruction string) (string, error) {
		ctx = withDelegatedParentSkillDefinitions(ctx, llmAgent.AttachedSkills())
		return api.executeBackgroundDelegatedTask(ctx, parentReq, sessionID, name, instruction)
	}
	querier := &bgAgentQuerierImpl{registry: api.bgAgentRegistry}

	for _, definition := range definitions {
		if definition.Function == nil {
			continue
		}
		internalName := definition.Function.Name
		publicName := internalName
		description := definition.Function.Description
		if internalName == "delegate" {
			publicName = "run_in_background"
			description = "Start an independent background coding agent with the same project access and attached skills. Returns immediately; Work automatically resumes this chat with the result when the agent finishes. Use reasoning_level high, medium, or low. Do not poll unless the user asks for live status."
		}
		executor := executors[internalName]
		if executor == nil {
			continue
		}
		var parameters map[string]interface{}
		if definition.Function.Parameters != nil {
			encoded, err := json.Marshal(definition.Function.Parameters)
			if err != nil {
				return fmt.Errorf("encode %s parameters: %w", publicName, err)
			}
			if err := json.Unmarshal(encoded, &parameters); err != nil {
				return fmt.Errorf("decode %s parameters: %w", publicName, err)
			}
		}
		captured := executor
		wrapped := func(ctx context.Context, args map[string]interface{}) (string, error) {
			ctx = context.WithValue(ctx, common.UserIDKey, userID)
			ctx = context.WithValue(ctx, virtualtools.SessionEventEmitterKey, &sessionEventEmitter{eventStore: api.eventStore, sessionID: sessionID})
			ctx = context.WithValue(ctx, virtualtools.ChatsFolderKey, perUserChatsFolderFor(userID))
			ctx = context.WithValue(ctx, virtualtools.BackgroundDelegateKey, virtualtools.BackgroundDelegateFunc(backgroundDelegate))
			ctx = context.WithValue(ctx, virtualtools.BGAgentRegistryKey, querier)
			ctx = context.WithValue(ctx, virtualtools.BGAgentSessionIDKey, sessionID)
			if tierConfig != nil {
				ctx = context.WithValue(ctx, virtualtools.DelegationTierConfigKey, tierConfig)
			}
			return captured(ctx, args)
		}
		if err := llmAgent.RegisterCustomToolWithTimeout(publicName, description, parameters, wrapped, 0, category); err != nil {
			return fmt.Errorf("register %s: %w", publicName, err)
		}
	}
	return nil
}
