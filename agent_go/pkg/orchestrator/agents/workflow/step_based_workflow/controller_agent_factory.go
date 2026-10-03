package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	orchestrator_events "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowkb"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/agent/codeexec"
	mcpllm "github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/observability"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// ============================================================================
// Phase 1: Helper Methods (Extracted for Reusability)
// ============================================================================

// normalizeServerNames ensures an empty server list is replaced with the NoServers
// sentinel so the connection layer doesn't interpret [] as "connect to all servers".
func normalizeServerNames(servers []string) []string {
	if len(servers) == 0 {
		return []string{mcpclient.NoServers}
	}
	return servers
}

// forceWorkflowClaudeCodeInteractiveTransport sets the tmux baseline for Claude
// Code workflow agents. Claude Code no longer supports the old print/stream-json
// transport, so workflow execution must stay on tmux even when old step configs
// still contain transport="structured".
func forceWorkflowClaudeCodeInteractiveTransport(config *agents.OrchestratorAgentConfig) {
	if workflowAgentConfigUsesClaudeCode(config) {
		config.ClaudeCodeTransport = mcpllm.ClaudeCodeTransportTmux
	}
}

// applyWorkflowTransportToAgentConfig sets the coding-agent CLI transport for a
// workflow EXECUTION agent (steps, message_sequence/execution-only, KB agents,
// todo-task orchestrator). It does NOT apply to the interactive
// workflow-builder agent, which stays on tmux because a human chats with it —
// see forceWorkflowClaudeCodeInteractiveTransport.
//
// Coding-agent CLIs in a workflow ALWAYS use structured JSON. There is no
// per-step choice: a workflow step is unattended, one-shot work, so the
// properties that matter are the ones structured JSON gives directly rather
// than by interpreting a terminal pane — explicit completion events (when the
// turn is done / when to send the next message), reliable token-usage and
// tool-call reporting, and a clean final response with no terminal wrapping or
// ANSI. Every workflow-execution bug class this project has hit
// (premature/missed completion, prompt text leaking into the reply, garbled
// reassembly) comes from scraping a pane. tmux earns its place only where a
// human can steer mid-turn — i.e. the builder/chat agents, not here.
//
// This reverses an earlier "always tmux for workflows" decision, which rested
// on a belief written into this file that "Claude Code no longer supports the
// old print/stream-json transport". That is stale: Claude Code structured is
// live-verified and P0-certified in mcpagent
// (TestStructuredTransportMultiTurn/Claude, multi-turn over native --resume).
func (hcpo *StepBasedWorkflowOrchestrator) applyWorkflowTransportToAgentConfig(config *agents.OrchestratorAgentConfig, stepConfig *AgentConfigs, agentKind string) string {
	if config == nil {
		return ""
	}
	provider := config.LLMConfig.Primary.Provider
	if !common.IsCLIProvider(provider) {
		// Non-CLI (API) providers have no process transport at all.
		config.ForceStructuredCodingAgent = false
		return ""
	}
	config.ForceStructuredCodingAgent = true
	hcpo.GetLogger().Info(fmt.Sprintf("🔧 %s transport: structured JSON for CLI provider '%s'", agentKind, provider))
	return "structured"
}

// publishWorkflowTransportContext tags the live event stream with the
// transport and, when the caller knows the plan step, its execution mode
// (PLAT-287: derived from the step type — "scripted" for regular,
// "agentic" for message sequences; "" when no
// step is in scope, e.g. KB maintenance agents).
func (hcpo *StepBasedWorkflowOrchestrator) publishWorkflowTransportContext(effectiveTransport string, executionMode string) {
	cab, ok := hcpo.GetContextAwareBridge().(*orchestrator.ContextAwareEventBridge)
	if !ok {
		return
	}
	rich := orchestrator.RichStepContext{
		Transport:     strings.TrimSpace(effectiveTransport),
		ExecutionMode: strings.TrimSpace(executionMode),
	}
	if rich.ExecutionMode != "" || rich.Transport != "" {
		cab.MergeRichStepContext(rich)
	}
}

func workflowAgentConfigUsesClaudeCode(config *agents.OrchestratorAgentConfig) bool {
	if config == nil {
		return false
	}
	if config.LLMConfig.Primary.Provider == string(mcpllm.ProviderClaudeCode) {
		return true
	}

	return false
}

// filterServersByWorkflow intersects stepServers with workflowServers so that the
// workflow-level server list acts as a hard cap. If the workflow has no servers
// (user removed all MCPs), no step can bypass that restriction.
// Returns mcpclient.NoServers sentinel when the result is empty, because an empty
// []string is treated as "all servers" by the connection layer.
func filterServersByWorkflow(stepServers, workflowServers []string) []string {
	if len(workflowServers) == 0 {
		return []string{mcpclient.NoServers}
	}
	workflowSet := make(map[string]bool, len(workflowServers))
	for _, s := range workflowServers {
		workflowSet[s] = true
	}
	result := make([]string, 0, len(stepServers))
	for _, s := range stepServers {
		if workflowSet[s] {
			result = append(result, s)
		}
	}
	if len(result) == 0 {
		return []string{mcpclient.NoServers}
	}
	return result
}

// filterToolsByWorkflow filters stepTools keeping only those whose server prefix
// is allowed by workflowServers. Workflow is the hard cap — if a server was
// removed from the workflow no step can re-enable it via its own tool list.
func filterToolsByWorkflow(stepTools, workflowServers []string) []string {
	if len(workflowServers) == 0 {
		return []string{}
	}
	workflowSet := make(map[string]bool, len(workflowServers))
	for _, s := range workflowServers {
		workflowSet[s] = true
	}
	result := make([]string, 0, len(stepTools))
	for _, t := range stepTools {
		serverName := t
		if idx := strings.Index(t, ":"); idx >= 0 {
			serverName = t[:idx]
		}
		if workflowSet[serverName] {
			result = append(result, t)
		}
	}
	return result
}

func buildSessionScopedMCPAPIURL(sessionID string) string {
	baseURL := strings.TrimSpace(os.Getenv("MCP_API_URL"))
	sessionID = strings.TrimSpace(sessionID)
	if baseURL == "" || sessionID == "" {
		return ""
	}
	if idx := strings.Index(baseURL, "/s/"); idx >= 0 {
		baseURL = baseURL[:idx]
	}
	return strings.TrimRight(baseURL, "/") + "/s/" + sessionID
}

// stepRuntimeEnv returns the workflow runtime environment that is safe to copy
// into a step-scoped shell. Session/path/MCP values are deliberately excluded:
// they are execution-specific and are set below after the shared environment is
// merged. This keeps resolved VAR_* values and SECRET_* values available to
// coding-agent bridge calls without leaking stale parent-session routing.
func stepRuntimeEnv(workspaceEnv map[string]string) map[string]string {
	if len(workspaceEnv) == 0 {
		return nil
	}
	result := make(map[string]string, len(workspaceEnv))
	for key, value := range workspaceEnv {
		switch key {
		case "STEP_OUTPUT_DIR", "STEP_EXECUTION_DIR", "DB_PATH", "RUN_FOLDER",
			"MCP_SESSION_ID", "MCP_API_URL", "MCP_CUSTOM", "MCP_AUTH", "MCP_VIRTUAL":
			continue
		}
		result[key] = value
	}
	return result
}

func injectStepEnvIntoShellExecutor(executors map[string]interface{}, stepOutputAbsPath, stepExecutionAbsPath, dbAbsPath, runFolder string, mcpSessionID string, workspaceEnv map[string]string) {
	if len(executors) == 0 || strings.TrimSpace(stepOutputAbsPath) == "" {
		return
	}
	original, ok := executors["execute_shell_command"].(func(ctx context.Context, args map[string]interface{}) (string, error))
	if !ok || original == nil {
		return
	}
	executors["execute_shell_command"] = func(ctx context.Context, args map[string]interface{}) (string, error) {
		if args == nil {
			args = make(map[string]interface{})
		}
		// DB_PATH is the ABSOLUTE path to the workflow's db/db.sqlite. Step shells run
		// with their working directory set to the step's execution folder (not the
		// workflow root), so a relative "db/db.sqlite" would resolve to the wrong place
		// ("unable to open database file"). Steps must use "$DB_PATH".
		mergedEnv := make(map[string]interface{})
		for key, value := range stepRuntimeEnv(workspaceEnv) {
			mergedEnv[key] = value
		}
		mergedEnv["STEP_OUTPUT_DIR"] = stepOutputAbsPath
		mergedEnv["STEP_EXECUTION_DIR"] = stepExecutionAbsPath
		if strings.TrimSpace(dbAbsPath) != "" {
			mergedEnv["DB_PATH"] = dbAbsPath
		}
		mergedEnv["RUN_FOLDER"] = runFolder
		if rawExtraEnv, exists := args["extra_env"]; exists {
			switch typed := rawExtraEnv.(type) {
			case map[string]interface{}:
				for k, v := range typed {
					mergedEnv[k] = v
				}
			case map[string]string:
				for k, v := range typed {
					mergedEnv[k] = v
				}
			}
		}
		// Per-step values must always win over any stale caller-provided value.
		mergedEnv["STEP_OUTPUT_DIR"] = stepOutputAbsPath
		mergedEnv["STEP_EXECUTION_DIR"] = stepExecutionAbsPath
		if strings.TrimSpace(dbAbsPath) != "" {
			mergedEnv["DB_PATH"] = dbAbsPath
		}
		mergedEnv["RUN_FOLDER"] = runFolder
		if strings.TrimSpace(mcpSessionID) != "" {
			// Shell/file tools must resolve against the step-local MCP session so the
			// session-level folder guard matches the prompt's narrow read/write scope.
			mergedEnv["MCP_SESSION_ID"] = mcpSessionID
			if scopedURL := buildSessionScopedMCPAPIURL(mcpSessionID); scopedURL != "" {
				mergedEnv["MCP_API_URL"] = scopedURL
			}
		}
		stringEnv := make(map[string]string, len(mergedEnv))
		for k, v := range mergedEnv {
			if s, ok := v.(string); ok {
				stringEnv[k] = s
			}
		}
		common.PopulateMCPBridgeShortEnv(stringEnv)
		for k, v := range stringEnv {
			mergedEnv[k] = v
		}
		args["extra_env"] = mergedEnv
		// The executor's workspace client may have been created for the parent
		// workflow session. Its security lookup reads ChatSessionIDKey before the
		// command environment, so carry the trusted step identity in the internal
		// context too. This keeps the working directory and folder guard aligned
		// with the step-scoped MCP URL above; model-supplied extra_env cannot
		// select a different security session.
		if strings.TrimSpace(mcpSessionID) != "" {
			ctx = context.WithValue(ctx, common.ChatSessionIDKey, mcpSessionID)
		}
		return original(ctx, args)
	}
}

func registerStepSessionShellEnv(sessionID, stepOutputAbsPath, stepExecutionAbsPath, dbAbsPath, runFolder string, workspaceEnv map[string]string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	env := stepRuntimeEnv(workspaceEnv)
	if env == nil {
		env = make(map[string]string)
	}
	env["STEP_OUTPUT_DIR"] = stepOutputAbsPath
	env["STEP_EXECUTION_DIR"] = stepExecutionAbsPath
	if strings.TrimSpace(dbAbsPath) != "" {
		env["DB_PATH"] = dbAbsPath
	}
	env["RUN_FOLDER"] = runFolder
	common.SetSessionShellEnv(sessionID, env)
}

// stepExecutionScopeAbsPath returns the collaboration root visible to the
// current invocation. Top-level steps keep the run execution directory;
// nested agents and scripts receive their immediate owning Agent subtree.
func stepExecutionScopeAbsPath(stepOutputAbsPath string) string {
	clean := filepath.Clean(stepOutputAbsPath)
	separator := string(filepath.Separator)
	marker := separator + "execution" + separator
	markerIndex := strings.LastIndex(clean, marker)
	if markerIndex < 0 {
		return filepath.Dir(clean)
	}
	executionRoot := clean[:markerIndex+len(marker)-1]
	relativeOutput := clean[markerIndex+len(marker):]
	if parentRoot := nestedArtifactParentRoot(relativeOutput); parentRoot != "" {
		return filepath.Join(executionRoot, parentRoot)
	}
	return filepath.Dir(clean)
}

const workflowDBAccessEnv = "WORKFLOW_DB_ACCESS"

// configureWorkflowDBSession records the trusted logical DB capability for the
// tool executor and, for managed agents, hard-blocks raw db.sqlite/WAL/SHM file
// access. db/README.md and db/assets/ remain governed by the ordinary read/write
// paths. Saved scripted steps retain direct DB access during migration.
func configureWorkflowDBSession(sessionID, workspacePath, dbAccess string, direct bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	common.SetSessionShellEnv(sessionID, map[string]string{workflowDBAccessEnv: dbAccess})
	if direct {
		return
	}
	blocked := []string{}
	if cfg := common.GetSessionShellConfig(sessionID); cfg != nil {
		blocked = append(blocked, cfg.BlockedPaths...)
	}
	dbPath := filepath.Join(workspacePath, DBFolderName, "db.sqlite")
	blocked = append(blocked, dbPath, dbPath+"-wal", dbPath+"-shm")
	common.SetSessionFolderGuardBlockedPaths(sessionID, common.DeduplicateStrings(blocked))
}

// grantScriptBridgeSessionDB gives the session a scripted step's script calls
// the bridge as the step's own database access. A scripted step runs its
// script directly, under the group's MCP session (its MCP_SESSION_ID), not
// under the step's exec session where configureWorkflowDBSession put the
// grant. The group session copies only folder capabilities from its parent
// (common.CopySessionFolderGuard), never the DB grant, so a script's
// mutate_workflow_db was refused ("explicit db_access=read-write is required
// (effective value \"\")") while its query_workflow_db worked. Direct: only the
// grant is set; the session's path blocks stay as they are, so a script still
// cannot open db.sqlite itself.
func grantScriptBridgeSessionDB(sessionID, workspacePath, dbAccess string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || strings.TrimSpace(dbAccess) == "" {
		return
	}
	configureWorkflowDBSession(sessionID, workspacePath, dbAccess, true)
}

// ConfigureManagedWorkflowDBSession applies the workflow database trust
// boundary to a long-lived managed session such as the main Workflow Builder
// chat. These sessions are configured outside this package, unlike workflow
// steps and workshop child agents, so they need an explicit exported entry
// point rather than duplicating the logical grant and raw-file deny rules.
//
// Managed sessions must use query_workflow_db, mutate_workflow_db, and
// apply_workflow_db_migration. Raw access to db.sqlite and its WAL/SHM
// sidecars stays blocked even when the surrounding db/ folder is writable for
// migrations, documentation, and db/assets.
func ConfigureManagedWorkflowDBSession(sessionID, workspacePath string, readWrite bool) {
	dbAccess := DBAccessRead
	if readWrite {
		dbAccess = DBAccessReadWrite
	}
	configureWorkflowDBSession(sessionID, workspacePath, dbAccess, false)
}

// appendManagedDBFileAccess is the common filesystem contract for agentic
// message sequences and orchestrators. SQL access remains mediated by DB tools;
// never grant the parent db/ directory, which would also expose db.sqlite.
func appendManagedDBFileAccess(workspacePath string, readPaths, writePaths []string) ([]string, []string) {
	dbPath := getDBPath(workspacePath)
	assetsPath := filepath.Join(dbPath, DBAssetsFolderName)
	readPaths = append(readPaths, assetsPath, filepath.Join(dbPath, "README.md"))
	writePaths = append(writePaths, assetsPath)
	return readPaths, writePaths
}

func dbWritePathGranted(writePaths []string, workspacePath string) bool {
	want := filepath.Clean(getDBPath(workspacePath))
	for _, path := range writePaths {
		if filepath.Clean(path) == want {
			return true
		}
	}
	return false
}

// setupBrowserDownloadsPathOverride configures the Downloads path for browser automation tools.
// Execution and orchestrator agents share this run-specific agent_browser folder.
func (hcpo *StepBasedWorkflowOrchestrator) setupBrowserDownloadsPathOverride(ctx context.Context, _ *agents.OrchestratorAgentConfig, _ *AgentConfigs) {
	hasAgentBrowser := false
	for _, skill := range hcpo.GetSelectedSkills() {
		if skill == "agent-browser" {
			hasAgentBrowser = true
			break
		}
	}

	if !hasAgentBrowser {
		return // No browser tool, nothing to configure
	}

	// CRITICAL: Ensure selectedRunFolder is set before configuring Downloads path
	// If it's empty, all agents in this group will share a connection with wrong Downloads path
	if hcpo.selectedRunFolder == "" {
		hcpo.GetLogger().Error("❌ [CRITICAL] selectedRunFolder is EMPTY when configuring agent-browser Downloads path! Ensure ApplyExecutionContext is called before creating agents.", nil)
		// Don't return error - continue with default path but log the issue
	}

	// Route browser downloads to execution/Downloads folder in the run directory
	workspacePath := hcpo.GetWorkspacePath()

	// Build the relative path to Downloads folder
	// If run folder is selected: "runs/{runFolder}/execution/Downloads"
	// Otherwise: "execution/Downloads"
	var downloadsRelativePath string
	hcpo.GetLogger().Info(fmt.Sprintf("🔍 [DEBUG] Setting Downloads path - selectedRunFolder: '%s', workspacePath: '%s', sessionID: '%s'", hcpo.selectedRunFolder, workspacePath, hcpo.getSessionID()))
	if hcpo.selectedRunFolder != "" {
		downloadsRelativePath = filepath.Join("runs", hcpo.selectedRunFolder, "execution", "Downloads")
	} else {
		// WARNING: selectedRunFolder is empty - downloads will go to execution/Downloads instead of group-specific folder
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [CRITICAL] selectedRunFolder is EMPTY when setting Downloads path! Downloads will go to execution/Downloads instead of group-specific folder. This may indicate ApplyExecutionContext was not called or selectedRunFolder was not set correctly."))
		downloadsRelativePath = filepath.Join("execution", "Downloads")
	}
	hcpo.GetLogger().Info(fmt.Sprintf("🔍 [DEBUG] Downloads relative path: '%s'", downloadsRelativePath))

	// Create folder via Workspace API with workspacePath for normalization
	if err := createFolderViaAPI(ctx, downloadsRelativePath, workspacePath); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to create downloads directory via API %s: %v", downloadsRelativePath, err))
	}

	// Store the workspace-relative downloads path for agent-browser.
	// The browser executor reads this from context and passes it as WorkingDirectory
	// to the workspace API, so it needs to be relative to workspace root (workspacePath + downloadsRelativePath).
	browserRelPath := filepath.Join(workspacePath, downloadsRelativePath)
	hcpo.SetBrowserDownloadsPath(browserRelPath)
	hcpo.GetLogger().Info(fmt.Sprintf("🔧 Set browser downloads path on orchestrator: %s", browserRelPath))

	hcpo.GetLogger().Info(fmt.Sprintf("🔍 [DEBUG] Browser tool: agent-browser, Session ID: %s, selectedRunFolder: '%s', downloadsPath: '%s'", hcpo.getSessionID(), hcpo.selectedRunFolder, browserRelPath))
}

// isGenericAgentStep reports whether a step was spawned via call_generic_agent.
func isGenericAgentStep(stepID, stepPath string) bool {
	return strings.HasPrefix(stepID, "generic-") || strings.Contains(filepath.ToSlash(stepPath), "/agents/generic/")
}

// setupExecutionFolderGuard sets up folder guard paths for execution agents.
// kbAccess must be one of KBAccessRead / Write / ReadWrite / None — callers resolve it
// via resolveKnowledgebaseAccess before invoking. learningsAccess must be resolved via
// resolveExecutionLearningsAccess so evaluation/routing isolation matches the prompt.
// When kbAccess permits writes the step is the KB writer,
// so knowledgebase/notes/ is added to writePaths. Returns readPaths and writePaths.
func (hcpo *StepBasedWorkflowOrchestrator) setupExecutionFolderGuard(stepPath string, stepID string, kbAccess string, learningsAccess string, dbAccess string, stepConfig *AgentConfigs) (readPaths, writePaths []string) {
	baseWorkspacePath := hcpo.GetWorkspacePath()
	// Use run folder if available, otherwise use base workspace (backward compatibility)
	var runWorkspacePath string
	if hcpo.selectedRunFolder != "" {
		runWorkspacePath = fmt.Sprintf("%s/runs/%s", baseWorkspacePath, hcpo.selectedRunFolder)
	} else {
		runWorkspacePath = baseWorkspacePath
	}
	executionWorkspacePath := fmt.Sprintf("%s/execution", runWorkspacePath)
	// Set folder guard paths:
	// READ: execution folder (to read previous step results) + soul north-star file
	// + global and step-specific learnings (if mode grants read) + knowledgebase folder (if mode grants read)
	// WRITE: only the specific step folder (including nested sub-agent folders) plus execution/Downloads.
	// NOTE: under kbWriteMethod=direct we add knowledgebase/notes/ to writePaths so the
	// step can write per-topic markdown through execute_shell_command. Under
	// kbWriteMethod=agent we add nothing — notes/ is only writable by the post-step KB
	// update agent (setupKBUpdateFolderGuard, triggered by a non-empty knowledgebase_contribution).
	// Use getExecutionFolderPath to support top-level and nested executions.
	stepFolderPath := getExecutionFolderPath(executionWorkspacePath, stepID, stepPath)
	writeRootPath := stepFolderPath
	if parentRoot := nestedArtifactParentRoot(stepPath); parentRoot != "" {
		writeRootPath = filepath.Join(executionWorkspacePath, parentRoot)
	}
	downloadsPath := fmt.Sprintf("%s/Downloads", executionWorkspacePath)
	soulPath := fmt.Sprintf("%s/soul", baseWorkspacePath)
	// planning/ is READ-ONLY here and deliberately so. A step previously could not
	// see the plan at all: it got its own description plus resolved dependencies and
	// had no way to tell whether it was the first of nine or the last, or what
	// consumed its output. Read access lets a step judge its own scope. Writes stay
	// impossible — planning/ is absent from writePaths below, is excluded from
	// WorkflowWritableSubfolders, and isProtectedPlanningPath is the runtime backstop;
	// plan.json/step_config.json are only ever mutated through the typed plan-mod tools.
	planningPath := fmt.Sprintf("%s/%s", baseWorkspacePath, PlanningFolderName)
	// runWorkspacePath, not just its execution/ child. A step is told its outputs
	// live at runs/<run>/execution/<step>/, and confirming that path means walking
	// down to it — but only the leaf was readable, so `ls runs/iteration-0/default`
	// returned "Operation not permitted" while `ls .../execution/<step>` would have
	// worked. A CDP test step on 2026-08-02 burned four calls on exactly that walk.
	// The run folder also holds logs/ and run_metadata.json, which are ordinary
	// evidence for a step reasoning about its own run. Read-only, and scoped to
	// this run — it grants nothing outside what execution/ already exposes.
	// MCP_TOOL_OUTPUT_DIR (mcpagent's spill target for any bridge tool result
	// past its inline size cap — e.g. a large agent_browser snapshot) resolves
	// to <workspace-root>/tool_output_folder, a sibling of runs/ that nothing
	// below granted read access to. Without it, a step told to read its own
	// spilled tool output back hits "outside every workspace root" and has no
	// legal way to comply (PLAT-073 cluster F, dd9ede3c).
	toolOutputPath := fmt.Sprintf("%s/tool_output_folder", baseWorkspacePath)
	readPaths = []string{runWorkspacePath, executionWorkspacePath, soulPath, planningPath, toolOutputPath}
	// Generic agents are also used as read-only Pulse specialists. Their review
	// contracts span plan, eval, report, cost, config, store, and run evidence,
	// so give them workflow-wide read access while retaining the narrow write
	// scope established below.
	if isGenericAgentStep(stepID, stepPath) {
		readPaths = []string{baseWorkspacePath}
	}
	if learningsAccess != LearningsAccessNone {
		readPaths = appendLearningReadPaths(readPaths, baseWorkspacePath, stepID)
	}

	// Every routed child shares its owning Agent subtree. This permits sibling
	// agents and scripts under the same parent to exchange artifacts without
	// granting write access to another top-level step.
	writePaths = []string{writeRootPath, downloadsPath}

	// db/ is always readable. Ordinary workflow execution currently resolves dbAccess
	// to read-write uniformly, so db/ is also writable for those steps. The read branch
	// remains only for callers that explicitly construct a reader profile while the
	// canonical reader/writer refactor is incomplete.
	dbPath := getDBPath(baseWorkspacePath)
	readPaths = append(readPaths, dbPath)
	if dbAccess != DBAccessRead {
		writePaths = append(writePaths, dbPath)
	}

	// Add knowledgebase folder to READ paths when the mode grants read. Under
	// kbWriteMethod=direct, also add knowledgebase/notes/ to WRITE paths so the step
	// can author per-topic markdown through execute_shell_command.
	if kbAccess != KBAccessNone && kbAccessAllowsRead(kbAccess) {
		knowledgebasePath := getKnowledgebasePath(baseWorkspacePath)
		readPaths = append(readPaths, knowledgebasePath)
	}
	if kbAccessAllowsWrite(kbAccess) {
		notesPath := fmt.Sprintf("%s/notes", getKnowledgebasePath(baseWorkspacePath))
		writePaths = append(writePaths, notesPath)
		// A "write" knowledgebase_source grants the same notes/-only write,
		// just in a resolved external workflow's KB instead of this one's own.
		writePaths = append(writePaths, writableExternalKBNotesPaths(baseWorkspacePath)...)
	}

	// User-supplied runtime context lives under knowledgebase/context/
	// — read access is granted by the same kbAccess check above (recursive
	// subtree). No separate flag. The optimizer's reorganize/consolidate passes
	// are responsible for skipping knowledgebase/context/ so user-supplied content
	// is never silently rewritten.

	if opts := hcpo.GetExecutionOptions(); opts != nil && opts.TriggerContextFile != "" {
		readPaths = append(readPaths, opts.TriggerContextFile)
	}
	if opts := hcpo.GetExecutionOptions(); opts != nil && opts.WebhookInputFile != "" {
		readPaths = append(readPaths, opts.WebhookInputFile)
	}
	readPaths = appendAdditionalWorkflowReadPaths(readPaths, baseWorkspacePath, stepConfig)
	readPaths, writePaths, _, _ = appendWorkflowFolderAccess(baseWorkspacePath, readPaths, writePaths, kbAccessAllowsRead(kbAccess))
	readPaths, writePaths = hcpo.appendCDPHostDownloadsPaths(readPaths, writePaths)
	// PLAT-284: one persistent, workflow-scoped folder for anything a step
	// installs (pip --user site, npm globals, downloaded binaries). Steps run
	// in runs/iteration-N/, which rotates, so without this every run
	// reinstalled the same packages from PyPI. The sandbox recognises the
	// folder by its name and points package managers at it
	// (workspace/security/sandbox_tool_env.go); this grant is what makes it
	// writable for every step of the workflow, root-wide write or not.
	sandboxCachePath := fmt.Sprintf("%s/%s", baseWorkspacePath, security.SandboxPersistentDirName)
	if hcpo.usesCodeTree() {
		codePath := baseWorkspacePath + "/code"
		readPaths = append(readPaths, codePath)
		if stepConfig == nil || stepConfig.LockCode == nil || !*stepConfig.LockCode {
			writePaths = append(writePaths, codePath)
		}
	}
	readPaths = append(readPaths, sandboxCachePath)
	writePaths = append(writePaths, sandboxCachePath)
	return common.DeduplicateStrings(readPaths), common.DeduplicateStrings(writePaths)
}

func appendAdditionalWorkflowReadPaths(readPaths []string, baseWorkspacePath string, stepConfig *AgentConfigs) []string {
	if stepConfig == nil || len(stepConfig.AdditionalReadPaths) == 0 {
		return readPaths
	}
	normalized, err := normalizeAdditionalReadPaths(stepConfig.AdditionalReadPaths)
	if err != nil {
		// Tool-authored configs are validated before saving. Revalidate here as a
		// security boundary for hand-edited and legacy config files; an invalid
		// entry grants nothing.
		return readPaths
	}
	for _, relativePath := range normalized {
		readPaths = append(readPaths, filepath.Join(baseWorkspacePath, filepath.FromSlash(relativePath)))
	}
	return readPaths
}

// appendLearningReadPaths grants a step its shared workflow guidance and its own
// saved implementation/helpers. It deliberately does not grant the learnings root
// or another step's folder, and it never grants write access.
func appendLearningReadPaths(readPaths []string, baseWorkspacePath string, stepID string) []string {
	readPaths = append(readPaths, filepath.Join(baseWorkspacePath, LearningsFolderName, GlobalLearningID))

	stepID = strings.TrimSpace(stepID)
	if stepID == "" || stepID == GlobalLearningID || filepath.Base(stepID) != stepID || stepID == "." || stepID == ".." {
		return readPaths
	}
	return append(readPaths, filepath.Join(baseWorkspacePath, LearningsFolderName, stepID))
}

// getCodeExecutionMode determines code execution mode with priority: step config > workflow/preset default
// Note: The workflow/preset default reflects what the user explicitly set. Server.go no longer
// auto-enables code execution mode for the entire workflow. Provider-based auto-enable
// Coding-agent provider setup is handled per-agent in applyStepConfigToAgentConfig.
func (hcpo *StepBasedWorkflowOrchestrator) getCodeExecutionMode(stepConfig *AgentConfigs) bool {
	if stepConfig != nil && stepConfig.UseCodeExecutionMode != nil {
		isCodeExecutionMode := *stepConfig.UseCodeExecutionMode
		hcpo.GetLogger().Info(fmt.Sprintf("🔧 Using step-specific code execution mode: %v", isCodeExecutionMode))
		return isCodeExecutionMode
	}
	isCodeExecutionMode := hcpo.GetUseCodeExecutionMode()
	hcpo.GetLogger().Info(fmt.Sprintf("🔧 Using workflow/preset code execution mode: %v", isCodeExecutionMode))
	return isCodeExecutionMode
}

// resolveStepID resolves the step ID from stepIDOverride or falls back to stepPath
// Priority: stepIDOverride > stepPath fallback
func (hcpo *StepBasedWorkflowOrchestrator) resolveStepID(stepPath, stepIDOverride string) string {
	if stepIDOverride != "" {
		return stepIDOverride
	}

	hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Could not determine step ID for %s, using stepPath as fallback", stepPath))
	return stepPath
}

// selectExecutionLLM resolves the configured execution model before generation.
//
// Priority for main step execution:
//  1. step config ExecutionLLM   — explicit per-step override; always wins when set
//  2. parent ExecutionLLM via context — propagated when the parent todo-task step
//     has an ExecutionLLM set; when present, tier selection is skipped entirely
//  3. tiered mode                — workshop override, persistent step execution_tier,
//     preferred_tier from context, or the default tier
//  4. orchestrator main LLM      — inherited default
func (hcpo *StepBasedWorkflowOrchestrator) selectExecutionLLM(
	ctx context.Context,
	stepConfig *AgentConfigs,
	stepPath string,
) *orchestrator.LLMConfig {
	orchestratorLLMConfig := hcpo.GetLLMConfig()
	// Guard against nil — scheduler-triggered sessions may not have an orchestrator LLM set.
	if orchestratorLLMConfig == nil {
		orchestratorLLMConfig = &orchestrator.LLMConfig{}
	}

	// ── 1. STEP CONFIG ExecutionLLM ──────────────────────────────────────────
	// Explicit per-step execution model always wins, including in tiered mode.
	if stepConfig != nil && stepConfig.ExecutionLLM != nil && stepConfig.ExecutionLLM.Provider != "" && stepConfig.ExecutionLLM.ModelID != "" {
		hcpo.GetLogger().Info(fmt.Sprintf("🔧 [STEP OVERRIDE] Using step ExecutionLLM for step %s: %s/%s",
			stepPath, stepConfig.ExecutionLLM.Provider, stepConfig.ExecutionLLM.ModelID))
		return &orchestrator.LLMConfig{
			Primary: orchestrator.LLMModel{
				Provider:     stepConfig.ExecutionLLM.Provider,
				ModelID:      stepConfig.ExecutionLLM.ModelID,
				Options:      stepConfig.ExecutionLLM.Options,
				ConnectionID: stepConfig.ExecutionLLM.ConnectionID,
			},
			APIKeys: orchestratorLLMConfig.APIKeys,
		}
	}

	// ── 2. SUB-AGENT OVERRIDE ────────────────────────────────────────────────
	// When the parent todo-task step pins an ExecutionLLM, the wrapper injects it
	// into the sub-agent's context. Its presence is itself the signal that we're
	// in "propagate parent's LLM" mode — tier selection is bypassed.
	if subAgentLLM, ok := ctx.Value(virtualtools.SubAgentLLMContextKey).(*AgentLLMConfig); ok &&
		subAgentLLM != nil && subAgentLLM.Provider != "" && subAgentLLM.ModelID != "" {
		hcpo.GetLogger().Info(fmt.Sprintf("🎯 [SUB-AGENT] Using parent ExecutionLLM for step %s: %s/%s",
			stepPath, subAgentLLM.Provider, subAgentLLM.ModelID))
		return &orchestrator.LLMConfig{
			Primary: orchestrator.LLMModel{
				Provider:     subAgentLLM.Provider,
				ModelID:      subAgentLLM.ModelID,
				Options:      subAgentLLM.Options,
				ConnectionID: subAgentLLM.ConnectionID,
			},
			APIKeys: hcpo.GetAPIKeys(),
		}
	}

	// ── 3. TIERED MODE ───────────────────────────────────────────────────────
	// Default-tier resolution when no explicit step override is set.
	if hcpo.tierResolver != nil {
		// Workshop execute_step tier override (e.g., execute_step(step_id, tier="medium"))
		if workshopTier, ok := ctx.Value(WorkshopTierOverrideKey).(int); ok && workshopTier >= 1 && workshopTier <= 3 {
			tier := TierLevel(workshopTier)
			llmConfig := hcpo.tierResolver.ResolveTier(tier)
			if llmConfig != nil {
				hcpo.GetLogger().Info(fmt.Sprintf("🏷️ [TIERED] Workshop tier override: Tier %d (%s) for step %s: %s/%s",
					workshopTier, TierLevelLabel(tier), stepPath, llmConfig.Primary.Provider, llmConfig.Primary.ModelID))
			}
			return llmConfig
		}
		if stepConfig != nil {
			if fixedTier, ok := ParseTierOverride(stepConfig.ExecutionTier); ok {
				llmConfig := hcpo.tierResolver.ResolveTier(fixedTier)
				if llmConfig != nil {
					hcpo.GetLogger().Info(fmt.Sprintf("🏷️ [TIERED] Execution agent for step %s using fixed execution_tier=%s: %s/%s",
						stepPath, NormalizeTierOverride(stepConfig.ExecutionTier), llmConfig.Primary.Provider, llmConfig.Primary.ModelID))
				}
				return llmConfig
			}
			if strings.TrimSpace(stepConfig.ExecutionTier) != "" {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Invalid execution_tier=%q for step %s — ignoring and continuing with normal tier selection", stepConfig.ExecutionTier, stepPath))
			}
		}
		if preferredTier, ok := ctx.Value(virtualtools.PreferredTierContextKey).(int); ok && preferredTier >= 1 && preferredTier <= 3 {
			tier := TierLevel(preferredTier)
			llmConfig := hcpo.tierResolver.ResolveTier(tier)
			if llmConfig != nil {
				hcpo.GetLogger().Info(fmt.Sprintf("🏷️ [TIERED] Execution agent using PREFERRED Tier %d (%s) for step %s: %s/%s",
					preferredTier, TierLevelLabel(tier), stepPath, llmConfig.Primary.Provider, llmConfig.Primary.ModelID))
			}
			return llmConfig
		}
		// No automatic history-based tier promotion. Architecture review proposes
		// explicit configuration changes; an unconfigured execution step stays High.

		llmConfig, tier := hcpo.tierResolver.ResolveForExecution()
		if llmConfig != nil {
			hcpo.GetLogger().Info(fmt.Sprintf("🏷️ [TIERED] Execution agent for step %s using Tier %d (%s): %s/%s",
				stepPath, int(tier), TierLevelLabel(tier), llmConfig.Primary.Provider, llmConfig.Primary.ModelID))
		}
		return llmConfig
	}

	// ── 4. NO VALID CONFIG ──────────────────────────────────────────────────
	// Return nil so the caller can surface a user-visible error instead of crashing.
	hcpo.GetLogger().Warn(fmt.Sprintf("❌ selectExecutionLLM: no valid LLM configuration found for step %s — tier resolver is required", stepPath))
	return nil
}

// applyStepConfigToAgentConfig applies step-specific configuration overrides to agent config
func (hcpo *StepBasedWorkflowOrchestrator) applyStepConfigToAgentConfig(ctx context.Context, config *agents.OrchestratorAgentConfig, stepConfig *AgentConfigs, isCodeExecutionMode bool, isScripted bool) {
	workflowServers := hcpo.GetSelectedServers()
	// Use step-specific servers if provided, filtered against workflow-level servers.
	// Workflow is the hard cap: if a server was removed from the workflow no step can use it.
	if stepConfig != nil && stepConfig.SelectedServers != nil && len(stepConfig.SelectedServers) > 0 {
		filtered := filterServersByWorkflow(stepConfig.SelectedServers, workflowServers)
		config.ServerNames = filtered
		hcpo.GetLogger().Info(fmt.Sprintf("🔧 Using step-specific execution-only servers (workflow-filtered): %v → %v", stepConfig.SelectedServers, filtered))
	} else {
		// Use orchestrator defaults when stepConfig is nil or SelectedServers is empty
		config.ServerNames = normalizeServerNames(workflowServers)
		if stepConfig != nil {
			hcpo.GetLogger().Info(fmt.Sprintf("🔧 Step config found but SelectedServers is empty - using orchestrator defaults: %v", config.ServerNames))
		} else {
			hcpo.GetLogger().Info(fmt.Sprintf("🔧 Step config not found - using orchestrator defaults: %v", config.ServerNames))
		}
	}
	hcpo.addPlaceMCPServers(ctx, config)
	if stepConfig != nil && len(stepConfig.SelectedTools) > 0 {
		filtered := filterToolsByWorkflow(stepConfig.SelectedTools, workflowServers)
		config.SelectedTools = filtered
		hcpo.GetLogger().Info(fmt.Sprintf("🔧 Using step-specific execution-only tools (workflow-filtered): %v → %v", stepConfig.SelectedTools, filtered))
	} else {
		// Explicitly set orchestrator defaults when stepConfig is nil or SelectedTools is empty
		config.SelectedTools = hcpo.GetSelectedTools()
		if stepConfig != nil {
			// Log when stepConfig exists but SelectedTools is empty (will use orchestrator defaults)
			hcpo.GetLogger().Info(fmt.Sprintf("🔧 Step config found but no SelectedTools specified - using orchestrator defaults: %v", config.SelectedTools))
		}
	}

	// Determine execution mode: CLI providers and scripted steps always use code execution mode.
	// scripted steps need code execution mode so the agent gets the tool index and get_api_spec
	// virtual tool — without these, the LLM has to guess MCP server/tool names when writing main.py.
	// isScripted comes from the caller, which knows the plan step (PLAT-287:
	// the plan type decides; the config no longer carries a mode).
	actualProvider := config.LLMConfig.Primary.Provider
	if common.IsCLIProvider(actualProvider) {
		config.UseCodeExecutionMode = true
		hcpo.GetLogger().Info(fmt.Sprintf("🔧 Code execution mode forced for CLI provider '%s' - MCP tools accessed via HTTP bridge", actualProvider))
	} else if isScripted {
		config.UseCodeExecutionMode = true
		hcpo.GetLogger().Info(fmt.Sprintf("🔧 Code execution mode forced for scripted step — LLM needs tool index and get_api_spec to write main.py"))
	} else if stepConfig != nil && stepConfig.UseCodeExecutionMode != nil {
		config.UseCodeExecutionMode = *stepConfig.UseCodeExecutionMode
		hcpo.GetLogger().Info(fmt.Sprintf("🔧 Using step-specific code execution mode: %v", config.UseCodeExecutionMode))
	} else {
		config.UseCodeExecutionMode = false
		hcpo.GetLogger().Info(fmt.Sprintf("🔧 Provider '%s': code execution mode disabled (not CLI provider)", actualProvider))
	}
	config.CodingAgentKeepAlive = false
	if stepConfig != nil && strings.TrimSpace(stepConfig.CodingAgentTmuxLifecycle) != "" {
		normalizedLifecycle := normalizeCodingAgentTmuxLifecycle(stepConfig.CodingAgentTmuxLifecycle)
		if normalizedLifecycle == "" {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Unknown coding_agent_tmux_lifecycle=%q for provider '%s'; defaulting to close_on_completion", stepConfig.CodingAgentTmuxLifecycle, actualProvider))
		} else if normalizedLifecycle == CodingAgentTmuxLifecycleKeepAlive {
			config.CodingAgentKeepAlive = true
			hcpo.GetLogger().Info(fmt.Sprintf("🔧 Coding-agent tmux lifecycle for provider '%s': %s", actualProvider, normalizedLifecycle))
		} else {
			hcpo.GetLogger().Info(fmt.Sprintf("🔧 Coding-agent tmux lifecycle for provider '%s': %s", actualProvider, normalizedLifecycle))
		}
	}

	// Keep generated CLI files in a private, session-stable runtime. Execution
	// factories separately link output/ to the exact step artifact directory;
	// native access still follows the step's admitted permissions and tool mode.
	config.IsolateCodingAgentWorkspace = true

	effectiveTransport := hcpo.applyWorkflowTransportToAgentConfig(config, stepConfig, "workflow step")
	hcpo.publishWorkflowTransportContext(effectiveTransport, executionModeLabel(isScripted))

}

// Long-running workflow execution agents should inherit cancellation from the
// outer workflow/tool context instead of the generic 5-minute agent timeout.
// Otherwise, saved-script execution and sub-agent orchestration get canceled
// even when their tool-specific timeouts are configured correctly.
func (hcpo *StepBasedWorkflowOrchestrator) disableParentAgentTimeout(config *agents.OrchestratorAgentConfig, agentKind string) {
	if config == nil {
		return
	}
	config.Timeout = 0
	hcpo.GetLogger().Info(fmt.Sprintf("⏱️ Disabled parent agent timeout for %s; using outer workflow/tool cancellation instead", agentKind))
}

// prepareCustomTools filters and prepares custom tools based on step config
func (hcpo *StepBasedWorkflowOrchestrator) prepareCustomTools(stepConfig *AgentConfigs) ([]llmtypes.Tool, map[string]interface{}) {
	// Explicit selection narrows optional tools. Defaults and explicit lists
	// share the same capability-derived additions below. Human tools are named
	// individually: a step needs immediate feedback, notifications, and durable
	// decision creation, not every administrative/connector tool that happens to
	// share the human_tools registry.
	enabledTools := []string{"workspace_advanced:*"}
	if stepConfig != nil && len(stepConfig.EnabledCustomTools) > 0 {
		enabledTools = nil
		hasAdvanced := false
		for _, entry := range stepConfig.EnabledCustomTools {
			// Retired media categories cannot re-enable provider tools.
			if entry == "workspace_image:*" || strings.HasPrefix(entry, "workspace_image:") ||
				entry == "workspace_image_gen:*" || strings.HasPrefix(entry, "workspace_image_gen:") ||
				entry == "workspace_image_edit:*" || strings.HasPrefix(entry, "workspace_image_edit:") {
				continue
			}
			// Legacy step configs commonly persisted human_tools:*. Treat it as
			// the safe baseline; additional human tools must now be named.
			if strings.TrimSpace(entry) == "human_tools:*" {
				continue
			}
			if strings.HasPrefix(entry, "workspace_advanced") {
				hasAdvanced = true
			}
			enabledTools = append(enabledTools, entry)
		}
		if !hasAdvanced {
			enabledTools = append(enabledTools, "workspace_advanced:*")
			hcpo.GetLogger().Info("🔧 Auto-including workspace_advanced:*")
		}
	}
	for _, required := range []string{
		"human_tools:human_feedback",
		"human_tools:notify_user",
		"human_tools:create_human_input_request",
	} {
		if !slices.Contains(enabledTools, required) {
			enabledTools = append(enabledTools, required)
		}
	}

	// DB and cost access come from capabilities, not from whether an explicit
	// tool list was supplied. Filtering against the actual workspace pool below
	// cannot introduce a tool withheld by the owning profile.
	enabledTools = append(enabledTools, "workflow_db:query_workflow_db", "workflow_costs:query_workflow_costs", "workflow:get_goal_metrics", "workflow:record_goal_observations")
	if resolveDBAccess(stepConfig) == DBAccessReadWrite {
		enabledTools = append(enabledTools, "workflow_db:mutate_workflow_db", "workflow_db:apply_workflow_db_migration")
	}

	hasBrowserCategory := false
	for _, entry := range enabledTools {
		if strings.HasPrefix(entry, "workspace_browser") {
			hasBrowserCategory = true
			break
		}
	}
	if !hasBrowserCategory {
		for _, tool := range hcpo.WorkspaceTools {
			if tool.Function != nil && tool.Function.Name == "agent_browser" {
				enabledTools = append(enabledTools, "workspace_browser:*")
				break
			}
		}
	}
	tools, executors := orchestrator.FilterCustomToolsByCategory(hcpo.WorkspaceTools, hcpo.WorkspaceToolExecutors, enabledTools)
	hcpo.GetLogger().Info(fmt.Sprintf("🔧 Filtered custom tools: %d tools enabled from %d entries: %v", len(tools), len(enabledTools), enabledTools))
	return tools, executors
}

// prepareWorkspaceToolsOnly prepares minimal tools for KB maintenance agents.
// They can inspect structured workflow state through query_workflow_db and can
// update their authorized files, but they do not receive database mutation or
// human tools.
func (hcpo *StepBasedWorkflowOrchestrator) prepareWorkspaceToolsOnly() ([]llmtypes.Tool, map[string]interface{}) {
	tools, executors := orchestrator.FilterCustomToolsByCategory(
		hcpo.WorkspaceTools,
		hcpo.WorkspaceToolExecutors,
		[]string{
			"workspace_advanced:execute_shell_command",
			"workspace_advanced:diff_patch_workspace_file",
			"workflow_db:query_workflow_db",
		},
	)
	hcpo.GetLogger().Info(fmt.Sprintf("🔧 Prepared %d KB maintenance tools (shell + diff_patch + DB query, no mutation/human tools)", len(tools)))
	return tools, executors
}

// setupKBUpdateFolderGuard grants the KB update agent read on the step's execution
// folder (+ siblings, so relative-path references to other step outputs resolve) and
// knowledgebase/, and write on knowledgebase/ only.
func (hcpo *StepBasedWorkflowOrchestrator) setupKBUpdateFolderGuard(stepID string, stepPath string) (readPaths, writePaths []string) {
	baseWorkspacePath := hcpo.GetWorkspacePath()
	var runWorkspacePath string
	if hcpo.selectedRunFolder != "" {
		runWorkspacePath = fmt.Sprintf("%s/runs/%s", baseWorkspacePath, hcpo.selectedRunFolder)
	} else {
		runWorkspacePath = baseWorkspacePath
	}
	executionWorkspacePath := fmt.Sprintf("%s/execution", runWorkspacePath)
	stepFolderPath := getExecutionFolderPath(executionWorkspacePath, stepID, stepPath)
	knowledgebasePath := getKnowledgebasePath(baseWorkspacePath)

	// tool_output_folder for the same reason every other guard grants it: any
	// bridge tool result past its inline cap is spilled there, and an agent told
	// "full output saved to <path>" needs a legal way to read it back. A KB
	// update agent reads large step artifacts to summarize them, so it hits this
	// as readily as an execution step does.
	toolOutputPath := fmt.Sprintf("%s/tool_output_folder", baseWorkspacePath)

	readPaths = []string{executionWorkspacePath, stepFolderPath, knowledgebasePath, toolOutputPath}
	writePaths = []string{knowledgebasePath}
	return readPaths, writePaths
}

// setupSubAgentSessionGuard creates a dedicated MCP session ID for a sub-agent
// that should NOT share the workflow's group MCP session, and registers its
// folder guard at that session's level so sandbox-exec enforces the correct
// writes when the sub-agent issues shell commands.
//
// Why this exists: sub-agents with `ServerNames = [NoServers]` (learning agent,
// KB update/consolidate/reorganize, eval scoring when workspace-only) don't
// need to share the group's MCP session for browser/gmail connection reuse.
// But if they DO share it, their folder guard is set at orchestrator level
// (SetWorkspacePathForFolderGuard) while the group session's guard is set at
// session level (SetSessionFolderGuard) — and session-level wins in
// pkg/workspace/execute_shell_command.go's priority order. Result: the sub-
// agent's writes get denied by the parent step's guard, which excludes paths
// like learnings/_global/ and knowledgebase/.
//
// Returns the dedicated session ID — assign to `config.MCPSessionID` BEFORE
// calling CreateAndSetupStandardAgentWithConfig. The caller should also call
// `common.ClearSessionShellConfig(sessionID)` after the agent finishes
// (typically deferred in the runXxxPhase function).
func (hcpo *StepBasedWorkflowOrchestrator) setupSubAgentSessionGuard(agentKind string, stepID string, readPaths []string, writePaths []string, kbReadOverride ...bool) string {
	sessionID := fmt.Sprintf("sub-%s-%s-%d", agentKind, stepID, time.Now().UnixNano())
	hcpo.configureSubAgentSessionGuard(sessionID, agentKind, stepID, readPaths, writePaths, kbReadOverride...)
	return sessionID
}

// workflowStepShellWorkingDir returns the server-side cwd every workflow-step
// tool session must use. It is derived from the step's own execution context,
// not inherited through the parent HTTP/group session chain: those sessions use
// different identities and are created in different orders in workshop and
// batch execution.
func (hcpo *StepBasedWorkflowOrchestrator) workflowStepShellWorkingDir() string {
	workspacePath := strings.TrimRight(strings.TrimSpace(hcpo.GetWorkspacePath()), "/")
	runFolder := strings.Trim(strings.TrimSpace(hcpo.selectedRunFolder), "/")
	if workspacePath == "" || runFolder == "" {
		return ""
	}
	return fmt.Sprintf("%s/runs/%s/execution", workspacePath, runFolder)
}

func (hcpo *StepBasedWorkflowOrchestrator) configureSubAgentSessionGuard(sessionID string, agentKind string, stepID string, readPaths []string, writePaths []string, kbReadOverride ...bool) {
	kbRead := false
	localKB := filepath.Join(GetPromptDocsRoot(), hcpo.GetWorkspacePath(), "knowledgebase")
	for _, path := range readPaths {
		if !filepath.IsAbs(path) {
			path = filepath.Join(GetPromptDocsRoot(), path)
		}
		if workflowkb.Within(path, localKB) {
			kbRead = true
		}
	}
	if len(kbReadOverride) > 0 {
		kbRead = kbReadOverride[0]
	}
	readPaths, writePaths, readOnlyPaths, folderEnv := appendWorkflowFolderAccess(hcpo.GetWorkspacePath(), readPaths, writePaths, kbRead)
	common.SetSessionFolderGuard(sessionID, readPaths, writePaths)
	if opts := hcpo.GetExecutionOptions(); opts != nil && opts.WebhookInputFile != "" {
		readOnlyPaths = append(readOnlyPaths, opts.WebhookInputFile)
		if opts.TriggerContextFile != "" {
			readOnlyPaths = append(readOnlyPaths, opts.TriggerContextFile)
		}
	}
	configureWorkflowFolderAccessSession(sessionID, hcpo.GetWorkspacePath(), readOnlyPaths, folderEnv)
	hcpo.grantSessionCDPHostDownloadsReadWrite(sessionID)
	virtualtools.InheritSessionNotificationDestination(hcpo.GetMCPSessionID(), sessionID)

	// Set the child identity's cwd directly. The run cwd used to be recorded on
	// httpSessionID, while shell calls carry this dedicated sessionID. Group
	// creation copied only folder guards, so inheriting through the group missed
	// the cwd and silently fell back to workspace root. Deriving it here also
	// makes workshop and batch setup order irrelevant.
	workingDir := hcpo.workflowStepShellWorkingDir()
	if workingDir == "" {
		// Preserve compatibility for non-run maintenance callers. A real workflow
		// step also carries STEP_OUTPUT_DIR and is rejected by the shell bridge if
		// neither this direct value nor a parent fallback is available.
		if parentSessionID := strings.TrimSpace(hcpo.GetMCPSessionID()); parentSessionID != "" {
			if parentCfg := common.GetSessionShellConfig(parentSessionID); parentCfg != nil {
				workingDir = strings.TrimSpace(parentCfg.WorkingDir)
			}
		}
	}
	if workingDir != "" {
		common.SetSessionWorkingDir(sessionID, workingDir)
	}

	hcpo.GetLogger().Info(fmt.Sprintf(
		"🔒 Sub-agent session %q (%s/%s) — cwd=%q folder guard set at session level Read=%v Write=%v",
		sessionID, agentKind, stepID, workingDir, readPaths, writePaths,
	))
}

func (hcpo *StepBasedWorkflowOrchestrator) selectPhaseLLM(agentPurpose string) *orchestrator.LLMConfig {
	if hcpo.presetPhaseLLM == nil || hcpo.presetPhaseLLM.Provider == "" || hcpo.presetPhaseLLM.ModelID == "" {
		hcpo.GetLogger().Warn(fmt.Sprintf("selectPhaseLLM: no valid phase LLM configured for %s", agentPurpose))
		return nil
	}
	hcpo.GetLogger().Info(fmt.Sprintf("🔧 Using phase LLM for %s: %s/%s",
		agentPurpose, hcpo.presetPhaseLLM.Provider, hcpo.presetPhaseLLM.ModelID))
	return &orchestrator.LLMConfig{
		Primary: orchestrator.LLMModel{
			Provider:     hcpo.presetPhaseLLM.Provider,
			ModelID:      hcpo.presetPhaseLLM.ModelID,
			Options:      hcpo.presetPhaseLLM.Options,
			ConnectionID: hcpo.presetPhaseLLM.ConnectionID,
		},
		APIKeys: hcpo.GetAPIKeys(),
	}
}

func (hcpo *StepBasedWorkflowOrchestrator) selectPulseLLM(agentPurpose string) *orchestrator.LLMConfig {
	if hcpo.presetPulseLLM == nil || hcpo.presetPulseLLM.Provider == "" || hcpo.presetPulseLLM.ModelID == "" {
		return hcpo.selectPhaseLLM(agentPurpose)
	}
	hcpo.GetLogger().Info(fmt.Sprintf("🔧 Using Pulse LLM for %s: %s/%s",
		agentPurpose, hcpo.presetPulseLLM.Provider, hcpo.presetPulseLLM.ModelID))
	return &orchestrator.LLMConfig{
		Primary: orchestrator.LLMModel{
			Provider:     hcpo.presetPulseLLM.Provider,
			ModelID:      hcpo.presetPulseLLM.ModelID,
			Options:      hcpo.presetPulseLLM.Options,
			ConnectionID: hcpo.presetPulseLLM.ConnectionID,
		},
		APIKeys: hcpo.GetAPIKeys(),
	}
}

// selectPulseUpkeepLLM is the model for Pulse's platform-upkeep work (Plan
// Drift, Technical and Architecture reviews, KB maintenance): the workflow's
// Medium tier, so pulse_llm stays reserved for Goal Work. Without a Medium
// tier it falls back to the Pulse model, then the phase model.
func (hcpo *StepBasedWorkflowOrchestrator) selectPulseUpkeepLLM(agentPurpose string) *orchestrator.LLMConfig {
	if hcpo.tierResolver != nil {
		if cfg := hcpo.tierResolver.ResolveTier(TierMedium); cfg != nil {
			hcpo.GetLogger().Info(fmt.Sprintf("🔧 Using Medium tier for Pulse upkeep %s: %s/%s",
				agentPurpose, cfg.Primary.Provider, cfg.Primary.ModelID))
			return cfg
		}
	}
	return hcpo.selectPulseLLM(agentPurpose)
}

// selectBackgroundTaskLLM picks the model for a run_in_background child. A
// child launched by a scheduler Pulse turn is a Pulse agent: Goal Work
// (review module strategic_review) gets the Pulse model, and every other Pulse
// child (Plan Drift, Technical, Architecture) gets the cheaper upkeep model.
// Any other background task follows the Builder (phase) model like the chat
// that spawned it. The parent conversation keeps its retained coding CLI and
// cannot change model mid-turn, but a background child always starts its own
// process, so this is where the choice takes effect.
func (hcpo *StepBasedWorkflowOrchestrator) selectBackgroundTaskLLM(pulseLifecycleTurn bool, reviewModule, agentPurpose string) *orchestrator.LLMConfig {
	if !pulseLifecycleTurn {
		return hcpo.selectPhaseLLM(agentPurpose)
	}
	if reviewModule == "strategic_review" {
		return hcpo.selectPulseLLM(agentPurpose)
	}
	return hcpo.selectPulseUpkeepLLM(agentPurpose)
}

// createKBConsolidateAgent builds the one-shot KB consolidate agent. Same folder-guard
// shape as KB update/reorganize: read execution folder + KB, write KB only. The read
// path on executionWorkspacePath is what gives it access to ALL step output folders
// under the selected run, which is exactly what distinguishes consolidation from per-step
// updates and from reorganize.
func (hcpo *StepBasedWorkflowOrchestrator) createKBConsolidateAgent(ctx context.Context, phase string, agentName string, stepConfig *AgentConfigs) (agents.OrchestratorAgent, error) {
	stepID := "builder-consolidate"
	stepPath := "builder-consolidate"

	readPaths, writePaths := hcpo.setupKBUpdateFolderGuard(stepID, stepPath)
	if err := hcpo.materializeWorkflowGuardPaths(readPaths, writePaths); err != nil {
		return nil, err
	}
	subAgentSessionID := hcpo.setupSubAgentSessionGuard("kb-consolidate", stepID, readPaths, writePaths)
	configureWorkflowDBSession(subAgentSessionID, hcpo.GetWorkspacePath(), DBAccessRead, false)
	hcpo.SetWorkspacePathForFolderGuard(readPaths, writePaths)
	hcpo.GetLogger().Info(fmt.Sprintf("🔒 Setting folder guard for KB consolidate agent - Read: %v, Write: %v", readPaths, writePaths))

	llmConfig := hcpo.selectPulseUpkeepLLM("KB consolidate agent")
	if llmConfig == nil {
		return nil, fmt.Errorf("no valid LLM configuration found for KB consolidate agent")
	}

	// Consolidation may touch many entities and write multiple pattern notes — give it
	// the same headroom as reorganize (60 turns).
	maxTurns := 60
	config := hcpo.CreateStandardAgentConfigWithLLM(agentName, maxTurns, agents.OutputFormatStructured, llmConfig)
	effectiveTransport := hcpo.applyWorkflowTransportToAgentConfig(config, stepConfig, "KB consolidate agent")
	hcpo.publishWorkflowTransportContext(effectiveTransport, "")
	config.ServerNames = []string{mcpclient.NoServers}
	config.MCPSessionID = subAgentSessionID
	config.UseCodeExecutionMode = requiresCodeExecutionForProvider(&AgentLLMConfig{
		Provider: config.LLMConfig.Primary.Provider,
		ModelID:  config.LLMConfig.Primary.ModelID,
	})
	disabled := false
	config.EnableContextOffloading = &disabled

	toolsToRegister, executorsToUse := hcpo.prepareWorkspaceToolsOnly()

	createAgentFunc := func(config *agents.OrchestratorAgentConfig, logger loggerv2.Logger, tracer observability.Tracer, eventBridge mcpagent.AgentEventListener) agents.OrchestratorAgent {
		return NewKBConsolidateAgent(config, logger, tracer, eventBridge)
	}
	agent, err := hcpo.CreateAndSetupStandardAgentWithConfig(
		ctx,
		config,
		phase,
		0,
		0,
		stepID,
		createAgentFunc,
		toolsToRegister,
		executorsToUse,
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create KB consolidate agent: %w", err)
	}
	if err := hcpo.applyPostSetupToAgent(agent, agentName); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Post-setup configuration failed for %s: %v", agentName, err))
	}
	return agent, nil
}

// createKBReorganizeAgent builds the one-shot KB reorganize agent. Same folder-guard
// shape as KB update (read execution + KB, write KB only). stepID/stepPath are
// synthetic because reorganize runs outside step context.
func (hcpo *StepBasedWorkflowOrchestrator) createKBReorganizeAgent(ctx context.Context, phase string, agentName string, stepConfig *AgentConfigs) (agents.OrchestratorAgent, error) {
	stepID := "builder-reorganize"
	stepPath := "builder-reorganize"

	readPaths, writePaths := hcpo.setupKBUpdateFolderGuard(stepID, stepPath)
	if err := hcpo.materializeWorkflowGuardPaths(readPaths, writePaths); err != nil {
		return nil, err
	}
	subAgentSessionID := hcpo.setupSubAgentSessionGuard("kb-reorganize", stepID, readPaths, writePaths)
	configureWorkflowDBSession(subAgentSessionID, hcpo.GetWorkspacePath(), DBAccessRead, false)
	hcpo.SetWorkspacePathForFolderGuard(readPaths, writePaths)
	hcpo.GetLogger().Info(fmt.Sprintf("🔒 Setting folder guard for KB reorganize agent - Read: %v, Write: %v", readPaths, writePaths))

	llmConfig := hcpo.selectPulseUpkeepLLM("KB reorganize agent")
	if llmConfig == nil {
		return nil, fmt.Errorf("no valid LLM configuration found for KB reorganize agent")
	}

	// Reorganize may do larger edits than an update; give it more turns than update's 40
	// but still cap to prevent runaway agents under ambiguous instructions.
	maxTurns := 60
	config := hcpo.CreateStandardAgentConfigWithLLM(agentName, maxTurns, agents.OutputFormatStructured, llmConfig)
	effectiveTransport := hcpo.applyWorkflowTransportToAgentConfig(config, stepConfig, "KB reorganize agent")
	hcpo.publishWorkflowTransportContext(effectiveTransport, "")
	config.ServerNames = []string{mcpclient.NoServers}
	config.MCPSessionID = subAgentSessionID
	config.UseCodeExecutionMode = requiresCodeExecutionForProvider(&AgentLLMConfig{
		Provider: config.LLMConfig.Primary.Provider,
		ModelID:  config.LLMConfig.Primary.ModelID,
	})
	disabled := false
	config.EnableContextOffloading = &disabled

	toolsToRegister, executorsToUse := hcpo.prepareWorkspaceToolsOnly()

	createAgentFunc := func(config *agents.OrchestratorAgentConfig, logger loggerv2.Logger, tracer observability.Tracer, eventBridge mcpagent.AgentEventListener) agents.OrchestratorAgent {
		return NewKBReorganizeAgent(config, logger, tracer, eventBridge)
	}
	agent, err := hcpo.CreateAndSetupStandardAgentWithConfig(
		ctx,
		config,
		phase,
		0, // stepIndex (not applicable for reorganize)
		0, // iteration (not applicable)
		stepID,
		createAgentFunc,
		toolsToRegister,
		executorsToUse,
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create KB reorganize agent: %w", err)
	}
	if err := hcpo.applyPostSetupToAgent(agent, agentName); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Post-setup configuration failed for %s: %v", agentName, err))
	}
	return agent, nil
}

// applyPostSetupToAgent applies post-setup configuration to an agent after base factory setup
// This includes setting folder guard paths and optionally updating the code execution registry
// agent: The orchestrator agent to configure
// agentName: Name of the agent (for logging)
func (hcpo *StepBasedWorkflowOrchestrator) applyPostSetupToAgent(agent agents.OrchestratorAgent, agentName string) error {
	baseAgent := agent.GetBaseAgent()
	if baseAgent == nil {
		return nil // No base agent, nothing to configure
	}

	// Set folder guard paths on MCP agent (required for both code execution mode and simple mode)
	// This ensures path validation works at the tool executor level
	readPaths, writePaths := hcpo.GetFolderGuardPaths()
	baseAgent.SetWorkspacePolicy(readPaths, writePaths)
	hcpo.GetLogger().Info(fmt.Sprintf("🔒 Folder guard paths set for %s agent - Read: %v, Write: %v", agentName, readPaths, writePaths))

	return nil
}

// ============================================================================
// Phase 2: Refactored Agent Creators (Using Base Factory)
// ============================================================================

// createExecutionOnlyAgent creates an execution-only agent that receives pre-discovered learning history.
// stepPath: Top-level step path or nested Agent artifact call path.
// stepIDOverride: Optional explicit step ID to use for learnings / metadata selection (e.g., sub-agent step ID).
// artifactFolderNameOverride: Optional execution/log folder name. This keeps logical step ID stable while isolating per-call artifacts.
//
//	When empty, the step ID will be derived from stepPath.
type parentStepIndexContextKey struct{}

func withParentStepIndex(ctx context.Context, stepIndex int) context.Context {
	return context.WithValue(ctx, parentStepIndexContextKey{}, stepIndex)
}

func parentStepIndexFromContext(ctx context.Context, fallback int) int {
	if stepIndex, ok := ctx.Value(parentStepIndexContextKey{}).(int); ok && stepIndex >= 0 {
		return stepIndex
	}
	return fallback
}

func (hcpo *StepBasedWorkflowOrchestrator) createExecutionOnlyAgent(ctx context.Context, phase string, stepPath string, agentName string, stepConfig *AgentConfigs, planStep PlanStepInterface, stepIDOverride string, artifactFolderNameOverride string) (agents.OrchestratorAgent, error) {
	// 1. Resolve stepID first (needed for folder guard setup)
	stepID := hcpo.resolveStepID(stepPath, stepIDOverride)
	artifactStepID := stepID
	artifactStepPath := stepPath
	if artifactFolderNameOverride = strings.TrimSpace(artifactFolderNameOverride); artifactFolderNameOverride != "" {
		artifactStepID = ""
		artifactStepPath = artifactFolderNameOverride
	}

	// 2. Setup folder guard (extracted method). Empty kbAccess defaults to orchestrator-level UseKnowledgebase.
	kbAccess := resolveKnowledgebaseAccess(stepConfig, hcpo.UseKnowledgebase())
	learningsAccess := resolveExecutionLearningsAccess(stepConfig, planStep)
	dbAccess := resolveDBAccess(stepConfig)
	readPaths, writePaths := hcpo.setupExecutionFolderGuard(artifactStepPath, artifactStepID, kbAccess, learningsAccess, dbAccess, stepConfig)
	stepEnvOutputPathOverride := ""
	if override, ok := ctx.Value(messageSequenceFolderGuardOverrideKey{}).(*messageSequenceFolderGuardOverride); ok && override != nil {
		readPaths = append([]string{}, override.ReadPaths...)
		writePaths = append([]string{}, override.WritePaths...)
		if strings.TrimSpace(override.OutputPath) != "" {
			stepEnvOutputPathOverride = override.OutputPath
		}
		// The item-specific override may narrow files/KB/learnings, but DB is a
		// uniform workflow-step capability. Restore the workflow DB path instead
		// of silently downgrading the child and removing mutate_workflow_db.
		if !dbWritePathGranted(writePaths, hcpo.GetWorkspacePath()) {
			dbPath := getDBPath(hcpo.GetWorkspacePath())
			readPaths = common.DeduplicateStrings(append(readPaths, dbPath))
			writePaths = common.DeduplicateStrings(append(writePaths, dbPath))
		}
		hcpo.GetLogger().Info(fmt.Sprintf("🔒 Message sequence folder guard override for execution agent - Read: %v Write: %v", readPaths, writePaths))
	}
	readPaths, writePaths, _, _ = appendWorkflowFolderAccess(hcpo.GetWorkspacePath(), readPaths, writePaths, kbAccessAllowsRead(kbAccess))

	// Scripted code mode: add code/ subdir to the enforced write paths so the LLM can write main.py there.
	// writePaths[0] is the step execution folder (e.g. execution/step-1); appending /code gives execution/step-1/code.
	hcpo.GetLogger().Info(fmt.Sprintf("🐍 [scripted_code] stepConfig nil=%v scripted=%v", stepConfig == nil, isScriptedStep(planStep, stepConfig)))
	if isScriptedStep(planStep, stepConfig) {
		if len(writePaths) > 0 {
			codePath := writePaths[0] + "/code"
			writePaths = append(writePaths, codePath)
			hcpo.GetLogger().Info(fmt.Sprintf("🐍 [scripted_code] Enforced write paths now include code/: %v", writePaths))
		} else {
			hcpo.GetLogger().Warn("🐍 [scripted_code] writePaths is empty — cannot append code/ subdir to folder guard")
		}
	}

	// Add skill folder paths to read paths (skills are read-only)
	effectiveSkills := GetEffectiveSkills(stepConfig, hcpo.BaseOrchestrator)
	if len(effectiveSkills) > 0 {
		skillReadPaths, _ := BuildSkillFolderGuardPaths(effectiveSkills)
		readPaths = append(readPaths, skillReadPaths...)
		hcpo.GetLogger().Info(fmt.Sprintf("🎯 Added skill folder paths to folder guard: %v", skillReadPaths))
	}
	if err := hcpo.materializeWorkflowGuardPaths(readPaths, writePaths); err != nil {
		return nil, err
	}

	// NOTE: We no longer call hcpo.SetWorkspacePathForFolderGuard here.
	// Instead, readPaths/writePaths are set on the per-agent config below (config.FolderGuardReadPaths/WritePaths)
	// to prevent race conditions when parallel sub-agents share the same orchestrator instance.
	hcpo.GetLogger().Info(fmt.Sprintf("🔒 Per-agent folder guard for execution-only agent - Read paths: %v, Write paths: %v (can write to %s and execution/Downloads/)", readPaths, writePaths, artifactStepPath))

	// 3. Determine settings (extracted methods)
	isCodeExecutionMode := hcpo.getCodeExecutionMode(stepConfig)
	maxTurns := hcpo.GetMaxTurns()

	// 4. Select LLM (extracted method)
	llmConfig := hcpo.selectExecutionLLM(ctx, stepConfig, stepPath)
	if llmConfig == nil {
		return nil, fmt.Errorf("no valid LLM configuration found for execution agent: step config and tier/preset execution LLM are all empty or invalid")
	}

	// 4. Create config
	config := hcpo.CreateStandardAgentConfigWithLLM(agentName, maxTurns, agents.OutputFormatStructured, llmConfig)
	// Execution-only agents (plain execution steps, message_sequence steps, repair
	// agents, continuation recovery) are workflow steps like any other, so they
	// follow the same transport rule instead of being pinned to tmux. This path
	// previously never consulted the resolver at all — harmless while everything
	// was tmux, but it would silently diverge now that structured is the default.
	execTransport := hcpo.applyWorkflowTransportToAgentConfig(config, stepConfig, "execution-only agent")
	hcpo.publishWorkflowTransportContext(execTransport, executionModeLabel(isScriptedStep(planStep, stepConfig)))
	hcpo.disableParentAgentTimeout(config, "execution-only agent")

	// Execution-only steps can run in parallel inside a group. If they all reuse the
	// group MCP session, the session-level folder guard becomes last-writer-wins and one
	// step can end up executing another step's commands under the wrong write scope.
	// Give each execution step its own session-level guard, just like learning/KB agents.
	// Dedicated tool session for this execution step's shell/filesystem calls. Browser
	// reuse is re-bound separately below, so shell isolation does not imply browser isolation.
	execSessionID := ""
	if override, ok := ctx.Value(messageSequenceRuntimeSessionOverrideKey{}).(*messageSequenceRuntimeSessionOverride); ok && override != nil && strings.TrimSpace(override.SessionID) != "" {
		execSessionID = strings.TrimSpace(override.SessionID)
		hcpo.configureSubAgentSessionGuard(execSessionID, "message-sequence", stepID, readPaths, writePaths, kbAccessAllowsRead(kbAccess))
	} else {
		execSessionID = hcpo.setupSubAgentSessionGuard("exec", stepID, readPaths, writePaths, kbAccessAllowsRead(kbAccess))
	}
	config.MCPSessionID = execSessionID
	directDBAccess := isScriptedStep(planStep, stepConfig)
	configureWorkflowDBSession(execSessionID, hcpo.GetWorkspacePath(), dbAccess, directDBAccess)
	// Bind the per-step tool session to the workflow's browser session. Tool-session
	// isolation protects folder and DB permissions without creating a second browser.
	sharedBrowserSessionID := hcpo.resolveWorkshopBrowserSessionID(hcpo.currentGroupName)
	hcpo.bindWorkshopBrowserSession(execSessionID, sharedBrowserSessionID)

	// Set per-agent folder guard paths on config to avoid race conditions with parallel sub-agents.
	// These take precedence over the shared BaseOrchestrator.folderGuardReadPaths/WritePaths
	// in registerCustomToolsForAgent, ensuring each agent gets its own correct paths.
	config.FolderGuardReadPaths = readPaths
	config.FolderGuardWritePaths = writePaths

	// Setup Downloads folder for agent-browser.
	// Use shared function to ensure both execution and orchestrator agents set the override correctly
	hcpo.setupBrowserDownloadsPathOverride(ctx, config, stepConfig)

	// Apply step-specific overrides
	hcpo.applyStepConfigToAgentConfig(ctx, config, stepConfig, isCodeExecutionMode, isScriptedStep(planStep, stepConfig))
	if override, ok := ctx.Value(messageSequenceRuntimeSessionOverrideKey{}).(*messageSequenceRuntimeSessionOverride); ok && override != nil && override.KeepAlive && common.IsCLIProvider(config.LLMConfig.Primary.Provider) && !config.ForceStructuredCodingAgent {
		config.CodingAgentKeepAlive = true
		hcpo.GetLogger().Info(fmt.Sprintf("🔁 message_sequence runtime will keep coding-agent session alive: %s", config.MCPSessionID))
	}

	// Enable parallel tool execution for execution agents
	// This allows concurrent execution of multiple independent tool calls
	config.EnableParallelToolExecution = true
	hcpo.GetLogger().Info("⚡ Parallel tool execution enabled for execution-only agent")

	// Allow step config to override parallel tool execution
	if stepConfig != nil && stepConfig.DisableParallelToolExecution != nil && *stepConfig.DisableParallelToolExecution {
		config.EnableParallelToolExecution = false
		hcpo.GetLogger().Info("🔧 Parallel tool execution DISABLED for execution-only agent via step config")
	}

	// 5. Prepare custom tools (filtered by step config)
	toolsToRegister, executorsToUse := hcpo.prepareCustomTools(stepConfig)
	if dbAccess == DBAccessRead {
		filtered := toolsToRegister[:0]
		for _, tool := range toolsToRegister {
			if tool.Function != nil && (tool.Function.Name == "mutate_workflow_db" || tool.Function.Name == "apply_workflow_db_migration") {
				continue
			}
			filtered = append(filtered, tool)
		}
		toolsToRegister = filtered
		delete(executorsToUse, "mutate_workflow_db")
		delete(executorsToUse, "apply_workflow_db_migration")
	}
	// Inject STEP_OUTPUT_DIR and STEP_EXECUTION_DIR for all execution-only agents (both scripted and agentic).
	// Any script run via execute_shell_command may need STEP_OUTPUT_DIR to know where to write output
	// and STEP_EXECUTION_DIR to read sibling step outputs.
	{
		executionWorkspacePath := fmt.Sprintf("%s/runs/%s/execution", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
		stepExecutionPath := getExecutionFolderPath(executionWorkspacePath, artifactStepID, artifactStepPath)
		if stepEnvOutputPathOverride != "" {
			stepExecutionPath = stepEnvOutputPathOverride
		}
		stepOutputAbsPath := filepath.Join(GetPromptDocsRoot(), stepExecutionPath)
		if common.IsCLIProvider(config.LLMConfig.Primary.Provider) {
			config.CodingAgentOutputDir = stepOutputAbsPath
		}
		stepExecutionAbsPath := stepExecutionScopeAbsPath(stepOutputAbsPath)
		dbAbsPath := ""
		if directDBAccess {
			dbAbsPath = filepath.Join(GetPromptDocsRoot(), hcpo.GetWorkspacePath(), DBFolderName, "db.sqlite")
		}
		workspaceEnv := hcpo.codeRuntimeEnv(hcpo.snapshotWorkspaceEnv())
		workspaceEnv = appendScriptedDelegationEnv(ctx, workspaceEnv)
		if directDBAccess && hcpo.usesCodeTree() {
			common.SetSessionWorkingDir(config.MCPSessionID, hcpo.scriptedWorkingDir(stepID, stepExecutionPath))
		}
		registerStepSessionShellEnv(config.MCPSessionID, stepOutputAbsPath, stepExecutionAbsPath, dbAbsPath, hcpo.selectedRunFolder, workspaceEnv)
		injectStepEnvIntoShellExecutor(executorsToUse, stepOutputAbsPath, stepExecutionAbsPath, dbAbsPath, hcpo.selectedRunFolder, config.MCPSessionID, workspaceEnv)
		hcpo.GetLogger().Info(fmt.Sprintf("📂 Injecting step shell env into execute_shell_command for %s: STEP_OUTPUT_DIR=%s MCP_SESSION_ID=%s", stepID, stepOutputAbsPath, config.MCPSessionID))
	}

	// 6. Use base factory! (This handles all setup automatically)
	pathInfo := parseStepPath(stepPath)
	parentStepIndex := parentStepIndexFromContext(ctx, pathInfo.ParentStepNumber-1)
	if parentStepIndex < 0 {
		parentStepIndex = 0
	}
	agent, err := hcpo.CreateAndSetupStandardAgentWithConfig(
		ctx,
		config,
		phase,
		parentStepIndex,
		0,      // iteration
		stepID, // Step ID (resolved from step path)
		func(cfg *agents.OrchestratorAgentConfig, logger loggerv2.Logger, tracer observability.Tracer, eventBridge mcpagent.AgentEventListener) agents.OrchestratorAgent {
			return NewWorkflowExecutionOnlyAgent(cfg, logger, tracer, eventBridge)
		},
		toolsToRegister,
		executorsToUse,
		false, // overwriteSystemPrompt
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create and setup execution-only agent: %w", err)
	}

	// 7. Post-setup: folder guard (after base factory setup)
	// Note: Base factory already updates code execution registry, but we need to set folder guard paths
	// on mcpAgent first, then update registry again with correct paths
	baseAgent := agent.GetBaseAgent()
	if baseAgent == nil {
		return nil, fmt.Errorf("base agent is nil after creation for %s - this should never happen", agentName)
	}
	// Inject supplementary prompts (skills, secrets, browser instructions)
	attachGlobalLearnings := learningsAccess != LearningsAccessNone
	hcpo.appendSupplementaryPrompts(ctx, baseAgent, config, effectiveSkills, attachGlobalLearnings, registeredToolNames(toolsToRegister), isScriptedStep(planStep, stepConfig))

	// Apply post-setup configuration (folder guard paths and optional registry update)
	if err := hcpo.applyPostSetupToAgent(agent, agentName); err != nil {
		// Log warning but don't fail agent creation
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Post-setup configuration failed for %s: %v", agentName, err))
	}

	return agent, nil
}

// ConversationEntry is a single flattened message in the sub-agent's conversation
type ConversationEntry struct {
	Index    int    `json:"index"`
	Role     string `json:"role"`              // "user", "assistant", "tool_call", "tool_result"
	Content  string `json:"content,omitempty"` // text content
	ToolName string `json:"tool_name,omitempty"`
	ToolArgs string `json:"tool_args,omitempty"`
}

// SubAgentCallRecord stores the full record of one predefined/scripted/generic sub-agent call.
type SubAgentCallRecord struct {
	Index         int                 `json:"index"` // 1-based call order
	ExecutionID   string              `json:"execution_id"`
	CalledAt      time.Time           `json:"called_at"`
	TodoID        string              `json:"task_id"`
	RouteID       string              `json:"route_id,omitempty"` // empty for generic
	AgentType     string              `json:"agent_type"`         // "predefined" | "generic"
	Success       bool                `json:"success"`
	Result        string              `json:"result"`
	Error         string              `json:"error,omitempty"`
	ExecutionTime string              `json:"execution_time"`
	Conversation  []ConversationEntry `json:"conversation"`
}

func subAgentConversationPage(records []SubAgentCallRecord, executionID string, fromLastX, offsetLastX int) (string, error) {
	executionID = strings.TrimSpace(executionID)
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].ExecutionID != executionID {
			continue
		}
		record := records[i]
		conv := record.Conversation
		total := len(conv)
		end := total - offsetLastX
		if end < 0 {
			end = 0
		}
		start := end - fromLastX
		if start < 0 {
			start = 0
		}
		trimmed := record
		trimmed.Conversation = conv[start:end]
		type resultWrapper struct {
			TotalEntries int                `json:"total_entries"`
			Showing      string             `json:"showing"`
			Record       SubAgentCallRecord `json:"record"`
		}
		out := resultWrapper{
			TotalEntries: total,
			Showing:      fmt.Sprintf("entries %d-%d of %d", start+1, end, total),
			Record:       trimmed,
		}
		data, _ := json.MarshalIndent(out, "", "  ")
		return string(data), nil
	}
	return "", fmt.Errorf("no sub-agent call found for execution_id %q", executionID)
}

// SubAgentExecutionContext holds the context needed for sub-agent execution from tools
type SubAgentExecutionContext struct {
	OrchestratorStep *OrchestratorPlanStep
	StepIndex        int
	StepPath         string
	AllSteps         []PlanStepInterface
	Progress         *StepProgress
	StepConfig       *AgentConfigs // Step-level configuration for LLM overrides

	// HumanInputs is the run_full_workflow human_inputs map, unscoped. A route
	// dispatch (executePredefinedSubAgent) looks up its own entry by the
	// dispatched step's ID. Without this, only the todo_task step's OWN entry
	// (keyed by its own ID) ever reached a prompt — a caller supplying guidance
	// keyed by a ROUTE's ID, e.g. human_inputs={"infographic-research": "..."},
	// had it silently dropped: no error, and the route ran with no idea it existed.
	HumanInputs map[string]string

	// TierSelectionRequired tells the sub-agent tool handlers to reject calls that
	// don't include a valid preferred_tier. Mirrors the enableTierSelection flag
	// used when building the tool schema.
	TierSelectionRequired bool

	// WorkshopCorrelationID is the correlation ID from the workshop's execute_step call.
	// Propagated to sub-agent contexts so their events are tagged with the workshop step's ID.
	WorkshopCorrelationID string
	// ParentContext owns every asynchronously launched child. It comes from the
	// workflow execution, not the detached HTTP/MCP request used to invoke the
	// custom tool, so stopping the workflow cancels its children deterministically.
	ParentContext context.Context
	// AsyncEnabled makes all sub-agent call tools return immediately and
	// lets the controller reconcile owned children outside the provider tool call.
	// Saved scripted fast paths remain synchronous because their scripts consume
	// the tool result in the same process invocation.
	AsyncEnabled bool
	// ToolSessionID identifies the session-scoped custom-tool registry owned by
	// this orchestrator. Nested todo_task orchestrators receive a distinct ID so
	// their call/query/stop handlers cannot replace the parent's handlers.
	ToolSessionID string

	// CallHistory records every sub-agent call made during this todo task step.
	// Protected by callHistoryMu for concurrent tool calls.
	CallHistory   []SubAgentCallRecord
	callHistoryMu sync.Mutex

	// Async calls return an execution ID to the LLM immediately. The controller
	// waits for these calls outside the provider tool request, then feeds one
	// completion batch back into the same orchestrator conversation.
	asyncCalls map[string]*asyncSubAgentCall
	asyncOrder []string
	asyncMu    sync.Mutex
}

// serializeConversationHistory converts raw llmtypes conversation history into a flat list of ConversationEntry
func serializeConversationHistory(history []llmtypes.MessageContent) []ConversationEntry {
	var entries []ConversationEntry
	for i, msg := range history {
		for _, part := range msg.Parts {
			entry := ConversationEntry{Index: i + 1}
			switch msg.Role {
			case llmtypes.ChatMessageTypeHuman:
				entry.Role = "user"
				if tc, ok := part.(llmtypes.TextContent); ok {
					entry.Content = tc.Text
				}
			case llmtypes.ChatMessageTypeAI:
				if tc, ok := part.(llmtypes.TextContent); ok {
					entry.Role = "assistant"
					entry.Content = tc.Text
				} else if tc, ok := part.(llmtypes.ToolCall); ok && tc.FunctionCall != nil {
					entry.Role = "tool_call"
					entry.ToolName = tc.FunctionCall.Name
					entry.ToolArgs = tc.FunctionCall.Arguments
				}
			case llmtypes.ChatMessageTypeTool:
				if tc, ok := part.(llmtypes.ToolCallResponse); ok {
					entry.Role = "tool_result"
					entry.ToolName = tc.Name
					entry.Content = tc.Content
				}
			}
			if entry.Role != "" {
				entries = append(entries, entry)
			}
		}
	}
	return entries
}

// createOrchestratorAgent creates a todo task orchestrator agent using the standard factory pattern
// This agent manages todo lists, creates tasks, and delegates to predefined or generic sub-agents
// Note: Folder guard paths should be set by the caller before calling this function (see controller_todo_task.go)
// The stepPath parameter is used to inject context for todo tools (e.g., "step-1")
// The subAgentExecCtx contains context for sub-agent tool execution (can be nil for simple cases)
func (hcpo *StepBasedWorkflowOrchestrator) createOrchestratorAgent(ctx context.Context, phase string, step, iteration int, stepID string, stepPath string, agentName string, stepConfig *AgentConfigs, orchestratorStepLLMConfig *orchestrator.LLMConfig, subAgentExecCtx *SubAgentExecutionContext) (agents.OrchestratorAgent, error) {
	// Todo task orchestrator agent needs folder guard (can write files)
	// Note: Folder guard is set by caller in controller_todo_task.go before agent creation
	// We apply it to the agent here via post-setup

	// Determine max turns: use orchestrator default
	maxTurns := hcpo.GetMaxTurns()

	// Determine LLM config: Priority: step config > preset default
	var llmConfig *orchestrator.LLMConfig
	orchestratorLLMConfig := hcpo.GetLLMConfig()
	if orchestratorStepLLMConfig != nil && orchestratorStepLLMConfig.Primary.Provider != "" && orchestratorStepLLMConfig.Primary.ModelID != "" {
		var apiKeys *orchestrator.APIKeys
		if orchestratorLLMConfig != nil {
			apiKeys = orchestratorLLMConfig.APIKeys
		}
		llmConfig = &orchestrator.LLMConfig{
			Primary: orchestrator.LLMModel{
				Provider:     orchestratorStepLLMConfig.Primary.Provider,
				ModelID:      orchestratorStepLLMConfig.Primary.ModelID,
				Options:      orchestratorStepLLMConfig.Primary.Options,
				ConnectionID: orchestratorStepLLMConfig.Primary.ConnectionID,
			},
			APIKeys: apiKeys, // Preserve API keys from orchestrator (may be nil)
		}
		hcpo.GetLogger().Info(fmt.Sprintf("🔧 Using step-specific todo task orchestrator LLM: %s/%s", orchestratorStepLLMConfig.Primary.Provider, orchestratorStepLLMConfig.Primary.ModelID))
	} else if hcpo.tierResolver != nil {
		// Use tiered allocation (high tier for orchestration)
		tieredLLM := hcpo.tierResolver.ResolveTier(TierHigh)
		if tieredLLM != nil {
			llmConfig = tieredLLM
			hcpo.GetLogger().Info(fmt.Sprintf("🏷️ Using Tier 1 (High) for todo task orchestrator: %s/%s", tieredLLM.Primary.Provider, tieredLLM.Primary.ModelID))
		}
	}
	if llmConfig == nil && hcpo.presetPhaseLLM != nil && hcpo.presetPhaseLLM.Provider != "" && hcpo.presetPhaseLLM.ModelID != "" {
		llmConfig = &orchestrator.LLMConfig{
			Primary: orchestrator.LLMModel{
				Provider:     hcpo.presetPhaseLLM.Provider,
				ModelID:      hcpo.presetPhaseLLM.ModelID,
				Options:      hcpo.presetPhaseLLM.Options,
				ConnectionID: hcpo.presetPhaseLLM.ConnectionID,
			},
			APIKeys: orchestratorLLMConfig.APIKeys, // Preserve API keys from orchestrator
		}
		hcpo.GetLogger().Info(fmt.Sprintf("🔧 Using preset phase LLM for todo task orchestrator: %s/%s", hcpo.presetPhaseLLM.Provider, hcpo.presetPhaseLLM.ModelID))
	}
	if llmConfig == nil {
		return nil, fmt.Errorf("no valid LLM configuration found for todo task orchestrator agent: step config, tiered, and preset phase LLM are all empty or invalid")
	}

	// Create agent config with custom LLM if needed
	config := hcpo.CreateStandardAgentConfigWithLLM(agentName, maxTurns, agents.OutputFormatStructured, llmConfig)
	effectiveTransport := hcpo.applyWorkflowTransportToAgentConfig(config, stepConfig, "todo task orchestrator agent")
	hcpo.publishWorkflowTransportContext(effectiveTransport, StepModeAgentic)
	hcpo.disableParentAgentTimeout(config, "todo task orchestrator agent")

	// Run the coding-CLI session in a fresh os.MkdirTemp dir instead of
	// CodingAgentWorkingDir — same protection the regular execution-step
	// path gets via applyStepConfigToAgentConfig. Without this, a
	// todo-task orchestrator agent collides with any other coding-CLI
	// session already attached to the workflow folder (notably the
	// workshop chat that triggered the run): some coding CLIs reject
	// concurrent sessions on the same dir with different MCP configs,
	// failing the step with a "does not support concurrent sessions in
	// working directory ... with different MCP configs"-style error. The
	// MCP bridge remains the authoritative path for any file changes the
	// model wants to make to the user's actual workspace.
	config.IsolateCodingAgentWorkspace = true

	// Give nested todo_task orchestrators their own session-level folder guard just like
	// normal execution steps. Without this, shell calls fall back to the broader parent
	// workflow/group MCP session and can see workflow-root files or sibling groups.
	todoReadPaths, todoWritePaths := hcpo.GetFolderGuardPaths()
	todoSessionID := hcpo.setupSubAgentSessionGuard("todo", stepID, todoReadPaths, todoWritePaths)
	config.MCPSessionID = todoSessionID
	dbAccess := resolveDBAccess(stepConfig)
	// An orchestrator step is never scripted (its job is runtime delegation),
	// so it never gets the scripted executor's direct DB path.
	directDBAccess := false
	configureWorkflowDBSession(todoSessionID, hcpo.GetWorkspacePath(), dbAccess, directDBAccess)
	if subAgentExecCtx != nil {
		subAgentExecCtx.ToolSessionID = todoSessionID
	}
	sharedBrowserSessionID := hcpo.resolveWorkshopBrowserSessionID(hcpo.currentGroupName)
	hcpo.bindWorkshopBrowserSession(todoSessionID, sharedBrowserSessionID)
	config.FolderGuardReadPaths = todoReadPaths
	config.FolderGuardWritePaths = todoWritePaths

	workflowServersTodo := hcpo.GetSelectedServers()
	// Use step-specific servers filtered against workflow-level servers (workflow is the hard cap)
	if stepConfig != nil && stepConfig.SelectedServers != nil && len(stepConfig.SelectedServers) > 0 {
		filtered := filterServersByWorkflow(stepConfig.SelectedServers, workflowServersTodo)
		config.ServerNames = filtered
		hcpo.GetLogger().Info(fmt.Sprintf("🔧 Using step-specific todo task orchestrator servers (workflow-filtered): %v → %v", stepConfig.SelectedServers, filtered))
	} else {
		// Use orchestrator defaults when stepConfig is nil or SelectedServers is empty
		config.ServerNames = normalizeServerNames(workflowServersTodo)
		if stepConfig != nil {
			hcpo.GetLogger().Info(fmt.Sprintf("🔧 Step config found but SelectedServers is empty - using orchestrator defaults: %v", config.ServerNames))
		} else {
			hcpo.GetLogger().Info(fmt.Sprintf("🔧 Step config not found - using orchestrator defaults: %v", config.ServerNames))
		}
	}
	hcpo.addPlaceMCPServers(ctx, config)
	if stepConfig != nil && len(stepConfig.SelectedTools) > 0 {
		filtered := filterToolsByWorkflow(stepConfig.SelectedTools, workflowServersTodo)
		config.SelectedTools = filtered
		hcpo.GetLogger().Info(fmt.Sprintf("🔧 Using step-specific todo task orchestrator tools (workflow-filtered): %v → %v", stepConfig.SelectedTools, filtered))
	} else {
		config.SelectedTools = hcpo.GetSelectedTools()
		hcpo.GetLogger().Info(fmt.Sprintf("🔧 Using orchestrator default todo task orchestrator tools: %v", config.SelectedTools))
	}

	// Enable code execution mode for CLI providers that need HTTP bridge tool routing.
	// Non-CLI providers use simple agent mode (no code execution)
	isCodeExecutionMode := common.IsCLIProvider(llmConfig.Primary.Provider)
	config.UseCodeExecutionMode = isCodeExecutionMode
	if isCodeExecutionMode {
		hcpo.GetLogger().Info(fmt.Sprintf("🔧 Todo task orchestrator: code execution mode enabled for CLI provider '%s'", llmConfig.Primary.Provider))
	}

	// Enable parallel tool execution for todo task orchestrator
	// This allows concurrent execution of multiple tool calls (e.g., call_sub_agent, call_generic_agent)
	config.EnableParallelToolExecution = true
	hcpo.GetLogger().Info("⚡ Parallel tool execution enabled for todo task orchestrator agent")

	// Setup Downloads folder for agent-browser.
	hcpo.setupBrowserDownloadsPathOverride(ctx, config, stepConfig)

	// Start with the exact same default custom-tool policy as every other
	// message-sequence agent. Specialist tools are the only conditional overlay.
	toolsToRegister, executorsToUse := hcpo.prepareCustomTools(stepConfig)
	// DB capability tools are always derived from trusted db_access even when a
	// step supplies a narrower custom-tool list.
	hasTool := func(name string) bool {
		for _, tool := range toolsToRegister {
			if tool.Function != nil && tool.Function.Name == name {
				return true
			}
		}
		return false
	}
	for _, name := range []string{"query_workflow_db", "mutate_workflow_db", "apply_workflow_db_migration"} {
		if (name == "mutate_workflow_db" || name == "apply_workflow_db_migration") && dbAccess == DBAccessRead {
			continue
		}
		if !hasTool(name) {
			for _, tool := range hcpo.WorkspaceTools {
				if tool.Function != nil && tool.Function.Name == name {
					toolsToRegister = append(toolsToRegister, tool)
					if executor, ok := hcpo.WorkspaceToolExecutors[name]; ok {
						executorsToUse[name] = executor
					}
					break
				}
			}
		}
	}
	if dbAccess == DBAccessRead {
		filtered := toolsToRegister[:0]
		for _, tool := range toolsToRegister {
			if tool.Function != nil && (tool.Function.Name == "mutate_workflow_db" || tool.Function.Name == "apply_workflow_db_migration") {
				continue
			}
			filtered = append(filtered, tool)
		}
		toolsToRegister = filtered
		delete(executorsToUse, "mutate_workflow_db")
		delete(executorsToUse, "apply_workflow_db_migration")
	}

	// Inject STEP_OUTPUT_DIR and STEP_EXECUTION_DIR into execute_shell_command so the
	// todo-task orchestrator's own shell calls resolve sibling step outputs via env vars
	// rather than having to rebuild absolute paths from the step context.
	{
		stepExecutionRelPath := hcpo.getOrchestratorStepExecutionPath(stepID, stepPath)
		stepOutputAbsPath := filepath.Join(GetPromptDocsRoot(), stepExecutionRelPath)
		if common.IsCLIProvider(config.LLMConfig.Primary.Provider) {
			config.CodingAgentOutputDir = stepOutputAbsPath
		}
		stepExecutionAbsPath := stepExecutionScopeAbsPath(stepOutputAbsPath)
		// The todo-task orchestrator now uses a dedicated MCP session for shell/file tools.
		// Browser reuse is bound separately above, so this session override narrows
		// filesystem scope without breaking shared browser behavior with the builder.
		dbAbsPath := ""
		if directDBAccess {
			dbAbsPath = filepath.Join(GetPromptDocsRoot(), hcpo.GetWorkspacePath(), DBFolderName, "db.sqlite")
		}
		workspaceEnv := hcpo.codeRuntimeEnv(hcpo.snapshotWorkspaceEnv())
		registerStepSessionShellEnv(config.MCPSessionID, stepOutputAbsPath, stepExecutionAbsPath, dbAbsPath, hcpo.selectedRunFolder, workspaceEnv)
		injectStepEnvIntoShellExecutor(executorsToUse, stepOutputAbsPath, stepExecutionAbsPath, dbAbsPath, hcpo.selectedRunFolder, config.MCPSessionID, workspaceEnv)
		hcpo.GetLogger().Info(fmt.Sprintf("📂 Injecting step shell env into execute_shell_command for todo task %s: STEP_OUTPUT_DIR=%s MCP_SESSION_ID=%s", stepID, stepOutputAbsPath, config.MCPSessionID))
	}

	// NOTE: Task management is handled directly by the orchestrator LLM via shell commands

	// Filter out human tools if "no human" execution mode is active
	execOpts := hcpo.GetExecutionOptions()
	if execOpts != nil && (execOpts.ExecutionStrategy == ExecutionStrategyStartFromBeginningNoHuman || execOpts.ExecutionStrategy == ExecutionStrategyResumeFromStepNoHuman) {
		var filteredTools []llmtypes.Tool
		filteredExecutors := make(map[string]interface{})

		for _, tool := range toolsToRegister {
			// Check if this tool is a human tool by looking at its category
			if hcpo.ToolCategories != nil {
				if category, exists := hcpo.ToolCategories[tool.Function.Name]; exists && virtualtools.IsHumanToolCategory(category) {
					// Skip human tools in "no human" mode
					hcpo.GetLogger().Info(fmt.Sprintf("🔧 Excluding human tool '%s' from todo task orchestrator agent (no human mode)", tool.Function.Name))
					continue
				}
			}
			filteredTools = append(filteredTools, tool)
			// Also filter executors
			if executor, exists := executorsToUse[tool.Function.Name]; exists {
				filteredExecutors[tool.Function.Name] = executor
			}
		}

		toolsToRegister = filteredTools
		executorsToUse = filteredExecutors
		hcpo.GetLogger().Info(fmt.Sprintf("🔧 Filtered out human tools for todo task orchestrator agent (no human mode): %d tools remaining", len(toolsToRegister)))
	}

	// IMPORTANT: Inject sub-agent tools for tool-based delegation
	// These tools allow the orchestrator to delegate work to sub-agents directly via tool calls
	if subAgentExecCtx != nil {
		// Tier selection is always required on every sub-agent call. The orchestrator
		// must reason about task difficulty per delegation — this is prompt-discipline
		// even when the workflow has no tier resolver or the step pins an ExecutionLLM.
		// In those cases the tier value is honored by the sub-agent LLM-selection path
		// if possible, else silently falls through to the inherited/pinned LLM.
		subAgentExecCtx.TierSelectionRequired = true
		subAgentTools := virtualtools.CreateSubAgentTools()
		subAgentExecutors := virtualtools.CreateSubAgentToolExecutors()
		subAgentCategory := virtualtools.GetSubAgentToolCategory()

		// Categories for built-in delegation tools are registered once when the
		// BaseOrchestrator is constructed; the map stays immutable so nested
		// orchestrators can be created in parallel safely.
		for _, tool := range subAgentTools {
			toolsToRegister = append(toolsToRegister, tool)
			hcpo.GetLogger().Info(fmt.Sprintf("🔧 Added sub-agent tool '%s' to todo task orchestrator (category: %s)", tool.Function.Name, subAgentCategory))
		}

		// Wrap sub-agent executors with context injection
		for toolName, executor := range subAgentExecutors {
			wrappedExecutor := hcpo.wrapSubAgentToolExecutor(executor, subAgentExecCtx)
			executorsToUse[toolName] = wrappedExecutor
			hcpo.GetLogger().Info(fmt.Sprintf("🔧 Wrapped sub-agent tool '%s' with execution context injection", toolName))
		}
	} else {
		hcpo.GetLogger().Info("🔧 Sub-agent execution context not provided - sub-agent tools will not be available")
	}

	// NOTE: mark_step_complete tool removed — completion is detected by pre-validation.

	// Use standard factory pattern - this handles initialization, event bridge connection, and tool registration
	agent, err := hcpo.CreateAndSetupStandardAgentWithConfig(
		ctx,
		config,
		phase,
		step,
		iteration,
		stepID, // Step ID
		func(cfg *agents.OrchestratorAgentConfig, logger loggerv2.Logger, tracer observability.Tracer, eventBridge mcpagent.AgentEventListener) agents.OrchestratorAgent {
			return NewWorkflowDelegatingAgent(cfg, logger, tracer, eventBridge)
		},
		toolsToRegister, // Pass workspace tools (filtered by step config if specified)
		executorsToUse,  // Pass workspace tool executors
		false,           // Don't overwrite system prompt - todo task orchestrator agent manages its own prompt
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create todo task orchestrator agent: %w", err)
	}

	// Post-setup: folder guard paths (todo task orchestrator agent may use code execution mode, so registry update may be needed)
	// Note: Folder guard paths are already set on orchestrator by caller, but we need to apply them to the agent
	if err := hcpo.applyPostSetupToAgent(agent, agentName); err != nil {
		return nil, fmt.Errorf("failed to apply post-setup to todo task orchestrator agent: %w", err)
	}

	// Inject supplementary prompts (skills, secrets, browser instructions).
	effectiveSkills := GetEffectiveSkills(stepConfig, hcpo.BaseOrchestrator)
	if baseAgent := agent.GetBaseAgent(); baseAgent != nil {
		if baseAgent.Agent() != nil {
			attachGlobalLearnings := resolveLearningsAccess(stepConfig) != LearningsAccessNone
			// An orchestrator step is never scripted (PLAT-287).
			hcpo.appendSupplementaryPrompts(ctx, baseAgent, config, effectiveSkills, attachGlobalLearnings, registeredToolNames(toolsToRegister), false)
			if inherited := backgroundAgentSkillsFromContext(ctx); len(inherited) > 0 {
				if err := applyInheritedBackgroundSkills(ctx, baseAgent, inherited); err != nil {
					return nil, fmt.Errorf("apply inherited background orchestrator skills: %w", err)
				}
				hcpo.GetLogger().Info(fmt.Sprintf("🎯 Inherited %d workflow-builder skill(s) for background todo task", len(backgroundSkillNames(inherited))))
			}
		}
	}

	hcpo.GetLogger().Info(fmt.Sprintf("✅ Created todo task orchestrator agent using standard factory pattern: %s (step %d, phase %s)", agentName, step+1, phase))
	return agent, nil
}

// restoreSubAgentToolExecutors re-registers a todo_task's handlers in that
// orchestrator's own session-scoped registry. Nested orchestrators use distinct
// ToolSessionIDs, so restoring one level cannot replace another level's routes.
func (hcpo *StepBasedWorkflowOrchestrator) restoreSubAgentToolExecutors(execCtx *SubAgentExecutionContext) {
	if execCtx == nil {
		return
	}
	sessionID := strings.TrimSpace(execCtx.ToolSessionID)
	if sessionID == "" {
		sessionID = hcpo.getSessionID()
	}
	if sessionID == "" {
		return
	}
	subAgentExecutors := virtualtools.CreateSubAgentToolExecutors()
	wrappedExecutors := make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error), len(subAgentExecutors))
	for toolName, executor := range subAgentExecutors {
		wrappedExecutors[toolName] = hcpo.wrapSubAgentToolExecutor(executor, execCtx)
	}
	codeexec.InitRegistryForSession(sessionID, wrappedExecutors, hcpo.GetLogger())
	hcpo.GetLogger().Info(fmt.Sprintf("🔄 Restored sub-agent tool executors in orchestrator session %s", sessionID))
}

// wrapSubAgentToolExecutor wraps a sub-agent tool executor to inject execution functions
// The wrapper adds: execute_predefined_sub_agent, execute_generic_agent, predefined_routes, sub_agent_llm
func (hcpo *StepBasedWorkflowOrchestrator) wrapSubAgentToolExecutor(
	originalExecutor func(ctx context.Context, args map[string]interface{}) (string, error),
	execCtx *SubAgentExecutionContext,
) func(ctx context.Context, args map[string]interface{}) (string, error) {
	// Return wrapper function that injects execution functions into context
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		// Signal to handlers that preferred_tier must be supplied when dynamic tier
		// selection is active for this orchestrator.
		if execCtx.TierSelectionRequired {
			ctx = context.WithValue(ctx, virtualtools.TierSelectionRequiredKey, true)
		}

		// Inject execute_predefined_sub_agent function
		executePredefinedFunc := hcpo.createExecutePredefinedSubAgentFunc(execCtx)
		ctx = context.WithValue(ctx, virtualtools.ExecutePredefinedSubAgentKey, executePredefinedFunc)

		// Inject execute_generic_agent function
		executeGenericFunc := hcpo.createExecuteGenericAgentFunc(execCtx)
		ctx = context.WithValue(ctx, virtualtools.ExecuteGenericAgentKey, executeGenericFunc)

		ctx = context.WithValue(ctx, virtualtools.QuerySubAgentKey, virtualtools.QuerySubAgentFunc(
			func(_ context.Context, executionID string) (string, error) {
				return execCtx.queryAsyncCall(executionID)
			},
		))
		ctx = context.WithValue(ctx, virtualtools.StopSubAgentKey, virtualtools.StopSubAgentFunc(
			func(_ context.Context, executionID string) (string, error) {
				return execCtx.stopAsyncCall(executionID)
			},
		))

		// Inject predefined routes for route lookup
		if execCtx.OrchestratorStep != nil {
			ctx = context.WithValue(ctx, virtualtools.PredefinedRoutesKey, execCtx.OrchestratorStep.PredefinedRoutes)

			// Build route descriptions map for get_route_description tool
			routeDescriptions := make(map[string]string)
			for _, route := range execCtx.OrchestratorStep.PredefinedRoutes {
				desc := ResolveVariables(route.Condition, hcpo.variableValues)
				if route.SubAgentStep != nil {
					desc += "\n\nRoute type: " + routeStepTypeSummary(route.SubAgentStep)
					desc += "\nBehavior: " + routeStepBehaviorDetails(route.SubAgentStep)
					desc += "\n\nDescription: " + ResolveVariables(route.SubAgentStep.GetDescription(), hcpo.variableValues)
					if contract := formatScriptParameterContract(scriptedParameterDefinitions(route.SubAgentStep)); contract != "" {
						desc += "\n\nScript parameters (pass these as call_scripted_sub_agent.parameters):\n" + contract
					}
					if sequenceRouteInfo := formatMessageSequenceRoutePromptBlock(route.SubAgentStep); sequenceRouteInfo != "" {
						desc += "\n\n" + sequenceRouteInfo
					}
				}
				routeDescriptions[route.RouteID] = desc
			}
			ctx = context.WithValue(ctx, virtualtools.RouteDescriptionsKey, routeDescriptions)
		}

		// Inject the parent step's ExecutionLLM as the sub-agent LLM override so every
		// sub-agent spawned by this todo-task orchestrator uses the same LLM as the
		// orchestrator itself. Works in both tiered and manual modes; skipped for
		// dynamic tier selection at the consumer side.
		if execCtx.StepConfig != nil && execCtx.StepConfig.ExecutionLLM != nil {
			ctx = context.WithValue(ctx, virtualtools.SubAgentLLMContextKey, execCtx.StepConfig.ExecutionLLM)
		}

		// Inject get_sub_agent_conversation function
		getConvFunc := virtualtools.GetSubAgentConversationFunc(func(ctx context.Context, executionID string, fromLastX, offsetLastX int) (string, error) {
			execCtx.callHistoryMu.Lock()
			defer execCtx.callHistoryMu.Unlock()
			return subAgentConversationPage(execCtx.CallHistory, executionID, fromLastX, offsetLastX)
		})
		ctx = context.WithValue(ctx, virtualtools.GetSubAgentConversationKey, getConvFunc)

		// Only emit task status updates for tools that change state (sub-agent calls),
		// Call original executor with enriched context
		result, err := originalExecutor(ctx, args)

		return result, err
	}
}

// createExecutePredefinedSubAgentFunc creates a function that executes predefined sub-agents
// This function is injected into context for both predefined-route call tools.
func (hcpo *StepBasedWorkflowOrchestrator) createExecutePredefinedSubAgentSyncFunc(
	execCtx *SubAgentExecutionContext,
) virtualtools.ExecutePredefinedSubAgentFunc {
	return func(ctx context.Context, routeID, todoID, instructions string) (string, error) {
		hcpo.GetLogger().Info(fmt.Sprintf("🤖 [TOOL] Executing predefined sub-agent via tool: route=%s, todo=%s", routeID, todoID))

		if strings.TrimSpace(routeID) == "" {
			return "", fmt.Errorf("invalid or missing route_id")
		}
		if execCtx.OrchestratorStep == nil {
			return "", fmt.Errorf("call_sub_agent is only available inside a todo_task step")
		}
		validRouteIDs := make([]string, 0, len(execCtx.OrchestratorStep.PredefinedRoutes))
		var selectedRoute *PlanOrchestrationRoute
		for _, route := range execCtx.OrchestratorStep.PredefinedRoutes {
			validRouteIDs = append(validRouteIDs, route.RouteID)
			if route.RouteID == routeID {
				routeCopy := route
				selectedRoute = &routeCopy
			}
		}
		if selectedRoute == nil {
			return "", fmt.Errorf("route_id %q not found in todo task step %q. Available route IDs: %v", routeID, execCtx.OrchestratorStep.GetID(), validRouteIDs)
		}
		parameters := virtualtools.SubAgentParametersFromContext(ctx)
		isScriptedRoute := selectedRoute.SubAgentStep != nil && isScriptedStep(selectedRoute.SubAgentStep, getAgentConfigs(selectedRoute.SubAgentStep))
		isScriptedCall := virtualtools.IsScriptedSubAgentInvocation(ctx)
		if isScriptedRoute && isScriptedCall {
			resolved, paramErr := validateAndResolveScriptParameters(selectedRoute.SubAgentStep, parameters)
			if paramErr != nil {
				return "", paramErr
			}
			parameters = resolved
		} else if isScriptedRoute {
			// Compatibility only: older scripted routes without a parameter contract
			// were invoked through call_sub_agent with free-form instructions. New or
			// parameterized routes must use the dedicated typed tool.
			if len(scriptedParameterDefinitions(selectedRoute.SubAgentStep)) > 0 {
				return "", fmt.Errorf("scripted route %q declares script_parameters and must be invoked with call_scripted_sub_agent", routeID)
			}
		} else if isScriptedCall {
			return "", fmt.Errorf("route %q is %s, not scripted; use call_sub_agent with instructions", routeID, routeStepTypeSummary(selectedRoute.SubAgentStep))
		} else if strings.TrimSpace(instructions) == "" {
			return "", fmt.Errorf("instructions are required for non-scripted route %q", routeID)
		}

		// Propagate workshop correlation IDs to sub-agent context so events are tagged correctly.
		// The ctx here comes from the tool call (mcpagent), which may not have the workshop's
		// ForceCorrelationIDKey. Use the workshop correlation ID from SubAgentExecutionContext.
		if execCtx.WorkshopCorrelationID != "" {
			if forcedID, ok := ctx.Value(orchestrator_events.ForceCorrelationIDKey).(string); !ok || forcedID == "" {
				ctx = context.WithValue(ctx, orchestrator_events.ForceCorrelationIDKey, execCtx.WorkshopCorrelationID)
				ctx = context.WithValue(ctx, orchestrator_events.IsSubAgentContextKey, true)
			}
		}

		// Build a OrchestratorDecision to reuse existing execution logic
		response := &OrchestratorDecision{
			NextAction:             "delegate",
			SelectedRouteID:        routeID,
			TodoIDToExecute:        todoID,
			InstructionsToSubAgent: instructions,
			ScriptParameters:       parameters,
		}

		// Emit route selected event BEFORE sub-agent execution so it appears before the agent card
		hcpo.emitOrchestratorRouteSelectedEvent(ctx, execCtx.OrchestratorStep, execCtx.StepIndex, execCtx.StepPath, 0, response, "")

		startTime := time.Now()

		// Execute using existing method
		result, history, err := hcpo.executePredefinedSubAgent(
			ctx,
			execCtx.OrchestratorStep,
			execCtx.StepIndex,
			execCtx.StepPath,
			response,
			execCtx.AllSteps,
			execCtx.Progress,
			execCtx.HumanInputs,
		)

		executionTime := time.Since(startTime)

		// RESTORE: Re-register outer sub-agent executors in the session registry.
		// A nested todo_task sub-agent overwrites the session-scoped call_sub_agent executor
		// with its own inner routes. After it returns, restore the outer executor so that
		// subsequent call_sub_agent calls in the same LLM turn (code execution mode) hit the
		// correct outer routes and not the inner ones.
		hcpo.restoreSubAgentToolExecutors(execCtx)

		// Store call record for get_sub_agent_conversation
		record := SubAgentCallRecord{
			CalledAt:      startTime,
			ExecutionID:   asyncSubAgentExecutionID(ctx),
			TodoID:        todoID,
			RouteID:       routeID,
			AgentType:     "predefined",
			Success:       err == nil,
			Result:        result,
			ExecutionTime: executionTime.String(),
			Conversation:  serializeConversationHistory(history),
		}
		if err != nil {
			record.Error = err.Error()
		}
		execCtx.callHistoryMu.Lock()
		record.Index = len(execCtx.CallHistory) + 1
		execCtx.CallHistory = append(execCtx.CallHistory, record)
		execCtx.callHistoryMu.Unlock()

		if err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [TOOL] Predefined sub-agent execution failed: %v", err))
			// Preserve the typed failure here. The virtual-tool boundary turns it
			// into a success:false payload for a synchronous caller, while the
			// async owner records a failed completion. Converting it to ERROR text
			// plus nil made failed children look completed (PLAT-082).
			return result, err
		}

		hcpo.GetLogger().Info(fmt.Sprintf("✅ [TOOL] Predefined sub-agent completed successfully: route=%s, todo=%s", routeID, todoID))
		return result, nil
	}
}

// createExecuteGenericAgentFunc creates a function that executes generic agents
// This function is injected into context for the call_generic_agent tool to use
// Sub-agents get all their input from the tool parameters (instructions)
// They do NOT read the tasks.md file - the orchestrator provides everything via the tool call
func (hcpo *StepBasedWorkflowOrchestrator) createExecuteGenericAgentSyncFunc(
	execCtx *SubAgentExecutionContext,
) virtualtools.ExecuteGenericAgentFunc {
	return func(ctx context.Context, todoID, instructions string) (string, error) {
		hcpo.GetLogger().Info(fmt.Sprintf("🤖 [TOOL] Executing generic agent via tool: todo=%s", todoID))

		// Propagate workshop correlation IDs to sub-agent context
		if execCtx.WorkshopCorrelationID != "" {
			if forcedID, ok := ctx.Value(orchestrator_events.ForceCorrelationIDKey).(string); !ok || forcedID == "" {
				ctx = context.WithValue(ctx, orchestrator_events.ForceCorrelationIDKey, execCtx.WorkshopCorrelationID)
				ctx = context.WithValue(ctx, orchestrator_events.IsSubAgentContextKey, true)
			}
		}

		// Build a OrchestratorDecision to reuse existing execution logic
		// All task info comes from the tool parameters, not from a file
		response := &OrchestratorDecision{
			NextAction:             "delegate",
			UseGenericAgent:        true,
			TodoIDToExecute:        todoID,
			InstructionsToSubAgent: instructions,
		}

		// Emit route selected event BEFORE sub-agent execution so it appears before the agent card
		hcpo.emitOrchestratorRouteSelectedEvent(ctx, execCtx.OrchestratorStep, execCtx.StepIndex, execCtx.StepPath, 0, response, "")

		startTime := time.Now()

		// Execute using existing method
		// All task info comes from tool parameters
		result, history, err := hcpo.executeGenericAgent(
			ctx,
			execCtx.OrchestratorStep,
			execCtx.StepIndex,
			execCtx.StepPath,
			response,
			execCtx.AllSteps,
			execCtx.Progress,
		)

		executionTime := time.Since(startTime)

		// Store call record for get_sub_agent_conversation
		record := SubAgentCallRecord{
			CalledAt:      startTime,
			ExecutionID:   asyncSubAgentExecutionID(ctx),
			TodoID:        todoID,
			AgentType:     "generic",
			Success:       err == nil,
			Result:        result,
			ExecutionTime: executionTime.String(),
			Conversation:  serializeConversationHistory(history),
		}
		if err != nil {
			record.Error = err.Error()
		}
		execCtx.callHistoryMu.Lock()
		record.Index = len(execCtx.CallHistory) + 1
		execCtx.CallHistory = append(execCtx.CallHistory, record)
		execCtx.callHistoryMu.Unlock()

		if err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [TOOL] Generic agent execution failed: %v", err))
			return result, err
		}

		hcpo.GetLogger().Info(fmt.Sprintf("✅ [TOOL] Generic agent completed successfully: todo=%s", todoID))
		return result, nil
	}
}

// Execute implements the Orchestrator interface
