package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	eventbridge "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/event_bridge"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/mcpagent/llm"
)

// buildWorkshopGroupInfo builds a human-readable summary of available variable groups
// for the interactive-workshop system prompt. Includes the user-selected group if any.
func buildWorkshopGroupInfo(
	ctx context.Context,
	workspacePath string,
	readFile func(context.Context, string) (string, error),
	selectedRunFolder string,
	enabledGroupNames []string,
) string {
	// Read variables manifest
	varPath := workspacePath + "/variables/variables.json"
	content, err := readFile(ctx, varPath)
	if err != nil {
		return ""
	}

	var manifest todo_creation_human.VariablesManifest
	if err := json.Unmarshal([]byte(content), &manifest); err != nil {
		return ""
	}

	if len(manifest.Groups) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**%d saved input presets** available:\n", len(manifest.Groups)))
	for _, g := range manifest.Groups {
		// Mark the user-selected group
		selected := ""
		for _, eid := range enabledGroupNames {
			if eid == g.Name {
				selected = " **[SELECTED]**"
				break
			}
		}
		sb.WriteString(fmt.Sprintf("- **%s** (input_preset: `%s`)%s\n", g.Name, g.Name, selected))
	}

	if selectedRunFolder != "" {
		sb.WriteString(fmt.Sprintf("\nSelected run folder: `%s`\n", selectedRunFolder))
	}

	if len(enabledGroupNames) > 0 {
		sb.WriteString(fmt.Sprintf("\nSelected input preset: %v — use it as the default input_preset for execute_step calls.\n", enabledGroupNames))
	}

	return sb.String()
}

// buildWorkshopConfig loads the full preset and builds a WorkshopConfig that replicates
// the exact same tool/LLM/browser/image-gen setup as a normal workflow execution.
// This mirrors the logic in the /api/workflow handler (lines ~2003-2260) so the workshop
// gets the same tools, executors, categories, and LLM configs.
func (api *StreamingAPI) buildWorkshopConfig(
	ctx context.Context,
	req QueryRequest,
	currentUserID string,
	workspacePath string,
	runFolder string,
	selectedServers []string,
	sessionID string,
	apiKeys ...*llm.ProviderAPIKeys, // Optional pre-loaded keys (avoids canceled context issues)
) (*todo_creation_human.WorkshopConfig, error) {
	// Extract enabled group names from execution options (toolbar selection)
	var enabledGroupNames []string
	if req.ExecutionOptions != nil {
		enabledGroupNames = req.ExecutionOptions.EnabledGroupNames
	}

	// Always use merged API keys (env + workspace) for workshop orchestrator
	workshopLLMConfig := req.LLMConfig
	if workshopLLMConfig == nil {
		workshopLLMConfig = &orchestrator.LLMConfig{}
	}
	var preloadedAPIKeys *llm.ProviderAPIKeys
	if len(apiKeys) > 0 && apiKeys[0] != nil {
		preloadedAPIKeys = apiKeys[0]
	}
	workshopAPIKeys, credentialErr := api.resolveEffectiveAPIKeys(ctx, currentUserID, workspacePath, preloadedAPIKeys)
	if credentialErr != nil {
		return nil, fmt.Errorf("load workflow provider credentials: %w", credentialErr)
	}
	workshopLLMConfig.APIKeys = workshopAPIKeys

	cfg := &todo_creation_human.WorkshopConfig{
		UserID:            currentUserID,
		SourcePlatform:    req.BotPlatform,
		WorkspacePath:     workspacePath,
		RunFolder:         runFolder,
		MCPConfigPath:     api.mcpConfigPath,
		SelectedServers:   append([]string(nil), selectedServers...), // copy to avoid mutation
		LLMConfig:         workshopLLMConfig,
		UseKnowledgebase:  true,
		LLMAllocationMode: "manual",
		Logger:            api.logger,
		SessionID:         sessionID,
		EnabledGroupNames: enabledGroupNames,
		InputPreset: func() string {
			if req.ExecutionOptions != nil {
				return req.ExecutionOptions.InputPreset
			}
			return ""
		}(),
		Inputs: func() map[string]string {
			if req.ExecutionOptions != nil {
				return req.ExecutionOptions.Inputs
			}
			return nil
		}(),
	}

	// Build base tools with session-aware workspace executors from the start.
	// This ensures MCP_API_URL in shell commands includes the session path prefix
	// (/s/{session_id}/...) so per-tool HTTP calls from inside Docker hit the
	// session-scoped route and get the correct executor.
	allTools, allExecutors, toolCategories := createCustomTools(true, currentUserID, sessionID, workspacePath)
	var messagingErr error
	allTools, messagingErr = api.appendWorkflowMessagingTools(allTools, allExecutors, toolCategories, currentUserID, sessionID, workspacePath, req)
	if messagingErr != nil {
		return nil, fmt.Errorf("workflow messaging tools: %w", messagingErr)
	}

	bindWorkflowStepIdentity(ctx, sessionID, allExecutors, toolCategories)
	allTools = restrictWorkflowNotificationTools(allTools, allExecutors, toolCategories, workflowNotificationsForPath(workspacePath))

	// Track preset's global secret selection (overrides req.SelectedGlobalSecrets which is nil for phase chat)
	var presetGlobalSecretNames *[]string

	// Load config from workflow.json manifest (single source of truth — no DB dependency).
	// Use context.Background() so a canceled request context doesn't silently skip manifest
	// loading. If the manifest cannot be read, fail immediately — a partially-configured
	// session with missing TieredConfig/servers/tools would cause cryptic failures later.
	if workspacePath != "" {
		manifest, found, mErr := ReadWorkflowManifest(context.Background(), workspacePath)
		if mErr != nil {
			return nil, fmt.Errorf("failed to read workflow manifest from %s: %w", workspacePath, mErr)
		} else if found {

			caps := manifest.Capabilities
			log.Printf("[WORKSHOP] Loaded config from manifest at %s", workspacePath)

			// Manifest is the source of truth for workflow-selected MCP servers.
			cfg.SelectedServers = caps.RuntimeServers()
			cfg.SelectedTools = caps.SelectedTools
			cfg.UseCodeExecutionMode = caps.UseCodeExecutionMode
			cfg.SelectedSkills = caps.SelectedSkills

			// Global secrets
			if caps.SelectedGlobalSecretNames != nil {
				presetGlobalSecretNames = caps.SelectedGlobalSecretNames
			}

			// Store configured browser intent only. Auto mode is resolved live by
			// agent_browser status/actions, never persisted as cdp or headless.
			configuredBrowserMode := strings.ToLower(strings.TrimSpace(caps.BrowserMode))
			if configuredBrowserMode == "cdp" && caps.BrowserCDPPort > 0 {
				port := caps.BrowserCDPPort
				req.CdpPort = &port
			}
			workshopConfiguredCDPPorts := configuredCDPPortsForMode(configuredBrowserMode, req.CdpPort, req.resolvedCDPPorts)
			cfg.BrowserRuntime = browser.NewBrowserRuntimeConfig(configuredBrowserMode, workshopConfiguredCDPPorts)
			if configuredBrowserMode != "" {
				common.SetSessionBrowserMode(sessionID, configuredBrowserMode)
			}
			if workspacePath != "" {
				browserCategory := virtualtools.GetWorkspaceBrowserToolCategory()
				browserTools := virtualtools.CreateWorkspaceBrowserTools()
				browserExecutors := workflowBrowserExecutors(sessionID, workspacePath, ReadWorkflowManifest)
				browserExecutors = api.bindWorkflowBrowserExecutors(ctx, sessionID, req, false, browserExecutors)
				allTools = append(allTools, browserTools...)
				for name, executor := range browserExecutors {
					allExecutors[name] = executor
				}
				for _, tool := range browserTools {
					if tool.Function != nil {
						toolCategories[tool.Function.Name] = browserCategory
					}
				}
				log.Printf("[WORKSHOP] Added dynamic browser tools (configured_mode=%s, candidate_cdp_ports=%v)", configuredBrowserMode, workshopConfiguredCDPPorts)
			}

			// LLM config from manifest
			log.Printf("[WORKSHOP] LLMConfig from manifest: isNil=%v", caps.LLMConfig == nil)
			if caps.LLMConfig != nil {
				llmCfg := lockedPresetLLMConfig(caps.LLMConfig)
				log.Printf("[WORKSHOP] LLMConfig details: mode=%q tieredConfig=%v providerProfile=%q",
					llmCfg.Mode, llmCfg.TieredConfig != nil, llmCfg.Provider)
				// Role models follow the run person's allowed models (PLAT-714).
				modelCtx := withModelLimitPerson(ctx, firstNonEmptyTrimmed(req.tokenOwner, currentUserID))
				cfg.PresetPhaseLLM, cfg.TieredConfig = workshopResolveLLMConfigFor(modelCtx, llmCfg)
				cfg.PresetBackgroundLLM = workshopResolveBackgroundLLMConfigFor(modelCtx, llmCfg)

				if llmCfg.UseKnowledgebase != nil {
					cfg.UseKnowledgebase = *llmCfg.UseKnowledgebase
				}
				// Tiered LLM allocation
				if cfg.TieredConfig != nil {
					cfg.LLMAllocationMode = "tiered"
					log.Printf("[WORKSHOP] Tiered mode: T1=%s T2=%s T3=%s",
						workshopFormatAgentLLM(cfg.TieredConfig.Tier1),
						workshopFormatAgentLLM(cfg.TieredConfig.Tier2),
						workshopFormatAgentLLM(cfg.TieredConfig.Tier3))
				}

				log.Printf("[WORKSHOP] LLM config loaded: phase=%v background=%v tiered=%v kb=%v",
					cfg.PresetPhaseLLM != nil, cfg.PresetBackgroundLLM != nil, cfg.TieredConfig != nil, cfg.UseKnowledgebase)
			}
		}
	}

	allSecrets, err := api.projectRunSecrets(ctx, currentUserID, workspacePath, workflowRunSecretSelection(req.SelectedGlobalSecrets, presetGlobalSecretNames))
	if err != nil {
		return nil, err
	}
	if len(allSecrets) > 0 {
		entries := make([]orchestrator.SecretEntry, len(allSecrets))
		for i, s := range allSecrets {
			entries[i] = orchestrator.SecretEntry{Name: s.Name, Value: s.Value}
		}
		cfg.Secrets = entries
		log.Printf("[WORKSHOP] Applied %d secrets", len(entries))
	}

	// Replace workspace executors with session-aware versions (same as normal workflow handler).
	// This sets MCP_SESSION_ID and secrets as shell env vars for code execution mode.
	secretEnvVars := make(map[string]string, len(allSecrets))
	for _, s := range allSecrets {
		secretEnvVars["SECRET_"+s.Name] = s.Value
	}
	sessionAwareExecutors, workspaceEnv := virtualtools.CreateWorkspaceAdvancedToolExecutorsWithSessionAndEnv(currentUserID, sessionID, secretEnvVars)
	virtualtools.SetGenerateTextWorkflowTierConfig(sessionAwareExecutors, getWorkspaceAPIURL(), generateTextWorkflowTiersFromResolved(cfg.TieredConfig), workshopAPIKeys)
	if mode := validatedWorkflowExecutionMode(req.ExecutionOptions); mode != "" {
		workspaceEnv["WORKFLOW_EXECUTION_MODE"] = mode
	}
	for name, executor := range sessionAwareExecutors {
		allExecutors[name] = executor
	}
	cfg.WorkspaceEnvRef = workspaceEnv
	// Working directory and folder guard are set per-request in handleQuery (line ~4415)
	// via workspace.SetSessionWorkingDir/SetSessionFolderGuard, not here.
	log.Printf("[WORKSHOP] Replaced workspace executors with session-aware versions (sessionID=%q, secrets=%d, MCP_API_URL=%s)", sessionID, len(secretEnvVars), workspaceEnv["MCP_API_URL"])

	cfg.CustomTools = allTools
	cfg.CustomToolExecutors = allExecutors
	cfg.ToolCategories = toolCategories

	// Create workshop event bridge for SSE emission from background goroutines
	cfg.EventBridge = &eventbridge.WorkflowEventBridge{
		BaseEventBridge: &eventbridge.BaseEventBridge{
			EventStore: api.eventStore,
			SessionID:  sessionID,
			Logger:     api.logger,
			BridgeName: "workshop",
		},
	}

	// Wire up live tool call query for query_step_tools
	cfg.ToolCallQueryFunc = formatToolCallSummaries(api)
	// Wire up live tmux session lookup so query_step can surface the coding-CLI terminal
	cfg.TmuxLookupFunc = makeStepTmuxLookup(api)

	// Wire up schedule management callbacks
	// Set workspace path for schedule management — prefer SelectedFolder, fall back to resolving from preset
	if req.SelectedFolder != "" {
		cfg.SchedulerWorkspacePath = req.SelectedFolder
	} else if req.PresetQueryID != "" {
		if wPath, wErr := api.resolveWorkspacePathFromPreset(context.Background(), req.PresetQueryID); wErr == nil && wPath != "" {
			cfg.SchedulerWorkspacePath = wPath
		}
	}
	cfg.SchedulerFuncs = api.buildSchedulerCallbacks()
	if invocation, ok := api.webhookInvocations.Load(sessionID); ok {
		cfg.WebhookInvocation = invocation.(*todo_creation_human.WebhookInvocation)
	}
	for ancestor, visited := sessionID, map[string]bool{}; ancestor != "" && !visited[ancestor]; {
		visited[ancestor] = true
		if invocation, ok := api.scheduleInvocations.Load(ancestor); ok {
			cfg.ScheduleInvocation = invocation.(*todo_creation_human.ScheduleInvocation)
			cfg.RunFolder = cfg.ScheduleInvocation.RunFolder
			break
		}
		if parent := virtualtools.GetParentChat(ancestor); parent != nil && parent.SessionID != ancestor {
			ancestor = parent.SessionID
			continue
		}
		active, ok := api.getActiveSession(ancestor)
		if !ok {
			break
		}
		ancestor = active.ParentSessionID
	}

	// Workflow execution resolves saved Crew configuration and scripts from the session.
	cfg.CrewResources = crewResourcesForRun(ctx, api.projectActions)
	cfg.AgentFunctionTools = api.workflowAgentFunctionToolsForRun(ctx)

	cfg.ScheduleCollisionCheck = api.scheduleCollisionCheck(cfg.WorkspacePath, sessionID, req.TriggeredBy)
	cfg.SkillFuncs = projectSkillCallbacks(api.buildSkillCallbacks(), strings.TrimSuffix(cfg.WorkspacePath, "/")+"/skills", "")
	cfg.LLMToolsFuncs = api.buildLLMToolsCallbacks()
	cfg.ListAvailableSecrets = func(ctx context.Context) ([]string, error) {
		nameSet := make(map[string]bool)
		// Global secrets from env vars
		for _, gs := range visibleGlobalSecrets(ctx, currentUserID) {
			nameSet[gs.Name] = true
		}
		names := make([]string, 0, len(nameSet))
		for name := range nameSet {
			names = append(names, name)
		}
		sort.Strings(names)
		return names, nil
	}
	cfg.ResolveSecretValues = func(ctx context.Context, names []string) map[string]string {
		if len(names) == 0 {
			return nil
		}
		// Keyed by the $SECRET_ name, resolved as a run resolves them: owner-lent Vault secrets.
		out := make(map[string]string, len(names))
		for _, s := range api.mergeGlobalSecretsFor(ctx, api.projectSecretsOwner(currentUserID, workspacePath), &names) {
			out[s.Name] = s.Value
		}
		return out
	}
	cfg.SecretsAttached = func(set map[string]string, removed []string) {
		for name, value := range set {
			virtualtools.SetSessionShellEnv(sessionID, "SECRET_"+name, value)
		}
		for _, name := range removed {
			virtualtools.DeleteSessionShellEnv(sessionID, "SECRET_"+name)
		}
		log.Printf("[SECRETS] Synced %d attached / %d removed SECRET_* into live shell clients for session %s", len(set), len(removed), sessionID)
	}

	return cfg, nil
}

func workshopConvertAgentLLMConfig(config *workflowtypes.AgentLLMConfig) *todo_creation_human.AgentLLMConfig {
	return workshopConvertAgentLLMConfigFor(context.Background(), config)
}

// workshopConvertAgentLLMConfigFor is workshopConvertAgentLLMConfig with the
// allowed models of the person ctx names (modelLimitPerson).
func workshopConvertAgentLLMConfigFor(ctx context.Context, config *workflowtypes.AgentLLMConfig) *todo_creation_human.AgentLLMConfig {
	if config == nil {
		return nil
	}
	// A saved role model its account does not allow (the admin restricted the
	// account after the workflow was set up) runs on the first allowed model.
	modelID, publishedID := config.ModelID, config.PublishedLLMID
	if constrained, changed, err := resolveAccountModel(ctx, config.Provider, config.ConnectionID, config.ModelID); err == nil && changed && strings.TrimSpace(config.ModelID) != "" {
		modelID, publishedID = constrained, ""
	}
	return &todo_creation_human.AgentLLMConfig{
		PublishedLLMID: publishedID,
		Provider:       config.Provider,
		ModelID:        modelID,
		Options:        config.Options,
		ConnectionID:   config.ConnectionID,
	}
}

func workshopConvertTieredLLMConfig(config *workflowtypes.TieredLLMConfig) *todo_creation_human.TieredLLMConfig {
	return workshopConvertTieredLLMConfigFor(context.Background(), config)
}

func workshopConvertTieredLLMConfigFor(ctx context.Context, config *workflowtypes.TieredLLMConfig) *todo_creation_human.TieredLLMConfig {
	if config == nil {
		return nil
	}

	tiered := &todo_creation_human.TieredLLMConfig{
		Tier1: workshopConvertAgentLLMConfigFor(ctx, config.Tier1),
		Tier2: workshopConvertAgentLLMConfigFor(ctx, config.Tier2),
		Tier3: workshopConvertAgentLLMConfigFor(ctx, config.Tier3),
	}

	if tiered.Tier1 == nil || tiered.Tier2 == nil || tiered.Tier3 == nil {
		log.Printf("[WORKSHOP] Partial tiered LLM config detected: T1=%t T2=%t T3=%t",
			tiered.Tier1 != nil, tiered.Tier2 != nil, tiered.Tier3 != nil)
	}

	return tiered
}

// generateTextWorkflowTiers converts the current workflow manifest's model
// roles into the compact config consumed by generate_text_llm. Provider-profile
// workflows (including Cursor managing the full workflow) are expanded first.
func generateTextWorkflowTiers(config *workflowtypes.PresetLLMConfig) *virtualtools.WorkflowLLMTierConfig {
	if config == nil {
		return nil
	}
	_, tiers := workshopResolveLLMConfig(lockedPresetLLMConfig(config))
	return generateTextWorkflowTiersFromResolved(tiers)
}

func generateTextWorkflowTiersFromResolved(tiers *todo_creation_human.TieredLLMConfig) *virtualtools.WorkflowLLMTierConfig {
	if tiers == nil {
		return nil
	}
	toTierModel := func(model *todo_creation_human.AgentLLMConfig) *virtualtools.TierModel {
		if model == nil {
			return nil
		}
		return &virtualtools.TierModel{
			Provider:     model.Provider,
			ModelID:      model.ModelID,
			Options:      model.Options,
			ConnectionID: model.ConnectionID,
		}
	}
	return &virtualtools.WorkflowLLMTierConfig{
		High:   toTierModel(tiers.Tier1),
		Medium: toTierModel(tiers.Tier2),
		Low:    toTierModel(tiers.Tier3),
	}
}

func workshopResolveLLMConfig(config *workflowtypes.PresetLLMConfig) (*todo_creation_human.AgentLLMConfig, *todo_creation_human.TieredLLMConfig) {
	return workshopResolveLLMConfigFor(context.Background(), config)
}

// workshopResolveLLMConfigFor resolves the manifest's role models under the
// allowed models of the person ctx names (modelLimitPerson); without one, the
// shared accounts' own lists apply.
func workshopResolveLLMConfigFor(ctx context.Context, config *workflowtypes.PresetLLMConfig) (*todo_creation_human.AgentLLMConfig, *todo_creation_human.TieredLLMConfig) {
	if config == nil {
		return nil, nil
	}
	if builder, tiered, ok := workflowtypes.ResolveProviderProfileConfig(config); ok {
		return workshopConvertAgentLLMConfigFor(ctx, builder), workshopConvertTieredLLMConfigFor(ctx, tiered)
	}

	builder := workshopConvertAgentLLMConfigFor(ctx, config.BuilderLLM)
	var tiered *todo_creation_human.TieredLLMConfig
	if config.Mode == workflowtypes.LLMConfigModeExplicit && config.TieredConfig != nil {
		tiered = workshopConvertTieredLLMConfigFor(ctx, config.TieredConfig)
	}
	return builder, tiered
}

func workshopResolveBackgroundLLMConfig(config *workflowtypes.PresetLLMConfig) *todo_creation_human.AgentLLMConfig {
	return workshopResolveBackgroundLLMConfigFor(context.Background(), config)
}

func workshopResolveBackgroundLLMConfigFor(ctx context.Context, config *workflowtypes.PresetLLMConfig) *todo_creation_human.AgentLLMConfig {
	if config == nil {
		return nil
	}
	if resolved, ok := workflowtypes.ResolveProviderProfileBackgroundConfig(config); ok {
		return workshopConvertAgentLLMConfigFor(ctx, resolved)
	}
	if config.BackgroundLLM != nil && config.BackgroundLLM.Provider != "" && config.BackgroundLLM.ModelID != "" {
		return workshopConvertAgentLLMConfigFor(ctx, config.BackgroundLLM)
	}
	return nil
}

func workshopFormatAgentLLM(config *todo_creation_human.AgentLLMConfig) string {
	if config == nil {
		return "<nil>"
	}
	if config.Provider == "" && config.ModelID == "" {
		return "<empty>"
	}
	return fmt.Sprintf("%s/%s", config.Provider, config.ModelID)
}
