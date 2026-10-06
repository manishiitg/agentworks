package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/testmode"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/observability"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// SubAgentNotifier is called by the todo task controller when a sub-agent starts and completes.
// Implemented by the server layer to inject auto-notifications into the main workshop agent.
type SubAgentNotifier interface {
	OnSubAgentStart(start WorkshopExecutionStart)
	OnSubAgentComplete(agentID, name, result string, err error)
}

// compositeSubAgentNotifier calls multiple notifiers in sequence.
type compositeSubAgentNotifier struct {
	notifiers []SubAgentNotifier
}

func (c *compositeSubAgentNotifier) OnSubAgentStart(start WorkshopExecutionStart) {
	for _, n := range c.notifiers {
		n.OnSubAgentStart(start)
	}
}

func (c *compositeSubAgentNotifier) OnSubAgentComplete(agentID, name, result string, err error) {
	for _, n := range c.notifiers {
		n.OnSubAgentComplete(agentID, name, result, err)
	}
}

// ChainSubAgentNotifiers returns a notifier that calls all provided notifiers in sequence.
func ChainSubAgentNotifiers(notifiers ...SubAgentNotifier) SubAgentNotifier {
	return &compositeSubAgentNotifier{notifiers: notifiers}
}

// StepBasedWorkflowOrchestrator manages simplified human-controlled todo planning process
// - Single execution (no iterations)
// - No validation phase
// - No critique phase
// - No cleanup phase
// - Simple direct planning approach
// - Always includes independent steps extraction for parallel execution
// - NEW: Includes learning phase after each step execution and validation
type StepBasedWorkflowOrchestrator struct {
	codeLayoutVersion      atomic.Int32
	platformStoresDisabled atomic.Bool
	// testRun is the active test-mode execution (PLAT-562), nil otherwise.
	// Every tool session this controller sets up while it is set belongs to
	// the test run.
	testRun atomic.Pointer[testmode.Run]
	// Base orchestrator for common functionality
	*orchestrator.BaseOrchestrator
	// NEW: Store planning conversation for iterative refinement
	sessionID     string // For human feedback tracking
	workflowID    string // For human feedback tracking
	httpSessionID string // HTTP session ID for MCP cleanup scoping
	cdpPort       int    // Primary CDP port for backward compatibility.
	cdpPorts      []int  // Authorized independent Chrome profiles for this workflow.
	browserMode   string // Browser mode: "cdp", "headless", "" (auto-detect)

	// Workshop MCP session cache: one reusable MCP session per group name within a
	// workshop session. This preserves browser/login state when the user runs
	// multiple steps for the same group, while isolating different groups.
	workshopGroupSessionsMu  sync.Mutex
	workshopGroupSessionIDs  map[string]string
	workshopGroupSessionRefs map[string]int
	workshopGroupLastUsed    map[string]time.Time

	// In-memory message_sequence ROUTE conversation cache. When a message_sequence is used
	// as a todo_task route, the orchestrator re-enters it across calls within one run; this
	// holds each route's conversation so it remembers prior calls WITHOUT reading back from
	// disk. Scoped to this orchestrator instance (one workflow run). Standalone
	// message_sequence steps never use this — they always run a fixed queue.
	msgSeqRoutesMu   sync.Mutex
	msgSeqRoutes     map[string]*messageSequenceSession
	msgSeqRouteLocks map[string]*sync.Mutex

	// Variable management
	variablesManifest *VariablesManifest // Extracted variables
	variableValues    map[string]string  // Runtime variable values
	variableManager   *VariableManager   // Variable manager for variable extraction operations (independent from controller)

	// Single step execution mode
	runSingleStepOnly bool // Whether to run only a single step and stop
	singleStepTarget  int  // Target step index to run (0-based)

	// Skip human input mode tracking (runs learning but skips human feedback)
	skipHumanInput bool // Whether to skip human feedback requests (auto-approve steps)

	// Plans are passed through execution contexts, never cached on a chat session.

	// Run folder management
	selectedRunFolder string // Current run folder name (iteration-0 for full workflow runs)

	// Batch execution context (current group identity for env refs, logs, event metadata)
	currentGroupName string // Current group name being executed
	currentGroupIdx  int    // 0-based index of current group
	totalGroups      int    // Total number of groups in batch

	// Frontend-provided execution options (when provided, skips interactive prompts)
	executionOptions *ExecutionOptions

	// Preset-level agent defaults (used when step config doesn't specify)
	presetPhaseLLM *AgentLLMConfig // The Builder model: workshop chat, planning, measurement setup, scheduled runs and every turn of the Pulse conversation (Gate, dispatch, Finalize).
	presetPulseLLM *AgentLLMConfig // The Pulse model: background review agents launched by Pulse turns (plan drift / technical / strategic review) and KB upkeep -- work that starts its own process.

	// Preset-level feature toggles
	useKnowledgebase bool   // Whether to create and reference knowledgebase folder (default: true)
	kbShape          string // Legacy field. Only "notes-only" is supported; legacy "graph+notes" values collapse to notes-only at runtime (see workflowtypes.ResolveKBShape).

	// Tiered LLM allocation mode
	tierResolver *TierResolver // nil when no tiered config

	// Workshop: toolbar-selected group IDs (used for auto-resolving variable values and run folders)
	enabledGroupNames []string

	// Human input overrides: per-step responses for human_input steps during run_full_workflow.
	// Key is step ID (e.g., "choose-workflow"), value is the response to use.
	// Checked before variableValues fallback when SkipHumanInput is true.
	humanInputOverrides map[string]string

	// SubAgentNotifier is called when a todo task sub-agent starts/completes.
	// Used by the server layer to inject auto-notifications into the main workshop agent.
	subAgentNotifier SubAgentNotifier

	// Workshop execution tracking hooks allow controller-launched background work
	// (like automatic success learning) to appear in the workshop registry/UI and be stoppable.
	workshopSessionCtx        context.Context
	workshopStepRegistry      *WorkshopStepRegistry
	workshopExecutionNotifier WorkshopExecutionNotifier
}

// SetSubAgentNotifier sets the notifier called on todo task sub-agent start/completion.
func (hcpo *StepBasedWorkflowOrchestrator) SetSubAgentNotifier(n SubAgentNotifier) {
	hcpo.subAgentNotifier = n
}

// GetSubAgentNotifier returns the current sub-agent notifier (may be nil).
func (hcpo *StepBasedWorkflowOrchestrator) GetSubAgentNotifier() SubAgentNotifier {
	return hcpo.subAgentNotifier
}

// SetWorkshopExecutionContext wires the workshop session context and execution registry
// into the controller so controller-managed background tasks can be tracked and canceled.
func (hcpo *StepBasedWorkflowOrchestrator) SetWorkshopExecutionContext(sessionCtx context.Context, registry *WorkshopStepRegistry) {
	hcpo.workshopSessionCtx = sessionCtx
	hcpo.workshopStepRegistry = registry
}

// SetWorkshopExecutionNotifier wires the server-side execution notifier into the controller
// so controller-managed background tasks keep the frontend polling/notification state updated.
func (hcpo *StepBasedWorkflowOrchestrator) SetWorkshopExecutionNotifier(n WorkshopExecutionNotifier) {
	hcpo.workshopExecutionNotifier = n
}

// NewStepBasedWorkflowOrchestrator creates a new human-controlled todo planner orchestrator
func NewStepBasedWorkflowOrchestrator(
	ctx context.Context,
	provider string,
	model string,
	temperature float64,
	agentMode string,
	selectedServers []string,
	selectedTools []string, // NEW parameter
	useCodeExecutionMode bool, // NEW parameter
	mcpConfigPath string,
	llmConfig *orchestrator.LLMConfig,
	maxTurns int,
	logger loggerv2.Logger,
	tracer observability.Tracer,
	eventBridge mcpagent.AgentEventListener,
	customTools []llmtypes.Tool,
	customToolExecutors map[string]interface{},
	toolCategories map[string]string, // NEW: tool category map
	presetPhaseLLM *AgentLLMConfig, // Optional preset default for lightweight phase agents
	presetPulseLLM *AgentLLMConfig, // Optional preset default for review, maintenance, and Pulse agents
	useKnowledgebase bool, // Whether to create and reference knowledgebase folder (default: true)
	tieredConfig *TieredLLMConfig, // Tiered LLM config (nil when not using tiered allocation)
) (*StepBasedWorkflowOrchestrator, error) {

	// Create base workflow orchestrator
	// Note: provider and model parameters removed - not used (LLM comes from step config/tiered/preset)
	baseOrchestrator, err := orchestrator.NewBaseOrchestrator(
		logger,
		eventBridge,
		orchestrator.OrchestratorTypeWorkflow,
		mcpConfigPath,
		temperature,
		agentMode,
		selectedServers,
		selectedTools,        // Pass through actual selected tools
		useCodeExecutionMode, // NEW: Pass code execution mode
		llmConfig,
		maxTurns,
		customTools,
		customToolExecutors,
		toolCategories, // NEW: Pass category map
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create base orchestrator: %w", err)
	}

	// Generate session ID for MCP connection sharing across all agents in this workflow
	// This MUST be set before creating any agents to ensure connection reuse
	// NOTE: We always run with groups, so include group name in sessionID format
	// If group name is not available yet (will be set later in batch execution), use "default-group" placeholder
	// The sessionID will be overridden in batch_execution.go when the actual group name is known
	groupName := "default-group" // Placeholder - will be overridden in batch execution with actual group name
	workflowSessionID := fmt.Sprintf("session-group-%s-%d", groupName, time.Now().UnixNano())
	baseOrchestrator.SetMCPSessionID(workflowSessionID)
	logger.Info(fmt.Sprintf("🔗 Set MCP session ID for workflow: %s (will be overridden with actual group name in batch execution)", workflowSessionID))

	hcpo := &StepBasedWorkflowOrchestrator{
		BaseOrchestrator: baseOrchestrator,
		sessionID:        workflowSessionID, // Use the same session ID set on BaseOrchestrator for MCP connection sharing
		workflowID:       fmt.Sprintf("workflow_%d", time.Now().UnixNano()),
		presetPhaseLLM:   presetPhaseLLM,
		presetPulseLLM:   presetPulseLLM,
		useKnowledgebase: useKnowledgebase,
	}

	// Set up tiered LLM allocation mode
	if tieredConfig != nil {
		orchestratorLLMConfig := llmConfig
		var apiKeys *orchestrator.APIKeys
		if orchestratorLLMConfig != nil {
			apiKeys = orchestratorLLMConfig.APIKeys
		}
		hcpo.tierResolver = NewTierResolver(tieredConfig, apiKeys)
		// Phase LLM is independent of tiered mode - always configured separately
		if hcpo.presetPhaseLLM != nil {
			logger.Info(fmt.Sprintf("🏷️ Phase LLM (independent): %s/%s", hcpo.presetPhaseLLM.Provider, hcpo.presetPhaseLLM.ModelID))
		} else {
			logger.Info("🏷️ WARNING: No Phase LLM configured - phase agents will fail")
		}
		if hcpo.presetPulseLLM != nil {
			logger.Info(fmt.Sprintf("🏷️ Pulse LLM (independent): %s/%s", hcpo.presetPulseLLM.Provider, hcpo.presetPulseLLM.ModelID))
		}
		logger.Info(fmt.Sprintf("🏷️ Tiered LLM allocation mode enabled - Tier1: %s, Tier2: %s, Tier3: %s",
			formatTierAgentLLM(tieredConfig.Tier1),
			formatTierAgentLLM(tieredConfig.Tier2),
			formatTierAgentLLM(tieredConfig.Tier3)))
	}

	// Create VariableManager for variable extraction operations (independent from controller)
	hcpo.variableManager = NewVariableManager(
		baseOrchestrator,
	)

	return hcpo, nil
}

// SetHTTPSessionID sets the HTTP session ID used for MCP session tracking.
// This allows CloseHTTPSession to close all group sessions when the workflow stops.
func (hcpo *StepBasedWorkflowOrchestrator) SetHTTPSessionID(httpSessionID string) {
	hcpo.httpSessionID = httpSessionID
}

func (hcpo *StepBasedWorkflowOrchestrator) resolveWorkshopBrowserSessionID(groupName string) string {
	return common.PrefixBrowserSessionID(workshopBrowserSessionID(hcpo.workshopBrowserNamespace(), hcpo.GetWorkspacePath(), groupName))
}

func workshopBrowserSessionID(browserNamespace, workspacePath, groupName string) string {
	if namespace := strings.TrimSpace(browserNamespace); namespace != "" {
		return namespace + "--browser"
	}
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		groupName = "default-group"
	}
	safeGroupName := strings.NewReplacer("/", "-", "\\", "-", " ", "-", ":", "-").Replace(groupName)
	workspacePath = strings.TrimSpace(workspacePath)
	browserNamespace = strings.TrimSpace(browserNamespace)
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(browserNamespace))
	_, _ = hasher.Write([]byte("::"))
	_, _ = hasher.Write([]byte(workspacePath))
	_, _ = hasher.Write([]byte("::"))
	_, _ = hasher.Write([]byte(groupName))
	name := fmt.Sprintf("workflow-browser-%x-%s", hasher.Sum64(), safeGroupName)
	if browserNamespace != "" {
		return browserNamespace + "--" + name
	}
	return name
}

func (hcpo *StepBasedWorkflowOrchestrator) workshopBrowserNamespace() string {
	for _, sessionID := range []string{hcpo.httpSessionID, hcpo.sessionID, hcpo.GetMCPSessionID()} {
		if cfg := common.GetSessionShellConfig(strings.TrimSpace(sessionID)); cfg != nil && strings.TrimSpace(cfg.BrowserSessionNamespace) != "" {
			return strings.TrimSpace(cfg.BrowserSessionNamespace)
		}
	}
	return ""
}

func (hcpo *StepBasedWorkflowOrchestrator) bindWorkshopBrowserSession(toolSessionID, browserSessionID string) {
	toolSessionID = strings.TrimSpace(toolSessionID)
	browserSessionID = strings.TrimSpace(browserSessionID)
	if toolSessionID == "" || browserSessionID == "" {
		return
	}
	if namespace := hcpo.workshopBrowserNamespace(); namespace != "" {
		common.SetSessionBrowserNamespace(toolSessionID, namespace)
	}
	common.SetSessionBrowserSessionID(toolSessionID, browserSessionID)
	// Dedicated execution/message-sequence sessions need the same authenticated
	// parent relationship as group sessions. The bridge checks this registry;
	// a shared browser namespace alone is not authority to use the parent's tools.
	if parent := strings.TrimSpace(hcpo.httpSessionID); parent != "" && parent != toolSessionID {
		mcpagent.RegisterHTTPSession(parent, toolSessionID)
	}
}

// switchWorkshopGroupSession ensures workshop step execution uses a stable MCP
// session per group name instead of the controller's "default-group" placeholder.
// Reusing a cached per-group session preserves browser/login state across steps
// for the same group. All groups inherit the workflow's browser.
func (hcpo *StepBasedWorkflowOrchestrator) switchWorkshopGroupSession(groupName string) (func(), error) {
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return func() {}, nil
	}
	hcpo.ApplyWorkflowLogContext(hcpo.GetWorkspacePath(), groupName)

	now := time.Now()
	var (
		groupSessionID string
		exists         bool
		created        bool
		evicted        map[string]string
		refCount       int
	)

	hcpo.workshopGroupSessionsMu.Lock()
	if hcpo.workshopGroupSessionIDs == nil {
		hcpo.workshopGroupSessionIDs = make(map[string]string)
	}
	if hcpo.workshopGroupSessionRefs == nil {
		hcpo.workshopGroupSessionRefs = make(map[string]int)
	}
	if hcpo.workshopGroupLastUsed == nil {
		hcpo.workshopGroupLastUsed = make(map[string]time.Time)
	}

	groupSessionID, exists = hcpo.workshopGroupSessionIDs[groupName]
	if !exists {
		for len(hcpo.workshopGroupSessionIDs) >= browser.MaxBrowserSessionsPerWorkflow {
			evictGroupName, evictSessionID, ok := hcpo.oldestIdleWorkshopGroupSessionLocked()
			if !ok {
				activeGroups := hcpo.activeWorkshopGroupsLocked()
				hcpo.workshopGroupSessionsMu.Unlock()
				return nil, fmt.Errorf(
					"cannot open workshop browser session for group %q: session already has %d active browser group sessions (max %d): %s",
					groupName,
					len(activeGroups),
					browser.MaxBrowserSessionsPerWorkflow,
					strings.Join(activeGroups, ", "),
				)
			}
			if evicted == nil {
				evicted = make(map[string]string)
			}
			evicted[evictGroupName] = evictSessionID
			delete(hcpo.workshopGroupSessionIDs, evictGroupName)
			delete(hcpo.workshopGroupSessionRefs, evictGroupName)
			delete(hcpo.workshopGroupLastUsed, evictGroupName)
		}

		groupSessionID = fmt.Sprintf("session-group-%s-%d", groupName, time.Now().UnixNano())
		hcpo.workshopGroupSessionIDs[groupName] = groupSessionID
		created = true
	}
	if !created {
		// Reusing a cached session ID — clear any stopped state from a previous run.
		// The mcpagent registry marks sessions stopped when the workflow is interrupted;
		// without this, the next run reuses the same ID and gets "session was stopped" errors.
		mcpagent.ClearSessionsStopped([]string{groupSessionID})
	}
	hcpo.workshopGroupSessionRefs[groupName]++
	refCount = hcpo.workshopGroupSessionRefs[groupName]
	hcpo.workshopGroupLastUsed[groupName] = now
	hcpo.workshopGroupSessionsMu.Unlock()

	hcpo.closeWorkshopGroupSessions(evicted, "Evicting idle cached group MCP session")
	if created && hcpo.httpSessionID != "" {
		mcpagent.RegisterHTTPSession(hcpo.httpSessionID, groupSessionID)
		// Inherit folder guard from parent HTTP session so tools called
		// under the group session enforce the same write restrictions
		// (e.g., planning/ is read-only in workflow-builder mode).
		common.CopySessionFolderGuard(hcpo.httpSessionID, groupSessionID)
	}

	previousSessionID := hcpo.GetMCPSessionID()
	hcpo.sessionID = groupSessionID
	hcpo.BaseOrchestrator.SetMCPSessionID(groupSessionID)
	virtualtools.InheritSessionNotificationDestination(previousSessionID, groupSessionID)
	if pc := virtualtools.GetParentChat(previousSessionID); pc != nil && pc.SessionID != "" {
		pcCopy := *pc
		if pcCopy.GroupName == "" {
			pcCopy.GroupName = groupName
		}
		virtualtools.RegisterParentChat(groupSessionID, &pcCopy)
		hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Registered parent chat for group session %s from previous session %s", groupSessionID, previousSessionID))
	}

	cacheAction := "reused"
	if !exists {
		cacheAction = "created"
	}
	hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] %s MCP session for group %s: %s (previous=%s, refs=%d)", cacheAction, groupName, groupSessionID, previousSessionID, refCount))

	browserSessionID := hcpo.resolveWorkshopBrowserSessionID(groupName)
	hcpo.bindWorkshopBrowserSession(groupSessionID, browserSessionID)
	if hcpo.httpSessionID != "" {
		hcpo.bindWorkshopBrowserSession(hcpo.httpSessionID, browserSessionID)
	}
	hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Bound tool sessions to shared browser session for group %s: browser=%s chat=%s run=%s",
		groupName, browserSessionID, hcpo.httpSessionID, groupSessionID))

	if strings.Contains(hcpo.GetMCPSessionID(), "default-group") {
		return nil, fmt.Errorf("workshop execution for group %q still has placeholder MCP session %q", groupName, hcpo.GetMCPSessionID())
	}
	cdpPorts := hcpo.cdpPortsForCleanup()
	browser.AcquireCDPTabOwnerLease(browserSessionID, cdpPorts)
	return func() {
		hcpo.releaseWorkshopGroupSession(groupName)
		browser.ReleaseCDPTabOwnerLease(browserSessionID, cdpPorts, browser.NewClient(getWorkspaceAPIURL()), browser.DefaultCDPTabCleanupDelay)
	}, nil
}

// CloseWorkshopGroupSessions closes all cached workshop group MCP sessions.
// This is best-effort cleanup for workshop sessions that switched groups.
func (hcpo *StepBasedWorkflowOrchestrator) CloseWorkshopGroupSessions() {
	hcpo.workshopGroupSessionsMu.Lock()
	if len(hcpo.workshopGroupSessionIDs) == 0 {
		hcpo.workshopGroupSessionsMu.Unlock()
		return
	}
	sessionsByGroup := make(map[string]string, len(hcpo.workshopGroupSessionIDs))
	for groupName, sessionID := range hcpo.workshopGroupSessionIDs {
		sessionsByGroup[groupName] = sessionID
	}
	hcpo.workshopGroupSessionIDs = make(map[string]string)
	hcpo.workshopGroupSessionRefs = make(map[string]int)
	hcpo.workshopGroupLastUsed = make(map[string]time.Time)
	hcpo.workshopGroupSessionsMu.Unlock()

	hcpo.closeWorkshopGroupSessions(sessionsByGroup, "Closing cached group MCP session")
}

func (hcpo *StepBasedWorkflowOrchestrator) oldestIdleWorkshopGroupSessionLocked() (string, string, bool) {
	var (
		oldestGroup     string
		oldestSessionID string
		oldestTime      time.Time
	)
	for groupName, sessionID := range hcpo.workshopGroupSessionIDs {
		if hcpo.workshopGroupSessionRefs[groupName] > 0 {
			continue
		}
		lastUsed := hcpo.workshopGroupLastUsed[groupName]
		if oldestGroup == "" || lastUsed.Before(oldestTime) {
			oldestGroup = groupName
			oldestSessionID = sessionID
			oldestTime = lastUsed
		}
	}
	return oldestGroup, oldestSessionID, oldestGroup != ""
}

func (hcpo *StepBasedWorkflowOrchestrator) activeWorkshopGroupsLocked() []string {
	activeGroups := make([]string, 0, len(hcpo.workshopGroupSessionRefs))
	for groupName, refs := range hcpo.workshopGroupSessionRefs {
		if refs > 0 {
			activeGroups = append(activeGroups, groupName)
		}
	}
	sort.Strings(activeGroups)
	return activeGroups
}

func (hcpo *StepBasedWorkflowOrchestrator) releaseWorkshopGroupSession(groupName string) {
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return
	}

	hcpo.workshopGroupSessionsMu.Lock()
	defer hcpo.workshopGroupSessionsMu.Unlock()

	if hcpo.workshopGroupSessionRefs == nil {
		return
	}
	refs := hcpo.workshopGroupSessionRefs[groupName]
	if refs <= 1 {
		delete(hcpo.workshopGroupSessionRefs, groupName)
		if hcpo.workshopGroupLastUsed == nil {
			hcpo.workshopGroupLastUsed = make(map[string]time.Time)
		}
		hcpo.workshopGroupLastUsed[groupName] = time.Now()
		hcpo.GetLogger().Debug(fmt.Sprintf("[WORKSHOP] Released group session %s (refs=0)", groupName))
		return
	}
	hcpo.workshopGroupSessionRefs[groupName] = refs - 1
	hcpo.workshopGroupLastUsed[groupName] = time.Now()
	hcpo.GetLogger().Debug(fmt.Sprintf("[WORKSHOP] Released group session %s (refs=%d)", groupName, refs-1))
}

func (hcpo *StepBasedWorkflowOrchestrator) closeWorkshopGroupSessions(sessionsByGroup map[string]string, action string) {
	if len(sessionsByGroup) == 0 {
		return
	}

	groupNames := make([]string, 0, len(sessionsByGroup))
	for groupName := range sessionsByGroup {
		groupNames = append(groupNames, groupName)
	}
	sort.Strings(groupNames)

	tracker := browser.GetSessionTracker()
	browserClient := browser.NewClient(getWorkspaceAPIURL())

	for _, groupName := range groupNames {
		sessionID := sessionsByGroup[groupName]
		browserSessionID := hcpo.resolveWorkshopBrowserSessionID(groupName)
		hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] %s: group=%s session=%s browser=%s", action, groupName, sessionID, browserSessionID))
		virtualtools.UnregisterParentChat(sessionID)
		virtualtools.DeleteSessionNotificationDestination(sessionID)
		// Mark all sessions as stopped BEFORE closing to prevent in-flight tool calls
		// from resurrecting connections via broken pipe handlers.
		mcpagent.MarkSessionsStopped([]string{sessionID})
		mcpagent.CloseSession(sessionID)
		tracker.CloseSession(browserSessionID, browserClient)
	}
}

// SetCdpPort sets the CDP port for browser mode detection.
func (hcpo *StepBasedWorkflowOrchestrator) SetCdpPort(port int) {
	hcpo.SetCdpPorts([]int{port})
}

// GetCdpPort returns the CDP port (0 = headless, >0 = CDP mode).
func (hcpo *StepBasedWorkflowOrchestrator) GetCdpPort() int {
	return hcpo.cdpPort
}

// SetCdpPorts configures explicitly authorized, independently-profiled Chrome
// browsers. Multiple ports are for specialized multi-login testing, not normal
// workflow concurrency.
func (hcpo *StepBasedWorkflowOrchestrator) SetCdpPorts(ports []int) {
	hcpo.cdpPorts = append([]int(nil), ports...)
	hcpo.cdpPort = 0
	if len(hcpo.cdpPorts) > 0 {
		hcpo.cdpPort = hcpo.cdpPorts[0]
	}
}

func (hcpo *StepBasedWorkflowOrchestrator) GetCdpPorts() []int {
	return append([]int(nil), hcpo.cdpPorts...)
}

func (hcpo *StepBasedWorkflowOrchestrator) cdpPortsForCleanup() []int {
	ports := append([]int(nil), hcpo.cdpPorts...)
	if hcpo.cdpPort > 0 {
		ports = append([]int{hcpo.cdpPort}, ports...)
	}
	seen := make(map[int]bool, len(ports))
	normalized := make([]int, 0, len(ports))
	for _, port := range ports {
		if port < 1 || port > 65535 || seen[port] {
			continue
		}
		seen[port] = true
		normalized = append(normalized, port)
	}
	return normalized
}

// SetBrowserMode sets the browser mode for prompt instructions.
// Valid values: "cdp", "headless", "" (auto-detect from skills).
func (hcpo *StepBasedWorkflowOrchestrator) SetBrowserMode(mode string) {
	hcpo.browserMode = mode
}

// GetBrowserMode returns the configured browser mode, or "" for auto-detect.
func (hcpo *StepBasedWorkflowOrchestrator) GetBrowserMode() string {
	return hcpo.browserMode
}

// HasBrowserCapability reports whether this workflow has browser automation available —
// the agent-browser runtime skill, a configured CDP port, or an explicit
// non-"none" browserMode. Use this (not
// GetBrowserMode directly) when deciding whether to emit browser-specific prompt
// content — an empty browserMode means "auto-detect", NOT "no browser".
func (hcpo *StepBasedWorkflowOrchestrator) HasBrowserCapability() bool {
	if mode := hcpo.browserMode; mode != "" && mode != "none" {
		return true
	}
	if hcpo.GetCdpPort() > 0 {
		return true
	}
	for _, s := range hcpo.GetSelectedSkills() {
		if isBrowserAutomationSkill(s) {
			return true
		}
	}
	return false
}

// CreateTodoList orchestrates the human-controlled todo planning process
// - Single execution (no iterations)
// - Includes validation phase (runs later in the workflow)
// - Includes critique phase during writer validation loop
// - Skips cleanup phase
// - Simple direct planning approach
// - NEW: Includes human approval loop with iterative plan refinement
// isPartialGroupRun reports whether this run refreshes only some enabled groups.
// Such a run must retain iteration-0 so the untouched groups stay live.
func (hcpo *StepBasedWorkflowOrchestrator) isPartialGroupRun() bool {
	if hcpo.executionOptions == nil || len(hcpo.executionOptions.EnabledGroupNames) == 0 || hcpo.variablesManifest == nil {
		return false
	}
	return len(hcpo.executionOptions.EnabledGroupNames) < len(hcpo.variablesManifest.GetEnabledGroups())
}

func (hcpo *StepBasedWorkflowOrchestrator) CreateTodoList(ctx context.Context, objective, workspacePath string) (string, error) {
	hcpo.ApplyWorkflowLogContext(workspacePath, orchestrator.SingleSelectedGroupName(func() []string {
		if hcpo.executionOptions == nil {
			return nil
		}
		return hcpo.executionOptions.EnabledGroupNames
	}()))
	hcpo.GetLogger().Info(fmt.Sprintf("🚀 Starting human-controlled todo planning for objective: %s", objective))
	hcpo.GetLogger().Info(fmt.Sprintf("🔍 [DEBUG] CreateTodoList: Starting - workspacePath=%s", workspacePath))

	// Set objective and workspace path directly
	// WorkspacePath is the base workspace path (no subdirectory)
	// The workspace API will handle internal resolution to ../workspace-docs/ when needed
	hcpo.SetObjective(objective)
	hcpo.SetWorkspacePath(workspacePath)
	// Direct execution does not pass through Builder's ReadCurrentPlan.
	// Resolve the persisted layout before selecting any saved scripts or deps.
	if err := hcpo.loadCodeLayout(ctx); err != nil {
		return "", err
	}
	// If no objective was threaded in from the caller, resolve from soul/soul.md
	// (canonical source) with workflow.json fallback. Downstream consumers like the
	// learning agent pull `CurrentObjective` from hcpo.GetObjective() — without this
	// fallback, the template var would be empty when the builder hadn't set an
	// explicit objective via the old set_workflow_objective tool.
	if strings.TrimSpace(hcpo.GetObjective()) == "" {
		if resolved, _ := hcpo.ResolveWorkflowObjective(ctx); resolved != "" {
			hcpo.SetObjective(resolved)
		}
	}
	hcpo.GetLogger().Info(fmt.Sprintf("🔍 [DEBUG] CreateTodoList: Objective and workspace path set to %s", workspacePath))

	// PHASE 0: Check both variables and plan at start (before any prompts)
	// Check if variables.json exists - OPTIONAL (planning agent can create it)
	variablesPath := fmt.Sprintf("%s/variables/variables.json", hcpo.GetWorkspacePath())
	variablesExist, existingVariablesManifest, err := hcpo.variableManager.checkExistingVariables(ctx, variablesPath)
	if err != nil {
		// Log error but continue without variables (planning agent can create them)
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to check for existing variables: %v - proceeding without variables", err))
		variablesExist = false
	}

	var templatedObjective string
	if variablesExist && existingVariablesManifest != nil {
		// Variables exist - use them
		hcpo.variablesManifest = existingVariablesManifest // Store in orchestrator so formatVariableNames/Values can access it
		templatedObjective = hcpo.GetObjective()
		hcpo.GetLogger().Info(fmt.Sprintf("✅ Using existing variables.json with %d variables", len(existingVariablesManifest.Variables)))
	} else {
		// No variables.json - planning agent can extract variables if needed
		hcpo.variablesManifest = nil
		// Use original objective (no templating)
		templatedObjective = hcpo.GetObjective()
	}

	// Check if plan.json exists - REQUIRED for execution
	// Use relative path - ReadWorkspaceFile auto-prepends workspacePath
	planPath := "planning/plan.json"
	planExists, existingPlan, err := hcpo.checkExistingPlan(ctx, planPath)
	if err != nil {
		return "", fmt.Errorf("failed to check for existing plan: %w", err)
	}
	if !planExists {
		return "", fmt.Errorf("plan.json not found at %s - planning must be run first as a separate phase", planPath)
	}

	// Plan exists - use it

	// Safety check: Ensure plan has steps
	if len(existingPlan.Steps) == 0 {
		hcpo.GetLogger().Error(fmt.Sprintf("❌ Existing plan has no steps"), nil)
		return "", fmt.Errorf(fmt.Sprintf("existing plan has no steps"), nil)
	}

	// Load runtime variable values if provided and switch to templated objective
	// If a specific group is selected via execution options, use that group's values
	var variableValues map[string]string
	if hcpo.executionOptions != nil && len(hcpo.executionOptions.EnabledGroupNames) > 0 && hcpo.variablesManifest != nil {
		// Specific group(s) selected - use the first group's values (for single group execution)
		requestedGroupName := hcpo.executionOptions.EnabledGroupNames[0]

		// Log available groups for debugging
		availableGroupNames := make([]string, len(hcpo.variablesManifest.Groups))
		for i, g := range hcpo.variablesManifest.Groups {
			availableGroupNames[i] = g.Name
		}

		variableValues = hcpo.variablesManifest.GetVariableValues(requestedGroupName)
		if variableValues == nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [VARIABLE LOADING] Group %s not found in manifest, falling back to LoadVariableValues", requestedGroupName))
			var err error
			variableValues, err = LoadVariableValues(ctx, hcpo.BaseOrchestrator, hcpo.GetWorkspacePath(), hcpo.GetWorkspacePath())
			if err != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [VARIABLE LOADING] Failed to load variable values: %v", err))
			} else {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [VARIABLE LOADING] Loaded from fallback LoadVariableValues (may not match requested group %s)", requestedGroupName))
			}
		} else {
			hcpo.GetLogger().Info(fmt.Sprintf("✅ [VARIABLE LOADING] Loaded variable values for selected group: %s (values: %v)", requestedGroupName, variableValues))

			// Validate: Double-check that we got the right group's values plus shared defaults.
			// Find the group in manifest to verify.
			for _, g := range hcpo.variablesManifest.Groups {
				if g.Name == requestedGroupName {
					expectedValues := MergeGroupWithDefaults(hcpo.variablesManifest, g.Values)
					// Compare values to ensure they match
					valuesMatch := true
					if len(variableValues) != len(expectedValues) {
						valuesMatch = false
					} else {
						for k, v := range variableValues {
							if expectedValues[k] != v {
								valuesMatch = false
								hcpo.GetLogger().Error(fmt.Sprintf("❌ [VARIABLE LOADING] Value mismatch for key %s: expected %s, got %s", k, expectedValues[k], v), nil)
								break
							}
						}
					}
					if !valuesMatch {
						hcpo.GetLogger().Error(fmt.Sprintf("❌ [VARIABLE LOADING] Variable values don't match merged values for group %s! Expected: %v, Got: %v", requestedGroupName, expectedValues, variableValues), nil)
					} else {
						hcpo.GetLogger().Info(fmt.Sprintf("✅ [VARIABLE LOADING] Verified variable values match merged values for group %s", requestedGroupName))
					}
					break
				}
			}
		}
	} else {
		// No specific group selected - use default LoadVariableValues (backward compatibility)
		var err error
		variableValues, err = LoadVariableValues(ctx, hcpo.BaseOrchestrator, hcpo.GetWorkspacePath(), hcpo.GetWorkspacePath())
		if err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [VARIABLE LOADING] Failed to load variable values: %v", err))
		}
	}

	if variableValues != nil {
		hcpo.variableValues = variableValues
		SyncVariablesToWorkspaceEnv(hcpo.BaseOrchestrator, variableValues)
		hcpo.GetLogger().Info(fmt.Sprintf("✅ [VARIABLE LOADING] Set hcpo.variableValues with %d variables", len(variableValues)))
	} else {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [VARIABLE LOADING] variableValues is nil - no variables loaded"))
	}

	// Switch to templated objective for all subsequent phases
	hcpo.SetObjective(templatedObjective)
	hcpo.GetLogger().Info(fmt.Sprintf("✅ Using templated objective with {{VARIABLES}}: %s", templatedObjective))

	// Populate runtime fields on plan steps for execution
	stepConfigs, err := hcpo.ReadStepConfigs(ctx)
	if err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to read step_config.json: %v (using defaults)", err))
		stepConfigs = []StepConfig{}
	}
	for _, step := range existingPlan.Steps {
		if err := populateRuntimeFields(step, stepConfigs); err != nil {
			return "", fmt.Errorf("failed to populate runtime fields: %w", err)
		}
	}
	breakdownSteps := existingPlan.Steps // Use PlanStepInterface directly
	if err := bindWebhookStep(hcpo.executionOptions, breakdownSteps); err != nil {
		return "", err
	}
	hcpo.GetLogger().Info(fmt.Sprintf("✅ Prepared existing plan: %d steps with runtime fields populated", len(breakdownSteps)))

	// Pin this execution to its loaded plan; builder reads cannot replace it.
	ctx = withExecutionPlan(ctx, existingPlan)

	// Note: Learning integration phase removed - execution agent now auto-discovers learning files and scripts

	// Resolve iteration folder.
	// When running a subset of groups, keep iteration-0 in place so other groups' output is preserved.
	// Batch execution handles per-group cleanup independently.
	execOpts := hcpo.executionOptions
	isPartialGroupRun := hcpo.isPartialGroupRun() && (execOpts == nil || (execOpts.WebhookInputFile == "" && execOpts.RunKind != "schedule" && execOpts.RunKind != "slack"))
	var selectedRunFolder string
	if isPartialGroupRun {
		// Partial group run — reuse iteration-0 without backup
		hcpo.GetLogger().Info(fmt.Sprintf("📦 Partial group run (%d of %d groups) — reusing iteration-0 without backup", len(execOpts.EnabledGroupNames), len(hcpo.variablesManifest.GetEnabledGroups())))
		selectedRunFolder = currentWorkflowRunFolder
		// Ensure iteration-0 structure exists (no-op if already there)
		iteration0Path := fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), currentWorkflowRunFolder)
		if mkErr := hcpo.createRunFolderStructure(ctx, iteration0Path); mkErr != nil {
			return "", fmt.Errorf("failed to ensure iteration-0: %w", mkErr)
		}
		if indexErr := hcpo.writeRunIndex(ctx, "partial_group_reuse"); indexErr != nil {
			return "", fmt.Errorf("failed to publish partial-group run provenance: %w", indexErr)
		}
	} else {
		// Full run — back up iteration-0 to iteration-N as before
		selectedRunFolder, err = hcpo.prepareCurrentRun(ctx, hcpo.GetWorkspacePath())
	}
	if err != nil {
		return "", fmt.Errorf("failed to resolve run folder: %w", err)
	}
	hcpo.selectedRunFolder = selectedRunFolder
	hcpo.GetLogger().Info(fmt.Sprintf("Using run folder: %s", selectedRunFolder))
	// Set iteration folder for real-time token persistence
	hcpo.SetIterationFolder(selectedRunFolder)
	// Update session working dir to the run's execution folder
	if hcpo.httpSessionID != "" && hcpo.GetWorkspacePath() != "" {
		common.SetSessionWorkingDir(hcpo.httpSessionID, fmt.Sprintf("%s/runs/%s/execution", hcpo.GetWorkspacePath(), selectedRunFolder))
	}

	// EARLY PROGRESS CHECK: Load progress from the selected run folder
	// Note: We no longer check if all steps are completed - execution will proceed regardless

	earlyProgress, err := hcpo.loadStepProgress(ctx)
	planChangeHandled := false // Track if we already handled plan change to avoid duplicate prompts
	if err == nil && earlyProgress != nil && len(earlyProgress.CompletedStepIndices) > 0 {
		hcpo.GetLogger().Info(fmt.Sprintf("📊 Found early progress: %d/%d steps completed", len(earlyProgress.CompletedStepIndices), earlyProgress.TotalSteps))

		// Check if total steps match
		if earlyProgress.TotalSteps == len(breakdownSteps) {
			// Plan matches - proceed with execution (no longer checking if all steps are completed)
			hcpo.GetLogger().Info(fmt.Sprintf("📊 Plan matches existing progress - will proceed with execution"))
		} else {
			// Plan changed - handle based on frontend options or ask user
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Total steps changed (previous: %d, current: %d)", earlyProgress.TotalSteps, len(breakdownSteps)))

			// The current run slot is always reused for interactive execution.
			shouldAsk := hcpo.shouldAskDeleteOldProgress(ctx, hcpo.GetWorkspacePath())
			if !shouldAsk {
				hcpo.GetLogger().Info("📁 No current run progress to reconcile")
				earlyProgress = nil
				planChangeHandled = true
			} else if execOpts != nil && execOpts.PlanChangeAction != "" {
				// Use frontend-provided action
				planChangeHandled = true
				switch execOpts.PlanChangeAction {
				case PlanChangeActionKeepOldProgress:
					hcpo.GetLogger().Info(fmt.Sprintf("✅ Frontend chose to keep old progress (will try to match steps)"))
					// Keep earlyProgress as-is
				case PlanChangeActionDeleteOldProgress:
					hcpo.GetLogger().Info(fmt.Sprintf("🔄 Frontend chose to delete old progress and start fresh"))
					execManager := hcpo.GetExecutionManager()
					if err := execManager.CleanupForPlanChange(ctx, len(breakdownSteps), hcpo.GetWorkspacePath()); err != nil {
						hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Plan change cleanup failed: %v", err))
					}
					earlyProgress = nil
				default:
					hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Unknown plan_change_action: %s, keeping old progress", execOpts.PlanChangeAction))
				}
			} else {
				// No frontend action provided - default to keeping old progress
				// User can select "start from beginning" from frontend if they want to start fresh
				planChangeHandled = true
				hcpo.GetLogger().Info(fmt.Sprintf("ℹ️ No plan_change_action provided, defaulting to keep old progress (user can select 'start from beginning' from frontend if needed)"))
				// Keep earlyProgress as-is
			}
		}
	}

	// Process execution strategy early to set controller state
	// This ensures skipHumanInput is set regardless of code path
	// All execution strategies now skip human input
	if execOpts != nil && execOpts.ExecutionStrategy != "" {
		hcpo.GetLogger().Info(fmt.Sprintf("🔧 Processing execution strategy early: %s", execOpts.ExecutionStrategy))
		hcpo.SetSkipHumanInput(true)

		// Apply human input overrides for routing/human_input steps.
		// Previously this was only set via PrepareExecution → ApplyExecutionContext,
		// which is only called for resume strategies. For start_from_beginning strategies
		// humanInputOverrides was never populated, causing routing steps to miss
		// injected human_input and fall through to their default route.
		if len(execOpts.HumanInputs) > 0 {
			hcpo.humanInputOverrides = execOpts.HumanInputs
			hcpo.GetLogger().Info(fmt.Sprintf("🔧 Set humanInputOverrides for %d step(s) (early apply)", len(execOpts.HumanInputs)))
		}
	}

	// Determine if user wants to resume or start from beginning
	// Frontend always sends either: start from beginning (selectedStartPoint = 0) or resume from step X
	var startFromStep int // 0-based index; the zero value means start from beginning
	var existingProgress *StepProgress
	isResuming := false

	// Check if user explicitly wants to resume
	// CRITICAL: Also check execution strategy, because resume strategy with ResumeFromStep=0
	// should still be treated as resuming (so validation can catch it)
	if execOpts != nil {
		// Check if it's a resume strategy
		isResumeStrategy := execOpts.ExecutionStrategy == ExecutionStrategyResumeFromStepNoHuman ||
			execOpts.ExecutionStrategy == ExecutionStrategyRunSingleStep

		if execOpts.ResumeFromStep > 0 || isResumeStrategy {
			isResuming = true
			hcpo.GetLogger().Info(fmt.Sprintf("🎯 User chose to resume from step (ResumeFromStep=%d, strategy=%s)", execOpts.ResumeFromStep, execOpts.ExecutionStrategy))
		}
	}

	// Use earlyProgress if available, otherwise load it
	if earlyProgress != nil {
		existingProgress = earlyProgress
		hcpo.GetLogger().Info(fmt.Sprintf("✅ Using early progress (avoided reload)"))
	} else {
		// Check if there's existing progress (only if we haven't already handled plan change)
		if !planChangeHandled {
			existingProgress, err = hcpo.loadStepProgress(ctx)
			if err != nil {
				// File doesn't exist - this is normal for first run, log and continue
				hcpo.GetLogger().Info(fmt.Sprintf("ℹ️ No existing progress file found (this is normal for first run), will start fresh execution"))
				existingProgress = nil
			}
		} else {
			// Plan change was already handled, don't reload to avoid duplicate prompts
			hcpo.GetLogger().Info(fmt.Sprintf("ℹ️ Plan change already handled, skipping reload to avoid duplicate prompts"))
			existingProgress = nil
		}
	}

	// Handle two cases: Start from beginning OR Resume from step X
	if !isResuming {
		// Case 1: Start from beginning
		hcpo.GetLogger().Info(fmt.Sprintf("🆕 Starting from beginning"))

		// Clean up execution folder when starting from beginning
		execManager := hcpo.GetExecutionManager()
		if err := execManager.CleanupForStartFromBeginning(ctx, hcpo.GetWorkspacePath()); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Start from beginning cleanup failed: %v", err))
		}
		// Reset progress to nil to ensure fresh initialization (this will reset RoutingEvaluationCounts)
		existingProgress = nil
		earlyProgress = nil // Also clear earlyProgress to ensure old counts don't persist
		// startFromStep is already 0 from initialization
	} else {
		// Case 2: Resume from step X
		hcpo.GetLogger().Info(fmt.Sprintf("🔄 Resuming from step"))

		// Load existing progress if available
		if existingProgress == nil {
			existingProgress, err = hcpo.loadStepProgress(ctx)
			if err != nil {
				hcpo.GetLogger().Info(fmt.Sprintf("ℹ️ No existing progress file found, will start from step specified by frontend"))
				existingProgress = nil
			}
		}

		// Handle plan change if steps don't match
		if existingProgress != nil && !planChangeHandled && existingProgress.TotalSteps != len(breakdownSteps) {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Plan has changed (previous: %d steps, current: %d steps)", existingProgress.TotalSteps, len(breakdownSteps)))

			// Use frontend-provided plan change action if available
			if execOpts != nil && execOpts.PlanChangeAction != "" {
				switch execOpts.PlanChangeAction {
				case PlanChangeActionKeepOldProgress:
					hcpo.GetLogger().Info(fmt.Sprintf("✅ Frontend chose to keep old progress"))
					// Keep existingProgress as-is
				case PlanChangeActionDeleteOldProgress:
					hcpo.GetLogger().Info(fmt.Sprintf("🔄 Frontend chose to delete old progress and start fresh"))
					execManager := hcpo.GetExecutionManager()
					if err := execManager.CleanupForPlanChange(ctx, len(breakdownSteps), hcpo.GetWorkspacePath()); err != nil {
						hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Plan change cleanup failed: %v", err))
					}
					existingProgress = nil
				}
			} else {
				// No frontend action provided - default to keeping old progress
				// User can select "start from beginning" from frontend if they want to start fresh
				hcpo.GetLogger().Info(fmt.Sprintf("ℹ️ No plan_change_action provided, defaulting to keep old progress (user can select 'start from beginning' from frontend if needed)"))
			}
		}

		// Process resume logic if we have existing progress
		if existingProgress != nil {

			hcpo.GetLogger().Info(fmt.Sprintf("📊 Found existing progress: %d/%d steps completed", len(existingProgress.CompletedStepIndices), existingProgress.TotalSteps))

			// Find next incomplete step (used as fallback if resume_from_step not specified)
			nextIncompleteStep := 0
			maxStepsToCheck := existingProgress.TotalSteps
			if maxStepsToCheck > len(breakdownSteps) {
				maxStepsToCheck = len(breakdownSteps)
			}
			for i := 0; i < maxStepsToCheck; i++ {
				completed := false
				for _, completedIdx := range existingProgress.CompletedStepIndices {
					if completedIdx == i {
						completed = true
						break
					}
				}
				if !completed {
					nextIncompleteStep = i + 1
					break
				}
			}
			if nextIncompleteStep == 0 && len(breakdownSteps) > existingProgress.TotalSteps {
				nextIncompleteStep = existingProgress.TotalSteps + 1
			}

			// Use resume_from_step from the frontend when resuming.
			if execOpts != nil {
				if execOpts.ResumeFromStep > 0 {
					// Use explicit step from frontend
					startFromStep = execOpts.ResumeFromStep - 1 // Convert to 0-based
					hcpo.GetLogger().Info(fmt.Sprintf("🎯 Resuming from step %d (from frontend)", execOpts.ResumeFromStep))
				} else if nextIncompleteStep > 0 {
					// Fallback to next incomplete step
					startFromStep = nextIncompleteStep - 1
					hcpo.GetLogger().Info(fmt.Sprintf("🎯 Resuming from next incomplete step %d", nextIncompleteStep))
				} else {
					// No incomplete step found, start from beginning
					hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ No incomplete step found, starting from beginning"))
					startFromStep = 0
				}

				// Handle execution strategy (fast execute, skip human input, etc.)
				if execOpts.ExecutionStrategy != "" {
					switch execOpts.ExecutionStrategy {
					case ExecutionStrategyRunSingleStep:
						hcpo.SetRunSingleStepMode(true, startFromStep)
					default:
						hcpo.SetSkipHumanInput(true)
					}
				}
			} else {
				// No execOpts - next incomplete step would be used as fallback
				// but startFromStep will be set by PrepareExecution if needed
			}
		} else {
			// No existing progress - startFromStep will be set by PrepareExecution if needed
			if execOpts != nil && execOpts.ResumeFromStep > 0 {
				hcpo.GetLogger().Info(fmt.Sprintf("🎯 Resuming from step %d (no existing progress)", execOpts.ResumeFromStep))
			}
		}
	}

	// Apply cleanup if explicitly resuming from a step.
	// This ensures step N and all subsequent steps are cleaned up before execution
	// CRITICAL: Also call PrepareExecution when resume strategy is selected even if ResumeFromStep=0
	// This allows validation to catch invalid resume_from_step=0 and request human feedback
	isResumeStrategy := false
	if execOpts != nil && execOpts.ExecutionStrategy != "" {
		isResumeStrategy = execOpts.ExecutionStrategy == ExecutionStrategyResumeFromStepNoHuman ||
			execOpts.ExecutionStrategy == ExecutionStrategyRunSingleStep
	}

	// Call PrepareExecution if:
	// 1. Resume strategy is selected (even if ResumeFromStep=0, so validation can catch it), OR
	// 2. ResumeFromStep > 0
	if execOpts != nil && (isResumeStrategy || execOpts.ResumeFromStep > 0) {
		execManager := hcpo.GetExecutionManager()

		// Use ExecutionManager to prepare execution setup (includes cleanup scope)
		// This will validate resume_from_step and request human feedback if invalid
		setup, err := execManager.PrepareExecution(ctx, execOpts, existingProgress, len(breakdownSteps), hcpo.selectedRunFolder)
		if err != nil {
			// If PrepareExecution returns error (e.g., user rejected human feedback), stop execution
			hcpo.GetLogger().Error(fmt.Sprintf("❌ Failed to prepare execution setup: %v", err), err)
			return "", fmt.Errorf("execution preparation failed: %w", err)
		} else if setup != nil {
			// Apply cleanup: delete step folders and update progress
			if err := execManager.ApplyCleanup(ctx, setup); err != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to apply cleanup for resume from step %d: %v (continuing anyway)", execOpts.ResumeFromStep, err))
			} else {
				// Log appropriate message based on cleanup scope
				if setup.Cleanup.CleanSpecificStep > 0 {
					hcpo.GetLogger().Info(fmt.Sprintf("✅ Cleaned up step %d for single step execution", setup.Cleanup.CleanSpecificStep))
				} else if setup.Cleanup.CleanFromStep > 0 {
					hcpo.GetLogger().Info(fmt.Sprintf("✅ Cleaned up step %d and all subsequent steps for resume", setup.Cleanup.CleanFromStep))
				} else {
					hcpo.GetLogger().Info(fmt.Sprintf("✅ Applied cleanup for resume (scope: %s)", execManager.GetCleanupDescription(setup.Cleanup)))
				}

				// Reload progress after cleanup to get updated state
				updatedProgress, err := hcpo.loadStepProgress(ctx)
				if err == nil && updatedProgress != nil {
					existingProgress = updatedProgress
					hcpo.GetLogger().Info(fmt.Sprintf("📊 Reloaded progress after cleanup: %d/%d steps completed", len(existingProgress.CompletedStepIndices), existingProgress.TotalSteps))
				}

				// Update startFromStep from setup (in case it was adjusted)
				if setup.StartFromStep >= 0 {
					startFromStep = setup.StartFromStep
					hcpo.GetLogger().Info(fmt.Sprintf("🎯 Updated startFromStep to %d (0-based) from execution setup", startFromStep))
				}
			}
		}
	}

	// Phase 2: Execute plan steps one by one (with validation after each step)

	// Safety check: Ensure breakdownSteps is not empty
	if len(breakdownSteps) == 0 {
		return "", fmt.Errorf(fmt.Sprintf("no steps to execute: breakdownSteps is empty (this should not happen - plan was approved but has no steps)"), nil)
	}

	hcpo.GetLogger().Info(fmt.Sprintf("✅ Proceeding to execution phase with %d steps", len(breakdownSteps)))

	// Build execution context once from current controller state
	execCtx := hcpo.buildExecutionContext()

	// Always use batch execution mode (even for single group) to ensure:
	// - Proper session ID management with actual group name (not "default-group")
	// - Consistent folder structure (runs/iteration-X/group-Y/)
	// - Better isolation and cleanup per group
	enabledGroups := hcpo.getEnabledGroupsForExecution()

	// NOTE: Progress initialization is skipped here because batch execution will handle it per group
	// Each group has its own run folder and progress file, initialized by ApplyCleanup in runBatchExecution
	// This prevents duplicate progress initialization before batch execution starts

	if len(enabledGroups) == 0 {
		return "", fmt.Errorf("no enabled variable groups found for execution")
	}

	// Validate that all groups have valid Names
	for i, group := range enabledGroups {
		if group.Name == "" {
			// PANIC for debugging: name is required for session ID and folder structure
			panic(fmt.Sprintf("CRITICAL: Group at index %d has empty Name - all groups must have valid names for batch execution. Group values: %v", i, group.Values))
		}
	}

	if len(enabledGroups) > 1 {
		hcpo.GetLogger().Info(fmt.Sprintf("🔄 Multiple variable groups detected (%d groups), using batch execution mode", len(enabledGroups)))
	} else {
		hcpo.GetLogger().Info(fmt.Sprintf("🔄 Single variable group detected (%s), using batch execution mode for consistent session ID and folder structure", enabledGroups[0].Name))
	}

	batchResult, err := hcpo.runBatchExecution(ctx, breakdownSteps, 1, execCtx)
	if err != nil {
		return "", fmt.Errorf("batch execution failed: %w", err)
	}
	if !batchResult.Success {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Batch execution completed with %d failed groups", batchResult.FailedGroups))
		duration := time.Since(hcpo.GetStartTime())
		hcpo.GetLogger().Info(fmt.Sprintf("⚠️ Human-controlled todo planning completed with failures in %v", duration))
		errMsg := batchResult.Error
		if errMsg == "" {
			errMsg = fmt.Sprintf("failed group(s): %v", batchResult.FailedGroupNames)
		}
		return "", fmt.Errorf("%s", errMsg)
	}

	duration := time.Since(hcpo.GetStartTime())
	hcpo.GetLogger().Info(fmt.Sprintf("✅ Human-controlled todo planning completed in %v", duration))

	return "Todo planning complete.", nil
}

func (hcpo *StepBasedWorkflowOrchestrator) Execute(ctx context.Context, objective string, workspacePath string, options map[string]interface{}) (string, error) {
	// Validate that no options are provided since this orchestrator doesn't use them
	if len(options) > 0 {
		return "", fmt.Errorf(fmt.Sprintf("human-controlled todo planner orchestrator does not accept options"), nil)
	}

	// Validate workspace path is provided
	if workspacePath == "" {
		return "", fmt.Errorf(fmt.Sprintf("workspace path is required"), nil)
	}

	// Call the existing CreateTodoList method
	hcpo.GetLogger().Info(fmt.Sprintf("🔍 [DEBUG] Execute: About to call CreateTodoList"))
	result, err := hcpo.CreateTodoList(ctx, objective, workspacePath)
	hcpo.GetLogger().Info(fmt.Sprintf("🔍 [DEBUG] Execute: CreateTodoList returned - error=%v", err))
	return result, err
}

// GetType returns the orchestrator type
func (hcpo *StepBasedWorkflowOrchestrator) GetType() string {
	return "human_controlled_todo_planner"
}

// UseKnowledgebase reports whether the knowledgebase prerequisite is enabled.
// Relay product policy disables platform stores. For ordinary workflows the
// knowledgebase is always enabled — the per-preset
// "enable KB" toggle was removed. Steps read it by default; an explicit
// knowledgebase_access="none" opts out, while writes still require an explicit
// contribution contract. This prerequisite no longer acts as a global
// kill-switch.
func (hcpo *StepBasedWorkflowOrchestrator) UseKnowledgebase() bool {
	return hcpo.platformStoresEnabled()
}

// KBShape returns the raw stored shape value. Retained for config compatibility;
// the runtime effective shape is always "notes-only" (see workflowtypes.ResolveKBShape).
func (hcpo *StepBasedWorkflowOrchestrator) KBShape() string {
	return hcpo.kbShape
}

// SetKBShape stores the workflow's KB shape value. Accepted for config compatibility
// with existing presets; runtime behavior is always notes-only regardless.
func (hcpo *StepBasedWorkflowOrchestrator) SetKBShape(v string) {
	hcpo.kbShape = v
}

// Helper methods for human feedback tracking

// getSessionID returns the session ID for this orchestrator
// DEBUG: Panic if sessionID is empty to catch cases where it wasn't set properly
func (hcpo *StepBasedWorkflowOrchestrator) getSessionID() string {
	if hcpo.sessionID == "" {
		// PANIC for debugging: sessionID should always be set (either at controller creation or in batch execution)
		// This helps catch cases where sessionID is not properly initialized
		panic(fmt.Sprintf("CRITICAL: sessionID is empty in StepBasedWorkflowOrchestrator.getSessionID() - this should never happen. SessionID must be set before use."))
	}
	return hcpo.sessionID
}

// getWorkflowID returns the workflow ID for this orchestrator
func (hcpo *StepBasedWorkflowOrchestrator) getWorkflowID() string {
	return hcpo.workflowID
}

// SetRunSingleStepMode sets the single step execution mode
func (hcpo *StepBasedWorkflowOrchestrator) SetRunSingleStepMode(enabled bool, stepIndex int) {
	hcpo.runSingleStepOnly = enabled
	hcpo.singleStepTarget = stepIndex
}

// SetSkipHumanInput sets the skip human input mode (runs learning but skips human feedback)
func (hcpo *StepBasedWorkflowOrchestrator) SetSkipHumanInput(enabled bool) {
	hcpo.skipHumanInput = enabled
	hcpo.GetLogger().Info(fmt.Sprintf("🔧 SetSkipHumanInput called with value: %v", enabled))
}

// IsSkipHumanInput checks if human feedback should be skipped
func (hcpo *StepBasedWorkflowOrchestrator) IsSkipHumanInput() bool {
	return hcpo.skipHumanInput
}

// SetExecutionOptions sets the execution options from frontend
// When set, backend will use these options instead of asking interactively
func (hcpo *StepBasedWorkflowOrchestrator) SetExecutionOptions(options *ExecutionOptions) {
	hcpo.executionOptions = options
}

// GetExecutionOptions returns the current execution options
func (hcpo *StepBasedWorkflowOrchestrator) GetExecutionOptions() *ExecutionOptions {
	return hcpo.executionOptions
}

// buildExecutionContext creates an ExecutionContext from current controller state
// This should be called once at execution start to create an immutable context
func (hcpo *StepBasedWorkflowOrchestrator) buildExecutionContext() *ExecutionContext {
	execCtx := &ExecutionContext{
		SkipHumanInput:    hcpo.skipHumanInput,
		RunSingleStepOnly: hcpo.runSingleStepOnly,
		SingleStepTarget:  hcpo.singleStepTarget,
		HumanInputs:       cloneWorkflowStringMap(hcpo.humanInputOverrides),
	}

	hcpo.GetLogger().Info(fmt.Sprintf("🔧 Built ExecutionContext: skipHumanInput=%v, runSingleStepOnly=%v, singleStepTarget=%d", execCtx.SkipHumanInput, execCtx.RunSingleStepOnly, execCtx.SingleStepTarget))

	return execCtx
}

// cloneWorkflowStringMap prevents per-run input maps from being shared between
// the workshop tool call, controller state, and batch/step execution contexts.
func cloneWorkflowStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

// executionContextForStep derives an isolated context for one step. A keyed
// run_full_workflow human_inputs value becomes prompt context only for its
// matching executable step (or the response for a matching human_input step).
// An explicit execute_step(human_input=...) value remains higher priority.
func executionContextForStep(base *ExecutionContext, stepID string) *ExecutionContext {
	if base == nil {
		return nil
	}
	scoped := *base
	scoped.HumanInputs = cloneWorkflowStringMap(base.HumanInputs)
	if strings.TrimSpace(scoped.WorkshopHumanInput) == "" {
		scoped.WorkshopHumanInput = scoped.HumanInputs[strings.TrimSpace(stepID)]
	}
	return &scoped
}

// HasExecutionOptions checks if execution options are set
func (hcpo *StepBasedWorkflowOrchestrator) HasExecutionOptions() bool {
	return hcpo.executionOptions != nil
}

// formatValidationResponseForTemplate formats validation response data for inclusion in template variables
// This makes validation output available to the execution agent via ValidationFeedback template variable
func (hcpo *StepBasedWorkflowOrchestrator) formatValidationResponseForTemplate(validationResponse *ValidationResponse, context string) string {
	if validationResponse == nil {
		return ""
	}

	var parts []string

	// Add context header
	if context != "" {
		parts = append(parts, fmt.Sprintf("## %s", context))
	}

	// Add reasoning
	if validationResponse.Reasoning != "" {
		parts = append(parts, fmt.Sprintf("**Reasoning**: %s", validationResponse.Reasoning))
	}

	// Add execution status
	if validationResponse.ExecutionStatus != "" {
		parts = append(parts, fmt.Sprintf("**Execution Status**: %s", validationResponse.ExecutionStatus))
	}

	// Add success criteria status
	parts = append(parts, fmt.Sprintf("**Success Criteria Met**: %v", validationResponse.IsSuccessCriteriaMet))

	// Add feedback items
	if len(validationResponse.Feedback) > 0 {
		feedbackParts := []string{"**Feedback**: "}
		for _, fb := range validationResponse.Feedback {
			feedbackParts = append(feedbackParts, fmt.Sprintf("- [%s] %s: %s", fb.Severity, fb.Type, fb.Description))
		}
		parts = append(parts, strings.Join(feedbackParts, "\n"))
	}

	return strings.Join(parts, "\n\n")
}

// conversation history formatting moved to shared.FormatConversationHistory

// SetSelectedRunFolder sets the run folder for the controller (exported for use in server.go chat mode).
func (hcpo *StepBasedWorkflowOrchestrator) SetSelectedRunFolder(folder string) {
	hcpo.selectedRunFolder = folder
	hcpo.SetIterationFolder(folder)
}

// requiresCodeExecutionForProvider checks if the given LLM config uses a
// CLI-based provider that requires code-execution mode to access tools via
// the HTTP bridge. Phase agents normally disable code-exec mode, but these
// providers need it enabled. Source of truth: pkg/common.IsCLIProvider.
func requiresCodeExecutionForProvider(config *AgentLLMConfig) bool {
	if config == nil {
		return false
	}
	return common.IsCLIProvider(config.Provider)
}

// isCliProviderForPrompt checks if the given provider is a CLI runtime
// (claude-code, codex-cli, pi-cli, kimi). CLI providers have their own
// tool-calling capabilities and use a different prompt template than the
// generic code-execution one, even though every agent now runs in
// code-execution mode for HTTP-bridge tool routing.
func isCliProviderForPrompt(provider string) bool {
	return common.IsCLIProvider(provider)
}

// preSavePromptsJSON saves a prompts.json file before agent execution so get_step_prompts works in real time.
// filename: e.g. "todo-task-prompts.json".
func (hcpo *StepBasedWorkflowOrchestrator) preSavePromptsJSON(stepIndex int, stepID, stepPath, agentType, systemPrompt, userMessage, _ string, filename string) {
	go func() {
		var vwp string
		if hcpo.selectedRunFolder != "" {
			vwp = fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
		} else {
			vwp = hcpo.GetWorkspacePath()
		}
		ld := getExecutionFolderPathForLogs(vwp, stepID, stepPath)
		pp := fmt.Sprintf("%s/%s", ld, filename)
		pd := map[string]interface{}{
			"step_index":    stepIndex + 1,
			"step_path":     stepPath,
			"agent_type":    agentType,
			"system_prompt": systemPrompt,
			"user_message":  userMessage,
			"saved_at":      "pre_execution",
			"timestamp":     time.Now().Format(time.RFC3339),
		}
		if pj, e := json.MarshalIndent(pd, "", "  "); e == nil {
			_ = hcpo.WriteWorkspaceFile(context.Background(), pp, string(pj))
		}
	}()
}
