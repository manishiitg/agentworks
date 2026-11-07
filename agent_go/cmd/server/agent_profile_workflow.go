package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

var agentProfileWorkflowToolNames = map[string]struct{}{
	"execute_step":        {},
	"query_step":          {},
	"send_step_message":   {},
	"stop_step":           {},
	"stop_all_executions": {},
	"list_executions":     {},
	"run_full_workflow":   {},
}

type agentProfileWorkflowRegistrar struct {
	target  definitionRegistrar
	prepare func(context.Context, string, map[string]interface{}) error
}

func (r *agentProfileWorkflowRegistrar) shouldRegister(name string) bool {
	_, ok := agentProfileWorkflowToolNames[name]
	return ok
}

func (r *agentProfileWorkflowRegistrar) wrap(name string, execute func(context.Context, map[string]interface{}) (string, error)) func(context.Context, map[string]interface{}) (string, error) {
	if execute == nil || (name != "execute_step" && name != "run_full_workflow") {
		return execute
	}
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		if args == nil {
			args = make(map[string]interface{})
		}
		if r.prepare != nil {
			if err := r.prepare(ctx, name, args); err != nil {
				return "Could not prepare this workflow run: " + err.Error(), nil
			}
		}
		return execute(ctx, args)
	}
}

func (r *agentProfileWorkflowRegistrar) RegisterCustomTool(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), category string) error {
	if !r.shouldRegister(name) {
		return nil
	}
	return r.target.RegisterCustomTool(name, description, parameters, r.wrap(name, execute), category)
}

func (r *agentProfileWorkflowRegistrar) RegisterCustomToolWithTimeout(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, category string) error {
	if !r.shouldRegister(name) {
		return nil
	}
	return r.target.RegisterCustomToolWithTimeout(name, description, parameters, r.wrap(name, execute), timeout, category)
}

func (r *agentProfileWorkflowRegistrar) AttachSkill(skill *llmtypes.Skill) error {
	return r.target.AttachSkill(skill)
}

func (r *agentProfileWorkflowRegistrar) AttachedSkills() []*llmtypes.Skill {
	return r.target.AttachedSkills()
}

func profileRequiresWorkflowRuntime(resolved *resolvedAgentProfile) bool {
	if resolved == nil {
		return false
	}
	requirement := resolved.Definition.Runtime.Capabilities.WorkflowExecution
	return requirement != "" && requirement != agentprofiles.CapabilityDisabled
}

func (api *StreamingAPI) registerAgentProfileWorkflowTools(
	ctx context.Context,
	registrar definitionRegistrar,
	resolved *resolvedAgentProfile,
	userID, sessionID, publicWorkspacePath string,
	selectedServers []string,
	mergedAPIKeys *llm.ProviderAPIKeys,
	req QueryRequest,
) error {
	if !profileRequiresWorkflowRuntime(resolved) {
		return nil
	}

	// The registrar below exposes only execution tools to the profile. Use the
	// normal Workshop runtime; Run is reserved for a read-only workflow user.
	runtimeWorkspacePath := agentProfileRuntimeWorkspace(userID, publicWorkspacePath)
	const runFolder = "iteration-0"
	// A browser tab/session can be reused after switching products or projects.
	// Include the immutable profile and workspace binding so a retained workshop
	// can never execute against the prior project's files.
	workshopKey := strings.Join([]string{
		"agent-profile-workflow",
		resolved.Definition.ID,
		fmt.Sprintf("v%d", resolved.Definition.Version),
		sessionID,
		runtimeWorkspacePath,
	}, ":")
	var workshopSession *todo_creation_human.WorkshopChatSession
	if cached, ok := api.workshopChatSessions.Load(workshopKey); ok {
		workshopSession, _ = cached.(*todo_creation_human.WorkshopChatSession)
		if workshopSession != nil {
			workshopSession.UpdateAPIKeys(mergedAPIKeys)
			workshopSession.SetWorkshopModeOverride("workshop")
		}
	}
	if workshopSession == nil {
		syntheticReq := req
		syntheticReq.SelectedFolder = runtimeWorkspacePath
		syntheticReq.ExecutionOptions = &ExecutionOptions{
			SelectedRunFolder: runFolder,
			ExecutionStrategy: "start_from_beginning_no_human",
			WorkshopMode:      "workshop",
		}
		cfg, err := api.buildWorkshopConfig(ctx, syntheticReq, userID, runtimeWorkspacePath, runFolder, selectedServers, sessionID, mergedAPIKeys)
		if err != nil {
			return fmt.Errorf("build profile workflow runtime: %w", err)
		}
		created, err := todo_creation_human.NewWorkshopChatSession(ctx, cfg)
		if err != nil {
			return fmt.Errorf("create profile workflow runtime: %w", err)
		}
		created.SetWorkshopModeOverride("workshop")
		workshopSession = created
		api.workshopChatSessions.Store(workshopKey, workshopSession)
	}

	workshopSession.SetExtraSubAgentNotifier(&workflowSubAgentTrackingNotifier{api: api, sessionID: sessionID})
	workshopSession.SetWorkshopExecutionNotifier(&workshopExecutionBgNotifier{
		api: api, sessionID: sessionID, workspacePath: runtimeWorkspacePath,
		presetQueryID: req.PresetQueryID, userID: userID,
	})
	workshopSession.SetOnStepCorrelationDone(cleanupStepDelegation)
	workshopSession.SetExecutionStateChecks(
		func() bool {
			api.pendingMu.RLock()
			defer api.pendingMu.RUnlock()
			return len(api.pendingCompletions[sessionID]) > 0
		},
		func() bool { return api.bgAgentRegistry.HasRunningAgents(sessionID) },
		func() { api.cancelBackgroundAgents(sessionID) },
		func() []todo_creation_human.ServerAgentInfo {
			agents := api.bgAgentRegistry.GetAll(sessionID)
			result := make([]todo_creation_human.ServerAgentInfo, 0, len(agents))
			for _, active := range agents {
				result = append(result, todo_creation_human.ServerAgentInfo{ID: active.ID, Name: active.Name, Status: string(active.GetStatus())})
			}
			return result
		},
	)

	filtered := &agentProfileWorkflowRegistrar{target: registrar}

	todo_creation_human.RegisterWorkshopChatTools(filtered, workshopSession, api.logger)
	todo_creation_human.RegisterRunFullWorkflowTool(filtered, workshopSession, api.logger)
	return nil
}
