package orchestrator

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// GetDefaultMaxTurnsFromEnv returns the default max turns from environment variable
// Checks MAX_TURNS and ORCHESTRATOR_MAX_TURNS (in that order)
// Returns 500 if neither is set or invalid
func GetDefaultMaxTurnsFromEnv() int {
	// Check MAX_TURNS first (more general)
	if envVal := os.Getenv("MAX_TURNS"); envVal != "" {
		if maxTurns, err := strconv.Atoi(envVal); err == nil && maxTurns > 0 {
			return maxTurns
		}
	}
	// Fall back to ORCHESTRATOR_MAX_TURNS (orchestrator-specific)
	if envVal := os.Getenv("ORCHESTRATOR_MAX_TURNS"); envVal != "" {
		if maxTurns, err := strconv.Atoi(envVal); err == nil && maxTurns > 0 {
			return maxTurns
		}
	}
	// Default to 500 if neither is set or invalid
	return 500
}

// SecretEntry represents a decrypted secret (name + value, where value can be any text)
type SecretEntry struct {
	Name  string
	Value string
}

// Orchestrator defines the common interface for all orchestrators
type Orchestrator interface {
	// Execute performs the orchestration logic
	Execute(ctx context.Context, objective string, workspacePath string, options map[string]interface{}) (string, error)

	// GetType returns the orchestrator type
	GetType() string
}

// BaseOrchestrator provides unified base functionality for all orchestrators
type BaseOrchestrator struct {
	// Context-aware event bridge for orchestrator-level events
	contextAwareBridge mcpagent.AgentEventListener

	// Logger for the orchestrator
	logger     loggerv2.Logger
	baseLogger loggerv2.Logger

	// Workspace tools for file operations
	WorkspaceTools         []llmtypes.Tool
	WorkspaceToolExecutors map[string]interface{}
	WorkspaceClient        *workspace.Client // Direct typed access to workspace API
	ToolCategories         map[string]string // Tool name to category mapping

	// Orchestrator type and configuration
	orchestratorType OrchestratorType
	startTime        time.Time

	// Common configuration shared between orchestrators
	mcpConfigPath        string
	temperature          float64
	agentMode            string
	selectedServers      []string
	selectedTools        []string      // Selected tools in "server:tool" format
	selectedSkills       []string      // Selected skill folder names for workflow
	secrets              []SecretEntry // Decrypted secrets to inject into agents
	useCodeExecutionMode bool          // MCP code execution mode
	llmConfig            *LLMConfig    // LLM configuration
	maxTurns             int           // Maximum turns for the orchestrator
	cliSecurityPolicy    *llmtypes.CLISecurityPolicy

	// Optional simple state (for workflow orchestrators)
	objective     string
	workspacePath string

	// Folder guard paths for fine-grained access control
	folderGuardReadPaths  []string
	folderGuardWritePaths []string

	// Iteration folder for token persistence (workflow-specific)
	iterationFolder string

	// Context offloading configuration
	largeOutputThreshold int // Token threshold for context offloading (0 = use default: 10000)

	// MCP session ID for connection sharing across agents
	// When set, all agents created by this orchestrator share MCP connections
	mcpSessionID string

	// Reference to the workspace executor env map (from CreateWorkspaceAdvancedToolExecutorsWithSession*)
	// When the MCP session ID changes (e.g., per-group in batch execution), we update
	// MCP_API_URL and MCP_SESSION_ID in this map so that code execution mode shell commands
	// use the correct session-scoped URL for stateful MCP connection reuse.
	workspaceEnvRef map[string]string
	// Mutex protecting concurrent writes to workspaceEnvRef (parallel sub-agents)
	workspaceEnvMu sync.Mutex

	// workspaceVars holds the workflow variable values that belong in the shell
	// as VAR_*, for the same reason `secrets` is held here: SetWorkspaceEnvRef
	// REPLACES the env map, so anything written into the previous map is lost
	// unless this type can put it back.
	//
	// Secrets already had that backfill. Variables did not, so a workflow that
	// loaded its variables early — a single-group workflow auto-loads all of
	// them at workshop start — lost every VAR_* the moment any later
	// initialization stored a fresh env map. Observed on customer-login: 28
	// variables synced at 12:16:15, absent from every env ref afterwards, and
	// the agent re-derived SITE_URL from variables/variables.json with jq.
	// Guarded by workspaceEnvMu.
	workspaceVars map[string]string

	// Browser downloads path (relative to workspace, e.g., "runs/iteration-2/xspaces/execution/Downloads")
	// Set by setupBrowserDownloadsPathOverride when agent-browser is detected.
	// Injected into context via BrowserDownloadsPathKey so the browser executor uses it as working directory.
	browserDownloadsPath string
}

// NewBaseOrchestrator creates a new unified base orchestrator
// Note: provider and model parameters removed - LLM selection uses temp override → step config → preset LLM priority
func NewBaseOrchestrator(
	logger loggerv2.Logger,
	eventBridge mcpagent.AgentEventListener,
	orchestratorType OrchestratorType,
	mcpConfigPath string,
	temperature float64,
	agentMode string,
	selectedServers []string,
	selectedTools []string, // NEW parameter
	useCodeExecutionMode bool, // NEW parameter
	llmConfig *LLMConfig,
	maxTurns int,
	customTools []llmtypes.Tool,
	customToolExecutors map[string]interface{},
	toolCategories map[string]string, // Tool name to category mapping
) (*BaseOrchestrator, error) {

	// Create context-aware event bridge that wraps the main event bridge
	contextAwareBridge := NewContextAwareEventBridge(eventBridge, logger)

	// Load context summarization configuration from environment variables
	// Load large output threshold for context offloading from environment
	// Default to 0 which means use library default (10000 tokens)
	largeOutputThreshold := 0
	if envVal := os.Getenv("LARGE_OUTPUT_THRESHOLD"); envVal != "" {
		if threshold, err := strconv.Atoi(envVal); err == nil && threshold > 0 {
			largeOutputThreshold = threshold
			logger.Info(fmt.Sprintf("🔧 Large output threshold set to %d tokens from env", largeOutputThreshold))
		}
	}

	// Default maxTurns from environment variable when not provided (0).
	// Negative values are preserved to mean "no turn cap".
	if maxTurns == 0 {
		maxTurns = GetDefaultMaxTurnsFromEnv()
		logger.Info(fmt.Sprintf("🔧 MaxTurns not provided or 0, defaulting to %d (from env or 500)", maxTurns))
	}

	// Tool categories are immutable after construction. Copy the caller's map
	// and pre-register the built-in delegation tools so parallel nested
	// orchestrators never write to a shared map during agent creation.
	immutableToolCategories := make(map[string]string, len(toolCategories)+4)
	for name, category := range toolCategories {
		immutableToolCategories[name] = category
	}
	for _, tool := range virtualtools.CreateSubAgentTools() {
		if tool.Function != nil {
			immutableToolCategories[tool.Function.Name] = virtualtools.GetSubAgentToolCategory()
		}
	}

	// Create orchestrator instance
	orchestrator := &BaseOrchestrator{
		contextAwareBridge:     contextAwareBridge,
		logger:                 logger,
		baseLogger:             logger,
		WorkspaceTools:         customTools,
		WorkspaceToolExecutors: customToolExecutors,
		ToolCategories:         immutableToolCategories,
		orchestratorType:       orchestratorType,
		startTime:              time.Now(),
		// Common configuration
		mcpConfigPath:        mcpConfigPath,
		temperature:          temperature,
		agentMode:            agentMode,
		selectedServers:      selectedServers,
		selectedTools:        selectedTools,        // NEW field
		useCodeExecutionMode: useCodeExecutionMode, // NEW field
		llmConfig:            llmConfig,
		maxTurns:             maxTurns,
		// Context offloading configuration
		largeOutputThreshold: largeOutputThreshold,
	}

	// Create workspace client for direct typed API access (used by orchestrator methods).
	// The executors are still used for LLM tool calls, but the client is used for
	// internal operations like ReadWorkspaceFile, ListWorkspaceFiles, etc.
	wsURL := os.Getenv("WORKSPACE_API_URL")
	if wsURL == "" {
		wsURL = "http://127.0.0.1:8081"
	}
	orchestrator.WorkspaceClient = workspace.NewClient(wsURL)

	// Note: No fallback to orchestrator default provider/model - LLM selection uses temp override → step config → preset LLM priority
	// llmConfig.Primary should be populated by the caller if needed

	// Set token persister on bridge (no longer using accumulators)
	contextAwareBridge.SetTokenPersister(orchestrator)
	// PLAT-164: durable background-agent transcript writer.
	contextAwareBridge.SetBackgroundAgentTranscriptWriter(orchestrator)

	return orchestrator, nil
}

// applyIterationFolderToBridge applies the stored iteration folder to the context-aware bridge
// This ensures all agents created by this orchestrator automatically get the iteration folder
func (bo *BaseOrchestrator) applyIterationFolderToBridge() {
	if bo.iterationFolder != "" {
		if bridge, ok := bo.contextAwareBridge.(*ContextAwareEventBridge); ok {
			bridge.SetIterationFolder(bo.iterationFolder)
			bo.GetLogger().Debug(fmt.Sprintf("📁 Applied iteration folder to bridge: %s", bo.iterationFolder))
		}
	}
}

// getMapKeys returns all keys from a map as a slice (helper for logging)
func getMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// SetMCPSessionID sets the MCP session ID for connection sharing across agents
// When set, all agents created by this orchestrator share MCP connections
// Connections persist until CloseSession() is called (not when agents close)
//
// Also updates MCP_API_URL and MCP_SESSION_ID in the workspace executor env map
// (if SetWorkspaceEnvRef was called). This ensures code execution mode shell commands
// use the correct session-scoped URL, preventing session registry misses that cause
// duplicate stateful MCP connections across calls.
func (bo *BaseOrchestrator) SetMCPSessionID(sessionID string) {
	previousSessionID := bo.mcpSessionID
	bo.mcpSessionID = sessionID
	bo.logger.Info(fmt.Sprintf("🔗 Set MCP session ID for connection sharing: %s (previous: %s)", sessionID, previousSessionID))

	// Propagate session change to workspace executor env map for code execution mode
	if bo.workspaceEnvRef != nil && sessionID != "" {
		if baseURL := os.Getenv("MCP_API_URL"); baseURL != "" {
			bo.workspaceEnvMu.Lock()
			oldURL := bo.workspaceEnvRef["MCP_API_URL"]
			newURL := baseURL + "/s/" + sessionID
			bo.workspaceEnvRef["MCP_API_URL"] = newURL
			bo.workspaceEnvRef["MCP_SESSION_ID"] = sessionID
			common.PopulateMCPBridgeShortEnv(bo.workspaceEnvRef)
			bo.workspaceEnvMu.Unlock()
			bo.logger.Info(fmt.Sprintf("🔗 Updated workspace env MCP_API_URL: %s → %s", oldURL, newURL))
			bo.logger.Info(fmt.Sprintf("🔗 Updated workspace env MCP_SESSION_ID: %s", sessionID))
		} else {
			bo.logger.Debug("🔗 MCP_API_URL env not set, skipping workspace env update")
		}
	} else if bo.workspaceEnvRef == nil {
		bo.logger.Debug("🔗 No workspace env ref set, skipping workspace env update (workspaceEnvRef is nil)")
	}
}

// GetMCPSessionID returns the MCP session ID
func (bo *BaseOrchestrator) GetMCPSessionID() string {
	return bo.mcpSessionID
}

// SetWorkspaceEnvRef stores a reference to the workspace executor env map.
// This allows SetMCPSessionID to update MCP_API_URL and MCP_SESSION_ID in-place
// so that code execution mode shell commands automatically use the correct session URL.
// The env map is the same reference used by workspace.Client.ExtraEnv (Go maps are reference types).
//
// Secrets may be loaded before the workspace executor is created (notably for
// workflow-builder sessions). Backfill them here so initialization order cannot
// leave the builder shell without SECRET_* variables that are already attached
// to the workflow.
func (bo *BaseOrchestrator) SetWorkspaceEnvRef(env map[string]string) {
	bo.workspaceEnvMu.Lock()
	common.PopulateMCPBridgeShortEnv(env)
	bo.workspaceEnvRef = env
	if env != nil {
		for _, secret := range bo.secrets {
			if secret.Name == "" {
				continue
			}
			env["SECRET_"+secret.Name] = secret.Value
		}
		// Same reason as the secrets above: this call replaces the map, so any
		// VAR_* written into the previous one is gone unless it is restored
		// here. Without this a workflow silently loses its variables to
		// initialization order and the agent re-derives them from
		// variables/variables.json, or hardcodes them.
		for name, value := range bo.workspaceVars {
			if name == "" {
				continue
			}
			env["VAR_"+name] = value
		}
	}
	bo.workspaceEnvMu.Unlock()
	if env != nil {
		bo.logger.Info(fmt.Sprintf("🔗 Stored workspace env ref (keys: %v, MCP_API_URL=%s, MCP_SESSION_ID=%s, secrets=%d)",
			getMapKeys(env), env["MCP_API_URL"], env["MCP_SESSION_ID"], len(bo.secrets)))
	}
}

// SetWorkspaceVariables records the workflow variable values that must survive
// a later SetWorkspaceEnvRef. Callers still write VAR_* into the live map for
// the current shell; this is what lets them be restored when the map is
// replaced. Values are copied so a caller mutating its own map afterwards
// cannot change what gets backfilled.
func (bo *BaseOrchestrator) SetWorkspaceVariables(values map[string]string) {
	if len(values) == 0 {
		return
	}
	copied := make(map[string]string, len(values))
	for name, value := range values {
		if name == "" {
			continue
		}
		copied[name] = value
	}
	bo.workspaceEnvMu.Lock()
	if bo.workspaceVars == nil {
		bo.workspaceVars = copied
	} else {
		// Merge rather than replace: a later group-scoped load should add to
		// what is already known, not drop variables an earlier one resolved.
		for name, value := range copied {
			bo.workspaceVars[name] = value
		}
	}
	bo.workspaceEnvMu.Unlock()
}

// ReplaceWorkspaceVariables replaces the complete workflow-variable snapshot and
// updates the live workspace shell environment atomically. Use this when a
// variable group has been fully resolved: unlike SetWorkspaceVariables, it also
// removes variables that were renamed or deleted so builder shells cannot see
// stale values from an earlier manifest or group.
func (bo *BaseOrchestrator) ReplaceWorkspaceVariables(values map[string]string) {
	copied := make(map[string]string, len(values))
	for name, value := range values {
		if name != "" {
			copied[name] = value
		}
	}

	bo.workspaceEnvMu.Lock()
	if bo.workspaceEnvRef != nil {
		for name := range bo.workspaceVars {
			delete(bo.workspaceEnvRef, "VAR_"+name)
		}
		for name, value := range copied {
			bo.workspaceEnvRef["VAR_"+name] = value
		}
	}
	bo.workspaceVars = copied
	bo.workspaceEnvMu.Unlock()
}

// GetWorkspaceEnvRef returns the workspace executor env map reference.
// Used to propagate the env ref from parent orchestrators to child orchestrators
// so that MCP_API_URL updates flow through when the session ID changes.
func (bo *BaseOrchestrator) GetWorkspaceEnvRef() map[string]string {
	return bo.workspaceEnvRef
}

// LockWorkspaceEnv locks the workspace env mutex.
// Must be called before writing to the map returned by GetWorkspaceEnvRef
// when concurrent access is possible (e.g. parallel sub-agent execution).
func (bo *BaseOrchestrator) LockWorkspaceEnv() {
	bo.workspaceEnvMu.Lock()
}

// UnlockWorkspaceEnv unlocks the workspace env mutex.
func (bo *BaseOrchestrator) UnlockWorkspaceEnv() {
	bo.workspaceEnvMu.Unlock()
}
