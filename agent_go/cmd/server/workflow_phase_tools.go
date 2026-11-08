package server

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// installWorkflowPhaseTools registers the phase-specific tool set on an agent
// (plan modification tools, workshop chat tools, run_full_workflow,
// guidance/reference doc tools, etc.).
//
// Extracted from the /api/query path in server.go (was the 358-line inline block
// inside the workflow-phase setup block), which is its only caller.
//
// It registers one complete tool surface and applies no workshop-mode narrowing.
// It used to take setToolPolicy/applyAllowList so /api/query could narrow the
// set per turn while the restore path deliberately did not — two paths that
// could disagree about what the CLI had been told exists, which is precisely
// how a registered tool became undiscoverable. Mode is a focus rule now and
// lives in the Builder prompt. See docs/design/agent_tool_surface_single_source.md.
//
// Returns an error instead of fatal-exiting on RegisterPlanModificationTools
// failure. /api/query wraps the error with log.Fatalf to preserve the pre-refactor
// semantics; the restore path logs and skips so a partial-registration failure
// doesn't crash the server during a routine auto-restore.
func (api *StreamingAPI) installWorkflowPhaseTools(
	ctx context.Context,
	definitionAgent definitionRegistrar,
	sessionID, userID, workflowPhaseID, phaseWorkspacePath, phaseRunFolder string,
	phaseTemplateVars map[string]string,
	selectedServers []string,
	mergedAPIKeys *llm.ProviderAPIKeys,
	phaseReadFile func(ctx context.Context, p string) (string, error),
	phaseWriteFile func(ctx context.Context, p, content string) error,
	phaseMoveFile func(ctx context.Context, src, dst string) error,
	syntheticReq QueryRequest,
	readOnly bool,
) error {
	if phaseWorkspacePath != "" {
		definitionAgent = workflowTeamRegistrar{definitionRegistrar: definitionAgent, root: phaseWorkspacePath, user: userID}
	}
	if err := api.registerWorkflowTeamTools(definitionAgent, phaseWorkspacePath, userID, readOnly); err != nil {
		return err
	}
	if check := api.scheduleCollisionCheck(phaseWorkspacePath, sessionID, syntheticReq.TriggeredBy); check != nil {
		definitionAgent = scheduleGuardRegistrar{definitionAgent, todo_creation_human.GuardScheduleTools(definitionAgent, check)}
	}
	active, _ := api.getActiveSession(sessionID)
	policy := resolveWorkflowChatPolicy(sessionID, syntheticReq, active, readOnly)
	definitionAgent = workflowContractExecutionGuardRegistrar{
		definitionRegistrar: definitionAgent,
		workspacePath:       phaseWorkspacePath,
		scheduled:           isScheduledSessionIdentity(sessionID, syntheticReq.TriggeredBy),
	}
	if err := api.registerWebhookTools(definitionAgent, userID, phaseWorkspacePath, policy); err != nil {
		return err
	}
	// Builder chat can call Crews and other workflows (workflow→crew,
	// workflow→workflow) on every turn of the person's own Builder chat (owner, 5 Nov), not a Slack bot turn.
	if policy.Mode == "builder" && policy.Origin != "bot" && policy.allows("plan_authoring") {
		if err := api.registerProjectTools(definitionAgent, nil, userID, GetUserFromContext(internalBotRequestContext(ctx, userID))); err != nil {
			return err
		}
		panelProduct := "agentworks"
		if phaseTemplateVars["WorkflowKind"] == "relay" {
			panelProduct = "relays"
		}
		if err := api.registerWorkspacePanelChatTools(definitionAgent, nil, userID, sessionID, phaseWorkspacePath, panelProduct); err != nil {
			return err
		}
		if err := api.registerGoalOwnerTabTools(definitionAgent, userID, phaseWorkspacePath); err != nil {
			return err
		}
		if err := api.registerCrewFunctionTools(definitionAgent, userID, sessionID, syntheticReq, workflowTriggerLinkCaller(phaseWorkspacePath), nil); err != nil {
			return err
		}
	} else {
		if err := api.registerAgentMessagingTools(definitionAgent, userID, sessionID, syntheticReq, workflowTriggerLinkCaller(phaseWorkspacePath), nil); err != nil {
			return err
		}
	}
	if policy.Mode == "run" && workflowPhaseID == workflowtypes.WorkflowStatusWorkflowBuilder && phaseTemplateVars["WorkflowKind"] != "relay" {
		if err := api.registerWorkflowPublishedFunctionTools(ctx, definitionAgent, userID, sessionID, phaseWorkspacePath); err != nil {
			return err
		}
	}
	if err := api.registerUserAccessTools(definitionAgent, userID, phaseWorkspacePath, policy); err != nil {
		return err
	}
	if policy.allows("plan_authoring") {
		if err := api.registerCustomCommandTools(definitionAgent, userID, phaseWorkspacePath); err != nil {
			return err
		}
	}
	if err := api.registerShareLinkTools(definitionAgent, userID, phaseWorkspacePath); err != nil {
		return err
	}
	// Internet sharing is admin-gated server control, not a read-only run
	// operation — same capability gate as user management.
	if policy.allows("user_management") {
		if err := api.registerShareTunnelTools(definitionAgent, userID); err != nil {
			return err
		}
	}
	log.Printf("[CHAT_POLICY] session=%s mode=%s origin=%s capabilities=%v", sessionID, policy.Mode, policy.Origin, policy.Capabilities)
	mcpManagement := (policy.allows("mcp_management") || policy.allows("mcp_inspection")) && workflowPhaseID == workflowtypes.WorkflowStatusWorkflowBuilder && syntheticReq.AgentProfileID == ""
	if mcpManagement {
		if err := api.registerMCPToolsForChat(definitionAgent, policy, nil); err != nil {
			return err
		}
		if policy.allows("mcp_management") {
			if err := api.registerVaultAccessChatTool(definitionAgent, userID); err != nil {
				return err
			}
			if err := api.registerMyVaultsTool(definitionAgent, userID); err != nil {
				return err
			}
		}
	}

	// Register phase-appropriate tools
	// PHASE_TOOL_RACE_DIAGNOSTIC: these are the registrations that
	// the auto-restore path in chat_history_routes.go has NOT seen
	// yet — see [PHASE_TOOL_RACE] AUTO_RESTORE_LAUNCH log. If
	// AUTO_RESTORE_LAUNCH fired before PHASE_TOOL_REGISTER_START
	// for the same session, the CLI's tool catalog at launch is
	// missing run_full_workflow / execute_step / etc.
	log.Printf("[PHASE_TOOL_RACE] PHASE_TOOL_REGISTER_START for session=%s phase=%s",
		sessionID, workflowPhaseID)
	phaseRegisterStart := time.Now()
	// Only an interactive Builder has a UI to control. Schedules share this
	// phase, so decide by origin/ownership, not WorkshopMode or write access.
	if err := api.registerWorkflowUIForCaller(definitionAgent, workflowPhaseID, sessionID, phaseWorkspacePath, syntheticReq, readOnly); err != nil {
		return fmt.Errorf("register workspace UI tools: %w", err)
	}
	switch workflowPhaseID {
	case workflowtypes.WorkflowStatusWorkflowBuilder:
		if phaseTemplateVars["WorkflowKind"] == "relay" && phaseTemplateVars["RelayRunMode"] == "true" {
			if err := api.registerRelayRunChatTools(definitionAgent, userID, phaseWorkspacePath); err != nil {
				return fmt.Errorf("register Flow Run tools: %w", err)
			}
		}
		if phaseTemplateVars["WorkflowKind"] == "relay" && policy.Origin != "bot" && policy.allows("plan_authoring") {
			if err := api.registerRelayReleaseTools(definitionAgent, phaseWorkspacePath, userID); err != nil {
				return fmt.Errorf("register Flow release tools: %w", err)
			}
			if err := api.registerPythonRelayBuilderTools(definitionAgent, phaseWorkspacePath, userID); err != nil {
				return fmt.Errorf("register Flow Python tools: %w", err)
			}
		}
		// Keep the definition stable across persistent CLI turns. The executor
		// reads current workflow intent on every call, including disabled mode.
		if phaseWorkspacePath != "" {
			execs := workflowBrowserExecutors(sessionID, phaseWorkspacePath, ReadWorkflowManifest)
			if err := registerCodingToolGroup(definitionAgent.RegisterCustomTool, virtualtools.CreateWorkspaceBrowserTools(), execs, func(string) string { return virtualtools.GetWorkspaceBrowserToolCategory() }, nil); err != nil {
				return err
			}
		}
		// Older plan-based workflows are no longer edited (PLAT-746, owner 8 Nov): no plan
		// modification tools are registered; changing one means converting it to a script.
		// STOP-RACE GUARD: Check if the session was stopped while this goroutine
		// was in flight. Without this check, the goroutine would create a new
		// WorkshopChatSession with a fresh context.Background() that is never
		// canceled, leaving orphaned CLI processes running indefinitely.
		// This was the root cause of the 2026-04-04 "can't stop" bug.
		if api.isSessionMarkedStopped(sessionID) {
			log.Printf("[WORKFLOW_PHASE] Session %s was stopped — aborting workshop creation to prevent orphaned processes", sessionID)
			return nil
		}

		// Get or create per-session workshop controller + step registry
		workshopSessionKey := sessionID
		var workshopSession *todo_creation_human.WorkshopChatSession
		if cached, ok := api.workshopChatSessions.Load(workshopSessionKey); ok {
			workshopSession = cached.(*todo_creation_human.WorkshopChatSession)
			log.Printf("[WORKFLOW_PHASE] Reusing existing workshop session for %s", sessionID)

			// Always refresh API keys on session reuse (workspace keys may have changed)
			// Use mergedAPIKeys loaded before goroutine (r.Context() is canceled inside goroutine)
			workshopSession.UpdateAPIKeys(mergedAPIKeys)

			preset := ""
			var inputs map[string]string
			if syntheticReq.ExecutionOptions != nil {
				preset, inputs = syntheticReq.ExecutionOptions.InputPreset, syntheticReq.ExecutionOptions.Inputs
			}
			if err := workshopSession.UpdateInvocationInputs(ctx, preset, inputs); err != nil {
				return err
			}

			// WorkshopMode is set unconditionally below on workshopSession, from
			// phaseTemplateVars["WorkshopMode"] — the single source of truth for
			// both this reused-session path and the freshly-created-session path.

			// Refresh all settings from manifest in case user edited the workflow
			if phaseWorkspacePath != "" {
				refreshManifest, refreshFound, refreshErr := ReadWorkflowManifest(context.Background(), phaseWorkspacePath)
				if refreshErr != nil {
					log.Printf("[WORKFLOW_PHASE] Warning: Failed to reload manifest: %v", refreshErr)
				} else if refreshFound {
					if refreshManifest.Kind == "relay" {
						_ = workshopSession.UpdateInvocationInputs(ctx, "", nil)
					}
					caps := refreshManifest.Capabilities
					selectedServers = caps.RuntimeServers()
					configuredBrowserMode := strings.ToLower(strings.TrimSpace(caps.BrowserMode))
					configuredBrowserPorts := configuredCDPPortsForMode(configuredBrowserMode, nil, nil)
					workshopSession.UpdateBrowserRuntime(configuredBrowserMode, configuredBrowserPorts)
					common.SetSessionBrowserMode(sessionID, configuredBrowserMode)
					log.Printf("[WORKFLOW_PHASE] Refreshed dynamic browser config: configured_mode=%s candidate_cdp_ports=%v", configuredBrowserMode, configuredBrowserPorts)

					refreshedTools := caps.SelectedTools
					toolsParsed := true
					refreshedSkills := caps.SelectedSkills
					skillsParsed := true

					// Refresh secrets
					effectiveSecretSelection := syntheticReq.SelectedGlobalSecrets
					if caps.SelectedGlobalSecretNames != nil {
						effectiveSecretSelection = caps.SelectedGlobalSecretNames
					}
					allRefreshedSecrets := api.mergeGlobalSecretsFor(context.Background(), api.projectSecretsOwner(userID, phaseWorkspacePath), effectiveSecretSelection)
					var secretEntries []orchestrator.SecretEntry
					for _, s := range allRefreshedSecrets {
						secretEntries = append(secretEntries, orchestrator.SecretEntry{Name: s.Name, Value: s.Value})
					}

					// LLM config
					refreshedKnowledgebase := true
					log.Printf("[WORKFLOW_PHASE] Refresh LLMConfig: isNil=%v", caps.LLMConfig == nil)
					if caps.LLMConfig != nil {
						log.Printf("[WORKFLOW_PHASE] Refresh LLMConfig details: mode=%q tieredConfig=%v",
							caps.LLMConfig.Mode, caps.LLMConfig.TieredConfig != nil)
						lockedCfg := lockedPresetLLMConfig(caps.LLMConfig)
						modelCtx := withModelLimitPerson(context.Background(), userID) // the person's allowed models (PLAT-714)
						phaseLLM, refreshedTiered := workshopResolveLLMConfigFor(modelCtx, lockedCfg)
						pulseLLM := workshopResolveBackgroundLLMConfigFor(modelCtx, lockedCfg)
						workshopSession.UpdatePresetLLMConfigs(phaseLLM, pulseLLM)

						if refreshedTiered != nil {
							workshopSession.UpdateTieredConfig(refreshedTiered)
							log.Printf("[WORKFLOW_PHASE] Refreshed tiered config from manifest")
						} else {
							workshopSession.UpdateTieredConfig(nil)
						}

						if caps.LLMConfig.UseKnowledgebase != nil {
							refreshedKnowledgebase = *caps.LLMConfig.UseKnowledgebase
						}
					}

					workshopSession.UpdatePresetSettings(
						selectedServers,
						refreshedTools, toolsParsed,
						caps.UseCodeExecutionMode,
						refreshedKnowledgebase,
						refreshedSkills, skillsParsed,
						secretEntries,
					)
					log.Printf("[WORKFLOW_PHASE] Refreshed settings from manifest: servers=%d tools=%d codeExec=%v kb=%v skills=%d secrets=%d",
						len(selectedServers), len(refreshedTools), caps.UseCodeExecutionMode,
						refreshedKnowledgebase, len(refreshedSkills), len(secretEntries))
				}
			}
		} else {
			// Build full workshop config matching normal workflow setup
			workshopCfg, cfgErr := api.buildWorkshopConfig(ctx, syntheticReq, userID, phaseWorkspacePath, phaseRunFolder, selectedServers, sessionID, mergedAPIKeys)
			if cfgErr != nil {
				log.Printf("[WORKFLOW_PHASE] Error: Failed to build workshop config for %s: %v — workshop execution tools unavailable", workflowPhaseID, cfgErr)
			} else {

				newSession, sessionErr := todo_creation_human.NewWorkshopChatSession(ctx, workshopCfg)
				if sessionErr != nil {
					log.Printf("[WORKFLOW_PHASE] Warning: Failed to create workshop session for %s: %v — workshop execution tools unavailable", workflowPhaseID, sessionErr)
				} else {
					workshopSession = newSession
					api.workshopChatSessions.Store(workshopSessionKey, workshopSession)
					log.Printf("[WORKFLOW_PHASE] Created new %s session for %s", workflowPhaseID, sessionID)
				}
			}
		}

		if workshopSession != nil {
			// PLAT-262: WorkshopMode is the single gate for tools/prompt/skills
			// (see interactive_workshop_manager.go). phaseTemplateVars["WorkshopMode"]
			// is already the final, authoritative value by this point — forced to
			// "run" for a read-only identity in server.go before this function was
			// even called, otherwise whatever the client/default resolved to. Set
			// it unconditionally here so both the reused-session and
			// freshly-created-session paths above always end up correct, with no
			// separate reuse-branch-only special case to keep in sync.
			workshopSession.SetWorkshopModeOverride(phaseTemplateVars["WorkshopMode"])
			workshopSession.SetExtraSubAgentNotifier(&workflowSubAgentTrackingNotifier{
				api:       api,
				sessionID: sessionID,
			})
			// The sub-agent tracker registers starts for status only and
			// notifies on completion. That gives the builder progress
			// updates for long-running orchestrator sub-agents without
			// synthetic turns at sub-agent start.
			//
			// Wire workshop execution notifier so execute_step and run_in_background
			// register in bgAgentRegistry (keeps frontend polling alive while background executions run).
			workshopSession.SetWorkshopExecutionNotifier(&workshopExecutionBgNotifier{
				api:           api,
				sessionID:     sessionID,
				workspacePath: phaseWorkspacePath,
				presetQueryID: syntheticReq.PresetQueryID,
				userID:        userID,
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
					for _, a := range agents {
						result = append(result, todo_creation_human.ServerAgentInfo{
							ID: a.ID, Name: a.Name, Status: string(a.GetStatus()),
						})
					}
					return result
				},
			)
			todo_creation_human.RegisterWorkshopChatTools(definitionAgent, workshopSession, api.logger)
			log.Printf("[WORKFLOW_PHASE] Registered workshop execution tools for %s (execute_step, query_step, stop_step, list_steps, etc.)", workflowPhaseID)

			builderSession := workshopSession
			afterUpsert := func(ctx context.Context, name, vaultName, value string) error {
				// The server saves the selection (the Vault name); the Workshop only refreshes its live copy.
				if err := selectProjectSecret(ctx, crewProfileID, phaseWorkspacePath, vaultName); err != nil {
					return err
				}
				// The Workshop side (steps) reads a live map; the chat agent's own
				// shell client took a snapshot at turn start, so push the value
				// there too or a shell command later in this same turn misses it.
				if n := virtualtools.SetSessionShellEnv(sessionID, "SECRET_"+name, value); n > 0 {
					log.Printf("[SECRETS] Pushed SECRET_%s into %d live shell client(s) for session %s", name, n, sessionID)
				}
				if builderSession == nil {
					return nil
				}
				return builderSession.AttachSecretToWorkflow(ctx, name, value)
			}
			afterDelete := func(ctx context.Context, name string) error {
				if err := unselectProjectSecret(ctx, crewProfileID, phaseWorkspacePath, name); err != nil {
					return err
				}
				name = vaultSecretShortName(name)
				virtualtools.DeleteSessionShellEnv(sessionID, "SECRET_"+name)
				if builderSession == nil {
					return nil
				}
				return builderSession.DetachSecretFromWorkflow(ctx, name)
			}
			if err := api.registerSecretManagementTools(definitionAgent, userID, phaseWorkspacePath, "secret_tools", !policy.allows("secret_management"), afterUpsert, afterDelete); err != nil {
				log.Printf("[WORKFLOW_PHASE] Warning: Failed to register secret tools in %s: %v", workflowPhaseID, err)
			} else {
				log.Printf("[WORKFLOW_PHASE] Registered secret tools in %s (list_secrets, set_workflow_secret, delete_workflow_secret) with workflow auto-detach", workflowPhaseID)
			}
		}

		// Only "workshop" or "run" can reach phaseTemplateVars: server.go
		// normalizes every legacy value before this point. Comparing against
		// the retired names asserted they still occur, which cost real
		// debugging time while diagnosing PLAT-125.
		if policy.allows("report_authoring") {
			if err := api.registerDashboardTools(definitionAgent, userID, phaseWorkspacePath, GetUserFromContext(internalBotRequestContext(ctx, userID))); err != nil {
				return err
			}
			// The HTML report is loaded directly from db/reports/index.html. The
			// builder edits those files with normal workspace tools and validates
			// each page; there is no report-plan JSON registry or widget layer.
			// validate_report_html also checks what the static parse cannot:
			// every literal window.report.query SQL is prepared against the
			// live db/db.sqlite, and every referenced db/ path is confirmed
			// to exist. Both go through the same workspace API the Report tab
			// uses, so a remote workspace validates exactly what it renders.
			reportWSClient := workspace.NewClient(getWorkspaceAPIURL(), workspace.WithUserID(userID))
			reportDBPath := filepath.ToSlash(filepath.Join(phaseWorkspacePath, "db", "db.sqlite"))
			reportHooks := todo_creation_human.ReportHTMLValidationHooks{
				ExplainSQL: func(ctx context.Context, sqlText string) error {
					_, err := reportWSClient.QueryAuthorizedWorkflowDB(ctx, workspace.QueryWorkflowDBParams{
						DBPath:      reportDBPath,
						SQL:         "EXPLAIN " + sqlText,
						PrepareOnly: true,
					})
					return err
				},
				FileExists: func(ctx context.Context, relativePath string) (bool, error) {
					full := filepath.ToSlash(filepath.Join(phaseWorkspacePath, relativePath))
					listing, exists, err := listWorkspaceFolder(ctx, filepath.ToSlash(filepath.Dir(full)), 1)
					if err != nil || !exists {
						return false, err
					}
					paths := []string{}
					collectWorkspaceFilePaths(listing, &paths)
					for _, candidate := range paths {
						if strings.Trim(candidate, "/") == strings.Trim(full, "/") {
							return true, nil
						}
					}
					return false, nil
				},
			}
			if err := todo_creation_human.RegisterHTMLReportTools(
				definitionAgent,
				phaseWorkspacePath,
				api.logger,
				phaseReadFile,
				reportHooks,
			); err != nil {
				log.Printf("[WORKFLOW_PHASE] Warning: Failed to register HTML report tools in %s: %v", workflowPhaseID, err)
			} else {
				log.Printf("[WORKFLOW_PHASE] Registered HTML report tools in %s", workflowPhaseID)
			}
			// preview_report: the true-render check (headless browser through the
			// Report tab's own host runtime), alongside the static validator.
			if err := api.registerReportPreviewTool(definitionAgent, sessionID, userID, phaseWorkspacePath); err != nil {
				log.Printf("[WORKFLOW_PHASE] Warning: Failed to register preview_report in %s: %v", workflowPhaseID, err)
			}
		} else {
			log.Printf("[WORKFLOW_PHASE] Skipped HTML report tools in %s mode for %s", phaseTemplateVars["WorkshopMode"], workflowPhaseID)
		}

		if workshopSession != nil {
			todo_creation_human.RegisterRunFullWorkflowTool(definitionAgent, workshopSession, api.logger)
			log.Printf("[WORKFLOW_PHASE] Registered run_full_workflow in %s", workflowPhaseID)
			// Guided-flow text for every workflow slash command, returned via
			// get_workflow_command_guidance(kind=...). Available across modes;
			// per-kind mode validation lives in the tool itself.
			guidance.RegisterGuidanceTool(definitionAgent, phaseTemplateVars["WorkshopMode"], api.logger)
			log.Printf("[WORKFLOW_PHASE] Registered get_workflow_command_guidance in %s (mode=%s)", workflowPhaseID, phaseTemplateVars["WorkshopMode"])

		}

		if phaseTemplateVars["WorkflowKind"] == "relay" {
			skillMode := "builder"
			if phaseTemplateVars["RelayRunMode"] == "true" {
				skillMode = "run"
			}
			skills, err := relayproduct.ChatSkills(skillMode)
			if err != nil {
				return fmt.Errorf("load Flow Builder skills: %w", err)
			}
			for _, skill := range skills {
				if err := definitionAgent.AttachSkill(skill); err != nil {
					return fmt.Errorf("attach Flow Builder skill %s: %w", skill.Name, err)
				}
			}
		} else {
			workshopMode := phaseTemplateVars["WorkshopMode"]
			if err := guidance.AttachConfiguredReferenceSurface(workshopMode, mcpManagement, agentworksproduct.ChatSkills(policy.Mode), func(skill *llmtypes.Skill) error {
				return definitionAgent.AttachSkill(skill)
			}); err != nil {
				return fmt.Errorf("attach reference surface in %s (mode=%s): %w", workflowPhaseID, workshopMode, err)
			}
		}
	}

	log.Printf("[PHASE_TOOL_RACE] PHASE_TOOL_REGISTER_END for session=%s phase=%s elapsed=%s",
		sessionID, workflowPhaseID, time.Since(phaseRegisterStart))

	// No per-turn tool narrowing happens here. Workshop mode is a focus rule,
	// and it is stated in the Builder system prompt rather than enforced by
	// hiding tools.
	//
	// The old allow list filtered the catalog the coding CLI caches at launch,
	// so an omitted-but-registered tool was undiscoverable rather than
	// rejected: the agent never learned it existed and shelled out instead.
	// That silently cost set_workflow_secret and list_llm_capabilities. It also
	// wrote through to the session-wide code-execution registry, which reaches
	// execution agents and sub-agents that workshop mode was never meant to
	// govern. See docs/design/agent_tool_surface_single_source.md.
	//
	// Authority (Reader/Writer, irreversible actions) is still enforced in code
	// — at the executor, and at construction for tool agents.

	return nil
}
