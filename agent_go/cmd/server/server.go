package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"net"
	"net/http"
	_ "net/http/pprof" // Register pprof handlers
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulepolicy"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/cliupdate"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/dominionproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/inspector"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/platformtools"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/sparkquillproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/videoproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	agent "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentwrapper"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/clisecurity"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	orchtypes "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/types"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pulsestore"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/voicestt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
	"github.com/manishiitg/mcpagent/observability"
	"github.com/manishiitg/mcpagent/toolcalllog"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxcapture"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/logger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"

	"github.com/joho/godotenv"

	eventbridge "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/event_bridge"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminalleases"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	browserinstructions "github.com/manishiitg/coding-agent-loop/agent_go/pkg/instructions"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"strconv"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/agent/retainedturn"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

var (
	cleanupClaudeCodeProviderSessions = llmproviders.CleanupClaudeCodeTmuxSessions
	cleanupCodexCLIProviderSessions   = llmproviders.CleanupCodexCLIInteractiveSessions
	cleanupCursorCLIProviderSessions  = llmproviders.CleanupCursorCLIInteractiveSessions
	cleanupPiCLIProviderSessions      = llmproviders.CleanupPiCLIInteractiveSessions
)

var mcpBridgeCustomToolCategories = map[string]bool{
	"workspace":            true,
	"workspace_tools":      true,
	"workspace_browser":    true,
	"workspace_advanced":   true,
	"workspace_image":      true,
	"workspace_image_gen":  true,
	"workspace_image_edit": true,
	"human_tools":          true,
	"delegation_tools":     true,
	"workflow":             true,
	"workflow_creator":     true,
	"knowledgebase_tools":  true,
	"llm_config_tools":     true,
	"secret_tools":         true,
	"notification_tools":   true,
	"skill_tools":          true,
	"mcp_server_tools":     true,
	"activity_status":      true,
	"auto_improvement":     true,
}

var mcpBridgeVirtualToolCategories = map[string]bool{}

// productEnabled reports whether a product's profiles and skills should be
// loaded by this server. An unset AGENT_PRODUCTS preserves the shared-server
// behavior of loading every product. Dedicated deployments can set a
// comma-separated allowlist (for example, "video-studio") so unrelated
// product startup failures cannot take down their agent API.
func productEnabled(product string) bool {
	configured := strings.TrimSpace(os.Getenv("AGENT_PRODUCTS"))
	if configured == "" {
		return true
	}
	for _, candidate := range strings.Split(configured, ",") {
		if strings.EqualFold(strings.TrimSpace(candidate), product) {
			return true
		}
	}
	return false
}

// isSingleProductServerDeployment reports whether this server instance is
// dedicated to exactly one product surface (Video Studio, Dominion, SparkQuill)
// via AGENT_PRODUCTS, as opposed to the shared desktop/multi-product server
// where AGENT_PRODUCTS is unset. Only a genuinely dedicated deployment is
// eligible for the missing-Claude-Code-token refusal in handleQuery: on the
// desktop app, falling back to the user's own locally logged-in `claude` CLI
// is correct, intended behavior, not a bug to guard against.
func isSingleProductServerDeployment() bool {
	configured := strings.TrimSpace(os.Getenv("AGENT_PRODUCTS"))
	if configured == "" {
		return false
	}
	count := 0
	for _, candidate := range strings.Split(configured, ",") {
		if strings.TrimSpace(candidate) != "" {
			count++
		}
	}
	return count == 1
}

// claudeCodeTokenMissingForSingleProductDeployment reports whether handleQuery
// must refuse this request: an active product profile resolves to the
// claude-code provider with no configured token, and either (a) this server
// is a dedicated single-product deployment (see
// isSingleProductServerDeployment -- the default safety net every product
// gets for free on such a deployment, no manifest change needed), or (b) the
// profile itself declares Runtime.RequireProviderToken (product.yaml opt-in
// for a profile that must never rely on an ambient CLI login regardless of
// deployment context).
func claudeCodeTokenMissingForSingleProductDeployment(resolvedProfile *resolvedAgentProfile, finalProvider string) bool {
	if resolvedProfile == nil || !strings.EqualFold(finalProvider, "claude-code") {
		return false
	}
	if !isSingleProductServerDeployment() && !resolvedProfile.Definition.Runtime.RequireProviderToken {
		return false
	}
	if resolvedProfile.APIKeys == nil || resolvedProfile.APIKeys.ClaudeCodeOAuthToken == nil {
		return true
	}
	return strings.TrimSpace(*resolvedProfile.APIKeys.ClaudeCodeOAuthToken) == ""
}

func normalizeMCPBridgeCategory(name string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), "-", "_"))
}

func isMCPBridgeCustomToolCategory(name string) bool {
	return mcpBridgeCustomToolCategories[normalizeMCPBridgeCategory(name)]
}

func isMCPBridgeVirtualToolCategory(name string) bool {
	return mcpBridgeVirtualToolCategories[normalizeMCPBridgeCategory(name)]
}

// runtimeMCPServers removes legacy custom-tool categories from a workflow's
// selected-server list. Older workflow.json files stored category labels such
// as workspace_advanced alongside real MCP servers. Those labels are used when
// registering direct tools later in setup; they must never be handed to the MCP
// connection layer as server names.
func runtimeMCPServers(selected []string) []string {
	servers := make([]string, 0, len(selected))
	for _, name := range selected {
		if isMCPBridgeCustomToolCategory(name) || isMCPBridgeVirtualToolCategory(name) {
			continue
		}
		servers = append(servers, name)
	}
	if len(servers) == 0 {
		return []string{mcpclient.NoServers}
	}
	return servers
}

// stepDelegationRegistry maps a workshop step's ForceCorrelationID ("workshop-step-*") to the
// delegation IDs spawned within that step. This lets query_step include tool calls from API-based
// delegation sub-agents that use their own correlation ID ("delegation-<index>-<ts>") instead of
// the parent step's correlation. CLI sub-agents share the parent's MCP session ID and are already
// covered by the toolcalllog prefix scan, so they don't need this registry.
var (
	stepDelegationMu  sync.RWMutex
	stepDelegationMap = make(map[string][]string) // workshopStepCorrelationID → []delegationID
)

func getStepDelegations(workshopStepCorrelationID string) []string {
	stepDelegationMu.RLock()
	defer stepDelegationMu.RUnlock()
	ids := stepDelegationMap[workshopStepCorrelationID]
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, len(ids))
	copy(out, ids)
	return out
}

func cleanupStepDelegation(workshopStepCorrelationID string) {
	stepDelegationMu.Lock()
	defer stepDelegationMu.Unlock()
	delete(stepDelegationMap, workshopStepCorrelationID)
}

const envMCPServerAPIToken = "MCP_SERVER_API_TOKEN"

// envBridgeTokenSecret pins the session-token signing secret across restarts
// (so retained CLI sessions keep working). It is read once and removed from
// the environment; without it a random secret is made per start.
const envBridgeTokenSecret = "MCP_BRIDGE_TOKEN_SECRET"

func resolveBridgeTokenSecret() string {
	secret := strings.TrimSpace(os.Getenv(envBridgeTokenSecret))
	os.Unsetenv(envBridgeTokenSecret)
	if secret != "" {
		return secret
	}
	return common.NewBridgeTokenSecret()
}

func resolveServerAPIToken() string {
	if token := strings.TrimSpace(os.Getenv(envMCPServerAPIToken)); token != "" {
		return token
	}
	return executor.GenerateAPIToken()
}

func seedMCPBridgeCodeExecRegistry(logger loggerv2.Logger) {
	advancedExecutors := virtualtools.CreateWorkspaceAdvancedToolExecutors()
	browserExecutors := virtualtools.CreateWorkspaceBrowserToolExecutors()

	bridgeExecutors := make(map[string]func(context.Context, map[string]interface{}) (string, error), 3)
	for _, name := range []string{"execute_shell_command", "diff_patch_workspace_file"} {
		if exec, ok := advancedExecutors[name]; ok {
			bridgeExecutors[name] = exec
		}
	}
	if exec, ok := browserExecutors["agent_browser"]; ok {
		bridgeExecutors["agent_browser"] = exec
	}

	codeexec.InitRegistryWithVirtualTools(nil, bridgeExecutors, nil, nil, logger)
}

// ServerCmd represents the server command
var ServerCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the streaming API server",
	Long: `Start the streaming API server that provides HTTP endpoints and Server-Sent Events (SSE) support 
for real-time agent streaming. This server enables frontend integration with the MCP agent.

The server provides:
- REST API endpoints for agent queries
- Server-Sent Events (SSE) for real-time streaming
- Polling API for event retrieval
- Multi-provider LLM support (Bedrock, OpenAI, Anthropic)
- Full observability and tracing

Examples:
  mcp-agent server                           # Start server with default settings
  mcp-agent server --port 8000              # Start on custom port
  mcp-agent server --provider openai        # Use OpenAI provider
  mcp-agent server --cors-origins "https://app.example.com"  # Allow a specific browser origin`,
	Run: runServer,
}

// Server configuration
type ServerConfig struct {
	Port          int      `json:"port"`
	Host          string   `json:"host"`
	CORSOrigins   []string `json:"cors_origins"`
	Provider      string   `json:"provider"`
	ModelID       string   `json:"model_id"`
	Temperature   float64  `json:"temperature"`
	MaxTurns      int      `json:"max_turns"`
	MCPConfigPath string   `json:"mcp_config_path"`
}

// ActiveSessionInfo represents an active session for page refresh recovery
type ActiveSessionInfo struct {
	SessionID string `json:"session_id"`
	// TurnProvider is the principal the current turn runs as (its claims
	// provider): the web chat's login provider, "bot_user" for the person's
	// 1:1 Slack DM, "bot_owner" for their WhatsApp, "bot_route" for a shared
	// Slack channel route. Set on every turn, never inherited.
	TurnProvider                string           `json:"-"`
	ParentSessionID             string           `json:"parent_session_id,omitempty"`
	SessionKind                 string           `json:"session_kind,omitempty"`
	AgentMode                   string           `json:"agent_mode"`
	Status                      string           `json:"status"` // "running", "paused", "completed"
	LastActivity                time.Time        `json:"last_activity"`
	CreatedAt                   time.Time        `json:"created_at"`
	Query                       string           `json:"query,omitempty"`
	Title                       string           `json:"title,omitempty"`
	WorkflowName                string           `json:"workflow_name,omitempty"`
	WorkflowLabel               string           `json:"workflow_label,omitempty"`
	WorkspacePath               string           `json:"workspace_path,omitempty"`
	PresetName                  string           `json:"preset_name,omitempty"`
	PresetQueryID               string           `json:"preset_query_id,omitempty"`
	PhaseID                     string           `json:"phase_id,omitempty"`
	PhaseName                   string           `json:"phase_name,omitempty"`
	WorkshopMode                string           `json:"workshop_mode,omitempty"`
	BotPlatform                 string           `json:"bot_platform,omitempty"`
	TriggeredBy                 string           `json:"triggered_by,omitempty"`
	TriggeredByLabel            string           `json:"triggered_by_label,omitempty"`
	LLMGuidance                 string           `json:"llm_guidance,omitempty"` // LLM guidance message for this session
	ChatsFolder                 string           `json:"chats_folder,omitempty"` // Per-user Chats folder (default: _users/<userID>/Chats)
	UserID                      string           `json:"-"`                      // User ID for session isolation (not exposed in JSON)
	Username                    string           `json:"-"`                      // Human-readable owner for logs only (not authorization)
	IsSyntheticTurn             bool             `json:"is_synthetic_turn"`      // True when running an auto-notification turn (not user-initiated)
	HasRunningBackgroundAgents  bool             `json:"has_running_background_agents,omitempty"`
	RunningBackgroundAgentCount int              `json:"running_background_agent_count,omitempty"`
	HasRetainedTmuxSession      bool             `json:"has_retained_tmux_session,omitempty"`
	CurrentExecutionName        string           `json:"current_execution_name,omitempty"`
	NeedsUserInput              bool             `json:"needs_user_input,omitempty"`
	WaitingEventType            string           `json:"waiting_event_type,omitempty"`
	WaitingMessage              string           `json:"waiting_message,omitempty"`
	WaitingSince                *time.Time       `json:"waiting_since,omitempty"`
	DisplayStatus               string           `json:"display_status,omitempty"`
	CanSteer                    bool             `json:"can_steer,omitempty"`
	RuntimeState                *RuntimeSnapshot `json:"runtime_state,omitempty"`
}

// StreamingAPI represents the streaming API server
type StreamingAPI struct {
	externalBuilderRuntime      externalBuilderRuntime
	internalExternalBuilderTurn func(context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error)
	postSlackMessage            func(context.Context, string, string, string) (string, error) // test seam; production uses SlackService

	botExecutionSessions sync.Map // session -> trusted connector request context

	playwrightLive      playwrightLiveRegistry
	accessTokenSessions accessTokenSessionRegistry
	uiControlOnce       sync.Once
	uiControl           *uiControlBroker
	config              ServerConfig
	cliSecurityStore    *clisecurity.Store
	agentProfiles       *agentprofiles.Registry
	productSchedules    *ProductScheduleService

	// internalQueryHandler is a narrow test seam for server-owned follow-up
	// turns. Production dispatch falls back to handleQuery.
	internalQueryHandler func(http.ResponseWriter, *http.Request)
	// internalUserMessageDeliveryHandler lets routing contract tests observe
	// delivery at the AgentWorks boundary. Production dispatch falls back to
	// Agent.DeliverUserMessage.
	internalUserMessageDeliveryHandler func(context.Context, *mcpagent.Agent, mcpagent.UserMessageDeliveryRequest) (mcpagent.UserMessageDeliveryResult, error)
	// internalRetainedTerminalInputHandler lets routing tests cover the
	// between-turn case where the Go Agent object has been released but the
	// provider-owned main tmux pane is still alive. Production dispatch uses the
	// provider's typed live-input entry point.
	internalRetainedTerminalInputHandler func(context.Context, llmproviders.Provider, string, string, string) error
	// internalRetainedTurnFinalResponseReader is the test seam for the read-only
	// coding-CLI sidecar lookup used after a directly injected turn completes.
	internalRetainedTurnFinalResponseReader func(llmproviders.Provider, string, time.Time) string
	// internalLiveInputPersistenceHandler lets routing tests verify that every
	// provider-confirmed live delivery is also written to durable chat history.
	internalLiveInputPersistenceHandler func(userID, sessionID, message string)
	// internalDurableAckHandler lets routing tests observe the
	// durability half of the two-stage delivery receipt without a real
	// CLI. Production dispatch falls back to the provider's typed
	// durable-ack await (live_input_durable.go).
	internalDurableAckHandler func(context.Context, llmproviders.Provider, string, string) (llmtypes.DurableAck, error)
	// internalLiveInputDeliver replaces the synchronous live-input send in
	// tests of the fast-response window.
	internalLiveInputDeliver func(w http.ResponseWriter, r *http.Request, sessionID, message, queryID string) bool
	// internalAgentToolsModeDecided lets tests see the agent-tools mode a
	// turn decided (after provider-account rules) and stop the turn there.
	internalAgentToolsModeDecided func(sessionID, mode string) bool
	// internalSteerTransportReady lets gate tests observe steer
	// readiness without a real CLI registry. Production dispatch
	// checks the provider's interactive-session registration.
	internalSteerTransportReady             func(provider, sessionID string) bool
	internalChatSubmissionStore             *chatSubmissionStore
	internalUncertainSubmissionRetryChecker func(context.Context, chatSubmissionRecord) bool
	// Test seams for the uncertain-submission adopt wait. Zero keeps the
	// production timeout and poll interval.
	internalUncertainSubmissionAdoptTimeout      time.Duration
	internalUncertainSubmissionAdoptPollInterval time.Duration

	// Note: Removed session management - fresh agents created per request

	// Agent cancel functions for proper context cancellation: sessionID -> context.CancelFunc
	agentCancelFuncs map[string]context.CancelFunc
	agentCancelMux   sync.RWMutex

	// Workflow orchestrator sessions: sessionID -> orchestrator.Orchestrator

	// Workflow orchestrator contexts for cancellation: queryID -> context.CancelFunc
	// Using queryID (not sessionID) ensures each execution is independent
	workflowOrchestratorContexts   map[string]context.CancelFunc
	workflowOrchestratorContextMux sync.RWMutex

	// Active workflow executions registry (in-memory, source of truth for "currently running")
	activeWorkflowExecutions    map[string]*ActiveWorkflowExecution // queryID -> execution info
	activeWorkflowExecutionsMux sync.RWMutex

	// Unified execution tracker for top-level workflow runs and workflow-builder background work.
	trackedWorkflowExecutions    map[string]*TrackedWorkflowExecution
	trackedWorkflowExecutionsMux sync.RWMutex

	// Mapping of sessionID -> []queryID to track which executions belong to which session
	// Used by handleStopSession to cancel all executions for a session
	sessionQueryIDs   map[string][]string
	sessionQueryIDMux sync.RWMutex

	// Workflow objectives: sessionID -> objective
	workflowObjectives   map[string]string
	workflowObjectiveMux sync.RWMutex

	// Workflow step IDs: presetQueryID -> stepID (temporary storage for step-specific phase execution)
	workflowStepIDs   map[string]string
	workflowStepIDMux sync.RWMutex

	// Conversation history storage: sessionID -> conversation history
	conversationHistory map[string][]llmtypes.MessageContent
	conversationMux     sync.RWMutex

	// Restored coding-agent persistence targets. Keyed by current Runloop
	// sessionID so follow-up turns after a native resume continue updating the
	// original conversation JSON instead of creating new chat-history entries.
	restoredConversationPersistTargets map[string]restoredChatHistoryPersistTarget
	restoredConversationPersistMux     sync.RWMutex

	// Operator-state store: bot connector configs, chat history, and the
	// encrypted secret boxes (workflow/project/product + managed globals).
	chatStore chathistory.Store

	// Global immutable cost event repository (SQLite in production).
	costLedger *costledger.Ledger
	// In-memory inspector event store. Holds the rolling debug-event
	// timeline per session for the inspector panel. Per-session ring
	// buffer; not persisted. Sessions opt in via
	// POST /api/inspector/<session>/enable (see inspector_routes.go).
	inspectorStore *inspector.Store

	// Polling system components
	eventStore *events.EventStore

	// View-only runtime terminal snapshots for coding-agent TUI streams.
	terminalStore *terminals.Store
	// Process ownership is independent from terminal snapshot visibility and
	// retention. The lease registry remains authoritative after UI dismissal.
	terminalLeaseRegistry *terminalleases.Registry
	terminalLeaseMux      sync.Mutex

	// Phase 1 observer: immutable aggregate runtime snapshots. Existing stores
	// remain authoritative until observer comparisons are proven in production.
	runtimeCoordinator *RuntimeCoordinator

	// Raw tmux pipe-pane recorder used for append/replay terminal display.
	terminalPipeRecorder *terminalPipeRecorder

	// Live-attach terminal transport (control mode). Non-nil when tmux is new
	// enough for control mode; otherwise the /api/terminals/{id}/stream endpoint
	// stays inert (404). It is the transport for the SELECTED live tmux terminal;
	// the snapshot/replay path above still serves the rail / non-selected panes.
	liveAttach *liveAttachManager

	// Short-lived, owner-only PTY sessions used by the guided provider setup
	// wizard. Commands are selected from a server-side allowlist.
	providerSetupMu sync.Mutex
	providerSetups  *providerSetupManager

	// Workflow orchestrator configuration
	provider      string
	model         string
	mcpConfigPath string
	temperature   float64
	workspaceRoot string

	// Active session tracking for page refresh recovery
	activeSessions    map[string]*ActiveSessionInfo
	activeSessionsMux sync.RWMutex

	//nolint:unused // kept for the pending session-reactivation path during the tracker refactor.
	// Session reactivation lock: prevents race conditions when calculating baseIndex
	// and initializing the event store for reactivated sessions
	sessionReactivationMux sync.Mutex

	// stoppedSessions tracks sessions that the user explicitly stopped.
	//
	// BUG FIX (2026-04-04): Race condition between stop and in-flight queries.
	// Timeline of the bug:
	//   1. User clicks stop → handleStopSession closes the WorkshopChatSession
	//      (cancels its context.Background()-derived ctx) and deletes it from
	//      workshopChatSessions map.
	//   2. An in-flight query goroutine (started before or concurrently with stop)
	//      reaches the workshop-creation code and calls NewWorkshopChatSession(),
	//      creating a FRESH session with a new context.Background() — completely
	//      detached from any cancellation.
	//   3. This new workshop spawns step execution goroutines (group sessions,
	//      isolated Codex CLI processes) that are never canceled because no
	//      subsequent stop targets the new workshop.
	//
	// Fix: handleStopSession adds the sessionID here. The query handler checks
	// this set before creating/reusing workshop sessions and bails early.
	// The flag is cleared when the session is explicitly reactivated by a
	// new user message (not by a racing goroutine).
	stoppedSessions   map[string]bool
	stoppedSessionsMu sync.RWMutex

	// interruptedTurns records a user cancel of only the foreground response.
	// Scheduled message sequences consume this marker so an interrupted turn is
	// never mistaken for a successfully completed (idle) turn and advanced.
	interruptedTurns   map[string]bool
	interruptedTurnsMu sync.Mutex

	// Orchestrator objects in memory for guidance injection
	workflowOrchestrators    map[string]orchestrator.Orchestrator
	workflowOrchestratorsMux sync.RWMutex

	// Background agent registry for async delegation in multi-agent mode
	bgAgentRegistry *BackgroundAgentRegistry

	// Session busy tracking — prevents synthetic turns from overlapping with user turns
	sessionBusy      map[string]bool
	sessionBusySince map[string]time.Time
	sessionBusyMu    sync.RWMutex

	// retainedMainTurns tracks direct follow-up turns submitted to a persistent
	// coding-CLI tmux after the server-managed foreground Go turn has ended.
	// Their structured terminal events remain available (the same events used by
	// Terminal Center's Formatted view), so keep an explicit runtime lifecycle
	// instead of guessing activity solely from the latest rendered spinner text.
	retainedMainTurns            map[string]time.Time
	retainedMainTurnExecutionIDs map[string]string
	// A retained CLI can accept another message while its current response is
	// still running. Coding providers queue that input as a distinct turn, so
	// preserve the corresponding execution roots in submission order. Settling
	// the current answer must promote the next root instead of completing every
	// message at the first idle boundary.
	retainedMainTurnPendingExecutionIDs map[string][]string
	retainedMainTurnWatchCancels        map[string]context.CancelFunc
	// retainedMainTurnCompletionEmitted guards emitRetainedMainTurnStreamCompletion
	// against firing twice for one logical turn. Idle-composer detection
	// (observeRetainedMainTurnStream) and a closed control stream that already
	// has a durable final response (handleRetainedMainTurnStreamClosed) can both
	// independently decide the same turn just finished -- without this guard
	// both emit their own unified_completion event carrying the identical final
	// text, rendered as the same answer twice in a row.
	retainedMainTurnCompletionEmitted map[string]time.Time
	retainedMainTurnsMu               sync.Mutex
	// nativeTranscriptSyncInFlight coalesces the post-completion durable sync
	// for live coding-CLI turns. Guarded separately so transcript I/O never
	// blocks the terminal/event observer.
	nativeTranscriptSyncInFlight map[string]bool
	nativeTranscriptSyncPending  map[string]bool
	nativeTranscriptSyncMu       sync.Mutex

	// Pending completions queue — background agent IDs that finished while session was busy
	pendingCompletions map[string][]string
	pendingMu          sync.RWMutex
	// completionRetryScheduled guards schedulePendingCompletionRetry so at most one
	// retry timer runs per session at a time. Guarded by pendingMu.
	completionRetryScheduled map[string]bool

	// Pending start notifications — background agent IDs that started while
	// the main session was busy. These are synthetic user messages just like
	// completion notifications, so they must be serialized with completions.
	pendingStartNotifications       map[string][]string
	startNotificationRetryScheduled map[string]bool // singleton guard — at most one retry timer per session; guarded by pendingStartMu
	pendingStartMu                  sync.RWMutex
	autoNotificationMu              sync.Mutex

	// completionOwners marks sessions whose step completions are delivered by
	// a runner that owns the session (a scheduled run), not by the
	// auto-notification path. sessionID -> *int32 claim count.
	completionOwners sync.Map

	// Last query request per session — used to construct synthetic turns
	lastQueryRequests       map[string]QueryRequest
	lastQueryMu             sync.RWMutex
	sessionWorkspaceFolders map[string]string // sessionID → resolved workflowPhaseFolder (for builder log persistence in synthetic turns)
	sessionWorkspaceMu      sync.RWMutex

	// Stored agent instances for synthetic turns (plan mode only)
	// Reused directly via StreamWithEvents() instead of re-creating agents per synthetic turn
	sessionAgents    map[string]*agent.LLMAgentWrapper
	sessionAgentsMux sync.RWMutex

	// Running agent references for steer message injection (chat mode)
	// Written when agent starts, deleted when streaming completes
	runningAgents    map[string]*mcpagent.Agent
	runningAgentsMux sync.RWMutex

	// Per-session turn lane shared by chat, Crew, workflows and internal
	// producers. Busy retained coding-CLI input is persisted by the dispatcher
	// instead of relying on a provider-local tmux queue.
	sessionInputLanes   map[string]*sessionInputLane
	sessionInputLanesMu sync.Mutex
	// Durable turns waiting behind that shared lane. Every producer reaches the
	// same dispatcher; tmux remains an execution transport, never a queue.
	conversationTurnQueueMu        sync.Mutex
	conversationTurnQueueOwners    map[string]string
	conversationTurnQueueDraining  map[string]bool
	conversationTurnQueueWaiters   map[string]chan queuedConversationTurnResult
	conversationTurnQueueCallbacks map[string]func(event *unifiedevents.AgentEvent)
	internalTurnQueueRead          func(context.Context, string) (string, bool, error)
	internalTurnQueueWrite         func(context.Context, string, string) error

	// Last-seen WorkshopMode per session — used to detect mode toggles between
	// turns. When the mode changes, native coding-agent resume is skipped for
	// that turn (so the new system prompt + tool list actually take effect on
	// the next CLI invocation) and the conversation history is replaced with a
	// synthetic recap so the new agent sees just enough context to continue.
	lastWorkshopModeBySession    map[string]string
	lastChatPolicyBySession      map[string]string
	lastChatPolicyRoleBySession  map[string]string
	lastAgentProfileKeyBySession map[string]string
	lastAgentToolsModeBySession  map[string]string
	// lastWorkflowToolsModeBySession is the agent tools mode a workflow chat
	// last ran with, to start fresh when its native tools switch flips.
	lastWorkflowToolsModeBySession   map[string]string
	launchedAgentProfileKeyBySession map[string]string
	agentProfileAdmissions           sync.Map // *mcpagent.Agent -> immutable launched profile key

	// Interactive workshop chat sessions — per-session controller + step registry
	// Key: sessionID, Value: *todo_creation_human.WorkshopChatSession
	workshopChatSessions sync.Map
	// reportRunSessions: live window.report.run MCP bridge sessions (report_run.go).
	reportRunSessions sync.Map

	// Cron scheduler service for scheduled workflow executions
	scheduler           *SchedulerService
	webhookInvocations  sync.Map // session ID -> *step_based_workflow.WebhookInvocation; internal only
	scheduleInvocations sync.Map // session ID -> *step_based_workflow.ScheduleInvocation; internal only

	// Background completion loop tracking — prevents multiple loops per session
	completionLoopStarted   map[string]bool
	completionLoopStartedMu sync.Mutex

	toolStatus    map[string]ToolStatus
	enabledTools  map[string][]string // queryID/sessionID -> enabled tool names
	toolStatusMux sync.RWMutex
	mcpConfig     *mcpclient.MCPConfig

	// Background tool discovery
	discoveryRunning       bool
	discoveryMux           sync.RWMutex
	lastDiscovery          time.Time
	discoveryTicker        *time.Ticker
	discoveryFailedServers map[string]string // serverName -> error reason (skipped on subsequent discovery cycles)

	// Per-server install/connection logs
	serverLogs    map[string][]ServerLogEntry
	serverLogsMux sync.RWMutex

	// Logger for structured logging
	logger loggerv2.Logger

	// Bot conversation manager for Slack/Discord/Telegram bot sessions
	botManager *services.BotConversationManager

	whatsappManager *services.WhatsAppServiceManager

	// API token for bearer auth on per-tool endpoints (code execution mode)
	apiToken string
}

func spaStaticFileHandler(root string) http.Handler {
	fileServer := http.FileServer(http.Dir(root))
	indexPath := filepath.Join(root, "index.html")

	// serveShell serves the SPA shell with no-cache so an upgraded desktop build's
	// new UI shows up on next launch without a manual hard reload. The agent server
	// runs on a stable localhost origin/port across upgrades, so without this header
	// Chromium's heuristic cache keeps serving the previous index.html (and its old
	// asset references). no-cache lets the browser store but forces revalidation.
	serveShell := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, indexPath)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			fileServer.ServeHTTP(w, r)
			return
		}

		if r.URL.Path == "/" {
			serveShell(w, r)
			return
		}

		relativePath := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if relativePath != "." && !strings.HasPrefix(relativePath, "..") {
			requestedPath := filepath.Join(root, relativePath)
			if info, err := os.Stat(requestedPath); err == nil && !info.IsDir() {
				// Build assets are content-hashed (assets/index-<hash>.js), so a
				// given URL is immutable and safe to cache for a year.
				if strings.HasPrefix(relativePath, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		if _, err := os.Stat(indexPath); err == nil {
			serveShell(w, r)
			return
		}

		fileServer.ServeHTTP(w, r)
	})
}

// QueryRequest represents an agent query request
type QueryRequest struct {
	ExternalBuilderOperationID string   `json:"-"` // Set from authenticated execution claims.
	ConnectionID               string   `json:"connection_id,omitempty"`
	Query                      string   `json:"query"`
	Message                    string   `json:"message,omitempty"`           // Alias for Query (used by frontend)
	SessionTitle               string   `json:"session_title,omitempty"`     // Short UI label for backend-started sessions; never use the full prompt here.
	ParentSessionID            string   `json:"parent_session_id,omitempty"` // Internal child-session ownership used by refresh recovery.
	SessionKind                string   `json:"session_kind,omitempty"`      // Stable runtime kind such as pulse_reviewer; never infer this from titles.
	Servers                    []string `json:"servers,omitempty"`
	EnabledServers             []string `json:"enabled_servers,omitempty"`
	SelectedTools              []string `json:"selected_tools,omitempty"` // Array of "server:tool" strings
	Provider                   string   `json:"provider,omitempty"`
	ModelID                    string   `json:"model_id,omitempty"`
	// ReasoningEffort overrides the "reasoning_effort" key of an agent
	// profile's provider_options[].Options for this turn only; every other
	// key stays as declared. Ignored outside the profile query path.
	ReasoningEffort string                  `json:"reasoning_effort,omitempty"`
	Temperature     float64                 `json:"temperature,omitempty"`
	MaxTurns        int                     `json:"max_turns,omitempty"`
	AgentMode       string                  `json:"agent_mode,omitempty"`
	LLMConfig       *orchestrator.LLMConfig `json:"llm_config,omitempty"`
	LLMConfigSource string                  `json:"llm_config_source,omitempty"`
	PresetQueryID   string                  `json:"preset_query_id,omitempty"`
	LLMGuidance     string                  `json:"llm_guidance,omitempty"` // LLM guidance message
	// AgentProfileID selects a registered, versioned main-agent definition.
	// The normal AgentWorks chat leaves this empty. Product workspaces pass a
	// profile plus trusted workspace scope through the same /api/query runtime.
	AgentProfileID      string                      `json:"agent_profile_id,omitempty"`
	AgentProfileVersion int                         `json:"agent_profile_version,omitempty"`
	AgentProfileContext agentprofiles.PromptContext `json:"agent_profile_context,omitempty"`
	// AgentProfileConversationKey lets the server re-resolve the product's
	// authoritative manifest for this turn. It is never itself a filesystem
	// authority: Work maps its workspace_id through the caller's current grants.
	AgentProfileConversationKey string `json:"agent_profile_conversation_key,omitempty"`
	// Code execution mode: When enabled, only virtual tools are added to LLM
	// MCP tools are accessed through generated scripts using the on-demand HTTP API specification.
	UseCodeExecutionMode bool `json:"use_code_execution_mode,omitempty"`
	// PinRunMode treats this turn as a read-only turn even when the caller
	// has workflow write access, so the session runs with the Run-mode
	// prompt and tool surface. It can only remove privilege, never grant
	// it: authoring stays impossible. Set server-side by the external run
	// tools, which are execution-only by contract.
	PinRunMode bool `json:"pin_run_mode,omitempty"`
	// CrewGuestCaller is the user a Crew turn works for when that user is not
	// the Crew's owner: a function call or ask from someone else's workflow,
	// Crew or external connection runs in the owner's namespace but only
	// as a guest (read-only, may answer and suggest). Honoured only on the
	// owner's own Crew turn, where it can only narrow access.
	CrewGuestCaller string `json:"crew_guest_caller,omitempty"`
	// Execution options from frontend (for workflow execution phase)
	ExecutionOptions *ExecutionOptions `json:"execution_options,omitempty"`
	// Workspace access configuration (legacy field, ignored — workspace is always enabled)
	EnableWorkspaceAccess *bool `json:"enable_workspace_access,omitempty"`
	// Browser automation access configuration
	EnableBrowserAccess *bool `json:"enable_browser_access,omitempty"` // Enable/disable browser automation tool (nil = inherit default, true/false = explicit override)
	// Explicit browser mode from frontend: none|auto|headless|cdp
	BrowserMode string `json:"browser_mode,omitempty"`
	// CDP port for connecting to an existing Chrome browser (local mode only)
	CdpPort *int `json:"cdp_port,omitempty"` // When set and > 0, connect to Chrome via CDP on this port instead of launching headless
	// Explicitly authorized CDP browsers for specialized multi-profile testing.
	// Each port must belong to a separate Chrome --user-data-dir. The legacy
	// cdp_port remains the primary/first port for backward compatibility.
	CdpPorts []int `json:"cdp_ports,omitempty"`
	// Selected skills to include in chat context
	SelectedSkills []string `json:"selected_skills,omitempty"` // Array of skill folder names
	// BotPlatform identifies the chat channel the session is talking through
	// (e.g. "slack", "whatsapp"). Set by the bot manager when wiring a bot
	// session; empty for chat-UI sessions. Drives channel-specific system
	// prompt additions (formatting rules), so bot replies render correctly.
	BotPlatform string `json:"bot_platform,omitempty"`
	// BotChannelID and BotThreadTS identify the originating connector
	// conversation so human tools can notify the same Slack/WhatsApp thread.
	BotChannelID string `json:"bot_channel_id,omitempty"`
	BotThreadTS  string `json:"bot_thread_ts,omitempty"`
	// BotConnectionID names the Slack app connection the originating
	// conversation lives on (empty = platform default). Set from the
	// arrival connection for bot sessions — which wins over the manifest
	// selection — and from the manifest otherwise.
	BotConnectionID string `json:"bot_connection_id,omitempty"`
	// BotUser* identifies the external chat user who created or continued the
	// bot thread. These fields are display metadata only, not authorization.
	BotUserID    string `json:"bot_user_id,omitempty"`
	BotUserName  string `json:"bot_user_name,omitempty"`
	BotUserEmail string `json:"bot_user_email,omitempty"`
	// Internal workflow wire field: name of the selected encrypted secret that
	// contains a Slack Incoming Webhook URL. The URL itself is never serialized.
	NotificationSlackWebhookSecretName string `json:"notification_slack_webhook_secret_name,omitempty"`
	// Per-workflow notification preferences resolved from workflow.json
	// notifications.*. Channel and recipient rules are backend-enforced;
	// owner-authored content preferences are visible to Workflow Builder and
	// used by the Pulse finalizer for their matching notification sections.
	NotificationRunSummaryInstructions         string   `json:"notification_run_summary_instructions,omitempty"`
	NotificationPulseSummaryInstructions       string   `json:"notification_pulse_summary_instructions,omitempty"`
	NotificationRunSummaryChannels             []string `json:"notification_run_summary_channels,omitempty"`
	NotificationPulseSummaryChannels           []string `json:"notification_pulse_summary_channels,omitempty"`
	NotificationExcludeChannels                []string `json:"notification_exclude_channels,omitempty"`
	NotificationBlockRecipients                []string `json:"notification_block_recipients,omitempty"`
	NotificationGmailConnectionID              string   `json:"notification_gmail_connection_id,omitempty"`
	NotificationRunSummaryGmailConnectionIDs   []string `json:"notification_run_summary_gmail_connection_ids,omitempty"`
	NotificationPulseSummaryGmailConnectionIDs []string `json:"notification_pulse_summary_gmail_connection_ids,omitempty"`
	NotificationRunSummaryRecipients           []string `json:"notification_run_summary_recipients,omitempty"`
	NotificationPulseSummaryRecipients         []string `json:"notification_pulse_summary_recipients,omitempty"`
	// Per-summary Slack channels, as encrypted-secret names holding webhook URLs.
	NotificationRunSummarySlackWebhookSecretNames   []string `json:"notification_run_summary_slack_webhook_secret_names,omitempty"`
	NotificationPulseSummarySlackWebhookSecretNames []string `json:"notification_pulse_summary_slack_webhook_secret_names,omitempty"`
	// Internal-only resolved notification credentials. Unlike DecryptedSecrets,
	// these values are never serialized, prompted, or injected as SECRET_*.
	// notificationSlackWebhookURL is the default/fallback webhook;
	// notificationSlackWebhookURLs holds every resolved webhook by secret name,
	// including the per-summary channels.
	notificationSlackWebhookURL  string            `json:"-"`
	notificationSlackWebhookURLs map[string]string `json:"-"`
	// Delegation tier configuration: Maps reasoning levels (high/medium/low) to specific provider/model pairs
	DelegationTierConfig *virtualtools.DelegationTierConfig `json:"delegation_tier_config,omitempty"`
	// Decrypted secrets to inject into agent system prompt
	DecryptedSecrets []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"decrypted_secrets,omitempty"`
	// Selected global secret names to include (if nil/absent, all global secrets are included)
	SelectedGlobalSecrets *[]string `json:"selected_global_secrets,omitempty"`
	// Workspace paths of workflows to inject context for (via # selector in chat)
	WorkflowContextPaths []string `json:"workflow_context_paths,omitempty"`
	// WorkflowContextRefs carries the label the user saw for each # reference
	// (#crew:<label> / #workflow:<label>). Display only: authorization and
	// kind always come from the paths.
	WorkflowContextRefs []workflowContextRef `json:"workflow_context_refs,omitempty"`
	// authorizedWorkflowContextReadPaths are server-resolved folder-guard roots.
	// They differ from WorkflowContextPaths for same-account Crew references,
	// whose durable logical path resolves below _users/<id>/ at authorization.
	authorizedWorkflowContextReadPaths []string `json:"-"`
	// Conversation JSON selected from /resume or previous chats. Used to seed
	// native coding-agent resume state from its saved runtime metadata.
	RestoredConversationPath string `json:"restored_conversation_path,omitempty"`
	// Previous persisted chat session to resume from when callers do not know
	// the conversation JSON path (for example Slack/WhatsApp bot channels).
	RestoredConversationSessionID string `json:"restored_conversation_session_id,omitempty"`
	// resolvedResumeTarget is attached only by a trusted server-owned product
	// route. It is never accepted from or serialized back to a browser.
	resolvedResumeTarget *resolvedResumeTarget `json:"-"`

	// Workflow phase chat: phase ID for running a phase as a conversational chat session
	// When agent_mode is "workflow_phase", this specifies which phase to run (e.g., "planning", "plan-improvement")
	PhaseID string `json:"phase_id,omitempty"`

	// Workspace path passed directly (used by scheduler to bypass preset lookup)
	SelectedFolder string `json:"selected_folder,omitempty"`

	// Triggered by: "manual", "cron" — for tracking execution source
	TriggeredBy string `json:"triggered_by,omitempty"`
	// TriggeredByLabel names who or what started the run, for display only
	// ("Called by RTS Flow Tester", "Schedule: Daily digest").
	TriggeredByLabel string `json:"triggered_by_label,omitempty"`
	// Auto-notification flag: when true, this is a background agent completion notification
	// (not user-initiated). Backend treats it as a synthetic turn so frontend doesn't block input.
	IsAutoNotification bool `json:"is_auto_notification,omitempty"`
	// Internal/system callers can force a real server-tracked turn instead of the
	// retained coding-CLI live-input shortcut. Scheduler sequences rely on this so
	// the next cron message waits for turn completion instead of racing a tmux
	// snapshot that may not have flipped to busy yet.
	DisableLiveInputDelivery bool `json:"disable_live_input_delivery,omitempty"`
	// KeepNativeSessionAlive was how a scheduler kept one native coding-CLI
	// process alive across its consecutive turns (run → Pulse). Every
	// chat-level turn now retains its CLI (codingAgentRequestAllowsPersistentInteractive),
	// so it no longer changes the transport; kept for wire compatibility with
	// callers that still send it.
	KeepNativeSessionAlive bool `json:"keep_native_session_alive,omitempty"`
	// PulseLifecycleTurn marks a scheduler-sent Pulse turn (Gate, review
	// dispatch, Finalize). The main conversation keeps the Builder model on
	// every turn; this flag only routes the background review agents that
	// turn launches (plan drift / technical / strategic review) to pulse_llm.
	PulseLifecycleTurn bool `json:"pulse_lifecycle_turn,omitempty"`
	// UserInteractiveContinuation promotes an observed schedule/bot conversation
	// into an interactive chat without changing its session or native resume ID.
	UserInteractiveContinuation bool `json:"user_interactive_continuation,omitempty"`
	// Internal: user ID for synthetic turn reconstruction (not from JSON)
	userID string `json:"-"`
}

func buildWorkflowNotificationInstructionsPrompt(runInstructions, pulseInstructions string) string {
	runInstructions = strings.TrimSpace(runInstructions)
	pulseInstructions = strings.TrimSpace(pulseInstructions)
	if runInstructions == "" && pulseInstructions == "" {
		return ""
	}
	prompt := "\n## Workflow Notification Preferences\n\n" +
		"The workflow owner saved the following workflow-scoped guidance for notification content. " +
		"Apply each preference only to its named section when designing, reviewing, or composing this workflow's notifications. " +
		"It controls content, detail, tone, emphasis, subject/header/sign-off conventions, and similar presentation preferences only. " +
		"It does not authorize changing recipients, channels, secrets, permissions, tools, schedules, plan behavior, or delivery configuration. " +
		"Do not copy it into soul/soul.md; workflow.json is the source of truth.\n"
	if runInstructions != "" {
		prompt += "\n### Workflow run summary\n<workflow_run_summary_preferences>\n" + runInstructions + "\n</workflow_run_summary_preferences>\n"
	}
	if pulseInstructions != "" {
		prompt += "\n### Pulse review summary\n<pulse_review_summary_preferences>\n" + pulseInstructions + "\n</pulse_review_summary_preferences>\n"
	}
	return prompt
}

func notificationDestinationFromQuery(req QueryRequest, userID string) *services.NotificationDestination {
	platform := strings.ToLower(strings.TrimSpace(req.BotPlatform))
	dest := &services.NotificationDestination{
		UserID:        userID,
		WorkflowName:  workflowNameFromWorkspacePath(req.SelectedFolder),
		WorkspacePath: strings.TrimSpace(req.SelectedFolder),
	}
	if req.ExecutionOptions != nil && len(req.ExecutionOptions.RouteSelections) > 0 {
		dest.RouteSelections = maps.Clone(req.ExecutionOptions.RouteSelections)
	}
	switch platform {
	case "slack":
		if req.BotChannelID != "" {
			dest.Slack = &services.SlackDest{
				ChannelID:    req.BotChannelID,
				ThreadTS:     req.BotThreadTS,
				ConnectionID: strings.TrimSpace(req.BotConnectionID),
			}
		}
	case "whatsapp":
		if req.BotChannelID != "" {
			dest.WhatsApp = &services.WhatsAppDest{
				ChannelID: req.BotChannelID,
			}
		}
	}
	if secretName := strings.TrimSpace(req.NotificationSlackWebhookSecretName); secretName != "" {
		dest.SlackWebhook = &services.SlackWebhookDest{
			SecretName: secretName,
			URL:        req.notificationSlackWebhookURL,
		}
	}
	// Per-summary Slack channels. A name whose secret did not resolve is dropped
	// rather than carried as an empty URL, so a missing credential shows up as
	// "this channel was skipped" instead of a silent post to nowhere.
	webhooksFor := func(names []string) []services.SlackWebhookDest {
		var webhooks []services.SlackWebhookDest
		for _, name := range uniqueNonEmpty(names) {
			url := strings.TrimSpace(req.notificationSlackWebhookURLs[name])
			if url == "" {
				continue
			}
			webhooks = append(webhooks, services.SlackWebhookDest{SecretName: name, URL: url})
		}
		return webhooks
	}
	dest.RunSummaryWebhooks = webhooksFor(req.NotificationRunSummarySlackWebhookSecretNames)
	dest.PulseSummaryWebhooks = webhooksFor(req.NotificationPulseSummarySlackWebhookSecretNames)
	// Per-workflow notification preferences (workflow.json notifications.*).
	if len(req.NotificationExcludeChannels) > 0 {
		dest.ExcludeChannels = append([]string(nil), req.NotificationExcludeChannels...)
	}
	dest.RunSummaryChannels = append([]string(nil), req.NotificationRunSummaryChannels...)
	dest.PulseSummaryChannels = append([]string(nil), req.NotificationPulseSummaryChannels...)
	dest.RunSummaryGmailConnectionIDs = append([]string(nil), req.NotificationRunSummaryGmailConnectionIDs...)
	dest.PulseSummaryGmailConnectionIDs = append([]string(nil), req.NotificationPulseSummaryGmailConnectionIDs...)
	dest.RunSummaryRecipients = append([]string(nil), req.NotificationRunSummaryRecipients...)
	dest.PulseSummaryRecipients = append([]string(nil), req.NotificationPulseSummaryRecipients...)
	if len(req.NotificationBlockRecipients) > 0 {
		if dest.Gmail == nil {
			dest.Gmail = &services.GmailDest{}
		}
		dest.Gmail.BlockedRecipients = append(dest.Gmail.BlockedRecipients, req.NotificationBlockRecipients...)
	}
	// Sender selection rides the same path as the recipient denylist, but stays
	// a separate field: one says which account sends, the other says who must
	// not receive. Resolution and validation happen in the Gmail service.
	if connID := strings.TrimSpace(req.NotificationGmailConnectionID); connID != "" {
		if dest.Gmail == nil {
			dest.Gmail = &services.GmailDest{}
		}
		dest.Gmail.ConnectionIDs = []string{connID}
	}
	if dest.UserID == "" && dest.WorkspacePath == "" && dest.Slack == nil && dest.SlackWebhook == nil && dest.WhatsApp == nil && dest.Gmail == nil &&
		len(dest.ExcludeChannels) == 0 && len(dest.RunSummaryRecipients) == 0 && len(dest.PulseSummaryRecipients) == 0 &&
		len(dest.RunSummaryGmailConnectionIDs) == 0 && len(dest.PulseSummaryGmailConnectionIDs) == 0 &&
		len(dest.RunSummaryWebhooks) == 0 && len(dest.PulseSummaryWebhooks) == 0 {
		return nil
	}
	return dest
}

// resolveNotificationSecretForRequest resolves a configured webhook into an
// internal delivery-only field and removes the same name from DecryptedSecrets.
// This is the boundary that prevents notification credentials from becoming
// agent-visible SECRET_* environment variables.
func (api *StreamingAPI) resolveNotificationSecretForRequest(ctx context.Context, userID, workflowPath string, req *QueryRequest) {
	if api == nil || req == nil {
		return
	}
	// Every configured webhook is a delivery credential, including the
	// per-summary channel webhooks. They must ALL be stripped from agent-visible
	// injection, not just the default one — otherwise adding a second channel
	// would quietly turn its URL into a SECRET_* variable the agent can read.
	defaultSecretName := strings.TrimSpace(req.NotificationSlackWebhookSecretName)
	secretNames := uniqueNonEmpty(append(
		append([]string{defaultSecretName}, req.NotificationRunSummarySlackWebhookSecretNames...),
		req.NotificationPulseSummarySlackWebhookSecretNames...,
	))
	if len(secretNames) == 0 {
		req.notificationSlackWebhookURL = ""
		req.notificationSlackWebhookURLs = nil
		return
	}

	wanted := make(map[string]bool, len(secretNames))
	for _, name := range secretNames {
		wanted[name] = true
	}
	resolved := make(map[string]string, len(secretNames))

	filtered := req.DecryptedSecrets[:0]
	for _, secret := range req.DecryptedSecrets {
		name := strings.TrimSpace(secret.Name)
		if wanted[name] {
			if resolved[name] == "" {
				resolved[name] = secret.Value
			}
			continue
		}
		filtered = append(filtered, secret)
	}
	req.DecryptedSecrets = filtered

	// nil means "inject every global secret", so convert it to an explicit
	// allow-list before removing the notification credentials. Otherwise a
	// GLOBAL_SECRET_<NAME> webhook would still leak through mergeGlobalSecrets.
	if req.SelectedGlobalSecrets == nil {
		allowed := make([]string, 0, len(getGlobalSecrets()))
		for _, secret := range getGlobalSecrets() {
			if !wanted[strings.TrimSpace(secret.Name)] {
				allowed = append(allowed, secret.Name)
			}
		}
		req.SelectedGlobalSecrets = &allowed
	} else {
		filteredGlobals := *req.SelectedGlobalSecrets
		for _, name := range secretNames {
			filteredGlobals = removeString(filteredGlobals, name)
		}
		req.SelectedGlobalSecrets = &filteredGlobals
	}

	for _, name := range secretNames {
		if strings.TrimSpace(resolved[name]) == "" {
			if value, ok := api.resolveBackendNotificationSecret(ctx, userID, workflowPath, name); ok {
				resolved[name] = value
			}
		}
	}
	req.notificationSlackWebhookURLs = resolved
	req.notificationSlackWebhookURL = resolved[defaultSecretName]
}

// uniqueNonEmpty trims, drops blanks, and de-duplicates while preserving order.
func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// resolveBackendNotificationSecret deliberately ignores agent secret-selection
// lists. Notification configuration is its own backend-only capability.
func (api *StreamingAPI) resolveBackendNotificationSecret(ctx context.Context, userID, workflowPath, secretName string) (string, bool) {
	secretName = strings.TrimSpace(secretName)
	if api == nil || secretName == "" {
		return "", false
	}
	for _, secret := range api.loadSelectedSecrets(ctx, userID, workflowPath, []string{secretName}) {
		if secret.Name == secretName && strings.TrimSpace(secret.Value) != "" {
			return secret.Value, true
		}
	}
	for _, secret := range getGlobalSecrets() {
		if secret.Name == secretName && strings.TrimSpace(secret.Value) != "" {
			return secret.Value, true
		}
	}
	return "", false
}

const maxCDPPortsPerRun = 4

// getCdpPorts returns the ordered, unique CDP ports explicitly authorized for
// this run. Multiple ports are reserved for separate Chrome profiles/login
// identities inside one workflow; ordinary concurrent workflows share one.
func getCdpPorts(req QueryRequest) []int {
	if !browser.CDPEnabled() {
		return nil
	}
	mode := strings.ToLower(strings.TrimSpace(req.BrowserMode))
	if mode == "none" || mode == "headless" {
		return nil
	}
	candidates := make([]int, 0, 1+len(req.CdpPorts))
	if req.CdpPort != nil {
		candidates = append(candidates, *req.CdpPort)
	}
	candidates = append(candidates, req.CdpPorts...)
	seen := make(map[int]bool, len(candidates))
	ports := make([]int, 0, len(candidates))
	for _, port := range candidates {
		if port < 1 || port > 65535 || seen[port] {
			continue
		}
		seen[port] = true
		ports = append(ports, port)
		if len(ports) == maxCDPPortsPerRun {
			break
		}
	}
	if len(ports) == 0 && strings.EqualFold(strings.TrimSpace(req.BrowserMode), "cdp") {
		return []int{defaultCDPPort}
	}
	return ports
}

func validateRequestedCDPPorts(req QueryRequest) error {
	if !browser.CDPEnabled() {
		if strings.EqualFold(strings.TrimSpace(req.BrowserMode), "cdp") || req.CdpPort != nil || len(req.CdpPorts) > 0 {
			return fmt.Errorf("CDP is disabled for this server deployment; use browser_mode auto, headless, or none without CDP ports")
		}
		return nil
	}
	candidates := append([]int{}, req.CdpPorts...)
	if req.CdpPort != nil {
		candidates = append([]int{*req.CdpPort}, candidates...)
	}
	seen := make(map[int]bool, len(candidates))
	for _, port := range candidates {
		if port < 1 || port > 65535 {
			return fmt.Errorf("CDP port %d must be between 1 and 65535", port)
		}
		seen[port] = true
	}
	if len(seen) > maxCDPPortsPerRun {
		return fmt.Errorf("a run may authorize at most %d CDP ports", maxCDPPortsPerRun)
	}
	return nil
}

// getCdpPort returns the primary CDP port from a QueryRequest, or 0 if unset.
func getCdpPort(req QueryRequest) int {
	if ports := getCdpPorts(req); len(ports) > 0 {
		return ports[0]
	}
	return 0
}

// getBrowserMode resolves effective browser mode with backward-compatible fallback.
func getBrowserMode(req QueryRequest) string {
	mode := strings.ToLower(strings.TrimSpace(req.BrowserMode))
	switch mode {
	case "none", "auto", "headless", "cdp":
		return mode
	}

	enableBrowser := req.EnableBrowserAccess != nil && *req.EnableBrowserAccess
	if enableBrowser {
		if getCdpPort(req) > 0 {
			return "cdp"
		}
		return "headless"
	}
	return "none"
}

// buildChatBrowserConfig resolves the browser configuration from a QueryRequest
// into the standardized BrowserConfig used by BuildBrowserInstructions.
func buildChatBrowserConfig(req QueryRequest) browserinstructions.BrowserConfig {
	mode := getBrowserMode(req)
	if mode == "auto" && !browser.CDPEnabled() {
		// CDP can never resolve on this deployment (getCdpPorts already
		// returns none, and validateRequestedCDPPorts already rejects an
		// explicit cdp request) -- so "auto" mode's whole point, deciding
		// between CDP and headless, has only one possible answer here.
		// Skipping straight to headless drops the "call agent_browser status
		// first, follow effective_mode" preamble and the phantom port-9222
		// candidate that GetAutoBrowserInstructions/BuildBrowserRuntimeInstructions
		// would otherwise always add, on every single turn, for a check that
		// can never succeed.
		mode = "headless"
	}
	ports := getCdpPorts(req)
	if mode == "auto" {
		ports = configuredCDPPortsForMode(mode, req.CdpPort, req.CdpPorts)
	}
	primary := 0
	if len(ports) > 0 {
		primary = ports[0]
	}
	cfg := browserinstructions.BrowserConfig{
		CdpPort:  primary,
		CdpPorts: ports,
		Mode:     mode,
	}
	hasBrowserAccess := req.EnableBrowserAccess != nil && *req.EnableBrowserAccess
	if hasBrowserAccess || mode == "auto" || mode == "headless" || mode == "cdp" {
		cfg.HasAgentBrowser = true
	}
	return cfg
}

func cdpPromptEndpoints(ports []int, primary int) (string, string) {
	ordered := append([]int{}, ports...)
	if primary > 0 {
		ordered = append([]int{primary}, ordered...)
	}
	endpoints := browser.ConfiguredCDPEndpoints(ordered)
	if len(endpoints) == 0 {
		endpoints = []string{browser.ConfiguredCDPEndpoint(defaultCDPPort)}
	}
	guidance := "This run authorizes CDP endpoint `" + endpoints[0] + "`."
	if len(endpoints) > 1 {
		guidance = "This run explicitly authorizes independent Chrome profiles at `" + strings.Join(endpoints, "`, `") + "`. Choose the endpoint matching the intended login/account on every call; keep separate labeled tabs per profile. Multiple ports are for specialized multi-login testing, not ordinary workflow concurrency."
	}
	return endpoints[0], guidance
}

func applyMultiAgentCapabilitiesToRequest(req *QueryRequest, caps WorkflowCapabilities) {
	if req == nil {
		return
	}

	req.EnabledServers = append([]string(nil), caps.SelectedServers...)
	req.Servers = nil
	req.SelectedTools = append([]string(nil), caps.SelectedTools...)
	req.SelectedSkills = append([]string(nil), caps.SelectedSkills...)
	req.UseCodeExecutionMode = caps.UseCodeExecutionMode
	req.BrowserMode = strings.ToLower(strings.TrimSpace(caps.BrowserMode))
	req.CdpPorts = append([]int(nil), caps.CDPPorts...)
	if caps.Notifications != nil {
		req.NotificationSlackWebhookSecretName = strings.TrimSpace(caps.Notifications.SlackWebhookSecretName)
		req.NotificationRunSummaryInstructions = caps.Notifications.EffectiveRunSummaryInstructions()
		req.NotificationPulseSummaryInstructions = caps.Notifications.EffectivePulseSummaryInstructions()
		req.NotificationRunSummaryChannels = append([]string(nil), caps.Notifications.RunSummaryChannels...)
		req.NotificationPulseSummaryChannels = append([]string(nil), caps.Notifications.PulseSummaryChannels...)
		req.NotificationExcludeChannels = append([]string(nil), caps.Notifications.ExcludeChannels...)
		req.NotificationBlockRecipients = append([]string(nil), caps.Notifications.BlockRecipients...)
		req.NotificationGmailConnectionID = caps.Notifications.GmailConnectionID
		req.NotificationRunSummaryGmailConnectionIDs = append([]string(nil), caps.Notifications.RunSummaryGmailConnectionIDs...)
		req.NotificationPulseSummaryGmailConnectionIDs = append([]string(nil), caps.Notifications.PulseSummaryGmailConnectionIDs...)
		req.NotificationRunSummaryRecipients = append([]string(nil), caps.Notifications.RunSummaryRecipients...)
		req.NotificationPulseSummaryRecipients = append([]string(nil), caps.Notifications.PulseSummaryRecipients...)
		req.NotificationRunSummarySlackWebhookSecretNames = append([]string(nil), caps.Notifications.RunSummarySlackWebhookSecretNames...)
		req.NotificationPulseSummarySlackWebhookSecretNames = append([]string(nil), caps.Notifications.PulseSummarySlackWebhookSecretNames...)
	}
	if req.BrowserMode == "" {
		req.BrowserMode = "none"
	}

	enableBrowser := req.BrowserMode == "auto" || req.BrowserMode == "headless" || req.BrowserMode == "cdp"
	req.EnableBrowserAccess = &enableBrowser

	if caps.SelectedGlobalSecretNames != nil {
		copied := append([]string(nil), (*caps.SelectedGlobalSecretNames)...)
		req.SelectedGlobalSecrets = &copied
	}
	if cfg := queryLLMConfigFromPreset(lockedPresetLLMConfig(caps.LLMConfig)); cfg != nil {
		req.LLMConfig = cfg
	}
}

func (api *StreamingAPI) applySavedMultiAgentChatConfig(ctx context.Context, req *QueryRequest, userID string) {
	if req == nil || !isToolBackedChatMode(req.AgentMode) || strings.EqualFold(strings.TrimSpace(req.TriggeredBy), "cron") {
		return
	}
	if userID == "" {
		userID = "default"
	}
	cfg, found, err := ReadMultiAgentChatConfig(ctx, userID)
	if err != nil {
		log.Printf("[MULTIAGENT_CONFIG] Failed to load saved chat capabilities for user %s: %v", userID, err)
		return
	}
	if !found || cfg == nil {
		return
	}

	applyMultiAgentCapabilitiesToRequest(req, cfg.Capabilities)
	if len(cfg.Capabilities.SelectedSecrets) > 0 {
		req.DecryptedSecrets = api.loadSelectedSecrets(ctx, userID, "", cfg.Capabilities.SelectedSecrets)
	}
	log.Printf("[MULTIAGENT_CONFIG] Applied saved chat capabilities for user %s: servers=%d tools=%d skills=%d secrets=%d browser_mode=%q code_execution=%v llm=%t",
		userID,
		len(req.EnabledServers),
		len(req.SelectedTools),
		len(req.SelectedSkills),
		len(req.DecryptedSecrets),
		req.BrowserMode,
		req.UseCodeExecutionMode,
		req.LLMConfig != nil,
	)
}

func queryLLMConfigFromPreset(preset *workflowtypes.PresetLLMConfig) *orchestrator.LLMConfig {
	if preset == nil {
		return nil
	}
	primary := presetPrimaryLLMForChat(preset)
	if primary == nil || strings.TrimSpace(primary.Provider) == "" || strings.TrimSpace(primary.ModelID) == "" {
		return nil
	}
	cfg := &orchestrator.LLMConfig{
		Primary: orchestrator.LLMModel{
			Provider:     primary.Provider,
			ModelID:      primary.ModelID,
			Options:      primary.Options,
			ConnectionID: primary.ConnectionID,
		},
	}

	return cfg
}

func presetPrimaryLLMForChat(preset *workflowtypes.PresetLLMConfig) *workflowtypes.AgentLLMConfig {
	if preset == nil {
		return nil
	}
	for _, candidate := range []*workflowtypes.AgentLLMConfig{
		preset.BuilderLLM,
	} {
		if candidate != nil && strings.TrimSpace(candidate.Provider) != "" && strings.TrimSpace(candidate.ModelID) != "" {
			return candidate
		}
	}
	if preset.TieredConfig != nil {
		for _, candidate := range []*workflowtypes.AgentLLMConfig{
			preset.TieredConfig.Tier1,
			preset.TieredConfig.Tier2,
			preset.TieredConfig.Tier3,
		} {
			if candidate != nil && strings.TrimSpace(candidate.Provider) != "" && strings.TrimSpace(candidate.ModelID) != "" {
				return candidate
			}
		}
	}
	if builder, _, ok := workflowtypes.ResolveProviderProfileConfig(preset); ok {
		return builder
	}
	return nil
}

// QueryResponse represents an agent query response
type QueryResponse struct {
	QueryID   string `json:"query_id"`
	SessionID string `json:"session_id"` // The actual session ID used for conversation history
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
	// DeliveryStatus/Provider are populated only when handleQuery short-circuits a
	// message into retained coding-agent CLI delivery (Status ==
	// "live_input_delivered"). They mirror the live-input endpoint's response so the
	// chat UI can render the same "sent to CLI" feedback without a second endpoint.
	DeliveryStatus string `json:"delivery_status,omitempty"`
	Provider       string `json:"provider,omitempty"`
	// DeliveryTransport and DeliverySource make retained delivery auditable.
	// Transport is the transport that actually accepted the message, not the
	// provider's default. Source distinguishes the durable mcpagent Session path
	// from the temporary cold-restart compatibility path.
	DeliveryTransport string `json:"delivery_transport,omitempty"`
	DeliverySource    string `json:"delivery_source,omitempty"`
	// MessageID identifies the recorded user_message row so the chat UI
	// can upgrade its optimistic bubble (and later its delivery tick)
	// by backend identity instead of content matching.
	MessageID string `json:"message_id,omitempty"`
	// QueuePosition is populated when the durable conversation dispatcher
	// accepts this request behind a turn that already owns the session lane.
	QueuePosition int `json:"queue_position,omitempty"`
	// Delivery timing is populated for coding-agent messages that reached a CLI.
	// The browser uses its own monotonic clock for click-to-ack; these server
	// values isolate request routing from the provider/tmux handoff.
	SubmissionID     string  `json:"submission_id,omitempty"`
	ServerReceivedAt string  `json:"server_received_at,omitempty"`
	CLIAcceptedAt    string  `json:"cli_accepted_at,omitempty"`
	ServerToCLIMs    float64 `json:"server_to_cli_ms,omitempty"`
}

// queryStatusLiveInputDelivered is the QueryResponse.Status returned when a
// /api/query message was delivered to a retained coding-agent CLI instead of
// starting a new streaming turn. This is the single backend source of truth for
// deciding whether ordinary chat starts, queues, or steers a turn. The separate
// /live-input route remains only as a compatibility shim for older clients.
const queryStatusLiveInputDelivered = "live_input_delivered"

const (
	queryDeliverySourceMCPAgentSession       = "mcpagent_session"
	queryDeliverySourceRetainedCompatibility = "retained_terminal_compatibility"
	queryDeliverySourceRunningAgent          = "running_agent"
)

const (
	llmConfigSourceAgentProfile = "agent_profile"
)

func requestLLMConfigOverridesManifest(req QueryRequest) bool {
	if req.LLMConfig == nil {
		return false
	}
	switch strings.TrimSpace(req.LLMConfigSource) {
	case llmConfigSourceAgentProfile:
		return true
	default:
		return false
	}
}

func shouldSerializeInteractiveQueryInput(req QueryRequest) bool {
	mode := normalizeAgentMode(req.AgentMode)
	return mode == "workflow_phase" || isToolBackedChatMode(mode)
}

// Only a person's fresh chat submission may bypass the next-turn queue to
// steer a retained CLI. A claimed queue item must execute as its own turn, and
// bot/scheduled/Pulse/token/synthetic producers retain their turn boundaries.
func shouldTryRetainedDeliveryBeforeQueue(ctx context.Context, req QueryRequest, sessionID string) bool {
	trigger := strings.ToLower(strings.TrimSpace(req.TriggeredBy))
	claims := GetUserFromContext(ctx)
	if conversationTurnQueueExecution(ctx) || req.DisableLiveInputDelivery ||
		req.IsAutoNotification || req.PulseLifecycleTurn ||
		(claims != nil && claims.AccessToken != nil) {
		return false
	}
	// A person's follow-up in a bot conversation (Slack DM or thread,
	// WhatsApp) steers the running CLI, like a message in the web chat
	// (user decision 2026-09-26: always steer with tmux). Workflow bot turns
	// carry the schedule builder's "cron" trigger, so the platform, not the
	// trigger, marks them. A Slack workflow trigger run is a direct webhook
	// execution with its own session and keeps its turn boundary.
	if strings.TrimSpace(req.BotPlatform) != "" && ctx.Value(directWebhookExecutionKey{}) == nil {
		return true
	}
	return !isScheduledSessionIdentity(sessionID, req.TriggeredBy) &&
		!strings.HasPrefix(trigger, "bot:") &&
		(trigger == "" || trigger == "manual" || trigger == "interactive" || trigger == "workflow_builder")
}

func (api *StreamingAPI) queueOccupiedConversationTurn(w http.ResponseWriter, r *http.Request, userID, sessionID string, req QueryRequest) bool {
	if !shouldUseDurableConversationTurnQueue(req) || conversationTurnQueueExecution(r.Context()) ||
		!(api.conversationTurnOccupied(sessionID) || api.externalBuilderOwnsSession(sessionID)) {
		return false
	}
	return api.queueConversationTurnResponse(w, r, userID, sessionID, req)
}

func (api *StreamingAPI) queueConversationTurnResponse(w http.ResponseWriter, r *http.Request, userID, sessionID string, req QueryRequest) bool {
	turn, position, err := api.enqueueConversationTurn(r.Context(), userID, sessionID, req)
	if err != nil {
		http.Error(w, "Cannot durably queue conversation turn: "+err.Error(), http.StatusServiceUnavailable)
		return true
	}
	api.recordQueuedConversationUserMessage(sessionID, turn)
	api.kickConversationTurnQueue(sessionID)
	_ = json.NewEncoder(w).Encode(QueryResponse{
		QueryID: turn.ID, SessionID: sessionID, Status: "accepted",
		Message:        "Queued behind the active conversation turn",
		DeliveryStatus: "queued_for_turn", MessageID: turn.ID, QueuePosition: position,
	})
	return true
}

type sessionInputLane struct {
	sem  chan struct{} // capacity 1: a waiter holds the token while its turn owns the lane
	refs int
}

// sessionTurnInProgress reports whether a turn currently holds — or is already
// queued for — this session's input lane.
//
// The lane is not a signal ABOUT occupancy; it IS occupancy. A turn occupies a
// session exactly when it holds this lane token, because the token is what serializes
// access to the one tmux pane, the conversation history, and runningAgents.
//
// PLAT-113: callers needing "is a turn running" previously read sessionBusy,
// which is a user-facing display flag deliberately NOT set for workflow turns
// (see the !isWorkflowPhase guard where it is assigned). Auto-notifications
// therefore skipped their queue during a workflow run and blocked on this lane
// instead, piling 25 never-started synthetic turns behind one 5-hour turn until
// the idle-wait watchdog killed a healthy run. Read the authority, not the
// display flag.
func (api *StreamingAPI) sessionTurnInProgress(sessionID string) bool {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	api.sessionInputLanesMu.Lock()
	defer api.sessionInputLanesMu.Unlock()
	lane := api.sessionInputLanes[sessionID]
	return lane != nil && lane.refs > 0
}

func (api *StreamingAPI) lockSessionInputLane(sessionID string) func() {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return func() {}
	}
	lane := api.sessionInputLaneFor(sessionID)
	lane.sem <- struct{}{}
	return api.releaseSessionInputLaneFunc(sessionID, lane)
}

// sessionInputLaneInteractiveWait bounds how long an interactive send waits
// for a running turn to release the session lane before the backend answers
// 409 turn_running (nothing was sent; the frontend retries). Synthetic and
// scheduled turns keep the unbounded wait: they are served by internal
// queue/retry machinery, not by an impatient user-facing POST.
const sessionInputLaneInteractiveWait = 10 * time.Second

// tryLockSessionInputLane acquires the session input lane, waiting at most
// timeout. It reports false (holding nothing) when the lane stays busy; the
// caller must answer without starting a turn.
func (api *StreamingAPI) tryLockSessionInputLane(sessionID string, timeout time.Duration) (func(), bool) {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return func() {}, true
	}
	lane := api.sessionInputLaneFor(sessionID)
	if timeout <= 0 {
		select {
		case lane.sem <- struct{}{}:
			return api.releaseSessionInputLaneFunc(sessionID, lane), true
		default:
			api.dropSessionInputLaneRef(sessionID, lane)
			return nil, false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case lane.sem <- struct{}{}:
		return api.releaseSessionInputLaneFunc(sessionID, lane), true
	case <-timer.C:
		api.dropSessionInputLaneRef(sessionID, lane)
		return nil, false
	}
}

func (api *StreamingAPI) sessionInputLaneFor(sessionID string) *sessionInputLane {
	api.sessionInputLanesMu.Lock()
	defer api.sessionInputLanesMu.Unlock()
	if api.sessionInputLanes == nil {
		api.sessionInputLanes = make(map[string]*sessionInputLane)
	}
	lane := api.sessionInputLanes[sessionID]
	if lane == nil {
		lane = &sessionInputLane{sem: make(chan struct{}, 1)}
		api.sessionInputLanes[sessionID] = lane
	}
	lane.refs++
	return lane
}

func (api *StreamingAPI) releaseSessionInputLaneFunc(sessionID string, lane *sessionInputLane) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			<-lane.sem
			api.dropSessionInputLaneRef(sessionID, lane)
			// The persisted queue is the authority for what runs next. Kicking
			// after release closes the accept-vs-completion race.
			api.kickConversationTurnQueue(sessionID)
		})
	}
}

func (api *StreamingAPI) dropSessionInputLaneRef(sessionID string, lane *sessionInputLane) {
	api.sessionInputLanesMu.Lock()
	defer api.sessionInputLanesMu.Unlock()
	lane.refs--
	if lane.refs == 0 && api.sessionInputLanes[sessionID] == lane {
		delete(api.sessionInputLanes, sessionID)
	}
}

// LLMGuidanceRequest represents a request to set LLM guidance for a session
type LLMGuidanceRequest struct {
	SessionID string `json:"session_id"`
	Guidance  string `json:"guidance"`
}

// LLMGuidanceResponse represents the response for LLM guidance operations
type LLMGuidanceResponse struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
	Guidance  string `json:"guidance,omitempty"`
}

// LiveInputRequest represents a user message delivered to a coding agent. For
// CLI transports this can target the retained tmux-backed agent; otherwise the
// backend may start the next turn.
type LiveInputRequest struct {
	Message      string `json:"message"`
	SubmissionID string `json:"submission_id,omitempty"`
}

type LiveInputResponse struct {
	Success        bool   `json:"success"`
	Message        string `json:"message,omitempty"`
	DeliveryStatus string `json:"delivery_status,omitempty"`
	Provider       string `json:"provider,omitempty"`
	MessageID      string `json:"message_id,omitempty"`
	QueryID        string `json:"query_id,omitempty"`
	QueuePosition  int    `json:"queue_position,omitempty"`
}

// ControlKeyRequest carries a tmux control key (e.g. "Escape") to inject into
// a running coding-agent session.
type ControlKeyRequest struct {
	Key string `json:"key"`
}

// ControlKeyResponse mirrors the live-input response shape for ergonomic frontend
// consumption — same delivery/provider fields the live-input UX already
// renders.
type ControlKeyResponse struct {
	Success  bool   `json:"success"`
	Message  string `json:"message,omitempty"`
	Provider string `json:"provider,omitempty"`
	Key      string `json:"key,omitempty"`
}

const (
	liveCodingAgentInputTimeout    = 15 * time.Second
	liveCodingAgentDeliveryTimeout = 6 * time.Minute // adapter durable-ack budgets can reach 300s after a pane miss
)

// HumanFeedbackRequest represents a request to submit human feedback
type HumanFeedbackRequest struct {
	UniqueID string `json:"unique_id"`
	Response string `json:"response"`
}

// HumanFeedbackResponse represents the response for human feedback operations
type HumanFeedbackResponse struct {
	UniqueID string `json:"unique_id"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
}

// --- TOOL MANAGEMENT API ---

func init() {
	// Add server command flags
	ServerCmd.Flags().IntP("port", "p", 8000, "Server port")
	ServerCmd.Flags().StringP("host", "H", "127.0.0.1", "Server host")
	ServerCmd.Flags().StringSlice("cors-origins", []string{"loopback"}, "CORS allowed origins; use loopback for localhost/127.0.0.1/::1")
	ServerCmd.Flags().String("provider", "bedrock", "LLM provider (bedrock, openai, anthropic)")
	ServerCmd.Flags().String("model", "", "Model ID (uses provider default if empty)")
	ServerCmd.Flags().Float64("temperature", 0.0, "Temperature for LLM")
	ServerCmd.Flags().Int("max-turns", 500, "Maximum conversation turns")
	ServerCmd.Flags().String("mcp-config", "configs/mcp_servers_clean.json", "MCP servers configuration path")

	// Bind flags to viper
	viper.BindPFlags(ServerCmd.Flags())

	ServerCmd.AddCommand(rotateProviderKeysCmd)
	ServerCmd.AddCommand(migrateSparkQuillCmd)
	ServerCmd.AddCommand(migrateProductSecretsCmd)
	ServerCmd.AddCommand(migrateDurableChatsCmd)
	ServerCmd.AddCommand(dedupeChatHistoryCmd)
}

func runServer(cmd *cobra.Command, args []string) {
	// Standard-library logs are emitted from many downstream packages that do
	// not accept a logger. Enforce stable username/workflow keys at the process
	// boundary and enrich session-tagged lines from the request registry.
	installDefaultServerLogContextWriter()
	installProviderAccountConfigHook()

	// Load configuration
	config := ServerConfig{
		Port:          viper.GetInt("port"),
		Host:          viper.GetString("host"),
		CORSOrigins:   viper.GetStringSlice("cors-origins"),
		Provider:      viper.GetString("provider"),
		ModelID:       viper.GetString("model"),
		Temperature:   viper.GetFloat64("temperature"),
		MaxTurns:      viper.GetInt("max-turns"),
		MCPConfigPath: viper.GetString("mcp-config"),
	}

	log.Printf("[SERVER DEBUG] Using MCP config file: %s", config.MCPConfigPath)

	// Load .env file for environment variables (OPENAI_API_KEY, etc.)
	// Only load if not already loaded
	if os.Getenv("MCP_ENV_LOADED") == "" {
		if err := godotenv.Load(); err == nil {
			os.Setenv("MCP_ENV_LOADED", "1")
			fmt.Println("[ENV] Loaded .env file for LLM config")
		}
	}

	cliUpdateCtx, stopCLIUpdates := context.WithCancel(context.Background())
	defer stopCLIUpdates()
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("CLI_UPDATE_ENABLED")), "false") && !strings.EqualFold(strings.TrimSpace(os.Getenv("CLI_UPDATE_DRY_RUN")), "true") {
		root, updateErr := cliupdate.DefaultRoot()
		if updateErr == nil {
			var manager *cliupdate.Manager
			manager, updateErr = cliupdate.New(root, log.Printf)
			if updateErr == nil {
				updateErr = manager.Prepare()
			}
			if updateErr == nil {
				updateErr = manager.Activate()
			}
			if updateErr == nil {
				log.Printf("[CLI UPDATE] daily checks enabled; state: %s", filepath.Join(root, "state.json"))
				go manager.Run(cliUpdateCtx)
			}
		}
		if updateErr != nil {
			log.Printf("[CLI UPDATE] unavailable: %v", updateErr)
		}
	}

	// Startup recovery is ownership-aware: only tagged coding-agent tmux
	// sessions whose backend PID is dead are removed. Untagged legacy sessions
	// and sessions owned by another live backend/test are preserved.
	if os.Getenv("MCP_DISABLE_TMUX_STARTUP_SWEEP") == "" {
		sweepCtx, cancelSweep := context.WithTimeout(context.Background(), 10*time.Second)
		if n := sweepOrphanedOwnedTmuxSessions(sweepCtx); n > 0 {
			fmt.Printf("🧹 Swept %d orphaned coding-agent tmux session(s) from a previous run\n", n)
			log.Printf("[STARTUP] swept %d orphaned coding-agent tmux sessions", n)
		}
		// Bridge token files of backends that exited (their tokens no longer
		// verify); a live backend's are kept (PLAT-362 D2).
		if n := cursorcli.SweepStaleBridgeTokenFiles(); n > 0 {
			log.Printf("[STARTUP] swept bridge token files of %d exited backend(s)", n)
		}
		cancelSweep()
	}

	// Upgrade every currently available workflow database before schedules and
	// interactive Pulse sessions can write. This is intentionally repeated at
	// startup: the version receipt makes it a no-op after the first successful
	// deployment, while the lazy open guard covers laptops that were offline.
	pulseMigrationCtx, cancelPulseMigration := context.WithTimeout(context.Background(), 10*time.Minute)
	pulseMigrationReport, pulseMigrationErr := pulsestore.MigrateWorkspaceDatabases(pulseMigrationCtx, fsutil.WorkspaceDocsRoot())
	cancelPulseMigration()
	if pulseMigrationErr != nil {
		log.Printf("[PULSE MIGRATION] deployment scan failed: %v", pulseMigrationErr)
	} else {
		log.Printf("[PULSE MIGRATION] scanned=%d migrated=%d current=%d skipped=%d failures=%d",
			pulseMigrationReport.Scanned, pulseMigrationReport.Migrated, pulseMigrationReport.Current,
			pulseMigrationReport.Skipped, len(pulseMigrationReport.Failures))
		for _, failure := range pulseMigrationReport.Failures {
			log.Printf("[PULSE MIGRATION] blocked database=%s error=%s", failure.Path, failure.Error)
		}
	}

	// Show execution agent LLM config at startup
	agentProvider := os.Getenv("AGENT_PROVIDER")
	if agentProvider == "" {
		agentProvider = "bedrock" // fallback default
	}
	agentModel := os.Getenv("AGENT_MODEL")
	if agentModel == "" {
		agentModel = os.Getenv("BEDROCK_PRIMARY_MODEL") // Use .env configuration
	}
	fmt.Printf("\U0001F916 Agent:   %s | Model: %s\n", agentProvider, agentModel)

	// Apply environment overrides to config (ensure Terraform env vars take precedence)
	if val := os.Getenv("AGENT_PROVIDER"); val != "" {
		config.Provider = val
	}
	// Also apply model override if set (and not just falling back to defaults)
	if agentModel != "" && (os.Getenv("AGENT_MODEL") != "" || os.Getenv("BEDROCK_PRIMARY_MODEL") != "") {
		config.ModelID = agentModel
	}
	// Validate provider
	llmProvider, err := llm.ValidateProvider(config.Provider)
	if err != nil {
		log.Fatalf("Invalid provider: %v", err)
	}

	// Set default model if not specified
	if config.ModelID == "" {
		config.ModelID = llm.GetDefaultModel(llmProvider)
	}

	if err := ValidateConfiguredAuthSecret(); err != nil {
		log.Fatalf("[AUTH] FATAL: %v. Generate a random secret and add it to your deployment configuration.", err)
	}
	// Import AUTH_USERS into config/users.json and apply ADMIN_USERS once the
	// workspace API (which stores that file) answers. Retried briefly because
	// the workspace server usually starts in parallel with this one.
	go func() {
		for attempt := 0; attempt < 60; attempt++ {
			if _, _, err := readFileFromWorkspace(context.Background(), userDirectoryFilePath()); err == nil {
				bootstrapUserDirectory(context.Background())
				return
			}
			time.Sleep(2 * time.Second)
		}
		log.Printf("[USERS] bootstrap skipped: workspace API not reachable")
	}()

	// Clean up stale agent-browser runtime state (dead PID files, sockets)
	// to prevent "CDP response channel closed" errors on first browser use.
	browser.CleanupStaleRuntimeState()

	// Start background reaper: kills browser sessions idle for >15 min so
	// Chrome/daemon processes don't accumulate and exhaust memory.
	browser.StartIdleReaper(15 * time.Minute)

	fmt.Printf("🚀 Starting Streaming API Server\n")
	fmt.Printf("📡 Host: %s:%d\n", config.Host, config.Port)
	fmt.Printf("🤖 Primary Provider: %s | Model: %s\n", config.Provider, config.ModelID)
	// Show tracing configuration
	tracingProvider := os.Getenv("TRACING_PROVIDER")
	if tracingProvider == "" {
		tracingProvider = "noop"
	}
	fmt.Printf("📊 Tracing: %s\n", tracingProvider)

	fmt.Printf("🌐 CORS Origins: %v\n", config.CORSOrigins)
	fmt.Printf("🔒 LLM Config Locked: %v (Env: %s)\n", isGlobalLLMConfigLocked(), os.Getenv("LLM_CONFIG_LOCKED"))

	// Daily ping to keep a Supabase free-tier auth project from auto-pausing.
	// No-op unless AUTH_PROVIDERS includes supabase.
	StartSupabaseKeepalive(context.Background())
	fmt.Printf("📋 Supported Providers: %s\n", os.Getenv("SUPPORTED_LLM_PROVIDERS"))
	fmt.Printf("📁 Config: %s\n", config.MCPConfigPath)

	// Create streaming API server
	configPath := config.MCPConfigPath

	// Resolve a release-independent catalog/overlay pair when running as a service.
	if stateDir := os.Getenv("AGENTWORKS_MCP_STATE_DIR"); strings.TrimSpace(stateDir) != "" {
		runtimePath, err := prepareMCPRuntimeConfig(configPath, stateDir)
		if err != nil {
			log.Fatalf("Failed to prepare durable MCP config: %v", err)
		}
		configPath = runtimePath
	}

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Printf("⚠️  MCP config file not found at %s, initializing empty config...", configPath)

		// Ensure directory exists
		configDir := filepath.Dir(configPath)
		if err := os.MkdirAll(configDir, 0755); err != nil {
			log.Fatalf("Failed to create config directory: %v", err)
		}

		// Create empty config file
		emptyConfig := &mcpclient.MCPConfig{MCPServers: make(map[string]mcpclient.MCPServerConfig)}
		if err := mcpclient.SaveConfig(configPath, emptyConfig); err != nil {
			log.Fatalf("Failed to create empty MCP config: %v", err)
		}
		log.Printf("✅ Created empty MCP config at %s", configPath)
	}

	mcpConfig, err := mcpclient.LoadConfig(configPath, nil) // Logger not yet available, will be created later
	if err != nil {
		log.Fatalf("Failed to load MCP config: %v", err)
	}

	// Initialize polling system (activity callback will be set after api is created).
	// Keep the backend close to the frontend retention window. Large workflow runs can
	// emit bulky tool events; retaining 10k events per session makes the server process
	// balloon even after the UI trims them.
	maxSessionEvents := 1500
	if raw := strings.TrimSpace(os.Getenv("EVENT_STORE_MAX_EVENTS")); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 {
			maxSessionEvents = parsed
		} else {
			log.Printf("⚠️  Invalid EVENT_STORE_MAX_EVENTS=%q; using default %d", raw, maxSessionEvents)
		}
	}
	eventStore := events.NewEventStore(maxSessionEvents)
	carryOverLegacyStateRoot()
	eventStateRoot, err := workflowCLIStateRoot()
	if err != nil {
		log.Fatalf("Failed to resolve durable structured-event state: %v", err)
	}
	migrateDurableChatsAtStartup(eventStateRoot)
	dedupeChatHistoriesAtStartup(eventStateRoot)
	eventJournal, err := events.OpenSQLiteEventJournal(filepath.Join(eventStateRoot, "structured-chat-events-v2.sqlite"))
	if err != nil {
		log.Fatalf("Failed to initialize durable structured-event journal: %v", err)
	}
	eventStore.SetDurableJournal(eventJournal)
	terminalStore := terminals.NewStore()
	serverStartedAt := time.Now()
	if processStart, ok := processStartedAt(os.Getpid()); ok {
		serverStartedAt = processStart
	}
	terminalLeaseRegistry := terminalleases.NewRegistry(
		fmt.Sprintf("%d-%d", os.Getpid(), serverStartedAt.UnixNano()),
		os.Getpid(),
		serverStartedAt,
	)
	terminalPipeRecorder := newTerminalPipeRecorder()
	terminalEventObserver := func(sessionID string, event events.Event) {
		if !terminalStore.HandleEventWithChange(sessionID, event) {
			return
		}
		for _, snapshot := range terminalStore.ListRaw(sessionID) {
			if lease, acquired := terminalLeaseRegistry.Observe(snapshot, event.Timestamp); acquired {
				go markTerminalLeaseOwnership(lease)
			}
		}
		if terminalPipeRecorder != nil {
			terminalPipeRecorder.ObserveSnapshots(terminalStore.List(sessionID))
		}
	}
	eventStore.SetEventAddedCallback(terminalEventObserver)
	log.Printf("📡 EventStore retention: max %d events per session", maxSessionEvents)
	startMemoryLog(eventStore)

	// Initialize the operator-state store (bot connector configs + user
	// secrets) and the authoritative global cost event database.
	chatStore, err := chathistory.NewWorkspaceAPIStore(getWorkspaceAPIURL())
	if err != nil {
		log.Fatalf("Failed to initialize workspace API operator store: %v", err)
	}
	defer chatStore.Close()
	costDBPath := filepath.Join(fsutil.WorkspaceDocsRoot(), "_system", "costs.sqlite")
	costLedger, err := costledger.NewSQLiteLedger(costDBPath)
	if err != nil {
		log.Fatalf("Failed to initialize cost event database: %v", err)
	}
	defer costLedger.Close()
	costledger.SetDefaultLedger(costLedger)
	defer costledger.SetDefaultLedger(nil)
	legacyCostPath := filepath.Join(fsutil.WorkspaceDocsRoot(), "_system", "costs.jsonl")
	if report, migrateErr := costLedger.MigrateLegacyJSONL(legacyCostPath); migrateErr != nil {
		// SQLite is already initialized and remains authoritative. A damaged or
		// unreadable compatibility file must not make every future startup fail.
		log.Printf("[COST_LEDGER] Legacy migration skipped after error: %v", migrateErr)
	} else if report.Imported > 0 || report.Duplicates > 0 || report.Quarantined > 0 {
		log.Printf("[COST_LEDGER] Legacy migration imported=%d duplicates=%d quarantined=%d",
			report.Imported, report.Duplicates, report.Quarantined)
	}
	fmt.Printf("💾 Operator store: workspace API (%s)\n", getWorkspaceAPIURL())
	fmt.Printf("💵 Cost events: SQLite (%s)\n", costDBPath)

	notificationManager := services.GetNotificationManager()
	if notificationManager != nil {
		// The Org Dashboard is an always-on internal notification destination.
		// It persists classified workflow summaries independently of whether any
		// external channel is configured or successfully delivers.
		notificationManager.RegisterConnector(services.NewOrgDashboardConnector())
		notificationManager.SetFeedbackResponseFunc(
			func(uniqueID string, response string) error {
				store := virtualtools.GetHumanFeedbackStore()
				if store != nil {
					return store.SubmitResponse(uniqueID, response)
				}
				return nil
			},
		)
	}

	// Initialize Slack service. Notification delivery is registered through
	// BotConversationManager.RegisterConnector when Slack bot mode is enabled.
	configureSlackCredentialCodec()
	slackSvc, err := services.InitSlackService()
	if err != nil {
		log.Printf("⚠️  Failed to initialize Slack service: %v (Slack integration will be disabled)", err)
	} else {
		log.Printf("✅ Slack service initialized")
	}

	// Initialize Gmail service (single-user, backed by the `gws` CLI). Unlike
	// Slack/WhatsApp it is send-only, so it registers directly with the
	// NotificationManager rather than the BotConversationManager.
	gmailSvc, err := services.InitGmailService()
	if err != nil {
		log.Printf("⚠️  Failed to initialize Gmail service: %v (Gmail integration will be disabled)", err)
	} else if gmailSvc.IsEnabled() {
		log.Printf("✅ Gmail service initialized and enabled")
		if notificationManager != nil {
			notificationManager.RegisterConnector(gmailSvc)
		}
	} else {
		log.Printf("✅ Gmail service initialized (disabled — set config/gmail-config.json)")
	}

	cliSecurityRoot, err := clisecurity.DefaultRoot()
	if err != nil {
		log.Fatalf("Failed to resolve AgentWorks CLI security config directory: %v", err)
	}
	cliSecurityStore, err := clisecurity.NewStore(cliSecurityRoot)
	if err != nil {
		log.Fatalf("Failed to initialize AgentWorks CLI security store: %v", err)
	}
	// A broken AGENTWORKS_PROVIDER_POLICY, or a product default its
	// provider's policy does not admit, stops the server here instead of
	// failing runs later.
	if err := validateProviderAccountInstallation(); err != nil {
		log.Fatalf("Provider accounts: %v", err)
	}
	profileRegistry := agentprofiles.NewRegistry()
	// Platform-owned tools first: any product's manifest may bind them.
	if err := platformtools.RegisterAgentProfileTools(profileRegistry); err != nil {
		log.Fatalf("Failed to register platform agent profile tools: %v", err)
	}
	if productEnabled("video-studio") {
		if err := videoproduct.RegisterProductSkills(); err != nil {
			log.Fatalf("Failed to register Video Studio skills: %v", err)
		}
		if err := videoproduct.SyncVisibleSkillsForExistingProjects(os.Getenv("WORKSPACE_DOCS_PATH")); err != nil {
			log.Printf("Failed to sync visible Video Studio skills into existing projects: %v", err)
		}
		for _, profile := range videoproduct.BuiltinAgentProfiles() {
			profile.Product = "video-studio"
			if err := profileRegistry.RegisterProfile(profile); err != nil {
				log.Fatalf("Failed to register Video Studio agent profile: %v", err)
			}
		}
		if err := videoproduct.RegisterAgentProfileRuntime(profileRegistry, getWorkspaceAPIURL()); err != nil {
			log.Fatalf("Failed to register Video Studio agent profile runtime: %v", err)
		}
	}
	if productEnabled("sparkquill") {
		if err := sparkquillproduct.RegisterProductSkills(); err != nil {
			log.Fatalf("Failed to register SparkQuill skills: %v", err)
		}
		for _, profile := range sparkquillproduct.BuiltinAgentProfiles() {
			profile.Product = sparkquillproduct.ProductName
			if err := profileRegistry.RegisterProfile(profile); err != nil {
				log.Fatalf("Failed to register SparkQuill agent profile: %v", err)
			}
		}
		if err := sparkquillproduct.RegisterAgentProfileRuntime(profileRegistry, getWorkspaceAPIURL()); err != nil {
			log.Fatalf("Failed to register SparkQuill agent profile runtime: %v", err)
		}
		runSparkQuillLegacyMigrationIfNeeded()
	}
	if productEnabled("dominion") {
		if err := dominionproduct.RegisterProductSkills(); err != nil {
			log.Fatalf("Failed to register Dominion skills: %v", err)
		}
		for _, profile := range dominionproduct.BuiltinAgentProfiles() {
			profile.Product = "dominion"
			if err := profileRegistry.RegisterProfile(profile); err != nil {
				log.Fatalf("Failed to register Dominion agent profile: %v", err)
			}
		}
		if err := dominionproduct.RegisterAgentProfileRuntime(profileRegistry, getWorkspaceAPIURL()); err != nil {
			log.Fatalf("Failed to register Dominion agent profile runtime: %v", err)
		}
	}
	if productEnabled("agentworks") {
		// Scaffolding only: no bespoke tools yet, so no
		// RegisterAgentProfileRuntime call -- see product.yaml's own comment
		// for why this registration is inert until something explicitly
		// requests AgentProfileID="agentworks".
		if err := agentworksproduct.RegisterProductSkills(); err != nil {
			log.Fatalf("Failed to register AgentWorks skills: %v", err)
		}
		for _, profile := range agentworksproduct.BuiltinAgentProfiles() {
			profile.Product = "agentworks"
			if err := profileRegistry.RegisterProfile(profile); err != nil {
				log.Fatalf("Failed to register AgentWorks agent profile: %v", err)
			}
		}
	}
	if productEnabled("work") {
		if err := workproduct.RegisterProductSkills(); err != nil {
			log.Fatalf("Failed to register Work skills: %v", err)
		}
		for _, profile := range workproduct.BuiltinAgentProfiles() {
			profile.Product = "work"
			applyInstallationProductDefault(&profile)
			if err := profileRegistry.RegisterProfile(profile); err != nil {
				log.Fatalf("Failed to register Work agent profile: %v", err)
			}
		}
		if err := workproduct.RegisterAgentProfileRuntime(profileRegistry, getWorkspaceAPIURL()); err != nil {
			log.Fatalf("Failed to register Work agent profile runtime: %v", err)
		}
	}
	if productEnabled(codeproduct.ProfileID) {
		if err := codeproduct.RegisterProductSkills(); err != nil {
			log.Fatalf("Failed to register Code skills: %v", err)
		}
		for _, profile := range codeproduct.BuiltinAgentProfiles() {
			profile.Product = codeproduct.ProfileID
			applyInstallationProductDefault(&profile)
			if err := profileRegistry.RegisterProfile(profile); err != nil {
				log.Fatalf("Failed to register Code agent profile: %v", err)
			}
		}
		if err := codeproduct.RegisterAgentProfileRuntime(profileRegistry, getWorkspaceAPIURL()); err != nil {
			log.Fatalf("Failed to register Code agent profile runtime: %v", err)
		}
	}

	// Fail fast when product.yaml's run-mode external_tools disagree with the
	// implemented external catalog, instead of 500ing the first CLI/MCP call.
	if _, err := externalTools(); err != nil {
		log.Fatalf("Failed to build external CLI/MCP catalog: %v", err)
	}

	api := &StreamingAPI{
		config:                             config,
		cliSecurityStore:                   cliSecurityStore,
		agentProfiles:                      profileRegistry,
		agentCancelFuncs:                   make(map[string]context.CancelFunc),
		workflowOrchestratorContexts:       make(map[string]context.CancelFunc),
		activeWorkflowExecutions:           make(map[string]*ActiveWorkflowExecution),
		trackedWorkflowExecutions:          make(map[string]*TrackedWorkflowExecution),
		sessionQueryIDs:                    make(map[string][]string),
		workflowObjectives:                 make(map[string]string),
		conversationHistory:                make(map[string][]llmtypes.MessageContent),
		restoredConversationPersistTargets: make(map[string]restoredChatHistoryPersistTarget),
		chatStore:                          chatStore,
		costLedger:                         costLedger,
		inspectorStore:                     inspector.NewStore(),
		eventStore:                         eventStore,
		terminalStore:                      terminalStore,
		runtimeCoordinator:                 NewRuntimeCoordinator(),
		terminalLeaseRegistry:              terminalLeaseRegistry,
		terminalPipeRecorder:               terminalPipeRecorder,
		liveAttach:                         newLiveAttachManagerIfEnabled(),
		providerSetups:                     newProviderSetupManager(),
		provider:                           config.Provider,
		model:                              config.ModelID,
		mcpConfigPath:                      configPath,
		temperature:                        config.Temperature,
		workspaceRoot:                      "./Tasks",
		toolStatus:                         make(map[string]ToolStatus),
		enabledTools:                       make(map[string][]string),
		mcpConfig:                          mcpConfig,
		serverLogs:                         make(map[string][]ServerLogEntry),
		logger:                             createServerLogger(),
		// Initialize background discovery fields
		discoveryRunning:       false,
		lastDiscovery:          time.Time{},
		discoveryTicker:        nil,
		discoveryFailedServers: make(map[string]string),
		// Initialize active session tracking
		activeSessions: make(map[string]*ActiveSessionInfo),
		// Initialize orchestrator storage
		workflowOrchestrators: make(map[string]orchestrator.Orchestrator),
		// Initialize workflow step ID storage
		workflowStepIDs: make(map[string]string),
		// Initialize background agent infrastructure
		bgAgentRegistry:                     NewBackgroundAgentRegistry(),
		sessionBusy:                         make(map[string]bool),
		sessionBusySince:                    make(map[string]time.Time),
		retainedMainTurns:                   make(map[string]time.Time),
		retainedMainTurnExecutionIDs:        make(map[string]string),
		retainedMainTurnPendingExecutionIDs: make(map[string][]string),
		retainedMainTurnWatchCancels:        make(map[string]context.CancelFunc),
		nativeTranscriptSyncInFlight:        make(map[string]bool),
		pendingCompletions:                  make(map[string][]string),
		completionRetryScheduled:            make(map[string]bool),
		pendingStartNotifications:           make(map[string][]string),
		startNotificationRetryScheduled:     make(map[string]bool),
		lastQueryRequests:                   make(map[string]QueryRequest),
		sessionWorkspaceFolders:             make(map[string]string),
		sessionAgents:                       make(map[string]*agent.LLMAgentWrapper),
		runningAgents:                       make(map[string]*mcpagent.Agent),
		sessionInputLanes:                   make(map[string]*sessionInputLane),
		conversationTurnQueueOwners:         make(map[string]string),
		conversationTurnQueueDraining:       make(map[string]bool),
		conversationTurnQueueWaiters:        make(map[string]chan queuedConversationTurnResult),
		conversationTurnQueueCallbacks:      make(map[string]func(event *unifiedevents.AgentEvent)),
		completionLoopStarted:               make(map[string]bool),
		lastWorkshopModeBySession:           make(map[string]string),
		lastChatPolicyBySession:             make(map[string]string),
		lastChatPolicyRoleBySession:         make(map[string]string),
		lastAgentProfileKeyBySession:        make(map[string]string),
		stoppedSessions:                     make(map[string]bool),
		interruptedTurns:                    make(map[string]bool),
	}
	// Pulse Goal Work reaches other workflows and Crews through the external API.
	pulsePlatformAPI = api
	// Terminal Center's Formatted view and the runtime coordinator now consume
	// the same accepted structured events. The terminal observer updates the
	// durable pane snapshot first; retained-turn reconciliation then uses that
	// authoritative result to settle only the matching main-agent turn.
	eventStore.SetEventAddedCallback(func(sessionID string, event events.Event) {
		terminalEventObserver(sessionID, event)
		api.observeRetainedMainTurnEvent(sessionID, event)
	})

	// Some tool calls never produce a tool_call_end on the live stream, which
	// left the chat showing a finished command as unresolved. The provider's own
	// transcript has the result and the real runtime; this lets the event store
	// ask for them (PLAT-141).
	api.installToolResultRecovery()
	stopNativeTranscriptRecovery := api.resumePendingNativeTranscriptRecovery()
	defer stopNativeTranscriptRecovery()

	// BG-001: Wire the onDropped callback so a full notification channel re-queues
	// the completion instead of silently losing it permanently.
	api.bgAgentRegistry.onDropped = func(sessionID, agentID string) {
		api.queuePendingCompletion(sessionID, agentID)
		api.schedulePendingCompletionRetry(sessionID)
	}

	// Kill orphaned browser processes only for the normal singleton instance.
	// A named isolated instance must never issue workspace-wide --kill-all/pkill:
	// the workspace runs natively and those processes may belong to the user's
	// main AgentWorks instance.
	if browser.ShouldRunGlobalStartupCleanup() {
		go func() {
			workspaceAPIURL := os.Getenv("WORKSPACE_API_URL")
			if workspaceAPIURL == "" {
				workspaceAPIURL = "http://127.0.0.1:8081"
			}
			client := browser.NewClient(workspaceAPIURL)
			// Send a kill-all command via workspace-api to clean up any leftover browsers
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_, err := client.ExecuteCommand(ctx, []string{"--kill-all"}, &browser.ExecuteOptions{Timeout: 15 * time.Second})
			if err != nil {
				// --kill-all may not be supported; fall back to pkill
				log.Printf("[BROWSER_CLEANUP] --kill-all not supported, trying pkill fallback")
				killCmd := "pkill -9 -f 'agent-browser' 2>/dev/null; pkill -9 -f chromium 2>/dev/null; pkill -9 -f 'Google Chrome for Testing' 2>/dev/null; echo 'cleanup done'"
				reqBody := browser.ShellExecuteRequest{Command: killCmd, WorkingDirectory: ".", Timeout: 10}
				jsonBody, _ := json.Marshal(reqBody)
				req, _ := http.NewRequestWithContext(ctx, "POST", workspaceAPIURL+"/api/execute", bytes.NewBuffer(jsonBody))
				if req != nil {
					req.Header.Set("Content-Type", "application/json")
					// /api/execute is token-protected. This fallback builds its request
					// by hand instead of going through workspace.Client.doRequest, which
					// is the only place the token is normally attached — so without this
					// it 401s and the cleanup silently never happens.
					if token := strings.TrimSpace(os.Getenv("WORKSPACE_API_TOKEN")); token != "" {
						req.Header.Set("X-Workspace-Token", token)
					}
					resp, execErr := http.DefaultClient.Do(req)
					if execErr != nil {
						log.Printf("[BROWSER_CLEANUP] Startup cleanup failed: %v (browsers may still be running)", execErr)
					} else {
						resp.Body.Close()
						log.Printf("[BROWSER_CLEANUP] Startup cleanup: killed orphaned browser processes in workspace-api")
					}
				}
			} else {
				log.Printf("[BROWSER_CLEANUP] Startup cleanup: killed orphaned browser processes via --kill-all")
			}
		}()
	} else {
		log.Printf("[BROWSER_CLEANUP] Isolated instance: skipped workspace-wide startup cleanup")
	}

	// Generate API token for code execution mode per-tool endpoints. Tests and
	// local harnesses may set MCP_SERVER_API_TOKEN before startup so sibling
	// E2E processes can authenticate against this same server without exposing
	// a token read endpoint.
	api.apiToken = resolveServerAPIToken()
	// Agents get a token for their own session (pkg/common/bridge_token.go,
	// bridge_auth.go), signed with a secret that exists only in this
	// process's memory: never the API token, never in the environment, so no
	// child process can mint a token for another session. Set before any
	// shell client or bridge env is built.
	common.SetBridgeTokenSecret(resolveBridgeTokenSecret())
	// Neither the API token nor the signing secret stays in the environment
	// children inherit (tmux panes, live-attach terminals, anything exec'd with
	// the default env). Agents get only their own session's token.
	os.Unsetenv(envMCPServerAPIToken)
	os.Unsetenv("MCP_API_TOKEN")

	// Set env vars for code execution mode (mcpagent reads these as fallback).
	// MCP_API_URL may be explicitly configured for a rootless deployment; otherwise
	// GetCodeExecAPIURL selects a sensible Docker/native default.
	// MCP_BRIDGE_API_URL = host-reachable URL (for mcpbridge binary running on the host)
	os.Setenv("MCP_API_URL", api.GetCodeExecAPIURL())
	os.Setenv("MCP_BRIDGE_API_URL", api.GetAPIURL())
	seedMCPBridgeCodeExecRegistry(api.logger)

	// Load global secrets from GLOBAL_SECRET_* environment variables
	loadGlobalSecrets()
	if err := api.loadManagedGlobalSecrets(context.Background()); err != nil {
		log.Fatalf("Failed to load managed global secrets: %v", err)
	}

	// Setup routes
	router := mux.NewRouter()

	// CORS middleware
	router.Use(api.corsMiddleware)

	// Auth middleware - applies to all API routes
	// Note: AuthMiddleware handles skipping auth for public endpoints (login, register, health, shared)
	router.Use(AuthMiddleware)

	// API routes
	apiRouter := router.PathPrefix("/api").Subrouter()
	apiRouter.Use(api.apiRequestLogMiddleware)

	// Authentication API routes (public - no auth required, handled by AuthMiddleware)
	apiRouter.HandleFunc("/auth/register", api.handleRegister).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/auth/login", api.handleLogin).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/auth/logout", api.handleLogout).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/auth/me", api.handleGetCurrentUser).Methods("GET")
	apiRouter.HandleFunc("/auth/mode", api.handleGetAuthMode).Methods("GET")
	apiRouter.HandleFunc("/client-telemetry/chat-delivery", api.handleClientChatTelemetry).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/auth/users", requireWorkflowOwnerAccess(api.handleListAuthUsers)).Methods("GET", "OPTIONS")
	// Account management (docs/design/user_accounts_and_workflow_sharing.md):
	// the user directory in config/users.json; admins only.
	apiRouter.HandleFunc("/auth/password", api.handleChangeOwnPassword).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/auth/access-tokens", api.handleAccessTokens).Methods("GET", "POST")
	apiRouter.HandleFunc("/auth/access-tokens/{id}", api.handleAccessTokens).Methods("DELETE")
	apiRouter.HandleFunc("/oauth/mcp/register", api.handleMCPOAuthRegister).Methods("POST")
	apiRouter.HandleFunc("/oauth/mcp/authorize", api.handleMCPOAuthAuthorize).Methods("GET")
	apiRouter.HandleFunc("/oauth/mcp/token", api.handleMCPOAuthToken).Methods("POST")
	apiRouter.HandleFunc("/oauth/mcp/consent", api.handleMCPOAuthConsent).Methods("GET", "POST")
	apiRouter.HandleFunc("/oauth/mcp/connections", api.handleMCPOAuthConnections).Methods("GET")
	apiRouter.HandleFunc("/oauth/mcp/connections/{id}", api.handleMCPOAuthConnections).Methods("DELETE")
	apiRouter.HandleFunc("/oauth/cli/device", api.handleCLIOAuthDevice).Methods("POST")
	apiRouter.HandleFunc("/oauth/cli/consent", api.handleCLIOAuthConsent).Methods("GET", "POST")
	apiRouter.HandleFunc("/oauth/cli/token", api.handleCLIOAuthToken).Methods("POST")
	apiRouter.HandleFunc("/oauth/cli/revoke", api.handleCLIOAuthRevoke).Methods("POST")
	// Per-workflow ownership and sharing (workflow_access.go).
	apiRouter.HandleFunc("/workflow/access", api.handleGetWorkflowAccess).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/access", api.handleSetWorkflowAccess).Methods("PUT", "POST")
	apiRouter.HandleFunc("/users/directory", api.handleUserDirectory).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/share-tunnel/status", requireAdmin(api.handleGetShareTunnelStatus)).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/admin/users", requireAdmin(api.handleAdminListUsers)).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/admin/users", requireAdmin(api.handleAdminCreateUser)).Methods("POST")
	apiRouter.HandleFunc("/admin/users/{id}", requireAdmin(api.handleAdminUpdateUser)).Methods("PUT", "OPTIONS")
	apiRouter.HandleFunc("/admin/users/{id}", requireAdmin(api.handleAdminDeleteUser)).Methods("DELETE")
	apiRouter.HandleFunc("/workflow/user-permissions", requireWorkflowOwnerAccess(api.handleListWorkflowUserPermissions)).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/user-permissions", requireWorkflowOwnerAccess(api.handleUpsertWorkflowUserPermission)).Methods("PUT", "POST", "OPTIONS")
	apiRouter.HandleFunc("/workflow/user-permissions", requireWorkflowOwnerAccess(api.handleDeleteWorkflowUserPermission)).Methods("DELETE")
	// Multi-provider OAuth routes
	apiRouter.HandleFunc("/auth/start", api.handleAuthStart).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/auth/callback", api.handleAuthCallback).Methods("GET")
	apiRouter.HandleFunc("/auth/desktop/connect", api.handleDesktopConnect).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/auth/desktop/exchange", api.handleDesktopConnectExchange).Methods("POST", "OPTIONS")

	apiRouter.HandleFunc("/query", api.handleQuery).Methods("POST", "OPTIONS")
	AgentProfileRoutes(apiRouter, api.agentProfiles)
	apiRouter.HandleFunc("/agent-profiles/{id}/query", api.handleAgentProfileChatQuery).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/conversation", api.handleResolveAgentProfileConversation).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/conversation/new", api.handleRotateAgentProfileConversation).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/conversation/switch", api.handleSwitchAgentProfileConversation).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/conversations", api.handleListAgentProfileConversations).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/conversations/{session_id}", api.handleDeleteAgentProfileConversation).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/projects/{project_id}", api.handleDeleteAgentProfileProject).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/shared-projects", api.handleListSharedProjects).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/code/projects/{project_id}/shares", api.handleGetCodeShares).Methods("GET", "OPTIONS")
	// Admin inspection of Code (read-only, audited; admin checked in-handler).
	apiRouter.HandleFunc("/admin/code/workspaces", api.handleAdminListCodeWorkspaces).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/admin/code/workspaces/{owner}/{project_id}/files", api.handleAdminCodeFiles).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/admin/code/workspaces/{owner}/{project_id}/file", api.handleAdminCodeFile).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/admin/code/workspaces/{owner}/{project_id}/chats", api.handleAdminCodeChats).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/admin/code/workspaces/{owner}/{project_id}/chats/{session_id}", api.handleAdminCodeChat).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/admin/code/audit", api.handleAdminCodeAudit).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/admin/code/workspaces/{owner}/{project_id}/mcp", api.handleAdminCodeMCP).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/code/projects/{project_id}/shares", api.handlePutCodeShares).Methods("PUT")
	// Pins of Code chats whose Code is gone, once the workspace is up.
	go func() {
		time.Sleep(3 * time.Minute)
		api.sweepOrphanCodeSessionPins(context.Background())
	}()
	// A person's own MCP servers and secrets (docs/design/code_private_mcp.md).
	apiRouter.HandleFunc("/me/mcp/servers", api.handleListPersonalMCP).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/me/mcp/servers", api.handleAddPersonalMCP).Methods("POST")
	apiRouter.HandleFunc("/me/mcp/servers/{name}", api.handleRemovePersonalMCP).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/me/mcp/servers/{name}/connect", api.handleConnectPersonalMCP).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/me/mcp/servers/{name}/codes/{project_id}", api.handleSwitchPersonalMCP).Methods("PUT", "OPTIONS")
	apiRouter.HandleFunc("/me/secrets/{name}", api.handlePutPersonalSecret).Methods("PUT", "DELETE", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/shared-projects/{project_id}/files", api.handleListSharedProjectFiles).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/shared-projects/{project_id}/file", api.handleGetSharedProjectFile).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/agent-profiles/{id}/presentations/{presentationID}", api.handleAgentProfilePresentationDelete).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/work/folders", api.handleListWorkFolders).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/work/folders", api.handleAddWorkFolder).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/work/folders/{id}", api.handleDeleteWorkFolder).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/work/roots", api.handleGetWorkRoots).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/work/roots", api.handlePutWorkRoots).Methods("PUT", "OPTIONS")
	apiRouter.HandleFunc("/health", api.handleHealth).Methods("GET")
	apiRouter.HandleFunc("/capabilities", api.handleCapabilities).Methods("GET")
	CLISecurityRoutes(apiRouter, api.cliSecurityStore)
	apiRouter.HandleFunc("/cdp-check", api.handleCdpCheck).Methods("GET")
	apiRouter.HandleFunc("/downloads/chrome-cdp-macOS.zip", api.handleChromeCdpDownload).Methods("GET")
	apiRouter.HandleFunc("/downloads/cli/{file}", api.handleCliDownload).Methods("GET")
	apiRouter.HandleFunc("/llm-config/defaults", api.handleGetLLMDefaults).Methods("GET")
	apiRouter.HandleFunc("/llm-config/discovery", api.handleDiscoverLLMSetup).Methods("GET")
	apiRouter.HandleFunc("/llm-config/models/metadata", api.handleGetModelMetadata).Methods("GET")
	apiRouter.HandleFunc("/llm-config/azure/deployments", api.handleGetAzureDeployedModels).Methods("POST")
	apiRouter.HandleFunc("/llm-config/validate-key", api.handleValidateAPIKey).Methods("POST")
	apiRouter.HandleFunc("/llm-config/delegation-tiers", api.handleGetDelegationTierDefaults).Methods("GET")
	apiRouter.HandleFunc("/llm-config/providers", api.handleGetProviderManifest).Methods("GET")
	apiRouter.HandleFunc("/llm-config/providers/{provider}/models", api.handleGetProviderModels).Methods("GET")
	apiRouter.HandleFunc("/provider-connections", api.handleProviderConnections).Methods("GET", "POST", "OPTIONS")
	apiRouter.HandleFunc("/provider-connections/share-targets", api.handleProviderShareTargets).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/provider-connections/{connectionID}/status", api.handleProviderAccountStatus).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/provider-connections/{connectionID}/sign-out", api.handleProviderAccountSignOut).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/provider-accounts/product-defaults", api.handleProductDefaults).Methods("GET", "PUT", "OPTIONS")
	apiRouter.HandleFunc("/provider-accounts/costs", api.handleProviderAccountCosts).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/provider-connections/{connectionID}", api.handleProviderConnection).Methods("PATCH", "DELETE", "OPTIONS")
	apiRouter.HandleFunc("/provider-setup/sessions", api.handleStartProviderSetup).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/provider-setup/sessions/{id}", api.handleProviderSetupSession).Methods("GET", "DELETE", "OPTIONS")
	apiRouter.HandleFunc("/provider-setup/sessions/{id}/stream", api.handleProviderSetupStream).Methods("GET")
	apiRouter.HandleFunc("/session/cancel-turn", api.handleCancelCurrentTurn).Methods("POST")
	apiRouter.HandleFunc("/session/stop", api.handleStopSession).Methods("POST")
	apiRouter.HandleFunc("/session/clear", api.handleClearSession).Methods("POST")

	// Tool management routes (from tools.go)
	apiRouter.HandleFunc("/tools", api.handleGetTools).Methods("GET")
	apiRouter.HandleFunc("/tools/detail", api.handleGetToolDetail).Methods("GET")
	apiRouter.HandleFunc("/tools/enabled", api.handleSetEnabledTools).Methods("POST")
	apiRouter.HandleFunc("/tools/add", api.handleAddServer).Methods("POST")
	apiRouter.HandleFunc("/tools/edit", api.handleEditServer).Methods("POST")
	apiRouter.HandleFunc("/tools/remove", api.handleRemoveServer).Methods("POST")

	// Tool execution APIs - handlers provided by mcpagent/executor library
	// Pass server logger for proper debugging of session registry usage
	executorHandlers := executor.NewExecutorHandlers(api.mcpConfigPath, api.logger)
	executorHandlers.SetMCPServerResolver(api.resolveMCPServer)

	apiRouter.HandleFunc("/mcp/execute", api.requireOwnBodySession(executorHandlers.HandleMCPExecute)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/custom/execute", executorHandlers.HandleCustomExecute).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/virtual/execute", api.requireOwnBodySession(executorHandlers.HandleVirtualExecute)).Methods("POST", "OPTIONS")

	// Per-tool endpoints for code execution mode (bearer token auth, bypasses JWT)
	// LLM-generated code calls these directly, so they use API token auth instead of JWT.
	//
	// NOTE: The system prompt tool index lists custom tool categories (e.g. workspace_advanced)
	// and virtual tool categories (e.g. workflow) alongside real MCP servers. Claude Code agents
	// call them all via /tools/mcp/{server}/{tool}. The routeMCPRequest helper detects these
	// categories and redirects to the correct handler (custom or virtual).
	routeMCPRequest := func(w http.ResponseWriter, r *http.Request, server, tool string) {
		// Global bridge URLs carry the session in X-Session-ID. Preserve it in
		// context as well as the request header so workspace tools can resolve
		// session-scoped folder guards, working directories, and read-only host
		// grants (for example the native Chrome Downloads directory).
		if sid := strings.TrimSpace(r.Header.Get("X-Session-ID")); sid != "" {
			r = r.WithContext(context.WithValue(r.Context(), common.ChatSessionIDKey, sid))
		}
		if isMCPBridgeCustomToolCategory(server) {
			log.Printf("[ROUTE] Redirecting /tools/mcp/%s/%s → custom tool handler", server, tool)
			executorHandlers.HandlePerToolCustomRequest(w, r, tool)
			return
		}
		if isMCPBridgeVirtualToolCategory(server) {
			log.Printf("[ROUTE] Redirecting /tools/mcp/%s/%s → virtual tool handler", server, tool)
			executorHandlers.HandlePerToolVirtualRequest(w, r, tool)
			return
		}
		executorHandlers.HandlePerToolMCPRequest(w, r, server, tool)
		api.recordMCPBridgeCall(strings.TrimSpace(r.Header.Get("X-Session-ID")), server, tool)
	}

	toolsRouter := router.PathPrefix("/tools").Subrouter()
	toolsRouter.Use(bridgeAuthMiddleware(api.apiToken))
	toolsRouter.HandleFunc("/mcp/{server}/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		routeMCPRequest(w, r, vars["server"], vars["tool"])
	}).Methods("POST", "OPTIONS")
	toolsRouter.HandleFunc("/custom/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		log.Printf("[GLOBAL_ROUTE_DEBUG] Global custom tool request: tool=%s url=%s x-session-id=%s", vars["tool"], r.URL.Path, r.Header.Get("X-Session-ID"))
		if sid := strings.TrimSpace(r.Header.Get("X-Session-ID")); sid != "" {
			r = r.WithContext(context.WithValue(r.Context(), common.ChatSessionIDKey, sid))
		}
		executorHandlers.HandlePerToolCustomRequest(w, r, vars["tool"])
	}).Methods("POST", "OPTIONS")
	toolsRouter.HandleFunc("/virtual/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		executorHandlers.HandlePerToolVirtualRequest(w, r, vars["tool"])
	}).Methods("POST", "OPTIONS")

	// Session-scoped per-tool endpoints: /s/{session_id}/tools/...
	// These routes bake the session_id into the URL path, so the LLM-generated code
	// doesn't need to explicitly include session_id in request bodies.
	// The session_id is extracted from the path and injected as X-Session-ID header,
	// which the per-tool handler reads as a fallback when body session_id is empty.
	sessionToolsRouter := router.PathPrefix("/s/{session_id}/tools").Subrouter()
	sessionToolsRouter.Use(bridgeAuthMiddleware(api.apiToken))
	sessionToolsRouter.HandleFunc("/browser/packages/{package}", api.handlePlaywrightPackage).Methods("GET")
	sessionToolsRouter.HandleFunc("/browser/live", api.handlePlaywrightPublisher).Methods("GET")
	sessionToolsRouter.HandleFunc("/mcp/{server}/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		sid := vars["session_id"]
		r.Header.Set("X-Session-ID", sid)
		// MCP-style URLs can be redirected to custom workspace tools. Those
		// tools resolve folder guards from context, so mirror /tools/custom.
		ctx := context.WithValue(r.Context(), common.ChatSessionIDKey, sid)
		routeMCPRequest(w, r.WithContext(ctx), vars["server"], vars["tool"])
	}).Methods("POST", "OPTIONS")
	sessionToolsRouter.HandleFunc("/custom/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		sid := vars["session_id"]
		tool := vars["tool"]
		log.Printf("[SESSION_ROUTE_DEBUG] Session-scoped custom tool request: session=%s tool=%s url=%s", sid, tool, r.URL.Path)
		r.Header.Set("X-Session-ID", sid)
		// Inject ChatSessionIDKey so execute_shell_command can look up
		// the session's working directory and folder guard from the global map.
		ctx := context.WithValue(r.Context(), common.ChatSessionIDKey, sid)
		executorHandlers.HandlePerToolCustomRequest(w, r.WithContext(ctx), tool)
	}).Methods("POST", "OPTIONS")
	sessionToolsRouter.HandleFunc("/virtual/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		r.Header.Set("X-Session-ID", vars["session_id"])
		executorHandlers.HandlePerToolVirtualRequest(w, r, vars["tool"])
	}).Methods("POST", "OPTIONS")

	// MCP Config API routes (from mcp_config_routes.go)
	apiRouter.HandleFunc("/mcp-config", requireAdmin(api.handleGetMCPConfig)).Methods("GET")
	apiRouter.HandleFunc("/mcp-config", requireAdmin(api.handleSaveMCPConfig)).Methods("POST")
	apiRouter.HandleFunc("/mcp-config/discover", requireAdmin(api.handleDiscoverServers)).Methods("POST")
	apiRouter.HandleFunc("/mcp-config/status", requireAdmin(api.handleGetMCPConfigStatus)).Methods("GET")
	apiRouter.HandleFunc("/mcp-config/logs", requireAdmin(api.handleGetServerLogs)).Methods("GET")

	// Connector connection state (from oauth_routes.go)
	apiRouter.HandleFunc("/mcp/connect", requireAdmin(api.handleConnectServer)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/mcp/disconnect", requireAdmin(api.handleDisconnectServer)).Methods("POST", "OPTIONS")

	// Secrets encryption API routes (from secrets_routes.go)
	apiRouter.HandleFunc("/secrets/encrypt", api.handleEncryptSecret).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/secrets/decrypt", api.handleDecryptSecret).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/secrets/global", api.handleGetGlobalSecrets).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/secrets/global", api.handleManageGlobalSecret).Methods("POST", "PUT", "DELETE")
	apiRouter.HandleFunc("/secrets/global/reveal", api.handleRevealGlobalSecret).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/secrets/workflow/store", api.handleStoreWorkflowSecret).Methods("PUT", "OPTIONS")
	apiRouter.HandleFunc("/secrets/workflow/store/{name}", api.handleDeleteWorkflowSecret).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/secrets/workflow/stored", api.handleListStoredWorkflowSecrets).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow-provider-credentials/claude-code", api.handleGetWorkflowClaudeCodeCredential).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow-provider-credentials/claude-code", api.handleStoreWorkflowClaudeCodeCredential).Methods("PUT", "OPTIONS")
	apiRouter.HandleFunc("/workflow-provider-credentials/claude-code", api.handleDeleteWorkflowClaudeCodeCredential).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/workflow-provider-credentials/cursor-cli", api.handleGetWorkflowCursorCLICredential).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow-provider-credentials/cursor-cli", api.handleStoreWorkflowCursorCLICredential).Methods("PUT", "OPTIONS")
	apiRouter.HandleFunc("/workflow-provider-credentials/cursor-cli", api.handleDeleteWorkflowCursorCLICredential).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/workflow-provider-credentials/pi-cli", api.handleGetWorkflowPiCLICredential).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow-provider-credentials/pi-cli", api.handleStoreWorkflowPiCLICredential).Methods("PUT", "OPTIONS")
	apiRouter.HandleFunc("/workflow-provider-credentials/pi-cli", api.handleDeleteWorkflowPiCLICredential).Methods("DELETE", "OPTIONS")

	// Provider API keys (encrypted file storage for scheduled runs)
	apiRouter.HandleFunc("/provider-keys", api.handleSaveProviderKeys).Methods("PUT", "OPTIONS")
	apiRouter.HandleFunc("/provider-keys", api.handleLoadProviderKeys).Methods("GET", "OPTIONS")

	// Published LLMs (workspace-backed JSON storage)
	apiRouter.HandleFunc("/published-llms", api.handleSavePublishedLLMs).Methods("PUT", "OPTIONS")
	apiRouter.HandleFunc("/published-llms", api.handleLoadPublishedLLMs).Methods("GET", "OPTIONS")

	// Legacy bot-connector model profile. Direct chat and product chat use their
	// selected/profile model directly and do not load delegation tiers.
	apiRouter.HandleFunc("/delegation-tier-config", api.handleSaveDelegationTierConfig).Methods("PUT", "OPTIONS")
	apiRouter.HandleFunc("/delegation-tier-config", api.handleLoadDelegationTierConfig).Methods("GET", "OPTIONS")

	// OAuth API routes (from oauth_routes.go)
	apiRouter.HandleFunc("/oauth/start", requireAdmin(api.handleOAuthStart)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/oauth/callback", api.handleOAuthCallback).Methods("GET")
	apiRouter.HandleFunc("/oauth/status", api.handleOAuthStatus).Methods("GET")
	apiRouter.HandleFunc("/oauth/logout", requireAdmin(api.handleOAuthLogout)).Methods("POST", "OPTIONS")

	// Observer APIs removed - events are now stored by sessionID, no observers needed

	// Browser session tracking API
	apiRouter.HandleFunc("/browser/sessions", api.handleGetBrowserSessions).Methods("GET")
	apiRouter.HandleFunc("/browser/live/sessions", api.handleLiveBrowserSessions).Methods("GET")
	apiRouter.HandleFunc("/browser/live/{session}/stream", api.handleLiveBrowserStream).Methods("GET")
	apiRouter.HandleFunc("/browser/live/{session}/recording", api.handleBrowserRecording).Methods("GET", "POST")

	// Active Session API routes (from polling.go)
	apiRouter.HandleFunc("/sessions/active", api.handleGetActiveSessions).Methods("GET")
	// Combined poll for the app header (ModePresetBar + GlobalActivityMonitor):
	// active sessions + workflow schedule counts in one round trip.
	apiRouter.HandleFunc("/header-summary", api.handleGetHeaderSummary).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/events", api.handleGetSessionEvents).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/chat-artifacts/{artifact_id}", api.handleGetChatArtifact).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/ui-control", api.handleUIControl).Methods("POST")
	apiRouter.HandleFunc("/sessions/{session_id}/events/stream", api.handleSSEStream).Methods("GET")
	// Header + right-pane live stream (change notices only; chat keeps its own streams).
	apiRouter.HandleFunc("/live", api.handleLiveFeed).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/reconnect", api.handleReconnectSession).Methods("POST")
	apiRouter.HandleFunc("/sessions/{session_id}/status", api.handleGetSessionStatus).Methods("GET")
	// The product raw view receives only its owning chat's main terminal. All
	// child-pane enumeration and controls remain behind runtime diagnostics.
	apiRouter.HandleFunc("/sessions/{session_id}/main-terminal", api.handleGetMainTerminal).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/sessions/{session_id}/main-terminal/stream", api.handleMainTerminalStream).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/execution-tree", api.runtimeDiagnosticsHandler(api.handleGetSessionExecutionTree)).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/activity-tree", api.runtimeDiagnosticsHandler(api.handleGetSessionActivityTree)).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/dismiss", api.handleDismissSession).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/terminals", api.runtimeDiagnosticsHandler(api.handleListTerminals)).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}/events", api.runtimeDiagnosticsHandler(api.handleGetTerminalEvents)).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}", api.runtimeDiagnosticsHandler(api.handleGetTerminal)).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}", api.runtimeDiagnosticsHandler(api.handleDismissTerminal)).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}/complete", api.runtimeDiagnosticsHandler(api.handleCompleteTerminal)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}/fail", api.runtimeDiagnosticsHandler(api.handleFailTerminal)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}/refresh", api.runtimeDiagnosticsHandler(api.handleRefreshTerminal)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}/kill", api.runtimeDiagnosticsHandler(api.handleKillTerminal)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}/input", api.runtimeDiagnosticsHandler(api.handleSendTerminalInput)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}/key", api.runtimeDiagnosticsHandler(api.handleSendTerminalKey)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/terminals/{terminal_id}/resize", api.runtimeDiagnosticsHandler(api.handleResizeTerminal)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/terminals/size-hint", api.runtimeDiagnosticsHandler(api.handleTerminalSizeHint)).Methods("POST", "OPTIONS")
	// Live-attach (control-mode) WebSocket transport for the selected live tmux
	// terminal. Inert (404) only if tmux is too old; see terminal_live_attach.go.
	apiRouter.HandleFunc("/terminals/{terminal_id}/stream", api.runtimeDiagnosticsHandler(api.handleTerminalStream)).Methods("GET")

	// Streaming speech-to-text (agentprofiles.RuntimeCapabilities.Voice). Gated
	// per-request by profile_id inside the handler, not by whether this route is
	// registered — mirroring how Browser/Secrets stay one shared implementation
	// that products opt into via product.yaml rather than owning a copy.
	apiRouter.HandleFunc("/voice/stream", api.handleVoiceStream).Methods("GET")
	// First-run setup for the mic: the model is a one-time ~690MB download
	// that the composer asks about explicitly instead of the first click
	// silently waiting on it. status is what the progress bar polls.
	apiRouter.HandleFunc("/voice/status", api.handleVoiceStatus).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/voice/warm", api.handleVoiceWarm).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/voice/unload", api.handleVoiceUnload).Methods("POST", "OPTIONS")
	// Load the engine at server startup, not on the first mic click: loading
	// takes ~1-2s, and a user clicking before it finished used to sit looking
	// at a silent button. Only when the model is already on disk, though — a
	// machine that never used voice is not made to download 690MB just
	// because the server started; the composer offers that as an explicit
	// first-time step (/voice/warm above).
	if voiceManager.Status().Installed {
		voiceManager.Warm()
	}

	// LLM Guidance API routes
	apiRouter.HandleFunc("/sessions/{session_id}/llm-guidance", api.handleSetLLMGuidance).Methods("POST", "OPTIONS")

	apiRouter.HandleFunc("/sessions/{session_id}/live-input", api.handleLiveInputMessage).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/chat/submissions/{submission_id}", api.handleChatSubmissionStatus).Methods("GET")
	apiRouter.HandleFunc("/sessions/{session_id}/control", api.handleControlKey).Methods("POST", "OPTIONS")

	// Human Feedback API
	apiRouter.HandleFunc("/human-feedback/submit", api.handleSubmitHumanFeedback).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/human-feedback/pending", api.handleListPendingHumanFeedback).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/report-human-inputs", requireReportHumanInputAccess(false, api.handleListReportHumanInputs)).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/report-human-inputs/aggregate", api.handleListReportHumanInputsAggregate).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/report-human-inputs", requireReportHumanInputAccess(true, api.handleCreateReportHumanInput)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/report-human-inputs/{input_id}/answer", requireReportHumanInputAccess(true, api.handleAnswerReportHumanInput)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/report-human-inputs/{input_id}/dismiss", requireReportHumanInputAccess(true, api.handleDismissReportHumanInput)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/report-human-inputs/{input_id}/consume", requireReportHumanInputAccess(true, api.handleConsumeReportHumanInput)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/workflow/pulse-module-state", api.handleGetPulseModuleState).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/pulse-findings", api.handleGetPulseFindings).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/pulse-reviews", api.handleGetPulseReviews).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/pulse-agent-metrics", api.handleGetPulseAgentMetrics).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/pulse-impact", api.handleGetPulseImpact).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/pulse-context", api.handleGetPulseContext).Methods("GET", "OPTIONS")

	// Workflow running-session API (decoupled from chat session storage).
	apiRouter.HandleFunc("/workflow/running", api.handleListRunningWorkflows).Methods("GET")
	apiRouter.HandleFunc("/workflow/running/{session_id}", api.handleGetRunningWorkflow).Methods("GET")
	apiRouter.HandleFunc("/workflow/running/{session_id}", api.handleUpdateRunningWorkflow).Methods("PATCH", "OPTIONS")

	// Global cost ledger summary.
	apiRouter.HandleFunc("/cost/summary", api.handleCostSummary).Methods("GET")
	apiRouter.HandleFunc("/cost/overview", api.handleCostOverview).Methods("GET")

	// Inspector debug panel — opt-in per-session timeline of
	// structured InspectorEvents emitted by the LLM adapters. See
	// inspector_routes.go and internal/inspector/store.go.
	apiRouter.HandleFunc("/inspector", api.handleInspectorSessions).Methods("GET")
	apiRouter.HandleFunc("/inspector/{session_id}", api.handleInspectorEvents).Methods("GET")
	apiRouter.HandleFunc("/inspector/{session_id}", api.handleInspectorClear).Methods("DELETE")

	// Chat history (read-only, persisted to workspace)
	ChatHistoryRoutes(router, api)

	// Slack Feedback API routes
	SlackFeedbackRoutes(router, api)

	// Slack connection registry (per-workflow Slack apps)
	SlackConnectionRoutes(router, api)

	// Gmail (outbound-only) config/status/test API routes
	GmailFeedbackRoutes(router, api)
	GmailConnectionRoutes(router, api)
	GmailOAuthRoutes(router, api)
	GmailOAuthClientRoutes(router, api)

	// Per-user notification preferences (Slack channel, WhatsApp number)
	NotificationPreferencesRoutes(router)

	// Initialize Bot Conversation Manager
	workspaceURL := os.Getenv("WORKSPACE_API_URL")
	if workspaceURL == "" {
		workspaceURL = "http://127.0.0.1:8081"
	}
	botManager := services.NewBotConversationManager(chatStore, configPath, workspaceURL)
	botManager.SetEventSubscriber(NewBotEventSubscriberAdapter(eventStore))
	// Bot sessions use ONLY delegation tier config from DB for LLM selection — no server defaults needed
	api.botManager = botManager
	// Every server hook (session start, routing, turn building) in one place,
	// shared with the bot dry-run tests.
	api.wireBotManager(botManager)
	// Install the chat injector used by background-agent and workflow lifecycle
	// messages. Human-input requests are intentionally not relayed through this
	// path; users answer those directly in the correlated UI card.
	virtualtools.SetChatInjector(func(ctx context.Context, sessionID, userID, message string) error {
		api.executeSyntheticTurn(sessionID, message)
		return nil
	})
	// Install the bot manager as the spawn listener. Any tool that registers a
	// parent chat (run_full_workflow, scheduled runs, …) will now
	// automatically mirror its background session's agent messages into the
	// parent's Slack thread — no per-tool hooks required.
	virtualtools.SetSpawnListener(botManager)

	// Wire bot session checker for human feedback (skip 2-min delay for bot sessions)
	feedbackStore := virtualtools.GetHumanFeedbackStore()
	if feedbackStore != nil {
		feedbackStore.SetBotSessionChecker(func(sessionID string) bool {
			return botManager.IsBotSession(sessionID)
		})
	}

	// Register Slack as a bot connector when the platform switch is on, any
	// owner-saved channel route exists, or any enabled workflow- or
	// crew-owned Slack app exists (its own socket needs the handler).
	// Per-message and per-route gates still silence unrouted channels while
	// the platform switch is off.
	if slackSvc != nil {
		api.migrateCrewBotScopes(context.Background(), slackSvc)
		botConfig, _ := chatStore.GetBotConnectorConfig(context.Background(), "slack")
		if slackBotConnectorWantedAtStartup(botConfig, slackSvc) && registerSlackBotConnector(botManager, slackSvc) {
			log.Printf("✅ Slack bot mode enabled")
		}
	}

	// Register WhatsApp connector unless explicitly disabled. Each workspace
	// user gets a separate WhatsApp Web client and session DB under the
	// session directory, so users can pair their own bot accounts independently.
	// Set WHATSAPP_ENABLED=false to disable and optionally WHATSAPP_SESSION_DIR
	// to override the default session directory.
	//
	// DB usage note: this server otherwise avoids databases and persists to
	// workspace/ files only. WhatsApp is an intentional exception because
	// whatsmeow needs a transactional SQLite store for its Signal-protocol
	// keys (identity, sessions, prekeys). The file is agent-local — not
	// shared infra, not replicated across nodes — so it behaves more like a
	// protocol-state cache than a "database" in the architectural sense.
	// Deleting the file and re-pairing via QR fully restores functionality.
	whatsappEnabled := strings.ToLower(strings.TrimSpace(os.Getenv("WHATSAPP_ENABLED")))
	if whatsappEnabled != "false" && whatsappEnabled != "0" {
		sessionDir := os.Getenv("WHATSAPP_SESSION_DIR")
		if sessionDir == "" {
			if legacyDBPath := strings.TrimSpace(os.Getenv("WHATSAPP_SESSION_DB")); legacyDBPath != "" {
				sessionDir = filepath.Join(filepath.Dir(legacyDBPath), "whatsapp-sessions")
			} else {
				sessionDir = filepath.Join(fsutil.WorkspaceDocsRoot(), "config", "whatsapp-sessions")
			}
		}
		whatsappManager := services.NewWhatsAppServiceManager(sessionDir)
		whatsappManager.SetWorkflowAccessFunc(api.checkWhatsAppWorkflowAccess)
		botManager.RegisterConnector(whatsappManager)
		api.whatsappManager = whatsappManager
		if err := whatsappManager.StartListening(context.Background()); err != nil {
			log.Printf("❌ WhatsApp service failed to start: %v", err)
		} else {
			log.Printf("✅ WhatsApp bot mode enabled (session_dir=%s)", sessionDir)
		}
	}

	// Register bot routes
	BotRoutes(router, api)
	virtualtools.SetSlackMessageHandler(api.sendSlackMessageFromTool)
	virtualtools.SetSlackCLIHandler(api.slackCLIFromTool)
	if slackSvc != nil {
		slackSvc.SetTriggerHandler(api.dispatchSlackTrigger)
	}
	if api.whatsappManager != nil {
		WhatsAppRoutes(router, api.whatsappManager, api.whatsappDefaultProfileResolver)
		// Unrouted WhatsApp messages on a pairing with a default product
		// profile run in that profile's own conversation; the product's own
		// @tokens pick one of its other profiles.
		api.whatsappManager.SetProfileRouter(api.whatsappProfileRouter)
		// A voice note is transcribed on-device before it ever reaches a
		// conversation — the chat never learns it arrived as audio at all.
		// Never triggers the engine's one-time model download (that can
		// take minutes): a not-yet-installed model asks the parent to set
		// it up instead of blocking the message handler on a download.
		api.whatsappManager.SetVoiceTranscriber(func(ctx context.Context, path string) (string, error) {
			if !voiceManager.Status().Installed {
				return "", services.ErrVoiceNotInstalled
			}
			samples, err := voicestt.DecodeFile(ctx, path)
			if err != nil {
				return "", fmt.Errorf("decode voice note: %w", err)
			}
			return voiceManager.Transcribe(ctx, samples)
		})
	}
	// Set activity callback for event store to update session LastActivity when events are added
	eventStore.SetActivityCallback(func(sessionID string) {
		api.updateSessionActivity(sessionID)
	})

	// Start background cleanup goroutine to mark inactive sessions (10 minute timeout)
	go api.cleanupInactiveSessions()
	go api.watchScheduleSummaryForLiveFeed()
	go api.warmLLMConfigCaches()
	go preloadProviderAccountSettings()

	// Initialize and start the cron scheduler
	// Set SCHEDULER_ENABLED=false in .env to disable on secondary machines sharing the same workspace files.
	schedulerCtx, schedulerCancel := context.WithCancel(context.Background())
	defer schedulerCancel()
	schedulerSvc := NewSchedulerService(api)
	api.scheduler = schedulerSvc
	productScheduleSvc := NewProductScheduleService(api, profileRegistry)
	api.productSchedules = productScheduleSvc
	// Recover accepted conversation turns before either scheduler can compete
	// for those same session lanes. Both services are already wired so a
	// recovered turn sees the normal runtime capabilities.
	api.recoverConversationTurnQueue(schedulerCtx)
	if os.Getenv("SCHEDULER_ENABLED") == "false" {
		log.Printf("[SCHEDULER] Disabled via SCHEDULER_ENABLED=false — skipping cron execution on this machine")
	} else {
		go func() {
			if err := schedulerSvc.Start(schedulerCtx); err != nil {
				log.Printf("[SCHEDULER] Error: %v", err)
			}
		}()
	}

	// Register scheduler routes
	if os.Getenv("SCHEDULER_ENABLED") != "false" {
		go productScheduleSvc.Start(schedulerCtx)
	}
	SchedulerRoutes(router, schedulerSvc)
	WorkflowWebhookRoutes(router, schedulerSvc)
	ProductWebhookRoutes(router, productScheduleSvc)

	// Workflow API routes
	apiRouter.HandleFunc("/workflow/create", requireWorkflowCreateAccess(api.handleCreateWorkflow)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/workflow/status", api.handleGetWorkflowStatus).Methods("GET")
	apiRouter.HandleFunc("/workflow/update", api.handleUpdateWorkflow).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/workflow/constants", orchtypes.HandleWorkflowConstants).Methods("GET")
	apiRouter.HandleFunc("/workflow/active-executions", api.handleGetActiveExecutions).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/builder-session", api.handleGetWorkflowBuilderSession).Methods("GET", "OPTIONS")
	// Headless report preview data (preview_report tool). The only API paths a
	// report-preview scoped token can reach; see report_preview_routes.go.
	apiRouter.HandleFunc("/workflow/report-preview/file", api.handleReportPreviewFile).Methods("GET")
	apiRouter.HandleFunc("/workflow/report-preview/query", api.handleReportPreviewQuery).Methods("POST")
	apiRouter.HandleFunc("/workflow/report-preview/costs", api.handleReportPreviewMetrics).Methods("GET")
	apiRouter.HandleFunc("/workflow/report-preview/media-url", api.handleReportMediaURL).Methods("POST")
	apiRouter.HandleFunc("/workflow/report-preview/run", api.handleReportRun).Methods("POST")
	apiRouter.HandleFunc("/workflow/report-media", api.handleReportMediaStream).Methods("GET", "HEAD")

	// Generic AgentWorks chat defaults (skills, servers, secrets, browser).
	MultiAgentConfigRoutes(apiRouter)

	// Workspace API reverse proxy (auth-protected) — frontend calls /api/wp/* instead of /workspace/*
	apiRouter.PathPrefix("/wp/").Handler(workspaceProxyHandler())

	// Consolidated workspace state endpoint (NEW - loads everything in one call)
	apiRouter.HandleFunc("/workspace/state", api.handleLoadWorkspaceState).Methods("GET", "OPTIONS")

	// Focused workflow endpoints used for mutations and incremental refreshes.
	apiRouter.HandleFunc("/workflow/run-folders", api.handleGetRunFolders).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/run-folder", api.handleCreateRunFolder).Methods("POST", "OPTIONS")
	// /workflow/progress endpoint removed — steps_done.json progress tracking no longer consumed by frontend
	apiRouter.HandleFunc("/workflow/run-folder", requireWorkflowWriteAccess(api.handleDeleteRunFolder)).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/workflow/learnings", requireWorkflowWriteAccess(api.handleDeleteStepLearnings)).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/workflow/learnings/all", api.handleGetAllStepLearnings).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/variable-groups", api.handleGetVariableGroups).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/variable-groups", requireWorkflowWriteAccess(api.handleUpdateVariableGroups)).Methods("POST", "PUT", "OPTIONS")
	apiRouter.HandleFunc("/workflow/logs", api.handleGetExecutionLogs).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/logs/webhook-payload", api.handleGetExecutionWebhookPayload).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/logs/file", api.handleGetLogFile).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/costs", api.handleGetCosts).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/review-data", api.handleGetWorkflowReviewData).Methods("GET", "OPTIONS")

	// Auto-improvement framework — see docs/workflow/auto_improvement_framework.md
	apiRouter.HandleFunc("/workflow/builder-doc", api.handleGetBuilderDoc).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/org-dashboard/notifications", api.handleGetOrgDashboardNotifications).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/plan-changelog", api.handleGetPlanChangelog).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/plan-changelog/prune", requireWorkflowWriteAccess(api.handlePrunePlanChangelog)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/workflow/framework-health", api.handleGetFrameworkHealth).Methods("GET", "OPTIONS")

	// Plan and Step Config API routes
	apiRouter.HandleFunc("/external/v1/tools", api.handleExternalTools).Methods("GET")
	apiRouter.HandleFunc("/external/v1/call", api.handleExternalCall).Methods("POST")
	apiRouter.HandleFunc("/relays/{id}/runs", api.handleStartRelayRun).Methods("POST")
	apiRouter.HandleFunc("/relays/{id}/runs/{run}", api.handleGetRelayRun).Methods("GET")
	apiRouter.HandleFunc("/external/v1/files/content", api.handleExternalAssetContent).Methods("GET", "HEAD")
	apiRouter.HandleFunc("/external/v1/mcp", api.handleExternalMCP).Methods("POST", "GET", "DELETE")
	apiRouter.HandleFunc("/external/v1/skill.md", api.handleExternalSkillMD).Methods("GET")
	apiRouter.HandleFunc("/external/v1/skill.zip", api.handleExternalSkillZIP).Methods("GET")
	apiRouter.HandleFunc("/external/v1/agentworks.plugin", api.handleExternalPlugin).Methods("GET")
	apiRouter.HandleFunc("/workflow/plan/update-step", requireWorkflowWriteAccess(api.handleUpdatePlanStep)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/workflow/plan/update-step-config", requireWorkflowWriteAccess(api.handleUpdateStepConfig)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/workflow/plan/batch-update-steps", requireWorkflowWriteAccess(api.handleBatchUpdateSteps)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/workflow/plan/delete-step", requireWorkflowWriteAccess(api.handleDeleteStep)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/workflow/plan/add-step", requireWorkflowWriteAccess(api.handleAddStep)).Methods("POST", "OPTIONS")
	// Dynamic report system. The frontend ReportViewer loads db/reports/index.html
	// directly; HTML pages read durable data through window.report.

	apiRouter.HandleFunc("/workflow/backup", api.handleGetWorkflowBackup).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/publish", api.handleGetWorkflowPublish).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/notifications", api.handleGetWorkflowNotifications).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflow/publish/secret", requireWorkflowWriteAccess(api.handleGetWorkflowPublishSecret)).Methods("GET", "OPTIONS")
	// Manifest-backed workflow API routes (file-backed workflow definitions)
	apiRouter.HandleFunc("/workflows/summary", api.handleGetWorkflowsSummary).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflows/overview", api.handleGetWorkflowsOverview).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflows/manifests", api.handleListWorkflowManifests).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflows/knowledgebase-sources", api.handleWorkflowKnowledgebaseSources).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflows/manifest", api.handleGetWorkflowManifest).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflows/goal-setup", api.handleWorkflowGoalSetup).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflows/goal-setup", requireWorkflowWriteAccess(api.handleWorkflowGoalSetup)).Methods("POST")
	apiRouter.HandleFunc("/workflows/manifest", requireWorkflowCreateAccess(api.handleCreateWorkflowManifest)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/workflows/manifest", requireWorkflowWriteAccess(api.handleUpdateWorkflowManifest)).Methods("PUT", "OPTIONS")
	apiRouter.HandleFunc("/workflows/manifest", requireWorkflowWriteAccess(api.handleDeleteWorkflowManifest)).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/workflows/folder", requireWorkflowWriteAccess(api.handleDeleteWorkflowFolder)).Methods("DELETE", "OPTIONS")
	apiRouter.HandleFunc("/workflows/manifest/duplicate", requireWorkflowCreateAccess(api.handleDuplicateWorkflowManifest)).Methods("POST", "OPTIONS")
	apiRouter.HandleFunc("/playbooks", api.handleListPlaybooks).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflows/playbooks", api.handleListInstalledPlaybooks).Methods("GET", "OPTIONS")
	apiRouter.HandleFunc("/workflows/playbooks/install", requireWorkflowWriteAccess(api.handleInstallPlaybook)).Methods("POST", "OPTIONS")

	// Skills API routes (from skill_routes.go)
	RegisterSkillRoutes(apiRouter, api)

	// Note: System skills sync runs inside the workspace Docker container (workspace/skill_sync.go)
	// The backend server only proxies skill API calls to the workspace.

	// Sub-agent template API routes (from subagent_routes.go)
	RegisterSubAgentRoutes(apiRouter, api)

	// User-defined command routes (from command_routes.go)
	RegisterCommandRoutes(apiRouter, api)

	// Public file sharing routes — filepath passed as base64 query param
	apiRouter.HandleFunc("/public/file", api.handlePublicFile).Methods("GET")
	apiRouter.HandleFunc("/public/folder", api.handlePublicFolder).Methods("GET")
	apiRouter.HandleFunc("/public/folder/download", api.handlePublicFolderDownload).Methods("GET")

	// pprof routes for profiling (must be before static file serving)
	router.PathPrefix("/debug/pprof/").Handler(http.DefaultServeMux)

	// Pre-bind listener so we can support dynamic port (port 0) and report the actual port
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", config.Host, config.Port))
	if err != nil {
		log.Fatalf("Failed to listen on %s:%d: %v", config.Host, config.Port, err)
	}
	actualPort := listener.Addr().(*net.TCPAddr).Port
	SetShareTunnelServerPort(actualPort)

	// Dynamically serve runtime-config.js so the frontend learns the real ports.
	// In packaged/desktop mode ports are dynamic (--port 0), so the static file's
	// hardcoded values are wrong. Serve same-origin URLs for the agent API and
	// the workspace URL passed via WORKSPACE_API_URL env var.
	router.HandleFunc("/runtime-config.js", func(w http.ResponseWriter, r *http.Request) {
		workspaceURL := os.Getenv("WORKSPACE_API_URL")
		if workspaceURL == "" {
			workspaceURL = fmt.Sprintf("http://localhost:%d", actualPort)
		}
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprint(w, runtimeFrontendConfigJS(actualPort, workspaceURL))
	}).Methods("GET")

	// OAuth discovery must resolve before the SPA fallback.
	router.HandleFunc("/.well-known/oauth-protected-resource", api.handleMCPOAuthProtectedResource).Methods("GET")
	router.HandleFunc(mcpOAuthProtectedResourcePath, api.handleMCPOAuthProtectedResource).Methods("GET")
	router.HandleFunc(mcpOAuthMetadataPath, api.handleMCPOAuthMetadata).Methods("GET")

	// Headless report preview page (preview_report tool): embedded in the
	// binary, so it works wherever the server runs regardless of how the SPA
	// is served. Registered before the SPA catch-all below.
	router.HandleFunc(reportPreviewPagePath, api.reportPreviewPageHandler).Methods("GET")
	router.HandleFunc(reportPreviewPagePath+"report-preview.js", api.reportPreviewScriptHandler).Methods("GET")

	// Static file serving (for frontend). Unknown GET/HEAD routes fall back to
	// index.html so dedicated SPA URLs like /report, /file, and /folder work
	// when opened directly in a browser. Defaults to cwd-relative "./static/"
	// (unchanged); STATIC_DIR overrides it, see staticFrontendDir.
	router.PathPrefix("/").Handler(spaStaticFileHandler(staticFrontendDir()))

	// Create HTTP server
	srv := &http.Server{
		WriteTimeout: 0,                 // No write timeout — long-running tool calls (sub-agents) can take 30+ minutes
		ReadTimeout:  time.Second * 30,  // Read timeout for incoming requests
		IdleTimeout:  time.Second * 300, // 5 min idle timeout to prevent early closes during long queries
		Handler:      router,
	}

	// Initialize tool cache BEFORE starting HTTP server so the first getTools()
	// request from the frontend gets real data instead of an empty list.
	fmt.Printf("🔄 Initializing tool cache on server startup...\n")
	api.initializeToolCache()

	// Start the coding-CLI rate-limit watchdog: force-stops sessions whose tmux
	// pane is parked on a provider usage/rate-limit wall, so they do not wedge
	// "running" forever and lock the UI.
	api.startCodingTmuxRateLimitWatchdog()

	// Sync system skills (currently skill-creator; see GetSystemSkills) in
	// background. The agent-browser skill is builtin —
	// served from code, never installed to the skills/ folder.
	go func() {
		syncCtx, syncCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer syncCancel()
		workspaceAPIURL := os.Getenv("WORKSPACE_API_URL")
		if workspaceAPIURL == "" {
			workspaceAPIURL = "http://127.0.0.1:8081"
		}
		installed, errs := todo_creation_human.SyncSystemSkills(syncCtx, workspaceAPIURL)
		if len(errs) > 0 {
			for _, e := range errs {
				log.Printf("[SKILLS] %s", e)
			}
		}
		if installed > 0 {
			log.Printf("[SKILLS] ✅ Installed %d system skills on startup", installed)
		}
	}()

	// Start server in a goroutine
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	fmt.Printf("✅ Server started on %s:%d\n", config.Host, actualPort)
	fmt.Printf("DynamicPort: %d\n", actualPort)
	fmt.Printf("🔗 API endpoint: http://%s:%d/api/query\n", config.Host, actualPort)
	fmt.Printf("📡 Polling API: http://%s:%d/api/sessions/{session_id}/events\n", config.Host, actualPort)

	// Wait for interrupt signal to gracefully shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	<-c
	stopCLIUpdates()

	fmt.Println("\n🛑 Shutting down server...")

	// Cancel active agents first. Coding-agent tmux sessions are owned by those
	// turns, so killing tmux before the handlers observe cancellation can race
	// with capture/poll commands and make shutdown hang.
	fmt.Println("⏹️ Canceling active agent work...")
	cancelStart := time.Now()
	stopNativeTranscriptRecovery()
	api.cancelActiveWorkForShutdown()
	fmt.Printf("✅ Active agent work canceled (%s)\n", time.Since(cancelStart).Round(time.Millisecond))

	// An internet share tunnel must not outlive the server it exposes.
	StopShareTunnel()

	// Stop background discovery
	fmt.Println("⏹️ Stopping background tool discovery...")
	discoveryStart := time.Now()
	api.stopPeriodicRefresh()
	fmt.Printf("✅ Background tool discovery stopped (%s)\n", time.Since(discoveryStart).Round(time.Millisecond))

	// Create a deadline for HTTP handlers to observe cancellation and return.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Shutdown server before final process cleanup. If a handler is still stuck,
	// continue cleanup anyway so detached tmux/browser sessions do not survive.
	fmt.Println("⏳ Waiting for HTTP handlers to stop (15s max)...")
	httpStart, stopHTTPProgress := beginShutdownProgress("waiting for HTTP handlers")
	if err := srv.Shutdown(ctx); err != nil {
		stopHTTPProgress()
		elapsed := time.Since(httpStart).Round(time.Millisecond)
		log.Printf("Server graceful shutdown timed out after %s: %v", elapsed, err)
		fmt.Printf("⚠️ HTTP graceful shutdown timed out after %s: %v\n", elapsed, err)
	} else {
		stopHTTPProgress()
		fmt.Printf("✅ HTTP server stopped (%s)\n", time.Since(httpStart).Round(time.Millisecond))
	}

	// Close all MCP session connections to prevent orphaned subprocesses
	fmt.Println("🧹 Closing all MCP sessions...")
	mcpStart, stopMCPProgress := beginShutdownProgress("closing MCP sessions")
	mcpagent.CloseAllSessions()
	stopMCPProgress()
	fmt.Printf("✅ MCP sessions closed (%s)\n", time.Since(mcpStart).Round(time.Millisecond))

	// Kill all active browser daemons and Chrome processes so they don't linger
	fmt.Println("🧹 Killing all browser sessions...")
	browserStart, stopBrowserProgress := beginShutdownProgress("killing browser sessions")
	browser.KillAllTrackedSessions()
	stopBrowserProgress()
	fmt.Printf("✅ Browser session cleanup finished (%s)\n", time.Since(browserStart).Round(time.Millisecond))
	fmt.Println("🧹 Cleaning coding-agent tmux sessions...")
	codingStart, stopCodingProgress := beginShutdownProgress("cleaning coding-agent tmux sessions")
	cleanupCodingAgentInteractiveSessions("shutdown")
	stopCodingProgress()
	fmt.Printf("✅ Coding-agent tmux cleanup finished (%s)\n", time.Since(codingStart).Round(time.Millisecond))

	fmt.Println("✅ Server shutdown complete")
}

func beginShutdownProgress(label string) (time.Time, func()) {
	start := time.Now()
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Printf("⏳ Still %s (%s elapsed)\n", label, time.Since(start).Round(time.Second))
			}
		}
	}()
	return start, func() {
		once.Do(func() {
			close(done)
		})
	}
}

func cleanupCodingAgentInteractiveSessions(phase string) {
	cleanupProvider := func(name string, cleanup func(context.Context) error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		start, stopProgress := beginShutdownProgress(fmt.Sprintf("cleaning %s", name))
		fmt.Printf("  • %s cleanup started (5s max)\n", name)
		if err := cleanup(ctx); err != nil {
			stopProgress()
			elapsed := time.Since(start).Round(time.Millisecond)
			log.Printf("[%s] %s cleanup failed after %s: %v", name, phase, elapsed, err)
			fmt.Printf("  ⚠️ %s cleanup failed after %s: %v\n", name, elapsed, err)
			return
		}
		if ctx.Err() != nil {
			stopProgress()
			elapsed := time.Since(start).Round(time.Millisecond)
			log.Printf("[%s] %s cleanup context ended after %s: %v", name, phase, elapsed, ctx.Err())
			fmt.Printf("  ⚠️ %s cleanup context ended after %s: %v\n", name, elapsed, ctx.Err())
			return
		}
		stopProgress()
		fmt.Printf("  ✅ %s cleanup done (%s)\n", name, time.Since(start).Round(time.Millisecond))
	}

	cleanupProvider("CLAUDE-CODE", cleanupClaudeCodeProviderSessions)
	cleanupProvider("CODEX-CLI", cleanupCodexCLIProviderSessions)
	cleanupProvider("CURSOR-CLI", cleanupCursorCLIProviderSessions)
	cleanupProvider("PI-CLI", cleanupPiCLIProviderSessions)
}

func (api *StreamingAPI) cancelActiveWorkForShutdown() {
	if api == nil {
		return
	}

	var agentCancels []context.CancelFunc
	api.agentCancelMux.Lock()
	for sessionID, cancelFunc := range api.agentCancelFuncs {
		if cancelFunc != nil {
			agentCancels = append(agentCancels, cancelFunc)
		}
		delete(api.agentCancelFuncs, sessionID)
	}
	api.agentCancelMux.Unlock()
	for _, cancelFunc := range agentCancels {
		cancelFunc()
	}

	var workflowCancels []context.CancelFunc
	api.workflowOrchestratorContextMux.Lock()
	for queryID, cancelFunc := range api.workflowOrchestratorContexts {
		if cancelFunc != nil {
			workflowCancels = append(workflowCancels, cancelFunc)
		}
		delete(api.workflowOrchestratorContexts, queryID)
	}
	api.workflowOrchestratorContextMux.Unlock()
	for _, cancelFunc := range workflowCancels {
		cancelFunc()
	}

	sessionIDs := make([]string, 0)
	seen := make(map[string]struct{})
	api.activeSessionsMux.RLock()
	for sessionID := range api.activeSessions {
		seen[sessionID] = struct{}{}
		sessionIDs = append(sessionIDs, sessionID)
	}
	api.activeSessionsMux.RUnlock()

	api.sessionQueryIDMux.Lock()
	for sessionID := range api.sessionQueryIDs {
		if _, ok := seen[sessionID]; !ok {
			seen[sessionID] = struct{}{}
			sessionIDs = append(sessionIDs, sessionID)
		}
	}
	api.sessionQueryIDs = map[string][]string{}
	api.sessionQueryIDMux.Unlock()

	for _, sessionID := range sessionIDs {
		api.cancelBackgroundAgents(sessionID)
		api.cancelTrackedExecutionsForSession(sessionID)
		api.setSessionBusy(sessionID, false)
		api.setSyntheticTurn(sessionID, false)
	}

	api.workshopChatSessions.Range(func(key, value interface{}) bool {
		if ws, ok := value.(interface{ Close() }); ok {
			ws.Close()
		}
		api.workshopChatSessions.Delete(key)
		return true
	})
}

// GetAPIURL returns the base URL for the API server
// It handles replacing 0.0.0.0 with 127.0.0.1 for local loopback calls
func (api *StreamingAPI) GetAPIURL() string {
	host := api.config.Host
	if host == "0.0.0.0" || host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d", host, api.config.Port)
}

// GetCodeExecAPIURL returns the API URL as seen from wherever shell commands execute.
// In Docker mode, shell commands run inside the workspace-api container and need
// host.docker.internal to reach the Go server on the host.
// In native mode, shell commands run directly on the host, so they use 127.0.0.1.
func (api *StreamingAPI) GetCodeExecAPIURL() string {
	// An explicitly supplied URL wins. A rootless service and the shell commands
	// it launches share the same host, so its deployment sets this to loopback.
	// Do this before inferring topology from WORKSPACE_API_URL: localhost there
	// used to mean "Docker maps a host port", but it also describes a native
	// rootless workspace service.
	if configured := strings.TrimRight(strings.TrimSpace(os.Getenv("MCP_API_URL")), "/"); configured != "" {
		return configured
	}
	if common.IsNativeWorkspace() {
		return fmt.Sprintf("http://127.0.0.1:%d", api.config.Port)
	}

	wsURL := getWorkspaceAPIURL()
	// If workspace API points to localhost (Docker-mapped port), shell commands
	// run inside Docker and need host.docker.internal to reach the host
	if strings.Contains(wsURL, "localhost") || strings.Contains(wsURL, "127.0.0.1") {
		return fmt.Sprintf("http://host.docker.internal:%d", api.config.Port)
	}
	// In Docker Compose networking, use the Go server's service name or host
	return api.GetAPIURL()
}

func latestAssistantTextFromHistory(history []llmtypes.MessageContent) string {
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if msg.Role != llmtypes.ChatMessageTypeAI {
			continue
		}
		var textParts []string
		for _, part := range msg.Parts {
			if t, ok := part.(llmtypes.TextContent); ok && strings.TrimSpace(t.Text) != "" {
				textParts = append(textParts, t.Text)
			}
		}
		if len(textParts) > 0 {
			return strings.TrimSpace(strings.Join(textParts, "\n"))
		}
	}
	return ""
}

// Scoped (workflow/project/product) secrets take priority on name collision.
// If selectedGlobalNames is non-nil, only global secrets whose name is in the list are included.
func mergeGlobalSecrets(scopedSecrets []struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}, selectedGlobalNames *[]string) []struct {
	Name  string `json:"name"`
	Value string `json:"value"`
} {
	globals := getGlobalSecrets()
	if len(globals) == 0 {
		return scopedSecrets
	}
	// Build filter set from selected global names (nil = include all)
	var allowedGlobals map[string]bool
	if selectedGlobalNames != nil {
		allowedGlobals = make(map[string]bool, len(*selectedGlobalNames))
		for _, name := range *selectedGlobalNames {
			allowedGlobals[name] = true
		}
	}
	// Build a set of scoped secret names for dedup
	scopedNames := make(map[string]bool, len(scopedSecrets))
	for _, s := range scopedSecrets {
		scopedNames[s.Name] = true
	}
	// Prepend globals that don't collide with scoped secrets and are in the allowed set
	var merged []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	for _, g := range globals {
		if scopedNames[g.Name] {
			continue
		}
		if allowedGlobals != nil && !allowedGlobals[g.Name] {
			continue
		}
		merged = append(merged, struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}{Name: g.Name, Value: g.Value})
	}
	merged = append(merged, scopedSecrets...)
	return merged
}

func (api *StreamingAPI) loadSelectedSecrets(ctx context.Context, userID, workflowPath string, selectedNames []string) []struct {
	Name  string `json:"name"`
	Value string `json:"value"`
} {
	if userID == "" || len(selectedNames) == 0 {
		return nil
	}

	selectedSet := make(map[string]bool, len(selectedNames))
	for _, name := range selectedNames {
		selectedSet[name] = true
	}

	// Track which selected names were actually resolved so we can surface orphans.
	// An orphan is a name attached to the workflow with no value in the workflow
	// store and no matching GLOBAL_SECRET_* env var — runtime would silently set
	// $SECRET_<NAME> to empty, masking downstream failures.
	resolved := make(map[string]bool, len(selectedNames))

	resultByName := make(map[string]string, len(selectedNames))
	var resultOrder []string
	addResult := func(name, value string) {
		if _, exists := resultByName[name]; !exists {
			resultOrder = append(resultOrder, name)
		}
		resultByName[name] = value
	}

	if strings.TrimSpace(workflowPath) != "" {
		// Workflow secrets are shared by everyone with access to the workflow
		// (PLAT-272): they are read from the workflow's own document and bound
		// to the workflow path, so the value is the same whether this run was
		// started by the owner, a read-only user, or the scheduler.
		workflowSecrets, err := api.ensureSharedWorkflowSecrets(ctx, workflowPath, userID)
		if err != nil {
			log.Printf("[SECRETS] Failed to list workflow secrets for %s: %v", workflowPath, err)
		} else {
			for _, s := range workflowSecrets {
				if !selectedSet[s.Name] {
					continue
				}
				plaintext, err := decryptSharedWorkflowSecret(workflowPath, s)
				if err != nil {
					log.Printf("[SECRETS] Failed to decrypt workflow secret %q for workflow %s: %v", s.Name, workflowPath, err)
					continue
				}
				addResult(s.Name, plaintext)
				resolved[s.Name] = true
			}
		}
	}

	// A promoted secret keeps working through the source workflow's existing
	// selected_secrets attachment. Its value now resolves from the global store.
	for _, secret := range getGlobalSecrets() {
		if secret.Managed && selectedSet[secret.Name] && !resolved[secret.Name] {
			addResult(secret.Name, secret.Value)
			resolved[secret.Name] = true
		}
	}

	var result []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	for _, name := range resultOrder {
		result = append(result, struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}{Name: name, Value: resultByName[name]})
	}

	// Also treat globals as resolved — mergeGlobalSecrets layers these in separately.
	for _, gs := range getGlobalSecrets() {
		if selectedSet[gs.Name] {
			resolved[gs.Name] = true
		}
	}

	var orphans []string
	for _, name := range selectedNames {
		if !resolved[name] {
			orphans = append(orphans, name)
		}
	}
	if len(orphans) > 0 {
		log.Printf("[SECRETS] ⚠️  Workflow attaches secret name(s) with no stored value for user %s workflow %s: %v — $SECRET_<NAME> will be EMPTY at runtime. Store a value with set_workflow_secret or detach via update_workflow_config(remove_secrets=[...]).", userID, workflowPath, orphans)
	}

	return result
}

// CORS middleware
func (api *StreamingAPI) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		originAllowed := origin != "" && isAllowedCORSOrigin(origin, api.config.CORSOrigins)
		if originAllowed {
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, X-User-ID, X-Session-ID, Idempotency-Key, X-Conversation-Continuation, X-Queued-Chat-Delivery, X-Client-Submitted-At")
		w.Header().Set("Access-Control-Expose-Headers", "Server-Timing")
		if originAllowed {
			w.Header().Set("Timing-Allow-Origin", origin)
		}

		if r.Method == "OPTIONS" {
			if origin != "" && !originAllowed {
				http.Error(w, `{"error": "CORS origin not allowed"}`, http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func isAllowedCORSOrigin(origin string, allowedOrigins []string) bool {
	for _, allowed := range allowedOrigins {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		if allowed == "loopback" && isLoopbackOrigin(origin) {
			return true
		}
		if allowed == origin {
			return true
		}
	}
	return false
}

func isLoopbackOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

var apiRequestsInFlight int64

type statusCapturingResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
	start  time.Time
}

// stampServerTiming adds time-to-headers as Server-Timing before the headers
// are sent, so the browser shows server time next to network time.
func (w *statusCapturingResponseWriter) stampServerTiming() {
	if w.status == 0 && !w.start.IsZero() && w.Header().Get("Server-Timing") == "" {
		w.Header().Set("Server-Timing", serverTimingValue(time.Since(w.start)))
	}
}

func (w *statusCapturingResponseWriter) WriteHeader(status int) {
	w.stampServerTiming()
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusCapturingResponseWriter) Write(data []byte) (int, error) {
	w.stampServerTiming()
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += n
	return n, err
}

// Hijack forwards to the underlying ResponseWriter when it supports hijacking
// (any real net/http connection does). Required for ANY websocket handler
// reached through apiRequestLogMiddleware: an embedded http.ResponseWriter
// INTERFACE field only promotes the methods that interface declares
// (Header/Write/WriteHeader), not Hijack — so without this,
// gorilla/websocket's Upgrade() type-asserts for http.Hijacker, fails, and
// every upgrade attempt returns "response does not implement http.Hijacker"
// whenever request logging is on, which defaults to true (shouldLogAPIRequests).
// Caught live: this broke /api/voice/stream's very first non-browser test
// client, which (unlike a browser) has no Origin header so it sailed past the
// separate CheckOrigin issue and hit this one instead.
func (w *statusCapturingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying ResponseWriter does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *statusCapturingResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusCapturingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (api *StreamingAPI) apiRequestLogMiddleware(next http.Handler) http.Handler {
	startAPILatencySummary()
	slow := apiSlowRequestThreshold()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !shouldLogAPIRequests() {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		recorder := &statusCapturingResponseWriter{ResponseWriter: w, start: start}
		route := apiRouteTemplate(r)
		stream := isAPIStreamRequest(r, route)
		quiet := isQuietAPIRoute(route)
		traceRequest := shouldTraceAPIRequest(r) && !quiet && !stream
		var inFlight int64
		if traceRequest {
			inFlight = atomic.AddInt64(&apiRequestsInFlight, 1)
			logfWithContext(api.httpRequestLogContext(r), "[API] --> %s %s request_id=%q in_flight=%d", r.Method, requestLogPath(r), requestLogID(r), inFlight)
		}

		defer func() {
			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}
			elapsed := time.Since(start)
			if stream {
				// Streams are long-lived by design: one line when they close,
				// never counted in the latency percentiles.
				logfWithContext(api.httpRequestLogContext(r), "[API] stream closed %s status=%d bytes=%d duration=%s",
					route, status, recorder.bytes, elapsed.Round(time.Millisecond))
				return
			}
			apiLatency.record(route, status, elapsed)
			if quiet && (status < http.StatusBadRequest || status == http.StatusConflict) {
				return
			}
			if !traceRequest && status < http.StatusBadRequest && elapsed >= slow {
				logfWithContext(api.httpRequestLogContext(r), "[API] slow %s %s request_id=%q status=%d bytes=%d duration=%s",
					r.Method, requestLogPath(r), requestLogID(r), status, recorder.bytes, elapsed.Round(time.Millisecond))
				return
			}
			if traceRequest {
				remaining := atomic.AddInt64(&apiRequestsInFlight, -1)
				logfWithContext(api.httpRequestLogContext(r), "[API] <-- %s %s request_id=%q status=%d bytes=%d duration=%s in_flight=%d",
					r.Method,
					requestLogPath(r),
					requestLogID(r),
					status,
					recorder.bytes,
					time.Since(start).Round(time.Millisecond),
					remaining,
				)
			} else if status >= http.StatusBadRequest {
				// Background reads are quiet when they succeed, but failures remain
				// actionable in the normal server log.
				logfWithContext(api.httpRequestLogContext(r), "[API] <-- %s %s request_id=%q status=%d bytes=%d duration=%s",
					r.Method,
					requestLogPath(r),
					requestLogID(r),
					status,
					recorder.bytes,
					time.Since(start).Round(time.Millisecond),
				)
			}
		}()

		next.ServeHTTP(recorder, r)
	})
}

// shouldTraceAPIRequest keeps normal logs focused on user actions and writes.
// The frontend makes frequent successful GET requests for state refreshes; their
// failures are still logged by apiRequestLogMiddleware. Set
// API_REQUEST_LOG_INCLUDE_GET=true to temporarily trace GET/HEAD too, e.g. to
// measure real polling frequency from server logs instead of the browser
// Network tab — remove this override once the investigation is done.
func shouldTraceAPIRequest(r *http.Request) bool {
	if r.URL.Path == "/api/client-telemetry/chat-delivery" {
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return apiRequestLogIncludeGET()
	default:
		return true
	}
}

func apiRequestLogIncludeGET() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("API_REQUEST_LOG_INCLUDE_GET")))
	return value == "true" || value == "1"
}

func shouldLogAPIRequests() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("API_REQUEST_LOG")))
	return value != "false" && value != "0" && value != "off"
}

func requestLogID(r *http.Request) string {
	if r == nil {
		return "-"
	}
	if requestID := strings.TrimSpace(r.Header.Get("X-Request-ID")); requestID != "" {
		return requestID
	}
	return "-"
}

func requestLogPath(r *http.Request) string {
	if r.URL.RawQuery == "" {
		return r.URL.Path
	}
	query := r.URL.Query()
	for _, key := range []string{"token", "access_token", "auth_token"} {
		if query.Has(key) {
			query.Set(key, "[REDACTED]")
		}
	}
	return r.URL.Path + "?" + query.Encode()
}

// Query endpoint - handles POST requests to start agent streaming.
//
// TWO PRODUCT MODES share this one handler (see the isWorkflowPhase block
// below for the full explanation):
//
//  1. Multiagent chat  — agent_mode="multi-agent": the normal conversational
//     agent with delegation/sub-agents.
//  2. Workflow builder — agent_mode="workflow_phase" + phase_id: a chat whose
//     job is to construct/edit a workflow.json. It is NOT a separate engine —
//     it loads phase-specific config (prompt/tools/folder/LLM) from
//     workflow.json and then sets req.AgentMode="multi-agent", falling through
//     this same path. The two modes therefore diverge only via the ~30
//     `isWorkflowPhase` / workflowPhase* checks scattered below, not via
//     separate code paths.
//
// (A third, deprecated agent_mode="workflow" headless-run path is handled
// separately right after the workflow_phase block.)
func (api *StreamingAPI) handleQuery(w http.ResponseWriter, r *http.Request) {
	requestReceivedAt := time.Now()
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Parse request body first
	var req QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorMsg := fmt.Sprintf("Invalid request body: %v", err)
		http.Error(w, errorMsg, http.StatusBadRequest)
		return
	}
	// External Builder authority is carried only by authenticated dispatch/queue
	// context. JSON cannot supply it, including when reusing a browser chat.
	if claims := GetUserFromContext(r.Context()); claims != nil {
		req.ExternalBuilderOperationID = claims.ExternalBuilderOperationID
	}
	if req.ExternalBuilderOperationID != "" {
		claims, err := api.validateExternalBuilderTurn(r.Context(), req.ExternalBuilderOperationID, r.Header.Get("X-Session-ID"), req.SelectedFolder)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
	}
	principalContext, principalErr := api.revalidateExecutionPrincipal(r.Context(), req)
	if principalErr != nil {
		http.Error(w, principalErr.Error(), http.StatusForbidden)
		return
	}
	r = r.WithContext(principalContext)
	if err := api.bindSlackInvocation(r.Context(), &req, r.Header.Get("X-Session-ID")); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	api.clearBotOriginForOwnerTurn(r.Header.Get("X-Session-ID"), GetUserFromContext(r.Context()), req)
	if target, ok := r.Context().Value(resolvedResumeTargetContextKey{}).(*resolvedResumeTarget); ok {
		req.resolvedResumeTarget = target
	}
	if err := validateRequestedCDPPorts(req); err != nil {
		http.Error(w, "Invalid CDP configuration: "+err.Error(), http.StatusBadRequest)
		return
	}

	// A workflow may keep durable read-only links in workflow.json. Merge those
	// with one-message # references before the common authorization boundary so
	// both forms receive identical access checks and folder guards.
	if contextErr := admitWorkflowBuilderContextPaths(r.Context(), &req); contextErr != nil {
		http.Error(w, contextErr.Error(), http.StatusForbidden)
		return
	}

	// Handle alias: Map Message to Query if Query is empty
	if req.Query == "" && req.Message != "" {
		req.Query = req.Message
	}

	// Validate required fields
	if req.Query == "" {
		errorMsg := "Query is required"
		http.Error(w, errorMsg, http.StatusBadRequest)
		return
	}

	// Record start time for duration calculation
	startTime := time.Now()

	// Generate query ID
	queryID := fmt.Sprintf("query_%d", time.Now().UnixNano())

	// Initialize Langfuse tracing - single trace for entire conversation
	// Read tracing provider from environment variable, default to "noop"
	tracingProvider := os.Getenv("TRACING_PROVIDER")
	if tracingProvider == "" {
		tracingProvider = "noop"
	}
	tracer := observability.GetTracer(tracingProvider)
	traceName := fmt.Sprintf("agent-conversation: %s", r.Header.Get("X-Session-ID"))
	if traceName == "agent-conversation: " {
		traceName = fmt.Sprintf("agent-conversation: %s", queryID)
	}
	traceUsername := ""
	if traceUser := GetUserFromContext(r.Context()); traceUser != nil {
		traceUsername = traceUser.Username
	}
	traceID := tracer.StartTrace(traceName, map[string]interface{}{
		"method":     r.Method,
		"url":        r.URL.String(),
		"user_agent": r.Header.Get("User-Agent"),
		"session_id": r.Header.Get("X-Session-ID"),
		"username":   logContextValue(traceUsername),
		"workflow":   logContextValue(workflowLogName(req.SelectedFolder)),
		"query":      req.Query,
		"query_id":   queryID,
	})

	// NOTE: For workflow mode, LLM selection follows priority: temp override → step config → preset LLM
	// No orchestrator default fallback. req.Provider and req.ModelID are not used for workflow agents.
	// For non-workflow agents (multi-agent / chat mode), req.Provider and req.ModelID come from the
	// frontend request. Environment variable fallbacks have been removed — frontend always sends these.

	// Normalize legacy "simple" → "multi-agent" at the request boundary so
	// every downstream branch sees the canonical name.
	req.AgentMode = normalizeAgentMode(req.AgentMode)

	// Extract sessionID from header/cookie or fallback to queryID
	sessionID := r.Header.Get("X-Session-ID")
	if sessionID == "" {
		sessionID = queryID // fallback: use queryID as sessionID if not provided
	} else if !api.canUseSessionIDForQuery(r, sessionID) {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Get current user ID for session isolation
	currentUserID := GetUserIDFromContext(r.Context())
	// A Code chat belongs to its pinned person: nothing else (a live-input
	// delivery below, a queued turn) may run in it as anyone else.
	if refusal := codeSessionTurnRefusal(sessionID, GetUserFromContext(r.Context())); refusal != "" {
		http.Error(w, refusal, http.StatusForbidden)
		return
	}
	queryLogCtx := requestLogContext(r.Context(), req, sessionID)
	registerServerLogContext(queryLogCtx, sessionID, queryID)
	queryLogger := queryLogCtx.Logger(api.logger)
	logfWithContext(queryLogCtx, "[LATENCY_DEBUG] T+0ms | Request received | query_preview=%q", truncateForLog(req.Query, 80))
	logfWithContext(queryLogCtx, "[USER_ID_DEBUGGING] HTTP handler: currentUserID=%q (from auth context)", currentUserID)

	// PLAT-262: resolved fresh from this request's own live auth claims, not
	// cached anywhere — a demotion takes effect on the caller's very next
	// request. Gates mutating tool registration and workflow-folder write
	// access below; never gates WorkshopMode (that axis stays prompt-only
	// focus, per docs/design/agent_tool_surface_single_source.md).
	currentUserIsReadOnly := workflowAccessForClaims(GetUserFromContext(r.Context())) == WorkflowAccessRead

	resolvedProfile, access, admitErr := api.admitQueryTarget(r.Context(), &req, currentUserID, sessionID)
	if admitErr != nil {
		status := http.StatusForbidden
		if admitErr.invalidProfile {
			status = http.StatusBadRequest
		}
		http.Error(w, admitErr.Error(), status)
		return
	}
	currentUserIsReadOnly = readOnlyForRequest(access, req)
	// Provider accounts: every turn re-checks that this principal may use the
	// account the turn names here, before any retained CLI gets the message.
	// A denied account fails the turn; it never falls back to another one.
	accountScope := queryProviderAccountScope(req, currentUserID, resolvedProfile, false, "")
	// The FINAL account of the turn: a workflow chat that does not override
	// the manifest runs on the workflow's saved model and account.
	turnProvider, turnConnectionID := api.finalQueryTurnConnection(r.Context(), req, sessionID)
	if turnProvider != "" {
		admitID := turnConnectionID
		if admitID == "" {
			// No account named: the turn runs on the server account. Checked
			// here too, so a retained CLI relaunch or live input is covered.
			admitID = serverDefaultConnectionID(turnProvider)
		}
		if _, accountErr := api.admitProviderAccount(r.Context(), accountScope, turnProvider, admitID); accountErr != nil {
			http.Error(w, accountErr.Error(), http.StatusForbidden)
			return
		}
	}
	crewGuest := crewGuestCallerForTurn(req, currentUserID)
	if crewGuest != "" {
		currentUserIsReadOnly = true
	}
	normalizeWorkflowConversationMode(&req, currentUserIsReadOnly)
	if api.eventStore != nil {
		class := sessionPersistenceClassForRequest(req)
		if err := api.eventStore.SetSessionPersistenceClass(sessionID, class); err != nil {
			logfWithContext(queryLogCtx, "[EVENT_JOURNAL] Session classification rejected: %v", err)
			http.Error(w, "Session persistence classification conflict", http.StatusConflict)
			return
		}
	}

	// Do not let a browser follow-up reconfigure or steer an MCP operation.
	// Its own claims are persisted by the queue and restored on its next turn.
	if api.externalBuilderOwnsSession(sessionID) && !conversationTurnQueueExecution(r.Context()) {
		// Preserve browser retry idempotency before the early queue return.
		// Enqueue even if the MCP turn finishes during journal admission;
		// kickConversationTurnQueue will then execute the idle chat's turn.
		var finish func()
		var accepted bool
		w, r, finish, accepted = api.beginChatSubmission(w, r, sessionID, req.SelectedFolder, req.Query)
		if !accepted {
			return
		}
		defer finish()
		api.queueConversationTurnResponse(w, r, currentUserID, sessionID, req)
		return
	}

	var resolvedProfileSkills []*llmtypes.Skill
	if resolvedProfile != nil {
		identitySkillNames := skills.WithAgentBrowserCapability(req.SelectedSkills, buildChatBrowserConfig(req).HasAgentBrowser)
		resolvedProfileSkills = skills.LoadAttachableIn(getWorkspaceAPIURL(), req.SelectedFolder, identitySkillNames)
	}
	api.conversationMux.Lock()
	if api.lastAgentProfileKeyBySession == nil {
		api.lastAgentProfileKeyBySession = make(map[string]string)
	}
	api.lastAgentProfileKeyBySession[sessionID] = agentProfileSessionKey(resolvedProfile)
	if api.lastAgentToolsModeBySession == nil {
		api.lastAgentToolsModeBySession = make(map[string]string)
	}
	agentToolsMode := agentProfileToolsMode(resolvedProfile)
	// A workflow's Builder/Run chat takes the workflow's "Native agent tools"
	// switch (a Crew's comes through its resolved profile).
	workflowNativeAgentTools := resolvedProfile == nil && api.workflowChatNativeAgentTools(r.Context(), req, sessionID, currentUserIsReadOnly)
	if workflowNativeAgentTools {
		agentToolsMode = "hybrid"
	}
	api.lastAgentToolsModeBySession[sessionID] = agentToolsMode
	api.conversationMux.Unlock()
	if api.internalAgentToolsModeDecided != nil && api.internalAgentToolsModeDecided(sessionID, agentToolsMode) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Scheduled/Chief requests may already carry the configured secret name at
	// this point. Resolve it for backend delivery and strip it from agent env.
	api.resolveNotificationSecretForRequest(r.Context(), currentUserID, req.SelectedFolder, &req)
	// Browser names supplied by an agent (including the conventional "default")
	// are public aliases. Bind them to the durable conversation workspace:
	// workflows share one browser across their authorized users, while Crew
	// projects retain one browser per authenticated owner and project. Other
	// product chats keep the existing user boundary; guests remain chat-scoped.
	selected := strings.Trim(strings.TrimSpace(req.SelectedFolder), "/")
	if selected == "" && strings.TrimSpace(req.PresetQueryID) != "" {
		if resolved, resolveErr := api.resolveWorkspacePathFromPreset(r.Context(), req.PresetQueryID); resolveErr == nil {
			selected = strings.Trim(strings.TrimSpace(resolved), "/")
		}
	}
	// Workflow phase payloads identify the workspace through their preset.
	// Resolve it before both journal acceptance and retained-policy admission.
	if req.AgentMode == "workflow_phase" && strings.HasPrefix(selected, "Workflow/") {
		req.SelectedFolder = selected
	}
	bindConversationBrowserIsolation(sessionID, currentUserID, selected, resolvedProfile)
	common.SetSessionBrowserMode(sessionID, getBrowserMode(req))
	// Keep configured browser intent on the request. In auto mode,
	// agent_browser queries current CDP reachability at tool-call time.

	// Default maxTurns only when omitted (0). Negative values are preserved to mean "no turn cap".
	// Multi-agent chat and the workflow builder run uncapped by default.
	isWorkflowBuilderPhase := req.AgentMode == "workflow_phase" && req.PhaseID == workflowtypes.WorkflowStatusWorkflowBuilder
	if req.MaxTurns == 0 {
		if isToolBackedChatMode(req.AgentMode) || isWorkflowBuilderPhase {
			req.MaxTurns = -1
			log.Printf("[AGENT] MaxTurns omitted for %s mode, running without a turn cap", req.AgentMode)
		} else {
			req.MaxTurns = orchestrator.GetDefaultMaxTurnsFromEnv()
			log.Printf("[AGENT] MaxTurns not provided or 0, defaulting to %d (from env or 500)", req.MaxTurns)
		}
	}

	// Use enabled_servers if provided, otherwise fall back to servers
	selectedServers := req.EnabledServers
	if len(selectedServers) == 0 {
		selectedServers = req.Servers
	}

	// Default to NO_SERVERS if none specified (user didn't select any MCP servers)
	// This ensures the orchestrator and all sub-agents correctly get "no servers"
	// instead of an empty slice which would be treated as "all servers" downstream
	if len(selectedServers) == 0 {
		selectedServers = []string{mcpclient.NoServers}
	}

	var serverList string
	// Check for explicit "NO_SERVERS" request (pure LLM mode, no tools)
	if len(selectedServers) == 1 && selectedServers[0] == mcpclient.NoServers {
		// Keep NoServers constant as-is - this will be handled by integration code
		serverList = mcpclient.NoServers
	} else {
		// Convert server array to comma-separated string for agent compatibility
		serverList = strings.Join(selectedServers, ",")
	}

	if !req.IsAutoNotification && !isScheduledSessionIdentity(sessionID, req.TriggeredBy) {
		var finishSubmission func()
		var accepted bool
		w, r, finishSubmission, accepted = api.beginChatSubmission(w, r, sessionID, req.SelectedFolder, req.Query)
		if !accepted {
			return
		}
		defer finishSubmission()
	}
	preferRetainedDelivery := !api.externalBuilderOwnsSession(sessionID) && shouldTryRetainedDeliveryBeforeQueue(r.Context(), req, sessionID)
	if !preferRetainedDelivery && api.queueOccupiedConversationTurn(w, r, currentUserID, sessionID, req) {
		return
	}

	// SINGLE-ENTRY ROUTING (tmux-transport coding-agent input): the frontend no
	// longer decides live-input-vs-new-turn from terminal liveness — it always
	// POSTs /api/query and the backend is the single source of truth. If this
	// session has a retained live-input-capable coding-agent object, deliver the
	// message to that CLI and return WITHOUT starting a second streaming turn.
	// Otherwise fall through to the normal setup + new-turn + terminal-materialize
	// path below. Auto-notifications keep their synthetic-turn semantics and are
	// never short-circuited. Scoped to live-input-capable coding agents so API/LLM
	// chat is unchanged.
	retainedProfileCompatible := api.agentProfileAllowsRetainedLiveInput(currentUserID, sessionID, req.SelectedFolder, resolvedProfile != nil)
	// A changed Crew/product definition makes the retained CLI stale (it is
	// interrupted below and relaunched), but it is NOT a reason to drop the
	// native conversation: the relaunch resumes the same native session with
	// the current definition. Whether a definition change needs a fresh
	// session (agent-tools mode, a CLI that cannot reload instructions) is
	// decided per runtime by seedCodingAgentRuntimeFromRestoredConversation.
	productDefinitionRefreshed := resolvedProfile != nil && !retainedProfileCompatible
	if productDefinitionRefreshed {
		log.Printf("[CHAT_HISTORY] Product definition changed for session %s; relaunching the coding CLI and resuming its native session where supported", sessionID)
	}
	if !retainedProfileCompatible && !req.DisableLiveInputDelivery && !req.IsAutoNotification && !requestLLMConfigOverridesManifest(req) {
		api.interruptWorkflowPolicySession(sessionID, req.Provider)
	}
	// Automated schedule and webhook turns must preserve turn boundaries. They
	// queue on the session input lane instead of steering a person's currently
	// running Crew reply through retained CLI live input.
	//
	// Product chats are eligible for retained delivery like any other chat.
	// LLMConfigSource=agent-profile only records that the profile owns the
	// request's model binding (see requestLLMConfigOverridesManifest); it is
	// not evidence that the retained CLI is stale. Definition staleness is
	// decided by retainedProfileCompatible + prepareWorkflowRetainedDelivery,
	// and a runtime rebind closes the retained CLI before this handler runs,
	// so a stale target surfaces as "no live target" below and starts a
	// normal new turn instead of hanging behind one.
	retainedDeliveryEligible := preferRetainedDelivery && retainedProfileCompatible
	retainedWorkflowCompatible, policyErr := api.prepareWorkflowRetainedDelivery(r.Context(), sessionID, req, retainedDeliveryEligible)
	if policyErr != nil {
		http.Error(w, policyErr.Error(), http.StatusForbidden)
		return
	}
	if retainedWorkflowCompatible && api.tryDeliverQueryAsLiveInput(w, r, sessionID, req.Query, queryID, requestReceivedAt) {
		return
	}

	// A human message gets one attempt at a compatible retained CLI before it
	// becomes a new turn. If no target exists, this persisted queue owns the
	// message; an uncertain send above never falls through and double-dispatches.
	// Claimed queue workers and non-human producers skip retained delivery.
	if preferRetainedDelivery && api.queueOccupiedConversationTurn(w, r, currentUserID, sessionID, req) {
		return
	}

	// This request starts a new turn: its user_message comes from the agent
	// library, so tell the event store which client id that echo carries.
	if clientMessageID := clientMessageIDFromContext(r.Context()); clientMessageID != "" && api.eventStore != nil {
		api.eventStore.ExpectClientUserMessage(sessionID, clientMessageID, req.Query)
	}

	// Only genuinely new turns use the backend lane. Taking this before the
	// retained-live-input check made normal follow-ups wait behind slow CLI
	// startup and caused later messages/notifications to arrive in bursts.
	var releaseInputLane func()
	if shouldSerializeInteractiveQueryInput(req) {
		// User input has priority over a synthetic completion turn. Cancel it
		// before waiting for the shared turn lane; otherwise the user would wait
		// behind the very synthetic turn it is meant to interrupt.
		if !req.IsAutoNotification && req.AgentMode != "workflow_phase" && api.isSyntheticTurn(sessionID) {
			api.agentCancelMux.RLock()
			cancelFn := api.agentCancelFuncs[sessionID]
			api.agentCancelMux.RUnlock()
			if cancelFn != nil {
				log.Printf("[SYNTHETIC_TURN] Canceling synthetic turn for session %s before user turn waits for lane", sessionID)
				cancelFn()
			}
		}
		if !req.IsAutoNotification && !isScheduledSessionIdentity(sessionID, req.TriggeredBy) {
			// An interactive POST must never hang silently behind a running
			// turn: wait briefly, then answer 409 turn_running so the UI can
			// show "waiting" and retry. Nothing was sent or queued, so a
			// retry (fresh Idempotency-Key) cannot duplicate.
			var acquired bool
			releaseInputLane, acquired = api.tryLockSessionInputLane(sessionID, sessionInputLaneInteractiveWait)
			if !acquired {
				logfWithContext(queryLogCtx, "[TURN_RUNNING] Rejected interactive send for session %s: input lane still held", sessionID)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error":      "turn_running",
					"status":     "turn_running",
					"message":    "The previous turn is still running. Nothing was sent; retry this message.",
					"session_id": sessionID,
				})
				return
			}
		} else {
			releaseInputLane = api.lockSessionInputLane(sessionID)
		}
		defer func() {
			if releaseInputLane != nil {
				releaseInputLane()
			}
		}()
	}

	// Builder-chat single-runner constraint: only one workflow-builder chat
	// session per workflow folder may run at a time. Phase executions
	// (cron, bot, manual phase runs) are NOT subject to this — they have
	// their own concurrency handling. The rejection fires only when both
	// the incoming request AND the currently-running execution are
	// workflow-builder chats on the same workspace; same-session sends
	// (follow-up messages on the already-running builder session) pass
	// through. Frontend "+ new chat" pre-checks /api/workflow/running and
	// offers a kill-and-start dialog; this 409 is the backend guard
	// against races.
	if workflowBusyGuardApplies(sessionID, req) {
		if running := api.findRunningTrackedExecutionForWorkspaceWhere(req.SelectedFolder, func(exec *TrackedWorkflowExecution) bool {
			return trackedExecutionBlocksNewWorkflowBuilderChat(exec)
		}); running != nil && running.SessionID != sessionID {
			logfWithContext(queryLogCtx, "[WORKFLOW_BUSY] Rejected workflow_builder chat for workspace %q: running session %s started %s (triggered_by=%s)", req.SelectedFolder, running.SessionID, running.StartedAt.Format(time.RFC3339), running.TriggeredBy)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error":          "workflow_busy",
				"message":        "Workflow builder chat is already running on this workflow. Stop the running chat before starting a new one.",
				"workspace_path": running.WorkspacePath,
				"running": map[string]interface{}{
					"session_id":   running.SessionID,
					"execution_id": running.ExecutionID,
					"triggered_by": running.TriggeredBy,
					"source":       running.Source,
					"started_at":   running.StartedAt,
					"phase_id":     running.PhaseID,
					"phase_name":   running.PhaseName,
					"title":        running.Title,
				},
			})
			return
		}
	}

	// AgentWorks has one interactive root-chat lane per user. Product-profile,
	// bot, and scheduled sessions use separate lanes, but a second ordinary root
	// chat must not race through from another browser/tab.
	claimedAgentWorksChat := false
	if req.AgentMode == "multi-agent" &&
		resolvedProfile == nil &&
		!req.IsAutoNotification &&
		!isScheduledSessionIdentity(sessionID, req.TriggeredBy) &&
		strings.TrimSpace(req.BotPlatform) == "" {
		if blocking := api.claimAgentWorksChatSession(sessionID, currentUserID, req.Query, req.TriggeredBy); blocking != nil {
			logfWithContext(queryLogCtx, "[AGENTWORKS_CHAT_BUSY] Rejected second interactive chat: running session %s", blocking.SessionID)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "agentworks_chat_busy",
				"message": "An AgentWorks chat is already active. Stop it or use New Chat before starting another.",
				"running": map[string]interface{}{
					"session_id":    blocking.SessionID,
					"status":        blocking.Status,
					"last_activity": blocking.LastActivity,
				},
			})
			return
		}
		claimedAgentWorksChat = true
	}

	// Chat sessions are in-memory only — tracked via activeSessions map
	// below. No persistent session metadata.

	// Only a real user message may reactivate a stopped/interrupted session.
	// Cron sequence turns and synthetic auto-notifications are internal work; if
	// they clear these guards, Pulse can continue after the user pressed
	// Stop. startSessionInternal preserves TriggeredBy="cron", so this distinction
	// also applies to backend-driven turns.
	if !req.IsAutoNotification && !strings.EqualFold(strings.TrimSpace(req.TriggeredBy), "cron") {
		api.clearSessionStopped(sessionID)
		// Lift the MCP registry zombie-prevention flag only for that intentional
		// user resume, otherwise internal recovery can resurrect stopped bridges.
		mcpagent.ClearHTTPSessionStopped(sessionID)
	}

	// Track active session for page refresh recovery (no observer needed)
	if !claimedAgentWorksChat {
		api.trackActiveSession(sessionID, req.AgentMode, req.Query, currentUserID, req.BotPlatform, req.TriggeredBy, req.SessionTitle, req.ParentSessionID, req.SessionKind)
	}
	api.activeSessionsMux.Lock()
	if sess, ok := api.activeSessions[sessionID]; ok {
		sess.Username = queryLogCtx.Username
		sess.TurnProvider = ""
		if claims := GetUserFromContext(r.Context()); claims != nil {
			sess.TurnProvider = claims.Provider
		}
		if label := strings.TrimSpace(req.TriggeredByLabel); label != "" {
			sess.TriggeredByLabel = label
		}
		if strings.TrimSpace(req.PresetQueryID) != "" {
			sess.PresetQueryID = strings.TrimSpace(req.PresetQueryID)
		}
		if strings.TrimSpace(req.SelectedFolder) != "" {
			sess.WorkspacePath = strings.TrimSpace(req.SelectedFolder)
		}
		if strings.TrimSpace(req.PhaseID) != "" {
			sess.PhaseID = strings.TrimSpace(req.PhaseID)
		}
		if req.ExecutionOptions != nil && strings.TrimSpace(req.ExecutionOptions.WorkshopMode) != "" {
			sess.WorkshopMode = strings.TrimSpace(req.ExecutionOptions.WorkshopMode)
		}
	}
	api.activeSessionsMux.Unlock()

	// Per-user chat folder. It lives under _users/<userID>/ so different users
	// don't share each other's chat output files.
	perUserChatsFolder := perUserChatsFolderFor(currentUserID)
	api.activeSessionsMux.Lock()
	if sess, ok := api.activeSessions[sessionID]; ok {
		if sess.ChatsFolder == "" {
			sess.ChatsFolder = perUserChatsFolder
		} else {
			perUserChatsFolder = sess.ChatsFolder
		}
	}
	api.activeSessionsMux.Unlock()
	if err := createWorkspaceFolder(context.Background(), perUserChatsFolder); err != nil {
		logfWithContext(queryLogCtx, "[SESSION] Warning: could not pre-create per-user folder %s: %v", perUserChatsFolder, err)
	}

	configuredRequestBrowserMode := getBrowserMode(req)
	enableBrowserAccess := (req.EnableBrowserAccess != nil && *req.EnableBrowserAccess) ||
		configuredRequestBrowserMode == "auto" || configuredRequestBrowserMode == "headless" || configuredRequestBrowserMode == "cdp"
	cdpPort := 0
	if req.CdpPort != nil {
		cdpPort = *req.CdpPort
	}
	logfWithContext(
		queryLogCtx,
		"[QUERY] session=%s enable_browser_access=%v browser_mode=%q cdp_port=%d enabled_servers=%v llm_guidance_len=%d query=%q",
		sessionID,
		enableBrowserAccess,
		getBrowserMode(req),
		cdpPort,
		req.EnabledServers,
		len(req.LLMGuidance),
		req.Query,
	)
	logfWithContext(queryLogCtx, "[LATENCY_DEBUG] T+%dms | Session setup complete | sessionID=%s", time.Since(startTime).Milliseconds(), sessionID)

	// Create fresh agent for this request
	// Use LLM configuration from request if provided, otherwise use request defaults
	var finalProvider string
	var finalModelID string

	if isGlobalLLMConfigLocked() {
		// Locked mode: use server env for API keys; the request's choice only
		// counts if a product profile owns it or the published list names it.
		finalProvider, finalModelID = resolveLockedLLM(req.LLMConfig, req.LLMConfigSource)
		// Everything downstream that reads req.LLMConfig directly (the workflow
		// orchestrator's base config, run metadata) must see the locked answer
		// too, not the request's original choice.
		connectionID := req.ConnectionID
		if req.LLMConfig != nil && req.LLMConfig.Primary.ConnectionID != "" {
			connectionID = req.LLMConfig.Primary.ConnectionID
		}
		req.LLMConfig = &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: finalProvider, ModelID: finalModelID, ConnectionID: connectionID}}
		req.LLMConfigSource = ""
		supported := getSupportedProviders()
		if len(supported) > 0 {
			allowed := make(map[string]bool)
			for _, p := range supported {
				allowed[p] = true
			}
			if !allowed[finalProvider] {
				finalProvider = supported[0]
				finalModelID = llm.GetDefaultModel(llm.Provider(finalProvider))
			}
		}
	} else if req.LLMConfig != nil {
		// Use LLM configuration from frontend (new unified structure)
		finalProvider = req.LLMConfig.Primary.Provider
		finalModelID = req.LLMConfig.Primary.ModelID

		// Fallback to request defaults if LLMConfig is partially empty
		if finalProvider == "" {
			finalProvider = req.Provider
		}
		if finalModelID == "" {
			finalModelID = req.ModelID
		}

	} else {
		// Fall back to request defaults
		finalProvider = req.Provider
		finalModelID = req.ModelID
	}

	// A dedicated single-product server deployment (Video Studio, Dominion,
	// SparkQuill) resolving to the claude-code provider with no configured
	// token must refuse loudly here, before the CLI process is ever spawned.
	// Left unchecked, provider initialization falls back to "the CLI's own
	// saved login": on a fresh HOME that hangs on an unattended interactive
	// login screen nobody can answer; on a HOME shared with a prior `claude
	// login` it silently authenticates as -- and bills -- that account
	// instead. The desktop app (AGENT_PRODUCTS unset) is deliberately exempt:
	// using the operator's own logged-in CLI there is correct, not a bug.
	if claudeCodeTokenMissingForSingleProductDeployment(resolvedProfile, finalProvider) {
		http.Error(w, "No Claude Code token configured for this deployment. Set CLAUDE_CODE_OAUTH_TOKEN before starting the service.", http.StatusBadRequest)
		return
	}

	// Session config isn't persisted anymore — follow-up messages rely on the
	// frontend to pass the provider/model on every request.

	// === Workflow builder vs multiagent chat: the mode discriminator ===
	//
	// `isWorkflowPhase` is the single flag that distinguishes the WORKFLOW
	// BUILDER from plain MULTIAGENT CHAT for the rest of this handler. There is
	// no separate builder code path: the block just below loads the builder's
	// config (LLM, servers, browser mode, secrets, working folder) from the
	// preset's workflow.json, and then sets req.AgentMode="multi-agent" so the
	// request continues down the shared chat path.
	//
	// Consequence: every place the builder must behave differently from chat is
	// an `if isWorkflowPhase` / `if !isWorkflowPhase` check further down (~30 of
	// them — folder guard, LLM resolution, tool registration, secrets injection,
	// session binding, chat-history persistence, etc.). Grep
	// `isWorkflowPhase` to see the full set of divergence points. If you need
	// the builder and chat to diverge more, prefer threading the difference
	// through `workflowPhaseFolder`/config rather than adding yet another bool
	// check here — the scattered checks are already the main source of
	// confusion in this 3k-line function.
	//
	// This runs BEFORE the deprecated agent_mode=="workflow" orchestrator branch
	// to intercept and redirect.
	//
	// req.AgentMode is one of: "workflow_phase" (Workflow Builder chat, this
	// branch), the deprecated "workflow" (see below), or "multi-agent" — the
	// generic direct-chat mode (model + skills + tools + workspace context)
	// that the base agentworks surface uses by default and that other product
	// surfaces (Video Studio, Dominion) opt into explicitly for plain chat.
	// "multi-agent" is NOT a "Chief of Staff" product — no such product exists
	// on main (only on the separate, unmerged feature/chief-of-staff-product
	// branch) — see frontend/src/utils/agentModeDescriptions.ts.
	isWorkflowPhase := req.AgentMode == "workflow_phase"
	workflowPhaseID := req.PhaseID
	workflowPhaseFolder := "" // The preset's SelectedFolder — used to auto-grant write access in FolderGuard
	workflowPhaseRunFolder := ""
	var workflowPhasePrimaryOptions map[string]interface{}
	_ = workflowPhaseFolder // used later in the function
	if isWorkflowPhase {
		logfWithContext(queryLogCtx, "[WORKFLOW_PHASE] Phase chat mode detected: phase=%s preset=%s session=%s", workflowPhaseID, req.PresetQueryID, sessionID)
		if req.PresetQueryID == "" {
			logfWithContext(queryLogCtx, "[WORKFLOW_PHASE] ERROR: workflow_phase mode requires a preset_query_id")
			http.Error(w, `{"error":"workflow_phase mode requires a preset_query_id (workflow preset)"}`, http.StatusBadRequest)
			return
		}

		// Try manifest-first resolution for workflow_phase
		// Priority: resolve from preset DB → fallback to req.SelectedFolder (scheduler sets this directly)
		phaseManifestLoaded := false
		resolvedWPath := ""
		if wPath, wErr := api.resolveWorkspacePathFromPreset(context.Background(), req.PresetQueryID); wErr == nil && wPath != "" {
			resolvedWPath = wPath
		} else if req.SelectedFolder != "" {
			// Scheduler/cron sets selected_folder directly — no DB lookup needed
			resolvedWPath = req.SelectedFolder
			logfWithContext(queryLogCtx.WithWorkflow(resolvedWPath), "[WORKFLOW_PHASE] Using selected_folder as workspace path: %s", resolvedWPath)
		}
		var resolvedManifest *WorkflowManifest
		if resolvedWPath != "" {
			if manifest, found, mErr := ReadWorkflowManifest(context.Background(), resolvedWPath); mErr == nil && found {
				resolvedManifest = manifest
			}
		}
		if resolvedWPath != "" {
			queryLogCtx = queryLogCtx.WithWorkflow(resolvedWPath)
			registerServerLogContext(queryLogCtx, sessionID, queryID)
			queryLogger = queryLogCtx.Logger(api.logger)
			api.activeSessionsMux.Lock()
			if sess, ok := api.activeSessions[sessionID]; ok {
				// PLAT-159: WorkflowLabel/PresetName used to get the raw folder
				// name too, same as WorkflowName. That made the same running
				// session look like a different workflow wherever the UI reads
				// the label instead of the folder name (the Global Activity
				// Monitor pill, in particular) — see workflowDisplayLabel.
				workflowName := workflowNameFromWorkspacePath(resolvedWPath)
				displayLabel := workflowDisplayLabel(resolvedWPath, resolvedManifest)
				sess.PresetQueryID = req.PresetQueryID
				sess.WorkspacePath = resolvedWPath
				sess.WorkflowName = workflowName
				sess.WorkflowLabel = displayLabel
				sess.PresetName = displayLabel
				if workflowPhaseID != "" {
					sess.CurrentExecutionName = workflowPhaseID
				}
			}
			api.activeSessionsMux.Unlock()
		}
		if resolvedWPath != "" && resolvedManifest != nil {
			manifest := resolvedManifest
			{
				phaseManifestLoaded = true
				workflowPhaseFolder = resolvedWPath
				logfWithContext(queryLogCtx.WithWorkflow(resolvedWPath), "[WORKFLOW_PHASE] Loaded config from manifest at %s", resolvedWPath)
				if manifest.Capabilities.LLMConfig != nil {
					phaseLLM, _ := workshopResolveLLMConfig(lockedPresetLLMConfig(manifest.Capabilities.LLMConfig))
					if phaseLLM != nil && phaseLLM.Provider != "" && phaseLLM.ModelID != "" {
						if requestLLMConfigOverridesManifest(req) {
							logfWithContext(queryLogCtx.WithWorkflow(resolvedWPath), "[WORKFLOW_PHASE] Preserving request LLM %s/%s from %s over manifest phase LLM %s/%s",
								finalProvider, finalModelID, req.LLMConfigSource, phaseLLM.Provider, phaseLLM.ModelID)
						} else {
							finalProvider = phaseLLM.Provider
							finalModelID = phaseLLM.ModelID
							workflowPhasePrimaryOptions = phaseLLM.Options
							// The manifest selects credentials as well as the model. Clear
							// stale request account IDs when switching back to a server account.
							req.ConnectionID = phaseLLM.ConnectionID
							if req.LLMConfig != nil {
								configCopy := *req.LLMConfig
								configCopy.Primary.ConnectionID = phaseLLM.ConnectionID
								req.LLMConfig = &configCopy
							}
							logfWithContext(queryLogCtx.WithWorkflow(resolvedWPath), "[WORKFLOW_PHASE] Using workshop LLM from manifest: %s/%s", finalProvider, finalModelID)
						}
					}
				}
				// If manifest has explicit selection, use it; otherwise leave nil (= all globals included)
				if req.SelectedGlobalSecrets == nil && manifest.Capabilities.SelectedGlobalSecretNames != nil {
					req.SelectedGlobalSecrets = manifest.Capabilities.SelectedGlobalSecretNames
				}
				// Manifest is the source of truth for workflow-selected secrets too.
				req.DecryptedSecrets = api.loadSelectedSecrets(context.Background(), currentUserID, resolvedWPath, manifest.Capabilities.SelectedSecrets)
				// A bot session already carries its arrival connection, which
				// wins over the manifest selection for that conversation.
				if strings.TrimSpace(req.BotConnectionID) == "" {
					req.BotConnectionID = strings.TrimSpace(manifest.Capabilities.SlackConnectionID)
				}
				if manifest.Capabilities.Notifications != nil {
					req.NotificationSlackWebhookSecretName = strings.TrimSpace(manifest.Capabilities.Notifications.SlackWebhookSecretName)
					req.NotificationRunSummaryInstructions = manifest.Capabilities.Notifications.EffectiveRunSummaryInstructions()
					req.NotificationPulseSummaryInstructions = manifest.Capabilities.Notifications.EffectivePulseSummaryInstructions()
					req.NotificationRunSummaryChannels = append([]string(nil), manifest.Capabilities.Notifications.RunSummaryChannels...)
					req.NotificationPulseSummaryChannels = append([]string(nil), manifest.Capabilities.Notifications.PulseSummaryChannels...)
					req.NotificationExcludeChannels = append([]string(nil), manifest.Capabilities.Notifications.ExcludeChannels...)
					req.NotificationBlockRecipients = append([]string(nil), manifest.Capabilities.Notifications.BlockRecipients...)
					req.NotificationGmailConnectionID = manifest.Capabilities.Notifications.GmailConnectionID
					req.NotificationRunSummaryGmailConnectionIDs = append([]string(nil), manifest.Capabilities.Notifications.RunSummaryGmailConnectionIDs...)
					req.NotificationPulseSummaryGmailConnectionIDs = append([]string(nil), manifest.Capabilities.Notifications.PulseSummaryGmailConnectionIDs...)
					req.NotificationRunSummaryRecipients = append([]string(nil), manifest.Capabilities.Notifications.RunSummaryRecipients...)
					req.NotificationPulseSummaryRecipients = append([]string(nil), manifest.Capabilities.Notifications.PulseSummaryRecipients...)
					req.NotificationRunSummarySlackWebhookSecretNames = append([]string(nil), manifest.Capabilities.Notifications.RunSummarySlackWebhookSecretNames...)
					req.NotificationPulseSummarySlackWebhookSecretNames = append([]string(nil), manifest.Capabilities.Notifications.PulseSummarySlackWebhookSecretNames...)
				}
				api.resolveNotificationSecretForRequest(context.Background(), currentUserID, resolvedWPath, &req)

				// Manifest is the source of truth for servers and browser mode.
				if len(manifest.Capabilities.SelectedServers) > 0 {
					selectedServers = manifest.Capabilities.SelectedServers
					serverList = strings.Join(selectedServers, ",")
				}
				if manifest.Capabilities.BrowserMode != "" {
					req.BrowserMode = manifest.Capabilities.BrowserMode
					req.CdpPorts = append([]int(nil), manifest.Capabilities.CDPPorts...)
				}
			}
		}

		if !phaseManifestLoaded {
			// Manifest-only mode: workflow.json is the source of truth.
			logfWithContext(queryLogCtx, "[WORKFLOW_PHASE] WARNING: No workflow.json found for preset %s - phase will use request defaults only", req.PresetQueryID)
			// Still need to resolve workspace folder for FolderGuard write access
			if workflowPhaseFolder == "" {
				if wPath, wErr := api.resolveWorkspacePathFromPreset(context.Background(), req.PresetQueryID); wErr == nil && wPath != "" {
					workflowPhaseFolder = wPath
				} else if req.SelectedFolder != "" {
					workflowPhaseFolder = req.SelectedFolder
				}
			}
		}
		// Convert to multi-agent mode so it falls through to the standard agent path
		req.AgentMode = "multi-agent"
	}

	// Handle workflow mode - use workflow orchestrator.
	// Deprecated: agent_mode "workflow" is the headless run-without-chat path.
	// Direct plan execution is used by webhooks and workflow-run tools.
	// Interactive authoring uses the separate workflow_phase Builder path.
	if req.AgentMode == "workflow" {
		directWebhookOptions, _ := r.Context().Value(directWebhookExecutionKey{}).(*todo_creation_human.ExecutionOptions)

		// Check if preset_id is provided and workflow is approved (in-memory runtime state)
		if req.PresetQueryID != "" {
			if wfState := getWorkflowRuntime(req.PresetQueryID); wfState != nil {
				log.Printf("[WORKFLOW CHECK] Found workflow runtime: workflowStatus=%s", wfState.WorkflowStatus)
				if wfState.WorkflowStatus == workflowtypes.WorkflowStatusPostVerification {
					log.Printf("[WORKFLOW CHECK] Workflow is approved - proceeding with execution")
				} else {
					log.Printf("[WORKFLOW CHECK] Workflow is not approved yet - proceeding with planning phase")
				}
			} else {
				log.Printf("[WORKFLOW CHECK] No workflow runtime state for preset_id %s - will proceed with defaults", req.PresetQueryID)
			}
		}

		// Create workflow event bridge for event emission
		workflowEventBridge := &eventbridge.WorkflowEventBridge{
			BaseEventBridge: &eventbridge.BaseEventBridge{
				EventStore: api.eventStore,
				SessionID:  sessionID,
				Logger:     queryLogger,
				BridgeName: "workflow",
			},
		}

		// Create custom tools for workflow agents (workspace tools + human tools).
		// Workflow agents can be Simple or ReAct agents, tools are registered based on mode.
		allTools, allExecutors, toolCategories := createCustomTools(true, currentUserID, sessionID) // Workflow mode: session-aware
		api.guardPulseResultExecutor(allExecutors, sessionID)

		// NOTE: Workspace executor replacement with session + secrets happens after secrets are merged (see below).

		// Load selected tools, code execution mode, skills, and preset LLM config from preset if available (for workflow agents)
		var selectedTools []string
		var useCodeExecutionMode bool
		var selectedSkills []string
		var presetLLMConfig *workflowtypes.PresetLLMConfig

		// Try manifest-first resolution: resolve workspace path, then load from workflow.json
		// Priority: req.SelectedFolder (direct) > resolveWorkspacePathFromPreset (preset-based)
		manifestLoaded := false
		manifestWorkspacePath := ""
		if req.SelectedFolder != "" {
			manifestWorkspacePath = req.SelectedFolder
		} else if req.PresetQueryID != "" {
			if wPath, wErr := api.resolveWorkspacePathFromPreset(context.Background(), req.PresetQueryID); wErr == nil && wPath != "" {
				manifestWorkspacePath = wPath
			}
		}
		if manifestWorkspacePath != "" {
			queryLogCtx = queryLogCtx.WithWorkflow(manifestWorkspacePath)
			registerServerLogContext(queryLogCtx, sessionID, queryID)
			queryLogger = queryLogCtx.Logger(api.logger)
			workflowEventBridge.BaseEventBridge.Logger = queryLogger
			caps, found, mErr := LoadManifestForExecution(context.Background(), manifestWorkspacePath)
			if mErr != nil {
				log.Printf("[MANIFEST] Error loading manifest from %s: %v (falling back to defaults)", manifestWorkspacePath, mErr)
			} else if found {
				manifestLoaded = true
				log.Printf("[MANIFEST] Loaded workflow config from manifest at %s", manifestWorkspacePath)
				selectedTools = caps.SelectedTools
				selectedSkills = caps.SelectedSkills
				presetLLMConfig = lockedPresetLLMConfig(caps.LLMConfig)

				if len(caps.SelectedServers) > 0 {
					selectedServers = caps.SelectedServers
					serverList = strings.Join(selectedServers, ",")
				}

				// Global secrets from manifest — if explicit selection, use it; otherwise leave nil (= all globals included)
				if caps.SelectedGlobalSecretNames != nil {
					req.SelectedGlobalSecrets = caps.SelectedGlobalSecretNames
				}
				// User-stored secrets from manifest are authoritative for workflow UI edits.
				req.DecryptedSecrets = api.loadSelectedSecrets(context.Background(), currentUserID, manifestWorkspacePath, caps.SelectedSecrets)
				// A bot session already carries its arrival connection, which
				// wins over the manifest selection for that conversation.
				if strings.TrimSpace(req.BotConnectionID) == "" {
					req.BotConnectionID = strings.TrimSpace(caps.SlackConnectionID)
				}
				if caps.Notifications != nil {
					req.NotificationSlackWebhookSecretName = strings.TrimSpace(caps.Notifications.SlackWebhookSecretName)
					req.NotificationRunSummaryInstructions = caps.Notifications.EffectiveRunSummaryInstructions()
					req.NotificationPulseSummaryInstructions = caps.Notifications.EffectivePulseSummaryInstructions()
					req.NotificationRunSummaryChannels = append([]string(nil), caps.Notifications.RunSummaryChannels...)
					req.NotificationPulseSummaryChannels = append([]string(nil), caps.Notifications.PulseSummaryChannels...)
					req.NotificationExcludeChannels = append([]string(nil), caps.Notifications.ExcludeChannels...)
					req.NotificationBlockRecipients = append([]string(nil), caps.Notifications.BlockRecipients...)
					req.NotificationGmailConnectionID = caps.Notifications.GmailConnectionID
					req.NotificationRunSummaryGmailConnectionIDs = append([]string(nil), caps.Notifications.RunSummaryGmailConnectionIDs...)
					req.NotificationPulseSummaryGmailConnectionIDs = append([]string(nil), caps.Notifications.PulseSummaryGmailConnectionIDs...)
					req.NotificationRunSummaryRecipients = append([]string(nil), caps.Notifications.RunSummaryRecipients...)
					req.NotificationPulseSummaryRecipients = append([]string(nil), caps.Notifications.PulseSummaryRecipients...)
					req.NotificationRunSummarySlackWebhookSecretNames = append([]string(nil), caps.Notifications.RunSummarySlackWebhookSecretNames...)
					req.NotificationPulseSummarySlackWebhookSecretNames = append([]string(nil), caps.Notifications.PulseSummarySlackWebhookSecretNames...)
				}
				api.resolveNotificationSecretForRequest(context.Background(), currentUserID, manifestWorkspacePath, &req)
				req.CdpPorts = append([]int(nil), caps.CDPPorts...)

				// Browser mode from manifest
				if caps.BrowserMode != "" && caps.BrowserMode != "none" && (req.BrowserMode == "" || req.BrowserMode == "none") {
					req.BrowserMode = caps.BrowserMode
				}
			}
		}

		if !manifestLoaded && req.PresetQueryID != "" {
			// Manifest-only mode: workflow.json is the source of truth for workflow config.
			// If no manifest was found, log a warning. The workflow will run with request defaults only.
			log.Printf("[MANIFEST] WARNING: No workflow.json found for preset %s - workflow will run with request defaults only. Run migration: POST /api/workflows/migrate", req.PresetQueryID)
		}

		// --- Post-load processing: browser configuration ---
		// Runs after either manifest or preset loading has populated the config variables.

		// Resolve effective browser mode.
		workflowBrowserMode := req.BrowserMode
		// Store resolved browser mode on session for context-aware shell blocking
		if workflowBrowserMode != "" {
			common.SetSessionBrowserMode(sessionID, workflowBrowserMode)
		}
		if workflowBrowserMode == "auto" || workflowBrowserMode == "headless" || workflowBrowserMode == "cdp" {
			wfCdpPorts := getCdpPorts(req)
			if workflowBrowserMode == "auto" {
				wfCdpPorts = configuredCDPPortsForMode(workflowBrowserMode, req.CdpPort, req.CdpPorts)
			}

			browserCategory := virtualtools.GetWorkspaceBrowserToolCategory()
			browserTools := virtualtools.CreateWorkspaceBrowserTools()
			browserRuntime := browser.NewBrowserRuntimeConfig(workflowBrowserMode, wfCdpPorts)
			browserExecutors := virtualtools.CreateWorkspaceBrowserToolExecutorsWithRuntime(sessionID, browserRuntime)

			allTools = append(allTools, browserTools...)
			for name, executor := range browserExecutors {
				allExecutors[name] = executor
			}
			for _, tool := range browserTools {
				if tool.Function != nil {
					toolCategories[tool.Function.Name] = browserCategory
				}
			}
			log.Printf("[WORKFLOW] Added browser tools (mode=%s, cdp_ports=%v, sessionID=%s)", workflowBrowserMode, wfCdpPorts, sessionID)

			hasAgentBrowserSkill := false
			for _, skill := range selectedSkills {
				if skill == "agent-browser" {
					hasAgentBrowserSkill = true
					break
				}
			}
			if !hasAgentBrowserSkill {
				selectedSkills = append(selectedSkills, "agent-browser")
				log.Printf("[WORKFLOW] Auto-adding agent-browser skill for browser access")
			}

		}

		// Use selected tools from request if preset didn't provide any
		if len(selectedTools) == 0 && len(req.SelectedTools) > 0 {
			selectedTools = req.SelectedTools
			if len(selectedTools) > 0 {
				log.Printf("[TOOLS] Using %d specific tools from request", len(selectedTools))
			} else {
				log.Printf("[TOOLS] Request has empty tool selection - will use ALL tools from selected servers")
			}
		} else if len(selectedTools) == 0 {
			log.Printf("[TOOLS] No tool selection specified - will use ALL tools from selected servers")
		}

		// Workflow execution now always uses code execution mode. Browser access is
		// exposed as a tool/capability and must not disable the get_api_spec/API bridge.
		useCodeExecutionMode = true
		if workflowBrowserMode != "" && workflowBrowserMode != "none" {
			log.Printf("[CODE_EXECUTION] Code execution mode enabled with browser_mode=%s", workflowBrowserMode)
		} else {
			log.Printf("[CODE_EXECUTION] Code execution mode enabled")
		}

		// Inject merged API keys (env + workspace) into LLM config for workflow execution.
		// Without this, workflow agents (todo task orchestrators, sub-agents) won't have
		// provider API keys and authenticated CLI providers will fail.
		workflowLLMConfig := req.LLMConfig
		if workflowLLMConfig == nil {
			workflowLLMConfig = &orchestrator.LLMConfig{}
		}
		workflowKeys, credentialErr := api.resolveEffectiveAPIKeys(r.Context(), currentUserID, manifestWorkspacePath, nil)
		if credentialErr != nil {
			http.Error(w, "Failed to load workflow provider credentials", http.StatusInternalServerError)
			return
		}
		workflowLLMConfig.APIKeys = workflowKeys
		workflowCLISecurityPolicy, policyErr := api.cliSecurityStore.Resolve(
			currentUserID,
			"",
			[]string{codingAgentWorkspaceWorkingDir(manifestWorkspacePath)},
			[]string{codingAgentWorkspaceWorkingDir(manifestWorkspacePath)},
		)
		if policyErr != nil {
			http.Error(w, "CLI security policy cannot be enforced: "+policyErr.Error(), http.StatusConflict)
			return
		}

		// Create workflow orchestrator for this request.
		// Note: req.MaxTurns is already normalized earlier in the handler:
		// 0 => default, negative => uncapped, positive => explicit limit.
		// Note: provider and model parameters removed - LLM selection uses temp override → step config → preset LLM
		workflowOrchestrator, err := orchtypes.NewWorkflowOrchestrator(
			api.mcpConfigPath,    // mcpConfigPath
			api.temperature,      // temperature
			"workflow",           // agentMode
			queryLogger,          // logger
			workflowEventBridge,  // eventBridge
			tracer,               // tracer
			selectedServers,      // selectedServers
			selectedTools,        // NEW: selectedTools
			useCodeExecutionMode, // NEW: code execution mode
			allTools,             // customTools
			allExecutors,         // customToolExecutors
			workflowLLMConfig,    // llmConfig (with merged API keys)
			req.MaxTurns,         // maxTurns (normalized earlier in the handler)
			toolCategories,       // NEW: toolCategories
			presetLLMConfig,      // preset LLM config for agent defaults
		)
		if err != nil {
			log.Printf("[WORKFLOW ERROR] Failed to create workflow orchestrator: %v", err)
			http.Error(w, fmt.Sprintf("Failed to create workflow orchestrator: %v", err), http.StatusInternalServerError)
			return
		}
		workflowOrchestrator.SetCLISecurityPolicy(workflowCLISecurityPolicy)

		// Set selected skills on the orchestrator
		if len(selectedSkills) > 0 {
			workflowOrchestrator.SetSelectedSkills(selectedSkills)
			log.Printf("[SKILLS] Applied %d skills to workflow orchestrator: %v", len(selectedSkills), selectedSkills)
		}

		// Merge global secrets with user-supplied secrets, then set on orchestrator
		allSecrets := mergeGlobalSecrets(req.DecryptedSecrets, req.SelectedGlobalSecrets)
		if len(allSecrets) > 0 {
			entries := make([]orchestrator.SecretEntry, len(allSecrets))
			for i, s := range allSecrets {
				entries[i] = orchestrator.SecretEntry{Name: s.Name, Value: s.Value}
			}
			workflowOrchestrator.SetSecrets(entries)
			log.Printf("[SECRETS] Applied %d secrets (%d global + %d scoped) to workflow orchestrator", len(entries), len(entries)-len(req.DecryptedSecrets), len(req.DecryptedSecrets))
		}

		// Replace workspace advanced executors with session-aware versions that include secrets.
		// This must happen AFTER secrets are merged so secrets are available as shell env vars.
		// createCustomTools uses the no-session CreateWorkspaceAdvancedToolExecutors(),
		// which means MCP_SESSION_ID won't be set and secrets won't be in the shell env.
		secretEnvVars := make(map[string]string, len(allSecrets))
		for _, s := range allSecrets {
			secretEnvVars["SECRET_"+s.Name] = s.Value
		}
		sessionAwareExecutors, workspaceEnv := virtualtools.CreateWorkspaceAdvancedToolExecutorsWithSessionAndEnv(currentUserID, sessionID, secretEnvVars)
		api.setPlaywrightExecutionContext(workspaceEnv, sessionID, req)
		virtualtools.SetGenerateTextWorkflowTierConfig(sessionAwareExecutors, getWorkspaceAPIURL(), generateTextWorkflowTiers(presetLLMConfig), workflowKeys)
		if mode := validatedWorkflowExecutionMode(req.ExecutionOptions); mode != "" {
			workspaceEnv["WORKFLOW_EXECUTION_MODE"] = mode
		}
		for name, executor := range sessionAwareExecutors {
			allExecutors[name] = executor
		}
		log.Printf("[WORKFLOW] Replaced workspace executors with session-aware versions (userID=%q, sessionID=%q, secrets=%d)", currentUserID, sessionID, len(secretEnvVars))

		// Store workspace env map reference on orchestrator so that when the MCP session ID
		// changes (e.g., per-group in batch execution), MCP_API_URL in the env is updated
		// automatically. This prevents session registry misses that cause new browser instances.
		workflowOrchestrator.SetWorkspaceEnvRef(workspaceEnv)
		log.Printf("[WORKFLOW] Stored workspace env ref on orchestrator (MCP_API_URL=%s)", workspaceEnv["MCP_API_URL"])

		// Store workflow orchestrator for guidance injection
		api.storeWorkflowOrchestrator(sessionID, workflowOrchestrator)

		// Track HTTP session ID on the orchestrator so MCP sessions can be closed on stop
		workflowOrchestrator.SetHTTPSessionID(sessionID)

		if workflowBrowserMode != "" && workflowBrowserMode != "none" {
			workflowOrchestrator.SetBrowserMode(workflowBrowserMode)
			log.Printf("[WORKFLOW] Set browser mode on orchestrator: %s", workflowBrowserMode)
		}

		// Propagate authorized CDP profiles for browser prompts and execution agents.
		orchestratorCDPPorts := getCdpPorts(req)
		if workflowBrowserMode == "auto" {
			orchestratorCDPPorts = configuredCDPPortsForMode(workflowBrowserMode, req.CdpPort, req.CdpPorts)
		}
		if cdpPorts := orchestratorCDPPorts; len(cdpPorts) > 0 {
			workflowOrchestrator.SetCdpPorts(cdpPorts)
			log.Printf("[WORKFLOW] Set CDP ports on orchestrator: %v (browser_mode=%s)", cdpPorts, req.BrowserMode)
		}

		// Wire up live tool call query for workshop query_step_tools
		workflowOrchestrator.SetToolCallQueryFunc(formatToolCallSummaries(api))

		// Create a cancellable context for workflow execution using background context.
		// This prevents normal HTTP workflow requests from being canceled when the
		// request returns. Workflow runs launched internally from Multi Agent Chat use
		// a synthetic wfrun_* request whose context is owned by the background run
		// wrapper, so deriving from r.Context() lets stop_workflow_run/terminate_agent
		// cancel the actual orchestrator context instead of only the wrapper waiter.
		workflowBaseCtx := context.Background()
		if directWebhookOptions != nil || (req.TriggeredBy == "chat_tool" && strings.HasPrefix(sessionID, "wfrun_")) {
			workflowBaseCtx = r.Context()
		}
		workflowCtx, workflowCancel := context.WithCancel(workflowBaseCtx)
		workflowCtx = queryLogCtx.Context(workflowCtx)

		// Inject user ID into the workflow context
		workflowCtx = context.WithValue(workflowCtx, common.UserIDKey, currentUserID)
		workflowCtx = costobserver.ContextWithSourcePlatform(workflowCtx, req.BotPlatform)
		// Inject chat session ID so execute_shell_command can look up the session's
		// working directory and folder guard config from the global session map.
		// Without this, execution agents always get workspace root as their shell cwd.
		workflowCtx = context.WithValue(workflowCtx, common.ChatSessionIDKey, sessionID)
		if dest := notificationDestinationFromQuery(req, currentUserID); dest != nil {
			virtualtools.RegisterSessionNotificationDestination(sessionID, dest)
			workflowCtx = context.WithValue(workflowCtx, virtualtools.BotNotificationDestinationKey, dest)
		}

		// Store the cancel function for potential cancellation (keyed by queryID for independent executions)
		api.workflowOrchestratorContextMux.Lock()
		api.workflowOrchestratorContexts[queryID] = workflowCancel
		api.workflowOrchestratorContextMux.Unlock()

		// Track which queryIDs belong to this session (for handleStopSession)
		api.sessionQueryIDMux.Lock()
		api.sessionQueryIDs[sessionID] = append(api.sessionQueryIDs[sessionID], queryID)
		api.sessionQueryIDMux.Unlock()

		// Return immediate response with query ID
		response := QueryResponse{
			QueryID:   queryID,
			SessionID: sessionID, // Include the actual session ID used for conversation history
			Status:    "started",
			Message:   "Query processing started. Use polling API to get real-time updates.",
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

		// Execute workflow asynchronously
		go func() {
			defer func() {
				// Clean up the cancel function when done (keyed by queryID)
				api.workflowOrchestratorContextMux.Lock()
				delete(api.workflowOrchestratorContexts, queryID)
				api.workflowOrchestratorContextMux.Unlock()

				// Remove from active executions registry
				api.activeWorkflowExecutionsMux.Lock()
				delete(api.activeWorkflowExecutions, queryID)
				api.activeWorkflowExecutionsMux.Unlock()
				api.finalizeTrackedExecutionIfRunning(queryID, trackedExecutionStatusCanceled, "workflow execution ended before completion was recorded")

				// Remove queryID from session tracking
				api.sessionQueryIDMux.Lock()
				if queryIDs, exists := api.sessionQueryIDs[sessionID]; exists {
					// Filter out this queryID
					newQueryIDs := make([]string, 0, len(queryIDs)-1)
					for _, qid := range queryIDs {
						if qid != queryID {
							newQueryIDs = append(newQueryIDs, qid)
						}
					}
					if len(newQueryIDs) > 0 {
						api.sessionQueryIDs[sessionID] = newQueryIDs
					} else {
						delete(api.sessionQueryIDs, sessionID)
					}
				}
				api.sessionQueryIDMux.Unlock()
			}()

			if isWorkflowPhase && workflowPhaseFolder != "" && workflowPhaseFolder != "default_workspace" {
				triggeredBy := "workflow_phase"
				if workflowPhaseID == workflowtypes.WorkflowStatusWorkflowBuilder {
					triggeredBy = "workflow_builder"
				}
				// Scheduler turns intentionally use the workflow-builder phase,
				// but they are still cron work. Preserve that origin in the running
				// registry so the frontend restores a Schedule lane rather than
				// treating it as the user's interactive Builder chat.
				if requestedTrigger := strings.TrimSpace(req.TriggeredBy); requestedTrigger != "" {
					triggeredBy = requestedTrigger
				}

				runFolder := ""
				if req.ExecutionOptions != nil {
					runFolder = req.ExecutionOptions.SelectedRunFolder
				}
				api.registerRunningWorkflow(&ActiveWorkflowExecution{
					QueryID:       queryID,
					SessionID:     sessionID,
					PresetQueryID: req.PresetQueryID,
					WorkspacePath: workflowPhaseFolder,
					RunFolder:     runFolder,
					PhaseID:       workflowPhaseID,
					Status:        "running",
					UserID:        currentUserID,
					Title:         req.SessionTitle,
					Query:         req.Query,
					TriggeredBy:   triggeredBy,
					StartedAt:     time.Now(),
				})
			}

			// Check in-memory runtime state for workflow approval status
			workflowStatus := workflowtypes.WorkflowStatusPreVerification // Default status
			var selectedOptions *workflowtypes.WorkflowSelectedOptions
			var stepID string
			if req.PresetQueryID != "" && directWebhookOptions == nil {
				if wfState := getWorkflowRuntime(req.PresetQueryID); wfState != nil {
					workflowStatus = wfState.WorkflowStatus
					selectedOptions = wfState.SelectedOptions
					log.Printf("[WORKFLOW CHECK] Runtime state: workflowStatus=%s", workflowStatus)
				} else {
					log.Printf("[WORKFLOW CHECK] No runtime state for preset_id %s", req.PresetQueryID)
				}

				// Retrieve step_id if it was stored for this preset
				api.workflowStepIDMux.RLock()
				if api.workflowStepIDs != nil {
					if storedStepID, exists := api.workflowStepIDs[req.PresetQueryID]; exists {
						stepID = storedStepID
						log.Printf("[WORKFLOW CHECK] Found step_id for preset: %s", stepID)
						// Clear it after retrieval (one-time use)
						delete(api.workflowStepIDs, req.PresetQueryID)
					}
				}
				api.workflowStepIDMux.RUnlock()
			} else {
				log.Printf("[WORKFLOW CHECK] No preset_query_id provided, using default workflowStatus: %s", workflowStatus)
			}

			// Chat-only phases should not go through the orchestrator path.
			// If the database has these as workflow status, reject early with a clear message.
			if workflowStatus == workflowtypes.WorkflowStatusWorkflowBuilder {
				log.Printf("[WORKFLOW ERROR] Phase %q is chat-only — cannot execute via orchestrator. Use phase chat mode instead.", workflowStatus)
				api.eventStore.AddEvent(sessionID, events.Event{
					ID:        fmt.Sprintf("chat_only_error_%d", time.Now().UnixNano()),
					Type:      "workflow_error",
					Timestamp: time.Now(),
					Data: &unifiedevents.AgentEvent{
						Type:      "workflow_error",
						Timestamp: time.Now(),
						Data: &unifiedevents.GenericEventData{
							Data: map[string]interface{}{
								"error": fmt.Sprintf("%s is a chat-only phase. Please use the phase chat tab instead of the Execute button.", workflowStatus),
							},
						},
					},
				})
				return
			}

			log.Printf("[WORKFLOW EXECUTION] Executing workflow with status: %s", workflowStatus)
			if stepID != "" {
				log.Printf("[WORKFLOW EXECUTION] Step-specific execution for step: %s", stepID)
			}

			// Get the actual objective and workspace path — try SelectedFolder first, then preset
			workflowObjective := req.Query // Default to query if not available
			workflowWorkspacePath := ""
			execManifestResolved := false

			// Resolve workspace path: direct > preset-based
			execWorkspacePath := ""
			if req.SelectedFolder != "" {
				execWorkspacePath = req.SelectedFolder
			} else if req.PresetQueryID != "" {
				if wPath, wErr := api.resolveWorkspacePathFromPreset(context.Background(), req.PresetQueryID); wErr == nil && wPath != "" {
					execWorkspacePath = wPath
				}
			}

			if execWorkspacePath != "" {
				_, found, mErr := ReadWorkflowManifest(context.Background(), execWorkspacePath)
				if mErr == nil && found {
					execManifestResolved = true
					workflowWorkspacePath = execWorkspacePath
					// Objective comes from variables/variables.json, not the manifest
					log.Printf("[WORKFLOW EXECUTION] Using manifest: workspace=%s", execWorkspacePath)
				}
			}
			if !execManifestResolved && execWorkspacePath != "" {
				workflowWorkspacePath = execWorkspacePath
				log.Printf("[MANIFEST] WARNING: No workflow.json found at %s - using request defaults", execWorkspacePath)
			}

			// Fallback: Extract workspace path from objective if not found in preset
			if workflowWorkspacePath == "" {
				workflowWorkspacePath = extractWorkspacePathFromObjective(workflowObjective)
				if workflowWorkspacePath == "" {
					log.Printf("[WORKFLOW ERROR] Workspace path not found in objective for query %s", queryID)
					workflowWorkspacePath = "default_workspace" // fallback
				}
			}

			// Register in active executions registry
			activeExec := &ActiveWorkflowExecution{
				QueryID:       queryID,
				SessionID:     sessionID,
				PresetQueryID: req.PresetQueryID,
				WorkspacePath: workflowWorkspacePath,
				RunFolder:     "iteration-0",
				Status:        "running",
				UserID:        currentUserID,
				Query:         req.Query,
				TriggeredBy:   "manual",
				StartedAt:     time.Now(),
			}
			if req.TriggeredBy != "" {
				activeExec.TriggeredBy = req.TriggeredBy
			}
			if directWebhookOptions != nil {
				activeExec.RunFolder = directWebhookOptions.SelectedRunFolder
			}
			api.registerRunningWorkflow(activeExec)

			// Prepare options for the Execute method
			workflowOptions := map[string]interface{}{
				"workflowStatus":  workflowStatus,  // Current workflow status
				"selectedOptions": selectedOptions, // Pass selected options from database
			}
			if stepID != "" {
				workflowOptions["stepId"] = stepID // Pass step ID for step-specific phase execution
			}

			// Pass execution options from frontend if provided
			log.Printf("[EXECUTION_OPTIONS_DEBUG] [Backend] Received request - req.ExecutionOptions is nil: %v", req.ExecutionOptions == nil)
			if req.ExecutionOptions != nil {
				// Interactive runs rotate iteration-0; webhooks retain their bound folder.
				if directWebhookOptions != nil {
					req.ExecutionOptions.SelectedRunFolder = directWebhookOptions.SelectedRunFolder
				} else {
					req.ExecutionOptions.SelectedRunFolder = "iteration-0"
				}

				log.Printf("[EXECUTION_OPTIONS_DEBUG] [Backend] Execution options received: %+v", req.ExecutionOptions)
				log.Printf("[WORKFLOW EXECUTION] Frontend execution options provided: strategy=%s, run_folder=%s, resume_from_step=%d, enabled_group_names=%v, save_validation_responses=%v",
					req.ExecutionOptions.ExecutionStrategy, req.ExecutionOptions.SelectedRunFolder, req.ExecutionOptions.ResumeFromStep, req.ExecutionOptions.EnabledGroupNames, req.ExecutionOptions.SaveValidationResponses)

				// Convert to controller ExecutionOptions and pass to workflow orchestrator
				controllerOpts := &todo_creation_human.ExecutionOptions{
					SelectedRunFolder: req.ExecutionOptions.SelectedRunFolder,
					ExecutionStrategy: req.ExecutionOptions.ExecutionStrategy,
					ResumeFromStep:    req.ExecutionOptions.ResumeFromStep,
					PlanChangeAction:  req.ExecutionOptions.PlanChangeAction,
					EnabledGroupNames: req.ExecutionOptions.EnabledGroupNames,
					RouteSelections:   req.ExecutionOptions.RouteSelections,
				}

				// Internal webhook bindings cannot be supplied by public request JSON.
				if directWebhookOptions != nil {
					controllerOpts = directWebhookOptions
				}
				// Crew steps invoke Crew triggers through a server-owned runner.
				// The run owner scopes crew lookups; without a user there is no
				// runner and crew steps fail with a clear error at execution.
				controllerOpts.CrewRunner = crewRunnerForRun(r.Context(), api.productSchedules)
				log.Printf("[WORKFLOW EXECUTION] Setting strategy=%s folder=%s", controllerOpts.ExecutionStrategy, controllerOpts.SelectedRunFolder)
				workflowOrchestrator.SetExecutionOptions(controllerOpts)
				log.Printf("[EXECUTION_OPTIONS_DEBUG] [Backend] Execution options set on orchestrator successfully")
			} else {
				log.Printf("[EXECUTION_OPTIONS_DEBUG] [Backend] No execution options provided in request - req.ExecutionOptions is nil")
				// Runs without execution options (Builder chat runs, live
				// input) still need the server-owned crew runner: without
				// it crew steps fail even though the run is fully
				// authenticated. A minimal options value is behavior-neutral
				// for every other consumer — they all nil-guard and match
				// on specific fields.
				if runner := crewRunnerForRun(r.Context(), api.productSchedules); runner != nil {
					workflowOrchestrator.SetExecutionOptions(&todo_creation_human.ExecutionOptions{
						CrewRunner: runner,
					})
				}
			}

			// Set default working directory and folder guard for workflow shell commands
			if workflowWorkspacePath != "" {
				workspace.SetSessionWorkingDir(sessionID, workflowWorkspacePath)
				workspace.SetSessionFolderGuard(sessionID,
					[]string{workflowWorkspacePath},
					[]string{workflowWorkspacePath},
				)
				// Crew attachments resolve through aliases at read time; grant
				// the attached roots read-only for the run. Roots derive from
				// the freshly authorized crew binding, like the run preflight
				// below; without a user there is no authorization and no grant.
				if claims := GetUserFromContext(r.Context()); claims != nil && api.productSchedules != nil {
					common.GrantSessionCrewAttachmentReads(sessionID, crewAttachmentReadRoots(workflowCtx, api.productSchedules, claims.UserID, workflowWorkspacePath))
				}
				if hostDownloads := common.GrantSessionCDPHostDownloadsReadWrite(sessionID, workflowBrowserMode); hostDownloads != "" {
					log.Printf("[WORKFLOW EXECUTION] Added read-write CDP host Downloads: %s", hostDownloads)
				}
			}

			// Update run_metadata.json with LLM config before execution starts
			if req.ExecutionOptions != nil && workflowWorkspacePath != "" {
				runFolder := req.ExecutionOptions.SelectedRunFolder
				if runFolder != "" {
					metaPath := workflowWorkspacePath + "/runs/" + runFolder + "/run_metadata.json"
					if existingMeta, err := readRunMetadata(workflowCtx, metaPath); err == nil && existingMeta != nil {
						models := &RunMetadataModels{}
						if presetLLMConfig != nil {
							models.AllocationMode = presetLLMConfig.Mode
							if presetLLMConfig.BuilderLLM != nil {
								models.BuilderLLM = &RunMetadataLLM{Provider: presetLLMConfig.BuilderLLM.Provider, ModelID: presetLLMConfig.BuilderLLM.ModelID}
							}
							if presetLLMConfig.TieredConfig != nil {
								if presetLLMConfig.TieredConfig.Tier1 != nil {
									models.Tier1 = &RunMetadataLLM{Provider: presetLLMConfig.TieredConfig.Tier1.Provider, ModelID: presetLLMConfig.TieredConfig.Tier1.ModelID}
								}
								if presetLLMConfig.TieredConfig.Tier2 != nil {
									models.Tier2 = &RunMetadataLLM{Provider: presetLLMConfig.TieredConfig.Tier2.Provider, ModelID: presetLLMConfig.TieredConfig.Tier2.ModelID}
								}
								if presetLLMConfig.TieredConfig.Tier3 != nil {
									models.Tier3 = &RunMetadataLLM{Provider: presetLLMConfig.TieredConfig.Tier3.Provider, ModelID: presetLLMConfig.TieredConfig.Tier3.ModelID}
								}
							}
						}
						existingMeta.Models = models
						_ = writeRunMetadata(workflowCtx, metaPath, existingMeta)
					}
				}
			}

			// Execute workflow with the preset objective (not the phase query)
			log.Printf("[WORKFLOW DEBUG] Starting workflow execution for query %s with objective: %s, workspace: %s", queryID, workflowObjective, workflowWorkspacePath)
			var err error
			if claims := GetUserFromContext(r.Context()); claims != nil {
				// Fail fast before any step executes when a crew step lost
				// access to its Crew; shares the execution error reporting below.
				err = preflightCrewSteps(workflowCtx, api.productSchedules, claims.UserID, workflowWorkspacePath)
			}
			if err == nil {
				_, err = workflowOrchestrator.Execute(
					workflowCtx,
					workflowObjective, // Use preset objective instead of req.Query
					workflowWorkspacePath,
					workflowOptions,
				)
			}
			if err != nil {
				// Check if this is a zombie execution: if our queryID is no longer registered
				// for this session, the session was stopped/replaced by a newer execution.
				// Avoid overwriting the newer execution's session status with our stale error.
				api.sessionQueryIDMux.RLock()
				isCurrentExecution := false
				for _, qid := range api.sessionQueryIDs[sessionID] {
					if qid == queryID {
						isCurrentExecution = true
						break
					}
				}
				api.sessionQueryIDMux.RUnlock()

				if !isCurrentExecution {
					log.Printf("[WORKFLOW COMPLETION] Skipping error status update for zombie execution %s (session %s has a newer execution or was intentionally stopped)", queryID, sessionID)
					return
				}

				log.Printf("[WORKFLOW ERROR] Workflow execution failed for query %s: %v", queryID, err)

				// Extract root cause error from the error chain
				rootCauseError := extractRootCauseError(err)
				fullError := err.Error()

				// Emit UnifiedCompletionEvent with root cause error (for UI display)
				errorEventData := unifiedevents.NewUnifiedCompletionEventWithError(
					"workflow",            // agentType
					"workflow",            // agentMode
					workflowObjective,     // question
					rootCauseError,        // root cause error message
					time.Since(startTime), // duration
					0,                     // turns
				)
				agentEvent := unifiedevents.NewAgentEvent(errorEventData)
				agentEvent.SessionID = sessionID
				status := trackedExecutionStatusFailed
				if strings.Contains(fullError, "context canceled") || strings.Contains(fullError, "context deadline exceeded") {
					status = trackedExecutionStatusCanceled
				}
				api.completeTrackedExecution(queryID, status, rootCauseError, nil)
				api.removeSessionQueryID(sessionID, queryID)
				completionEvent := events.Event{
					ID:        fmt.Sprintf("workflow_completion_error_%s_%d", queryID, time.Now().UnixNano()),
					Type:      string(unifiedevents.EventTypeUnifiedCompletion),
					Timestamp: time.Now(),
					Data:      agentEvent,
					SessionID: sessionID,
				}
				api.eventStore.AddEvent(sessionID, completionEvent)
				log.Printf("[WORKFLOW ERROR] Emitted UnifiedCompletionEvent with root cause error for query %s: %s", queryID, rootCauseError)

				// Also send workflow_error event with both root cause and full chain
				errorData := map[string]interface{}{
					"error":       rootCauseError, // Root cause (most important)
					"error_chain": fullError,      // Full error chain for debugging
					"query_id":    queryID,
				}
				api.eventStore.AddEvent(sessionID, events.Event{
					ID:        fmt.Sprintf("workflow_error_%s_%d", queryID, time.Now().UnixNano()),
					Type:      "workflow_error",
					Timestamp: time.Now(),
					Data: &unifiedevents.AgentEvent{
						Type:      "workflow_error",
						Timestamp: time.Now(),
						Data: &unifiedevents.GenericEventData{
							Data: errorData,
						},
					},
					SessionID: sessionID,
				})

				// Update active session status to error
				log.Printf("[WORKFLOW COMPLETION] Updating session %s status to error", sessionID)
				api.updateSessionStatus(sessionID, "error")
				// Clean up HTTP session → MCP session tracker on error completion
				mcpagent.CloseHTTPSession(sessionID)
				// Kill headless browser processes for this session
				api.cleanupBrowserSessions(sessionID)
			} else {
				log.Printf("[WORKFLOW DEBUG] Workflow execution completed for query %s", queryID)
				// Workflow completion events are now handled by the workflow orchestrator itself
				api.completeTrackedExecution(queryID, trackedExecutionStatusCompleted, "", nil)
				api.removeSessionQueryID(sessionID, queryID)

				// Update active session status to completed
				log.Printf("[WORKFLOW COMPLETION] Updating session %s status to completed", sessionID)
				api.updateSessionStatus(sessionID, "completed")
				// Clean up HTTP session → MCP session tracker on successful completion
				mcpagent.CloseHTTPSession(sessionID)
				// Kill headless browser processes for this session
				api.cleanupBrowserSessions(sessionID)
			}
		}()
		return
	}

	// Load preset LLM config for chat/simple mode (for feature toggle fallbacks)
	// Source: workflow.json manifest (no DB dependency)
	var presetLLMConfig *workflowtypes.PresetLLMConfig
	{
		wsPath := req.SelectedFolder
		if wsPath == "" && req.PresetQueryID != "" {
			if p, e := api.resolveWorkspacePathFromPreset(context.Background(), req.PresetQueryID); e == nil && p != "" {
				wsPath = p
			}
		}
		if wsPath != "" {
			if manifest, found, mErr := ReadWorkflowManifest(context.Background(), wsPath); mErr == nil && found && manifest.Capabilities.LLMConfig != nil {
				presetLLMConfig = lockedPresetLLMConfig(manifest.Capabilities.LLMConfig)
			}
		}
	}

	// Every /api/query message is an execution root of its own. The query id is
	// returned to internal sequence callers and is also the parent of any
	// background agents launched by this turn. Do this before writing the
	// response so a fast waiter can never observe an unregistered root.
	req.userID = currentUserID
	api.trackConversationTurnStart(queryID, sessionID, req)

	// Return immediate response with query ID
	response := QueryResponse{
		QueryID:   queryID,
		SessionID: sessionID, // Include the actual session ID used for conversation history
		Status:    "started",
		Message:   "Query processing started. Use polling API to get real-time updates.",
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		api.completeTrackedExecution(queryID, trackedExecutionStatusFailed, fmt.Sprintf("encode query response: %v", err), nil)
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}

	// Don't clear events - let the frontend handle event continuation
	// The deduplication logic in the frontend will handle any duplicates

	// Store the last full query request so server-side follow-up routing can
	// start the next turn from a lightweight live-input message when a retained
	// coding CLI session is idle. Synthetic turns also reuse this context.
	req.userID = currentUserID
	continuationReq := queryRequestForContinuation(req, isWorkflowPhase, workflowPhaseFolder)
	continuationReq = queryRequestWithEffectiveRuntime(continuationReq, finalProvider, finalModelID)
	api.lastQueryMu.Lock()
	api.lastQueryRequests[sessionID] = continuationReq
	api.lastQueryMu.Unlock()

	// Set user-facing busy state for regular chat turns.
	if !isWorkflowPhase {
		api.setSessionBusy(sessionID, true)
		// Mark auto-notification turns as synthetic so frontend doesn't block input
		if req.IsAutoNotification {
			api.setSyntheticTurn(sessionID, true)
		} else {
			api.setSyntheticTurn(sessionID, false)
		}
	}

	// Load merged API keys (env + workspace) while r.Context() is still valid (before goroutine)
	mergedAPIKeys := MergedProviderAPIKeys(r.Context())
	if isWorkflowPhase && workflowPhaseFolder != "" {
		var credentialErr error
		mergedAPIKeys, credentialErr = api.resolveEffectiveAPIKeys(r.Context(), currentUserID, workflowPhaseFolder, mergedAPIKeys)
		if credentialErr != nil {
			logfWithContext(queryLogCtx.WithWorkflow(workflowPhaseFolder), "[WORKFLOW_AUTH] Failed to load provider credentials: %v", credentialErr)
		}
	} else if resolvedProfile != nil && resolvedProfile.APIKeys != nil {
		// resolveAgentProfileForQuery resolved a project-scoped credential for a
		// product surface (e.g. Video Studio) from the encrypted per-workspace
		// store. Recomputing mergedAPIKeys from scratch below would discard it:
		// NewLLMAgentWrapper reads mergedAPIKeys, not req.LLMConfig.
		//
		// The credential is read from the RESOLVER'S RESULT, never from
		// req.LLMConfig. req is deserialized from the client body — `llm_config`
		// is `json:"llm_config,omitempty"` and every ProviderAPIKeys field except
		// ClaudeCodeOAuthToken is JSON-visible — so keying this branch off the
		// request would (a) let any authenticated caller replace the turn's
		// provider keys, including pointing Azure.Endpoint at a host they control,
		// and (b) fire on ordinary chats, because the frontend always sends
		// `api_keys: {}` and an empty JSON object unmarshals to a NON-NIL pointer,
		// wiping every server-resolved key.
		//
		// This branch is deliberately narrower than "any chat with SelectedFolder
		// set": an ordinary multi-agent chat that merely references a workflow
		// folder must not inherit that workflow's credential (see delegation.go's
		// workflowOwnedDelegation check, which enforces the same rule for
		// sub-agents).
		mergedAPIKeys = resolvedProfile.APIKeys
	}

	mergedAPIKeys = api.withConnectionResolver(mergedAPIKeys, queryProviderAccountScope(req, currentUserID, resolvedProfile, isWorkflowPhase, workflowPhaseFolder))
	queryInputLaneRelease := releaseInputLane
	if queryInputLaneRelease != nil {
		// Ownership moves to the background turn for its full lifetime. Normal
		// follow-ups bypass this lane through confirmed provider live input; only a
		// true next turn waits here, preventing overlapping cleanup/state writes.
		releaseInputLane = nil
	}

	// Process the query in the background
	go func() {
		turnStatus := trackedExecutionStatusCanceled
		turnError := "conversation turn ended before completion was recorded"
		var agentCancel context.CancelFunc
		defer func() {
			if agentCancel != nil {
				agentCancel()
				api.agentCancelMux.Lock()
				delete(api.agentCancelFuncs, sessionID)
				api.agentCancelMux.Unlock()
			}
			// Publish completion before releasing the turn lane. A queued synthetic
			// turn can then acquire the lane and set running=true without an older
			// cleanup racing afterward and clearing its state.
			if !isWorkflowPhase {
				api.setSyntheticTurn(sessionID, false)
				api.setSessionBusy(sessionID, false)
			}
			api.observeRuntimeSnapshot(sessionID)
			if queryInputLaneRelease != nil {
				queryInputLaneRelease()
			}
			if !isWorkflowPhase {
				// Drain only after releasing the lane; synthetic turns acquire it.
				api.drainPendingAutoNotificationsAfterTurn(sessionID)
			}
			api.completeTrackedExecution(queryID, turnStatus, turnError, nil)
		}()

		// Helper function to send error and continue (not terminate)
		sendError := func(errorMsg string, shouldTerminate bool) {
			if shouldTerminate {
				turnStatus = trackedExecutionStatusFailed
				turnError = errorMsg
				tracer.EndTrace(traceID, map[string]interface{}{
					"status": "failed",
				})

				// Emit server-level error completion event
				// Create an error completion event using UnifiedCompletionEvent
				errorEventData := unifiedevents.NewUnifiedCompletionEventWithError(
					"server",              // agentType
					req.AgentMode,         // agentMode
					req.Query,             // question
					errorMsg,              // error message
					time.Since(startTime), // duration
					0,                     // turns
				)

				agentEvent := unifiedevents.NewAgentEvent(errorEventData)
				agentEvent.SessionID = sessionID

				serverErrorEvent := events.Event{
					ID:        fmt.Sprintf("server_error_%s_%d", queryID, time.Now().UnixNano()),
					Type:      string(unifiedevents.EventTypeUnifiedCompletion),
					Timestamp: time.Now(),
					Data:      agentEvent,
					SessionID: sessionID,
				}
				api.eventStore.AddEvent(sessionID, serverErrorEvent)
				log.Printf("[SERVER DEBUG] Emitted server error completion event for query %s", queryID)
			}
		}

		// Validate the one model selected for this chat. Product profiles resolve
		// their model server-side; ordinary chat sends its primary model directly.
		providerToValidate := finalProvider
		if providerToValidate == "" {
			providerToValidate = req.Provider
		}
		if !isPublishedLLMProviderAllowed(providerToValidate) {
			sendError(fmt.Sprintf("Selected provider %q is not supported; select an available provider", providerToValidate), true)
			return
		}

		llmProvider, err := llm.ValidateProvider(providerToValidate)
		if err != nil {
			sendError(fmt.Sprintf("Invalid provider: %v", err), true)
			return
		}
		// Validate LLM provider - no need to initialize since agent wrapper handles it
		_ = llmProvider // Use provider variable to avoid unused variable error

		// Create a detached context for the entire streaming operation.
		// Execution is stopped by explicit cancellation, not by a wall-clock timeout.
		streamCtx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
		streamCtx = queryLogCtx.Context(streamCtx)
		// Tools that start their own model (generate_text_llm, read_image)
		// resolve and admit accounts with this turn's scope.
		streamCtx = virtualtools.WithProviderAccountKeys(streamCtx, mergedAPIKeys)
		defer cancel()

		// Load selected tools and code execution mode from preset if available (for simple/ReAct agents)
		var selectedTools []string
		var useCodeExecutionMode bool

		// Load selected tools from manifest (no DB dependency)
		{
			wsPath := req.SelectedFolder
			if wsPath == "" && req.PresetQueryID != "" {
				if p, e := api.resolveWorkspacePathFromPreset(context.Background(), req.PresetQueryID); e == nil && p != "" {
					wsPath = p
				}
			}
			if wsPath != "" {
				if manifest, found, mErr := ReadWorkflowManifest(context.Background(), wsPath); mErr == nil && found {
					selectedTools = manifest.Capabilities.SelectedTools
					if len(selectedTools) > 0 {
						log.Printf("[TOOLS] Loaded %d specific tools from manifest", len(selectedTools))
					}
				}
			}
		}

		// Use selected tools from request if preset didn't provide any
		if len(selectedTools) == 0 && len(req.SelectedTools) > 0 {
			selectedTools = req.SelectedTools
			if len(selectedTools) > 0 {
				log.Printf("[TOOLS] Using %d specific tools from request", len(selectedTools))
			} else {
				log.Printf("[TOOLS] Request has empty tool selection - will use ALL tools from selected servers")
			}
		} else if len(selectedTools) == 0 {
			log.Printf("[TOOLS] No tool selection specified - will use ALL tools from selected servers")
		}

		// Multi-agent chat / generic agent always runs in code-execution mode
		// regardless of provider. Tool-search and simple-agent paths have been
		// retired. Provider-specific CLI handling (CLI prompt template, native
		// context, api-bridge tool mapping) is decided separately via
		// common.IsCLIProvider further down the request lifecycle.
		useCodeExecutionMode = true
		if req.BrowserMode != "" && req.BrowserMode != "none" {
			log.Printf("[CODE_EXECUTION] Code execution mode enabled with browser_mode=%s", req.BrowserMode)
		} else {
			log.Printf("[CODE_EXECUTION] Code execution mode enabled (always on)")
		}

		var resolvedPrimaryOptions map[string]interface{}
		if req.LLMConfig != nil {
			resolvedPrimaryOptions = req.LLMConfig.Primary.Options
		}
		if isWorkflowPhase && workflowPhasePrimaryOptions != nil {
			resolvedPrimaryOptions = workflowPhasePrimaryOptions
		}
		if !isPublishedLLMProviderAllowed(finalProvider) {
			sendError(fmt.Sprintf("Selected provider %q is not supported; select an available provider", finalProvider), true)
			return
		}

		// Create new agent with streamCtx instead of r.Context()
		log.Printf("[AGENT CONFIG DEBUG] Creating agent with ServerName: %s, UseCodeExecutionMode: %v", serverList, useCodeExecutionMode)
		profileAgentToolsMode := ""
		profileApprovalsMode := ""
		// Hybrid enables only native read/search, skills, todos and subagents;
		// shell and file changes still go through the bridge, so mcpagent's
		// shell/diff routing block stays in every mode.
		var profileBridgeRoutingInstructions *string
		if resolvedProfile != nil {
			profileAgentToolsMode = resolvedProfile.Definition.Runtime.AgentTools.Mode
			profileApprovalsMode = resolvedProfile.Definition.Runtime.Approvals.Mode
		} else if workflowNativeAgentTools {
			profileAgentToolsMode = "hybrid"
		}
		allowPersistentInteractive := codingAgentRequestAllowsPersistentInteractive(&req)
		forceStructuredCodingAgent := codingAgentUsesStructuredTransportForChat(finalProvider, allowPersistentInteractive)
		claudeCodePersistentInteractive, codexPersistentInteractive, cursorPersistentInteractive, piPersistentInteractive, musePersistentInteractive, agyPersistentInteractive := codingAgentPersistentInteractiveFlags(finalProvider, allowPersistentInteractive, forceStructuredCodingAgent)
		claudeCodeTransport := codingAgentClaudeCodeChatTransport(finalProvider)
		if forceStructuredCodingAgent {
			// A structured coding CLI is a one-shot native JSON process. There is
			// no persistent pane to retain, and preserving one would accidentally
			// route this product back into tmux behavior.
			claudeCodePersistentInteractive = false
			codexPersistentInteractive = false
			cursorPersistentInteractive = false
			piPersistentInteractive = false
			musePersistentInteractive = false
			agyPersistentInteractive = false
			claudeCodeTransport = ""
		}
		chatWorkingFolder := perUserChatsFolder
		if resolvedProfile != nil {
			chatWorkingFolder = agentProfileRuntimeWorkspace(currentUserID, req.SelectedFolder)
		}
		if isWorkflowPhase && workflowPhaseFolder != "" && workflowPhaseFolder != "default_workspace" {
			chatWorkingFolder = workflowPhaseFolder
		}
		chatWorkingDir := codingAgentWorkspaceWorkingDir(chatWorkingFolder)
		workspace.SetSessionWorkingDir(sessionID, chatWorkingFolder)
		sharedChatWorkingDir := chatWorkingDir
		if isWorkflowPhase {
			var isolationErr error
			chatWorkingDir, isolationErr = workflowCLIWorkingDir(chatWorkingFolder, currentUserID, sessionID, finalProvider, workflowCLIMode(&req, currentUserIsReadOnly))
			if isolationErr != nil {
				sendError(isolationErr.Error(), true)
				return
			}
		}
		cliReadPaths := []string{sharedChatWorkingDir}
		cliWritePaths := []string{sharedChatWorkingDir}
		if chatWorkingDir != sharedChatWorkingDir {
			cliReadPaths = append(cliReadPaths, chatWorkingDir)
			cliWritePaths = append(cliWritePaths, chatWorkingDir)
		}
		if resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID) {
			grantRead, grantWrite, _, _ := workFolderGuardInputs(r.Context())
			cliReadPaths = appendUniqueStrings(cliReadPaths, grantRead...)
			cliWritePaths = appendUniqueStrings(cliWritePaths, grantWrite...)
		}
		cliSecurityPolicy, err := api.cliSecurityStore.Resolve(
			currentUserID,
			finalProvider,
			cliReadPaths,
			cliWritePaths,
		)
		if err != nil {
			logfWithContext(queryLogCtx, "[CLI_SECURITY] Failed to resolve policy: %v", err)
			sendError(fmt.Sprintf("CLI security policy cannot be enforced: %v", err), true)
			return
		}
		if piPersistentInteractive {
			closed := api.cleanupConflictingPiCLIInteractiveSessions(sessionID, chatWorkingDir, "starting chat agent")
			if closed > 0 {
				log.Printf("[PI_CLI_CONFLICT] Cleared %d conflicting Pi CLI session(s) before starting chat session %s in %s", closed, sessionID, chatWorkingDir)
			}
		}
		if req.ExternalBuilderOperationID != "" {
			req.DecryptedSecrets = nil
			noGlobalSecrets := []string{}
			req.SelectedGlobalSecrets = &noGlobalSecrets
		}
		codingAgentSecretEnvironment := make(map[string]string)
		for _, secret := range mergeGlobalSecrets(req.DecryptedSecrets, req.SelectedGlobalSecrets) {
			codingAgentSecretEnvironment["SECRET_"+secret.Name] = secret.Value
		}
		if req.ExternalBuilderOperationID != "" {
			codingAgentSecretEnvironment = map[string]string{}
		}
		nativeShellAPITransport := false
		if resolvedProfile != nil && strings.EqualFold(strings.TrimSpace(resolvedProfile.Definition.Runtime.APITransport.Mode), "native_shell") {
			nativeShellAPITransport = true
			// Product APIs remain session-scoped HTTP endpoints, but this profile
			// deliberately does not expose AgentWorks' execute_shell_command.
			// Give the native coding CLI only the derived bridge routes and its
			// bearer header, so its own Bash tool can call a documented API without
			// inheriting the broad server shell executor.
			nativeAPIEnv := map[string]string{
				"MCP_API_URL":    strings.TrimRight(os.Getenv("MCP_API_URL"), "/") + "/s/" + sessionID,
				"MCP_SESSION_ID": sessionID,
			}
			if strings.TrimSpace(os.Getenv("MCP_API_URL")) != "" && common.BridgeTokensEnabled() {
				common.PopulateMCPBridgeShortEnv(nativeAPIEnv)
				for name, value := range nativeAPIEnv {
					codingAgentSecretEnvironment[name] = value
				}
			} else {
				// Fail closed. This profile has no execute_shell_command and its
				// product tools are only reachable over these routes, so starting
				// the turn without them yields an agent that cannot call anything
				// and cannot tell a missing route from a missing tool. It reports
				// an outage and asks the user to reload, which is exactly the
				// failure this transport was introduced to remove — and it did
				// that while logging this line, which nobody read.
				logfWithContext(queryLogCtx, "[API_TRANSPORT] native_shell requested but MCP bridge environment is incomplete (MCP_API_URL/MCP_API_TOKEN unset); refusing the turn")
				sendError("This product's API transport is not configured on the server: MCP_API_URL and MCP_API_TOKEN must be set for a native_shell profile. No product tool can be called until that is fixed.", true)
				return
			}
		}
		// The product tool gate is the one place a profile decides its tool
		// surface. It is a construction input, so every registration path is
		// covered without each one having to remember to consult a policy.
		// Without tool_policy.mode=allowlist it only observes and logs, which is
		// how a real enabled: list gets seeded from a live session rather than
		// guessed.
		toolGate := newProductToolGate(resolvedProfile)
		relayChat := false
		if isWorkflowPhase && workflowPhaseID == workflowtypes.WorkflowStatusWorkflowBuilder && workflowPhaseFolder != "" {
			// /api/query acknowledges before this worker finishes; the HTTP request
			// context is already canceled here. Match the workflow setup reads above.
			manifest, found, manifestErr := ReadWorkflowManifest(context.Background(), workflowPhaseFolder)
			if manifestErr != nil {
				sendError(fmt.Sprintf("Failed to read workflow manifest: %v", manifestErr), true)
				return
			}
			if found && manifest.Kind == "relay" {
				relayChat = true
				if currentUserIsReadOnly {
					sendError("Relay Builder requires edit access to this Relay.", true)
					return
				}
				relayTools, toolErr := relayproduct.BuilderTools()
				if toolErr != nil {
					sendError(fmt.Sprintf("Failed to load Relay Builder tools: %v", toolErr), true)
					return
				}
				toolGate = newProductToolGateForAllowlist("relays", relayTools)
			}
		}
		if req.ExternalBuilderOperationID != "" {
			claims := GetUserFromContext(r.Context())
			toolGate.DenyWhere(func(name string) bool { return externalBuilderToolDenied(claims, name) })
		}
		if currentUserIsReadOnly && resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID) {
			toolGate.DenyReaderTools(crewReaderDeniedTools()...)
		}
		defer toolGate.logSurface(sessionID)
		platformBridgeTools := []string{}
		if toolGate.Admit("read_image") {
			platformBridgeTools = append(platformBridgeTools, "read_image")
		}

		resolvedConnectionID := req.ConnectionID
		if req.LLMConfig != nil && req.LLMConfig.Primary.ConnectionID != "" {
			resolvedConnectionID = req.LLMConfig.Primary.ConnectionID
		}
		agentConfig := agent.LLMAgentConfig{
			ConnectionID:       resolvedConnectionID,
			Name:               "chat-agent",
			ServerName:         serverList, // Use full server list, not just first one
			ConfigPath:         api.mcpConfigPath,
			AdmitTool:          toolGate.Admit,
			Provider:           llm.Provider(finalProvider),
			ModelID:            finalModelID,
			Options:            resolvedPrimaryOptions,
			Temperature:        req.Temperature,
			MaxTurns:           req.MaxTurns,
			ToolChoice:         "auto",
			StreamingChunkSize: 50,
			Timeout:            0,             // No per-Invoke timeout; streamCtx is explicitly canceled when needed.
			SelectedTools:      selectedTools, // NEW: Pass selected tools
			// read_image remains the platform's workspace-aware image-analysis
			// executor. Expose it directly through every coding-agent bridge so
			// Muse gets the same path as Codex without re-enabling native file or
			// shell tools. Admission and registration remain authoritative: if a
			// product excludes read_image, the bridge cannot advertise it.
			AdditionalBridgeTools: platformBridgeTools,

			// Detailed LLM configuration from frontend (unified fallback structure)

			// Code execution mode: When enabled, only virtual tools are added to LLM
			// MCP tools are accessed through generated scripts using the on-demand HTTP API specification.
			UseCodeExecutionMode:                   useCodeExecutionMode,
			ClaudeCodePersistentInteractiveSession: claudeCodePersistentInteractive,
			CodexPersistentInteractiveSession:      codexPersistentInteractive,
			CursorPersistentInteractiveSession:     cursorPersistentInteractive,
			CursorBridgeToolsMode:                  strings.EqualFold(finalProvider, string(llm.ProviderCursorCLI)),
			CodingAgentToolsMode:                   profileAgentToolsMode,
			CodingAgentApprovalsMode:               profileApprovalsMode,
			BridgeRoutingInstructionsOverride:      profileBridgeRoutingInstructions,
			PiPersistentInteractiveSession:         piPersistentInteractive,
			MusePersistentInteractiveSession:       musePersistentInteractive,
			AgyPersistentInteractiveSession:        agyPersistentInteractive,
			ClaudeCodeTransport:                    claudeCodeTransport,
			ForceStructuredCodingAgent:             forceStructuredCodingAgent,
			CodingAgentWorkingDir:                  chatWorkingDir,
			CLISecurityPolicy:                      cliSecurityPolicy,
			CodingAgentSecretEnvironment:           codingAgentSecretEnvironment,
			// A native_shell profile reaches its product APIs over session-scoped
			// HTTP from the CLI's own shell, so codex's workspace-write sandbox
			// has to allow network. macOS does not actually enforce codex's
			// network_access=false (verified against codex 0.147.0: curl
			// succeeds either way), so this changes nothing locally — Linux
			// enforces it, which is where this would otherwise silently break.
			CodexNetworkAccess: nativeShellAPITransport,
			APIKeys:            mergedAPIKeys,
			// Tool timeout, large-output offloading, and parallel tool execution
			// are set by applySharedLLMAgentTuning below (shared with sub-agent creation).
			// MCP session ID for stateful connection reuse.
			// Use the chat session ID so all agents in the same session share MCP connections
			SessionID: sessionID,
			// MCP OAuth is platform-wide. Keep this empty so mcpagent preserves
			// the token path from the shared merged configuration instead of
			// rewriting it beneath the interactive/scheduled user's ID.
			UserID: "",
		}

		// Legacy manifests may still place built-in tool categories in
		// SelectedServers. Keep those categories for direct tool registration,
		// but never attempt to connect to them as MCP servers.
		selectedServers = runtimeMCPServers(selectedServers)
		// A Code chat: pin its person before anything connects, then add that
		// person's own servers switched on for this Code (never anyone
		// else's), carried as complete configs (docs/design/code_private_mcp.md).
		if resolvedProfile != nil && strings.EqualFold(resolvedProfile.Definition.ID, codeproduct.ProfileID) {
			codeRoot := agentProfileRuntimeWorkspace(currentUserID, req.SelectedFolder)
			// A channel-route turn acts for arbitrary channel members: a
			// Code (DM-only) never pins one to a person.
			if claims := GetUserFromContext(r.Context()); claims != nil && claims.Provider == "bot_route" {
				sendError("Code chats are direct messages only.", true)
				return
			}
			if pinErr := pinCodeSession(sessionID, currentUserID, codeRoot); pinErr != nil {
				if errors.Is(pinErr, errCodeSessionPinnedToAnother) {
					sendError("This Code chat belongs to another person. Open your own chat of this Code.", true)
				} else {
					sendError(fmt.Sprintf("Could not start this Code chat: %v", pinErr), true)
				}
				return
			}
			personalNames, personalOverrides := personalMCPServersForTurn(currentUserID, codeRoot)
			selectedServers = mergeServerLists(selectedServers, personalNames)
			if len(personalOverrides) > 0 {
				if agentConfig.RuntimeOverrides == nil {
					agentConfig.RuntimeOverrides = mcpclient.RuntimeOverrides{}
				}
				for name, override := range personalOverrides {
					agentConfig.RuntimeOverrides[name] = override
				}
			}
		}
		// Apply the external Builder boundary last, including after a Code
		// profile has considered any personal server overrides.
		selectedServers = externalBuilderMCPServers(req, selectedServers)
		if len(selectedServers) == 1 && selectedServers[0] == mcpclient.NoServers {
			serverList = mcpclient.NoServers
		} else {
			serverList = strings.Join(selectedServers, ",")
		}
		agentConfig.ServerName = serverList

		applySharedLLMAgentTuning(&agentConfig, &req, presetLLMConfig)

		// Set agent mode based on request
		agentConfig.AgentMode = mcpagent.SimpleAgent
		log.Printf("[AGENT DEBUG] Creating agent with mode: %s, servers: %s", agentConfig.AgentMode, serverList)
		logfWithContext(queryLogCtx, "[LATENCY_DEBUG] T+%dms | Agent config built, creating agent wrapper | provider=%s model=%s", time.Since(startTime).Milliseconds(), finalProvider, finalModelID)
		agentConfig.ToolExecutionContext = api.bindToolExecutionContext(r.Context(), sessionID, req, currentUserIsReadOnly)
		// Create LLM agent wrapper with trace using streamCtx
		llmAgent, err := agent.NewLLMAgentWrapperWithTrace(streamCtx, agentConfig, tracer, traceID, queryLogger)
		if err != nil {
			logfWithContext(queryLogCtx, "[AGENT DEBUG] Failed to create LLM agent wrapper: %v", err)
			sendError(fmt.Sprintf("Failed to create agent: %v", err), true)
			return
		}
		logfWithContext(queryLogCtx, "[LATENCY_DEBUG] T+%dms | Agent wrapper created", time.Since(startTime).Milliseconds())

		// Prime MCP server configs in the session registry for chat mode.
		// Workflow mode does this inside the orchestrator.
		if api.mcpConfig != nil {
			registry := mcpclient.GetSessionRegistry()
			for _, sName := range selectedServers {
				if sName == mcpclient.NoServers {
					continue
				}
				serverCfg, cfgErr := api.mcpConfig.GetServer(sName)
				if cfgErr != nil {
					continue
				}
				registry.StoreServerConfig(sessionID, sName, serverCfg)
			}
		}

		// Add workspace tools to chat agents (multi-agent chat mode)
		// Workflow mode handles workspace tools differently, so exclude it
		isChatMode := isToolBackedChatMode(req.AgentMode)

		// Resolve all conditional folder-guard grants once for this request.
		// See conditional_grants.go for the registry. The result is reused across
		// every folder guard and system prompt site below.
		resolvedGrants := resolveConditionalGrants(req)
		if req.ExternalBuilderOperationID != "" {
			resolvedGrants = ResolvedGrants{}
		}
		// When skill-creator is selected, ensure it's installed (auto-fetch from GitHub
		// if missing). This is the one piece of grant-specific logic that doesn't fit
		// the registry — it's an install-on-demand side effect unique to skill-creator.
		if resolvedGrants.HasGrant("skill-creator") {
			// The workspace API, not this server. GetAPIURL points at the agent
			// server, so the lookup could never find the skill and every turn
			// re-imported it from GitHub — an unauthenticated API call per
			// message, on its way to being rate limited.
			workspaceAPIURL := getWorkspaceAPIURL()
			_, err := skills.GetSkill(workspaceAPIURL, "skill-creator")
			if err != nil {
				log.Printf("[SKILL CREATOR] skill-creator not found, attempting import from GitHub...")
				_, err := skills.ImportGitHubSkill(workspaceAPIURL, "https://github.com/anthropics/skills/tree/main/skills/skill-creator", "")
				if err != nil {
					log.Printf("[SKILL CREATOR] Warning: Failed to import skill-creator: %v", err)
				} else {
					log.Printf("[SKILL CREATOR] Successfully imported skill-creator")
				}
			}
		}

		var workspaceEnv map[string]string // hoisted so secrets can be injected after allChatSecrets is computed
		log.Printf("[CHAT_TOOLS_DEBUG] isChatMode=%v agentNonNil=%v", isChatMode, llmAgent.GetUnderlyingAgent() != nil)

		// Extract #workflow read-only folders early — needed both inside isChatMode block
		// (for folder guard setup) and in the workflow_phase block (for shell isolator).
		_, workflowReadOnlyFolders := collectSplitFolderGuardFolders(req.Query, req.authorizedWorkflowContextReadPaths)
		if req.ExternalBuilderOperationID != "" {
			workflowReadOnlyFolders = nil
		}

		if isChatMode && llmAgent.GetUnderlyingAgent() != nil {
			// Handle browser access: when enabled, add agent-browser skill
			enableBrowserAccess := false
			if req.EnableBrowserAccess != nil && *req.EnableBrowserAccess {
				enableBrowserAccess = true
				// Auto-add agent-browser skill if not already selected
				hasAgentBrowserSkill := false
				for _, skill := range req.SelectedSkills {
					if skill == "agent-browser" {
						hasAgentBrowserSkill = true
						break
					}
				}
				if !hasAgentBrowserSkill {
					req.SelectedSkills = append(req.SelectedSkills, "agent-browser")
				}
				log.Printf("[BROWSER] Auto-adding agent-browser skill and tool (enable_browser_access: true)")
			}

			// Create Chats/ folder if it doesn't exist
			if err := skills.CreateFolder("Chats"); err != nil {
				log.Printf("[WORKSPACE] Warning: Could not create Chats/ folder: %v", err)
			}

			// Create skills/ folder if it doesn't exist
			if err := skills.CreateFolder("skills"); err != nil {
				log.Printf("[WORKSPACE] Warning: Could not create skills/ folder: %v", err)
			} else {
				// Create skills/custom/ folder for Skill Builder
				if err := skills.CreateFolder("skills/custom"); err != nil {
					log.Printf("[WORKSPACE] Warning: Could not create skills/custom/ folder: %v", err)
				} else {
					log.Printf("[WORKSPACE] Ensured skills/ and skills/custom/ folders exist")
				}
			}

			// Create subagents/ folder if it doesn't exist
			if err := skills.CreateFolder("subagents"); err != nil {
				log.Printf("[WORKSPACE] Warning: Could not create subagents/ folder: %v", err)
			} else {
				if err := skills.CreateFolder("subagents/custom"); err != nil {
					log.Printf("[WORKSPACE] Warning: Could not create subagents/custom/ folder: %v", err)
				}
			}

			// Create memories/ folder if it doesn't exist
			if err := skills.CreateFolder("memories"); err != nil {
				log.Printf("[WORKSPACE] Warning: Could not create memories/ folder: %v", err)
			}

			// Chat mode: LLM-visible workspace tools (advanced + media/provider tools).
			// Basic tools (list/read/write/search) and git tools are not needed — shell is sufficient.
			// These tools are restricted to the current workspace/chat folder guard.
			//
			// Inject the session's secrets as $SECRET_<NAME> shell env so the CHAT
			// agent's own execute_shell_command can access them — not just the
			// step-execution controller. Without this the workflow builder could
			// set_workflow_secret but never read the value back (its own shell had
			// no SECRET_* env), even though attached secrets reach real step runs.
			// req.DecryptedSecrets is loaded from the workflow manifest above
			// (workflow_phase), so it picks up secrets attached in earlier turns;
			// multi-agent chat without loaded secrets yields an empty map (no-op).
			chatAgentSecrets := mergeGlobalSecrets(req.DecryptedSecrets, req.SelectedGlobalSecrets)
			chatAgentSecretEnv := make(map[string]string, len(chatAgentSecrets))
			for _, s := range chatAgentSecrets {
				chatAgentSecretEnv["SECRET_"+s.Name] = s.Value
			}
			if req.ExternalBuilderOperationID != "" {
				chatAgentSecretEnv = map[string]string{}
			}
			workspaceRegistry := virtualtools.CreateWorkspaceToolRegistry(virtualtools.WorkspaceToolRegistryConfig{
				WorkspaceAPIURL:      getWorkspaceAPIURL(),
				UserID:               currentUserID,
				SessionID:            sessionID,
				ExtraEnvVars:         chatAgentSecretEnv,
				GenerateTextLLMTiers: generateTextWorkflowTiers(presetLLMConfig),
				APIKeys:              mergedAPIKeys,
			})
			if len(chatAgentSecretEnv) > 0 {
				logfWithContext(queryLogCtx, "[SECRETS] Injected %d secret(s) into chat agent shell env (isWorkflowPhase=%v)", len(chatAgentSecretEnv), isWorkflowPhase)
			}
			workspaceTools := workspaceRegistry.Tools
			workspaceExecutors := workspaceRegistry.Executors
			workspaceEnv = workspaceRegistry.Env
			// A retained native coding session can keep an older MCP workspace
			// client across turns. Refresh every live client from durable secret
			// storage as each turn starts, so secrets saved before this process or
			// before live mutation support was deployed become usable without a
			// new chat.
			virtualtools.ReplaceSessionShellSecrets(sessionID, chatAgentSecretEnv)
			api.setPlaywrightExecutionContext(workspaceEnv, sessionID, req)
			toolCategories := workspaceRegistry.Categories
			logfWithContext(queryLogCtx, "[USER_ID_DEBUGGING] Main agent workspace executors: created with explicit userID=%q sessionID=%q", currentUserID, sessionID)
			// Inject LLM config fallback for read_image HTTP calls (e.g., from claude CLI subprocess)
			if underlying := llmAgent.GetUnderlyingAgent(); underlying != nil {
				virtualtools.SetReadImageLLMConfig(workspaceExecutors, mcpagent.ReadAgentRuntimeInfo(underlying).LLMConfig)
			}

			// Merge @context file paths into additional folder-guard write access.
			// workflowReadOnlyFolders was computed above.
			fileContextWriteFolders := extractFileContextWriteFolders(req.Query)
			if req.ExternalBuilderOperationID != "" {
				fileContextWriteFolders = nil
			}
			if len(fileContextWriteFolders) > 0 {
				log.Printf("[FILE CONTEXT] Extracted write folder-guard paths from @context: %v", fileContextWriteFolders)
			}
			if len(workflowReadOnlyFolders) > 0 {
				log.Printf("[FILE CONTEXT] Extracted read-only folder-guard paths from #workflow: %v", workflowReadOnlyFolders)
			}

			// Workflow phase: grant write access to the whole workflow folder (prefix match)
			// and block writes to planning/ via the separate blocked-write list. This is
			// "allow everything except planning/" expressed as one prefix + one exception,
			// which is immune to the drift class of bugs that came from enumerating
			// individual writable subfolders (reports/, db/, soul/ previously fell out of
			// sync). planning/ stays read-only because plan.json / step_config.json /
			// workflow_layout.json must go through typed plan-mod tools that serialize
			// full structs, not raw writes.
			var fileContextBlockedWriteFolders []string
			if req.ExternalBuilderOperationID != "" {
				fileContextBlockedWriteFolders = append(fileContextBlockedWriteFolders, workflowPhaseFolder+"/workflow.json")
			}
			if execution, ok := api.botExecutionForSession(sessionID); ok && execution.Request.PresetQueryID != "" {
				fileContextBlockedWriteFolders = append(fileContextBlockedWriteFolders, workflowPhaseFolder+"/runs/iteration-0/")
			}
			// PLAT-262: a read-only identity gets none of the whole-workflow write
			// grant below — effectiveWorkflowPhaseFolderForWrites stays "" for them,
			// which workflowPhaseWriteFolders() below treats as "no workflow write
			// root", leaving only its unconditional Downloads/ + the caller's own
			// chat-history folder writable. Reads are untouched (readPaths below
			// still uses the real workflowPhaseFolder).
			effectiveWorkflowPhaseFolderForWrites := workflowPhaseFolder
			if currentUserIsReadOnly {
				effectiveWorkflowPhaseFolderForWrites = ""
			}
			if isWorkflowPhase && effectiveWorkflowPhaseFolderForWrites != "" {
				fileContextWriteFolders = append(fileContextWriteFolders, effectiveWorkflowPhaseFolderForWrites+"/")
				blockedPlanning := effectiveWorkflowPhaseFolderForWrites + "/" + todo_creation_human.PlanningFolderName + "/"
				fileContextBlockedWriteFolders = append(fileContextBlockedWriteFolders, blockedPlanning)
				log.Printf("[WORKFLOW_PHASE FOLDER GUARD] Write access: %s/ (whole workflow) with blocked-write prefix: %s", effectiveWorkflowPhaseFolderForWrites, blockedPlanning)
				// workflow.json carries the access record and created_by (whom
				// schedules run as): only an owner's session may write it, so an
				// editor cannot have the agent promote them or change run-as.
				if blockedManifest := workflowManifestBlockedWriteForNonOwner(context.WithoutCancel(r.Context()), GetUserFromContext(r.Context()), effectiveWorkflowPhaseFolderForWrites); blockedManifest != "" {
					fileContextBlockedWriteFolders = append(fileContextBlockedWriteFolders, blockedManifest)
					log.Printf("[WORKFLOW_PHASE FOLDER GUARD] Non-owner session: blocked writes to %s", blockedManifest)
				}
			} else if isWorkflowPhase && currentUserIsReadOnly {
				log.Printf("[WORKFLOW_PHASE FOLDER GUARD] Read-only identity — no whole-workflow write grant for session=%s", sessionID)
			}

			// Apply folder guard to restrict writes based on mode.
			// Non-workflow plan/chat sessions write to the per-user Chats folder.
			// Workflow phase writes to the active workflow folder (plus
			// Downloads and chat_history) and keeps Chats read-only so
			// builder artifacts cannot drift into normal chat storage.
			if !isWorkflowPhase {
				// Per-user chat folders replace the legacy global "Chats/" write path.
				perUserChatsWrite := perUserChatsFolder + "/"
				perUserChatHistory := strings.TrimSuffix(perUserChatsFolder, "Chats") + "chat_history/"
				if resolvedProfile != nil && !isGlobalScopedProfile(resolvedProfile) {
					// A project-scoped product profile is bound to one project. Do
					// not inherit the chat-wide grants or @context write expansion.
					// Explicit workflow references remain readable, never writable.
					// A global-scoped profile falls through to the
					// else branch below instead -- it keeps the same chat-wide
					// grants, including the pulse/ write grant, a profile-less turn
					// already has.
					profileRoot := agentProfileRuntimeWorkspace(currentUserID, req.SelectedFolder)
					profileWrite := strings.TrimSuffix(profileRoot, "/") + "/"
					sandbox := resolvedProfile.Definition.Runtime.Sandbox
					// Crew references (# tags and attached Crews, any owner) are
					// shared read-write like the Crew itself; workflow references
					// stay read-only. A Crew Run-mode reader keeps them read-only.
					crewRefWrite, nonCrewReadOnly := splitCrewReferenceFolders(workflowReadOnlyFolders)
					if currentUserIsReadOnly && isProjectProfileID(resolvedProfile.Definition.ID) {
						crewRefWrite, nonCrewReadOnly = nil, workflowReadOnlyFolders
					}
					profileReadOnly := agentProfileReadOnlyFolders(sandbox, nonCrewReadOnly)
					chatHistoryGrants := agentProfileChatHistoryGrants(sandbox, perUserChatHistory)
					workGrantWrite := []string(nil)
					workGrantReadOnly := []string(nil)
					if isProjectProfileID(resolvedProfile.Definition.ID) {
						// Owner-attached external folders: readable always,
						// writable for read_write grants, exposed to tools
						// as WORK_FOLDER_<ALIAS> session env.
						grantRead, grantWrite, grantReadOnly, grantEnv := workFolderGuardInputs(r.Context())
						profileReadOnly = append(profileReadOnly, grantRead...)
						workGrantWrite = grantWrite
						workGrantReadOnly = grantReadOnly
						if len(grantEnv) > 0 {
							common.SetSessionShellEnv(sessionID, grantEnv)
						}
						log.Printf("[WORK FOLDER GUARD] Applied %d attached folder(s) (write=%v)", len(grantRead), grantWrite)
					}
					// Crew Run mode: a reader turn keeps the project root
					// readable but drops it from every write set, with an
					// explicit blocked-write entry (the same shape as crew
					// attachment roots). The caller's own chat-history
					// folder stays writable — conversation persistence,
					// not project mutation — as do the caller's own
					// attached folders. Scoped to the work profile so
					// other products keep today's behavior.
					crewReadOnlyTurn := currentUserIsReadOnly && isProjectProfileID(resolvedProfile.Definition.ID)
					guardWriteRoot := profileRoot
					guardReadOnly := profileReadOnly
					guardWrite := []string{profileWrite}
					guardBlocked := []string(nil)
					if crewReadOnlyTurn {
						guardWriteRoot = ""
						guardReadOnly = append([]string{profileWrite}, profileReadOnly...)
						guardWrite = nil
						guardBlocked = append(guardBlocked, profileWrite)
					}
					executorWrite := append(append(append([]string{}, chatHistoryGrants...), workGrantWrite...), crewRefWrite...)
					workspaceExecutors = wrapExecutorsWithPlanFolderGuard(workspaceExecutors, guardWriteRoot, guardReadOnly, executorWrite...)
					workspace.SetSessionWorkingDir(sessionID, profileRoot)
					if strings.EqualFold(resolvedProfile.Definition.ID, codeproduct.ProfileID) {
						// The chat's person is pinned once; personal MCP servers
						// and secrets follow the pin, never the mutable session
						// owner, and a turn from anyone else is refused.
						if pinErr := pinCodeSession(sessionID, currentUserID, profileRoot); pinErr != nil {
							if errors.Is(pinErr, errCodeSessionPinnedToAnother) {
								sendError("This Code chat belongs to another person. Open your own chat of this Code.", true)
							} else {
								sendError(fmt.Sprintf("Could not start this Code chat: %v", pinErr), true)
							}
							return
						}
						// Google accounts and other Code-private resources key off
						// this mark, which survives shell-config clears.
						common.MarkCodeSession(sessionID, profileRoot)
						// workflow.json (MCP selection, sharing-relevant settings)
						// and product.json change only through the role-checked
						// API and tools, never a file write from a chat.
						guardBlocked = append(guardBlocked,
							filepath.Join(profileRoot, "workflow.json"), filepath.Join(profileRoot, "product.json"))
					}
					workspace.SetSessionFolderGuard(sessionID,
						append(append(append([]string{profileWrite}, chatHistoryGrants...), profileReadOnly...), crewRefWrite...),
						append(append(append(guardWrite, chatHistoryGrants...), workGrantWrite...), crewRefWrite...),
					)
					guardBlocked = append(guardBlocked, workGrantReadOnly...)
					if isProjectProfileID(resolvedProfile.Definition.ID) {
						// A Crew writes only its own database; other Crews' db/
						// stays readable but never writable.
						guardBlocked = append(guardBlocked, foreignCrewDBWriteBlockedPaths(profileRoot, workflowReadOnlyFolders)...)
					}
					if len(guardBlocked) > 0 {
						workspace.SetSessionFolderGuardBlockedWritePaths(sessionID, guardBlocked)
					}
					if isProjectProfileID(resolvedProfile.Definition.ID) {
						// Other Crews' files are shared, their chats are not: deny
						// every referenced Crew's builder/ (transcripts and other
						// users' mirrored chats). This Crew's own chats stay open.
						workspace.SetSessionFolderGuardBlockedPaths(sessionID, foreignCrewChatBlockedPaths(profileRoot, workflowReadOnlyFolders))
					}
					if isProjectProfileID(resolvedProfile.Definition.ID) {
						// Work uses the shared managed database boundary: migrations and
						// row tools are allowed, while raw SQLite/WAL/SHM access is denied.
						// Readers get the read side of that boundary.
						todo_creation_human.ConfigureManagedWorkflowDBSession(sessionID, profileRoot, !crewReadOnlyTurn)
					}
					if sandbox.IsStrict() {
						// The profile asked for the deny-by-default shell: only the
						// paths above, system binaries and scratch exist for the
						// command, with network cut when the policy says so.
						workspace.SetSessionSandbox(sessionID, true, sandbox.NetworkDisabled())
					}
					if hostDownloads := common.GrantSessionCDPHostDownloadsReadWrite(sessionID, hostDownloadsBrowserMode(req)); hostDownloads != "" {
						log.Printf("[AGENT PROFILE FOLDER GUARD] Added read-write CDP host Downloads: %s", hostDownloads)
					}
					log.Printf("[AGENT PROFILE FOLDER GUARD] Applied project restriction (profile=%s workspace=%s read-only=%v)", resolvedProfile.Definition.ID, profileWrite, profileReadOnly)
				} else {
					orgPulseWrite := "pulse/"
					additionalFolders := append([]string{}, resolvedGrants.WriteFolders...)
					additionalFolders = append(additionalFolders, fileContextWriteFolders...)
					additionalFolders = append(additionalFolders, perUserChatHistory)
					additionalFolders = append(additionalFolders, orgPulseWrite)
					workspaceExecutors = wrapExecutorsWithPlanFolderGuard(workspaceExecutors, perUserChatsFolder, workflowReadOnlyFolders, additionalFolders...)
					workspace.SetSessionWorkingDir(sessionID, chatWorkingFolder)
					readPaths := append([]string{perUserChatsWrite, perUserChatHistory, "skills/", "subagents/", "Downloads/", "Workflow/"}, additionalFolders...)
					readPaths = append(readPaths, resolvedGrants.ReadOnlyExtra...)
					readPaths = append(readPaths, workflowReadOnlyFolders...)
					workspace.SetSessionFolderGuard(sessionID,
						readPaths,
						append([]string{perUserChatsWrite, "Downloads/", perUserChatHistory}, additionalFolders...),
					)
					if hostDownloads := common.GrantSessionCDPHostDownloadsReadWrite(sessionID, hostDownloadsBrowserMode(req)); hostDownloads != "" {
						log.Printf("[MULTI-AGENT FOLDER GUARD] Added read-write CDP host Downloads: %s", hostDownloads)
					}
					log.Printf("[MULTI-AGENT FOLDER GUARD] Applied per-user folder restriction (chats: %s, write: %v, read-only: %v, grants: %v)", perUserChatsWrite, additionalFolders, workflowReadOnlyFolders, resolvedGrants.AppliedNames)
				}
			} else {
				perUserChatsWrite := perUserChatsFolder + "/"
				perUserChatHistory := strings.TrimSuffix(perUserChatsFolder, "Chats") + "chat_history/"
				// PLAT-262: a read-only identity gets none of the approved external
				// write grants either — write access is write access regardless of
				// source. Their own chat-history folder stays writable (that's
				// conversation persistence, not workflow mutation) and readPaths
				// below is built the same for everyone (full reads, unaffected).
				var extraWriteFolders []string
				if !currentUserIsReadOnly {
					extraWriteFolders = append([]string{}, resolvedGrants.WriteFolders...)
					extraWriteFolders = append(extraWriteFolders, fileContextWriteFolders...)
				}
				extraFolders := append(append([]string{}, extraWriteFolders...), perUserChatHistory)
				workspaceExecutors = wrapExecutorsWithWorkflowPhaseFolderGuard(workspaceExecutors, effectiveWorkflowPhaseFolderForWrites, workflowReadOnlyFolders, fileContextBlockedWriteFolders, extraFolders...)
				workspace.SetSessionWorkingDir(sessionID, chatWorkingFolder)
				workflowReadRoot := tokenSessionWorkflowReadRoot(GetUserFromContext(r.Context()), workflowPhaseFolder)
				readPaths := append([]string{perUserChatsWrite, perUserChatHistory, "Downloads/", "skills/", "subagents/", workflowReadRoot}, extraFolders...)
				readPaths = append(readPaths, workflowReadOnlyFolders...)
				writePaths := workflowPhaseWriteFolders(effectiveWorkflowPhaseFolderForWrites, extraFolders...)
				if req.ExternalBuilderOperationID != "" {
					readPaths, writePaths = externalBuilderFolderPaths(workflowPhaseFolder)
					workspaceExecutors = wrapExecutorsWithFolderGuard(workspaceExecutors, "EXTERNAL BUILDER", "EXTERNAL BUILDER", folderGuardContextWorkflow, nil, fileContextBlockedWriteFolders, writePaths)
				}
				workspace.SetSessionFolderGuard(sessionID,
					readPaths,
					writePaths,
				)
				// Blocked-write paths flow through to the isolator's
				// FolderGuardConfig.BlockedWritePaths and are enforced at kernel-sandbox
				// level — source of truth for what the shell can actually write. Matches
				// the blocked-write list applied to the typed-tool wrapper above so both
				// surfaces deny the same prefixes. Reads remain permitted so agents can
				// still inspect plan.json and friends.
				if len(fileContextBlockedWriteFolders) > 0 {
					workspace.SetSessionFolderGuardBlockedWritePaths(sessionID, fileContextBlockedWriteFolders)
				}
				// PLAT-221: the main Workflow Builder is a managed DB client just
				// like its workshop children. Give the dedicated DB tools their
				// trusted logical capability and hard-block raw db.sqlite/WAL/SHM
				// access so a denied migration cannot fall back to the sqlite CLI.
				todo_creation_human.ConfigureManagedWorkflowDBSession(
					sessionID,
					workflowPhaseFolder,
					!currentUserIsReadOnly,
				)
				protectOtherWorkflowBuilderChats(sessionID, workflowPhaseFolder, currentUserID)
				if hostDownloads := externalBuilderHostDownloads(req, sessionID); hostDownloads != "" {
					log.Printf("[WORKFLOW PHASE FOLDER GUARD] Added read-write CDP host Downloads: %s", hostDownloads)
				}
				if req.ExternalBuilderOperationID == "" {
					todo_creation_human.RefreshWorkflowFolderAccessSession(sessionID, workflowPhaseFolder)
				}
				log.Printf("[WORKFLOW PHASE FOLDER GUARD] Applied workflow folder restriction (workflow writes: %v, chats read-only: %s, read-only: %v, blocked-write: %v)", writePaths, perUserChatsWrite, workflowReadOnlyFolders, fileContextBlockedWriteFolders)
			}

			if err := api.registerExternalBuilderWorkspaceTools(llmAgent, workflowPhaseFolder, GetUserFromContext(r.Context())); err != nil {
				sendError(fmt.Sprintf("Failed to register external Builder file tools: %v", err), true)
				return
			}

			// Coding-agent adapters project system instructions, selected skills,
			// MCP config and provider metadata into the working directory. Keep
			// those readable for the CLI but outside the agent's write authority.
			protectManagedCodingAgentProjectionWrites(sessionID, chatWorkingFolder)

			// Report the selected filesystem skills, not a restriction. Every
			// branch above grants "skills/" wholesale, and this list is used
			// nowhere else — the old wording ("only selected skills accessible")
			// described a guard that is not applied, which sent me looking for a
			// permission problem when a skill failed to attach.
			// Runtime-only skills such as agent-browser are exposed through tools/prompts, not skills/<name>/SKILL.md.
			if filesystemSkills := filesystemSelectedSkills(req.SelectedSkills); len(filesystemSkills) > 0 {
				log.Printf("[SKILLS] Filesystem skills selected for this session: %v (skills/ is readable in full)", filesystemSkills)
			}

			workspaceToolModeLabel := "chat mode"
			if isWorkflowPhase {
				workspaceToolModeLabel = "workflow builder"
			}
			log.Printf("[WORKSPACE TOOLS] Registering %d workspace tools for %s", len(workspaceTools), workspaceToolModeLabel)

			for _, tool := range workspaceTools {
				if tool.Function == nil {
					log.Printf("[WORKSPACE TOOLS] Warning: Skipping tool with nil Function")
					continue
				}
				toolName := tool.Function.Name
				if externalBuilderToolDenied(GetUserFromContext(r.Context()), toolName) {
					continue
				}
				if profileDisablesVirtualTool(resolvedProfile, toolName) {
					log.Printf("[AGENT_PROFILE] Skipping disabled virtual tool %s for profile %s", toolName, resolvedProfile.Definition.ID)
					continue
				}
				if executor, exists := workspaceExecutors[toolName]; exists {
					// Enhance tool description based on mode
					var enhancedDescription string
					if !isWorkflowPhase {
						descriptionRoot := perUserChatsFolder
						if resolvedProfile != nil {
							descriptionRoot = agentProfileRuntimeWorkspace(currentUserID, req.SelectedFolder)
						}
						enhancedDescription = enhanceToolDescriptionForMultiAgentMode(toolName, tool.Function.Description, descriptionRoot, resolvedProfile)
					} else {
						enhancedDescription = enhanceToolDescriptionForWorkflowPhase(toolName, tool.Function.Description, workflowPhaseFolder)
					}

					// Convert Parameters to map[string]interface{}
					var params map[string]interface{}
					if tool.Function.Parameters != nil {
						paramsBytes, err := json.Marshal(tool.Function.Parameters)
						if err == nil {
							json.Unmarshal(paramsBytes, &params)
						}
					}
					if params == nil {
						log.Printf("[WORKSPACE TOOLS] Warning: Failed to convert parameters for tool %s", toolName)
						continue
					}

					// Get tool category from the category map - REQUIRED
					toolCategory := toolCategories[toolName]
					if toolCategory == "" {
						log.Printf("[WORKSPACE TOOLS ERROR] Tool %s not found in toolCategories map - category is REQUIRED!", toolName)
						sendError(fmt.Sprintf("Failed to register workspace tool %s: category is REQUIRED", toolName), true)
						return
					}

					// Executor is already the correct type (func(ctx, args) (string, error))
					// No type assertion needed unlike workflow where executors are map[string]interface{}
					if err := llmAgent.RegisterCustomTool(
						toolName,
						enhancedDescription,
						params,
						executor,
						toolCategory,
					); err != nil {
						log.Printf("[WORKSPACE TOOLS ERROR] Failed to register tool %s: %v", toolName, err)
						sendError(fmt.Sprintf("Failed to register workspace tool %s: %v", toolName, err), true)
						return
					}
					log.Printf("[WORKSPACE TOOLS] Registered workspace tool: %s (category: %s)", toolName, toolCategory)
				}
			}
			log.Printf("[WORKSPACE TOOLS] Successfully registered %d workspace tools for %s", len(workspaceTools), workspaceToolModeLabel)

			// Register browser tool if browser access is enabled. Shared with the
			// restore path via registerCodingBrowserTools; the folder guard is
			// supplied as a closure so this path's rich grant-based guard is applied
			// verbatim (the shared helper never computes guards itself).
			if enableBrowserAccess {
				browserGuard := func(execs codingAgentToolExecutors) codingAgentToolExecutors {
					if !isWorkflowPhase {
						if resolvedProfile != nil && !isGlobalScopedProfile(resolvedProfile) {
							profileRoot := agentProfileRuntimeWorkspace(currentUserID, req.SelectedFolder)
							profileReadOnly := agentProfileReadOnlyFolders(resolvedProfile.Definition.Runtime.Sandbox, workflowReadOnlyFolders)
							// Crew Run mode: browser tools in a reader turn
							// keep the project readable but unwritable,
							// matching the session guard above.
							if currentUserIsReadOnly && isProjectProfileID(resolvedProfile.Definition.ID) {
								profileReadOnly = append([]string{strings.TrimSuffix(profileRoot, "/") + "/"}, profileReadOnly...)
								profileRoot = ""
							}
							return wrapExecutorsWithPlanFolderGuard(execs, profileRoot, profileReadOnly)
						}
						additionalFolders := append([]string{}, resolvedGrants.WriteFolders...)
						additionalFolders = append(additionalFolders, fileContextWriteFolders...)
						return wrapExecutorsWithPlanFolderGuard(execs, perUserChatsFolder, workflowReadOnlyFolders, additionalFolders...)
					}
					// PLAT-262: this closure previously granted full write access to a
					// read-only session's browser tools unconditionally — a real gap,
					// found while investigating the shell-command read regression above.
					var browserExtraFolders []string
					browserGuardWriteFolder := workflowPhaseFolder
					if currentUserIsReadOnly {
						browserGuardWriteFolder = ""
					} else {
						browserExtraFolders = append([]string{}, resolvedGrants.WriteFolders...)
						browserExtraFolders = append(browserExtraFolders, fileContextWriteFolders...)
					}
					return wrapExecutorsWithWorkflowPhaseFolderGuard(execs, browserGuardWriteFolder, workflowReadOnlyFolders, fileContextBlockedWriteFolders, browserExtraFolders...)
				}
				browserPorts := getCdpPorts(req)
				if getBrowserMode(req) == "auto" {
					browserPorts = configuredCDPPortsForMode(getBrowserMode(req), req.CdpPort, req.CdpPorts)
				}
				if err := registerCodingBrowserTools(llmAgent, sessionID, getBrowserMode(req), browserPorts, browserGuard); err != nil {
					log.Printf("[BROWSER TOOLS ERROR] %v", err)
				} else {
					log.Printf("[BROWSER TOOLS] Successfully registered browser tools for %s", workspaceToolModeLabel)
				}
			}

		}

		// Add custom agent instructions based on agent mode
		if underlyingAgent := llmAgent.GetUnderlyingAgent(); underlyingAgent != nil {
			// Create custom tools for the agent. Workflow-phase (workshop) agents need
			// the full applicable human-tool set registered — notably notify_user.
			// Chat mode stays minimal (workflowMode=false). Without
			// this, notify_user was never registered as a custom tool, so it never landed
			// in a.customTools and was invisible to CLI agents via get_api_spec.
			allTools, allExecutors, toolCategories := createCustomTools(isWorkflowPhase, currentUserID, sessionID) // session-aware
			api.guardPulseResultExecutor(allExecutors, sessionID)

			// Register each custom tool with the agent
			// This updates the custom-tool registry and invalidates affected API specifications.
			// Note: Workspace tools are already registered above, skip them in allTools
			registeredCount := 0
			for _, tool := range allTools {
				if tool.Function != nil {
					toolName := tool.Function.Name
					toolCategory := toolCategories[toolName]
					chatMode := "builder"
					if currentUserIsReadOnly {
						chatMode = "run"
					}
					allowedHumanTool := false
					if relayChat {
						allowedHumanTool = relayproduct.BuilderAllowsTool(toolName)
					} else {
						allowedHumanTool = agentworksproduct.ChatAllowsTool(chatMode, toolName)
					}
					if isWorkflowPhase && virtualtools.IsHumanToolCategory(toolCategory) && !allowedHumanTool {
						log.Printf("[CUSTOM TOOLS] Skipping human tool %s because the product chat surface does not admit it in %s mode", toolName, chatMode)
						continue
					}
					if isWorkflowPhase && (accessTokenRunToolDenied(GetUserFromContext(r.Context()), toolName) || externalBuilderToolDenied(GetUserFromContext(r.Context()), toolName)) {
						log.Printf("[CUSTOM TOOLS] Skipping external-token-denied tool %s in session %s", toolName, sessionID)
						continue
					}

					// Skip workspace tools - already registered above.
					switch toolCategories[toolName] {
					case "workspace_tools", virtualtools.GetWorkspaceAdvancedToolCategory(), virtualtools.GetWorkspaceBrowserToolCategory():
						continue
					}
					if executor, exists := allExecutors[toolName]; exists {
						// Convert executor to the expected function signature
						if execFunc, ok := executor.(func(ctx context.Context, args map[string]interface{}) (string, error)); ok {
							// Convert Parameters to map[string]interface{} by marshaling/unmarshaling
							var params map[string]interface{}
							if tool.Function.Parameters != nil {
								paramsBytes, err := json.Marshal(tool.Function.Parameters)
								if err == nil {
									json.Unmarshal(paramsBytes, &params)
								}
							}
							if params == nil {
								params = make(map[string]interface{})
							}

							// Get tool category from the category map - REQUIRED
							if toolCategory == "" {
								log.Printf("[CUSTOM TOOLS ERROR] Tool %s not found in toolCategories map - category is REQUIRED!", toolName)
								// Continue to next tool instead of failing entire request
								continue
							}

							// Wrap human tools to inject SessionEventEmitter for blocking events (feedback/questions)
							registrationFunc := execFunc
							if virtualtools.IsHumanToolCategory(toolCategory) {
								originalExec := execFunc
								registrationFunc = func(ctx context.Context, args map[string]interface{}) (string, error) {
									ctx = context.WithValue(ctx, virtualtools.SessionEventEmitterKey, &sessionEventEmitter{
										eventStore: api.eventStore,
										sessionID:  sessionID,
									})
									return originalExec(ctx, args)
								}
							}

							// Register the tool and refresh the relevant runtime metadata.
							if err := llmAgent.RegisterCustomTool(
								toolName,
								tool.Function.Description,
								params,
								registrationFunc,
								toolCategory,
							); err != nil {
								log.Printf("[CUSTOM TOOLS ERROR] Failed to register tool %s: %v", toolName, err)
								// Continue to next tool instead of failing entire request
								continue
							}
							registeredCount++
							log.Printf("[CUSTOM TOOLS] Registered custom tool: %s (category: %s)", toolName, toolCategory)
						}
					}
				}
			}
			log.Printf("[CUSTOM TOOLS] Registered %d custom tools with agent", registeredCount)

			crewReadOnly := currentUserIsReadOnly && resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID)
			if err := api.registerAgentProfileTools(llmAgent, toolGate, resolvedProfile, currentUserID, sessionID, req.SelectedFolder, crewReadOnly, req); err != nil {
				logfWithContext(queryLogCtx, "[AGENT PROFILE] Failed to register tools: %v", err)
				sendError(fmt.Sprintf("Failed to register agent profile tools: %v", err), true)
				return
			}
			if err := api.registerAgentProfileWorkflowTools(
				context.WithoutCancel(streamCtx),
				llmAgent,
				resolvedProfile,
				currentUserID,
				sessionID,
				req.SelectedFolder,
				selectedServers,
				mergedAPIKeys,
				req,
			); err != nil {
				logfWithContext(queryLogCtx, "[AGENT PROFILE] Failed to register workflow runtime: %v", err)
				sendError(fmt.Sprintf("Failed to register agent profile workflow runtime: %v", err), true)
				return
			}
			// Crew Run mode: readers get foreground-only turns (background
			// delegates would need their own guard inheritance proof) and
			// no dashboard authoring.
			if !crewReadOnly {
				if err := api.registerWorkBackgroundTools(llmAgent, resolvedProfile, req, sessionID, currentUserID); err != nil {
					logfWithContext(queryLogCtx, "[WORK BACKGROUND] Failed to register tools: %v", err)
					sendError(fmt.Sprintf("Failed to register Work background tools: %v", err), true)
					return
				}
				if err := api.registerWorkDashboardTools(llmAgent, resolvedProfile, sessionID, currentUserID, req.SelectedFolder); err != nil {
					logfWithContext(queryLogCtx, "[WORK DASHBOARD] Failed to register tools: %v", err)
					sendError(fmt.Sprintf("Failed to register Work Dashboard tools: %v", err), true)
					return
				}
			}
			// Code-first asynchronous waits are available only in ordinary Builder
			// chat and writable Crew chat. Workflow-phase agents already have their
			// own execution lifecycle, and read-only Crew runs must not gain a new
			// process/write path.
			canTriggerAutoNotify := triggerAutoNotifyAvailable(resolvedProfile, currentUserID, req.SelectedFolder, isWorkflowPhase, crewReadOnly, toolGate)
			if canTriggerAutoNotify {
				if err := api.registerBackgroundCodeTools(llmAgent, req, sessionID, currentUserID); err != nil {
					logfWithContext(queryLogCtx, "[BACKGROUND CODE] Failed to register tools: %v", err)
					sendError(fmt.Sprintf("Failed to register background code tools: %v", err), true)
					return
				}
			}
			isToolBackedChat := !isWorkflowPhase
			isAgentWorksChat := isToolBackedChat && resolvedProfile == nil
			if isToolBackedChat {
				if err := api.registerMultiAgentLLMTools(llmAgent, func(toolName string) bool {
					return profileDisablesVirtualTool(resolvedProfile, toolName)
				}); err != nil {
					logfWithContext(queryLogCtx, "[LLM TOOLS] Failed to register multi-agent LLM tools: %v", err)
					sendError(fmt.Sprintf("Failed to register multi-agent LLM tools: %v", err), true)
					return
				}
				logfWithContext(queryLogCtx, "[LLM TOOLS] Registered multi-agent LLM tools")

				skillProductID := ""
				if resolvedProfile != nil {
					skillProductID = resolvedProfile.Definition.ID
				}
				// A Code's skills are private to it: installs and removals act on
				// its own skills/ folder, never the shared library.
				projectSkillsDir := ""
				if resolvedProfile != nil && strings.EqualFold(resolvedProfile.Definition.ID, codeproduct.ProfileID) && isActiveWorkProjectWorkspace(currentUserID, req.SelectedFolder) {
					projectSkillsDir = strings.TrimSuffix(agentProfileRuntimeWorkspace(currentUserID, req.SelectedFolder), "/") + "/skills"
				}
				if err := api.registerMultiAgentSkillToolsIn(llmAgent, func(toolName string) bool {
					return profileDisablesVirtualTool(resolvedProfile, toolName)
				}, skillProductID, projectSkillsDir); err != nil {
					logfWithContext(queryLogCtx, "[SKILL TOOLS] Failed to register multi-agent skill tools: %v", err)
					sendError(fmt.Sprintf("Failed to register multi-agent skill tools: %v", err), true)
					return
				}
				logfWithContext(queryLogCtx, "[SKILL TOOLS] Registered multi-agent skill tools")
			}
			if isWorkflowBuilderPhase && req.ExternalBuilderOperationID == "" {
				if err := api.registerAccessibleWorkflowListTool(llmAgent, currentUserID, nil); err != nil {
					logfWithContext(queryLogCtx, "[WORKFLOW DISCOVERY] Failed to register accessible workflow listing: %v", err)
					sendError(fmt.Sprintf("Failed to register workflow discovery tools: %v", err), true)
					return
				}
				logfWithContext(queryLogCtx, "[WORKFLOW DISCOVERY] Registered list_accessible_workflows for Builder")
			}
			if isWorkflowPhase && req.ExternalBuilderOperationID == "" {
				if err := api.registerWorkflowLLMDiscoveryTools(llmAgent); err != nil {
					logfWithContext(queryLogCtx, "[LLM TOOLS] Failed to register workflow LLM discovery tools: %v", err)
					sendError(fmt.Sprintf("Failed to register workflow LLM discovery tools: %v", err), true)
					return
				}
				logfWithContext(queryLogCtx, "[LLM TOOLS] Registered workflow LLM discovery tools")
			}
			if isToolBackedChat {
				activeForMCP, _ := api.getActiveSession(sessionID)
				mcpPolicy := resolveWorkflowChatPolicy(sessionID, req, activeForMCP, currentUserIsReadOnly)
				if err := api.registerMCPToolsForChat(llmAgent, mcpPolicy, func(toolName string) bool {
					return profileDisablesVirtualTool(resolvedProfile, toolName)
				}); err != nil {
					logfWithContext(queryLogCtx, "[MCP SERVER TOOLS] Failed to register multi-agent MCP server tools: %v", err)
					sendError(fmt.Sprintf("Failed to register multi-agent MCP server tools: %v", err), true)
					return
				}
				logfWithContext(queryLogCtx, "[CHAT_POLICY] MCP management admission: mode=%s origin=%s allowed=%v inspect=%v", mcpPolicy.Mode, mcpPolicy.Origin, mcpPolicy.allows("mcp_management"), mcpPolicy.allows("mcp_inspection"))
			}
			if isToolBackedChat {
				secretWorkflowPath := ""
				if resolvedProfile != nil {
					secretWorkflowPath = req.SelectedFolder
				}
				// Secret tools run inside the current agent turn. Push mutations into
				// every live workspace client for this retained session so a Work
				// agent can save a project secret and use it in its very next shell
				// call. Later turns still reload project secrets from encrypted
				// storage in resolveAgentProfileForQuery.
				afterSecretUpsert := func(ctx context.Context, name, value string) error {
					if resolvedProfile != nil && !isGlobalScopedProfile(resolvedProfile) && secretWorkflowPath != "" {
						if err := updateProductSelectedSecrets(ctx, resolvedProfile.Definition.ID, secretWorkflowPath, func(current []string) []string {
							return appendUniqueStrings(current, name)
						}); err != nil {
							return err
						}
					}
					updated := virtualtools.SetSessionShellEnv(sessionID, "SECRET_"+name, value)
					logfWithContext(queryLogCtx, "[SECRET TOOLS] Made SECRET_%s available to %d live shell client(s) for session %s", name, updated, sessionID)
					return nil
				}
				afterSecretDelete := func(ctx context.Context, name string) error {
					if resolvedProfile != nil && !isGlobalScopedProfile(resolvedProfile) && secretWorkflowPath != "" {
						if err := updateProductSelectedSecrets(ctx, resolvedProfile.Definition.ID, secretWorkflowPath, func(current []string) []string {
							return removeString(current, name)
						}); err != nil {
							return err
						}
					}
					updated := virtualtools.DeleteSessionShellEnv(sessionID, "SECRET_"+name)
					logfWithContext(queryLogCtx, "[SECRET TOOLS] Removed SECRET_%s from %d live shell client(s) for session %s", name, updated, sessionID)
					return nil
				}
				if err := api.registerSecretManagementTools(llmAgent, currentUserID, secretWorkflowPath, "secret_tools", currentUserIsReadOnly, afterSecretUpsert, afterSecretDelete); err != nil {
					logfWithContext(queryLogCtx, "[SECRET TOOLS] Failed to register multi-agent secret tools: %v", err)
					sendError(fmt.Sprintf("Failed to register multi-agent secret tools: %v", err), true)
					return
				}
				logfWithContext(queryLogCtx, "[SECRET TOOLS] Registered multi-agent secret tools with live session injection (list_secrets; global names read-only)")
			}
			if isAgentWorksChat {
				if err := api.registerCustomCommandTools(llmAgent, currentUserID, ""); err != nil {
					sendError(fmt.Sprintf("Failed to register custom command tools: %v", err), true)
					return
				}
				// Generic AgentWorks chat can create workflows and inspect live
				// workflow activity. Product-owned agents opt into their own tools.
				if userAccessForClaims(GetUserFromContext(r.Context())).CanCreate {
					if err := api.registerWorkflowCreatorTool(llmAgent); err != nil {
						logfWithContext(queryLogCtx, "[WORKFLOW CREATOR] Failed to register create_workflow tool: %v", err)
						sendError(fmt.Sprintf("Failed to register create_workflow tool: %v", err), true)
						return
					}
					logfWithContext(queryLogCtx, "[WORKFLOW CREATOR] Registered create_workflow tool")
				} else {
					logfWithContext(queryLogCtx, "[WORKFLOW CREATOR] create_workflow omitted: account cannot create workflows")
				}

				if err := api.registerActivityStatusTool(llmAgent, currentUserID); err != nil {
					logfWithContext(queryLogCtx, "[ACTIVITY STATUS] Failed to register get_activity_status tool: %v", err)
					sendError(fmt.Sprintf("Failed to register get_activity_status tool: %v", err), true)
					return
				}
				logfWithContext(queryLogCtx, "[ACTIVITY STATUS] Registered get_activity_status tool")

			}

			// Read session state early for guidance injection.
			// NOTE: UpdateCodeExecutionRegistry is called AFTER all AppendSystemPrompt calls
			// so that AppendedSystemPrompts is fully populated before the registry rebuild
			// re-assembles the final system prompt.
			api.activeSessionsMux.RLock()
			llmGuidance := ""
			if sess, ok := api.activeSessions[sessionID]; ok {
				llmGuidance = sess.LLMGuidance
			}
			api.activeSessionsMux.RUnlock()

			// ── PROMPT ASSEMBLY ORDER ──
			// Priority-ordered: operating mode → workspace map → context → mode-specific → reference docs.
			// This ensures the LLM sees its core behavior rules before any reference material.

			shellRoot := fsutil.WorkspaceShellRoot()

			// 1. OPERATING MODE — the agent's core direct-chat behavior.
			//    This MUST come first so it takes precedence over reference material.
			//
			// Product profiles use their own prompt. Generic AgentWorks chat uses
			// a small direct-chat prompt with the per-user output path.
			if resolvedProfile != nil && !isGlobalScopedProfile(resolvedProfile) {
				if err := llmAgent.ResetInstructions(resolvedProfile.Prompt); err != nil {
					sendError(fmt.Sprintf("Failed to apply agent profile prompt: %v", err), true)
					return
				}
				logfWithContext(queryLogCtx, "[AGENT PROFILE] Applied %s@%d system prompt", resolvedProfile.Definition.ID, resolvedProfile.Definition.Version)
				// Crew Run mode: readers chat under a read-only operating
				// mode on top of the crew's own prompt. Tools and guards
				// enforce it; the prompt states it so refusals are
				// coherent instead of confused retries.
				if isCrewReaderTurn(req, currentUserID) {
					_ = llmAgent.AddInstructions(crewReaderSystemPrompt(req.SelectedFolder))
				} else if crewGuest != "" {
					_ = llmAgent.AddInstructions(crewGuestSystemPrompt(crewGuest))
				}
			} else if !isWorkflowPhase {
				_ = llmAgent.AddInstructions(virtualtools.GetAgentWorksChatInstructionsWithUser(perUserChatsFolder, currentUserID))
				logfWithContext(queryLogCtx, "[CHAT] Added direct-chat instructions to system prompt")
				// Attach the full reference surface for generic multi-agent chat.
				// A resolved global profile declares its own material individually
				// in profile.skills[] (registerAgentProfileTools' sibling,
				// req.SelectedSkills assignment in resolveAgentProfileForQuery)
				// instead of the mode-gated bundle; attaching both would
				// duplicate the same content under two different shapes.
				// mcpagent exposes attached bundles through read_skill on API
				// and coding-CLI transports; native CLI projection is only an
				// additional browseable view.
				if resolvedProfile == nil {
					if err := guidance.AttachReferenceSurface("multi-agent", llmAgent.AttachSkill); err != nil {
						logfWithContext(queryLogCtx, "[REFERENCE_DOC] Failed to attach multi-agent reference surface: %v", err)
					}
				}
			}
			// 2. CONTEXT — skills. Attaching a skill is not an instruction
			//    section (AttachSkill, not AddInstructions), so it stays here
			//    rather than in the prompt-section registry below.
			identitySkillNames := skills.WithAgentBrowserCapability(req.SelectedSkills, buildChatBrowserConfig(req).HasAgentBrowser)
			if len(identitySkillNames) > 0 {
				// Phase 3 rewire: skills are now first-class on the agent.
				// mcpagent's ensureSystemPrompt auto-injects the progressive-
				// disclosure listing (name + description); CLI transports
				// additionally project SKILL.md folders to disk in Phase 3b.
				// The legacy buildSkillPrompt path is gone.
				// Resolve against the session's own folder first. A product that
				// installs skills into its project (Video Studio installs its
				// managed HyperFrames set there) was invisible to the unscoped
				// lookup, so those skills never attached — and read_skill, which
				// serves only ATTACHED skills, then had nothing to read. That is
				// what surfaced in chat as "the skill reader was blocked".
				// read_skill can also serve a skill installed in this workspace
				// but not attached, so a router skill names the specialists and
				// the agent asks by name instead of shelling out to a path.
				if err := llmAgent.SetInstalledSkillResolver(installedSkillResolver(req.SelectedFolder)); err != nil {
					logfWithContext(queryLogCtx, "[SKILLS] Failed to configure installed-skill resolver: %v", err)
				}
				attached := resolvedProfileSkills
				if resolvedProfile == nil {
					attached = skills.LoadAttachableIn(getWorkspaceAPIURL(), req.SelectedFolder, identitySkillNames)
				}
				if len(attached) > 0 {
					attachedNames := make([]string, 0, len(attached))
					for _, s := range attached {
						_ = llmAgent.AttachSkill(s)
						attachedNames = append(attachedNames, s.Name)
					}
					log.Printf("[SKILLS] Attached %d of %d skill(s): %v", len(attached), len(identitySkillNames), attachedNames)
					if resolvedProfile != nil {
						api.conversationMux.Lock()
						api.lastAgentProfileKeyBySession[sessionID] = agentProfileSessionKey(resolvedProfile)
						api.conversationMux.Unlock()
					}
				}
			}

			// 3. INSTRUCTION SECTIONS — workspace map, capabilities, workflow
			//    context, channel formatting, browser pointer, reference docs,
			//    grants, and the CLI tool environment. Each is a named section
			//    with its condition beside every other section's, and the
			//    assembler logs what it applied. See prompt_sections.go for why
			//    these stopped being inline ifs.
			promptCtx := promptContext{
				Provider:                 req.Provider,
				HasProfile:               resolvedProfile != nil,
				IsWorkflowPhase:          isWorkflowPhase,
				HasTriggerAutoNotifyTool: canTriggerAutoNotify,
				ShellRoot:                shellRoot,
				PerUserChatsFolder:       perUserChatsFolder,
				WorkflowPhaseFolder:      workflowPhaseFolder,
				ProfileWorkspace:         agentProfileRuntimeWorkspace(currentUserID, req.SelectedFolder),
				CapabilitySection:        buildLLMCapabilityPromptSection(r.Context()),
				// The snapshot instructs the agent to call these. The gate is the
				// authority on whether it can, so ask it rather than assuming.
				HasLLMCapabilityTools: toolGate.Admit("list_llm_capabilities") ||
					toolGate.Admit("set_provider_auth"),
				ChannelFormatting: buildChannelFormattingInstructions(req.BotPlatform),
				GrantSections:     resolvedGrants.PromptSections,
			}
			if resolvedProfile != nil {
				promptCtx.ProfileID = resolvedProfile.Definition.ID
				promptCtx.NativeCodingTools = strings.EqualFold(strings.TrimSpace(resolvedProfile.Definition.Runtime.AgentTools.Mode), "hybrid")
				promptCtx.FeatureExtensions = agentprofiles.FeaturePromptExtensions(resolvedProfile.Definition)
			}
			if len(req.WorkflowContextPaths) > 0 {
				promptPaths := req.authorizedWorkflowContextReadPaths
				if len(promptPaths) == 0 {
					promptPaths = req.WorkflowContextPaths
				}
				promptCtx.WorkflowContext = buildWorkflowContextPromptWithLabels(promptPaths, workflowContextLabels(req.WorkflowContextRefs))
			}
			if resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID) {
				promptCtx.WorkFolders = workproduct.BuildAttachedFoldersPrompt(workFolderGrantsForClaims(r.Context(), GetUserFromContext(r.Context())))
			}
			// The full browser guide is a ~10KB on-demand skill; this is only a
			// pointer so the agent knows the surface exists.
			chatBrowserCfg := buildChatBrowserConfig(req)
			if chatBrowserCfg.HasAgentBrowser {
				browserPrompt := "\n## Browser\n\nThis session has a browser tool configured (mode=" + chatBrowserCfg.Mode + "). " +
					"CDP availability is live state and is not stored in this prompt. Before first use, call `agent_browser(command=\"status\", session=\"default\")` and follow its `effective_mode` and authorized endpoints. Read `read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/browser-usage.md\"}])` for tab, file, and safety rules. "
				if chatBrowserCfg.Mode == "auto" || chatBrowserCfg.Mode == "cdp" {
					_, endpointGuidance := cdpPromptEndpoints(chatBrowserCfg.CdpPorts, chatBrowserCfg.CdpPort)
					browserPrompt += endpointGuidance + " These endpoints are configured candidates; live status is authoritative.\n"
				}
				promptCtx.BrowserPointer = browserPrompt
			}
			// Skip the generic hardcoded tool inventory when the profile has its
			// own tool_policy allowlist -- that product's own system prompt is
			// already the authoritative source of what tools exist. The generic
			// text predates per-product custom tools: it names platform tools an
			// allowlist filters out and never mentions the product's own tool at
			// all, so a CLI-provider product chat (e.g. SparkQuill, Dominion) can
			// correctly run its one allowlisted tool and then, in the very same
			// turn, tell the user it has no working tool because this stale text
			// contradicted what actually happened.
			hasProductAllowlist := relayChat || resolvedProfile != nil && resolvedProfile.Definition.ToolPolicy.IsAllowlist()
			if common.IsCLIProvider(req.Provider) && !hasProductAllowlist {
				promptCtx.CLIToolEnvironment = virtualtools.BuildCLIToolEnvironmentPrompt(req.Provider)
			}

			includedSections, skippedSections, sectionErr := assemblePromptSections(llmAgent, promptCtx)
			if sectionErr != nil {
				sendError(fmt.Sprintf("Failed to assemble the system prompt: %v", sectionErr), true)
				return
			}
			logPromptAssembly(promptCtx, includedSections, skippedSections)
			if len(resolvedGrants.PromptSections) > 0 {
				log.Printf("[GRANTS] Appended %d prompt section(s) for active grants: %v", len(resolvedGrants.PromptSections), resolvedGrants.AppliedNames)
			}

			log.Printf("[SYSTEM_PROMPT] Final assembled prompt length=%d chars, hasGuidance=%v", len(llmAgent.AssemblyInstructions()), req.LLMGuidance != "" || llmGuidance != "")

			// --- Workflow Phase Chat Mode ---
			// Override system prompt and register plan modification tools for conversational phase editing
			if isWorkflowPhase && workflowPhaseID != "" {
				log.Printf("[WORKFLOW_PHASE] Setting up phase chat mode: phase=%s preset=%s", workflowPhaseID, req.PresetQueryID)

				// Get workspace path and objective from preset or request
				phaseWorkspacePath := ""
				phaseObjective := ""
				// For scheduler/cron triggers, the workspace path comes from selected_folder
				// and preset may not exist in the DB. Use selected_folder as primary source.
				if req.SelectedFolder != "" {
					phaseWorkspacePath = req.SelectedFolder
				}
				// Resolve workspace path from manifest if not already set
				if phaseWorkspacePath == "" && req.PresetQueryID != "" {
					if p, e := api.resolveWorkspacePathFromPreset(context.Background(), req.PresetQueryID); e == nil && p != "" {
						phaseWorkspacePath = p
					}
				}
				// Load objective from manifest label
				if phaseWorkspacePath != "" && phaseObjective == "" {
					if manifest, found, mErr := ReadWorkflowManifest(context.Background(), phaseWorkspacePath); mErr == nil && found {
						phaseObjective = manifest.Label
					}
				}
				if phaseWorkspacePath == "" {
					// Fallback: try to extract workspace path from the query's file context marker
					phaseWorkspacePath = extractWorkspacePathFromObjective(req.Query)
				}
				if phaseWorkspacePath == "" {
					log.Printf("[WORKFLOW_PHASE] WARNING: No workspace path found for phase=%s preset=%s - using default_workspace", workflowPhaseID, req.PresetQueryID)
					phaseWorkspacePath = "default_workspace"
				}
				// Set default shell working directory for this session.
				// The global map is read by execute_shell_command at call time.
				if phaseWorkspacePath != "" && phaseWorkspacePath != "default_workspace" {
					workspace.SetSessionWorkingDir(sessionID, phaseWorkspacePath)
					// underlyingAgent is provably non-nil at this point
					// (vet flags the prior nil check as tautological);
					// nil-check removed to silence (govet/nilness).
					phaseCLIWorkingDir, isolationErr := workflowCLIWorkingDir(phaseWorkspacePath, currentUserID, sessionID, finalProvider, workflowCLIMode(&req, currentUserIsReadOnly))
					if isolationErr != nil {
						sendError(isolationErr.Error(), true)
						return
					}
					if err := llmAgent.SetCodingAgentWorkingDir(phaseCLIWorkingDir); err != nil {
						sendError(fmt.Sprintf("Failed to configure coding-agent working directory: %v", err), true)
						return
					}
					// Restrict shell commands to the workflow folder via Isolator
					// Include #workflow read-only paths so the builder can read referenced workflows
					phaseReadPaths := []string{phaseWorkspacePath, "Chats", "skills", "subagents", "Downloads"}
					phaseReadPaths = append(phaseReadPaths, workflowReadOnlyFolders...)
					workspace.SetSessionFolderGuard(sessionID,
						phaseReadPaths,
						[]string{phaseWorkspacePath, "Downloads"},
					)
					if req.ExternalBuilderOperationID != "" {
						readPaths, writePaths := externalBuilderFolderPaths(phaseWorkspacePath)
						workspace.SetSessionFolderGuard(sessionID, readPaths, writePaths)
						workspace.SetSessionFolderGuardBlockedWritePaths(sessionID, []string{phaseWorkspacePath + "/workflow.json", phaseWorkspacePath + "/planning/"})
						protectOtherWorkflowBuilderChats(sessionID, phaseWorkspacePath, currentUserID)
					}
					// The phase setup above rebuilds the long-lived Builder guard.
					// Reapply the managed DB boundary on every setup/restore so old
					// sessions cannot retain broad raw SQLite or sidecar access.
					todo_creation_human.ConfigureManagedWorkflowDBSession(
						sessionID,
						phaseWorkspacePath,
						!currentUserIsReadOnly,
					)
					if hostDownloads := externalBuilderHostDownloads(req, sessionID); hostDownloads != "" {
						log.Printf("[WORKFLOW_PHASE] Added read-write CDP host Downloads: %s", hostDownloads)
					}
					if req.ExternalBuilderOperationID == "" {
						todo_creation_human.RefreshWorkflowFolderAccessSession(sessionID, phaseWorkspacePath)
					}
					if len(workflowReadOnlyFolders) > 0 {
						log.Printf("[WORKFLOW_PHASE] Added read-only access for #workflow references: %v", workflowReadOnlyFolders)
					}

					// Phase 4 carry-over to the workshop chat: auto-attach
					// the workflow's accumulated learnings as a first-class
					// skill so the builder agent sees what the workflow has
					// learned across runs. Mirrors the step-time attach in
					// step_based_workflow.appendSupplementaryPrompts.
					if !relayChat {
						if globalSkill := skills.LoadGlobalSkill(getWorkspaceAPIURL(), phaseWorkspacePath); globalSkill != nil {
							_ = llmAgent.AttachSkill(globalSkill)
							log.Printf("[SKILLS] Auto-attached workflow global skill (_global) from learnings/_global/SKILL.md")
						}
					}
					// Playbooks are workflow-local skills for Builder setup. They are
					// intentionally attached here, after the definitive workflow path is
					// known, and never added to the workflow execution skill defaults.
					if workflowPhaseID == workflowtypes.WorkflowStatusWorkflowBuilder && !relayChat {
						if manifest, found, readErr := ReadWorkflowManifest(r.Context(), phaseWorkspacePath); readErr == nil && found {
							builderSkills := installedBuilderSkillNames(manifest.InstalledPlaybooks)
							for _, playbookSkill := range skills.LoadAttachableIn(getWorkspaceAPIURL(), phaseWorkspacePath, builderSkills) {
								_ = llmAgent.AttachSkill(playbookSkill)
								log.Printf("[PLAYBOOKS] Attached %s to Workflow Builder", playbookSkill.Name)
							}
						}
					}
				}

				// Create workspace client for reading plan.json and variables.json
				phaseWSClient := workspace.NewClient(
					getWorkspaceAPIURL(),
					workspace.WithUserID(currentUserID),
				)

				// readFile closure: reads file content from workspace
				phaseReadFile := func(ctx context.Context, filePath string) (string, error) {
					result, err := phaseWSClient.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: filePath})
					if err != nil {
						return "", err
					}
					return result.Content, nil
				}

				// writeFile closure: writes content to workspace
				phaseWriteFile := func(ctx context.Context, filePath string, content string) error {
					_, err := phaseWSClient.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: filePath, Content: content})
					return err
				}

				// moveFile closure: moves file in workspace
				phaseMoveFile := func(ctx context.Context, src string, dst string) error {
					_, err := phaseWSClient.MoveWorkspaceFile(ctx, workspace.MoveWorkspaceFileParams{SourceFilepath: src, DestinationFilepath: dst})
					return err
				}

				// Build template vars by reading current plan and variables from workspace
				phaseRunFolder := "iteration-0"
				if req.ExecutionOptions != nil && strings.TrimSpace(req.ExecutionOptions.SelectedRunFolder) != "" {
					phaseRunFolder = strings.TrimSpace(req.ExecutionOptions.SelectedRunFolder)
				}
				workflowPhaseRunFolder = phaseRunFolder
				var phaseEnabledGroupNames []string
				if req.ExecutionOptions != nil {
					phaseEnabledGroupNames = req.ExecutionOptions.EnabledGroupNames
				}
				// All workshop agents now run in code-execution mode regardless of
				// provider — there is no longer a tool-search / simple-agent path.
				// Provider-specific CLI handling (prompt template, api-bridge tool
				// mapping, native context) is decided separately via
				// common.IsCLIProvider.
				phaseIsCodeExec := true
				log.Printf("[WORKFLOW_PHASE] Mode detection: finalProvider=%q, isCodeExec=%v (always true)", finalProvider, phaseIsCodeExec)
				phaseTemplateVars := map[string]string{
					"Objective":                   phaseObjective,
					"WorkspacePath":               phaseWorkspacePath,
					"IsCodeExecutionMode":         fmt.Sprintf("%v", phaseIsCodeExec),
					"UseProjectedReferenceSkills": "true", // legacy template key; mcpagent read_skill is transport-neutral
				}
				if relayChat {
					phaseTemplateVars["WorkflowKind"] = "relay"
				}

				// Pass workshop mode from frontend override (auto-detection happens after plan is loaded below).
				// Migrate legacy values to the current 2-mode scheme
				// (workshop/run). The "workshop" mode merges what used to be
				// "builder" + "optimizer" + "reporting" — the merged tool
				// list is a strict superset of all three, and the unified
				// agent decides phase from workspace state (plan exists?
				// runs exist? report work requested?).
				// Two modes exist: "run" (products and bot-routed workflow
				// execution) and "workshop" (everything else). Rather than
				// enumerating retired names — builder, optimizer, reporting,
				// eval, output, ask, debugger, runner — anything that is not
				// "run" collapses to "workshop". An old client or persisted
				// session cannot introduce a value this misses, which is what
				// enumeration risked: an unrecognized mode reaches
				// MaterializeReferenceSkill and yields NO reference surface
				// at all.
				if req.ExecutionOptions != nil && req.ExecutionOptions.WorkshopMode != "" {
					mode := req.ExecutionOptions.WorkshopMode
					// Two modes exist: "run" and "workshop". Anything that is
					// not "run" collapses to "workshop" rather than being
					// enumerated, so no retired name can slip through into
					// MaterializeReferenceSkill and yield NO reference surface.
					switch strings.ToLower(strings.TrimSpace(mode)) {
					case "run", "ask", "debugger", "runner":
						mode = "run"
					default:
						mode = "workshop"
					}
					phaseTemplateVars["WorkshopMode"] = mode
					log.Printf("[WORKSHOP_MODE] Using frontend override: %s (raw=%s)", mode, req.ExecutionOptions.WorkshopMode)
				}

				// Use a detached context for workflow-builder setup. /api/query returns
				// an acknowledgement before the background turn finishes, so r.Context()
				// is canceled while these short setup reads/session refreshes still run.
				setupCtx := context.WithoutCancel(r.Context())

				// Build GroupInfo and extra template vars for the interactive-workshop system prompt
				if workflowPhaseID == workflowtypes.WorkflowStatusWorkflowBuilder {
					groupInfo := buildWorkshopGroupInfo(setupCtx, phaseWorkspacePath, phaseReadFile, phaseRunFolder, phaseEnabledGroupNames)
					if groupInfo != "" {
						phaseTemplateVars["GroupInfo"] = groupInfo
					}
					phaseTemplateVars["RunFolder"] = phaseRunFolder
					phaseTemplateVars["UseKnowledgebase"] = "true"                 // default; overridden by preset below if needed
					phaseTemplateVars["KBShape"] = workflowtypes.KBShapeGraphNotes // default; overridden from manifest below if set
					if phaseWorkspacePath != "" {
						if manifest, found, mErr := ReadWorkflowManifest(context.Background(), phaseWorkspacePath); mErr == nil && found && manifest.Capabilities.LLMConfig != nil {
							if manifest.Capabilities.LLMConfig.KBShape != "" {
								phaseTemplateVars["KBShape"] = workflowtypes.ResolveKBShape(manifest.Capabilities.LLMConfig.KBShape)
							}
						}
					}
				}

				// Read existing plan from workspace (if any)
				existingPlanJSON := todo_creation_human.ReadPlanFromWorkspace(setupCtx, phaseWorkspacePath, phaseReadFile)
				if existingPlanJSON != "" {
					phaseTemplateVars["ExistingPlanJSON"] = existingPlanJSON
					log.Printf("[WORKFLOW_PHASE] Loaded existing plan (%d bytes)", len(existingPlanJSON))

					// Extract compact step summary for builder-style phase prompts
					if stepSummary := extractStepSummary(existingPlanJSON); stepSummary != "" {
						phaseTemplateVars["StepSummary"] = stepSummary
						log.Printf("[WORKFLOW_PHASE] Extracted step summary (%d steps)", strings.Count(stepSummary, "\n"))
					}
				}

				// Extract workflow objective and success_criteria from soul/soul.md —
				// the single canonical source (plan.json and workflow.json no longer
				// hold these fields). See ResolveWorkflowObjective in soul_helpers.go.
				if workflowPhaseID == workflowtypes.WorkflowStatusWorkflowBuilder {
					objective, successCriteria, _ := todo_creation_human.ReadWorkflowObjectiveFromSoul(setupCtx, phaseWorkspacePath, phaseReadFile)
					if objective != "" {
						phaseTemplateVars["WorkflowObjective"] = objective
					}
					if successCriteria != "" {
						phaseTemplateVars["WorkflowSuccessCriteria"] = successCriteria
					}
				}

				// Default workshop mode if not provided by frontend. Run/Reporting
				// are explicit user/frontend choices; everything else defaults
				// to the merged Workshop mode (was previously builder/optimizer).
				if phaseTemplateVars["WorkshopMode"] == "" && existingPlanJSON != "" && workflowPhaseID == workflowtypes.WorkflowStatusWorkflowBuilder {
					phaseTemplateVars["WorkshopMode"] = "workshop"
					log.Printf("[WORKSHOP_MODE] Defaulted to workshop")
				}
				if phaseTemplateVars["WorkshopMode"] == "" {
					phaseTemplateVars["WorkshopMode"] = "workshop"
				}

				// PLAT-262: a read-only identity is always pinned to Run mode,
				// overriding whatever was requested or defaulted above — the
				// client-side toggle is hidden for these users too, but the
				// server has the final say. This is the single point that
				// decides WorkshopMode for both the system prompt rendered
				// just below and every phaseTemplateVars["WorkshopMode"] read
				// inside installWorkflowPhaseTools (same map, passed by
				// reference). Read-only users never perform a same-session
				// Workshop<->Run toggle, so this does not reopen the removed
				// mode-based tool-catalog-filtering bug — see
				// installWorkflowPhaseTools's doc comment.
				if currentUserIsReadOnly {
					phaseTemplateVars["WorkshopMode"] = "run"
				} else {
					// Workshop/Run is permission-derived. Ignore stale saved modes or
					// client requests that attempt to put a writable workflow user on
					// the read-only prompt and tool surface.
					phaseTemplateVars["WorkshopMode"] = "workshop"
				}
				activeForCapabilities, _ := api.getActiveSession(sessionID)
				phasePolicy := resolveWorkflowChatPolicy(sessionID, req, activeForCapabilities, currentUserIsReadOnly)
				if !phasePolicy.allows("plan_authoring") {
					phaseTemplateVars["WorkshopMode"] = "run"
				}

				// Read variable names from workspace (if any)
				variableNames := todo_creation_human.ReadVariablesFromWorkspace(setupCtx, phaseWorkspacePath, phaseReadFile)
				if variableNames != "" {
					phaseTemplateVars["VariableNames"] = variableNames
					log.Printf("[WORKFLOW_PHASE] Loaded variable names")
				}

				// Collect optional runtime sections before composing the complete prompt.
				// The phase composer is shared with the assembled-prompt regression tests.
				var phaseAdditions []string
				if workflowPhaseID == workflowtypes.WorkflowStatusWorkflowBuilder {
					if userPrompt := authenticatedWorkflowUserPrompt(GetUserFromContext(r.Context())); userPrompt != "" {
						phaseAdditions = append(phaseAdditions, userPrompt)
					}
					if notificationPrompt := buildWorkflowNotificationInstructionsPrompt(req.NotificationRunSummaryInstructions, req.NotificationPulseSummaryInstructions); notificationPrompt != "" {
						phaseAdditions = append(phaseAdditions, notificationPrompt)
						log.Printf("[WORKFLOW_PHASE] Appended workflow notification preferences to %s system prompt", workflowPhaseID)
					}
				}

				if workflowPhaseID == workflowtypes.WorkflowStatusWorkflowBuilder {
					// Secrets
					phaseSecrets := mergeGlobalSecrets(req.DecryptedSecrets, req.SelectedGlobalSecrets)
					if len(phaseSecrets) > 0 {
						entries := make([]orchestrator.SecretEntry, len(phaseSecrets))
						for i, s := range phaseSecrets {
							entries[i] = orchestrator.SecretEntry{Name: s.Name, Value: s.Value}
						}
						secretPrompt := todo_creation_human.BuildWorkflowSecretPrompt(entries)
						if secretPrompt != "" {
							phaseAdditions = append(phaseAdditions, secretPrompt)
							log.Printf("[WORKFLOW_PHASE] Appended %d secrets to %s system prompt", len(entries), workflowPhaseID)
						}
					}

					// Browser instructions from manifest config
					// The manifest determines browser mode, not req.EnableBrowserAccess (which is false for workflow_phase)
					if phaseWorkspacePath != "" {
						phaseManifest, phaseFound, phaseMErr := ReadWorkflowManifest(context.Background(), phaseWorkspacePath)
						if phaseMErr == nil && phaseFound {
							configuredBrowserMode := strings.ToLower(strings.TrimSpace(phaseManifest.Capabilities.BrowserMode))
							phaseConfiguredCDPPorts := configuredCDPPortsForMode(configuredBrowserMode, req.CdpPort, append(append([]int{}, req.CdpPorts...), phaseManifest.Capabilities.CDPPorts...))

							// Browser mode is configured intent. In auto mode the executor
							// checks CDP live; no resolved cdp/headless result is persisted
							// in the prompt or chat runtime.
							phaseBrowserCfg := browserinstructions.BrowserConfig{}
							if len(phaseConfiguredCDPPorts) > 0 {
								phaseBrowserCfg.CdpPort = phaseConfiguredCDPPorts[0]
							}
							switch configuredBrowserMode {
							case "auto", "cdp", "headless":
								phaseBrowserCfg.HasAgentBrowser = true
							}
							if phaseBrowserCfg.HasAgentBrowser {
								// Replace the ~5-10KB BuildBrowserInstructions block with a
								// one-line pointer. The full guide (API + per-mode behaviors,
								// upload rules, session limits) lives in the builder-reference
								// mega-skill as `browser-usage` and is fetched on demand.
								browserPrompt := "\n## Browser\n\nThis phase has a browser tool configured (mode=" + configuredBrowserMode +
									"). CDP availability is live state and is never stored in this prompt. Before the first browser action, call `agent_browser(command=\"status\", session=\"default\")`, then follow its `effective_mode` and authorized endpoints.\n"
								if !relayChat {
									browserPrompt += " Read `read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/browser-usage.md\"}])` for Builder-specific tab, file, and safety rules.\n"
								}
								if configuredBrowserMode == "auto" || configuredBrowserMode == "cdp" {
									_, endpointGuidance := cdpPromptEndpoints(phaseConfiguredCDPPorts, phaseBrowserCfg.CdpPort)
									browserPrompt += endpointGuidance + " These are configured candidates only; `agent_browser status` is authoritative for current reachability.\n"
								}
								phaseAdditions = append(phaseAdditions, browserPrompt)
								log.Printf("[WORKFLOW_PHASE] Appended dynamic browser pointer to %s (configured_mode=%s, candidate_cdp_ports=%v)",
									workflowPhaseID, configuredBrowserMode, phaseConfiguredCDPPorts)
							}

						}
					}
				}

				// Workflow references are already included exactly once by the shared
				// assembler. UI guidance follows the same caller policy as registration.
				activeForPrompt, _ := api.getActiveSession(sessionID)
				promptCtx.WorkflowMode = phaseTemplateVars["WorkshopMode"]
				promptCtx.WorkflowUIAvailable = workflowUICallerAllowed(workflowPhaseID, sessionID, req, activeForPrompt)
				// Workflow browser configuration is authoritative; do not add the
				// generic chat browser pointer as well.
				promptCtx.BrowserPointer = ""
				phaseSystemPrompt, phaseIncluded, phaseSkipped, phasePromptErr := buildWorkflowPhaseSystemPrompt(workflowPhaseID, phaseTemplateVars, promptCtx, phaseAdditions...)
				if phasePromptErr != nil {
					sendError(fmt.Sprintf("Failed to assemble the system prompt for phase %s: %v", workflowPhaseID, phasePromptErr), true)
					return
				}
				if workflowCLIIsolationEnabled() && isCodingAgentProvider(finalProvider, finalModelID) {
					phaseSystemPrompt += workflowCLIWorkspaceInstructions(phaseWorkspacePath)
				}
				if err := llmAgent.ResetInstructions(phaseSystemPrompt); err != nil {
					sendError(fmt.Sprintf("Failed to set the system prompt for phase %s: %v", workflowPhaseID, err), true)
					return
				}
				logPromptAssembly(promptCtx, phaseIncluded, phaseSkipped)
				log.Printf("[WORKFLOW_PHASE] Set assembled system prompt (%d bytes) for phase=%s", len(phaseSystemPrompt), workflowPhaseID)

				// Register phase-appropriate tools via the shared helper. The
				// chat-history auto-restore path calls the same helper with a
				// synthesized request, and both now install the identical
				// surface — there is no per-turn narrowing for one path to
				// apply and the other to miss.
				syntheticReq := QueryRequest{
					ExternalBuilderOperationID:  req.ExternalBuilderOperationID,
					TriggeredBy:                 req.TriggeredBy,
					ParentSessionID:             req.ParentSessionID,
					SessionKind:                 req.SessionKind,
					BotPlatform:                 req.BotPlatform,
					AgentProfileID:              req.AgentProfileID,
					IsAutoNotification:          req.IsAutoNotification,
					UserInteractiveContinuation: req.UserInteractiveContinuation,
					PulseLifecycleTurn:          req.PulseLifecycleTurn,
					LLMConfig:                   req.LLMConfig,
					ExecutionOptions:            req.ExecutionOptions,
					SelectedGlobalSecrets:       req.SelectedGlobalSecrets,
					DecryptedSecrets:            req.DecryptedSecrets,
					PresetQueryID:               req.PresetQueryID,
				}
				if err := api.installWorkflowPhaseTools(
					setupCtx, externalBuilderRegistrar(llmAgent, GetUserFromContext(r.Context())), sessionID, currentUserID,
					workflowPhaseID, phaseWorkspacePath, phaseRunFolder,
					phaseTemplateVars, selectedServers, mergedAPIKeys,
					phaseReadFile, phaseWriteFile, phaseMoveFile,
					syntheticReq, currentUserIsReadOnly,
				); err != nil {
					// Preserve the original Fatal semantics for the /api/query
					// caller: the workflow-builder system prompt advertises the
					// plan modification tools, so a half-registered builder
					// silently hallucinates missing tools to the LLM.
					log.Fatalf("[WORKFLOW_PHASE] FATAL: phase tools install failed: %v", err)
				}

				log.Printf("[WORKFLOW_PHASE] Phase chat setup complete: phase=%s workspace=%s", workflowPhaseID, phaseWorkspacePath)
			}
		}

		// Secret values stay in the tool environment; only their names belong in
		// the immutable identity. Assemble that section before finalization.
		identitySecrets := mergeGlobalSecrets(req.DecryptedSecrets, req.SelectedGlobalSecrets)
		if len(identitySecrets) > 0 && req.PhaseID == "" {
			if underlyingAgent := llmAgent.GetUnderlyingAgent(); underlyingAgent != nil {
				_ = llmAgent.AddInstructions(buildSecretNamesPrompt(identitySecrets))
			}
		}

		// Observers are runtime construction inputs, so attach them to the draft
		// before finalization instead of mutating the live agent afterward.
		eventObserver := events.NewEventObserverWithLogger(api.eventStore, sessionID, queryLogger)
		// Scope resolution, in priority order (PLAT-088):
		//  1. The scheduler's explicit pulse_lifecycle_turn flag, with the older
		//     llm_config_source marker retained as compatibility. Pulse may use
		//     the Builder model, so model selection is not an attribution signal.
		//  2. Otherwise the agent mode + phase. req.AgentMode is rewritten to
		//     "multi-agent" far above purely to route workflow_phase requests
		//     down the standard agent path, so inferring from it directly
		//     charged every scheduled workflow AND Pulse turn to "chat".
		//     isWorkflowPhase is captured before that rewrite and is what the
		//     inference is supposed to see.
		costScope := scopeForScheduledTurn(req.PulseLifecycleTurn, req.LLMConfigSource)
		if costScope == "" {
			agentModeForScope := req.AgentMode
			if isWorkflowPhase {
				agentModeForScope = "workflow_phase"
			}
			costScope = inferCostScope(agentModeForScope, workflowPhaseID)
		}
		costObs := newCostObserver(
			api.costLedger,
			sessionID,
			currentUserID,
			req.AgentMode,
			withCostModel(finalProvider, finalModelID),
			costobserver.WithAccount(costobserver.AccountIDFor(finalProvider, func() string { _, id := queryTurnConnection(req); return id }())),
			withCostAttribution(
				costScope,
				costFirstNonEmpty(workflowPhaseFolder, req.SelectedFolder),
				workflowPhaseRunFolder,
				queryID,
			),
			withCostSourcePlatform(req.BotPlatform),
		)
		if err := llmAgent.AddObserver(eventObserver); err != nil {
			sendError(fmt.Sprintf("Failed to attach event observer: %v", err), true)
			return
		}
		if err := llmAgent.AddObserver(costObs); err != nil {
			sendError(fmt.Sprintf("Failed to attach cost observer: %v", err), true)
			return
		}

		// Freeze the incrementally assembled chat identity and runtime before any
		// turn can run. Finalization constructs the sole live Agent instance.
		if err := llmAgent.FinalizeDefinition(streamCtx); err != nil {
			sendError(fmt.Sprintf("Failed to finalize agent definition: %v", err), true)
			return
		}

		// Register the finalized instance for steering and lifecycle management.
		var registeredRunningAgent *mcpagent.Agent
		if underlyingAgent := llmAgent.GetUnderlyingAgent(); underlyingAgent != nil {
			api.runningAgentsMux.Lock()
			api.runningAgents[sessionID] = underlyingAgent
			api.runningAgentsMux.Unlock()
			registeredRunningAgent = underlyingAgent
		} else {
			log.Printf("[AGENT] ERROR: underlying MCP agent is nil for session %s", sessionID)
		}

		// Detect workshop-mode toggle since the previous turn. If the mode
		// changed, the saved CLI session IDs (gemini, claudeCode) are now
		// stale — they'd resume into a session whose system prompt and
		// allow-list reflect the previous mode. Drop them, then replace the
		// agent-replay history with a small pointer to the persisted conversation
		// JSON so the new mode can read previous context on demand.
		//
		// Source: req.ExecutionOptions.WorkshopMode is the frontend-supplied
		// mode override; phaseTemplateVars (where the workflow branch above
		// stores the resolved mode) is out of scope here. Apply the same
		// legacy-value migration as that branch so old values from saved
		// sessions / stale schedule entries don't trigger spurious changes.
		newWorkshopMode := ""
		if req.ExecutionOptions != nil {
			newWorkshopMode = normalizeChatHistoryWorkshopMode(req.ExecutionOptions.WorkshopMode)
		}
		if newWorkshopMode == "" && isWorkflowPhase {
			newWorkshopMode = "workshop"
		}
		if isWorkflowPhase && currentUserIsReadOnly {
			newWorkshopMode = "run"
		}
		modeChangedThisTurn := false
		modeChangePrevMode := ""
		modeChangeConversationPath := ""
		// Snapshot of the pre-mode-change history. When the user toggles mode
		// mid-session we replace agent context with a bounded text handoff
		// (so stale tool calls/prompts do not replay into the new mode), but the on-disk record should keep the full conversation.
		// This snapshot is merged with the new turn's exchange at save time below.
		var preModeChangeSnapshot []llmtypes.MessageContent
		if newWorkshopMode != "" {
			activeForPolicy, _ := api.getActiveSession(sessionID)
			currentPolicy := resolveWorkflowChatPolicy(sessionID, req, activeForPolicy, currentUserIsReadOnly)
			policyWorkflowKind := ""
			if relayChat {
				policyWorkflowKind = "relay"
			}
			policyKey := api.chatPolicySessionKey(currentPolicy, policyWorkflowKind)
			policyRoleKey := currentPolicy.sessionKey()
			codingProvider := common.IsCLIProvider(finalProvider)
			api.conversationMux.RLock()
			_, knownInMemory := api.lastChatPolicyBySession[sessionID]
			api.conversationMux.RUnlock()
			var savedRuntime *ChatHistoryAgentRuntime
			if codingProvider && !knownInMemory {
				var readErr error
				savedRuntime, _, readErr = ReadChatHistoryRuntimeForSession(currentUserID, sessionID, workflowPhaseFolder)
				if readErr != nil {
					logfWithContext(queryLogCtx, "[CHAT_POLICY] Could not read saved session policy: %v", readErr)
				}
			}
			api.conversationMux.Lock()
			if api.lastChatPolicyBySession == nil {
				api.lastChatPolicyBySession = make(map[string]string)
			}
			previousPolicy, knownPolicy := api.lastChatPolicyBySession[sessionID]
			api.lastChatPolicyBySession[sessionID] = policyKey
			if api.lastChatPolicyRoleBySession == nil {
				api.lastChatPolicyRoleBySession = make(map[string]string)
			}
			previousRole, knownRole := api.lastChatPolicyRoleBySession[sessionID]
			api.lastChatPolicyRoleBySession[sessionID] = policyRoleKey
			prevMode, hadPrev := api.lastWorkshopModeBySession[sessionID]
			if prevMode == "" && savedRuntime != nil {
				prevMode = savedRuntime.WorkshopMode
			}
			// Keep native continuation when its persisted admission still matches.
			// Fresh chats and API providers must not lose normal history replay.
			// Only a role change (mode, origin, capabilities) replaces the native
			// session. Definition/config drift (chat prompt, MCP or user config)
			// keeps the same conversation: the retained-policy check refuses the
			// stale process, and the relaunch resumes it with the current setup.
			// Exception: a CLI that cannot take new instructions into a resumed
			// session (Muse) gets a fresh session on any drift, carrying the
			// recent dialogue, rather than keep running on the old prompt.
			roleChanged := chatPolicyRoleRequiresReconnect(codingProvider, previousRole, policyRoleKey, knownRole, savedRuntime)
			// Turning the workflow's native agent tools on or off changes what
			// the CLI can do, like Builder <-> Run: start fresh with the recent
			// dialogue instead of resuming a transcript full of the other mode.
			if codingProvider {
				currentTools := normalizeAgentToolsMode(api.lastAgentToolsModeBySession[sessionID])
				if api.lastWorkflowToolsModeBySession == nil {
					api.lastWorkflowToolsModeBySession = make(map[string]string)
				}
				previousTools, knownTools := api.lastWorkflowToolsModeBySession[sessionID]
				if !knownTools && savedRuntime != nil && strings.TrimSpace(savedRuntime.AgentToolsMode) != "" {
					previousTools, knownTools = savedRuntime.AgentToolsMode, true
				}
				api.lastWorkflowToolsModeBySession[sessionID] = currentTools
				if knownTools && normalizeAgentToolsMode(previousTools) != currentTools {
					roleChanged = true
				}
			}
			definitionChanged := !codingProviderReloadsInstructionsOnResume(finalProvider) && chatPolicyRequiresReconnect(codingProvider, previousPolicy, policyKey, knownPolicy, savedRuntime)
			if roleChanged || definitionChanged || hadPrev && prevMode != "" && prevMode != newWorkshopMode {
				modeChangedThisTurn = true
				modeChangePrevMode = prevMode
				log.Printf("[CHAT_POLICY] Policy refresh for session %s (mode %q -> %q); starting a fresh native coding-agent session with recent conversation context", sessionID, prevMode, newWorkshopMode)
				// Snapshot existing history before replacing — the on-disk persisted
				// record reuses this so the user sees a complete conversation log.
				if existing, ok := api.conversationHistory[sessionID]; ok && len(existing) > 0 {
					preModeChangeSnapshot = make([]llmtypes.MessageContent, len(existing))
					copy(preModeChangeSnapshot, existing)
					delete(api.conversationHistory, sessionID)
				}
			}
			api.lastWorkshopModeBySession[sessionID] = newWorkshopMode
			api.conversationMux.Unlock()
		}
		if modeChangedThisTurn {
			if workflowPhaseFolder != "" {
				if existingPath, ok, err := findWorkflowScopedChatHistoryConversationPath(sessionID, workflowPhaseFolder); err != nil {
					logfWithContext(queryLogCtx, "[WORKSHOP_MODE] Failed to find previous conversation file for mode switch: %v", err)
				} else if ok {
					modeChangeConversationPath = existingPath
					// The process-local cache can be empty after recovery or cache
					// cleanup even though the canonical JSON exists. Load it before
					// switching modes; otherwise the overwrite guard correctly keeps
					// the old file but has no way to append this new turn.
					if len(preModeChangeSnapshot) == 0 {
						if snapshot, readErr := modeChangeHistorySnapshot(currentUserID, existingPath, nil); readErr != nil {
							logfWithContext(queryLogCtx, "[WORKSHOP_MODE] Failed to load previous conversation for mode-change merge: %v", readErr)
						} else if len(snapshot) > 0 {
							preModeChangeSnapshot = snapshot
						}
					}
				}
				if modeChangeConversationPath == "" && len(preModeChangeSnapshot) > 0 {
					modeChangeConversationPath = stableBuilderConversationLogPath(context.Background(), workflowPhaseFolder, currentUserID, sessionID)
					convData := map[string]interface{}{
						"session_id":           sessionID,
						"user_id":              currentUserID,
						"username":             chatHistoryUsername(currentUserID, ""),
						"phase_id":             workflowPhaseID,
						"workshop_mode":        modeChangePrevMode,
						"conversation_history": cleanChatHistoryForPersistence(preModeChangeSnapshot),
						"updated_at":           time.Now().Format(time.RFC3339),
					}
					if convJSON, err := json.MarshalIndent(convData, "", "  "); err != nil {
						logfWithContext(queryLogCtx, "[WORKSHOP_MODE] Failed to marshal previous conversation snapshot: %v", err)
					} else if err := persistRawConversationSnapshot(context.Background(), modeChangeConversationPath, string(convJSON)); err != nil {
						logfWithContext(queryLogCtx, "[WORKSHOP_MODE] Failed to write previous conversation snapshot to %s: %v", modeChangeConversationPath, err)
					}
				}
			}
			contextHandoff := buildModeChangeConversationContext(modeChangePrevMode, newWorkshopMode, modeChangeConversationPath, preModeChangeSnapshot)
			api.conversationMux.Lock()
			api.conversationHistory[sessionID] = []llmtypes.MessageContent{{
				Role:  llmtypes.ChatMessageTypeHuman,
				Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: contextHandoff}},
			}}
			api.conversationMux.Unlock()

			// Force the running coding-CLI session to relaunch so it picks up
			// the new mode's system prompt. The agent's in-memory prompt is
			// updated below (the phase-prompt assembly block at line ~4885
			// calls SetSystemPrompt with the new template), and
			// captureChatHistoryAgentRuntime will persist it, but the running
			// CLI process loaded its prompt at launch time and won't notice
			// the in-memory change — and the rule file on disk
			// (.agents/rules/mlp-system.md / .cursor/rules/mlp-system.mdc /
			// AGENTS.md / GEMINI.md) isn't rewritten on subsequent turns
			// either. Closing the persistent session here triggers the
			// adapter's cleanup (os.RemoveAll on the provider dir from the
			// earlier RemoveAll patch) and lets the next turn relaunch with
			// the new prompt, producing the correct rule file content.
			//
			// Symmetric across the tmux-backed coding-CLI providers.
			reason := fmt.Sprintf("workflow chat policy changed (mode %q -> %q)", modeChangePrevMode, newWorkshopMode)
			closeWorkflowPolicyCLI(sessionID, finalProvider, reason)
		}

		// Restore the durable transcript into the process-local UI cache. It is
		// deliberately NOT replayed into a coding agent: the agent's own live
		// transport or opaque provider-native continuation handle owns context.
		// Replaying the UI transcript caused repeated restore cycles to grow a
		// coding-agent prompt without bound.
		if !modeChangedThisTurn {
			if target := req.resolvedResumeTarget; target != nil {
				api.restoreResolvedResumeTarget(sessionID, target, newWorkshopMode)
			} else {
				api.restorePersistedConversationHistory(
					sessionID,
					currentUserID,
					workflowPhaseFolder,
					newWorkshopMode,
				)
			}
		}

		// Snapshot the UI history. Whether it becomes fallback agent context is
		// decided only after native coding-agent resume has been resolved.
		var historyForAgent []llmtypes.MessageContent
		api.conversationMux.RLock()
		if history, ok := api.conversationHistory[sessionID]; ok && len(history) > 0 {
			historyForAgent = append([]llmtypes.MessageContent(nil), history...)
			log.Printf("[CONVERSATION] Loaded %d UI-history messages for session %s", len(historyForAgent), sessionID)
		}
		api.conversationMux.RUnlock()

		// Note: User message is added by StreamWithEvents internally, no need to add it here

		log.Printf("[AGENT DEBUG] Starting agent processing for query %s", queryID)

		// Create a cancellable context for agent execution using background context
		// This prevents the agent from being canceled when the HTTP request ends
		agentCtx, agentCancel := context.WithCancel(context.Background())
		agentCtx = queryLogCtx.Context(agentCtx)
		agentCtx = withConversationTurnExecutionID(agentCtx, queryID)

		// Inject user ID into the agent context
		agentCtx = context.WithValue(agentCtx, common.UserIDKey, currentUserID)
		agentCtx = costobserver.ContextWithSourcePlatform(agentCtx, req.BotPlatform)
		agentCtx = context.WithValue(agentCtx, common.ChatSessionIDKey, sessionID)
		if dest := notificationDestinationFromQuery(req, currentUserID); dest != nil {
			virtualtools.RegisterSessionNotificationDestination(sessionID, dest)
			agentCtx = context.WithValue(agentCtx, virtualtools.BotNotificationDestinationKey, dest)
		}
		logfWithContext(queryLogCtx, "[USER_ID_DEBUGGING] Main agent: injected UserIDKey=%q, ChatSessionIDKey=%q into agentCtx", currentUserID, sessionID)

		// Store the cancel function for potential cancellation
		api.agentCancelMux.Lock()
		api.agentCancelFuncs[sessionID] = agentCancel
		api.agentCancelMux.Unlock()

		// Merge global secrets with user-supplied secrets, then inject into system prompt (not user message)
		chatQuery := req.Query

		// Skip secret prompt injection for workflow phases — they inject secrets in the phase setup above.
		// Only inject here for non-workflow chat agents (multi-agent, plain chat, etc.)
		//
		// NOTE: this SHADOWS the outer isWorkflowPhase and derives it from a
		// DIFFERENT basis — req.PhaseID != "" here, vs req.AgentMode ==
		// "workflow_phase" at the top of handleQuery. They usually agree but can
		// diverge (e.g. a request with PhaseID set but a non-workflow_phase mode).
		// Keep that in mind before relying on either one in this block.
		isWorkflowPhase := req.PhaseID != ""
		allChatSecrets := mergeGlobalSecrets(req.DecryptedSecrets, req.SelectedGlobalSecrets)
		if len(allChatSecrets) > 0 && !isWorkflowPhase {
			// Inject secret values as environment variables for shell execution (SECRET_ prefix)
			if workspaceEnv == nil {
				workspaceEnv = make(map[string]string, len(allChatSecrets))
			}
			for _, s := range allChatSecrets {
				workspaceEnv["SECRET_"+s.Name] = s.Value
			}
			logfWithContext(queryLogCtx, "[SECRETS] Injected %d secrets as environment variables for shell execution", len(allChatSecrets))

			logfWithContext(queryLogCtx, "[SECRETS] Injected %d secret names (not values) into immutable agent definition", len(allChatSecrets))
		}

		replayHistoryToAgent := true
		codingFallbackConversationPath := ""
		codingFallbackWorkspace := ""
		codingFallbackHistoryBase := -1
		codingFallbackInjectedMessages := 0
		if underlyingAgent := llmAgent.GetUnderlyingAgent(); underlyingAgent != nil {
			restoredNativeCodingResume := false
			restoredConversationPath := strings.TrimSpace(req.RestoredConversationPath)
			restoredConversationSessionID := strings.TrimSpace(req.RestoredConversationSessionID)
			restoredConversationWorkspace := strings.TrimSpace(workflowPhaseFolder)
			if restoredConversationWorkspace == "" {
				// Product-owned chats resolve SelectedFolder server-side. Work keeps
				// its transcripts inside that project, so use the trusted project
				// workspace when locating the native provider resume handle.
				restoredConversationWorkspace = productConversationRuntimeWorkspace(currentUserID, req.SelectedFolder)
			}
			restoredConversationPathForFallback := restoredConversationPath
			var restoredRuntime *ChatHistoryAgentRuntime
			if modeChangedThisTurn {
				// A role change (Builder <-> Run) must never seed the previous native session handle.
			} else if target := req.resolvedResumeTarget; target != nil {
				restoredConversationPathForFallback = target.ConversationPath
				restoredRuntime = target.Runtime
				restoredNativeCodingResume = api.seedCodingAgentRuntimeFromRestoredConversation(sessionID, finalProvider, newWorkshopMode, restoredRuntime, underlyingAgent)
			} else if runtime, ok, err := ReadChatHistoryRuntimeFromPath(currentUserID, restoredConversationPath); err != nil {
				logfWithContext(queryLogCtx, "[CHAT_HISTORY] Failed to read restored runtime from %s: %v", restoredConversationPath, err)
			} else if ok {
				restoredRuntime = runtime
				restoredNativeCodingResume = api.seedCodingAgentRuntimeFromRestoredConversation(sessionID, finalProvider, newWorkshopMode, restoredRuntime, underlyingAgent)
			}
			if !modeChangedThisTurn && req.resolvedResumeTarget == nil && !restoredNativeCodingResume && restoredConversationPath == "" && restoredConversationSessionID != "" {
				if runtime, ok, err := ReadChatHistoryRuntimeForSession(currentUserID, restoredConversationSessionID, restoredConversationWorkspace); err != nil {
					logfWithContext(queryLogCtx, "[CHAT_HISTORY] Failed to read restored runtime for session %s: %v", restoredConversationSessionID, err)
				} else if ok {
					restoredRuntime = runtime
					restoredNativeCodingResume = api.seedCodingAgentRuntimeFromRestoredConversation(sessionID, finalProvider, newWorkshopMode, restoredRuntime, underlyingAgent)
				}
				if restoredConversationPathForFallback == "" {
					if path, ok, err := FindChatHistoryConversationPathForSession(currentUserID, restoredConversationSessionID, restoredConversationWorkspace); err != nil {
						logfWithContext(queryLogCtx, "[CHAT_HISTORY] Failed to find restored conversation path for session %s: %v", restoredConversationSessionID, err)
					} else if ok {
						restoredConversationPathForFallback = path
					}
				}
			}
			if !restoredNativeCodingResume && !modeChangedThisTurn && restoredConversationPath == "" && restoredConversationSessionID == "" {
				if seeded, currentRuntime := api.seedCodingAgentRuntimeFromCurrentConversation(sessionID, currentUserID, finalProvider, newWorkshopMode, restoredConversationWorkspace, underlyingAgent); seeded {
					restoredNativeCodingResume = true
					// Active-tab auto-resume after idle/reap: this same session (the
					// current tab — NOT an explicit Resume, so RestoredConversationPath is
					// empty) has a recoverable native-resume handle, but its live tmux is
					// gone (the 3h idle reaper closed it and MarkStale'd the snapshot).
					// Adopt the recovered runtime as restoredRuntime so the FIX B block
					// below re-launches the coding agent with --resume AND re-materializes
					// the live tmux terminal — exactly like an explicit Resume — so the
					// next turn continues the session (context preserved) and the stale
					// terminal flips live, instead of running against a dead pane. Gated on
					// "no live tmux" so a genuinely live session is never disrupted by a
					// spurious relaunch (its existing pane keeps streaming via the seeded
					// PiSessionID/--resume without a re-launch).
					//
					// PLAT-130: also gated on isSessionMarkedStopped. A stopped session's
					// tmux is gone because Stop just killed it, not because it idled out —
					// relaunching here reproduced the "Stop doesn't stop" bug live: a
					// message_sequence's next item was already in this same handleQuery
					// call when Stop landed, saw "tmux is gone", and this block relaunched
					// a fresh coding-agent session that ran a real run_full_workflow turn
					// to completion after the session had already been marked stopped
					// twice. A genuine user resume already cleared isSessionMarkedStopped
					// earlier in this same request (see the clearSessionStopped call
					// above), so this check cannot block a real resume — only an internal
					// continuation of a session nobody has un-stopped yet.
					if currentRuntime != nil && !api.sessionHasLiveMainCodingTmux(sessionID) && !api.isSessionMarkedStopped(sessionID) {
						restoredRuntime = currentRuntime
						if forceStructuredCodingAgent {
							logfWithContext(queryLogCtx, "[CHAT_HISTORY] Active-tab auto-resume: session %s is routing through native structured continuation", sessionID)
						} else {
							logfWithContext(queryLogCtx, "[CHAT_HISTORY] Active-tab auto-resume: session %s tmux is gone; routing through --resume re-launch + materialize", sessionID)
						}
					} else if currentRuntime != nil && api.isSessionMarkedStopped(sessionID) {
						logfWithContext(queryLogCtx, "[CHAT_HISTORY] Active-tab auto-resume: session %s is stopped; refusing to relaunch its coding-agent tmux (PLAT-130)", sessionID)
					}
				}
			}
			// Materialize guard: a tmux-transport coding-agent turn must ALWAYS have a
			// live, registered terminal — regardless of whether this turn entered via
			// /api/query or via live-input → startNextTurnFromLiveInput.
			// seedCodingAgentRuntimeFromCurrentConversation bails (seeded=false,
			// runtime=nil) when the in-memory agent already carries a native-resume
			// handle, so the seed block above never adopts a restoredRuntime and the
			// FIX B re-launch+materialize below is skipped. That's wrong once the live
			// tmux is gone: the CLI pane exited after completing its previous turn (or
			// was idle-reaped) and the frontend, seeing STALE liveness, routed this
			// follow-up to live-input. The replayed turn would then run "headless" —
			// setup + tools register, but no terminal is materialized — leaving a blank
			// screen and an invisible agent response. Adopt the on-disk runtime and
			// route through the SAME FIX B re-launch + materialize as an explicit
			// Resume. Gated on a native-resume handle (it IS a coding agent mid-session)
			// AND "no live tmux" so a genuinely live session is never disrupted by a
			// spurious relaunch. Also gated on isSessionMarkedStopped, same PLAT-130
			// reasoning as the auto-resume block above — this is the second of the two
			// blocks that reproduced the live "Stop doesn't stop" incident.
			if !forceStructuredCodingAgent && !restoredNativeCodingResume && !modeChangedThisTurn && restoredRuntime == nil &&
				codingAgentHasNativeResume(finalProvider, underlyingAgent) && !api.sessionHasLiveMainCodingTmux(sessionID) &&
				!api.isSessionMarkedStopped(sessionID) {
				if runtime, ok, err := ReadChatHistoryRuntimeForSession(currentUserID, sessionID, restoredConversationWorkspace); err != nil {
					logfWithContext(queryLogCtx, "[CHAT_HISTORY] Materialize guard: failed to read runtime for session %s: %v", sessionID, err)
				} else if ok && runtime != nil && workflowCLIResumeAllowed(underlyingAgent, runtime) && restoredRuntimeUsesLaunchableTerminalTransport(runtime) {
					restoredRuntime = runtime
					restoredNativeCodingResume = true
					logfWithContext(queryLogCtx, "[CHAT_HISTORY] Materialize guard: session %s tmux is gone but agent has a native-resume handle; re-launching + materializing terminal so the next turn is visible", sessionID)
				}
			}
			if restoredNativeCodingResume {
				if !forceStructuredCodingAgent && restoredRuntimeUsesLaunchableTerminalTransport(restoredRuntime) {
					if handle, err := mcpagent.StartAgentTransportSession(agentCtx, underlyingAgent); err != nil {
						logfWithContext(queryLogCtx, "[CHAT_HISTORY] Failed to prelaunch restored coding-agent transport session: %v", err)
						// PLAT-067. This turn needs a launchable terminal transport and its
						// pane is already gone; StartAgentTransportSession IS the verify +
						// single-replacement attempt (it relaunches with --resume and waits
						// for a ready prompt). Its failure means there is no transport to
						// talk to, so this error must stop the turn rather than be logged
						// and stepped over. Streaming anyway sends the turn into a dead
						// pane where it produces nothing and sits until cancellation:
						// observed on an RTS Latency cron run as two consecutive ~32-minute
						// turns, which consumed the run's budget and killed every producing
						// step scheduled after them. Failing here also preserves any queued
						// background-child completion, so recovery retries only the parent
						// continuation and never re-runs a child that already succeeded.
						sendError(fmt.Sprintf("parent_transport_unavailable: could not restore the coding-agent terminal for this session: %v", err), true)
						return
					} else if handle != nil && strings.TrimSpace(handle.TmuxSession) != "" {
						// FIX B: After a server restart the original tmux is dead, so the
						// restore path published a STATIC snapshot (Active:false, empty
						// TmuxSession). The prelaunch above just started a NEW live tmux
						// session via --resume, so the new tmux session id is only now
						// known. Register it on the terminal store under the canonical
						// main-agent terminalID — the same registration a fresh chat and
						// the attach-existing restore tier use — so the snapshot the
						// frontend reads (GET /api/terminals) flips to Active:true with
						// the live tmux_session. Without this the frontend skips /resize
						// (no tmux_session → tmux stays 120, text overflow + spinner
						// geometry) and the append-only pipe recorder never engages
						// (it needs snapshot.Active), so content=history churns the xterm.
						newTmuxSession := strings.TrimSpace(handle.TmuxSession)
						if _, started, reason := api.materializeRestoredTmuxTerminal(agentCtx, sessionID, restoredRuntime, newTmuxSession); started {
							logfWithContext(queryLogCtx, "[CHAT_HISTORY] Registered relaunched restored coding-agent tmux as live session=%s tmux=%s", sessionID, newTmuxSession)
						} else if reason != "" {
							logfWithContext(queryLogCtx, "[CHAT_HISTORY] Could not register relaunched restored tmux session=%s tmux=%s reason=%s", sessionID, newTmuxSession, reason)
						}
					}
				}
				cleanedChatQuery := cleanChatHistoryQuery(chatQuery)
				if cleanedChatQuery != chatQuery {
					chatQuery = cleanedChatQuery
					logfWithContext(queryLogCtx, "[CHAT_HISTORY] Using native coding-agent resume; stripped restored conversation attach context from prompt")
				}
			} else if req.resolvedResumeTarget != nil && req.resolvedResumeTarget.crossesProvider(finalProvider) && restoredConversationPathForFallback != "" {
				// Provider-native handles cannot cross CLI implementations. Keep the
				// AgentWorks conversation stable and make the newly selected coding
				// provider read the same trusted, project-owned archive used for a
				// same-provider profile reconnect.
				if isCodingAgentProvider(finalProvider, finalModelID) {
					chatQuery = prependCodingAgentContinuityNotice(
						chatQuery,
						restoredConversationPathForFallback,
						restoredConversationWorkspace,
					)
				} else {
					chatQuery = appendRestoredConversationContext(chatQuery, restoredConversationPathForFallback)
				}
				replayHistoryToAgent = false
				logfWithContext(queryLogCtx, "[CHAT_HISTORY] Resuming across providers saved=%s current=%s with required complete conversation archive %s", req.resolvedResumeTarget.provider(), strings.ToLower(strings.TrimSpace(finalProvider)), restoredConversationPathForFallback)
			} else if restoredConversationPathForFallback != "" {
				// A conversation JSON is UI state. Never attach it as an ad-hoc
				// prompt file: native resume is preferred, and an unavailable native
				// resume falls back to the bounded in-memory tail below.
				chatQuery = cleanChatHistoryQuery(chatQuery)
				logfWithContext(queryLogCtx, "[CHAT_HISTORY] Native resume unavailable; refusing unbounded conversation-file prompt fallback")
			}

			if isCodingAgentProvider(finalProvider, finalModelID) &&
				// A pane launched with an older definition does not own the context
				// after a definition change; a seeded native resume does.
				(restoredNativeCodingResume || codingAgentHasNativeResume(finalProvider, underlyingAgent) || (!productDefinitionRefreshed && api.sessionHasLiveMainCodingTmux(sessionID))) {
				replayHistoryToAgent = false
				logfWithContext(queryLogCtx, "[CONVERSATION] Coding-agent context is owned by its transport/native continuation; UI history will not be replayed")
			}
			codingFallbackConversationPath = restoredConversationPathForFallback
			codingFallbackWorkspace = restoredConversationWorkspace
		}

		if len(historyForAgent) > 0 && replayHistoryToAgent {
			historyToReplay := historyForAgent
			if isCodingAgentProvider(finalProvider, finalModelID) {
				codingFallbackHistoryBase = len(llmAgent.GetHistory())
				if codingFallbackConversationPath == "" {
					if path, ok, err := FindChatHistoryConversationPathForSession(currentUserID, sessionID, codingFallbackWorkspace); err != nil {
						logfWithContext(queryLogCtx, "[CHAT_HISTORY] Could not resolve complete conversation archive for coding-agent fallback: %v", err)
					} else if ok {
						codingFallbackConversationPath = path
					}
				}
				if codingFallbackConversationPath != "" {
					chatQuery = prependCodingAgentContinuityNotice(
						chatQuery,
						codingFallbackConversationPath,
						codingFallbackWorkspace,
					)
					historyToReplay = nil
					logfWithContext(queryLogCtx, "[CONVERSATION] Native coding-agent continuation unavailable; sending visible archive-read instruction with the current user message for conversation archive %s (in-memory history: %d messages)", codingFallbackConversationPath, len(historyForAgent))
				} else {
					// A missing archive is an exceptional durability failure. Preserve
					// enough immediate context to answer rather than starting blind.
					historyToReplay = boundedChatHistoryTail(historyForAgent, maxCodingAgentFallbackMessages, maxCodingAgentFallbackBytes)
					logfWithContext(queryLogCtx, "[CONVERSATION] Complete conversation archive unavailable; replaying emergency bounded fallback of %d/%d UI-history messages", len(historyToReplay), len(historyForAgent))
				}
			} else {
				logfWithContext(queryLogCtx, "[CONVERSATION] Replaying %d in-memory messages for session %s", len(historyToReplay), sessionID)
			}
			for _, msg := range historyToReplay {
				llmAgent.AppendMessage(msg)
				if isCodingAgentProvider(finalProvider, finalModelID) {
					codingFallbackInjectedMessages++
				}
			}
		}

		if modeChangedThisTurn && isCodingAgentProvider(finalProvider, finalModelID) && modeChangeConversationPath != "" {
			chatQuery = prependCodingAgentContinuityNotice(chatQuery, modeChangeConversationPath, workflowPhaseFolder)
		}

		// Store the fully configured agent before streaming starts so ultra-fast background
		// completions (for example scripted fast-path runs) can trigger a synthetic turn
		// immediately. Waiting until the end of the first streamed turn creates a race where
		// the completion loop sees no stored agent and drops the auto-notification.
		{
			api.storeSessionAgent(sessionID, llmAgent)
			log.Printf("[BG AGENT] Stored agent for session %s for synthetic turn reuse (pre-stream)", sessionID)
		}

		// Use the enhanced wrapper to get text chunks - events are handled via EventObserver and polling API
		if resolvedProfile != nil {
			api.conversationMux.Lock()
			if api.launchedAgentProfileKeyBySession == nil {
				api.launchedAgentProfileKeyBySession = make(map[string]string)
			}
			api.launchedAgentProfileKeyBySession[sessionID] = agentProfileSessionKey(resolvedProfile)
			if launchedAgent := llmAgent.GetUnderlyingAgent(); launchedAgent != nil {
				api.agentProfileAdmissions.Store(launchedAgent, api.launchedAgentProfileKeyBySession[sessionID])
				defer api.agentProfileAdmissions.Delete(launchedAgent)
			}
			api.conversationMux.Unlock()
		}
		logfWithContext(queryLogCtx, "[STREAMING_LIFECYCLE] T+%dms | Starting StreamWithEvents | session=%s query=%.80s", time.Since(startTime).Milliseconds(), sessionID, chatQuery)
		textChan, err := llmAgent.StreamWithEvents(agentCtx, chatQuery)
		if err != nil {
			logfWithContext(queryLogCtx, "[AGENT DEBUG] llmAgent.StreamWithEvents() error: %v", err)
			sendError(fmt.Sprintf("Failed to start streaming: %v", err), true)
			return
		}
		logfWithContext(queryLogCtx, "[LATENCY_DEBUG] T+%dms | StreamWithEvents channel opened | queryID=%s", time.Since(startTime).Milliseconds(), queryID)
		log.Printf("[AGENT DEBUG] llmAgent.StreamWithEvents() started successfully for query %s", queryID)

		// Stream response chunks with enhanced error handling
		chunkCount := 0
		streamWaitStartedAt := time.Now()

		log.Printf("[AGENT DEBUG] Entering streaming loop for query %s", queryID)
		log.Printf("[COMPLETION_TRACE] stage=agentworks_stream_wait_started session=%q query=%q", sessionID, queryID)
		for chunk := range textChan {
			log.Printf("[AGENT DEBUG] raw chunk (len=%d): %s", len(chunk), chunk)
			chunkCount++

			// Note: Chunks are processed by the agent internally, no manual accumulation needed

			// Save conversation history incrementally during streaming
			// This ensures we don't lose progress if streaming is stopped mid-way
			api.conversationMux.Lock()
			incrementalHistory := llmAgent.GetHistory()
			if modeChangedThisTurn {
				incrementalHistory = mergeModeChangedChatHistory(preModeChangeSnapshot, incrementalHistory)
			} else if len(historyForAgent) > 0 && !replayHistoryToAgent {
				incrementalHistory = mergeNativeContinuationChatHistory(historyForAgent, incrementalHistory)
			}
			api.conversationHistory[sessionID] = incrementalHistory
			api.conversationMux.Unlock()

			// Check for context cancellation
			select {
			case <-streamCtx.Done():
				turnStatus = trackedExecutionStatusCanceled
				turnError = streamCtx.Err().Error()
				tracer.EndTrace(traceID, map[string]interface{}{
					"status":   "timeout",
					"query_id": queryID,
				})

				// Update active session status to error
				api.updateSessionStatus(sessionID, "error")

				// Emit server-level timeout completion event
				// Create a timeout completion event using UnifiedCompletionEvent
				timeoutEventData := unifiedevents.NewUnifiedCompletionEventWithError(
					"server",              // agentType
					req.AgentMode,         // agentMode
					req.Query,             // question
					"context timeout",     // error message
					time.Since(startTime), // duration
					0,                     // turns
				)

				agentEvent := unifiedevents.NewAgentEvent(timeoutEventData)
				agentEvent.SessionID = sessionID

				serverTimeoutEvent := events.Event{
					ID:        fmt.Sprintf("server_timeout_%s_%d", queryID, time.Now().UnixNano()),
					Type:      string(unifiedevents.EventTypeUnifiedCompletion),
					Timestamp: time.Now(),
					Data:      agentEvent,
					SessionID: sessionID,
				}
				api.eventStore.AddEvent(sessionID, serverTimeoutEvent)
				log.Printf("[SERVER DEBUG] Emitted server timeout completion event for query %s", queryID)
				return
			default:
			}
		}
		log.Printf("[COMPLETION_TRACE] stage=agentworks_stream_closed session=%q query=%q chunks=%d elapsed=%s", sessionID, queryID, chunkCount, time.Since(streamWaitStartedAt).Round(time.Millisecond))
		log.Printf("[STREAMING_LIFECYCLE] StreamWithEvents completed | session=%s chunks=%d duration=%dms", sessionID, chunkCount, time.Since(startTime).Milliseconds())
		log.Printf("[AGENT DEBUG] After streaming loop, streamCtx.Err(): %v", streamCtx.Err())

		// Clean up running agent reference (steer injection no longer possible)
		api.runningAgentsMux.Lock()
		if api.runningAgents[sessionID] == registeredRunningAgent {
			delete(api.runningAgents, sessionID)
		}
		api.runningAgentsMux.Unlock()

		// Final save of conversation history (in case streaming was stopped mid-way)
		// This ensures we capture the final state even if streaming was interrupted.
		// Agent context after a policy reconnect contains a bounded handoff.
		// Both the UI cache and disk must retain the complete conversation.
		finalHistory := llmAgent.GetHistory()
		if !modeChangedThisTurn && codingFallbackHistoryBase >= 0 && codingFallbackInjectedMessages > 0 &&
			codingFallbackHistoryBase+codingFallbackInjectedMessages <= len(finalHistory) {
			// Synthetic archive pointers and emergency replay tails are provider
			// context, not user messages. Strip them for both same-provider
			// reconnects and cross-provider resumes before durable persistence.
			finalHistory = mergeCodingAgentFallbackChatHistory(historyForAgent, finalHistory, codingFallbackHistoryBase, codingFallbackInjectedMessages)
			log.Printf("[CONVERSATION DEBUG] Coding-agent fallback merge: removed %d injected context messages, retained %d prior messages, final history now %d messages for session %s", codingFallbackInjectedMessages, len(historyForAgent), len(finalHistory), sessionID)
		} else if !modeChangedThisTurn && len(historyForAgent) > 0 && !replayHistoryToAgent {
			// Native coding-agent continuation owns model context, so the prior UI
			// transcript was intentionally not replayed into this wrapper. Merge it
			// back only for durable/UI history; otherwise every follow-up replaces
			// the conversation JSON with the latest turn even though the provider
			// itself correctly continued the same conversation.
			finalHistory = mergeNativeContinuationChatHistory(historyForAgent, finalHistory)
			log.Printf("[CONVERSATION DEBUG] Native continuation merge: retained %d prior messages, final history now %d messages for session %s", len(historyForAgent), len(finalHistory), sessionID)
		}
		// What we write to disk. Defaults to finalHistory; if mode changed
		// this turn, drop the synthetic handoff message (index 0) and append
		// the rest to the pre-change snapshot so the persisted file stays
		// the canonical record of the conversation.
		persistedHistory := finalHistory
		if modeChangedThisTurn {
			persistedHistory = mergeModeChangedChatHistory(preModeChangeSnapshot, finalHistory)
			newHistoryCount := len(finalHistory)
			if newHistoryCount > 0 {
				newHistoryCount--
			}
			log.Printf("[CONVERSATION DEBUG] Mode-change merge: persisting %d msgs (snapshot %d + new %d)", len(persistedHistory), len(preModeChangeSnapshot), newHistoryCount)
		}
		// Deliberately NOT bounded: the comment above calls this file the
		// canonical record of the conversation, and trimming it here deletes the
		// oldest messages on every write rather than withholding them. The
		// hazard the bound was added for is the no-resume fallback that feeds
		// this transcript to a provider as prompt context — that path is bounded
		// at its own call site with the much smaller
		// maxCodingAgentFallbackMessages/Bytes, which is lossless because the
		// file still holds everything.
		//
		// The "the coding agent's own conversation is not at risk" argument only
		// covers CLI providers, which keep their own rollout/transcript. This is
		// the generic query path: for an API-backed chat there is no provider
		// transcript behind it, so this file is the only record that exists.
		persistedHistory = cleanChatHistoryForPersistence(persistedHistory)
		api.conversationMux.Lock()
		api.conversationHistory[sessionID] = persistedHistory
		api.conversationMux.Unlock()

		runtimeWorkspacePath := strings.TrimSpace(req.SelectedFolder)
		if isWorkflowPhase && workflowPhaseFolder != "" {
			runtimeWorkspacePath = workflowPhaseFolder
		}
		var chatRuntime *ChatHistoryAgentRuntime
		if underlyingAgent := llmAgent.GetUnderlyingAgent(); underlyingAgent != nil {
			chatRuntime = api.captureChatHistoryAgentRuntime(sessionID, finalProvider, finalModelID, runtimeWorkspacePath, underlyingAgent)
			if chatRuntime != nil {
				chatRuntime.WorkshopMode = newWorkshopMode
			}
		}
		var uiEvents []events.Event
		if api.eventStore != nil {
			uiEvents = trimChatHistoryUIEvents(api.eventStore.GetAllEventsRaw(sessionID))
		}

		persistSessionID := sessionID
		persistConversationPath := ""
		persistedHistoryForDisk := persistedHistory
		var restoredPersistTarget *restoredChatHistoryPersistTarget
		var resolvedTarget *restoredChatHistoryPersistTarget
		var resolvedTargetOK bool
		var resolvedTargetErr error
		if req.resolvedResumeTarget != nil {
			resolvedTarget = req.resolvedResumeTarget.persistTarget()
			// Product resume keeps one AgentWorks conversation even when its CLI
			// provider changes. The next save replaces the runtime metadata with the
			// new provider's handle while preserving the original transcript path.
			resolvedTargetOK = req.resolvedResumeTarget.canPersist(newWorkshopMode)
			if resolvedTargetOK {
				api.rememberRestoredConversationPersistTarget(sessionID, *resolvedTarget)
			}
		} else {
			resolvedTarget, resolvedTargetOK, resolvedTargetErr = api.resolveRestoredCodingConversationPersistTarget(
				currentUserID,
				sessionID,
				req.RestoredConversationPath,
				req.RestoredConversationSessionID,
				firstNonEmptyTrimmed(workflowPhaseFolder, normalizeConversationWorkspace(req.SelectedFolder)),
				finalProvider,
				newWorkshopMode,
			)
		}
		if resolvedTargetErr != nil {
			logfWithContext(queryLogCtx, "[CHAT_HISTORY] Failed to resolve restored coding-agent persistence target: %v", resolvedTargetErr)
		} else if resolvedTargetOK && resolvedTarget != nil {
			restoredPersistTarget = resolvedTarget
			persistSessionID = resolvedTarget.SessionID
			persistConversationPath = resolvedTarget.ConversationPath
			// Also deliberately unbounded — and the merge makes it sharper: this
			// combines the restored conversation with the current turn, so a
			// bound here would trim the very history the user just reopened,
			// permanently, on the first message they send into it.
			persistedHistoryForDisk = mergeRestoredChatHistory(resolvedTarget.History, persistedHistory)
			logfWithContext(queryLogCtx, "[CHAT_HISTORY] Continuing restored coding-agent conversation current_session=%s persisted_session=%s path=%s merged_messages=%d",
				sessionID, persistSessionID, persistConversationPath, len(persistedHistoryForDisk))
		}

		botHistoryMeta := chatHistoryBotMetadataFromQuery(req)

		// Work uses the same workspace-owned transcript layout as AgentWorks.
		// When an older centrally stored chat is resumed, seed the local file first
		// so its durable UI events and terminal snapshot survive the move.
		if workConversationPath, ok := workProjectChatHistoryConversationPath(currentUserID, runtimeWorkspacePath, persistSessionID, time.Now()); ok {
			if err := copyChatHistoryConversationIfNeeded(persistConversationPath, workConversationPath); err != nil {
				logfWithContext(queryLogCtx, "[CHAT_HISTORY] Failed to move Work conversation into project workspace: %v", err)
			} else {
				persistConversationPath = workConversationPath
				if restoredPersistTarget != nil {
					restoredPersistTarget.ConversationPath = workConversationPath
					api.rememberRestoredConversationPersistTarget(sessionID, *restoredPersistTarget)
				}
			}
		}

		// Persist ordinary personal/product chats to global chat_history, except
		// Work projects which use the project-owned path selected above. Workflow
		// conversations are persisted below in their workspace Builder folder.
		if !isWorkflowPhase {
			api.persistChatConversationToPathWithTerminalSession(persistSessionID, sessionID, req.AgentMode, currentUserID, persistedHistoryForDisk, chatRuntime, uiEvents, persistConversationPath, botHistoryMeta)
			if isCrewReaderTurn(req, currentUserID) {
				mirrorCrewReaderConversation(currentUserID, req.SelectedFolder, persistSessionID, persistConversationPath, time.Now())
			}
		}

		// Store resolved workflowPhaseFolder so synthetic turns can persist builder conversations
		if isWorkflowPhase && workflowPhaseFolder != "" {
			api.sessionWorkspaceMu.Lock()
			api.sessionWorkspaceFolders[sessionID] = workflowPhaseFolder
			api.sessionWorkspaceMu.Unlock()
		}

		// Save builder conversation log + token_usage.json for workflow phase sessions.
		// One file per session — overwrites on each follow-up with the full cumulative history.
		// Resolve workspace-docs root so files are visible in the UI.
		if isWorkflowPhase && workflowPhaseFolder != "" && len(persistedHistoryForDisk) > 0 {
			convData := map[string]interface{}{
				"session_id":           persistSessionID,
				"user_id":              currentUserID,
				"username":             chatHistoryUsername(currentUserID, ""),
				"phase_id":             workflowPhaseID,
				"conversation_history": persistedHistoryForDisk,
				"updated_at":           time.Now().Format(time.RFC3339),
			}
			if newWorkshopMode != "" {
				convData["workshop_mode"] = newWorkshopMode
			}
			if chatRuntime != nil {
				convData["runtime"] = chatRuntime
			}
			applyChatHistoryBotMetadata(convData, botHistoryMeta)
			if terminalSnapshots := api.captureChatHistoryTerminalSnapshots(sessionID, chatRuntime); len(terminalSnapshots) > 0 {
				convData["terminal_snapshots"] = terminalSnapshots
			}
			if len(uiEvents) > 0 {
				convData["ui_events"] = uiEvents
			}
			if convJSON, err := json.MarshalIndent(convData, "", "  "); err == nil {
				logPath := persistConversationPath
				if strings.TrimSpace(logPath) == "" {
					logPath = stableBuilderConversationLogPath(context.Background(), workflowPhaseFolder, currentUserID, persistSessionID)
				}
				// Same guard as the shared persist path: a rebuild with fewer
				// user turns than the file already holds is partial, and
				// writing it destroys the fuller record.
				if lossy, existingTurns, nextTurns := conversationOverwriteWouldLoseTurns(context.Background(), logPath, persistedHistoryForDisk); lossy {
					refuseConversationOverwrite(logPath, existingTurns, nextTurns)
				} else if err := persistRawConversationSnapshot(context.Background(), logPath, string(convJSON)); err != nil {
					log.Printf("[BUILDER LOG] Failed to write conversation log: %v", err)
				} else {
					log.Printf("[BUILDER LOG] Saved conversation log (%d messages) to %s", len(finalHistory), logPath)
					// The workflow history list is served from chat-index.json. Writing
					// the transcript without this leaves the chat on disk but absent
					// from /resume, so the user is offered an older conversation and the
					// agent legitimately has no memory of the one they were just in.
					if err := updatePersistedChatHistoryIndex(currentUserID, persistSessionID, req.AgentMode, persistedHistoryForDisk, chatRuntime, logPath, int64(len(convJSON)), time.Now(), botHistoryMeta); err != nil {
						log.Printf("[BUILDER LOG] Failed to update chat index for %s: %v", logPath, err)
					}
				}
			}

			if underlying := llmAgent.GetUnderlyingAgent(); underlying != nil {
				usage := mcpagent.ReadAgentDiagnostics(underlying).Usage
				promptTokens, completionTokens := usage.PromptTokens, usage.CompletionTokens
				cacheTokens, reasoningTokens, llmCallCount := usage.CacheTokens, usage.ReasoningTokens, usage.LLMCalls
				inputCost, outputCost := usage.InputCostUSD, usage.OutputCostUSD
				reasoningCost, cacheCost, totalCost := usage.ReasoningCostUSD, usage.CacheCostUSD, usage.TotalCostUSD

				fmtM := func(tokens int) string {
					return fmt.Sprintf("%.3fM", float64(tokens)/1_000_000.0)
				}

				phaseKey := workflowPhaseID
				modelUsage := &orchestrator.ModelTokenUsage{
					Provider:          finalProvider,
					InputTokens:       promptTokens,
					OutputTokens:      completionTokens,
					InputTokensM:      fmtM(promptTokens),
					OutputTokensM:     fmtM(completionTokens),
					CacheTokens:       cacheTokens,
					CacheTokensM:      fmtM(cacheTokens),
					CacheReadTokens:   usage.CacheReadTokens,
					CacheReadTokensM:  fmtM(usage.CacheReadTokens),
					CacheWriteTokens:  usage.CacheWriteTokens,
					CacheWriteTokensM: fmtM(usage.CacheWriteTokens),
					ReasoningTokens:   reasoningTokens,
					ReasoningTokensM:  fmtM(reasoningTokens),
					LLMCallCount:      llmCallCount,
					InputCost:         inputCost,
					OutputCost:        outputCost,
					ReasoningCost:     reasoningCost,
					CacheCost:         cacheCost,
					TotalCost:         totalCost,
				}

				workflowRoot := workflowPhaseFolder
				legacyTokenFilePath := filepath.Join(workflowRoot, "token_usage.json")
				tokenFilePath := filepath.Join(workflowRoot, "costs", "phase", "token_usage.json")
				var tokenFile orchestrator.PhaseTokenUsageFile
				// Whether the pre-migration file is actually present. The delete
				// below is a one-time migration cleanup, but it used to run on
				// every write: once the legacy file was gone (or never existed),
				// each turn issued a DELETE that 404'd. deleteWorkspaceFile
				// swallows that, so the only trace was the workspace access log —
				// 15 wasted round-trips in one run, and a real delete failure
				// would have looked identical.
				legacyTokenFileExists := false
				if existingData, exists, err := readFileFromWorkspace(context.Background(), tokenFilePath); err == nil && exists {
					_ = json.Unmarshal([]byte(existingData), &tokenFile)
				} else if existingData, exists, err := readFileFromWorkspace(context.Background(), legacyTokenFilePath); err == nil && exists {
					_ = json.Unmarshal([]byte(existingData), &tokenFile)
					legacyTokenFileExists = true
				}
				now := time.Now()
				runtimeInfo := mcpagent.ReadAgentRuntimeInfo(underlying)
				modelID := strings.TrimSpace(runtimeInfo.EffectiveModelID)
				if modelID == "" {
					modelID = runtimeInfo.LLMConfig.ModelID
				}
				usageSessionKey := phaseUsageCumulativeKey(persistSessionID, queryID, chatRuntime)
				deltaUsage := orchestrator.ApplyCumulativeSessionModelUsageToPhaseTokenUsageFile(&tokenFile, usageSessionKey, phaseKey, modelID, modelUsage, now)

				if tokenJSON, err := json.MarshalIndent(tokenFile, "", "  "); err == nil {
					if err := writeRawFileToWorkspace(context.Background(), tokenFilePath, string(tokenJSON)); err != nil {
						log.Printf("[BUILDER LOG] Failed to write phase token usage: %v", err)
					} else {
						if legacyTokenFileExists {
							if err := deleteWorkspaceFile(context.Background(), legacyTokenFilePath); err != nil {
								log.Printf("[BUILDER LOG] Failed to delete legacy token_usage.json: %v", err)
							}
						}
						turnCost := 0.0
						if deltaUsage != nil {
							turnCost = deltaUsage.TotalCost
						}
						log.Printf("[BUILDER LOG] Updated %s (phase=%s, $%.4f this turn)", tokenFilePath, phaseKey, turnCost)
					}
				}

				dailyTokenFilePath := filepath.Join(workflowRoot, "costs", "phase", "daily", orchestrator.CostDateKey(now)+".json")
				var dailyTokenFile orchestrator.DailyPhaseTokenUsageFile
				if existingData, exists, err := readFileFromWorkspace(context.Background(), dailyTokenFilePath); err == nil && exists {
					_ = json.Unmarshal([]byte(existingData), &dailyTokenFile)
				}
				dailyTokenFile.Date = orchestrator.CostDateKey(now)
				dailyTokenFile.UpdatedAt = now
				if dailyTokenFile.TokenUsage == nil {
					dailyTokenFile.TokenUsage = &orchestrator.PhaseTokenUsageFile{}
				}
				orchestrator.ApplyModelUsageToPhaseTokenUsageFile(dailyTokenFile.TokenUsage, phaseKey, modelID, deltaUsage, now)

				if dailyTokenJSON, err := json.MarshalIndent(dailyTokenFile, "", "  "); err == nil {
					if err := writeRawFileToWorkspace(context.Background(), dailyTokenFilePath, string(dailyTokenJSON)); err != nil {
						log.Printf("[BUILDER LOG] Failed to write daily phase token usage: %v", err)
					}
				}
			}
		}

		// Store agent for reuse by synthetic turns (multi-agent chat and workflow phase chat).
		// The stored agent retains all tools, prompts, observers, and conversation history.
		{
			api.storeSessionAgent(sessionID, llmAgent)
			log.Printf("[BG AGENT] Stored agent for session %s for synthetic turn reuse", sessionID)
		}

		// A stop or runtime failure may close the stream while this goroutine is
		// still unwinding. Preserve that terminal state instead of resurrecting the
		// session as successfully completed.
		if activeSession, exists := api.getActiveSession(sessionID); exists {
			switch normalizeSessionLifecycleStatus(activeSession.Status) {
			case sessionLifecycleFailed:
				turnStatus = trackedExecutionStatusFailed
				turnError = "session reported failure"
				log.Printf("[COMPLETION] Preserving terminal status %q for session %s", activeSession.Status, sessionID)
				tracer.EndTrace(traceID, map[string]interface{}{
					"status": activeSession.Status,
				})
				return
			case sessionLifecycleStopped:
				turnStatus = trackedExecutionStatusCanceled
				turnError = "session was stopped"
				log.Printf("[COMPLETION] Preserving terminal status %q for session %s", activeSession.Status, sessionID)
				tracer.EndTrace(traceID, map[string]interface{}{
					"status": activeSession.Status,
				})
				return
			}
		}
		if api.isSessionMarkedStopped(sessionID) {
			turnStatus = trackedExecutionStatusCanceled
			turnError = "session was stopped"
			log.Printf("[COMPLETION] Session %s has a cancellation guard; skipping completed status", sessionID)
			tracer.EndTrace(traceID, map[string]interface{}{
				"status": "stopped",
			})
			return
		}

		// Update active session status to completed
		turnStatus = trackedExecutionStatusCompleted
		turnError = ""
		log.Printf("[COMPLETION] Updating session %s status to completed", sessionID)
		api.updateSessionStatus(sessionID, "completed")

		// End conversation trace
		tracer.EndTrace(traceID, map[string]interface{}{
			"status": "completed",
		})

		// Note: Completion events are emitted by the underlying agent, no need for server-level events

		log.Printf("[AGENT DEBUG] Query %s completed successfully", queryID)
	}()
}

func (api *StreamingAPI) verifySessionAccess(r *http.Request, sessionID string) error {
	currentUserID := GetUserIDFromContext(r.Context())
	api.activeSessionsMux.RLock()
	activeSession, exists := api.activeSessions[sessionID]
	api.activeSessionsMux.RUnlock()
	if !exists || (currentUserID != "" && activeSession.UserID != "" && activeSession.UserID != currentUserID) {
		return fmt.Errorf("session not found or access denied")
	}

	log.Printf("[SESSION STOP] Workflow session %s not in DB, verified via activeSessions (mode=%s)", sessionID, activeSession.AgentMode)
	return nil
}

// State management functions removed - orchestrator is now stateless

// createServerLogger creates a logger instance for the server
// This logger writes to stdout only to avoid duplication with shell redirection
func createServerLogger() loggerv2.Logger {
	// Force stdout logging by passing empty log file and enableStdout=true
	// This prevents the application from writing to the file directly,
	// allowing the shell script's redirection to handle file logging without duplicates.
	logFile := ""

	// Check for log level from environment variable
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	serverLogger, err := logger.CreateLogger(logFile, logLevel, "text", true)
	if err != nil {
		log.Fatalf("Failed to create server logger: %v", err)
	}
	return serverLogger
}

// --- ACTIVE SESSION MANAGEMENT ---

// scheduledRequestBypassesWorkflowBusy reports whether an incoming
// workflow-builder request is scheduled work that must not be refused because a
// user has a builder chat open.
//
// trackedExecutionBlocksNewWorkflowBuilderChat already encodes the same rule in
// the other direction: a running scheduled execution does not block a user from
// opening a chat. Only that half was applied, so the pairing worked one way —
// a user could always start a chat, but a schedule fired while a chat was open
// was rejected outright (in ~5ms, before any work began) with "Stop the running
// chat before starting a new one", which is not something a cron job can do.
//
// The rule is symmetric by intent: scheduled work uses the workflow-builder
// phase as an implementation detail, not as a real interactive chat, so it
// neither blocks nor is blocked by one.
func scheduledRequestBypassesWorkflowBusy(sessionID, triggeredBy string) bool {
	return isScheduledSessionIdentity(sessionID, triggeredBy)
}

func workflowBusyGuardApplies(sessionID string, req QueryRequest) bool {
	if req.AgentMode != "workflow_phase" ||
		req.PhaseID != workflowtypes.WorkflowStatusWorkflowBuilder ||
		strings.TrimSpace(req.SelectedFolder) == "" ||
		scheduledRequestBypassesWorkflowBusy(sessionID, req.TriggeredBy) {
		return false
	}
	mode := ""
	if req.ExecutionOptions != nil {
		mode = normalizeChatHistoryWorkshopMode(req.ExecutionOptions.WorkshopMode)
	}
	// Bot and scheduled workflow traffic use the workflow-builder phase as a
	// conversational runtime in run mode. The exclusive lock is only for
	// authoring modes that can mutate workflow design state.
	return mode != "run"
}

func isScheduledSessionIdentity(sessionID, triggeredBy string) bool {
	trigger := strings.ToLower(strings.TrimSpace(triggeredBy))
	id := strings.ToLower(strings.TrimSpace(sessionID))
	return trigger == "cron" || trigger == "webhook" ||
		strings.Contains(trigger, "schedule") ||
		strings.HasPrefix(id, "schedule-") ||
		strings.Contains(id, "-schedule-")
}

func agentWorksSessionBlocksNewChat(session *ActiveSessionInfo, userID string) bool {
	if session == nil || session.UserID != userID || normalizeAgentMode(session.AgentMode) != "multi-agent" {
		return false
	}
	if isScheduledSessionIdentity(session.SessionID, session.TriggeredBy) || strings.TrimSpace(session.BotPlatform) != "" {
		return false
	}
	return normalizeSessionLifecycleStatus(session.Status) == sessionLifecycleRunning ||
		session.HasRetainedTmuxSession ||
		session.HasRunningBackgroundAgents ||
		session.NeedsUserInput
}

// claimAgentWorksChatSession atomically checks and reserves the user's one
// interactive AgentWorks root-chat lane. The same session may continue sending
// follow-up messages; a different session is rejected while the lane is live.
func (api *StreamingAPI) claimAgentWorksChatSession(sessionID, userID, query, triggeredBy string) *ActiveSessionInfo {
	if api.eventStore != nil {
		api.eventStore.SetSessionOwner(sessionID, userID)
	}

	defer publishSessionsChanged() // runs after the unlock below
	api.activeSessionsMux.Lock()
	defer api.activeSessionsMux.Unlock()

	for existingID, existing := range api.activeSessions {
		if existingID == sessionID {
			continue
		}
		if agentWorksSessionBlocksNewChat(existing, userID) {
			return cloneActiveSessionInfo(existing)
		}
	}

	now := time.Now()
	createdAt := now
	if existing := api.activeSessions[sessionID]; existing != nil && !existing.CreatedAt.IsZero() {
		createdAt = existing.CreatedAt
		if strings.TrimSpace(triggeredBy) == "" {
			triggeredBy = existing.TriggeredBy
		}
	}
	api.activeSessions[sessionID] = &ActiveSessionInfo{
		SessionID:    sessionID,
		AgentMode:    "multi-agent",
		Status:       "running",
		LastActivity: now,
		CreatedAt:    createdAt,
		Query:        query,
		UserID:       userID,
		TriggeredBy:  triggeredBy,
	}
	return nil
}

// trackActiveSession tracks a new active session
func (api *StreamingAPI) trackActiveSession(sessionID, agentMode, query, userID, botPlatform, triggeredBy, sessionTitle, parentSessionID, sessionKind string) {
	if api.eventStore != nil {
		api.eventStore.SetSessionOwner(sessionID, userID)
	}

	defer publishSessionsChanged() // runs after the unlock below
	api.activeSessionsMux.Lock()
	defer api.activeSessionsMux.Unlock()

	if existing := api.activeSessions[sessionID]; existing != nil {
		if botPlatform == "" {
			botPlatform = existing.BotPlatform
		}
		if triggeredBy == "" {
			triggeredBy = existing.TriggeredBy
		}
		if strings.TrimSpace(sessionTitle) == "" {
			sessionTitle = existing.Title
		}
		if strings.TrimSpace(parentSessionID) == "" {
			parentSessionID = existing.ParentSessionID
		}
		if strings.TrimSpace(sessionKind) == "" {
			sessionKind = existing.SessionKind
		}
	}

	api.activeSessions[sessionID] = &ActiveSessionInfo{
		SessionID:       sessionID,
		ParentSessionID: strings.TrimSpace(parentSessionID),
		SessionKind:     strings.TrimSpace(sessionKind),
		AgentMode:       agentMode,
		Status:          "running",
		LastActivity:    time.Now(),
		CreatedAt:       time.Now(),
		Query:           query,
		Title:           strings.TrimSpace(sessionTitle),
		UserID:          userID,
		BotPlatform:     botPlatform,
		TriggeredBy:     triggeredBy,
	}

	logfWithContext(
		newServerLogContext("", "", agentMode, userID, "", sessionID),
		"[ACTIVE_SESSION] Tracked active session: %s (mode: %s, user: %s)",
		sessionID,
		agentMode,
		userID,
	)
}

func (api *StreamingAPI) captureChatHistoryAgentRuntime(sessionID, provider, modelID, workspacePath string, underlyingAgent *mcpagent.Agent) *ChatHistoryAgentRuntime {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelID = strings.TrimSpace(modelID)
	workspacePath = strings.TrimSpace(workspacePath)
	if provider == "" && modelID == "" && workspacePath == "" && underlyingAgent == nil {
		return nil
	}

	runtime := &ChatHistoryAgentRuntime{
		Kind:          "llm_agent",
		Provider:      provider,
		ModelID:       modelID,
		WorkspacePath: workspacePath,
		CapturedAt:    time.Now().Format(time.RFC3339),
	}
	if common.IsCLIProvider(provider) {
		runtime.Kind = "coding_agent"
	}

	if underlyingAgent != nil {
		if handle := mcpagent.SnapshotAgentSession(underlyingAgent); handle != nil && !handle.Empty() {
			runtime.AgentSessionHandle = handle
			if handle.Provider.Provider != "" && runtime.Provider == "" {
				runtime.Provider = strings.ToLower(strings.TrimSpace(handle.Provider.Provider))
			}
			if handle.Provider.Model != "" && runtime.ModelID == "" {
				runtime.ModelID = strings.TrimSpace(handle.Provider.Model)
			}
			if handle.Provider.Transport != "" && runtime.Transport == "" {
				runtime.Transport = strings.ToLower(strings.TrimSpace(handle.Provider.Transport))
			}
			if common.IsCLIProvider(runtime.Provider) {
				runtime.Kind = "coding_agent"
			}
			if handle.Provider.NativeSessionID != "" {
				runtime.ExternalSessionID = handle.Provider.NativeSessionID
				if codingAgentProviderSupportsNativeResume(runtime.Provider, runtime.ModelID) {
					runtime.ResumeSupported = true
				}
			}
			if handle.Provider.ProjectDirID != "" {
				runtime.ProjectDirID = handle.Provider.ProjectDirID
			}
		}
		provider = runtime.Provider
		if runtime.ExternalSessionID != "" {
			switch provider {
			case "claude-code", "cursor-cli":
				runtime.ResumeFlag = "--resume"
			case "codex-cli":
				runtime.ResumeFlag = "resume"
			case "pi-cli":
				runtime.ResumeFlag = "--session-id"
			}
			log.Printf("[%s] Saved native session ID %s for session %s", provider, runtime.ExternalSessionID, sessionID)
		}
		// Persist stable MCP server/tool selection only. Current prompts and
		// browser availability are rebuilt for each request.
		runtimeInfo := mcpagent.ReadAgentRuntimeInfo(underlyingAgent)
		runtime.ServerName = strings.TrimSpace(runtimeInfo.ConfiguredServerName)
		runtime.SelectedTools = runtimeInfo.SelectedTools
	}
	api.conversationMux.RLock()
	runtime.ChatPolicyKey = api.lastChatPolicyBySession[sessionID]
	runtime.ChatPolicyRoleKey = api.lastChatPolicyRoleBySession[sessionID]
	runtime.AgentProfileKey = api.lastAgentProfileKeyBySession[sessionID]
	runtime.AgentToolsMode = api.lastAgentToolsModeBySession[sessionID]
	api.conversationMux.RUnlock()
	if admitted, ok := api.agentProfileAdmissions.Load(underlyingAgent); ok {
		runtime.AgentProfileKey = admitted.(string)
	}

	normalizeChatHistoryRuntime(runtime)

	return runtime
}

// seedCodingAgentRuntimeFromCurrentConversation recovers this session's own
// persisted native-resume handle (when the in-memory agent is gone — e.g. after a
// long idle) and seeds it onto the fresh /api/query agent. It returns the
// recovered runtime alongside the success flag so the caller can route an
// idled-out active tab through the FIX B re-launch (relaunch with --resume +
// re-materialize the live tmux) instead of starting a fresh agent.
func (api *StreamingAPI) seedCodingAgentRuntimeFromCurrentConversation(sessionID, userID, currentProvider, currentWorkshopMode, workspacePath string, underlyingAgent *mcpagent.Agent) (bool, *ChatHistoryAgentRuntime) {
	if api == nil || underlyingAgent == nil || codingAgentHasNativeResume(currentProvider, underlyingAgent) {
		return false, nil
	}
	runtime, ok, err := ReadChatHistoryRuntimeForSession(userID, sessionID, workspacePath)
	if err != nil {
		log.Printf("[CHAT_HISTORY] Failed to read current conversation runtime for session %s: %v", sessionID, err)
		return false, nil
	}
	if !ok || runtime == nil {
		return false, nil
	}
	seeded := api.seedCodingAgentRuntimeFromRestoredConversation(sessionID, currentProvider, currentWorkshopMode, runtime, underlyingAgent)
	if seeded {
		log.Printf("[CHAT_HISTORY] Restored native coding-agent runtime from current conversation for session %s", sessionID)
		return true, runtime
	}
	return false, nil
}

// restorePersistedConversationHistory hydrates the process-local UI cache for a
// stable project session after a server restart. The transcript is authoritative
// for what the user sees, while a coding provider's native session handle is
// authoritative for agent context. Workflow history is restored only into its
// original Workshop/Run boundary. Callers must never treat this return value as
// a reason to suppress a compatible native resume.
func (api *StreamingAPI) restorePersistedConversationHistory(sessionID, userID, workspacePath, currentWorkshopMode string) bool {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}

	api.conversationMux.RLock()
	_, alreadyLoaded := api.conversationHistory[sessionID]
	api.conversationMux.RUnlock()
	if alreadyLoaded {
		return false
	}

	target, ok, err := readRestoredChatHistoryPersistTargetForSession(userID, sessionID, workspacePath)
	if err != nil {
		log.Printf("[CHAT_HISTORY] Failed to restore durable conversation for session %s: %v", sessionID, err)
		return false
	}
	if !ok || target == nil || len(target.History) == 0 {
		return false
	}
	restoredMode := target.WorkshopMode
	if target.Runtime != nil {
		restoredMode = firstNonEmptyTrimmed(target.Runtime.WorkshopMode, restoredMode)
	}
	if !chatHistoryResumeModesCompatible(restoredMode, currentWorkshopMode) {
		log.Printf("[CHAT_HISTORY] Skipping durable conversation replay for session %s: restored mode=%s current mode=%s", sessionID, normalizeChatHistoryWorkshopMode(restoredMode), normalizeChatHistoryWorkshopMode(currentWorkshopMode))
		return false
	}

	// Recheck under the write lock so simultaneous reconnects cannot replace a
	// history that was populated by the first request.
	history := append([]llmtypes.MessageContent(nil), target.History...)
	api.conversationMux.Lock()
	if _, exists := api.conversationHistory[sessionID]; exists {
		api.conversationMux.Unlock()
		return false
	}
	if api.conversationHistory == nil {
		api.conversationHistory = make(map[string][]llmtypes.MessageContent)
	}
	api.conversationHistory[sessionID] = history
	api.conversationMux.Unlock()

	// Keep writing to the same conversation JSON after recovery. This matters
	// when a session crosses a date boundary and prevents a second transcript
	// from being created for the same project.
	api.rememberRestoredConversationPersistTarget(sessionID, *target)
	log.Printf("[CHAT_HISTORY] Restored %d durable UI messages for project session %s from %s", len(history), sessionID, target.ConversationPath)
	return true
}

// sessionHasLiveCodingTmux reports whether the session currently has a live
// coding-agent terminal. Active is turn state, while ProcessState is process
// state: a retained main CLI is Active=false/ProcessState=live between turns.
// The idle reaper MarkStale's a closed pane and clears TmuxSession, so this still
// returns false once the tmux is genuinely gone.
func (api *StreamingAPI) sessionHasLiveCodingTmux(sessionID string) bool {
	if api == nil || api.terminalStore == nil {
		return false
	}
	for _, snapshot := range api.terminalStore.ListRaw(sessionID) {
		if terminalSnapshotHasLiveTmux(snapshot) {
			return true
		}
	}
	return false
}

// sessionHasLiveMainCodingTmux reports whether the main chat agent has a live
// tmux pane. A child workflow/background pane is aggregate session activity,
// but it cannot receive or preserve main-chat input.
func (api *StreamingAPI) sessionHasLiveMainCodingTmux(sessionID string) bool {
	_, ok := api.liveMainCodingTmuxSnapshot(sessionID)
	return ok
}

// liveMainCodingTmuxSnapshot returns the provider-owned main pane for a chat.
// The terminal is durable across logical turns; the Go Agent instance is not.
func (api *StreamingAPI) liveMainCodingTmuxSnapshot(sessionID string) (terminals.Snapshot, bool) {
	if api == nil || api.terminalStore == nil {
		return terminals.Snapshot{}, false
	}
	for _, snapshot := range api.terminalStore.ListRaw(sessionID) {
		if terminalSnapshotHasLiveTmux(snapshot) && codingAgentSnapshotIsMainAgent(snapshot) {
			return snapshot, true
		}
	}
	return terminals.Snapshot{}, false
}

func (api *StreamingAPI) sessionHasRetainedCodingTmux(sessionID string) bool {
	if api == nil || api.terminalStore == nil {
		return false
	}
	return api.terminalStore.SessionHasRetainedCodingTmux(sessionID)
}

func terminalSnapshotHasLiveTmux(snapshot terminals.Snapshot) bool {
	return strings.TrimSpace(snapshot.TmuxSession) != "" &&
		(snapshot.Active || strings.EqualFold(strings.TrimSpace(snapshot.ProcessState), "live"))
}

// retainedCodingAgentProvider infers the provider from durable terminal data
// when continuation request state has already been cleaned up. The tmux naming
// prefixes are created by the provider adapters and are more reliable than the
// rendered status label; the label remains a compatibility fallback for older
// retained panes.
func retainedCodingAgentProvider(snapshot terminals.Snapshot) string {
	tmuxSession := strings.ToLower(strings.TrimSpace(snapshot.TmuxSession))
	switch {
	case strings.HasPrefix(tmuxSession, "mlp-claude-code"), strings.HasPrefix(tmuxSession, "mlp-claude-"):
		return string(llm.ProviderClaudeCode)
	case strings.HasPrefix(tmuxSession, "mlp-codex-cli"):
		return string(llm.ProviderCodexCLI)
	case strings.HasPrefix(tmuxSession, "mlp-cursor-cli"):
		return string(llm.ProviderCursorCLI)
	case strings.HasPrefix(tmuxSession, "mlp-muse-"):
		return string(llm.ProviderMuseCLI)
	case strings.HasPrefix(tmuxSession, "mlp-pi-cli"):
		return string(llm.ProviderPiCLI)
	case strings.HasPrefix(tmuxSession, "agy-int-"):
		return string(llm.ProviderAgyCLI)
	}

	label := strings.ToLower(strings.TrimSpace(snapshot.Status.ProviderLabel))
	switch {
	case strings.Contains(label, "claude"):
		return string(llm.ProviderClaudeCode)
	case strings.Contains(label, "codex"):
		return string(llm.ProviderCodexCLI)
	case strings.Contains(label, "cursor"):
		return string(llm.ProviderCursorCLI)
	case strings.Contains(label, "muse"):
		return string(llm.ProviderMuseCLI)
	case strings.Contains(label, "pi-cli") || strings.HasPrefix(label, "pi "):
		return string(llm.ProviderPiCLI)
	case strings.Contains(label, "agy-cli"):
		return string(llm.ProviderAgyCLI)
	default:
		return ""
	}
}

// markRetainedMainCodingTurnRunning bridges the process/turn lifecycle when a
// follow-up is submitted directly to an idle retained CLI. This path does not
// bootstrap a fresh /api/query stream, so it must explicitly reactivate the
// existing terminal snapshot and session status after confirmed delivery.
func (api *StreamingAPI) markRetainedMainCodingTurnRunning(sessionID string, executionIDs ...string) {
	api.markRetainedMainCodingTurnRunningWithOwner(sessionID, false, executionIDs...)
}

// markMCPAgentSessionTurnRunning records the host-visible busy lifecycle for a
// retained turn whose completion is owned by mcpagent.Session. Unlike the
// cold-restart compatibility path, it does not start a second provider/tmux
// completion detector in AgentWorks.
func (api *StreamingAPI) markMCPAgentSessionTurnRunning(sessionID string, executionIDs ...string) {
	api.markRetainedMainCodingTurnRunningWithOwner(sessionID, true, executionIDs...)
}

func (api *StreamingAPI) markRetainedMainCodingTurnRunningWithOwner(sessionID string, sessionOwnsCompletion bool, executionIDs ...string) {
	if api == nil || api.terminalStore == nil {
		return
	}
	executionID := ""
	if len(executionIDs) > 0 {
		executionID = strings.TrimSpace(executionIDs[0])
	}
	for _, snapshot := range api.terminalStore.ListRaw(sessionID) {
		if !codingAgentSnapshotIsMainAgent(snapshot) {
			continue
		}
		// A Session acknowledgement proves that the provider accepted this input
		// even when AgentWorks' process_state snapshot is stale. The compatibility
		// path has no Session, so it still requires independent live-tmux proof.
		if !sessionOwnsCompletion && !terminalSnapshotHasLiveTmux(snapshot) {
			continue
		}
		api.terminalStore.MarkTurnRunning(snapshot.TerminalID)
		provider := llmproviders.Provider(retainedCodingAgentProvider(snapshot))
		watchCtx, watchCancel := context.WithCancel(context.Background())
		api.retainedMainTurnsMu.Lock()
		if api.retainedMainTurns == nil {
			api.retainedMainTurns = make(map[string]time.Time)
		}
		if api.retainedMainTurnWatchCancels == nil {
			api.retainedMainTurnWatchCancels = make(map[string]context.CancelFunc)
		}
		if api.retainedMainTurnExecutionIDs == nil {
			api.retainedMainTurnExecutionIDs = make(map[string]string)
		}
		if api.retainedMainTurnPendingExecutionIDs == nil {
			api.retainedMainTurnPendingExecutionIDs = make(map[string][]string)
		}
		if _, alreadyTracked := api.retainedMainTurns[sessionID]; alreadyTracked {
			primaryID := strings.TrimSpace(api.retainedMainTurnExecutionIDs[sessionID])
			if executionID != "" && executionID != primaryID {
				pending := api.retainedMainTurnPendingExecutionIDs[sessionID]
				alreadyPending := false
				for _, pendingID := range pending {
					if pendingID == executionID {
						alreadyPending = true
						break
					}
				}
				// PLAT-178: a pending entry must be an input the CLI has not
				// answered yet. Never queue an execution that already finished.
				if !alreadyPending && !api.trackedExecutionFinished(executionID) {
					api.retainedMainTurnPendingExecutionIDs[sessionID] = append(pending, executionID)
				} else if !alreadyPending {
					log.Printf("[RETAINED_TURN] Not queueing already-finished execution %s behind session=%s", executionID, sessionID)
				}
			}
			api.retainedMainTurnsMu.Unlock()
			watchCancel()
			api.setSessionBusy(sessionID, true)
			api.updateSessionStatus(sessionID, "running")
			return
		}
		previousWatchCancel := api.retainedMainTurnWatchCancels[sessionID]
		api.retainedMainTurns[sessionID] = time.Now()
		api.retainedMainTurnExecutionIDs[sessionID] = executionID
		api.retainedMainTurnWatchCancels[sessionID] = watchCancel
		api.retainedMainTurnsMu.Unlock()
		if previousWatchCancel != nil {
			previousWatchCancel()
		}
		api.setSessionBusy(sessionID, true)
		api.updateSessionStatus(sessionID, "running")
		if sessionOwnsCompletion {
			watchCancel()
		} else {
			go api.observeRetainedMainTurnStream(watchCtx, sessionID, snapshot, provider)
		}
		return
	}
}

const (
	retainedMainTurnStreamQuietWindow = 350 * time.Millisecond
	retainedMainTurnReadyStableWindow = 1200 * time.Millisecond
	retainedMainTurnCaptureTimeout    = 3 * time.Second
	retainedMainTurnRecheckWindow     = time.Second
)

var inspectRetainedMainTurnTmuxState = inspectCodingTmuxPaneState

var (
	// retainedMainTurnDurableQuietWindow is how long the tmux output stream
	// must stay silent before the observer consults the provider's durable
	// turn sidecar. Pane heuristics cannot see idle for every provider (Muse
	// has no PaneReadyForInput signal), but the provider's own retained-turn
	// reader already encodes its completion contract, so a quiet pane plus a
	// durable final response settles the turn on runner outcome instead of
	// pixels. Vars (not consts) so tests can shrink them.
	retainedMainTurnDurableQuietWindow = 15 * time.Second
	// retainedMainTurnDurableRecheckWindow bounds how often the observer
	// consults the sidecar while the pane stays quiet. Each consult can
	// capture a pane and read a transcript, so it stays well below the
	// 1s pane-recheck cadence.
	retainedMainTurnDurableRecheckWindow = 5 * time.Second
	// retainedMainTurnStuckWarnAfter emits one diagnostic line per observer
	// when a turn still has neither pane-idle nor a durable final response.
	// It never settles the turn: marathon turns stay untouched.
	retainedMainTurnStuckWarnAfter = 30 * time.Minute
	// retainedMainTurnStuckFailAfter is the PLAT-178 backstop: an observer
	// that has seen neither pane-idle nor a durable final response for this
	// long fails the turn so the chat lane is released and queued turns run
	// instead of waiting behind it forever.
	retainedMainTurnStuckFailAfter = 30 * time.Minute
	// retainedMainTurnPromotionEvidenceGrace bounds how long a promoted
	// pending input may show no new CLI output before it is treated as already
	// answered by the previous response. CLIs often answer several rapid inputs
	// in one response, which leaves pending entries with no reply of their own.
	retainedMainTurnPromotionEvidenceGrace = 75 * time.Second
	// retainedMainTurnPaneCheckInterval throttles the tmux existence check the
	// observer runs while a turn stays busy.
	retainedMainTurnPaneCheckInterval = 5 * time.Second
)

// retainedMainTurnPaneMissesToFail is how many consecutive missing/dead pane
// checks fail a busy retained turn; one transient tmux miss never does.
const retainedMainTurnPaneMissesToFail = 3

// retainedTurnFinalResponse reads the provider's durable retained-turn
// sidecar for this turn. Providers with retained-turn readers report their
// latest turn text there; providers without one return "". Reading is safe
// for every provider (completion payload, stream-closed reconcile); settling
// a live turn on the sidecar is additionally gated by
// retainedTurnDurableSettleAllowed below.
func (api *StreamingAPI) retainedTurnFinalResponse(provider llmproviders.Provider, sessionID string, turnStartedAt time.Time) string {
	readFinalResponse := retainedturn.FinalResponse
	if api != nil && api.internalRetainedTurnFinalResponseReader != nil {
		readFinalResponse = api.internalRetainedTurnFinalResponseReader
	}
	return readFinalResponse(provider, sessionID, turnStartedAt)
}

// retainedTurnDurableSettleAllowed reports whether a durable final response
// on a quiet pane may settle a live turn for this provider. Only readers
// that assert completion — not just "latest text" — qualify:
//   - Muse: museRetainedTurnReady (log quiet + TUI at prompt + pane stable,
//     pending-input excluded) gates ReadRetainedTurnMessages.
//   - Codex: the rollout phase must be final_answer/final for this turn.
//   - Claude: completedAssistantResponseFromTranscript must report Completed.
//   - Cursor: the idle composer is required in addition to the transcript.
//
// Pi is deliberately excluded: its reader returns all in-turn messages with
// no completion gate, so a question awaiting a slow user would settle early.
// Providers outside this set keep pane-only behavior (fail closed).
func retainedTurnDurableSettleAllowed(provider llmproviders.Provider) bool {
	switch llmproviders.Provider(strings.ToLower(strings.TrimSpace(string(provider)))) {
	case llmproviders.ProviderMuseCLI, llmproviders.ProviderCodexCLI, llmproviders.ProviderClaudeCode, llmproviders.ProviderCursorCLI:
		return true
	default:
		return false
	}
}

// observeRetainedMainTurnStream derives the logical end of a direct retained
// turn from the real tmux output stream. Stream output schedules a coherent
// in-band pane capture after a short quiet boundary; the provider adapter then
// decides whether the screen is truly back at its idle composer. Requiring the
// ready screen to remain stable prevents an intermediate repaint from settling
// a turn that immediately continues into another tool/action. When the pane
// never reports idle but stays silent, the provider's durable turn sidecar is
// consulted instead, so runner outcome — not pixels — settles providers whose
// composer the pane heuristics cannot see (Muse).
func (api *StreamingAPI) observeRetainedMainTurnStream(
	ctx context.Context,
	sessionID string,
	snapshot terminals.Snapshot,
	provider llmproviders.Provider,
) {
	api.observeRetainedMainTurnStreamMode(ctx, sessionID, snapshot, provider, false)
}

// observeRetainedMainTurnStreamMode is observeRetainedMainTurnStream; promoted
// marks a turn promoted from the pending list after an earlier completion,
// which must show new CLI output before it can be trusted as a live turn.
func (api *StreamingAPI) observeRetainedMainTurnStreamMode(
	ctx context.Context,
	sessionID string,
	snapshot terminals.Snapshot,
	provider llmproviders.Provider,
	promoted bool,
) {
	if api == nil || api.liveAttach == nil || strings.TrimSpace(snapshot.TmuxSession) == "" || provider == "" {
		return
	}
	api.retainedMainTurnsMu.Lock()
	turnStartedAt := api.retainedMainTurns[sessionID]
	api.retainedMainTurnsMu.Unlock()
	if turnStartedAt.IsZero() {
		turnStartedAt = time.Now()
	}
	output := make(chan struct{}, 1)
	var realOutputSeen atomic.Bool
	stream, unsubscribe, err := api.liveAttach.observeOutput(snapshot.TmuxSession, func([]byte) {
		realOutputSeen.Store(true)
		select {
		case output <- struct{}{}:
		default:
		}
	})
	if err != nil {
		log.Printf("[RETAINED_TURN] Could not observe tmux stream session=%s terminal=%s: %v", sessionID, snapshot.TerminalID, err)
		return
	}
	defer unsubscribe()

	// Inspect once even when the turn completed faster than stream attachment.
	// Confirmed delivery plus a stable idle composer is a valid completion.
	output <- struct{}{}
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	resetTimer := func(delay time.Duration) {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(delay)
	}

	var readySince time.Time
	observeStartedAt := time.Now()
	lastOutputAt := observeStartedAt
	var lastDurableCheck time.Time
	var lastPaneCheck time.Time
	paneMisses := 0
	warnedStuck := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-stream.done:
			if ctx.Err() != nil {
				return
			}
			api.handleRetainedMainTurnStreamClosed(ctx, sessionID, snapshot, provider)
			return
		case <-output:
			readySince = time.Time{}
			lastOutputAt = time.Now()
			resetTimer(retainedMainTurnStreamQuietWindow)
		case <-timer.C:
			captureCtx, cancel := context.WithTimeout(ctx, retainedMainTurnCaptureTimeout)
			pane, captureErr := stream.capturePane(captureCtx)
			cancel()
			if captureErr != nil {
				if ctx.Err() == nil {
					log.Printf("[RETAINED_TURN] Tmux stream capture failed session=%s terminal=%s: %v", sessionID, snapshot.TerminalID, captureErr)
					api.handleRetainedMainTurnStreamClosed(ctx, sessionID, snapshot, provider)
				}
				return
			}
			if !llmproviders.CodingAgentPaneReady(provider, pane) {
				readySince = time.Time{}
				now := time.Now()
				// The pane heuristics cannot see idle for every provider.
				// Once the stream has gone quiet, a durable final response
				// from the provider's own turn contract settles the turn on
				// runner outcome instead of pixels — but only for readers
				// that assert completion, so an ungated reader (Pi) can never
				// settle a turn that is merely awaiting a slow user.
				if retainedTurnDurableSettleAllowed(provider) &&
					now.Sub(lastOutputAt) >= retainedMainTurnDurableQuietWindow &&
					now.Sub(lastDurableCheck) >= retainedMainTurnDurableRecheckWindow {
					lastDurableCheck = now
					if finalResult := api.retainedTurnFinalResponse(provider, sessionID, turnStartedAt); strings.TrimSpace(finalResult) != "" {
						log.Printf("[RETAINED_TURN] Durable provider sidecar reports completion while pane stays busy; settling session=%s terminal=%s provider=%s quiet=%s", sessionID, snapshot.TerminalID, provider, now.Sub(lastOutputAt).Round(time.Second))
						api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, provider, "completed", "")
						return
					}
				}
				if !warnedStuck && now.Sub(observeStartedAt) >= retainedMainTurnStuckWarnAfter {
					warnedStuck = true
					log.Printf("[RETAINED_TURN] WARNING: turn still observed with no pane-idle and no durable final response session=%s terminal=%s provider=%s elapsed=%s quiet=%s", sessionID, snapshot.TerminalID, provider, now.Sub(observeStartedAt).Round(time.Second), now.Sub(lastOutputAt).Round(time.Second))
				}
				// PLAT-178: a promoted pending input that never produced new CLI
				// output was already answered by the previous response. Settle it
				// (and any backlog behind it) instead of waiting for a reply that
				// will never come.
				if promoted && !realOutputSeen.Load() && now.Sub(observeStartedAt) >= retainedMainTurnPromotionEvidenceGrace {
					api.settleUnansweredRetainedPromotion(sessionID, snapshot, provider, now.Sub(observeStartedAt))
					return
				}
				// PLAT-178: a vanished pane can never report idle or write a new
				// durable response. Fail after consecutive misses.
				if now.Sub(lastPaneCheck) >= retainedMainTurnPaneCheckInterval {
					lastPaneCheck = now
					switch inspectRetainedMainTurnTmuxState(snapshot.TmuxSession) {
					case codingTmuxPaneMissing, codingTmuxPaneDead:
						paneMisses++
					default:
						paneMisses = 0
					}
					if paneMisses >= retainedMainTurnPaneMissesToFail {
						reason := "retained pane gone"
						log.Printf("[RETAINED_TURN] %s after %d consecutive checks; failing turn session=%s terminal=%s tmux=%s", reason, paneMisses, sessionID, snapshot.TerminalID, snapshot.TmuxSession)
						api.reconcileUnexpectedTerminalExit(snapshot, reason)
						api.terminalStore.MarkFailed(snapshot.TerminalID)
						api.terminalStore.MarkProcessClosed(snapshot.TerminalID, reason)
						api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, provider, "failed", reason)
						return
					}
				}
				// PLAT-178 backstop: never hold the chat lane forever.
				if now.Sub(observeStartedAt) >= retainedMainTurnStuckFailAfter {
					reason := fmt.Sprintf("retained coding-agent turn produced no completion within %s; releasing the chat", retainedMainTurnStuckFailAfter)
					log.Printf("[RETAINED_TURN] %s session=%s terminal=%s provider=%s quiet=%s", reason, sessionID, snapshot.TerminalID, provider, now.Sub(lastOutputAt).Round(time.Second))
					// The pane may still be alive, so the terminal is not marked
					// failed; fail the tracked inputs directly so the settle below
					// releases the lane instead of promoting the backlog.
					api.failRetainedTurnExecutions(sessionID, reason)
					api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, provider, "failed", reason)
					return
				}
				// A capture can race the final repaint and leave no later output
				// to wake this observer. Keep checking while the turn is active.
				resetTimer(retainedMainTurnRecheckWindow)
				continue
			}
			if readySince.IsZero() {
				readySince = time.Now()
				resetTimer(retainedMainTurnReadyStableWindow)
				continue
			}
			api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, provider, "completed", "")
			return
		}
	}
}

// handleRetainedMainTurnStreamClosed reconciles a control-stream exit instead
// of silently leaving the logical turn busy. The provider sidecar wins when it
// already contains a final response. A missing provider process is a failure;
// a still-live pane gets a fresh observer because the control client can fail
// independently of the coding CLI.
func (api *StreamingAPI) handleRetainedMainTurnStreamClosed(
	ctx context.Context,
	sessionID string,
	snapshot terminals.Snapshot,
	provider llmproviders.Provider,
) {
	api.retainedMainTurnsMu.Lock()
	turnStartedAt, tracked := api.retainedMainTurns[sessionID]
	api.retainedMainTurnsMu.Unlock()
	if !tracked || ctx.Err() != nil {
		return
	}
	if finalResult := api.retainedTurnFinalResponse(provider, sessionID, turnStartedAt); strings.TrimSpace(finalResult) != "" {
		log.Printf("[RETAINED_TURN] Tmux stream closed after a durable final response; settling session=%s terminal=%s", sessionID, snapshot.TerminalID)
		api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, provider, "completed", "")
		return
	}

	switch inspectRetainedMainTurnTmuxState(snapshot.TmuxSession) {
	case codingTmuxPaneMissing, codingTmuxPaneDead:
		reason := "retained coding-agent terminal exited before producing a final response"
		log.Printf("[RETAINED_TURN] %s session=%s terminal=%s tmux=%s", reason, sessionID, snapshot.TerminalID, snapshot.TmuxSession)
		api.reconcileUnexpectedTerminalExit(snapshot, reason)
		api.terminalStore.MarkFailed(snapshot.TerminalID)
		api.terminalStore.MarkProcessClosed(snapshot.TerminalID, reason)
		api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, provider, "failed", reason)
	default:
		log.Printf("[RETAINED_TURN] Tmux control stream closed while provider pane remains live; reattaching session=%s terminal=%s", sessionID, snapshot.TerminalID)
		timer := time.NewTimer(retainedMainTurnRecheckWindow)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			go api.observeRetainedMainTurnStream(ctx, sessionID, snapshot, provider)
		}
	}
}

// settleUnansweredRetainedPromotion completes a promoted pending input that
// never produced CLI output of its own, plus any inputs still pending behind
// it: the idle CLI already answered them in its previous response. The
// completion it emits then finds no pending work, releases the chat lane and
// kicks the conversation-turn queue.
func (api *StreamingAPI) settleUnansweredRetainedPromotion(sessionID string, snapshot terminals.Snapshot, provider llmproviders.Provider, waited time.Duration) {
	api.retainedMainTurnsMu.Lock()
	promotedID := strings.TrimSpace(api.retainedMainTurnExecutionIDs[sessionID])
	backlog := append([]string(nil), api.retainedMainTurnPendingExecutionIDs[sessionID]...)
	delete(api.retainedMainTurnPendingExecutionIDs, sessionID)
	api.retainedMainTurnsMu.Unlock()
	log.Printf("[RETAINED_TURN] Promoted execution %s showed no new CLI output within %s; treating it and %d pending input(s) as answered by the previous response session=%s terminal=%s provider=%s",
		promotedID, waited.Round(time.Second), len(backlog), sessionID, snapshot.TerminalID, provider)
	meta := map[string]string{"settled_reason": "answered_by_previous_response"}
	for _, pendingID := range backlog {
		if pendingID = strings.TrimSpace(pendingID); pendingID != "" && pendingID != promotedID {
			api.completeTrackedExecution(pendingID, trackedExecutionStatusCompleted, "", meta)
		}
	}
	api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, provider, "completed", "")
}

// failRetainedTurnExecutions fails the active retained execution and every
// input pending behind it, and clears the pending list.
func (api *StreamingAPI) failRetainedTurnExecutions(sessionID, reason string) {
	api.retainedMainTurnsMu.Lock()
	ids := append([]string{api.retainedMainTurnExecutionIDs[sessionID]}, api.retainedMainTurnPendingExecutionIDs[sessionID]...)
	delete(api.retainedMainTurnPendingExecutionIDs, sessionID)
	api.retainedMainTurnsMu.Unlock()
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			api.completeTrackedExecution(id, trackedExecutionStatusFailed, reason, nil)
		}
	}
}

func (api *StreamingAPI) emitRetainedMainTurnStreamCompletion(sessionID string, snapshot terminals.Snapshot, provider llmproviders.Provider, status, failureReason string) {
	if api == nil {
		return
	}
	now := time.Now()
	api.retainedMainTurnsMu.Lock()
	executionID := strings.TrimSpace(api.retainedMainTurnExecutionIDs[sessionID])
	turnStartedAt := api.retainedMainTurns[sessionID]
	alreadyEmitted := !turnStartedAt.IsZero() && api.retainedMainTurnCompletionEmitted[sessionID].Equal(turnStartedAt)
	if !alreadyEmitted && !turnStartedAt.IsZero() {
		if api.retainedMainTurnCompletionEmitted == nil {
			api.retainedMainTurnCompletionEmitted = make(map[string]time.Time)
		}
		api.retainedMainTurnCompletionEmitted[sessionID] = turnStartedAt
	}
	api.retainedMainTurnsMu.Unlock()
	if alreadyEmitted {
		log.Printf("[RETAINED_TURN] Completion already emitted for this turn, skipping duplicate session=%s terminal=%s", sessionID, snapshot.TerminalID)
		return
	}
	if executionID == "" {
		executionID = strings.TrimSpace(snapshot.ExecutionID)
	}
	if executionID == "" {
		executionID = "main:" + sessionID
	}
	finalResult := api.retainedTurnFinalResponse(provider, sessionID, turnStartedAt)
	if strings.TrimSpace(finalResult) == "" && strings.TrimSpace(failureReason) != "" {
		finalResult = strings.TrimSpace(failureReason)
	}
	if strings.TrimSpace(status) == "" {
		status = "completed"
	}
	completion := unifiedevents.NewUnifiedCompletionEvent(
		"coding_agent",
		"retained",
		"",
		finalResult,
		status,
		now.Sub(turnStartedAt),
		1,
	)
	completion.SessionID = sessionID
	completion.Metadata["source"] = "coding_agent_sidecar"
	completion.Metadata["provider"] = string(provider)
	completion.Metadata["tmux_session"] = snapshot.TmuxSession
	completion.Metadata["execution_kind"] = "main_agent"
	completion.Metadata["scope"] = "main_agent"
	if finalResult == "" {
		completion.Metadata["final_response_missing"] = true
		log.Printf("[RETAINED_TURN] Sidecar had no final assistant response session=%s terminal=%s provider=%s", sessionID, snapshot.TerminalID, provider)
	}
	event := events.Event{
		ID:              fmt.Sprintf("retained-turn-completion-%d", now.UnixNano()),
		Type:            "unified_completion",
		Timestamp:       now,
		SessionID:       sessionID,
		ExecutionID:     executionID,
		ExecutionKind:   "main_agent",
		TerminalOwnerID: "main:" + sessionID,
		TerminalID:      snapshot.TerminalID,
		Data: &unifiedevents.AgentEvent{
			Type:      unifiedevents.EventType("unified_completion"),
			Timestamp: now,
			SessionID: sessionID,
			Data:      completion,
		},
	}
	if api.eventStore != nil {
		api.eventStore.AddEvent(sessionID, event)
		return
	}
	api.observeRetainedMainTurnEvent(sessionID, event)
}

func retainedMainTurnCompletionEvent(eventType string) bool {
	switch strings.ToLower(strings.TrimSpace(eventType)) {
	case "streaming_end", "agent_end", "conversation_end", "unified_completion":
		return true
	default:
		return false
	}
}

// mcpAgentSessionCompletion reports whether this is the canonical completion
// emitted by the durable mcpagent.Session retained-turn lifecycle. Events
// crossing BaseEventBridge intentionally do not carry AgentWorks terminal IDs,
// so the source marker is the authoritative way to distinguish this main-turn
// completion from unrelated child-agent completions in the same session.
func mcpAgentSessionCompletion(event events.Event) bool {
	agentEvent := event.Data
	if agentEvent == nil {
		return false
	}
	completion, ok := agentEvent.Data.(*unifiedevents.UnifiedCompletionEvent)
	if !ok || completion == nil || completion.Metadata == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(fmt.Sprint(completion.Metadata["source"])), "mcpagent_session")
}

// observeRetainedMainTurnEvent settles the explicit busy lifecycle opened by
// markRetainedMainCodingTurnRunning. These are the same structured events that
// back Terminal Center's Formatted view. A normal foreground turn is unaffected
// because it never enters retainedMainTurns, and a child completion is ignored
// because its terminal ownership does not resolve to the main coding agent.
func (api *StreamingAPI) observeRetainedMainTurnEvent(sessionID string, event events.Event) {
	eventType := strings.ToLower(strings.TrimSpace(event.Type))
	if api == nil || api.terminalStore == nil || !retainedMainTurnCompletionEvent(eventType) {
		return
	}

	api.retainedMainTurnsMu.Lock()
	startedAt, tracked := api.retainedMainTurns[sessionID]
	api.retainedMainTurnsMu.Unlock()
	if !tracked || (!event.Timestamp.IsZero() && event.Timestamp.Before(startedAt)) {
		return
	}

	snapshot, ok := api.terminalStore.GetRaw(event.TerminalID)
	if !ok || !codingAgentSnapshotIsMainAgent(snapshot) {
		// BaseEventBridge preserves the canonical mcpagent event but has no
		// AgentWorks terminal identity to attach to it. Resolve the main terminal
		// from the already-tracked owner session only for the explicit Session
		// completion source. This keeps child completions from settling the turn.
		if !mcpAgentSessionCompletion(event) {
			return
		}
		ok = false
		for _, candidate := range api.terminalStore.ListRaw(sessionID) {
			if codingAgentSnapshotIsMainAgent(candidate) {
				snapshot = candidate
				ok = true
				break
			}
		}
		if !ok {
			return
		}
	}

	// streaming_end is first applied by the terminal observer. It can reject an
	// intermediate provider end while the pane still shows newer active work.
	// Higher-level completion events are definitive and may settle the retained
	// logical turn while deliberately keeping the tmux process alive for resume.
	if eventType != "streaming_end" && snapshot.Active {
		if completed, changed := api.terminalStore.MarkTurnCompleted(snapshot.TerminalID); changed {
			snapshot = completed
		}
	}
	if snapshot.Active {
		return
	}

	api.retainedMainTurnsMu.Lock()
	currentStart, stillTracked := api.retainedMainTurns[sessionID]
	executionID := strings.TrimSpace(api.retainedMainTurnExecutionIDs[sessionID])
	var watchCancel context.CancelFunc
	var nextWatchCtx context.Context
	var nextSnapshot terminals.Snapshot
	var nextProvider llmproviders.Provider
	promotedExecutionID := ""
	failedPendingExecutionIDs := []string(nil)
	if stillTracked && currentStart.Equal(startedAt) {
		watchCancel = api.retainedMainTurnWatchCancels[sessionID]
		pending := api.retainedMainTurnPendingExecutionIDs[sessionID]
		if len(pending) > 0 && !strings.EqualFold(strings.TrimSpace(snapshot.State), "failed") {
			promotedExecutionID = strings.TrimSpace(pending[0])
			api.retainedMainTurnPendingExecutionIDs[sessionID] = append([]string(nil), pending[1:]...)
			api.retainedMainTurnExecutionIDs[sessionID] = promotedExecutionID
			// The provider accepted this input while the previous response was
			// active and begins it immediately after that response completes.
			// Move the boundary forward so the prior completion cannot settle it.
			api.retainedMainTurns[sessionID] = time.Now()
			// The mcpagent Session owns only the response that was active when
			// delivery began. Once it emits that completion its bridge can close,
			// while the CLI continues with already-queued input. Observe the live
			// terminal for every promoted turn so its later response is not lost.
			var nextCancel context.CancelFunc
			nextWatchCtx, nextCancel = context.WithCancel(context.Background())
			api.retainedMainTurnWatchCancels[sessionID] = nextCancel
			nextSnapshot = snapshot
			nextProvider = llmproviders.Provider(retainedCodingAgentProvider(snapshot))
		} else {
			if strings.EqualFold(strings.TrimSpace(snapshot.State), "failed") {
				failedPendingExecutionIDs = append(failedPendingExecutionIDs, pending...)
			}
			delete(api.retainedMainTurns, sessionID)
			delete(api.retainedMainTurnExecutionIDs, sessionID)
			delete(api.retainedMainTurnPendingExecutionIDs, sessionID)
			delete(api.retainedMainTurnWatchCancels, sessionID)
		}
	} else {
		stillTracked = false
	}
	api.retainedMainTurnsMu.Unlock()
	if !stillTracked {
		return
	}
	if watchCancel != nil {
		watchCancel()
	}

	if strings.EqualFold(strings.TrimSpace(snapshot.State), "failed") {
		api.setSessionBusy(sessionID, false)
		api.updateSessionStatus(sessionID, "error")
		if executionID != "" {
			api.completeTrackedExecution(executionID, trackedExecutionStatusFailed, "retained coding-agent turn failed", nil)
		}
		for _, pendingID := range failedPendingExecutionIDs {
			if pendingID = strings.TrimSpace(pendingID); pendingID != "" && pendingID != executionID {
				api.completeTrackedExecution(pendingID, trackedExecutionStatusFailed, "retained coding-agent turn failed", nil)
			}
		}
	} else {
		if executionID != "" {
			api.completeTrackedExecution(executionID, trackedExecutionStatusCompleted, "", nil)
		}
		if promotedExecutionID != "" {
			api.terminalStore.MarkTurnRunning(snapshot.TerminalID)
			api.setSessionBusy(sessionID, true)
			api.updateSessionStatus(sessionID, "running")
			if nextWatchCtx != nil {
				go api.observeRetainedMainTurnStreamMode(nextWatchCtx, sessionID, nextSnapshot, nextProvider, true)
			}
		} else {
			api.setSessionBusy(sessionID, false)
			api.updateSessionStatus(sessionID, "completed")
		}
	}
	// A live retained CLI turn bypasses handleQuery's normal final-save block.
	// Persist an exact structured completion directly whenever it carries the
	// final reply. Native transcript reconciliation is only the emergency path
	// for missing structured output or a failed canonical append.
	if !api.persistRetainedStructuredCompletion(sessionID, event) {
		api.scheduleWorkflowBuilderNativeTranscriptSync(sessionID)
	}
	log.Printf("[RETAINED_TURN] Settled retained main-agent turn from structured %s event session=%s terminal=%s state=%s next_execution=%s",
		eventType, sessionID, snapshot.TerminalID, snapshot.State, promotedExecutionID)
	if promotedExecutionID == "" {
		api.kickConversationTurnQueue(sessionID)
	}
}

// deliverRetainedMainTerminalInput sends directly to a live main coding-agent
// pane when its short-lived Go Agent object is no longer registered. A retained
// tmux pane is still the same provider conversation, so starting a fresh
// workflow-builder turn here would both lose continuity and allow unrelated
// scheduled work to block the user's message.
//
// handled is true only when this is a live, supported coding-agent session. A
// handled delivery error must be surfaced to the caller; it must never be
// hidden behind an asynchronous next-turn fallback.
func (api *StreamingAPI) deliverRetainedMainTerminalInput(ctx context.Context, sessionID, message string) (provider string, handled bool, err error) {
	if api == nil {
		return "", false, nil
	}
	snapshot, live := api.liveMainCodingTmuxSnapshot(sessionID)
	if !live {
		return "", false, nil
	}

	api.lastQueryMu.RLock()
	request, ok := api.lastQueryRequests[sessionID]
	api.lastQueryMu.RUnlock()
	liveProvider := strings.TrimSpace(retainedCodingAgentProvider(snapshot))
	storedProvider := ""
	modelID := ""
	if ok {
		storedProvider = strings.TrimSpace(request.Provider)
		modelID = strings.TrimSpace(request.ModelID)
		if request.LLMConfig != nil {
			if storedProvider == "" {
				storedProvider = strings.TrimSpace(request.LLMConfig.Primary.Provider)
			}
			if modelID == "" {
				modelID = strings.TrimSpace(request.LLMConfig.Primary.ModelID)
			}
		}
	}
	// The live tmux identifies the process that will receive this input. A
	// continuation record can legitimately lag behind after Update Automation
	// switches the workflow from one coding provider to another, so it may
	// supply a model only when it describes that same provider.
	provider = liveProvider
	if provider == "" {
		provider = storedProvider
	} else if storedProvider != "" && !strings.EqualFold(provider, storedProvider) {
		modelID = ""
	}
	if provider == "" {
		return "", false, nil
	}
	contract, isCodingAgent := llmproviders.GetCodingAgentProviderContract(llmproviders.Provider(provider), modelID)
	if !isCodingAgent || !contract.SupportsLiveInput {
		return provider, false, nil
	}

	if api.internalRetainedTerminalInputHandler != nil {
		return provider, true, api.internalRetainedTerminalInputHandler(ctx, llmproviders.Provider(provider), modelID, sessionID, message)
	}
	return provider, true, llmproviders.SendCodingAgentRetainedInput(ctx, llmproviders.Provider(provider), modelID, sessionID, message)
}

func (api *StreamingAPI) recordRetainedTerminalLiveInput(sessionID, message, provider, clientMessageID string, executionIDs ...string) string {
	return api.recordRetainedLiveInput(sessionID, message, provider, clientMessageID, false, executionIDs...)
}

func (api *StreamingAPI) recordMCPAgentSessionLiveInput(sessionID, message, provider, clientMessageID string, executionIDs ...string) string {
	return api.recordRetainedLiveInput(sessionID, message, provider, clientMessageID, true, executionIDs...)
}

func (api *StreamingAPI) recordRetainedLiveInput(sessionID, message, provider, clientMessageID string, sessionOwnsCompletion bool, executionIDs ...string) string {
	messageID := newSteerMessageID()
	executionID := "live-turn:" + messageID
	if len(executionIDs) > 0 && strings.TrimSpace(executionIDs[0]) != "" {
		executionID = strings.TrimSpace(executionIDs[0])
	}
	request := QueryRequest{Query: message, TriggeredBy: "interactive"}
	api.lastQueryMu.RLock()
	if prior, ok := api.lastQueryRequests[sessionID]; ok {
		request = prior
		request.Query = message
	}
	api.lastQueryMu.RUnlock()
	api.trackConversationTurnStart(executionID, sessionID, request)
	api.recordLiveCodingAgentUserMessage(sessionID, message, provider, messageID, "sent_to_cli", clientMessageID)
	if sessionOwnsCompletion {
		api.markMCPAgentSessionTurnRunning(sessionID, executionID)
	} else {
		api.markRetainedMainCodingTurnRunning(sessionID, executionID)
	}
	return messageID
}

func writeRetainedTerminalLiveInputResponse(w http.ResponseWriter, sessionID, message, provider, clientMessageID string, api *StreamingAPI) {
	messageID := api.recordRetainedTerminalLiveInput(sessionID, message, provider, clientMessageID)
	writeRetainedTerminalLiveInputResponseWithMessageID(w, sessionID, provider, messageID)
}

func writeRetainedTerminalLiveInputResponseWithMessageID(w http.ResponseWriter, sessionID, provider, messageID string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(LiveInputResponse{
		Success:        true,
		Message:        "User message delivered to retained coding-agent CLI",
		DeliveryStatus: "sent_to_cli",
		Provider:       provider,
		MessageID:      messageID,
	})
}

func codingAgentHasNativeResume(provider string, underlyingAgent *mcpagent.Agent) bool {
	if underlyingAgent == nil {
		return false
	}
	handle := mcpagent.SnapshotAgentSession(underlyingAgent)
	if handle == nil || handle.Provider.Empty() || strings.TrimSpace(handle.Provider.NativeSessionID) == "" {
		return false
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider != "" && !strings.EqualFold(handle.Provider.Provider, provider) {
		return false
	}

	// Provider identity alone is not a continuation. A newly configured coding
	// agent already has its provider/model/working directory, but it cannot
	// resume until the provider has returned a native conversation ID. Treating
	// that incomplete handle as resumable prevents the current conversation's
	// persisted handle from being restored on the next request.
	if strings.TrimSpace(handle.Provider.NativeSessionID) != "" {
		return true
	}
	// Codex can resume from its project directory when a native thread ID is
	// unavailable. Other providers require their native session ID.
	return strings.EqualFold(handle.Provider.Provider, "codex-cli") && strings.TrimSpace(handle.Provider.ProjectDirID) != ""
}

func codingAgentProviderSupportsNativeResume(provider, modelID string) bool {
	contract, ok := codingAgentProviderContract(provider, modelID)
	return ok && contract.SupportsNativeResume
}

func isCodingAgentProvider(provider, modelID string) bool {
	_, ok := codingAgentProviderContract(provider, modelID)
	return ok
}

func codingAgentProviderContract(provider, modelID string) (llmproviders.CodingAgentProviderContract, bool) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return llmproviders.CodingAgentProviderContract{}, false
	}
	return llmproviders.GetCodingAgentProviderContract(llmproviders.Provider(provider), strings.TrimSpace(modelID))
}

func (api *StreamingAPI) seedCodingAgentRuntimeFromRestoredConversation(sessionID, currentProvider, currentWorkshopMode string, runtime *ChatHistoryAgentRuntime, underlyingAgent *mcpagent.Agent) bool {
	if api == nil || underlyingAgent == nil || runtime == nil {
		return false
	}
	if !workflowCLIResumeAllowed(underlyingAgent, runtime) {
		log.Printf("[CHAT_HISTORY] Native resume skipped for session %s: CLI runtime directory changed; using saved application history", sessionID)
		return false
	}
	hasAgentSessionHandle := runtime.AgentSessionHandle != nil && !runtime.AgentSessionHandle.Empty()
	if runtime.Kind != "coding_agent" || (!runtime.ResumeSupported && !hasAgentSessionHandle) {
		return false
	}
	provider := strings.ToLower(strings.TrimSpace(runtime.Provider))
	if provider == "" && hasAgentSessionHandle {
		provider = strings.ToLower(strings.TrimSpace(runtime.AgentSessionHandle.Provider.Provider))
	}
	currentProvider = strings.ToLower(strings.TrimSpace(currentProvider))
	if provider == "" || (currentProvider != "" && provider != currentProvider) {
		return false
	}
	if !chatHistoryResumeModesCompatible(runtime.WorkshopMode, currentWorkshopMode) {
		log.Printf("[CHAT_HISTORY] Skipping native coding-agent resume for session %s: restored mode=%s current mode=%s", sessionID, normalizeChatHistoryWorkshopMode(runtime.WorkshopMode), normalizeChatHistoryWorkshopMode(currentWorkshopMode))
		return false
	}
	api.conversationMux.RLock()
	currentProfileKey, profileKeyKnown := api.lastAgentProfileKeyBySession[sessionID]
	api.conversationMux.RUnlock()
	// A changed profile definition (system prompt, tool allowlist, MCP servers,
	// Crew identity) must never cost the conversation its memory: the CLI is
	// relaunched with the current definition and resumes the same native
	// session. Retained live input is still refused for a stale process (see
	// agentProfileAllowsRetainedLiveInput), which is what forces that relaunch.
	if profileKeyKnown && strings.TrimSpace(runtime.AgentProfileKey) != currentProfileKey {
		// Switching a Crew's native agent tools (hybrid <-> AgentWorks-only) is
		// a capability change, like Builder <-> Run: the old transcript is full
		// of tool use the new mode cannot perform. Start fresh with the recent
		// dialogue instead of resuming it.
		api.conversationMux.RLock()
		currentToolsMode, toolsModeKnown := api.lastAgentToolsModeBySession[sessionID]
		api.conversationMux.RUnlock()
		if toolsModeKnown && normalizeAgentToolsMode(runtime.AgentToolsMode) != normalizeAgentToolsMode(currentToolsMode) {
			log.Printf("[CHAT_HISTORY] Agent tools mode changed for session %s (%s -> %s); starting a fresh native session with the recent dialogue", sessionID, normalizeAgentToolsMode(runtime.AgentToolsMode), normalizeAgentToolsMode(currentToolsMode))
			return false
		}
		if !codingProviderReloadsInstructionsOnResume(provider) {
			log.Printf("[CHAT_HISTORY] Agent profile definition changed for session %s; %s cannot load new instructions into a resumed session, starting a fresh one with the recent dialogue", sessionID, provider)
			return false
		}
		log.Printf("[CHAT_HISTORY] Agent profile definition changed for session %s; resuming the same native coding-agent session with the current definition", sessionID)
	}
	externalSessionID := strings.TrimSpace(runtime.ExternalSessionID)
	projectDirID := strings.TrimSpace(runtime.ProjectDirID)
	currentOwnerSessionID := ""
	if current := mcpagent.SnapshotAgentSession(underlyingAgent); current != nil {
		currentOwnerSessionID = strings.TrimSpace(current.SessionID)
	}
	var resumeHandle mcpagent.AgentSessionHandle
	if hasAgentSessionHandle {
		resumeHandle = *runtime.AgentSessionHandle
		if externalSessionID == "" {
			externalSessionID = strings.TrimSpace(runtime.AgentSessionHandle.Provider.NativeSessionID)
		}
		if projectDirID == "" {
			projectDirID = strings.TrimSpace(runtime.AgentSessionHandle.Provider.ProjectDirID)
		}
	}
	if externalSessionID == "" && projectDirID == "" {
		return false
	}

	// Restore provider-native conversation state only. The current /api/query
	// path has already rebuilt the prompt, tools, browser intent, secrets, and
	// permissions. Re-applying persisted instructions here would overwrite live
	// state with a stale snapshot.

	if provider != "codex-cli" && externalSessionID == "" {
		return false
	}
	resumeHandle.SessionID = firstNonEmptyTrimmed(currentOwnerSessionID, sessionID)
	resumeHandle.OwnerID = resumeHandle.SessionID
	resumeHandle.Provider.Provider = provider
	resumeHandle.Provider.Model = firstNonEmptyTrimmed(resumeHandle.Provider.Model, runtime.ModelID)
	resumeHandle.Provider.NativeSessionID = externalSessionID
	resumeHandle.Provider.ProjectDirID = projectDirID
	if !mcpagent.ApplyAgentResumeHandle(underlyingAgent, &resumeHandle) {
		log.Printf("[%s] Rejected stale resume handle for session %s; using saved application history", provider, sessionID)
		return false
	}
	log.Printf("[%s] Restored native runtime from chat history for session %s", provider, sessionID)
	return true
}

// updateSessionActivity updates the LastActivity timestamp for a session when events are added
func (api *StreamingAPI) updateSessionActivity(sessionID string) {
	api.activeSessionsMux.Lock()
	if session, exists := api.activeSessions[sessionID]; exists {
		session.LastActivity = time.Now()
		// Don't log every activity update to avoid log spam
	}
	api.activeSessionsMux.Unlock()
}

// updateSessionStatus updates the status of an active session in memory.
func (api *StreamingAPI) updateSessionStatus(sessionID, status string) {
	changed, workspacePath, scheduled := false, "", false
	api.activeSessionsMux.Lock()
	if session, exists := api.activeSessions[sessionID]; exists {
		changed = session.Status != status
		workspacePath = session.WorkspacePath
		scheduled = liveFeedScheduledSession(sessionID, session.TriggeredBy)
		session.Status = status
		session.LastActivity = time.Now()
		log.Printf("[ACTIVE_SESSION] Updated session %s status to: %s", sessionID, status)
	}
	api.activeSessionsMux.Unlock()
	api.observeRuntimeSnapshot(sessionID)
	if changed {
		publishSessionsChanged()
		// Only a finished scheduled run refreshes the right pane. A chat turn
		// (sending a message) must change nothing outside the chat.
		if scheduled && liveFeedTerminalStatus(status) {
			publishWorkflowSettled(workspacePath)
		}
	}
}

// removeSessionQueryID removes a completed workflow query from the session ->
// query index used by stop/reconnect/scheduler wait logic.
func (api *StreamingAPI) removeSessionQueryID(sessionID, queryID string) {
	if sessionID == "" || queryID == "" {
		return
	}

	api.sessionQueryIDMux.Lock()
	defer api.sessionQueryIDMux.Unlock()

	queryIDs := api.sessionQueryIDs[sessionID]
	if len(queryIDs) == 0 {
		return
	}

	next := queryIDs[:0]
	for _, qid := range queryIDs {
		if qid != queryID {
			next = append(next, qid)
		}
	}
	if len(next) == 0 {
		delete(api.sessionQueryIDs, sessionID)
		return
	}
	api.sessionQueryIDs[sessionID] = next
}

// handleDismissSession marks a session as dismissed so it won't be auto-restored on page refresh.
func (api *StreamingAPI) handleDismissSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	vars := mux.Vars(r)
	sessionID := vars["session_id"]

	if sessionID == "" {
		http.Error(w, "Session ID is required", http.StatusBadRequest)
		return
	}
	// Only someone who may see the session can dismiss it (PLAT-362 D5).
	if !api.canAccessTerminalSession(r, sessionID) {
		http.Error(w, "Session not found or access denied", http.StatusNotFound)
		return
	}

	api.activeSessionsMux.Lock()
	if session, exists := api.activeSessions[sessionID]; exists {
		session.Status = "dismissed"
		session.LastActivity = time.Now()
	}
	api.activeSessionsMux.Unlock()
	publishSessionsChanged()

	log.Printf("[ACTIVE_SESSION] Dismissed session %s", sessionID)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "dismissed",
		"session": sessionID,
	})
}

// sessionEventEmitter implements virtualtools.SessionEventEmitter to emit
// blocking_human_feedback events for human-input tools.
type sessionEventEmitter struct {
	eventStore *events.EventStore
	sessionID  string
}

func (e *sessionEventEmitter) EmitBlockingHumanFeedback(requestID, question, contextText string, yesNoOnly bool, yesLabel, noLabel string, options ...string) {
	now := time.Now()
	eventData := &orchEvents.BlockingHumanFeedbackEvent{
		BaseEventData: unifiedevents.BaseEventData{
			Timestamp: now,
		},
		Question:      question,
		AllowFeedback: !yesNoOnly && len(options) == 0, // Allow text input when not yes/no and no options
		Context:       contextText,
		SessionID:     e.sessionID,
		RequestID:     requestID,
		YesNoOnly:     yesNoOnly,
		YesLabel:      yesLabel,
		NoLabel:       noLabel,
		Options:       options,
	}
	event := events.Event{
		ID:        fmt.Sprintf("%s_human_feedback_%d", e.sessionID, now.UnixNano()),
		Type:      "blocking_human_feedback",
		Timestamp: now,
		SessionID: e.sessionID,
		Data: &unifiedevents.AgentEvent{
			Type:      orchEvents.BlockingHumanFeedback,
			Timestamp: now,
			SessionID: e.sessionID,
			Component: "delegation",
			Data:      eventData,
		},
	}
	e.eventStore.AddEvent(e.sessionID, event)
	log.Printf("[HUMAN_FEEDBACK] Emitted blocking_human_feedback event (request_id: %s, session: %s)", requestID, e.sessionID)
}

// EmitHumanFeedbackResolved records a durable answer/expiry marker for a
// blocking request so restore does not show an answered prompt as pending.
func (e *sessionEventEmitter) EmitHumanFeedbackResolved(requestID, outcome string) {
	now := time.Now()
	e.eventStore.AddEvent(e.sessionID, events.Event{
		ID:        fmt.Sprintf("%s_human_feedback_resolved_%s", e.sessionID, requestID),
		Type:      "human_feedback_resolved",
		Timestamp: now,
		SessionID: e.sessionID,
		Data: &unifiedevents.AgentEvent{
			Type:      unifiedevents.EventType("human_feedback_resolved"),
			Timestamp: now,
			SessionID: e.sessionID,
			Component: "delegation",
			Data: events.NewGenericEventData("human_feedback_resolved", map[string]interface{}{
				"request_id": requestID,
				"outcome":    outcome,
				"session_id": e.sessionID,
			}),
		},
	})
}

// EmitProductInteraction publishes the identical event a product.yaml tool
// binding's runtime.Emit produces (see emitAgentProfileEvent), for the human
// tools that have no such binding to carry that callback. Product is left
// blank: a session belongs to exactly one product already, so a UI reading
// its own session's events has no need to filter by it.
func (e *sessionEventEmitter) EmitProductInteraction(kind string, payload map[string]interface{}) {
	now := time.Now()
	data := &orchEvents.ProductInteractionEvent{Kind: kind, Payload: payload}
	data.BaseEventData = unifiedevents.BaseEventData{Timestamp: now}
	eventType := string(data.GetEventType())
	e.eventStore.AddEvent(e.sessionID, events.Event{
		ID:        fmt.Sprintf("%s_%s_%d", e.sessionID, strings.ReplaceAll(eventType, ".", "_"), now.UnixNano()),
		Type:      eventType,
		Timestamp: now,
		SessionID: e.sessionID,
		Data: &unifiedevents.AgentEvent{
			Type:      unifiedevents.EventType(eventType),
			Timestamp: now,
			SessionID: e.sessionID,
			Data:      data,
		},
	})
}

func sanitizeTierModel(model *virtualtools.TierModel) *virtualtools.TierModel {
	if model == nil || model.Provider == "" || model.ModelID == "" {
		return nil
	}
	sanitized := &virtualtools.TierModel{
		Provider:     strings.TrimSpace(model.Provider),
		ModelID:      strings.TrimSpace(model.ModelID),
		Options:      model.Options,
		ConnectionID: model.ConnectionID,
	}

	return sanitized
}

func queryRequestHasExplicitLLMSelection(req QueryRequest) bool {
	if req.LLMConfig != nil {
		if strings.TrimSpace(req.LLMConfig.Primary.Provider) != "" || strings.TrimSpace(req.LLMConfig.Primary.ModelID) != "" {
			return true
		}
	}
	return strings.TrimSpace(req.Provider) != "" || strings.TrimSpace(req.ModelID) != ""
}

func applyTopLevelDelegationModel(ctx context.Context, req QueryRequest, finalProvider, finalModelID string) (string, string, bool) {
	// A provider profile is itself the user's explicit selection. It must win
	// over stale tab-level provider/model fields sent by an older frontend state.
	providerProfileSelected := req.DelegationTierConfig != nil &&
		strings.EqualFold(strings.TrimSpace(req.DelegationTierConfig.Mode), "provider_profile") &&
		strings.TrimSpace(req.DelegationTierConfig.Provider) != ""
	if queryRequestHasExplicitLLMSelection(req) && !providerProfileSelected {
		return finalProvider, finalModelID, false
	}
	tierConfig := LoadAndResolveTierConfig(ctx, req.DelegationTierConfig)
	if tierConfig == nil {
		return finalProvider, finalModelID, false
	}
	if tierConfig.Main != nil && tierConfig.Main.Provider != "" && tierConfig.Main.ModelID != "" {
		return tierConfig.Main.Provider, tierConfig.Main.ModelID, true
	}
	if tierConfig.High != nil && tierConfig.High.Provider != "" && tierConfig.High.ModelID != "" {
		return tierConfig.High.Provider, tierConfig.High.ModelID, true
	}
	return finalProvider, finalModelID, false
}

// resolveDelegationTierConfig builds a DelegationTierConfig by merging:
// 1. Frontend config (from QueryRequest) - highest priority
// 2. Environment variables (DELEGATION_TIER_*) - fallback
// Returns nil if no tier config is available at all
func resolveDelegationTierConfig(frontendConfig *virtualtools.DelegationTierConfig) *virtualtools.DelegationTierConfig {
	if frontendConfig != nil && frontendConfig.Mode == "provider_profile" && strings.TrimSpace(frontendConfig.Provider) != "" {
		defaults, ok := llmproviders.GetCodingAgentDefaultTierModels(llmproviders.Provider(strings.TrimSpace(frontendConfig.Provider)))
		if ok {
			toTierModel := func(ref llmproviders.CodingAgentTierModelRef) *virtualtools.TierModel {
				if strings.TrimSpace(ref.Provider) == "" || strings.TrimSpace(ref.ModelID) == "" {
					return nil
				}
				return &virtualtools.TierModel{Provider: ref.Provider, ModelID: ref.ModelID, Options: ref.Options}
			}
			return &virtualtools.DelegationTierConfig{
				SchemaVersion: delegationTierConfigSchemaVersion,
				Mode:          "provider_profile",
				Provider:      strings.TrimSpace(frontendConfig.Provider),
				Main:          toTierModel(defaults.Builder),
				High:          toTierModel(defaults.High),
				Medium:        toTierModel(defaults.Medium),
				Low:           toTierModel(defaults.Low),
			}
		}
	}

	result := &virtualtools.DelegationTierConfig{SchemaVersion: delegationTierConfigSchemaVersion, Mode: "explicit"}
	hasAny := false

	// Start with env var defaults
	if p, m := os.Getenv("DELEGATION_TIER_HIGH_PROVIDER"), os.Getenv("DELEGATION_TIER_HIGH_MODEL"); p != "" && m != "" {
		result.High = &virtualtools.TierModel{Provider: p, ModelID: m}
		hasAny = true
	}
	if p, m := os.Getenv("DELEGATION_TIER_MEDIUM_PROVIDER"), os.Getenv("DELEGATION_TIER_MEDIUM_MODEL"); p != "" && m != "" {
		result.Medium = &virtualtools.TierModel{Provider: p, ModelID: m}
		hasAny = true
	}
	if p, m := os.Getenv("DELEGATION_TIER_LOW_PROVIDER"), os.Getenv("DELEGATION_TIER_LOW_MODEL"); p != "" && m != "" {
		result.Low = &virtualtools.TierModel{Provider: p, ModelID: m}
		hasAny = true
	}

	// Override with frontend config (higher priority)
	if frontendConfig != nil {
		if main := sanitizeTierModel(frontendConfig.Main); main != nil {
			result.Main = main
			hasAny = true
		}
		if high := sanitizeTierModel(frontendConfig.High); high != nil {
			result.High = high
			hasAny = true
		}
		if medium := sanitizeTierModel(frontendConfig.Medium); medium != nil {
			result.Medium = medium
			hasAny = true
		}
		if low := sanitizeTierModel(frontendConfig.Low); low != nil {
			result.Low = low
			hasAny = true
		}
		// Pass through custom tiers from frontend (no env var equivalent)
		if len(frontendConfig.Custom) > 0 {
			result.Custom = frontendConfig.Custom
			hasAny = true
		}
	}

	if !hasAny {
		return nil
	}
	return result
}

// handleGetDelegationTierDefaults returns the env var default values for delegation tier config
func (api *StreamingAPI) handleGetDelegationTierDefaults(w http.ResponseWriter, r *http.Request) {
	tierConfig := resolveDelegationTierConfig(nil) // env vars only
	if tierConfig == nil {
		tierConfig = &virtualtools.DelegationTierConfig{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tierConfig)
}

// getActiveSession retrieves an active session by ID
// truncateForLog truncates a string for logging purposes
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func (api *StreamingAPI) getActiveSession(sessionID string) (*ActiveSessionInfo, bool) {
	api.activeSessionsMux.RLock()
	defer api.activeSessionsMux.RUnlock()

	session, exists := api.activeSessions[sessionID]
	if !exists || session == nil {
		return nil, false
	}
	return cloneActiveSessionInfo(session), true
}

func cloneActiveSessionInfo(session *ActiveSessionInfo) *ActiveSessionInfo {
	if session == nil {
		return nil
	}
	copy := *session
	if session.WaitingSince != nil {
		waitingSince := *session.WaitingSince
		copy.WaitingSince = &waitingSince
	}
	if session.RuntimeState != nil {
		runtimeState := cloneRuntimeSnapshot(*session.RuntimeState)
		copy.RuntimeState = &runtimeState
	}
	return &copy
}

// getAllActiveSessions returns live sessions plus retained terminal sessions.
func (api *StreamingAPI) getAllActiveSessions() []*ActiveSessionInfo {
	api.activeSessionsMux.RLock()
	snapshots := make([]*ActiveSessionInfo, 0, len(api.activeSessions))
	for _, session := range api.activeSessions {
		snapshots = append(snapshots, cloneActiveSessionInfo(session))
	}
	api.activeSessionsMux.RUnlock()

	now := time.Now()
	inactivityTimeout := 10 * time.Minute
	sessionRetention := 24 * time.Hour
	sessions := make([]*ActiveSessionInfo, 0, len(snapshots))

	for _, session := range snapshots {
		if normalizeSessionLifecycleStatus(session.Status) == sessionLifecycleRunning {
			runtimeState, _ := api.authoritativeRuntimeSnapshot(session.SessionID)
			if now.Sub(session.LastActivity) < inactivityTimeout || runtimePhaseIsLive(runtimeState.Phase) {
				sessions = append(sessions, session)
			}
			continue
		}

		isTerminal := isStoppedSessionStatus(session.Status)
		if isTerminal && now.Sub(session.LastActivity) < sessionRetention {
			sessions = append(sessions, session)
		}
	}

	return sessions
}

// cleanupInactiveSessions runs periodically to:
// 1. Mark running sessions as inactive if no events for 10 minutes
// 2. Evict event store buffers when the retained session expires
// 3. Fully delete sessions from activeSessions after 24 hours
//
// The session entry is intentionally kept alive for 24 hours (not the old 30 minutes) so
// that verifySessionAccess continues to accept follow-up messages. Evicting a session from
// activeSessions causes the frontend to start a new session with no history, silently
// losing conversation context.
func (api *StreamingAPI) cleanupInactiveSessions() {
	ticker := time.NewTicker(2 * time.Minute) // Check every 2 minutes
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		api.cleanupInactiveSessionsAt(now)
		api.heartbeatTerminalLeases(now)
		if closed := api.cleanupStaleCodingAgentTmuxSessions(now); closed > 0 {
			log.Printf("[ACTIVE_SESSION] Cleanup: Closed %d stale coding-agent tmux session(s)", closed)
		}
		if registry := api.ensureTerminalLeaseRegistry(); registry != nil {
			registry.Prune(now)
		}
	}
}

type inactiveSessionCandidate struct {
	sessionID    string
	status       string
	lastActivity time.Time
}

func (api *StreamingAPI) cleanupInactiveSessionsAt(now time.Time) {
	const (
		inactivityTimeout    = 10 * time.Minute
		sessionRetention     = 24 * time.Hour
		eventBufferRetention = 24 * time.Hour
	)

	api.activeSessionsMux.RLock()
	candidates := make([]inactiveSessionCandidate, 0, len(api.activeSessions))
	for sessionID, session := range api.activeSessions {
		candidates = append(candidates, inactiveSessionCandidate{
			sessionID: sessionID, status: session.Status, lastActivity: session.LastActivity,
		})
	}
	api.activeSessionsMux.RUnlock()

	markInactive := make(map[string]time.Time)
	for _, candidate := range candidates {
		if normalizeSessionLifecycleStatus(candidate.status) != sessionLifecycleRunning || now.Sub(candidate.lastActivity) < inactivityTimeout {
			continue
		}
		api.pendingMu.RLock()
		hasPending := len(api.pendingCompletions[candidate.sessionID]) > 0 || api.completionRetryScheduled[candidate.sessionID]
		api.pendingMu.RUnlock()
		runtimeState, _ := api.authoritativeRuntimeSnapshot(candidate.sessionID)
		if !hasPending && !runtimePhaseIsLive(runtimeState.Phase) {
			markInactive[candidate.sessionID] = candidate.lastActivity
		}
	}

	sessionsToEvictEventBuffer := make([]string, 0)
	sessionsToEvictRuntime := make([]string, 0)
	sessionsMarkedInactive := make([]string, 0)
	api.activeSessionsMux.Lock()
	for sessionID, session := range api.activeSessions {
		age := now.Sub(session.LastActivity)
		if previousActivity, ok := markInactive[sessionID]; ok &&
			session.LastActivity.Equal(previousActivity) &&
			normalizeSessionLifecycleStatus(session.Status) == sessionLifecycleRunning {
			session.Status = string(sessionLifecycleInactive)
			session.LastActivity = now
			sessionsMarkedInactive = append(sessionsMarkedInactive, sessionID)
			age = 0
			log.Printf("[ACTIVE_SESSION] Marked session %s inactive after verified idle timeout", sessionID)
		}
		if !isStoppedSessionStatus(session.Status) {
			continue
		}
		if age >= eventBufferRetention {
			sessionsToEvictEventBuffer = append(sessionsToEvictEventBuffer, sessionID)
		}
		if age >= sessionRetention {
			delete(api.activeSessions, sessionID)
			sessionsToEvictRuntime = append(sessionsToEvictRuntime, sessionID)
			if api.terminalStore != nil {
				api.terminalStore.RemoveSession(sessionID)
			}
			log.Printf("[ACTIVE_SESSION] Cleanup: Removed old %s session %s from memory (>24h)", session.Status, sessionID)
		}
	}
	api.activeSessionsMux.Unlock()
	if len(sessionsMarkedInactive) > 0 || len(sessionsToEvictRuntime) > 0 {
		publishSessionsChanged()
	}

	for _, sessionID := range sessionsMarkedInactive {
		api.observeRuntimeSnapshot(sessionID)
	}

	for _, sessionID := range sessionsToEvictRuntime {
		workspace.ClearSessionShellConfig(sessionID)
		virtualtools.DeleteSessionNotificationDestination(sessionID)
		if api.runtimeCoordinator != nil {
			api.runtimeCoordinator.Evict(sessionID)
		}
	}

	for _, sessionID := range sessionsToEvictEventBuffer {
		if api.eventStore != nil {
			api.eventStore.RemoveSession(sessionID)
			log.Printf("[ACTIVE_SESSION] Cleanup: Removed event buffer for session %s", sessionID)
		}
	}
}

// storeWorkflowOrchestrator stores a workflow orchestrator for a session
func (api *StreamingAPI) storeWorkflowOrchestrator(sessionID string, orchestrator orchestrator.Orchestrator) {
	api.workflowOrchestratorsMux.Lock()
	defer api.workflowOrchestratorsMux.Unlock()
	api.workflowOrchestrators[sessionID] = orchestrator
	log.Printf("[ORCHESTRATOR] Stored workflow orchestrator for session %s", sessionID)
}

// --- LLM GUIDANCE API HANDLERS ---

// deserializeSerializedMessage converts a SerializedMessage (typed) back to llmtypes.MessageContent
//
//nolint:unused // kept for the serialized-history rehydration path during polling refactors.
func deserializeSerializedMessage(serialized unifiedevents.SerializedMessage) *llmtypes.MessageContent {
	var role llmtypes.ChatMessageType
	switch serialized.Role {
	case "human": // Standard value from llmtypes
		role = llmtypes.ChatMessageTypeHuman
	case "ai": // Standard value from llmtypes
		role = llmtypes.ChatMessageTypeAI
	case "tool": // Standard value from llmtypes
		role = llmtypes.ChatMessageTypeTool
	case "system": // Standard value from llmtypes
		role = llmtypes.ChatMessageTypeSystem
	case "user", "assistant": // Fallback for compatibility (shouldn't happen but handle gracefully)
		if serialized.Role == "user" {
			role = llmtypes.ChatMessageTypeHuman
		} else {
			role = llmtypes.ChatMessageTypeAI
		}
	default:
		// Default to human if unknown role
		log.Printf("[DESERIALIZE] Unknown role '%s', defaulting to human", serialized.Role)
		role = llmtypes.ChatMessageTypeHuman
	}

	msg := &llmtypes.MessageContent{
		Role:  role,
		Parts: []llmtypes.ContentPart{},
	}

	for _, part := range serialized.Parts {
		switch part.Type {
		case "text":
			if content, ok := part.Content.(string); ok {
				msg.Parts = append(msg.Parts, llmtypes.TextContent{Text: content})
			}
		case "tool_call":
			if contentMap, ok := part.Content.(map[string]interface{}); ok {
				toolCall := llmtypes.ToolCall{
					FunctionCall: &llmtypes.FunctionCall{}, // Initialize pointer
				}
				if id, ok := contentMap["id"].(string); ok {
					toolCall.ID = id
				}
				if fnName, ok := contentMap["function_name"].(string); ok {
					toolCall.FunctionCall.Name = fnName
				}
				if fnArgs, ok := contentMap["function_args"].(string); ok {
					toolCall.FunctionCall.Arguments = fnArgs
				}
				msg.Parts = append(msg.Parts, toolCall)
			}
		case "tool_response":
			if contentMap, ok := part.Content.(map[string]interface{}); ok {
				toolResp := llmtypes.ToolCallResponse{}
				if toolCallID, ok := contentMap["tool_call_id"].(string); ok {
					toolResp.ToolCallID = toolCallID
				}
				if content, ok := contentMap["content"].(string); ok {
					toolResp.Content = content
				}
				msg.Parts = append(msg.Parts, toolResp)
			}
		}
	}

	return msg
}

// handleSetLLMGuidance sets LLM guidance for a session
func (api *StreamingAPI) handleSetLLMGuidance(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	vars := mux.Vars(r)
	sessionID := vars["session_id"]
	if sessionID == "" {
		http.Error(w, "Session ID is required", http.StatusBadRequest)
		return
	}

	var req LLMGuidanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Validate session exists
	api.activeSessionsMux.RLock()
	session, exists := api.activeSessions[sessionID]
	api.activeSessionsMux.RUnlock()

	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Update guidance in activeSessions
	api.activeSessionsMux.Lock()
	session.LLMGuidance = req.Guidance
	session.LastActivity = time.Now()
	api.activeSessionsMux.Unlock()

	log.Printf("[LLM_GUIDANCE] Set guidance for session %s: %s", sessionID, req.Guidance)

	response := LLMGuidanceResponse{
		SessionID: sessionID,
		Status:    "success",
		Message:   "LLM guidance updated successfully",
		Guidance:  req.Guidance,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Compare current product setup to the admission of the actually launched
// agent. Saved runtime metadata may still belong to the preceding turn while
// the current turn streams; it is only a fallback after process restart.
func (api *StreamingAPI) agentProfileAllowsRetainedLiveInput(userID, sessionID, workspacePath string, hasProfile bool) bool {
	if api == nil || !hasProfile {
		return true
	}
	api.conversationMux.RLock()
	currentProfileKey, known := api.lastAgentProfileKeyBySession[sessionID]
	api.conversationMux.RUnlock()
	if !known || currentProfileKey == "" {
		return false
	}
	api.conversationMux.RLock()
	launched, launchKnown := api.launchedAgentProfileKeyBySession[sessionID]
	api.conversationMux.RUnlock()
	if launchKnown {
		return launched == currentProfileKey
	}

	runtime, ok, err := ReadChatHistoryRuntimeForSession(userID, sessionID, workspacePath)
	if err != nil || !ok || runtime == nil {
		log.Printf("[CHAT_HISTORY] Retained live input disabled for product session %s: current runtime fingerprint is unavailable", sessionID)
		return false
	}
	if strings.TrimSpace(runtime.AgentProfileKey) != currentProfileKey {
		log.Printf("[CHAT_HISTORY] Retained live input disabled for product session %s: agent profile definition changed", sessionID)
		return false
	}
	return true
}

// agentSupportsLiveInputDelivery reports whether the retained agent is a coding
// agent whose provider contract supports live input (tmux-transport CLIs). Only
// these short-circuit a /api/query into CLI delivery; API/LLM agents fall through
// to the normal new-turn path so their behavior is unchanged.
func agentSupportsLiveInputDelivery(agent *mcpagent.Agent) bool {
	if agent == nil {
		return false
	}
	runtime := mcpagent.ReadAgentRuntimeInfo(agent)
	contract, isCodingAgent := llm.GetCodingAgentProviderContract(mcpagent.ReadAgentRuntimeInfo(agent).Provider, runtime.LLMConfig.ModelID)
	return isCodingAgent && contract.SupportsLiveInput
}

// liveInputErrorProvesNoTarget reports whether a live-input Send error proves
// no CLI received the message: the retained target is gone (a pooled-session
// miss after a restart closed it, or a closed turn session). The caller falls
// through to a normal new turn instead of surfacing delivery_uncertain. Every
// tmux adapter emits the "no active X session registered for owner session"
// shape from its pooled-session lookup before touching tmux; note this is
// wider than mcpagent's unexported isInteractiveSessionNotRegistered, which
// misses claude-code's "tmux session" phrasing.
func liveInputErrorProvesNoTarget(err error) bool {
	if err == nil {
		return false
	}
	// A running agent with no turn in flight cannot take the message (its
	// steer queue drains only inside a turn), so start a new turn with it.
	var deliveryErr *mcpagent.CodingAgentDeliveryError
	if errors.As(err, &deliveryErr) && deliveryErr.Kind == mcpagent.DeliveryErrorKindNoSession {
		return true
	}
	return strings.Contains(err.Error(), "session registered for owner") ||
		strings.Contains(err.Error(), "session is closed")
}

// tryDeliverQueryAsLiveInput is the single-entry retained-CLI path for
// tmux-transport coding-agent input (see handleQuery). When the session still
// has a retained coding-agent object that supports native live input, it sends
// the incoming /api/query message to that CLI instead of requiring a separate
// foreground-turn/busy proof. Returns true if it handled and responded to the
// request; false lets handleQuery start a normal new turn.
//
// Delivery into a busy CLI can take minutes (pane retries, durable-ack
// budgets), but the gateway/CloudFront cut a request at 30-60s and show the
// user a hard failure for a message that may still land. So the send runs on
// a request-detached context and the HTTP reply waits at most
// liveInputFastResponseWindow: a send that finishes in time answers exactly as
// before; a slower one answers "accepted" (delivery_status "delivering") at
// once and records its real outcome later (journal + chat receipt).
func (api *StreamingAPI) tryDeliverQueryAsLiveInput(w http.ResponseWriter, r *http.Request, sessionID, message, queryID string, receivedAt ...time.Time) bool {
	if api == nil || strings.TrimSpace(message) == "" {
		return false
	}
	detached := r.WithContext(context.WithoutCancel(r.Context()))
	detached.Header = r.Header.Clone()
	recorded := &internalResponseCapture{header: http.Header{}}
	done := make(chan bool, 1)
	go func() {
		handled := false
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("[QUERY->LIVE] Delivery panicked for session %s: %v", sessionID, recovered)
				recorded = &internalResponseCapture{header: http.Header{}}
				writeSubmissionUncertain(recorded, detached.Header.Get("Idempotency-Key"))
				handled = true
			}
			done <- handled
		}()
		if api.internalLiveInputDeliver != nil {
			handled = api.internalLiveInputDeliver(recorded, detached, sessionID, message, queryID)
			return
		}
		handled = api.deliverQueryAsLiveInputNow(recorded, detached, sessionID, message, queryID, receivedAt...)
	}()
	timer := time.NewTimer(liveInputFastResponseWindow)
	defer timer.Stop()
	select {
	case handled := <-done:
		if handled {
			copyInternalResponse(w, recorded)
		}
		return handled
	case <-timer.C:
	}

	submission, _ := r.Context().Value(chatSubmissionContextKey{}).(chatSubmissionContext)
	clientMessageID := clientMessageIDFromContext(r.Context())
	log.Printf("[QUERY->LIVE] Delivery for session %s still running after %s; answering accepted and finishing in the background", sessionID, liveInputFastResponseWindow)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true, "status": "accepted", "session_id": sessionID, "query_id": queryID,
		"delivery_status": liveInputDeliveryInProgress,
		"message":         "Still delivering to the coding agent; the chat updates when it lands",
	})
	go func() {
		handled := <-done
		status := recorded.status
		if status == 0 {
			status = http.StatusOK
		}
		body := recorded.body.Bytes()
		failed := !handled || status >= 400 || submissionStateForResponse(status, body) == "delivery_uncertain"
		if failed {
			log.Printf("[QUERY->LIVE] Background delivery for session %s did not complete (handled=%t status=%d); marking uncertain", sessionID, handled, status)
			if !handled || status < 400 {
				recorded = &internalResponseCapture{header: http.Header{}}
				writeSubmissionUncertain(recorded, submission.ID)
				status, body = recorded.status, recorded.body.Bytes()
			}
			submission.handle.resolveLate(status, body)
			receiptID := clientMessageID
			if receiptID == "" {
				receiptID = queryID
			}
			api.recordLiveInputConfirmed(sessionID, "late-delivery-"+receiptID, string(llmtypes.DurableAckFailed), "", "", 0, clientMessageID)
			return
		}
		submission.handle.resolveLate(status, body)
		log.Printf("[QUERY->LIVE] Background delivery for session %s completed (status=%d)", sessionID, status)
	}()
	return true
}

// liveInputFastResponseWindow bounds how long a /query waits on live-input
// delivery before answering "accepted"; well under CloudFront's 30s origin
// timeout. A var so tests can shorten it.
var liveInputFastResponseWindow = 12 * time.Second

// liveInputDeliveryInProgress is the delivery_status of an early "accepted"
// reply whose send is still running in the background.
const liveInputDeliveryInProgress = "delivering"

// copyInternalResponse replays a buffered response onto the real writer.
func copyInternalResponse(w http.ResponseWriter, recorded *internalResponseCapture) {
	for key, values := range recorded.header {
		w.Header()[key] = values
	}
	status := recorded.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(recorded.body.Bytes())
}

// deliverQueryAsLiveInputNow performs the delivery synchronously; see
// tryDeliverQueryAsLiveInput for the response-time bound around it.
func (api *StreamingAPI) deliverQueryAsLiveInputNow(w http.ResponseWriter, r *http.Request, sessionID, message, queryID string, receivedAt ...time.Time) bool {
	requestReceivedAt := time.Now()
	if len(receivedAt) > 0 && !receivedAt[0].IsZero() {
		requestReceivedAt = receivedAt[0]
	}
	// The mcpagent Session, not this host's short-lived running-Agent map or a
	// provider-specific terminal branch, owns warm delivery. It survives the
	// wrapped turn and reports the transport that actually accepted the input.
	tryColdRetainedFallback := true
	if retainedSession, ok := mcpagent.LookupSession(sessionID); ok {
		if api.closeIdleRetainedTmuxSessionWithoutLiveTerminal(sessionID, retainedSession) {
			// Continue through cold-terminal compatibility and then the normal
			// new-turn path. No send was attempted, so delivery is not uncertain.
		} else {
			sessionInputCtx, sessionInputCancel := context.WithTimeout(r.Context(), liveCodingAgentDeliveryTimeout)
			delivery, err := retainedSession.Send(sessionInputCtx, message)
			sessionInputCancel()
			if err != nil {
				if liveInputErrorProvesNoTarget(err) {
					log.Printf("[QUERY->LIVE] No live target for session %s; starting a new turn: %v", sessionID, err)
					return false
				}
				log.Printf("[QUERY->LIVE] Delivery uncertain for session %s: %v", sessionID, err)
				writeSubmissionUncertain(w, r.Header.Get("Idempotency-Key"))
				return true
			} else if delivery.Status == mcpagent.UserMessageDeliveryStatusSentToCLI {
				provider := string(delivery.Provider)
				messageID := api.recordMCPAgentSessionLiveInput(sessionID, message, provider, clientMessageIDFromContext(r.Context()), queryID)
				api.persistLiveInputUserMessage(r.Context(), sessionID, message)
				w.Header().Set("Content-Type", "application/json")
				response := QueryResponse{
					QueryID: queryID, SessionID: sessionID,
					Status: queryStatusLiveInputDelivered, Message: "Delivered to retained coding-agent session",
					DeliveryStatus: string(delivery.Status), Provider: provider,
					DeliveryTransport: string(delivery.Transport), DeliverySource: queryDeliverySourceMCPAgentSession,
					MessageID: messageID,
				}
				api.stampCodingAgentDeliveryTiming(r, &response, requestReceivedAt)
				_ = json.NewEncoder(w).Encode(response)
				log.Printf("[QUERY->LIVE] Delivered /api/query through durable mcpagent session=%s provider=%s transport=%s: %.80s", sessionID, provider, delivery.Transport, message)
				return true
			} else {
				// Queueing is an accepted non-tmux submission. It is not evidence that
				// the durable session is broken, so do not bypass mcpagent via tmux.
				// Send accepted a non-CLI queue entry. Do not inject it again.
				log.Printf("[QUERY->LIVE] Queued /api/query for injection session=%s provider=%s transport=%s status=%s: %.80s", sessionID, delivery.Provider, delivery.Transport, delivery.Status, message)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "status": "accepted", "session_id": sessionID, "delivery_status": string(delivery.Status), "provider": string(delivery.Provider)})
				return true
			}
		}
	}
	if tryColdRetainedFallback {
		// Transitional cold-restart compatibility: a provider tmux from the
		// previous server process can outlive the in-memory Session registry. It
		// also recovers a stale/closed warm Session without paying for full Agent
		// reconstruction, preserving PLAT-102's latency guarantee.
		fallbackCtx, fallbackCancel := context.WithTimeout(r.Context(), liveCodingAgentDeliveryTimeout)
		retainedProvider, handled, err := api.deliverRetainedMainTerminalInput(fallbackCtx, sessionID, message)
		fallbackCancel()
		if handled {
			if err != nil {
				log.Printf("[QUERY->LIVE] Retained-terminal fallback uncertain for session %s: %v", sessionID, err)
				writeSubmissionUncertain(w, r.Header.Get("Idempotency-Key"))
				return true
			}
			messageID := api.recordRetainedTerminalLiveInput(sessionID, message, retainedProvider, clientMessageIDFromContext(r.Context()), queryID)
			api.persistLiveInputUserMessage(r.Context(), sessionID, message)
			w.Header().Set("Content-Type", "application/json")
			response := QueryResponse{QueryID: queryID, SessionID: sessionID, Status: queryStatusLiveInputDelivered, Message: "Delivered to retained coding-agent CLI", DeliveryStatus: "sent_to_cli", Provider: retainedProvider, DeliveryTransport: "tmux", DeliverySource: queryDeliverySourceRetainedCompatibility, MessageID: messageID}
			api.stampCodingAgentDeliveryTiming(r, &response, requestReceivedAt)
			_ = json.NewEncoder(w).Encode(response)
			log.Printf("[QUERY->LIVE] Delivered /api/query through retained-terminal fallback session=%s provider=%s transport=tmux: %.80s", sessionID, retainedProvider, message)
			return true
		}
	}

	api.runningAgentsMux.RLock()
	runningAgent := api.runningAgents[sessionID]
	api.runningAgentsMux.RUnlock()
	if !agentSupportsLiveInputDelivery(runningAgent) {
		return false
	}
	hasActiveForegroundTurn := api.hasActiveTurnCancel(sessionID)
	if !hasActiveForegroundTurn && !api.sessionHasLiveMainCodingTmux(sessionID) {
		log.Printf("[QUERY→LIVE] Retained CLI agent for session %s has no active turn and no live tmux; starting a new turn", sessionID)
		return false
	}

	if err := r.Context().Err(); err != nil {
		log.Printf("[QUERY→LIVE] Request canceled before delivery for session %s: %v", sessionID, err)
		return false
	}

	inputCtx, cancel := context.WithTimeout(r.Context(), liveCodingAgentDeliveryTimeout)
	defer cancel()
	delivery, err := api.deliverRunningAgentUserMessage(inputCtx, runningAgent, mcpagent.UserMessageDeliveryRequest{
		SessionID: sessionID,
		Message:   message,
		Intent:    mcpagent.UserMessageDeliveryIntentLiveInput,
	})
	if err != nil {
		if liveInputErrorProvesNoTarget(err) {
			log.Printf("[QUERY→LIVE] No live target for session %s; starting a new turn: %v", sessionID, err)
			return false
		}
		// Transport errors do not prove the CLI did not receive the input.
		// Keep the accepted submission uncertain instead of dispatching twice.
		log.Printf("[QUERY→LIVE] Delivery uncertain for session %s: %v", sessionID, err)
		writeSubmissionUncertain(w, r.Header.Get("Idempotency-Key"))
		return true
	}
	if delivery.DeliveryStatus == mcpagent.UserMessageDeliveryStatusQueuedForInjection {
		// Same accepted shape as the retained-session queue branch above:
		// the running turn owns this message at its next boundary.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "status": "accepted", "session_id": sessionID, "delivery_status": string(delivery.DeliveryStatus), "provider": string(delivery.Provider)})
		return true
	}
	if delivery.DeliveryStatus != mcpagent.UserMessageDeliveryStatusSentToCLI {
		log.Printf("[QUERY→LIVE] Provider did not confirm CLI submission for session %s status=%s", sessionID, delivery.DeliveryStatus)
		writeSubmissionUncertain(w, r.Header.Get("Idempotency-Key"))
		return true
	}

	messageID := newSteerMessageID()
	provider := string(delivery.Provider)
	if provider == "" {
		provider = string(mcpagent.ReadAgentRuntimeInfo(runningAgent).Provider)
	}
	deliveryStatus := string(delivery.DeliveryStatus)
	if deliveryStatus == "" {
		deliveryStatus = "queued_for_injection"
	}
	// Record the steered user message so it persists in the conversation timeline.
	// The chat UI dedups this against its optimistic bubble by exact content, so
	// there is no double message.
	if !hasActiveForegroundTurn {
		api.trackConversationTurnStart(queryID, sessionID, QueryRequest{Query: message, TriggeredBy: "interactive"})
		api.setSessionBusy(sessionID, false)
		api.markRetainedMainCodingTurnRunning(sessionID, queryID)
	}
	api.recordLiveCodingAgentUserMessage(sessionID, message, provider, messageID, deliveryStatus, clientMessageIDFromContext(r.Context()))
	api.persistLiveInputUserMessage(r.Context(), sessionID, message)
	log.Printf("[QUERY→LIVE] Delivered /api/query message to retained CLI for session %s status=%s: %.80s", sessionID, deliveryStatus, message)

	w.Header().Set("Content-Type", "application/json")
	response := QueryResponse{
		QueryID:           queryID,
		SessionID:         sessionID,
		Status:            queryStatusLiveInputDelivered,
		Message:           "Delivered to retained coding-agent CLI",
		DeliveryStatus:    deliveryStatus,
		Provider:          provider,
		DeliveryTransport: "tmux",
		DeliverySource:    queryDeliverySourceRunningAgent,
		MessageID:         messageID,
	}
	api.stampCodingAgentDeliveryTiming(r, &response, requestReceivedAt)
	_ = json.NewEncoder(w).Encode(response)
	return true
}

// handleLiveInputMessage delivers a user message to a session. Retained
// tmux-transport coding CLIs can accept the message directly; API/LLM agents
// still require a foreground turn or a normal next-turn fallback.
func (api *StreamingAPI) handleLiveInputMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	vars := mux.Vars(r)
	sessionID := vars["session_id"]
	if sessionID == "" {
		http.Error(w, "Session ID is required", http.StatusBadRequest)
		return
	}
	if !api.canAccessTerminalSession(r, sessionID) {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}
	if refusal := codeSessionTurnRefusal(sessionID, GetUserFromContext(r.Context())); refusal != "" {
		http.Error(w, refusal, http.StatusForbidden)
		return
	}

	var req LiveInputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Message == "" {
		http.Error(w, "Message is required", http.StatusBadRequest)
		return
	}

	if req.SubmissionID != "" && r.Header.Get("Idempotency-Key") == "" {
		r.Header.Set("Idempotency-Key", req.SubmissionID)
	}
	api.sessionWorkspaceMu.RLock()
	submissionProject, projectKnown := api.sessionWorkspaceFolders[sessionID]
	api.sessionWorkspaceMu.RUnlock()
	if !projectKnown {
		api.lastQueryMu.RLock()
		previousQuery, known := api.lastQueryRequests[sessionID]
		api.lastQueryMu.RUnlock()
		if known {
			submissionProject = previousQuery.SelectedFolder
			projectKnown = true
		}
	}
	if !projectKnown {
		r = r.WithContext(context.WithValue(r.Context(), chatSubmissionUnknownProjectKey{}, true))
	}
	var finishSubmission func()
	var accepted bool
	w, r, finishSubmission, accepted = api.beginChatSubmission(w, r, sessionID, submissionProject, req.Message)
	if !accepted {
		return
	}
	defer finishSubmission()

	api.lastQueryMu.RLock()
	policyRequest, policyKnown := api.lastQueryRequests[sessionID]
	api.lastQueryMu.RUnlock()
	// Live input is a turn too: re-check the session's account for the
	// person sending it (sharing changes apply from the next turn).
	if policyKnown {
		if provider, connectionID := queryTurnConnection(policyRequest); provider != "" && connectionID != "" {
			sender := GetUserIDFromContext(r.Context())
			scope := providerAccountScope{Principal: sender, WorkspacePath: policyRequest.SelectedFolder, Product: policyRequest.AgentProfileID}
			if _, accountErr := api.admitProviderAccount(r.Context(), scope, provider, connectionID); accountErr != nil {
				http.Error(w, accountErr.Error(), http.StatusForbidden)
				return
			}
		}
	}
	if !policyKnown && strings.HasPrefix(submissionProject, "Workflow/") {
		http.Error(w, "Workflow session configuration is unavailable; reopen the workflow chat", http.StatusConflict)
		return
	}
	policyRequest.ExternalBuilderOperationID = ""
	if policyKnown && api.externalBuilderOwnsSession(sessionID) {
		policyRequest.Query = req.Message
		policyRequest.Message = ""
		policyRequest.TriggeredBy = "interactive"
		policyRequest.IsAutoNotification = false
		policyRequest.DisableLiveInputDelivery = true
		if api.queueOccupiedConversationTurn(w, r, GetUserIDFromContext(r.Context()), sessionID, policyRequest) {
			return
		}
	}
	if policyKnown {
		compatible, policyErr := api.workflowRetainedPolicyCompatible(r.Context(), sessionID, policyRequest)
		if policyErr != nil {
			http.Error(w, policyErr.Error(), http.StatusForbidden)
			return
		}
		if !compatible {
			api.interruptWorkflowPolicySession(sessionID, policyRequest.Provider)
			r = r.WithContext(context.WithValue(r.Context(), workflowPolicyRefreshKey{}, true))
			if api.startNextTurnFromLiveInput(w, r, sessionID, req.Message, nil) {
				return
			}
			http.Error(w, "Unable to refresh workflow session", http.StatusConflict)
			return
		}
	}
	// /live-input is only a transport optimization; it must not bypass the
	// conversation dispatcher. Policy compatibility is checked first because a
	// changed Workflow authority must cancel/relaunch the stale runtime now,
	// rather than waiting behind it.
	if policyKnown && shouldUseDurableConversationTurnQueue(policyRequest) &&
		api.conversationTurnOccupied(sessionID) {
		queuedRequest := policyRequest
		queuedRequest.Query = req.Message
		queuedRequest.Message = ""
		queuedRequest.TriggeredBy = "interactive"
		queuedRequest.IsAutoNotification = false
		turn, position, queueErr := api.enqueueConversationTurn(r.Context(), GetUserIDFromContext(r.Context()), sessionID, queuedRequest)
		if queueErr != nil {
			http.Error(w, "Cannot durably queue conversation turn: "+queueErr.Error(), http.StatusServiceUnavailable)
			return
		}
		api.recordQueuedConversationUserMessage(sessionID, turn)
		api.kickConversationTurnQueue(sessionID)
		_ = json.NewEncoder(w).Encode(LiveInputResponse{
			Success: true, Message: "Queued behind the active conversation turn",
			DeliveryStatus: "queued_for_turn", MessageID: turn.ID, QueryID: turn.ID, QueuePosition: position,
		})
		return
	}

	// One durable mcpagent Session owns warm delivery across transports and
	// remains addressable after the wrapped Go turn has completed.
	tryColdRetainedFallback := true
	if retainedSession, ok := mcpagent.LookupSession(sessionID); ok {
		if api.closeIdleRetainedTmuxSessionWithoutLiveTerminal(sessionID, retainedSession) {
			// Continue through cold-terminal compatibility and then start the
			// resumed turn. No provider delivery was attempted.
		} else {
			sessionInputCtx, sessionInputCancel := context.WithTimeout(r.Context(), liveCodingAgentDeliveryTimeout)
			delivery, err := retainedSession.Send(sessionInputCtx, req.Message)
			sessionInputCancel()
			if err != nil {
				log.Printf("[LIVE INPUT] Delivery uncertain for session %s: %v", sessionID, err)
				writeSubmissionUncertain(w, r.Header.Get("Idempotency-Key"))
				return
			} else if delivery.Status == mcpagent.UserMessageDeliveryStatusSentToCLI {
				provider := string(delivery.Provider)
				messageID := api.recordMCPAgentSessionLiveInput(sessionID, req.Message, provider, clientMessageIDFromContext(r.Context()))
				writeRetainedTerminalLiveInputResponseWithMessageID(w, sessionID, provider, messageID)
				log.Printf("[LIVE INPUT] Delivered through durable mcpagent session=%s provider=%s transport=%s: %.80s", sessionID, provider, delivery.Transport, req.Message)
				api.persistLiveInputUserMessage(r.Context(), sessionID, req.Message)
				return
			} else {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(LiveInputResponse{Success: true, Message: "User message accepted by provider session", DeliveryStatus: string(delivery.Status), Provider: string(delivery.Provider)})
				return
			}
		}
	}
	if tryColdRetainedFallback {
		// Same cold-restart/stale-session compatibility as /api/query above.
		fallbackCtx, fallbackCancel := context.WithTimeout(r.Context(), liveCodingAgentDeliveryTimeout)
		retainedProvider, handled, err := api.deliverRetainedMainTerminalInput(fallbackCtx, sessionID, req.Message)
		fallbackCancel()
		if handled {
			if err != nil {
				writeSubmissionUncertain(w, r.Header.Get("Idempotency-Key"))
				return
			}
			writeRetainedTerminalLiveInputResponse(w, sessionID, req.Message, retainedProvider, clientMessageIDFromContext(r.Context()), api)
			api.persistLiveInputUserMessage(r.Context(), sessionID, req.Message)
			return
		}
	}

	// Look up the running agent for this session
	api.runningAgentsMux.RLock()
	runningAgent, exists := api.runningAgents[sessionID]
	api.runningAgentsMux.RUnlock()

	if !exists || runningAgent == nil {
		// There is no live, provider-addressable retained terminal. A normal new
		// turn is still appropriate when the previous pane truly exited.
		if api.startNextTurnFromLiveInput(w, r, sessionID, req.Message, nil) {
			return
		}
		http.Error(w, "No running agent for this session", http.StatusNotFound)
		return
	}
	hasActiveForegroundTurn := api.hasActiveTurnCancel(sessionID)
	if !hasActiveForegroundTurn && agentSupportsLiveInputDelivery(runningAgent) && !api.sessionHasLiveMainCodingTmux(sessionID) {
		log.Printf("[LIVE INPUT] Retained CLI agent for session %s has no active turn and no live tmux; starting next turn", sessionID)
		api.setSessionBusy(sessionID, false)
		if api.startNextTurnFromLiveInput(w, r, sessionID, req.Message, runningAgent) {
			return
		}
		http.Error(w, "No active foreground turn or live terminal for this session", http.StatusConflict)
		return
	}
	// Do not wait on the new-turn lane here. Live coding-agent input is ordered by
	// provider startup readiness and the per-tmux transaction broker; waiting on
	// request construction would turn a slow CLI launch into a blocked chat send.
	if !hasActiveForegroundTurn {
		// API/LLM agents still need a server-owned foreground turn. Retained
		// tmux-transport CLIs can accept the next message directly, whether their
		// pane currently looks busy or idle.
		if !agentSupportsLiveInputDelivery(runningAgent) {
			log.Printf("[LIVE INPUT] Rejecting stale live input for session %s: stored API/LLM agent has no foreground turn", sessionID)
			api.setSessionBusy(sessionID, false)
			hasRunningBackgroundAgents := api.bgAgentRegistry != nil && api.bgAgentRegistry.HasRunningAgents(sessionID)
			if !hasRunningBackgroundAgents && !api.isSyntheticTurn(sessionID) {
				api.updateSessionStatus(sessionID, "completed")
			}
			if api.startNextTurnFromLiveInput(w, r, sessionID, req.Message, runningAgent) {
				return
			}
			http.Error(w, "No active foreground turn for this session", http.StatusConflict)
			return
		}
		log.Printf("[LIVE INPUT] No foreground turn for session %s — delivering message to retained CLI", sessionID)
		api.setSessionBusy(sessionID, false)
	}

	if err := r.Context().Err(); err != nil {
		log.Printf("[LIVE INPUT] Request canceled before delivery for session %s: %v", sessionID, err)
		return
	}

	inputCtx, cancel := context.WithTimeout(r.Context(), liveCodingAgentDeliveryTimeout)
	defer cancel()
	delivery, err := api.deliverRunningAgentUserMessage(inputCtx, runningAgent, mcpagent.UserMessageDeliveryRequest{
		SessionID: sessionID,
		Message:   req.Message,
		Intent:    mcpagent.UserMessageDeliveryIntentLiveInput,
	})
	if err != nil {
		log.Printf("[LIVE INPUT] Live input unavailable for provider %s session %s: %v", mcpagent.ReadAgentRuntimeInfo(runningAgent).Provider, sessionID, err)
		// The agent refused because no turn is running to take the message:
		// nothing was delivered, so this is definite, not uncertain. Start
		// the next turn with it, as /api/query does (liveInputErrorProvesNoTarget).
		if liveInputErrorProvesNoTarget(err) {
			if api.startNextTurnFromLiveInput(w, r, sessionID, req.Message, runningAgent) {
				return
			}
			http.Error(w, "No active foreground turn for this session", http.StatusConflict)
			return
		}
		writeSubmissionUncertain(w, r.Header.Get("Idempotency-Key"))
		return
	}
	if agentSupportsLiveInputDelivery(runningAgent) && delivery.DeliveryStatus != mcpagent.UserMessageDeliveryStatusSentToCLI {
		log.Printf("[LIVE INPUT] Provider did not confirm CLI submission for session %s status=%s", sessionID, delivery.DeliveryStatus)
		writeSubmissionUncertain(w, r.Header.Get("Idempotency-Key"))
		return
	}

	messageID := newSteerMessageID()
	provider := string(delivery.Provider)
	if provider == "" {
		provider = string(mcpagent.ReadAgentRuntimeInfo(runningAgent).Provider)
	}
	deliveryStatus := string(delivery.DeliveryStatus)
	if deliveryStatus == "" {
		deliveryStatus = "queued_for_injection"
	}
	api.recordLiveCodingAgentUserMessage(sessionID, req.Message, provider, messageID, deliveryStatus, clientMessageIDFromContext(r.Context()))
	api.persistLiveInputUserMessage(r.Context(), sessionID, req.Message)
	if !hasActiveForegroundTurn {
		executionID := "live-turn:" + messageID
		api.trackConversationTurnStart(executionID, sessionID, QueryRequest{Query: req.Message, TriggeredBy: "interactive"})
		api.markRetainedMainCodingTurnRunning(sessionID, executionID)
	}
	log.Printf("[LIVE INPUT] Delivered user message to provider %s session %s status=%s: %.80s", provider, sessionID, deliveryStatus, req.Message)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(LiveInputResponse{
		Success:        true,
		Message:        "User message delivered",
		DeliveryStatus: deliveryStatus,
		Provider:       provider,
		MessageID:      messageID,
	})
}

func newSteerMessageID() string {
	return fmt.Sprintf("steer-message-%d", time.Now().UnixNano())
}

func (api *StreamingAPI) deliverRunningAgentUserMessage(ctx context.Context, runningAgent *mcpagent.Agent, req mcpagent.UserMessageDeliveryRequest) (mcpagent.UserMessageDeliveryResult, error) {
	if api.internalUserMessageDeliveryHandler != nil {
		return api.internalUserMessageDeliveryHandler(ctx, runningAgent, req)
	}
	return mcpagent.DeliverAgentInput(ctx, runningAgent, req)
}

type internalResponseCapture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

// queryRequestForContinuation restores the product-level mode after handleQuery
// has adapted a workflow-builder request to the shared multi-agent engine. The
// stored request is later used by /live-input when a retained CLI object is
// between turns. Persisting the adapted "multi-agent" value silently restarted
// workflow follow-ups in _users/default/Chats instead of their workflow folder.
func queryRequestForContinuation(req QueryRequest, isWorkflowPhase bool, workflowPhaseFolder string) QueryRequest {
	if !isWorkflowPhase {
		return req
	}
	req.AgentMode = "workflow_phase"
	if strings.TrimSpace(req.SelectedFolder) == "" {
		req.SelectedFolder = strings.TrimSpace(workflowPhaseFolder)
	}
	return req
}

// queryRequestWithEffectiveRuntime records the provider/model that actually
// launched the retained coding CLI. The incoming request may carry tab state
// from before Update Automation changed workflow.json, while finalProvider and
// finalModelID have already been resolved from the current manifest.
func queryRequestWithEffectiveRuntime(req QueryRequest, provider, modelID string) QueryRequest {
	provider = strings.TrimSpace(provider)
	modelID = strings.TrimSpace(modelID)
	if provider == "" && modelID == "" {
		return req
	}
	req.Provider = provider
	req.ModelID = modelID
	if req.LLMConfig != nil {
		configCopy := *req.LLMConfig
		configCopy.Primary = req.LLMConfig.Primary
		configCopy.Primary.Provider = provider
		configCopy.Primary.ModelID = modelID
		req.LLMConfig = &configCopy
	}
	return req
}

func (c *internalResponseCapture) Header() http.Header {
	if c.header == nil {
		c.header = make(http.Header)
	}
	return c.header
}

func (c *internalResponseCapture) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}

func (c *internalResponseCapture) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.body.Write(p)
}

func (api *StreamingAPI) startNextTurnFromLiveInput(w http.ResponseWriter, r *http.Request, sessionID, message string, runningAgent *mcpagent.Agent) bool {
	api.lastQueryMu.RLock()
	baseReq, ok := api.lastQueryRequests[sessionID]
	api.lastQueryMu.RUnlock()
	if !ok {
		return false
	}

	if refresh, _ := r.Context().Value(workflowPolicyRefreshKey{}).(bool); refresh {
		baseReq.DisableLiveInputDelivery = true
	}
	baseReq.Query = message
	baseReq.Message = message
	baseReq.IsAutoNotification = false
	baseReq.userID = GetUserIDFromContext(r.Context())

	if workflowBusyGuardApplies(sessionID, baseReq) {
		if running := api.findRunningTrackedExecutionForWorkspaceWhere(baseReq.SelectedFolder, func(exec *TrackedWorkflowExecution) bool {
			return trackedExecutionBlocksNewWorkflowBuilderChat(exec)
		}); running != nil && running.SessionID != sessionID {
			log.Printf("[LIVE INPUT] Refusing queued next turn for session %s: workflow builder is busy with session %s", sessionID, running.SessionID)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error":          "workflow_busy",
				"message":        "Workflow builder chat is already running on this workflow. Stop the running chat before starting a new one.",
				"workspace_path": running.WorkspacePath,
				"running": map[string]interface{}{
					"session_id":   running.SessionID,
					"execution_id": running.ExecutionID,
					"triggered_by": running.TriggeredBy,
					"source":       running.Source,
					"started_at":   running.StartedAt,
					"phase_id":     running.PhaseID,
					"phase_name":   running.PhaseName,
					"title":        running.Title,
				},
			})
			return true
		}
	}

	payload, err := json.Marshal(baseReq)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to prepare next turn: %v", err), http.StatusInternalServerError)
		return true
	}

	// The live-input request must be free to return as soon as the next turn is
	// accepted. The queued turn can legitimately outlive that request while it
	// waits for the current per-session input lane, so preserve request values
	// without inheriting its cancellation or deadline.
	nextTurnCtx := context.WithoutCancel(r.Context())
	nextReq, err := http.NewRequestWithContext(nextTurnCtx, http.MethodPost, "/api/query", bytes.NewReader(payload))
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to prepare next turn request: %v", err), http.StatusInternalServerError)
		return true
	}
	nextReq.Header = r.Header.Clone()
	nextReq.Header.Set("Content-Type", "application/json")
	nextReq.Header.Set("X-Session-ID", sessionID)

	provider := ""
	if runningAgent != nil {
		provider = string(mcpagent.ReadAgentRuntimeInfo(runningAgent).Provider)
	}
	if provider == "" {
		provider = baseReq.Provider
	}
	messageID := newSteerMessageID()

	// handleQuery takes ownership of the session input lane for the lifetime of
	// a turn. Calling it inline here made POST /live-input wait for the previous
	// turn to finish (73 seconds in the observed regression), leaving the draft
	// stuck on "Sending" and allowing duplicate submissions. Dispatch the
	// server-owned continuation independently and acknowledge it immediately.
	go func() {
		recorder := &internalResponseCapture{header: make(http.Header)}
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("[LIVE INPUT] Queued next turn panicked for session %s message_id=%s: %v", sessionID, messageID, recovered)
			}
		}()
		if api.internalQueryHandler != nil {
			api.internalQueryHandler(recorder, nextReq)
		} else {
			api.handleQuery(recorder, nextReq)
		}
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		if status >= http.StatusBadRequest {
			body := strings.TrimSpace(recorder.body.String())
			if body == "" {
				body = http.StatusText(status)
			}
			log.Printf("[LIVE INPUT] Queued next turn failed for session %s message_id=%s status=%d: %s", sessionID, messageID, status, body)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(LiveInputResponse{
		Success:        true,
		Message:        "Started next turn",
		DeliveryStatus: "next_turn_started",
		Provider:       provider,
		MessageID:      messageID,
	})
	return true
}

// handleControlKey injects a tmux control key (e.g. "Escape", "Enter", "Up",
// "Down") into a coding-agent session. The foreground Agent is preferred while
// a turn is running. Between turns, the interactive tmux process can remain
// alive after runningAgents has been cleared, so fall back to the terminal store.
func (api *StreamingAPI) handleControlKey(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	vars := mux.Vars(r)
	sessionID := vars["session_id"]
	if sessionID == "" {
		http.Error(w, "Session ID is required", http.StatusBadRequest)
		return
	}

	var req ControlKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(req.Key)
	if key == "" {
		http.Error(w, "Key is required", http.StatusBadRequest)
		return
	}
	if !llm.IsAllowedCodingAgentControlKey(key) {
		http.Error(w, fmt.Sprintf("Key %q is not allowed", key), http.StatusBadRequest)
		return
	}

	api.runningAgentsMux.RLock()
	runningAgent, exists := api.runningAgents[sessionID]
	api.runningAgentsMux.RUnlock()

	if err := r.Context().Err(); err != nil {
		log.Printf("[CONTROL] Request canceled before delivery for session %s: %v", sessionID, err)
		return
	}

	ctlCtx, cancel := context.WithTimeout(r.Context(), liveCodingAgentInputTimeout)
	defer cancel()
	if !exists || runningAgent == nil {
		provider, tmuxSession, err := api.deliverControlKeyToLiveMainTerminal(ctlCtx, sessionID, key)
		if err != nil {
			log.Printf("[CONTROL] Terminal fallback key %q unavailable for session %s tmux=%s: %v", key, sessionID, tmuxSession, err)
			http.Error(w, fmt.Sprintf("Control key unavailable: %v", err), http.StatusConflict)
			return
		}
		if tmuxSession == "" {
			http.Error(w, "No live coding-agent terminal for this session", http.StatusNotFound)
			return
		}
		log.Printf("[CONTROL] Delivered control key %q to completed live terminal session %s tmux=%s", key, sessionID, tmuxSession)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ControlKeyResponse{
			Success:  true,
			Message:  "Control key delivered to live terminal",
			Provider: provider,
			Key:      key,
		})
		return
	}

	result, err := mcpagent.DeliverAgentControlKey(ctlCtx, runningAgent, mcpagent.ControlKeyDeliveryRequest{
		SessionID: sessionID,
		Key:       key,
	})
	if err != nil {
		log.Printf("[CONTROL] Control key %q unavailable for provider %s session %s: %v", key, mcpagent.ReadAgentRuntimeInfo(runningAgent).Provider, sessionID, err)
		http.Error(w, fmt.Sprintf("Control key unavailable: %v", err), http.StatusConflict)
		return
	}

	provider := string(result.Provider)
	if provider == "" {
		provider = string(mcpagent.ReadAgentRuntimeInfo(runningAgent).Provider)
	}
	log.Printf("[CONTROL] Delivered control key %q to provider %s session %s", key, provider, sessionID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ControlKeyResponse{
		Success:  true,
		Message:  "Control key delivered",
		Provider: provider,
		Key:      key,
	})
}

// deliverControlKeyToLiveMainTerminal bridges the between-turn gap where the
// agent object is no longer registered but its interactive CLI pane is still
// alive. ListRaw retains process ownership independently of UI visibility.
func (api *StreamingAPI) deliverControlKeyToLiveMainTerminal(ctx context.Context, sessionID, key string) (provider, tmuxSession string, err error) {
	if api == nil || api.terminalStore == nil {
		return "", "", nil
	}
	for _, snapshot := range api.terminalStore.ListRaw(sessionID) {
		if !codingAgentSnapshotIsMainAgent(snapshot) {
			continue
		}
		tmuxSession = strings.TrimSpace(snapshot.TmuxSession)
		if tmuxSession == "" {
			continue
		}
		processState := strings.ToLower(strings.TrimSpace(snapshot.ProcessState))
		if processState == "closed" || processState == "stale" {
			continue
		}
		if err := sendTerminalKey(ctx, tmuxSession, key); err != nil {
			return "", tmuxSession, err
		}
		return strings.TrimSpace(snapshot.Status.ProviderLabel), tmuxSession, nil
	}
	return "", "", nil
}

func (api *StreamingAPI) recordLiveCodingAgentUserMessage(sessionID, message, provider, messageID, deliveryStatus, clientMessageID string) {
	message = strings.TrimSpace(message)
	if sessionID == "" || message == "" || api == nil || api.eventStore == nil {
		return
	}

	eventData := unifiedevents.NewUserMessageEvent(0, message, "user")
	eventData.Metadata = map[string]interface{}{
		"source":          "coding_agent_live_input",
		"provider":        provider,
		"message_id":      messageID,
		"delivery_status": deliveryStatus,
	}
	eventID := messageID
	if clientMessageID != "" {
		eventData.Metadata["client_message_id"] = clientMessageID
		eventID = clientUserMessageEventID(clientMessageID)
	}
	agentEvent := unifiedevents.NewAgentEvent(eventData)
	agentEvent.SessionID = sessionID
	agentEvent.Component = "coding_agent_live_input"

	event := events.Event{
		ID:        eventID,
		Type:      string(unifiedevents.UserMessage),
		Timestamp: time.Now(),
		Data:      agentEvent,
		SessionID: sessionID,
	}
	// Every accepted tmux live delivery funnels through here (running
	// agent, durable mcpagent session, cold-retained compatibility, and
	// background-agent steers), so the durability watch starts here too:
	// one choke point no future path can miss. When the provider can confirm
	// the CLI took the message, the row is recorded at that point so it sits
	// after any answer the CLI was still writing when the message was sent.
	if deliveryStatus == string(mcpagent.UserMessageDeliveryStatusSentToCLI) {
		if liveInputHasDurableAck(provider) {
			api.watchLiveInputDurableRecording(sessionID, provider, messageID, message, clientMessageID, &event)
			return
		}
		api.eventStore.AddEvent(sessionID, event)
		return
	}
	api.eventStore.AddEvent(sessionID, event)
}

// handleSubmitHumanFeedback handles human feedback submission
func (api *StreamingAPI) handleSubmitHumanFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	var req HumanFeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.UniqueID == "" {
		http.Error(w, "unique_id is required", http.StatusBadRequest)
		return
	}

	if req.Response == "" {
		http.Error(w, "response is required", http.StatusBadRequest)
		return
	}

	// Get human feedback store and submit response
	feedbackStore := virtualtools.GetHumanFeedbackStore()
	if err := feedbackStore.SubmitResponse(req.UniqueID, req.Response); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Responses may contain OTPs or other private input; never include the value
	// in application logs.
	log.Printf("[HUMAN_FEEDBACK] Submitted response for unique_id %s", req.UniqueID)

	response := HumanFeedbackResponse{
		UniqueID: req.UniqueID,
		Status:   "success",
		Message:  "Human feedback submitted successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleListPendingHumanFeedback exposes the authoritative in-memory queue so
// urgent prompts are visible even when their background session/tree is not
// currently mounted in the frontend.
func (api *StreamingAPI) handleListPendingHumanFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"requests": virtualtools.GetHumanFeedbackStore().ListPending(time.Now()),
	})
}

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
	sb.WriteString(fmt.Sprintf("**%d variable groups** available:\n", len(manifest.Groups)))
	for _, g := range manifest.Groups {
		status := "enabled"
		if !g.Enabled {
			status = "disabled"
		}
		// Mark the user-selected group
		selected := ""
		for _, eid := range enabledGroupNames {
			if eid == g.Name {
				selected = " **[SELECTED]**"
				break
			}
		}
		sb.WriteString(fmt.Sprintf("- **%s** (group_name: `%s`, %s)%s\n", g.Name, g.Name, status, selected))
	}

	if selectedRunFolder != "" {
		sb.WriteString(fmt.Sprintf("\nSelected run folder: `%s`\n", selectedRunFolder))
	}

	if len(enabledGroupNames) > 0 {
		sb.WriteString(fmt.Sprintf("\nUser has selected group(s): %v — use these as default for execute_step calls.\n", enabledGroupNames))
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
	if req.ExecutionOptions != nil && len(req.ExecutionOptions.EnabledGroupNames) > 0 {
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
	}

	// Build base tools with session-aware workspace executors from the start.
	// This ensures MCP_API_URL in shell commands includes the session path prefix
	// (/s/{session_id}/...) so per-tool HTTP calls from inside Docker hit the
	// session-scoped route and get the correct executor.
	allTools, allExecutors, toolCategories := createCustomTools(true, currentUserID, sessionID)
	api.guardPulseResultExecutor(allExecutors, sessionID)

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
			cfg.SelectedServers = append([]string(nil), caps.SelectedServers...)
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
			workshopConfiguredCDPPorts := configuredCDPPortsForMode(configuredBrowserMode, req.CdpPort, append(append([]int{}, req.CdpPorts...), caps.CDPPorts...))
			cfg.BrowserRuntime = browser.NewBrowserRuntimeConfig(configuredBrowserMode, workshopConfiguredCDPPorts)
			if configuredBrowserMode != "" {
				common.SetSessionBrowserMode(sessionID, configuredBrowserMode)
			}
			if workspacePath != "" {
				browserCategory := virtualtools.GetWorkspaceBrowserToolCategory()
				browserTools := virtualtools.CreateWorkspaceBrowserTools()
				browserExecutors := workflowBrowserExecutors(sessionID, workspacePath, ReadWorkflowManifest)
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
				cfg.PresetPhaseLLM, cfg.TieredConfig = workshopResolveLLMConfig(llmCfg)
				cfg.PresetPulseLLM = workshopResolvePulseLLMConfig(llmCfg)

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

				log.Printf("[WORKSHOP] LLM config loaded: phase=%v pulse=%v tiered=%v kb=%v",
					cfg.PresetPhaseLLM != nil, cfg.PresetPulseLLM != nil, cfg.TieredConfig != nil, cfg.UseKnowledgebase)
			}
		}
	}

	// Merge secrets — use preset's global secret selection if available (phase chat doesn't send req.SelectedGlobalSecrets)
	effectiveGlobalSecretSelection := req.SelectedGlobalSecrets
	if presetGlobalSecretNames != nil {
		effectiveGlobalSecretSelection = presetGlobalSecretNames
	}
	scopedSecrets := req.DecryptedSecrets
	if workspacePath != "" {
		if manifest, found, err := ReadWorkflowManifest(context.Background(), workspacePath); err == nil && found {
			scopedSecrets = api.loadSelectedSecrets(context.Background(), currentUserID, workspacePath, manifest.Capabilities.SelectedSecrets)
		}
	}
	allSecrets := mergeGlobalSecrets(scopedSecrets, effectiveGlobalSecretSelection)
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
	api.setPlaywrightExecutionContext(workspaceEnv, sessionID, req)
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

	// Builder-run crew steps (execute_step, run_full_workflow) resolve their
	// runner from the session config; without it they fail as unbound even
	// on fully authenticated runs.
	cfg.CrewRunner = crewRunnerForRun(ctx, api.productSchedules)

	cfg.ScheduleCollisionCheck = api.scheduleCollisionCheck(cfg.WorkspacePath, sessionID, req.TriggeredBy)
	cfg.SkillFuncs = api.buildSkillCallbacks()
	cfg.LLMToolsFuncs = api.buildLLMToolsCallbacks()
	cfg.ListAvailableSecrets = func(ctx context.Context) ([]string, error) {
		nameSet := make(map[string]bool)
		// Global secrets from env vars
		for _, gs := range getGlobalSecrets() {
			nameSet[gs.Name] = true
		}
		if workspacePath != "" {
			workflowSecrets, err := api.chatStore.ListWorkflowSecrets(ctx, currentUserID, workspacePath)
			if err == nil {
				for _, ws := range workflowSecrets {
					nameSet[ws.Name] = true
				}
			}
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
		out := make(map[string]string, len(names))
		wanted := make(map[string]bool, len(names))
		for _, n := range names {
			wanted[n] = true
		}
		// Globals first — they set the baseline. Workflow secrets can override by same name.
		for _, gs := range getGlobalSecrets() {
			if wanted[gs.Name] {
				out[gs.Name] = gs.Value
			}
		}
		decrypted := api.loadSelectedSecrets(ctx, currentUserID, workspacePath, names)
		for _, s := range decrypted {
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

// buildSchedulerCallbacks creates SchedulerCallbacks that bridge the workshop tools
// to the workflow.json manifest and scheduler service. No database dependency.
func (api *StreamingAPI) buildSchedulerCallbacks() *todo_creation_human.SchedulerCallbacks {
	return &todo_creation_human.SchedulerCallbacks{
		GetContractUpgrades: func(ctx context.Context, workspacePath string) (string, error) {
			return describeWorkflowContractUpgrades(ctx, workspacePath)
		},
		NextContractUpgrade: func(ctx context.Context, workspacePath string) (string, string, error) {
			return nextWorkflowContractUpgrade(ctx, workspacePath)
		},
		ListSchedules: func(ctx context.Context, workspacePath string) (string, error) {
			manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
			if err != nil || !found {
				return "No workflow manifest found.", nil
			}
			if len(manifest.Schedules) == 0 {
				return "No schedules found for this workflow.", nil
			}
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("## Schedules (%d found)\n\n", len(manifest.Schedules)))
			for _, sched := range manifest.Schedules {
				status := "disabled"
				if sched.Enabled {
					status = "enabled"
				}
				scheduleType := scheduleTypeOrDefault(sched.ScheduleType)
				mode := scheduleModeOrDefault(sched.Mode)
				workshopMode := strings.TrimSpace(sched.WorkshopMode)
				if workshopMode == "" {
					workshopMode = "run"
				}
				pulseMode := manifest.EffectivePulseMode(sched)
				sb.WriteString(fmt.Sprintf("### %s\n", sched.Name))
				sb.WriteString(fmt.Sprintf("- **ID**: `%s`\n", sched.ID))
				sb.WriteString(fmt.Sprintf("- **Type**: %s\n", scheduleType))
				sb.WriteString(fmt.Sprintf("- **Mode**: `%s`\n", mode))
				sb.WriteString(fmt.Sprintf("- **Workshop Mode**: `%s`\n", workshopMode))
				if scheduleType == "calendar" {
					sb.WriteString(fmt.Sprintf("- **Calendar Items**: %d\n", len(sched.CalendarItems)))
				} else {
					sb.WriteString(fmt.Sprintf("- **Cron**: `%s`\n", sched.CronExpression))
				}
				sb.WriteString(fmt.Sprintf("- **Timezone**: %s\n", sched.Timezone))
				sb.WriteString(fmt.Sprintf("- **Status**: %s\n", status))
				sb.WriteString(fmt.Sprintf("- **Pulse**: %s", pulseMode))
				if strings.TrimSpace(sched.PulseMode) == "" {
					sb.WriteString(" (workflow default)\n")
				} else {
					sb.WriteString(" (schedule override)\n")
				}
				if api.scheduler != nil {
					state := api.scheduler.GetRuntimeStateForWorkflow(workspacePath, sched.ID)
					if state.LastStatus != "" {
						sb.WriteString(fmt.Sprintf("- **Last Run**: %v (status: %s)\n", state.LastRunAt, state.LastStatus))
					}
					if state.NextRunAt != nil {
						sb.WriteString(fmt.Sprintf("- **Next Run**: %v\n", state.NextRunAt))
					}
					sb.WriteString(fmt.Sprintf("- **Run Count**: %d\n", state.RunCount))
				}
				if len(sched.GroupNames) > 0 {
					sb.WriteString(fmt.Sprintf("- **Groups**: %v\n", sched.GroupNames))
				} else {
					sb.WriteString("- **Groups**: all\n")
				}
				sb.WriteString(fmt.Sprintf("- **Pulse**: %s\n- **Pulse reason**: %s\n", manifest.EffectivePulseMode(sched), sched.PulseModeReason))
				if len(sched.RouteSelections) > 0 {
					sb.WriteString(fmt.Sprintf("- **Route selections**: %v\n", sched.RouteSelections))
				}
				if scheduleType == "webhook" {
					sb.WriteString(fmt.Sprintf("- **Delivery concurrency**: parallel, up to %d active deliveries (server-enforced)\n", maxWebhookConcurrency))
					sb.WriteString("- **Overflow policy**: retryable busy response (HTTP 503 with Retry-After)\n")
				} else {
					if strings.TrimSpace(sched.CollisionPolicy) != "" {
						sb.WriteString(fmt.Sprintf("- **Collision policy**: %s\n", sched.CollisionPolicy))
					}
					concurrencyMode := strings.TrimSpace(sched.ConcurrencyMode)
					if concurrencyMode == "" {
						concurrencyMode = "sequential"
					}
					sb.WriteString(fmt.Sprintf("- **Concurrency mode**: %s\n", concurrencyMode))
				}
				if sched.MaxStartDelayMinutes > 0 {
					sb.WriteString(fmt.Sprintf("- **Maximum start delay**: %d minutes\n", sched.MaxStartDelayMinutes))
				}
				if dependencyIDs := scheduleDependencyIDs(sched); len(dependencyIDs) > 0 {
					sb.WriteString(fmt.Sprintf("- **Waits for schedules**: %v\n", dependencyIDs))
					dependencyRelease := strings.TrimSpace(sched.AfterTerminalStatus)
					if dependencyRelease == "" {
						dependencyRelease = "completed"
					}
					sb.WriteString(fmt.Sprintf("- **Dependency release**: %s", dependencyRelease))
					if sched.AfterDelayMinutes > 0 {
						sb.WriteString(fmt.Sprintf(" + %d minute delay", sched.AfterDelayMinutes))
					}
					if strings.TrimSpace(sched.DependencyDeadline) != "" {
						sb.WriteString(fmt.Sprintf("; deadline %s local", sched.DependencyDeadline))
					}
					sb.WriteString("\n")
				}
				if strings.TrimSpace(sched.DirectMessagesReason) != "" {
					sb.WriteString(fmt.Sprintf("- **Direct message rationale**: %s\n", sched.DirectMessagesReason))
				}
				sb.WriteString("\n")
			}
			return sb.String(), nil
		},
		CreateSchedule: func(ctx context.Context, workspacePath, name, cronExpr, timezone string, groupNames []string, routeSelections map[string]string, mode string, messages []string, directMessagesReason string, workshopMode string, resumePrevious *bool, pulseReviewOnly bool, policy todo_creation_human.ScheduleRuntimePolicy) (string, error) {
			mode = scheduleModeOrDefault(mode)
			if mode == "multi-agent" {
				return "", fmt.Errorf("workflow schedules must use workflow mode")
			}
			if err := ValidateCronExpression(cronExpr); err != nil {
				return "", fmt.Errorf("invalid cron expression %q: %w", cronExpr, err)
			}
			if err := ValidateScheduleTimezone(timezone); err != nil {
				return "", err
			}
			if err := validateScheduleMessages(messages, directMessagesReason); err != nil {
				return "", err
			}
			manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
			if err != nil || !found {
				return "", fmt.Errorf("workflow manifest not found at %s", workspacePath)
			}
			// PLAT-115: a PulseReviewOnly schedule never runs the workflow, so it
			// has no group to validate — the same reason the manual "Run Pulse
			// now" trigger's synthetic schedule (manualWorkflowPulseScheduleID)
			// carries no GroupNames either.
			if !pulseReviewOnly {
				groupNames, err = validateScheduleGroupNamesForWorkspace(ctx, workspacePath, groupNames)
				if err != nil {
					return "", err
				}
			}
			newSched := WorkflowSchedule{
				ID:                       generateScheduleID(),
				Name:                     name,
				CronExpression:           cronExpr,
				Timezone:                 timezone,
				GroupNames:               groupNames,
				RouteSelections:          routeSelections,
				Enabled:                  true,
				Mode:                     mode,
				Messages:                 messages,
				DirectMessagesReason:     directMessagesReason,
				WorkshopMode:             workshopMode,
				ResumePrevious:           resumePrevious,
				PulseReviewOnly:          pulseReviewOnly,
				PulseMode:                strings.ToLower(strings.TrimSpace(policy.PulseMode)),
				PulseModeReason:          strings.TrimSpace(policy.PulseModeReason),
				ExecutionMode:            strings.TrimSpace(policy.ExecutionMode),
				CollisionPolicy:          strings.TrimSpace(policy.CollisionPolicy),
				ConcurrencyMode:          strings.TrimSpace(policy.ConcurrencyMode),
				ParallelRiskAcknowledged: policy.ParallelRiskAcknowledged,
				MaxStartDelayMinutes:     policy.MaxStartDelayMinutes,
				AfterScheduleID:          strings.TrimSpace(policy.AfterScheduleID),
				AfterScheduleIDs:         normalizeScheduleDependencyIDs(policy.AfterScheduleIDs),
				AfterTerminalStatus:      strings.TrimSpace(policy.AfterTerminalStatus),
				AfterDelayMinutes:        policy.AfterDelayMinutes,
				DependencyDeadline:       strings.TrimSpace(policy.DependencyDeadline),
			}
			if err := validateScheduleRuntimePolicy(newSched); err != nil {
				return "", err
			}
			if err := schedulepolicy.ValidatePulse(newSched.PulseMode, newSched.PulseModeReason); err != nil {
				return "", err
			}
			manifest.Schedules = append(manifest.Schedules, newSched)
			if err := ValidateManifest(manifest); err != nil {
				return "", err
			}
			if err := WriteWorkflowManifest(ctx, workspacePath, manifest); err != nil {
				return "", fmt.Errorf("failed to write manifest: %w", err)
			}
			// Load into gocron scheduler
			if api.scheduler != nil {
				sctx := buildScheduleContext(workspacePath, manifest, newSched)
				if err := api.scheduler.LoadSchedule(sctx); err != nil {
					return fmt.Sprintf("Schedule created (ID: %s) but failed to activate: %v", newSched.ID, err), nil
				}
			}
			nextRun := getNextRunTime(cronExpr, timezone)
			nextRunStr := "unknown"
			if nextRun != nil {
				nextRunStr = nextRun.Format(time.RFC3339)
			}
			result := fmt.Sprintf("Schedule created and activated.\n- **ID**: `%s`\n- **Name**: %s\n- **Cron**: `%s`\n- **Timezone**: %s\n- **Next Run**: %s", newSched.ID, name, cronExpr, timezone, nextRunStr)
			if advisory := scheduleMessagesAdvisory(messages, directMessagesReason); advisory != "" {
				result += "\n- **Execution model**: " + advisory
			}
			return result, nil
		},
		CreateCalendarSchedule: func(ctx context.Context, workspacePath, name, timezone string, groupNames []string, calendarItemsJSON string, mode string, messages []string, directMessagesReason string, workshopMode string, policy todo_creation_human.ScheduleRuntimePolicy) (string, error) {
			mode = scheduleModeOrDefault(mode)
			if mode == "multi-agent" {
				return "", fmt.Errorf("workflow calendar schedules must use workshop mode")
			}
			if err := ValidateScheduleTimezone(timezone); err != nil {
				return "", err
			}
			var calendarItems []CalendarScheduleItem
			if err := json.Unmarshal([]byte(calendarItemsJSON), &calendarItems); err != nil {
				return "", fmt.Errorf("invalid calendar_items JSON: %w", err)
			}
			calendarItems = normalizeCalendarScheduleItems(calendarItems)
			if err := validateScheduleRequest("calendar", "", calendarItems); err != nil {
				return "", err
			}
			allMessages := append([]string(nil), messages...)
			for _, item := range calendarItems {
				allMessages = append(allMessages, item.Messages...)
			}
			if err := validateScheduleMessages(allMessages, directMessagesReason); err != nil {
				return "", err
			}
			manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
			if err != nil || !found {
				return "", fmt.Errorf("workflow manifest not found at %s", workspacePath)
			}
			groupNames, err = validateScheduleGroupNamesForWorkspace(ctx, workspacePath, groupNames)
			if err != nil {
				return "", err
			}
			newSched := WorkflowSchedule{
				ID:                       generateScheduleID(),
				Name:                     name,
				ScheduleType:             "calendar",
				Timezone:                 timezone,
				CalendarItems:            calendarItems,
				GroupNames:               groupNames,
				Enabled:                  true,
				Mode:                     mode,
				Messages:                 messages,
				DirectMessagesReason:     directMessagesReason,
				WorkshopMode:             workshopMode,
				PulseMode:                strings.ToLower(strings.TrimSpace(policy.PulseMode)),
				PulseModeReason:          strings.TrimSpace(policy.PulseModeReason),
				CollisionPolicy:          strings.TrimSpace(policy.CollisionPolicy),
				ConcurrencyMode:          strings.TrimSpace(policy.ConcurrencyMode),
				ParallelRiskAcknowledged: policy.ParallelRiskAcknowledged,
				MaxStartDelayMinutes:     policy.MaxStartDelayMinutes,
				AfterScheduleIDs:         normalizeScheduleDependencyIDs(policy.AfterScheduleIDs),
				AfterTerminalStatus:      strings.TrimSpace(policy.AfterTerminalStatus),
				AfterDelayMinutes:        policy.AfterDelayMinutes,
				DependencyDeadline:       strings.TrimSpace(policy.DependencyDeadline),
			}
			if err := validateScheduleRuntimePolicy(newSched); err != nil {
				return "", err
			}
			if err := schedulepolicy.ValidatePulse(newSched.PulseMode, newSched.PulseModeReason); err != nil {
				return "", err
			}
			manifest.Schedules = append(manifest.Schedules, newSched)
			if err := ValidateManifest(manifest); err != nil {
				return "", err
			}
			if err := WriteWorkflowManifest(ctx, workspacePath, manifest); err != nil {
				return "", fmt.Errorf("failed to write manifest: %w", err)
			}
			if api.scheduler != nil {
				sctx := buildScheduleContext(workspacePath, manifest, newSched)
				if err := api.scheduler.LoadSchedule(sctx); err != nil {
					return fmt.Sprintf("Calendar schedule created (ID: %s) but failed to activate: %v", newSched.ID, err), nil
				}
			}
			nextRun := getNextRunTimeForCalendar(newSched)
			nextRunStr := "unknown"
			if nextRun != nil {
				nextRunStr = nextRun.Format(time.RFC3339)
			}
			result := fmt.Sprintf("Calendar schedule created and activated.\n- **ID**: `%s`\n- **Name**: %s\n- **Items**: %d\n- **Timezone**: %s\n- **Next Run**: %s", newSched.ID, name, len(calendarItems), timezone, nextRunStr)
			if advisory := scheduleMessagesAdvisory(allMessages, directMessagesReason); advisory != "" {
				result += "\n- **Execution model**: " + advisory
			}
			return result, nil
		},
		UpdateSchedule: func(ctx context.Context, jobID, name, cronExpr, timezone string, groupNames []string, setGroupNames bool, routeSelections map[string]string, setRouteSelections bool, enabled *bool, mode string, messages []string, setMessages bool, directMessagesReason *string, workshopMode string, resumePrevious *bool, pulseReviewOnly *bool, policy *todo_creation_human.ScheduleRuntimePolicy) (string, error) {
			if cronExpr != "" {
				if err := ValidateCronExpression(cronExpr); err != nil {
					return "", fmt.Errorf("invalid cron expression %q: %w", cronExpr, err)
				}
			}
			if timezone != "" {
				if err := ValidateScheduleTimezone(timezone); err != nil {
					return "", err
				}
			}
			workspacePath, manifest, idx, err := findScheduleByID(ctx, jobID)
			if err != nil {
				return "", fmt.Errorf("schedule not found: %w", err)
			}
			sched := &manifest.Schedules[idx]
			if name != "" {
				sched.Name = name
			}
			if cronExpr != "" {
				sched.CronExpression = cronExpr
			}
			if timezone != "" {
				sched.Timezone = timezone
			}
			if setGroupNames {
				validGroupNames, err := validateScheduleGroupNamesForWorkspace(ctx, workspacePath, groupNames)
				if err != nil {
					return "", err
				}
				sched.GroupNames = validGroupNames
			}
			if setRouteSelections {
				sched.RouteSelections = routeSelections
			}
			if enabled != nil {
				sched.Enabled = *enabled
			}
			if mode != "" || sched.Mode == "" || sched.Mode == "workflow" {
				normalizedMode := scheduleModeOrDefault(mode)
				if normalizedMode == "multi-agent" {
					return "", fmt.Errorf("workflow schedules must use workflow mode")
				}
				sched.Mode = normalizedMode
			}
			candidateMessages := sched.Messages
			candidateReason := sched.DirectMessagesReason
			if setMessages {
				candidateMessages = messages
			}
			if directMessagesReason != nil {
				candidateReason = *directMessagesReason
			}
			if setMessages || directMessagesReason != nil {
				if err := validateScheduleMessages(candidateMessages, candidateReason); err != nil {
					return "", err
				}
			}
			if setMessages {
				sched.Messages = messages
			}
			if directMessagesReason != nil {
				sched.DirectMessagesReason = *directMessagesReason
			}
			if workshopMode != "" {
				sched.WorkshopMode = workshopMode
			}
			if resumePrevious != nil {
				sched.ResumePrevious = resumePrevious
			}
			if pulseReviewOnly != nil {
				sched.PulseReviewOnly = *pulseReviewOnly
			}
			if policy != nil {
				if policy.SetPulseMode && strings.ToLower(strings.TrimSpace(policy.PulseMode)) != sched.PulseMode && !policy.SetPulseModeReason {
					return "", fmt.Errorf("pulse_mode_reason is required when changing pulse_mode")
				}
				if policy.SetPulseModeReason {
					sched.PulseModeReason = strings.TrimSpace(policy.PulseModeReason)
				}
				if policy.SetPulseMode {
					if strings.EqualFold(strings.TrimSpace(policy.PulseMode), schedulepolicy.LegacyFullPulseMode) {
						return "", schedulepolicy.ValidatePulse(policy.PulseMode, policy.PulseModeReason)
					}
					sched.PulseMode = strings.ToLower(strings.TrimSpace(policy.PulseMode))
				}
				if policy.SetExecutionMode {
					sched.ExecutionMode = strings.TrimSpace(policy.ExecutionMode)
				}
				if policy.SetCollisionPolicy {
					sched.CollisionPolicy = strings.TrimSpace(policy.CollisionPolicy)
				}
				if policy.SetConcurrencyMode {
					sched.ConcurrencyMode = strings.TrimSpace(policy.ConcurrencyMode)
					if sched.ConcurrencyMode != "parallel" && !policy.SetParallelRiskAcknowledged {
						sched.ParallelRiskAcknowledged = false
					}
				}
				if policy.SetParallelRiskAcknowledged {
					sched.ParallelRiskAcknowledged = policy.ParallelRiskAcknowledged
				}
				if policy.SetMaxStartDelayMinutes {
					sched.MaxStartDelayMinutes = policy.MaxStartDelayMinutes
				}
				if policy.SetAfterScheduleID {
					sched.AfterScheduleID = strings.TrimSpace(policy.AfterScheduleID)
				}
				if policy.SetAfterScheduleIDs {
					replaceScheduleDependencyIDs(sched, policy.AfterScheduleIDs, policy.SetAfterScheduleID)
				}
				if policy.SetAfterTerminalStatus {
					sched.AfterTerminalStatus = strings.TrimSpace(policy.AfterTerminalStatus)
				}
				if policy.SetAfterDelayMinutes {
					sched.AfterDelayMinutes = policy.AfterDelayMinutes
				}
				if policy.SetDependencyDeadline {
					sched.DependencyDeadline = strings.TrimSpace(policy.DependencyDeadline)
				}
				if err := validateScheduleRuntimePolicy(*sched); err != nil {
					return "", err
				}
			}
			// A stored legacy "full" was not chosen in this request: save it as
			// basic and keep the workflow's review on its own Pulse schedule.
			if strings.EqualFold(strings.TrimSpace(sched.PulseMode), schedulepolicy.LegacyFullPulseMode) {
				sched.PulseMode = schedulepolicy.NormalizePulse(sched.PulseMode)
				if sched.Enabled {
					if manifest.Pulse == nil {
						manifest.Pulse = &WorkflowPulseConfig{}
					}
					manifest.Pulse.Enabled = true
				}
			}
			if err := schedulepolicy.ValidatePulse(sched.PulseMode, sched.PulseModeReason); err != nil {
				return "", err
			}
			// PLAT-115: a PulseReviewOnly schedule carries no GroupNames — same
			// reason CreateSchedule skips the group-name requirement for it.
			if !sched.PulseReviewOnly {
				validGroupNames, err := validateScheduleGroupNamesForWorkspace(ctx, workspacePath, sched.GroupNames)
				if err != nil {
					return "", err
				}
				sched.GroupNames = validGroupNames
			}
			if err := ValidateManifest(manifest); err != nil {
				return "", err
			}
			if err := WriteWorkflowManifest(ctx, workspacePath, manifest); err != nil {
				return "", fmt.Errorf("failed to write manifest: %w", err)
			}
			if api.scheduler != nil {
				if err := api.scheduler.ReloadSchedule(ctx, workspacePath, jobID); err != nil {
					return fmt.Sprintf("Schedule updated but failed to reload: %v", err), nil
				}
			}
			nextRun := getNextRunTime(sched.CronExpression, sched.Timezone)
			nextRunStr := "unknown"
			if nextRun != nil {
				nextRunStr = nextRun.Format(time.RFC3339)
			}
			result := fmt.Sprintf("Schedule updated.\n- **ID**: `%s`\n- **Name**: %s\n- **Cron**: `%s`\n- **Enabled**: %v\n- **Next Run**: %s", sched.ID, sched.Name, sched.CronExpression, sched.Enabled, nextRunStr)
			if advisory := scheduleMessagesAdvisory(sched.Messages, sched.DirectMessagesReason); advisory != "" {
				result += "\n- **Execution model**: " + advisory
			}
			return result, nil
		},
		DeleteSchedule: func(ctx context.Context, jobID string) error {
			workspacePath, manifest, idx, err := findScheduleByID(ctx, jobID)
			if err != nil {
				return err
			}
			if api.scheduler != nil {
				_ = api.scheduler.RemoveWorkflowJob(workspacePath, jobID)
			}
			manifest.Schedules = append(manifest.Schedules[:idx], manifest.Schedules[idx+1:]...)
			return WriteWorkflowManifest(ctx, workspacePath, manifest)
		},
		TriggerSchedule: func(ctx context.Context, jobID string) (string, error) {
			if api.scheduler == nil {
				return "", fmt.Errorf("scheduler not available")
			}
			workspacePath := api.scheduler.GetWorkspaceForSchedule(jobID)
			if workspacePath == "" {
				wp, _, _, err := findScheduleByID(ctx, jobID)
				if err != nil {
					return "", err
				}
				workspacePath = wp
			}
			// Pass the chat that asked, so the run's terminals show up in its rail
			// instead of only under the schedule's own session.
			_, err := api.scheduler.TriggerNowFromSession(workspacePath, jobID, chatSessionIDFromContext(ctx))
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Schedule triggered. Job ID: `%s`", jobID), nil
		},
		GetScheduleRuns: func(ctx context.Context, jobID string, limit int) (string, error) {
			if limit <= 0 {
				limit = 10
			}
			workspacePath := ""
			if api.scheduler != nil {
				workspacePath = api.scheduler.GetWorkspaceForSchedule(jobID)
			}
			if workspacePath == "" {
				wp, _, _, err := findScheduleByID(ctx, jobID)
				if err != nil {
					return "", err
				}
				workspacePath = wp
			}
			runs, total, err := ListScheduleRuns(ctx, workspacePath, jobID, limit, 0)
			if err != nil {
				return "", err
			}
			var sb strings.Builder
			if len(runs) == 0 {
				sb.WriteString("No runs found for this schedule.\n\n")
			} else {
				sb.WriteString(fmt.Sprintf("## Run History (%d of %d)\n\n", len(runs), total))
				for _, r := range runs {
					duration := ""
					if r.DurationMs != nil {
						duration = fmt.Sprintf(" (%dms)", *r.DurationMs)
					}
					idPrefix := r.ID
					if len(idPrefix) > 8 {
						idPrefix = idPrefix[:8]
					}
					sb.WriteString(fmt.Sprintf("- **%s** [%s]%s — %s", idPrefix, r.Status, duration, r.StartedAt.Format("2006-01-02 15:04:05")))
					if r.RunFolder != "" {
						sb.WriteString(fmt.Sprintf(" → `%s`", r.RunFolder))
					}
					if r.Error != "" {
						sb.WriteString(fmt.Sprintf("\n  Error: %s", r.Error))
					}
					sb.WriteString("\n")
				}
			}
			// Every scheduled occurrence is recorded here, including the ones
			// that never produce a schedule_runs row. They are grouped by what
			// they mean: lost work, deferred work, or deliberately not run
			// (schedule_fire_decision_kind.go).
			if api.scheduler != nil {
				if decisions, decErr := api.scheduler.ListFireDecisions(ctx, workspacePath, jobID, limit); decErr == nil {
					sb.WriteString(formatScheduleNonRunOccurrences(decisions, collisionPolicyForSchedule(ctx, workspacePath, jobID)))
				}
			}
			return sb.String(), nil
		},
	}
}

// buildSkillCallbacks creates SkillCallbacks that bridge the workshop tools
// to the workspace skills API. Returns nil-safe callbacks.
func (api *StreamingAPI) buildSkillCallbacks() *todo_creation_human.SkillCallbacks {
	return api.buildSkillCallbacksForProduct("")
}

func skillSelectionHint(productID, skillNames string) string {
	if strings.EqualFold(strings.TrimSpace(productID), codeproduct.ProfileID) {
		return fmt.Sprintf("%s can be loaded immediately with read_skill. To attach it automatically on future turns in this workspace, call update_project_skill_selection(action=\"select\", skill=\"<folder-name>\"); the selection is saved in workflow.json and shown in Setup > Skills.", skillNames)
	}
	if strings.EqualFold(strings.TrimSpace(productID), "work") {
		return fmt.Sprintf("%s can be loaded immediately with read_skill. To attach it automatically on future Crew turns, call update_project_skill_selection(action=\"select\", skill=\"<folder-name>\"); Crew persists that selection in workflow.json and also shows it in Setup > Skills.", skillNames)
	}
	return fmt.Sprintf("Use update_workflow_config to add %s to the workflow's selected skills.", skillNames)
}

// projectSkillCallbacks points install, import and uninstall at one project's
// skills folder; listing and search still see the shared library.
func projectSkillCallbacks(base *todo_creation_human.SkillCallbacks, projectSkillsDir, productID string) *todo_creation_human.SkillCallbacks {
	wsURL := getWorkspaceAPIURL()
	scoped := *base
	scoped.InstallSkill = func(ctx context.Context, source string) (string, error) {
		result, err := skills.ImportToWorkspaceDir(ctx, wsURL, source, projectSkillsDir)
		if err != nil {
			return "", fmt.Errorf("failed to install skill: %w", err)
		}
		if len(result.InstalledSkills) == 0 {
			return "No skills were installed. Check the source format (e.g., 'owner/repo@skill-name').", nil
		}
		return fmt.Sprintf("Installed into this workspace's private skills/ folder: %s. %s", strings.Join(result.InstalledSkills, ", "), skillSelectionHint(productID, "These skills")), nil
	}
	scoped.ImportSkill = func(ctx context.Context, githubURL, token string) (string, error) {
		resp, err := skills.ImportGitHubSkillInto(wsURL, githubURL, token, projectSkillsDir)
		if err != nil {
			return "", fmt.Errorf("failed to import skill: %w", err)
		}
		if !resp.Success {
			return fmt.Sprintf("Failed to import skill: %s", resp.Error), nil
		}
		return fmt.Sprintf("Imported skill **%s** into this workspace's private skills/ folder. %s", resp.SkillName, skillSelectionHint(productID, "It")), nil
	}
	scoped.DeleteSkill = func(ctx context.Context, folderName string) error {
		return skills.DeleteProjectSkill(ctx, wsURL, projectSkillsDir, folderName)
	}
	return &scoped
}

func (api *StreamingAPI) buildSkillCallbacksForProduct(productID string) *todo_creation_human.SkillCallbacks {
	wsURL := getWorkspaceAPIURL() // workspace container URL, not backend URL
	return &todo_creation_human.SkillCallbacks{
		ListSkills: func(ctx context.Context) (string, error) {
			allSkills, err := skills.DiscoverSkills(wsURL)
			if err != nil {
				return "", fmt.Errorf("failed to discover skills: %w", err)
			}
			if len(allSkills) == 0 {
				return "No skills found in the workspace. Use install_skill to add skills.", nil
			}
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("## Skills (%d found)\n\n", len(allSkills)))
			for _, sk := range allSkills {
				sb.WriteString(fmt.Sprintf("### %s\n", sk.Frontmatter.Name))
				sb.WriteString(fmt.Sprintf("- **Folder**: `%s`\n", sk.FolderName))
				if sk.Frontmatter.Description != "" {
					sb.WriteString(fmt.Sprintf("- **Description**: %s\n", sk.Frontmatter.Description))
				}
				if sk.SourceURL != "" {
					sb.WriteString(fmt.Sprintf("- **Source**: %s\n", sk.SourceURL))
				}
				sb.WriteString("\n")
			}
			return sb.String(), nil
		},
		ImportSkill: func(ctx context.Context, githubURL, token string) (string, error) {
			resp, err := skills.ImportGitHubSkill(wsURL, githubURL, token)
			if err != nil {
				return "", fmt.Errorf("failed to import skill: %w", err)
			}
			if !resp.Success {
				return fmt.Sprintf("Failed to import skill: %s", resp.Error), nil
			}
			return fmt.Sprintf("Successfully imported skill **%s**. %s", resp.SkillName, skillSelectionHint(productID, "It")), nil
		},
		DeleteSkill: func(ctx context.Context, folderName string) error {
			err := skills.DeleteSkill(wsURL, folderName)
			if err == nil {
				_ = skills.RemoveFromLockFile(wsURL, folderName)
			}
			return err
		},
		SearchSkills: func(ctx context.Context, query string) (string, error) {
			results, err := skills.FindSkills(ctx, query)
			if err != nil {
				return "", fmt.Errorf("failed to search skills: %w", err)
			}
			if len(results) == 0 {
				return "No skills found matching your query.", nil
			}
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("## Search Results (%d found)\n\n", len(results)))
			sb.WriteString("Install with: `install_skill` tool using the source value.\n\n")
			for _, r := range results {
				sb.WriteString(fmt.Sprintf("- **%s** (%s) — %s\n", r.Skill, r.Source, r.Installs))
			}
			return sb.String(), nil
		},
		InstallSkill: func(ctx context.Context, source string) (string, error) {
			result, err := skills.ImportToWorkspace(ctx, wsURL, source)
			if err != nil {
				return "", fmt.Errorf("failed to install skill: %w", err)
			}
			if len(result.InstalledSkills) == 0 {
				return "No skills were installed. Check the source format (e.g., 'owner/repo@skill-name').", nil
			}
			return fmt.Sprintf("Successfully installed: %s. %s", strings.Join(result.InstalledSkills, ", "), skillSelectionHint(productID, "These skills")), nil
		},
	}
}

func (api *StreamingAPI) registerMultiAgentSkillTools(registrar interface {
	RegisterCustomTool(string, string, map[string]interface{}, func(context.Context, map[string]interface{}) (string, error), string) error
}, disabled func(string) bool, productID ...string) error {
	activeProductID := ""
	if len(productID) > 0 {
		activeProductID = productID[0]
	}
	return api.registerMultiAgentSkillToolsIn(registrar, disabled, activeProductID, "")
}

// registerMultiAgentSkillToolsIn registers the skill tools. A non-empty
// projectSkillsDir (a private Code's docs-relative skills folder) makes
// install, import and uninstall act on that folder only: the account-wide
// library stays readable but is never written from the project.
func (api *StreamingAPI) registerMultiAgentSkillToolsIn(registrar interface {
	RegisterCustomTool(string, string, map[string]interface{}, func(context.Context, map[string]interface{}) (string, error), string) error
}, disabled func(string) bool, activeProductID, projectSkillsDir string) error {
	skillFuncs := api.buildSkillCallbacksForProduct(activeProductID)
	if skillFuncs == nil {
		return fmt.Errorf("skill callbacks unavailable")
	}
	if projectSkillsDir = strings.Trim(strings.TrimSpace(projectSkillsDir), "/"); projectSkillsDir != "" {
		skillFuncs = projectSkillCallbacks(skillFuncs, projectSkillsDir, activeProductID)
	}

	registerTool := func(name, description string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error)) error {
		if disabled != nil && disabled(name) {
			return nil
		}
		return registrar.RegisterCustomTool(name, description, params, exec, "skill_tools")
	}

	if err := registerTool(
		"list_skills",
		"List skills available in the workspace. Use this to inspect installed skills before selecting them in chat settings or reading their files directly.",
		map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			return skillFuncs.ListSkills(ctx)
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"import_skill",
		"Import a skill from GitHub into the workspace. Imported skills become available for future chats and can also be read directly from the skills folder.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"github_url": map[string]interface{}{
					"type":        "string",
					"description": "GitHub URL of the skill to import, either a repo URL or a direct path to a skill folder.",
				},
				"token": map[string]interface{}{
					"type":        "string",
					"description": "Optional GitHub personal access token for private repositories.",
				},
			},
			"required": []string{"github_url"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			githubURL, _ := args["github_url"].(string)
			if strings.TrimSpace(githubURL) == "" {
				return "github_url is required.", nil
			}
			token, _ := args["token"].(string)
			return skillFuncs.ImportSkill(ctx, githubURL, token)
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"uninstall_skill",
		"Remove an installed skill from the workspace. Use list_skills first to confirm the folder name.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"folder_name": map[string]interface{}{
					"type":        "string",
					"description": "The skill folder name returned by list_skills.",
				},
			},
			"required": []string{"folder_name"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			folderName, _ := args["folder_name"].(string)
			if strings.TrimSpace(folderName) == "" {
				return "folder_name is required.", nil
			}
			if err := skillFuncs.DeleteSkill(ctx, folderName); err != nil {
				return fmt.Sprintf("Failed to uninstall skill %q: %v", folderName, err), nil
			}
			return fmt.Sprintf("Successfully uninstalled skill %q from workspace.", folderName), nil
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"search_skills",
		"Search the public skills registry for installable skills. Use install_skill with a returned source value to install one.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search terms such as 'browser automation', 'social media', or 'data analysis'.",
				},
			},
			"required": []string{"query"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			query, _ := args["query"].(string)
			if strings.TrimSpace(query) == "" {
				return "query is required.", nil
			}
			return skillFuncs.SearchSkills(ctx, query)
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"install_skill",
		"Install a skill from the public skills registry using owner/repo@skill-name format. Use search_skills first to find valid sources.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"source": map[string]interface{}{
					"type":        "string",
					"description": "Skill source in owner/repo@skill-name format.",
				},
			},
			"required": []string{"source"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			source, _ := args["source"].(string)
			if strings.TrimSpace(source) == "" {
				return "source is required (e.g. 'owner/repo@skill-name').", nil
			}
			return skillFuncs.InstallSkill(ctx, source)
		},
	); err != nil {
		return err
	}

	return nil
}

// buildLLMToolsCallbacks creates LLMToolsCallbacks that bridge the workshop tools
// to the published LLM list, model metadata catalog, and provider validation logic.
func (api *StreamingAPI) buildLLMToolsCallbacks() *todo_creation_human.LLMToolsCallbacks {
	return &todo_creation_human.LLMToolsCallbacks{
		ListPublishedLLMs: func(ctx context.Context) (string, error) {
			llms, err := LoadPublishedLLMsWithAuto(ctx)
			if err != nil {
				return "", fmt.Errorf("failed to load published LLMs: %w", err)
			}
			return prettyJSON(map[string]interface{}{
				"count": len(llms),
				"llms":  llms,
				"note":  "These are the published models available for workflow tier configuration.",
			}), nil
		},
		ListProviderModels: func(_ context.Context, provider string) (string, error) {
			return listProviderModelsJSON(provider), nil
		},
		ValidateLLM: func(ctx context.Context, args map[string]interface{}) (string, error) {
			provider := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", args["provider"])))
			modelID, _ := args["model_id"].(string)
			apiKey, _ := args["api_key"].(string)
			endpoint, _ := args["endpoint"].(string)
			region, _ := args["region"].(string)
			apiVersion, _ := args["api_version"].(string)
			options, _ := args["options"].(map[string]interface{})

			if provider == "" {
				return "provider is required.", nil
			}
			if !isPublishedLLMProviderAllowed(provider) {
				return fmt.Sprintf("unsupported chat LLM provider %q. Use coding agents or direct API providers: codex-cli, cursor-cli, pi-cli, claude-code, bedrock, openai, anthropic, vertex, or azure.", provider), nil
			}

			validationOptions := cloneOptionsMap(options)

			// Use workspace-backed auth if no explicit key provided
			usedWorkspaceAuth := false
			if strings.TrimSpace(apiKey) == "" {
				keys, err := LoadProviderKeys(ctx)
				if err == nil && keys != nil {
					if value := getStoredProviderAPIKey(keys, provider); value != "" {
						apiKey = value
						usedWorkspaceAuth = true
					}
					switch provider {
					case "bedrock":
						if keys.Bedrock != nil && keys.Bedrock.Region != "" {
							region = keys.Bedrock.Region
							usedWorkspaceAuth = true
						}
					case "azure":
						if keys.Azure != nil {
							if keys.Azure.APIKey != "" {
								apiKey = keys.Azure.APIKey
								usedWorkspaceAuth = true
							}
							if endpoint == "" && keys.Azure.Endpoint != "" {
								endpoint = keys.Azure.Endpoint
							}
							if apiVersion == "" && keys.Azure.APIVersion != "" {
								apiVersion = keys.Azure.APIVersion
							}
						}
					}
				}
			}

			if endpoint != "" || region != "" || apiVersion != "" {
				if validationOptions == nil {
					validationOptions = map[string]interface{}{}
				}
				if endpoint != "" {
					validationOptions["endpoint"] = endpoint
				}
				if region != "" {
					validationOptions["region"] = region
				}
				if apiVersion != "" {
					validationOptions["api_version"] = apiVersion
				}
			}
			req := llm.APIKeyValidationRequest{
				Provider: provider,
				ModelID:  modelID,
				APIKey:   apiKey,
				Options:  validationOptions,
			}

			resp := validateProviderConfig(req)
			return prettyJSON(map[string]interface{}{
				"provider":            provider,
				"model_id":            modelID,
				"valid":               resp.Valid,
				"message":             resp.Message,
				"error":               resp.Error,
				"corrected_options":   resp.CorrectedOptions,
				"used_workspace_auth": usedWorkspaceAuth,
			}), nil
		},
	}
}

func (api *StreamingAPI) registerMultiAgentMCPServerTools(registrar interface {
	RegisterCustomTool(string, string, map[string]interface{}, func(context.Context, map[string]interface{}) (string, error), string) error
}, disabled func(string) bool) error {
	registerTool := func(name, description string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error)) error {
		if disabled != nil && disabled(name) {
			return nil
		}
		if name == "install_mcp_server" || name == "add_mcp_server" || name == "edit_mcp_server" || name == "remove_mcp_server" || name == "list_mcp_servers" || name == "trigger_mcp_discovery" {
			original := exec
			exec = func(ctx context.Context, args map[string]interface{}) (string, error) {
				userID, err := api.mcpToolUserID(ctx)
				if err != nil {
					return "", err
				}
				ctx = context.WithValue(ctx, UserContextKey, &UserClaims{UserID: userID})
				if (name == "install_mcp_server" || name == "add_mcp_server" || name == "edit_mcp_server" || name == "remove_mcp_server") && !canManageGlobalSecrets(userID) {
					return "Managing shared MCP connections requires admin access.", nil
				}
				return original(ctx, args)
			}
		}
		return registrar.RegisterCustomTool(name, description, params, exec, "mcp_server_tools")
	}

	toStringSlice := func(raw interface{}) []string {
		items, ok := raw.([]interface{})
		if !ok {
			return nil
		}
		result := make([]string, 0, len(items))
		for _, item := range items {
			value, ok := item.(string)
			if !ok {
				continue
			}
			value = strings.TrimSpace(value)
			if value != "" {
				result = append(result, value)
			}
		}
		return result
	}

	toStringMap := func(raw interface{}) map[string]string {
		items, ok := raw.(map[string]interface{})
		if !ok {
			return nil
		}
		result := make(map[string]string, len(items))
		for key, value := range items {
			strValue, ok := value.(string)
			if !ok {
				continue
			}
			trimmedKey := strings.TrimSpace(key)
			if trimmedKey == "" {
				continue
			}
			result[trimmedKey] = strValue
		}
		if len(result) == 0 {
			return nil
		}
		return result
	}

	loadUserConfig := func() (string, *mcpclient.MCPConfig, error) {
		userConfigPath := strings.Replace(api.mcpConfigPath, ".json", "_user.json", 1)
		userConfig, err := mcpclient.LoadConfig(userConfigPath, api.logger)
		if err != nil {
			userConfig = &mcpclient.MCPConfig{MCPServers: make(map[string]mcpclient.MCPServerConfig)}
		}
		if userConfig.MCPServers == nil {
			userConfig.MCPServers = make(map[string]mcpclient.MCPServerConfig)
		}
		return userConfigPath, userConfig, nil
	}

	buildServerConfig := func(args map[string]interface{}) (mcpclient.MCPServerConfig, error) {
		server := mcpclient.MCPServerConfig{
			Args:       toStringSlice(args["args"]),
			Env:        toStringMap(args["env"]),
			Headers:    toStringMap(args["headers"]),
			PoolConfig: nil,
		}
		if value, ok := args["command"].(string); ok {
			server.Command = strings.TrimSpace(value)
		}
		if value, ok := args["working_dir"].(string); ok {
			server.WorkingDir = strings.TrimSpace(value)
		}
		if value, ok := args["description"].(string); ok {
			server.Description = strings.TrimSpace(value)
		}
		if value, ok := args["url"].(string); ok {
			server.URL = strings.TrimSpace(value)
		}
		if value, ok := args["protocol"].(string); ok {
			server.Protocol = mcpclient.ProtocolType(strings.TrimSpace(value))
		}
		return server, nil
	}

	if err := registerTool(
		"list_mcp_servers",
		"List shared platform MCP servers: for each, whether it is connected for AgentWorks (versus merely present in the catalog), whether it is a custom platform server, and whether discovery succeeded. Connecting requires admin access; in single-user setups the user is the admin, so proceed instead of asking for one. Connected credentials are reusable by Work, workflows, chats, and schedules. Use search_mcp_catalog for servers not yet configured.",
		map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			mergedConfig, err := api.loadMergedConfig()
			if err != nil {
				return "", fmt.Errorf("failed to load MCP config: %w", err)
			}

			userConfigPath, _, err := loadUserConfig()
			if err != nil {
				return "", err
			}

			toolStatusCopy := make(map[string]ToolStatus, len(mergedConfig.MCPServers))
			for name, cfg := range mergedConfig.MCPServers {
				toolStatusCopy[name] = api.mcpToolStatusForUser(name, GetUserIDFromContext(ctx), cfg)
			}

			names := make([]string, 0, len(mergedConfig.MCPServers))
			for name := range mergedConfig.MCPServers {
				names = append(names, name)
			}
			sort.Strings(names)

			var sb strings.Builder
			sb.WriteString("## MCP Servers\n\nEach name below is an exact connection ID. Separate connections may access different accounts. Verify workspace/account identity with each connection’s read-only service tools; never infer it from its name. If the intended account is unclear, ask which connection to use. Connected does not mean selected for this project or loaded in this turn.\n\n")
			if len(names) == 0 {
				sb.WriteString("No MCP servers are configured.\n")
				return sb.String(), nil
			}

			if isMCPConfigLocked() {
				sb.WriteString("Configuration mode: locked (read-only)\n\n")
			} else {
				sb.WriteString(fmt.Sprintf("Platform MCP overlay path: `%s`\n\n", userConfigPath))
			}

			for _, name := range names {
				server := mergedConfig.MCPServers[name]
				_, isBase := api.mcpConfig.MCPServers[name]

				// "source" names where the definition comes from; a base
				// catalog server also needs a separate platform connection
				// signal (searchable does not mean connected).
				source := "custom platform server"
				switch {
				case isBase && server.OAuth == nil:
					source = "catalog, no sign-in required"
				case isBase && !toolStatusCopy[name].RequiresOAuth:
					source = "catalog, installed/authorized"
				case isBase:
					source = "catalog, NOT installed — needs sign-in from the connector directory"
				}

				statusLabel := "not yet discovered"
				if status, ok := toolStatusCopy[name]; ok {
					switch status.Status {
					case "ok":
						statusLabel = fmt.Sprintf("discovered (%d tools)", len(status.FunctionNames))
					case "error":
						statusLabel = "discovery failed"
					case "not_connected":
						statusLabel = "admin sign-in required"
					case "not_loaded", "":
						statusLabel = "not yet discovered"
					default:
						statusLabel = status.Status
					}
				}

				sb.WriteString(fmt.Sprintf("- connection_id: `%s` [%s] [%s]\n", name, source, statusLabel))
				if server.Description != "" {
					sb.WriteString(fmt.Sprintf("  %s\n", server.Description))
				}
				protocol := server.GetProtocol()
				if server.URL != "" {
					sb.WriteString(fmt.Sprintf("  protocol: `%s`, url: `%s`\n", protocol, server.URL))
				} else {
					sb.WriteString(fmt.Sprintf("  protocol: `%s`, command: `%s`\n", protocol, server.Command))
				}
				if status, ok := toolStatusCopy[name]; ok && status.Error != "" {
					sb.WriteString(fmt.Sprintf("  last error: %s\n", status.Error))
				}
			}

			return sb.String(), nil
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"search_mcp_catalog",
		"Search for an MCP (Model Context Protocol) server for a given service or capability — e.g. \"is there an MCP for ClickUp?\". Searches our curated catalog, GitHub's public MCP Registry, and the official MCP Registry. Registries supply metadata, not a required hosting or authorization service. Prefer the provider's own documented endpoint. If no suitable MCP is found, continue with an internet search using available web/search/browser tools and verify the provider's official documentation or repository. A registry miss does not establish that no MCP exists; never invent an endpoint. Catalog hits report whether the user has already connected them; public registry hits are NOT vetted — do not assume they are safe to add sight-unseen, and note to the user that adding one (especially an OAuth or locally-run package one) needs the same scrutiny a human would give any third-party integration. Use install_mcp_server for a verified provider URL; add_mcp_server is for a custom configuration whose auth is already known.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Service or capability to search for, e.g. \"clickup\", \"playwright\", \"vector database\".",
				},
			},
			"required": []string{"query"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			query, _ := args["query"].(string)
			query = strings.TrimSpace(query)
			if query == "" {
				return "query is required.", nil
			}
			needle := strings.ToLower(query)

			_, userConfig, err := loadUserConfig()
			if err != nil {
				return "", err
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("## MCP search: %q\n\n", query))

			// Our own catalog first: every entry here was hand-verified (OAuth
			// shape, live-probed), unlike the public registries below.
			var catalogHits []string
			for name, server := range api.mcpConfig.MCPServers {
				if !strings.Contains(strings.ToLower(name), needle) {
					continue
				}
				status := "available, no sign-in required"
				if server.OAuth != nil {
					if _, connected := userConfig.MCPServers[name]; connected {
						status = "already connected"
					} else {
						status = "not connected yet — connect it from the connector directory"
					}
				}
				catalogHits = append(catalogHits, fmt.Sprintf("- **%s** [our catalog, vetted] — %s", name, status))
			}
			sort.Strings(catalogHits)
			sb.WriteString("### Our catalog\n\n")
			if len(catalogHits) == 0 {
				sb.WriteString("No match.\n\n")
			} else {
				for _, line := range catalogHits {
					sb.WriteString(line + "\n")
				}
				sb.WriteString("\n")
			}

			appendExternal := func(title string, results []services.MCPRegistryResult, searchErr error) {
				sb.WriteString(fmt.Sprintf("### %s (unvetted — review before adding)\n\n", title))
				if searchErr != nil {
					sb.WriteString(fmt.Sprintf("Search failed: %v\n\n", searchErr))
					return
				}
				if len(results) == 0 {
					sb.WriteString("No match.\n\n")
					return
				}
				for _, r := range results {
					kind := "requires a local package run (npm/npx, docker, etc.)"
					if r.Remote {
						kind = "remote HTTP server"
					}
					desc := r.Description
					if desc == "" {
						desc = "(no description provided)"
					}
					sb.WriteString(fmt.Sprintf("- **%s**", r.Name))

					sb.WriteString(fmt.Sprintf(" — %s. %s", desc, kind))
					if r.Link != "" {
						sb.WriteString(fmt.Sprintf(" (endpoint or repository: %s)", r.Link))
						if r.RepositoryURL != "" && r.RepositoryURL != r.Link {
							sb.WriteString(fmt.Sprintf("; source: %s", r.RepositoryURL))
						}
					}
					sb.WriteString("\n")
				}
				sb.WriteString("\n")
			}

			githubResults, githubErr := services.SearchGitHubMCPRegistry(ctx, query, 10)
			appendExternal("GitHub MCP Registry", githubResults, githubErr)

			officialResults, officialErr := services.SearchOfficialMCPRegistry(ctx, query, 10)
			appendExternal("Official MCP Registry", officialResults, officialErr)

			sb.WriteString("### Check official providers on the internet\n\nAlso search the internet for the provider's official MCP documentation or source repository using available web/search/browser tools. Registry listings can be incomplete and do not prove provider ownership. Verify and prefer the provider's direct endpoint before proposing a connection; explain any hosted intermediary. If no suitable match appears here, continue the internet search rather than concluding that no MCP exists.\n")

			return sb.String(), nil
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"install_mcp_server",
		"Platform install of an MCP server, shared by AgentWorks, Work, workflows, chats, and schedules. Requires admin access; in single-user setups the user is the admin, so proceed instead of asking for one. Installation does not select the server: workflows use update_workflow_config(add_servers=[name]), while an active Work project uses update_project_mcp_server_selection(action=select, server=name). Before OAuth or API-key setup in multi-user mode, explicitly tell the user that every AgentWorks user will be able to use the connected external account. For a fresh URL, live-probe its auth requirements and verify the provider. To reauthorize an existing OAuth connection, set reconnect=true.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"reconnect": map[string]interface{}{
					"type":        "boolean",
					"description": "Restart OAuth authorization for this existing connector without creating another server entry.",
				},
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Server name — exact catalog name from search_mcp_catalog/list_mcp_servers, or a name you choose for a fresh install from url.",
				},
				"url": map[string]interface{}{
					"type":        "string",
					"description": "Only when name is not already in our catalog: the server's URL (from a search_mcp_catalog public registry hit, or one the user gave you). Triggers a live auth probe before installing.",
				},
				"api_key": map[string]interface{}{
					"type":        "string",
					"description": "Optional. For a server that authenticates with a simple bearer API key rather than OAuth, once the user has provided one.",
				},
				"client_id": map[string]interface{}{
					"type":        "string",
					"description": "Optional. Only for an OAuth server with no Dynamic Client Registration support, after the user has registered their own OAuth app and given you its client_id.",
				},
			},
			"required": []string{"name"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if isMCPConfigLocked() {
				return "MCP configuration is locked (MCP_CONFIG_LOCKED is set), so chat cannot install servers.", nil
			}
			name, _ := args["name"].(string)
			name = strings.TrimSpace(name)
			if name == "" {
				return "name is required.", nil
			}
			candidateURL, _ := args["url"].(string)
			candidateURL = strings.TrimSpace(candidateURL)
			apiKey, _ := args["api_key"].(string)
			apiKey = strings.TrimSpace(apiKey)
			clientID, _ := args["client_id"].(string)
			clientID = strings.TrimSpace(clientID)

			// Chat has no other way to learn an install finished — the
			// connector-directory UI refreshes itself on its own button
			// clicks, but a chat-driven change needs to nudge the mcp
			// workspace view explicitly if it's open. Best-effort: a stale
			// panel just means the user needs to reopen it, not a failure.
			sessionID := executor.SessionIDFromContext(ctx)
			workspacePath := api.uiBroker().scope(sessionID)
			notifyMCPViewRefresh := func() {
				if sessionID == "" || workspacePath == "" {
					return
				}
				if event, evErr := workspaceViewAction("mcp", workspacePath, "refresh", ""); evErr == nil {
					api.emitAgentProfileEvent(sessionID, event)
				}
			}

			config, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
			if err != nil {
				return "", fmt.Errorf("failed to load MCP config: %w", err)
			}
			canonicalName, serverConfig, err := config.ResolveServer(name)
			notInConfigYet := err != nil
			if !notInConfigYet {
				name = canonicalName
			}
			if notInConfigYet && candidateURL == "" {
				return fmt.Sprintf("%q is not in our catalog. Pass url to install it fresh (from a search_mcp_catalog hit or any URL), or use add_mcp_server for a wholly custom stdio/no-auth server.", name), nil
			}

			_, userConfig, err := loadUserConfig()
			if err != nil {
				return "", err
			}
			if !notInConfigYet {
				if _, alreadyInstalled := userConfig.MCPServers[name]; alreadyInstalled {
					reconnect, _ := args["reconnect"].(bool)
					connected := serverConfig.OAuth == nil
					if serverConfig.OAuth != nil && !reconnect {
						owned := serverConfig
						oauthConfig := *owned.OAuth
						if strings.TrimSpace(oauthConfig.TokenFile) == "" {
							oauthConfig.TokenFile = getUserTokenFilePath(platformMCPTokenUserID, name)
						}
						owned.OAuth = &oauthConfig
						connected = hasOAuthTokenFile(owned)
					}
					if connected {
						return fmt.Sprintf("%q is already connected across AgentWorks. Select it with update_workflow_config(add_servers=[%q]) in a workflow, or update_project_mcp_server_selection(action=select, server=%q) in an active Work project. For OAuth reauthorization, call install_mcp_server with reconnect=true (requires admin access).", name, name, name), nil
					}
				}
			}

			// Fresh install from a URL: live-probe its auth requirements before
			// touching any config — the same scrutiny a human gives any
			// third-party integration, automated via spec-compliant discovery
			// rather than guessed from a search result's description text.
			if notInConfigYet {
				probe, probeErr := services.ProbeMCPServerAuth(ctx, candidateURL)
				if probeErr != nil {
					return fmt.Sprintf("Could not determine %s's auth requirements: %v. Not installing — this needs a human to verify before it's trustworthy to add.", candidateURL, probeErr), nil
				}
				serverConfig = mcpclient.MCPServerConfig{URL: candidateURL}
				if probe.NoAuthRequired {
					if apiKey != "" {
						serverConfig.Headers = map[string]string{"Authorization": "Bearer " + apiKey}
					}
					if err := api.persistOAuthConfig(name, serverConfig); err != nil {
						return "", fmt.Errorf("failed to install %q: %w", name, err)
					}
					api.invalidateServerDiscovery(name, "Installed — discovering tools...")
					api.startServerDiscovery(GetUserIDFromContext(ctx), name)
					notifyMCPViewRefresh()
					return fmt.Sprintf("Installed %q (no sign-in required). Discovery is running now. Select it with update_workflow_config(add_servers=[%q]) in a workflow, or update_project_mcp_server_selection(action=select, server=%q) in an active Work project.", name, name, name), nil
				}
				// Needs OAuth. Build the config from what discovery found.
				serverConfig.OAuth = &oauth.OAuthConfig{
					AuthURL:              probe.Endpoints.AuthURL,
					TokenURL:             probe.Endpoints.TokenURL,
					RegistrationEndpoint: probe.Endpoints.RegistrationEndpoint,
					Scopes:               probe.Endpoints.ScopesSupported,
					Resource:             probe.Endpoints.Resource,
					ClientID:             clientID,
				}
				if serverConfig.OAuth.RegistrationEndpoint == "" && clientID == "" {
					return fmt.Sprintf("%s requires OAuth but published no Dynamic Client Registration endpoint. It may still support a Client ID Metadata Document, or need a manually registered OAuth app — tell the user this before proceeding. Auth URL: %s — Token URL: %s. If they give you a client_id, call install_mcp_server again with the same name/url and client_id set.",
						candidateURL, probe.Endpoints.AuthURL, probe.Endpoints.TokenURL), nil
				}
				// Write the draft config (no token yet) so beginOAuthFlow can find it.
				if err := api.persistOAuthConfig(name, serverConfig); err != nil {
					return "", fmt.Errorf("failed to register %q: %w", name, err)
				}
			}

			// No OAuth: install directly, optionally with an API key header.
			if serverConfig.OAuth == nil {
				if apiKey != "" {
					if serverConfig.Headers == nil {
						serverConfig.Headers = make(map[string]string)
					}
					serverConfig.Headers["Authorization"] = "Bearer " + apiKey
				}
				if err := api.persistOAuthConfig(name, serverConfig); err != nil {
					return "", fmt.Errorf("failed to install %q: %w", name, err)
				}
				api.invalidateServerDiscovery(name, "Installed — discovering tools...")
				api.startServerDiscovery(GetUserIDFromContext(ctx), name)
				notifyMCPViewRefresh()
				return fmt.Sprintf("Installed %q. Discovery is running now. Select it with update_workflow_config(add_servers=[%q]) in a workflow, or update_project_mcp_server_selection(action=select, server=%q) in an active Work project.", name, name, name), nil
			}

			// OAuth: start the flow and hand back the URL for the user to open.
			redirectURI := deriveOAuthRedirectURIFromEnv()
			if redirectURI == "" {
				return fmt.Sprintf("%q requires OAuth sign-in, and this server has no PUBLIC_URL configured to build a callback URL from chat. Ask the user to connect it from the connector directory in the UI instead.", name), nil
			}

			startResp, discoveryResp, err := api.beginOAuthFlow(GetUserIDFromContext(ctx), sessionID, name, redirectURI, clientID, notifyMCPViewRefresh)
			if err != nil {
				return "", fmt.Errorf("failed to start OAuth for %q: %w", name, err)
			}
			if discoveryResp != nil {
				return fmt.Sprintf("%s Auth URL: %s — Token URL: %s. Once the user gives you their OAuth app's client_id, call install_mcp_server again with client_id set.",
					discoveryResp.Message, discoveryResp.AuthURL, discoveryResp.TokenURL), nil
			}
			return fmt.Sprintf("%q needs OAuth sign-in. Give the user this link to open in their browser — the connection finishes automatically once they authorize, no further action from you needed: %s", name, startResp.AuthURL), nil
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"add_mcp_server",
		"Add a shared platform MCP server configuration, then trigger discovery. Requires admin access; in single-user setups the user is the admin, so proceed instead of asking for one. Every AgentWorks product and user can reuse it; workflows still select servers explicitly.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Unique server name.",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Optional human-readable description.",
				},
				"protocol": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"stdio", "sse", "http"},
					"description": "Optional explicit protocol. If omitted, the backend infers it from url or command when possible.",
				},
				"command": map[string]interface{}{
					"type":        "string",
					"description": "Command to run for stdio servers.",
				},
				"args": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Command arguments for stdio servers.",
				},
				"env": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": map[string]interface{}{"type": "string"},
					"description":          "Optional environment variables for stdio servers.",
				},
				"working_dir": map[string]interface{}{
					"type":        "string",
					"description": "Optional working directory for stdio servers.",
				},
				"url": map[string]interface{}{
					"type":        "string",
					"description": "URL for SSE or HTTP MCP servers.",
				},
				"headers": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": map[string]interface{}{"type": "string"},
					"description":          "Optional HTTP headers for SSE or HTTP MCP servers.",
				},
			},
			"required": []string{"name"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if isMCPConfigLocked() {
				return "MCP configuration is locked (MCP_CONFIG_LOCKED is set), so chat cannot add or update servers.", nil
			}

			name, _ := args["name"].(string)
			name = strings.TrimSpace(name)
			if name == "" {
				return "name is required.", nil
			}

			if _, exists := api.mcpConfig.MCPServers[name]; exists {
				return fmt.Sprintf("Server %q is part of the base/admin config and can't be modified from chat. Use a new name or update the base config directly.", name), nil
			}

			userConfigPath, userConfig, err := loadUserConfig()
			if err != nil {
				return "", err
			}
			if _, exists := userConfig.MCPServers[name]; exists {
				return fmt.Sprintf("User-defined MCP server %q already exists. Use edit_mcp_server to change it.", name), nil
			}

			server, err := buildServerConfig(args)
			if err != nil {
				return "", err
			}

			if err := api.validateMCPConfig(&mcpclient.MCPConfig{
				MCPServers: map[string]mcpclient.MCPServerConfig{name: server},
			}); err != nil {
				return fmt.Sprintf("Invalid MCP server config: %v", err), nil
			}

			userConfig.MCPServers[name] = server
			if err := mcpclient.SaveConfig(userConfigPath, userConfig); err != nil {
				return "", fmt.Errorf("failed to save user MCP config: %w", err)
			}

			api.invalidateServerDiscovery(name, "Configuration saved — discovering tools...")
			api.startServerDiscovery(GetUserIDFromContext(ctx), name)

			return fmt.Sprintf("Saved user MCP server %q and started discovery. After discovery completes, select it with update_workflow_config(add_servers=[name]) in a workflow, or update_project_mcp_server_selection(action=select, server=name) in an active Work project. The chat catalog refreshes on the next turn; no server restart is needed.", name), nil
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"edit_mcp_server",
		"Edit an existing shared platform MCP server configuration, then trigger discovery. Requires admin access; in single-user setups the user is the admin, so proceed instead of asking for one. Base catalog servers cannot be edited from chat.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Existing user-defined server name.",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Optional human-readable description.",
				},
				"protocol": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"stdio", "sse", "http"},
					"description": "Optional explicit protocol. If omitted, the backend infers it from url or command when possible.",
				},
				"command": map[string]interface{}{
					"type":        "string",
					"description": "Command to run for stdio servers.",
				},
				"args": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Command arguments for stdio servers.",
				},
				"env": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": map[string]interface{}{"type": "string"},
					"description":          "Optional environment variables for stdio servers.",
				},
				"working_dir": map[string]interface{}{
					"type":        "string",
					"description": "Optional working directory for stdio servers.",
				},
				"url": map[string]interface{}{
					"type":        "string",
					"description": "URL for SSE or HTTP MCP servers.",
				},
				"headers": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": map[string]interface{}{"type": "string"},
					"description":          "Optional HTTP headers for SSE or HTTP MCP servers.",
				},
			},
			"required": []string{"name"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if isMCPConfigLocked() {
				return "MCP configuration is locked (MCP_CONFIG_LOCKED is set), so chat cannot edit servers.", nil
			}

			name, _ := args["name"].(string)
			name = strings.TrimSpace(name)
			if name == "" {
				return "name is required.", nil
			}

			if _, exists := api.mcpConfig.MCPServers[name]; exists {
				return fmt.Sprintf("Server %q is part of the base/admin config and can't be edited from chat.", name), nil
			}

			userConfigPath, userConfig, err := loadUserConfig()
			if err != nil {
				return "", err
			}
			if _, exists := userConfig.MCPServers[name]; !exists {
				return fmt.Sprintf("User-defined MCP server %q does not exist. Use add_mcp_server first.", name), nil
			}

			server, err := buildServerConfig(args)
			if err != nil {
				return "", err
			}
			if err := api.validateMCPConfig(&mcpclient.MCPConfig{
				MCPServers: map[string]mcpclient.MCPServerConfig{name: server},
			}); err != nil {
				return fmt.Sprintf("Invalid MCP server config: %v", err), nil
			}

			userConfig.MCPServers[name] = server
			if err := mcpclient.SaveConfig(userConfigPath, userConfig); err != nil {
				return "", fmt.Errorf("failed to save user MCP config: %w", err)
			}

			api.appendServerLog(name, "info", "Server configuration edited from multi-agent chat, triggering discovery...")
			go api.triggerMCPDiscovery()

			return fmt.Sprintf("Updated user MCP server %q and started discovery refresh.", name), nil
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"remove_mcp_server",
		"Remove a shared platform MCP server configuration. Requires admin access; in single-user setups the user is the admin, so proceed instead of asking for one. This affects Work, AgentWorks chats, workflows, and schedules. Base catalog servers cannot be removed from chat.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Server name to remove.",
				},
			},
			"required": []string{"name"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if isMCPConfigLocked() {
				return "MCP configuration is locked (MCP_CONFIG_LOCKED is set), so chat cannot remove servers.", nil
			}

			name, _ := args["name"].(string)
			name = strings.TrimSpace(name)
			if name == "" {
				return "name is required.", nil
			}

			if _, exists := api.mcpConfig.MCPServers[name]; exists {
				return fmt.Sprintf("Server %q is part of the base/admin config and can't be removed from chat.", name), nil
			}

			userConfigPath, userConfig, err := loadUserConfig()
			if err != nil {
				return "", err
			}
			if _, exists := userConfig.MCPServers[name]; !exists {
				return fmt.Sprintf("User-defined MCP server %q was not found.", name), nil
			}

			// Removal is platform-wide (this account's config), but a workflow
			// that attached this server via update_workflow_config(add_servers)
			// keeps a dangling reference — nothing else checks or cleans that up.
			// Warn about every workflow affected so the agent can actually tell
			// the user, instead of them finding out later when a run breaks.
			affectedWorkflows := workflowsReferencingMCPServer(ctx, name)

			delete(userConfig.MCPServers, name)
			if err := mcpclient.SaveConfig(userConfigPath, userConfig); err != nil {
				return "", fmt.Errorf("failed to save user MCP config: %w", err)
			}

			api.appendServerLog(name, "info", "Server removed from user MCP config")
			go api.triggerMCPDiscovery()

			if len(affectedWorkflows) == 0 {
				return fmt.Sprintf("Removed user MCP server %q and started discovery refresh. No workflow had it attached.", name), nil
			}
			return fmt.Sprintf("Removed user MCP server %q and started discovery refresh. WARNING: it is still attached to %d workflow(s), which now reference a server that no longer exists: %s. Tell the user this before they next run one of those workflows — offer to detach it there with update_workflow_config(remove_servers=[%q]).",
				name, len(affectedWorkflows), strings.Join(affectedWorkflows, ", "), name), nil
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"get_mcp_server_logs",
		"Show recent logs for a specific MCP server, or list which servers currently have logs.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Optional server name. If omitted, the tool lists servers that currently have stored logs.",
				},
			},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			name, _ := args["name"].(string)
			name = strings.TrimSpace(name)

			api.serverLogsMux.RLock()
			defer api.serverLogsMux.RUnlock()

			if name == "" {
				if len(api.serverLogs) == 0 {
					return "No MCP server logs are currently stored.", nil
				}
				names := make([]string, 0, len(api.serverLogs))
				for serverName := range api.serverLogs {
					names = append(names, serverName)
				}
				sort.Strings(names)
				return fmt.Sprintf("Servers with stored MCP logs: %s", strings.Join(names, ", ")), nil
			}

			logs := api.serverLogs[name]
			if len(logs) == 0 {
				return fmt.Sprintf("No stored logs for MCP server %q.", name), nil
			}

			start := 0
			if len(logs) > 20 {
				start = len(logs) - 20
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("## MCP Server Logs: %s\n\n", name))
			for _, entry := range logs[start:] {
				sb.WriteString(fmt.Sprintf("- %s [%s] %s\n", entry.Timestamp.Format(time.RFC3339), entry.Level, entry.Message))
			}
			return sb.String(), nil
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"trigger_mcp_discovery",
		"Trigger MCP server discovery in the background after config changes or when you want to refresh server tool metadata.",
		map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			cfg, err := api.loadMergedConfig()
			if err != nil {
				return "", err
			}
			for name, server := range cfg.MCPServers {
				if server.OAuth != nil {
					oauthConfig := *server.OAuth
					if strings.TrimSpace(oauthConfig.TokenFile) == "" {
						oauthConfig.TokenFile = getUserTokenFilePath(platformMCPTokenUserID, name)
					}
					server.OAuth = &oauthConfig
					if hasOAuthTokenFile(server) {
						api.startServerDiscovery(platformMCPTokenUserID, name)
					}
				}
			}
			go api.triggerMCPDiscovery()
			return "Triggered discovery for the shared AgentWorks MCP connections in the background.", nil
		},
	); err != nil {
		return err
	}

	return nil
}

// truncateString truncates s to maxLen and appends "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

type queryToolCallSummary struct {
	ToolCallID string
	ToolName   string
	Status     string
	Duration   time.Duration
	StartedAt  time.Time
	Args       string
	Result     string
	SessionID  string
}

// makeStepTmuxLookup returns a TmuxLookupFunc that resolves the live tmux session
// for a workshop step running a coding-CLI provider (tmux transport) and captures a
// fresh tail of its pane. It scans the terminal store for a tmux-backed snapshot
// matching the step (the same session shown in the UI terminal), then runs a live
// `tmux capture-pane` so query_step returns CURRENT output — the store's cached
// Content is only refreshed when the UI views the pane, so it can be stale for a
// headless builder. The capture result is also written back to keep the store warm.
func makeStepTmuxLookup(api *StreamingAPI) todo_creation_human.TmuxLookupFunc {
	capturer := tmuxcapture.Capturer{Run: func(ctx context.Context, args ...string) (string, error) {
		return runTerminalTmuxOutputCommand(ctx, args...)
	}}
	return func(ctx context.Context, mainSessionID, stepID string) (string, string, bool) {
		if api == nil || api.terminalStore == nil {
			return "", "", false
		}
		if strings.TrimSpace(mainSessionID) == "" || strings.TrimSpace(stepID) == "" {
			return "", "", false
		}
		for _, snap := range api.terminalStore.ListRaw(mainSessionID) {
			if snap.StepID != stepID {
				continue
			}
			ts := strings.TrimSpace(snap.TmuxSession)
			if ts == "" || !strings.EqualFold(snap.StepTransport, "tmux") {
				continue
			}
			var tail string
			cctx, cancel := context.WithTimeout(ctx, terminalTmuxActionTimeout)
			capture, err := capturer.CaptureAgentTail(cctx, ts, tmuxcapture.Options{})
			cancel()
			if err == nil && strings.TrimSpace(capture.PaneContent) != "" {
				api.terminalStore.RefreshContent(snap.TerminalID, capture.PaneContent)
				tail = capture.Text
			}
			return ts, tail, true
		}
		return "", "", false
	}
}

// formatToolCallSummaries returns a ToolCallQueryFunc that formats event-store
// tool calls plus live HTTP bridge snapshots from per-step MCP sessions.
// When toolCallID is empty, returns a summary with truncated args/results.
// When toolCallID is set, returns full input/output for that specific call.
func formatToolCallSummaries(api *StreamingAPI) todo_creation_human.ToolCallQueryFunc {
	return func(mainSessID, correlationID, stepID, toolCallID string) string {
		summaries := collectQueryToolCallSummaries(api, mainSessID, correlationID, stepID)
		if len(summaries) == 0 {
			return ""
		}

		// Detailed mode: find specific tool call and return full args/result
		if toolCallID != "" {
			for _, tc := range summaries {
				if tc.ToolCallID == toolCallID {
					var sb strings.Builder
					sb.WriteString(fmt.Sprintf("**%s** [%s]", tc.ToolName, strings.ToUpper(tc.Status)))
					if tc.Duration > 0 {
						sb.WriteString(fmt.Sprintf(" (%s)", tc.Duration.Round(time.Millisecond)))
					}
					sb.WriteString(fmt.Sprintf("\ntool_call_id: %s", tc.ToolCallID))
					if tc.SessionID != "" {
						sb.WriteString(fmt.Sprintf("\nsession_id: %s", tc.SessionID))
					}
					if tc.Args != "" {
						sb.WriteString(fmt.Sprintf("\n\n**Input:**\n```json\n%s\n```", tc.Args))
					}
					if tc.Result != "" {
						sb.WriteString(fmt.Sprintf("\n\n**Output:**\n```\n%s\n```", tc.Result))
					}
					return sb.String()
				}
			}
			return fmt.Sprintf("tool_call_id %q not found", toolCallID)
		}

		// Summary mode: truncated args/results
		var sb strings.Builder
		for i, tc := range summaries {
			if i > 0 {
				sb.WriteString("\n")
			}
			switch tc.Status {
			case "running":
				sb.WriteString(fmt.Sprintf("- [RUNNING] %s (id: %s)", tc.ToolName, tc.ToolCallID))
			case "done":
				if tc.Duration > 0 {
					sb.WriteString(fmt.Sprintf("- [DONE] %s (%s) (id: %s)", tc.ToolName, tc.Duration.Round(time.Millisecond), tc.ToolCallID))
				} else {
					sb.WriteString(fmt.Sprintf("- [DONE] %s (id: %s)", tc.ToolName, tc.ToolCallID))
				}
			case "error":
				if tc.Duration > 0 {
					sb.WriteString(fmt.Sprintf("- [ERROR] %s (%s) (id: %s)", tc.ToolName, tc.Duration.Round(time.Millisecond), tc.ToolCallID))
				} else {
					sb.WriteString(fmt.Sprintf("- [ERROR] %s (id: %s)", tc.ToolName, tc.ToolCallID))
				}
			default:
				sb.WriteString(fmt.Sprintf("- [%s] %s (id: %s)", strings.ToUpper(tc.Status), tc.ToolName, tc.ToolCallID))
			}
			if tc.SessionID != "" {
				sb.WriteString(fmt.Sprintf("\n  Session: %s", tc.SessionID))
			}
			if tc.Args != "" {
				sb.WriteString(fmt.Sprintf("\n  Args: %s", truncateString(tc.Args, 200)))
			}
			if tc.Result != "" {
				sb.WriteString(fmt.Sprintf("\n  Result: %s", truncateString(tc.Result, 200)))
			}
		}
		return sb.String()
	}
}

func collectQueryToolCallSummaries(api *StreamingAPI, mainSessID, correlationID, stepID string) []queryToolCallSummary {
	var summaries []queryToolCallSummary
	seen := make(map[string]struct{})

	add := func(tc queryToolCallSummary) {
		if tc.ToolCallID == "" {
			return
		}
		if _, exists := seen[tc.ToolCallID]; exists {
			return
		}
		seen[tc.ToolCallID] = struct{}{}
		summaries = append(summaries, tc)
	}

	if api != nil && api.eventStore != nil && mainSessID != "" && correlationID != "" {
		for _, tc := range api.eventStore.GetToolCallsByCorrelation(mainSessID, correlationID) {
			add(queryToolCallSummary{
				ToolCallID: tc.ToolCallID,
				ToolName:   tc.ToolName,
				Status:     tc.Status,
				Duration:   tc.Duration,
				StartedAt:  tc.StartedAt,
				Args:       tc.Args,
				Result:     tc.Result,
			})
		}

		// For workshop step executions, also include tool calls from API-based delegation
		// sub-agents. CLI sub-agents share the parent MCP session (covered by the prefix
		// scan below), but API sub-agents emit events under their own delegation correlation ID.
		if strings.HasPrefix(correlationID, "workshop-step-") {
			for _, delegID := range getStepDelegations(correlationID) {
				for _, tc := range api.eventStore.GetToolCallsByCorrelation(mainSessID, delegID) {
					add(queryToolCallSummary{
						ToolCallID: tc.ToolCallID,
						ToolName:   tc.ToolName,
						Status:     tc.Status,
						Duration:   tc.Duration,
						StartedAt:  tc.StartedAt,
						Args:       tc.Args,
						Result:     tc.Result,
					})
				}
			}
		}
	}

	addSnapshots := func(calls []toolcalllog.SnapshotCall) {
		for _, call := range calls {
			duration := time.Duration(0)
			if !call.StartedAt.IsZero() && !call.CompletedAt.IsZero() {
				duration = call.CompletedAt.Sub(call.StartedAt)
			}
			status := call.Status
			if status == "" {
				status = "done"
			}
			add(queryToolCallSummary{
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Status:     status,
				Duration:   duration,
				StartedAt:  call.StartedAt,
				Args:       call.ArgsJSON,
				Result:     call.Result,
				SessionID:  call.SessionID,
			})
		}
	}

	// Direct lookup covers background task sessions or future callers that pass
	// the actual MCP session id as correlationID.
	if correlationID != "" {
		addSnapshots(toolcalllog.Snapshot(correlationID))
	}

	// Workflow step agents use dedicated MCP session ids such as
	// sub-exec-<stepID>-<timestamp>. Those HTTP bridge calls do not always land
	// in the parent chat event store before query_step runs, so query the live
	// bridge log by the deterministic session prefix too.
	if stepID != "" {
		for _, kind := range []string{"exec", "todo", "learn", "kb-update", "kb-consolidate", "kb-reorganize"} {
			addSnapshots(toolcalllog.SnapshotBySessionPrefix(fmt.Sprintf("sub-%s-%s-", kind, stepID)))
		}
	}

	sort.SliceStable(summaries, func(i, j int) bool {
		left := summaries[i].StartedAt
		right := summaries[j].StartedAt
		if left.IsZero() || right.IsZero() {
			return i < j
		}
		if left.Equal(right) {
			return summaries[i].ToolCallID < summaries[j].ToolCallID
		}
		return left.Before(right)
	})

	return summaries
}

func workshopConvertAgentLLMConfig(config *workflowtypes.AgentLLMConfig) *todo_creation_human.AgentLLMConfig {
	if config == nil {
		return nil
	}
	return &todo_creation_human.AgentLLMConfig{
		PublishedLLMID: config.PublishedLLMID,
		Provider:       config.Provider,
		ModelID:        config.ModelID,
		Options:        config.Options,
		ConnectionID:   config.ConnectionID,
	}
}

func workshopConvertTieredLLMConfig(config *workflowtypes.TieredLLMConfig) *todo_creation_human.TieredLLMConfig {
	if config == nil {
		return nil
	}

	tiered := &todo_creation_human.TieredLLMConfig{
		Tier1: workshopConvertAgentLLMConfig(config.Tier1),
		Tier2: workshopConvertAgentLLMConfig(config.Tier2),
		Tier3: workshopConvertAgentLLMConfig(config.Tier3),
	}

	if tiered.Tier1 == nil || tiered.Tier2 == nil || tiered.Tier3 == nil {
		log.Printf("[WORKSHOP] Partial tiered LLM config detected: T1=%t T2=%t T3=%t",
			tiered.Tier1 != nil, tiered.Tier2 != nil, tiered.Tier3 != nil)
	}

	return tiered
}

// generateTextWorkflowTiers converts the current workflow manifest's model
// roles into the compact config consumed by generate_text_llm. Provider-profile
// workflows (including Cursor managing the full workflow) are expanded first,
// so this path never consults the unrelated global delegation-tier settings.
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
	if config == nil {
		return nil, nil
	}
	if builder, tiered, ok := workflowtypes.ResolveProviderProfileConfig(config); ok {
		return workshopConvertAgentLLMConfig(builder), workshopConvertTieredLLMConfig(tiered)
	}

	builder := workshopConvertAgentLLMConfig(config.BuilderLLM)
	var tiered *todo_creation_human.TieredLLMConfig
	if config.Mode == workflowtypes.LLMConfigModeExplicit && config.TieredConfig != nil {
		tiered = workshopConvertTieredLLMConfig(config.TieredConfig)
	}
	return builder, tiered
}

func workshopResolvePulseLLMConfig(config *workflowtypes.PresetLLMConfig) *todo_creation_human.AgentLLMConfig {
	if config == nil {
		return nil
	}
	if resolved, ok := workflowtypes.ResolveProviderProfilePulseConfig(config); ok {
		return workshopConvertAgentLLMConfig(resolved)
	}
	if config.PulseLLM != nil && config.PulseLLM.Provider != "" && config.PulseLLM.ModelID != "" {
		return workshopConvertAgentLLMConfig(config.PulseLLM)
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

// queryAdmissionError is a refusal from admitQueryTarget.
type queryAdmissionError struct {
	err            error
	invalidProfile bool
}

func (e *queryAdmissionError) Error() string {
	if e.invalidProfile {
		return "Invalid agent profile request: " + e.err.Error()
	}
	return e.err.Error()
}

func (e *queryAdmissionError) Unwrap() error { return e.err }

// admitQueryTarget resolves a turn's target and the caller's access to it:
// saved chat config, the agent profile, the workflow-phase folder, then
// conversationTargetAccess. handleQuery and the bot dry run
// (bot_dry_run.go) share it, so a dry run admits exactly what a real turn
// admits.
func (api *StreamingAPI) admitQueryTarget(ctx context.Context, req *QueryRequest, currentUserID, sessionID string) (*resolvedAgentProfile, WorkflowAccessLevel, *queryAdmissionError) {
	api.applySavedMultiAgentChatConfig(ctx, req, currentUserID)
	resolvedProfile, err := api.resolveAgentProfileForQuery(ctx, req, currentUserID, sessionID)
	if err != nil {
		return nil, WorkflowAccessNone, &queryAdmissionError{err: err, invalidProfile: true}
	}
	// Workflow-phase payloads identify the workspace through their preset,
	// not selected_folder (the client never sends it for these chats).
	// Resolve it BEFORE access: otherwise conversationTargetAccess skips the
	// manifest and answers from the account tier, so a manifest reader chats
	// as Builder while every tool call fails the permission-drift guard
	// once the folder is backfilled later in handleQuery.
	api.backfillWorkflowPhaseFolder(ctx, req)
	access, err := api.conversationTargetAccess(ctx, *req)
	if err != nil {
		return nil, WorkflowAccessNone, &queryAdmissionError{err: err}
	}
	return resolvedProfile, access, nil
}
