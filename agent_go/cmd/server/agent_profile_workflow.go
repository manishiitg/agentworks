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
	"debug_step":          {},
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
	if execute == nil {
		return execute
	}
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		if args == nil {
			args = make(map[string]interface{})
		}
		if r.prepare != nil {
			if err := r.prepare(ctx, name, args); err != nil {
				return "", err
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
	if name == "execute_step" {
		description += " In a Crew chat, omit run_folder to continue this chat's current saved run and inputs; the first call starts a new run. Use new_run=true for a separate run. The current run survives runtime eviction and restarts."
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
	readOnly bool,
) error {
	if readOnly || !profileRequiresWorkflowRuntime(resolved) {
		return nil
	}

	root := agentProfileRuntimeWorkspace(userID, publicWorkspacePath)
	workflowID := ""
	if isCrewProjectPath(publicWorkspacePath) {
		binding, err := api.crewChatWorkflowTarget(ctx, resolved, userID, publicWorkspacePath, req)
		if err != nil {
			return err
		}
		if binding == nil {
			return nil
		}
		root, workflowID = binding.Root, binding.ID
	}
	guarded := workflowContractExecutionGuardRegistrar{definitionRegistrar: registrar, workspacePath: root}
	if workflowID != "" {
		guarded.boundWorkflow = &crewExecutableBinding{ID: workflowID, Root: root, Kind: "workflow"}
	}
	filtered := &agentProfileWorkflowRegistrar{target: guarded}
	filtered.prepare = func(callCtx context.Context, _ string, _ map[string]interface{}) error {
		if !isCrewProjectPath(publicWorkspacePath) {
			return nil
		}
		binding, err := api.crewChatWorkflowTarget(callCtx, resolved, userID, publicWorkspacePath, req)
		if err != nil {
			return err
		}
		if binding == nil || binding.Root != root || binding.ID != workflowID {
			return fmt.Errorf("this Crew's workflow or edit access changed; reopen its chat")
		}
		return nil
	}
	todo_creation_human.RegisterLazyWorkshopTools(filtered, func(callCtx context.Context) (*todo_creation_human.WorkshopChatSession, error) {
		return api.agentProfileWorkflowSession(callCtx, resolved, userID, sessionID, publicWorkspacePath, root, workflowID, selectedServers, mergedAPIKeys, req)
	}, api.logger)
	return nil
}

// The caller never chooses a workflow path. Crew ownership and its trusted
// executable binding select the one runtime, including joined Team editors.
func (api *StreamingAPI) crewChatWorkflowTarget(ctx context.Context, resolved *resolvedAgentProfile, userID, root string, req QueryRequest) (*crewExecutableBinding, error) {
	if req.PinRunMode || crewGuestCallerForTurn(req, userID) != "" {
		return nil, nil
	}
	claims := GetUserFromContext(ctx)
	if claims == nil && resolved != nil {
		claims = resolved.ToolCallerClaims
	}
	if claims == nil {
		claims = currentProjectCallerClaims(ctx, userID)
	}
	if claims == nil || claims.UserID != userID || !crewProjectOwnedByCaller(userID, root) || !workflowPermissionInfoForClaims(claims).CanWriteWorkflows {
		return nil, nil
	}
	bindings, err := readCrewExecutables()
	if err != nil {
		return nil, err
	}
	var found *crewExecutableBinding
	for _, binding := range bindings {
		if binding.Kind != "workflow" || canonicalCrewWorkspaceRoot(binding.CrewRoot) != canonicalCrewWorkspaceRoot(root) {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("this Crew has conflicting workflows; repair its workflow registration first")
		}
		checked, err := findCrewExecutable(binding.Root)
		if err != nil {
			return nil, err
		}
		if checked != nil && checked.ID == binding.ID && checked.CrewID == binding.CrewID {
			found = checked
		}
	}
	return found, nil
}

func (api *StreamingAPI) agentProfileWorkflowSession(ctx context.Context, resolved *resolvedAgentProfile, userID, sessionID, publicWorkspacePath, runtimeWorkspacePath, workflowID string, selectedServers []string, mergedAPIKeys *llm.ProviderAPIKeys, req QueryRequest) (*todo_creation_human.WorkshopChatSession, error) {
	// The registrar below exposes only execution tools to the profile. Use the
	// normal Workshop runtime; Run is reserved for a read-only workflow user.
	const runFolder = ""
	// A browser tab/session can be reused after switching products or projects.
	// Include the immutable profile and workspace binding so a retained workshop
	// can never execute against the prior project's files.
	workshopKey := strings.Join([]string{
		"agent-profile-workflow",
		resolved.Definition.ID,
		fmt.Sprintf("v%d", resolved.Definition.Version),
		userID,
		sessionID,
		runtimeWorkspacePath,
	}, ":")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if api.isSessionMarkedStopped(sessionID) {
		return nil, fmt.Errorf("this chat was stopped; send a new message before running its steps")
	}
	// Parallel tool calls share one first-use runtime, not two independent
	// controllers that could both launch the same step.
	release, err := lockCrewExecution(ctx, workshopKey)
	if err != nil {
		return nil, err
	}
	defer release()
	if api.isSessionMarkedStopped(sessionID) {
		return nil, fmt.Errorf("this chat was stopped; send a new message before running its steps")
	}
	var workshopSession *todo_creation_human.WorkshopChatSession
	if cached, ok := api.workshopChatSessions.Load(workshopKey); ok {
		workshopSession, _ = cached.(*todo_creation_human.WorkshopChatSession)
		if workshopSession != nil {
			workshopSession.UpdateAPIKeys(mergedAPIKeys)
			workshopSession.SetWorkshopModeOverride("workshop")
			return workshopSession, nil
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
			return nil, fmt.Errorf("build profile workflow runtime: %w", err)
		}
		if isCrewProjectPath(publicWorkspacePath) {
			store := defaultProductConversationRegistryStore()
			cfg.CurrentStepRun = func(callCtx context.Context) (string, error) {
				return store.workflowStepRun(callCtx, userID, sessionID, publicWorkspacePath, workflowID, nil)
			}
			cfg.SelectStepRun = func(callCtx context.Context, folder string) error {
				_, err := store.workflowStepRun(callCtx, userID, sessionID, publicWorkspacePath, workflowID, &folder)
				return err
			}
		}
		created, err := todo_creation_human.NewWorkshopChatSession(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("create profile workflow runtime: %w", err)
		}
		created.SetWorkshopModeOverride("workshop")
		workshopSession = created
		api.stoppedSessionsMu.RLock()
		stopped := api.stoppedSessions[sessionID] || ctx.Err() != nil
		if !stopped {
			api.workshopChatSessions.Store(workshopKey, workshopSession)
		}
		api.stoppedSessionsMu.RUnlock()
		if stopped {
			workshopSession.Close()
			return nil, fmt.Errorf("this chat was stopped while its workflow was starting; no step started")
		}
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

	return workshopSession, nil
}
