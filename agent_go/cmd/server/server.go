package server

import (
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
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/noderuntime"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"

	"github.com/manishiitg/coding-agent-loop/workspace/gitcred"

	"github.com/joho/godotenv"
	eventbridge "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/event_bridge"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/cliupdate"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codingsdks"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/inspector"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/knowledgebaseproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/platformtools"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminalleases"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	agent "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentwrapper"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/clisecurity"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/guidancesessions"
	browserinstructions "github.com/manishiitg/coding-agent-loop/agent_go/pkg/instructions"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/logger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	orchtypes "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/types"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/voicestt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/agent/codeexec"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/observability"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/nativereplies"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cleanupClaudeCodeProviderSessions = llmproviders.CleanupClaudeCodeTmuxSessions
	cleanupCodexCLIProviderSessions   = llmproviders.CleanupCodexCLIInteractiveSessions
	cleanupCursorCLIProviderSessions  = llmproviders.CleanupCursorCLIInteractiveSessions
	cleanupPiCLIProviderSessions      = llmproviders.CleanupPiCLIInteractiveSessions
)

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
	CostAttribution costledger.Attribution `json:"-"`
	SessionID       string                 `json:"session_id"`
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
	LastWorkedAt                time.Time        `json:"-"` // real work only (events, tool calls, status); not idle housekeeping
	CreatedAt                   time.Time        `json:"created_at"`
	Query                       string           `json:"query,omitempty"`
	Title                       string           `json:"title,omitempty"`
	WorkflowName                string           `json:"workflow_name,omitempty"`
	WorkflowLabel               string           `json:"workflow_label,omitempty"`
	WorkspacePath               string           `json:"workspace_path,omitempty"`
	CrewProjectID               string           `json:"crew_project_id,omitempty"`
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
	desktopFailedAcks desktopFailedAckCache
	crewPanels        crewPanelCache

	crewTurnSnapshots sync.Map // execution ID -> *crewTurnSnapshot; no transcript content

	localDevices                sync.Map // user/device -> authenticated outbound executor
	codingAgentClarificationsMu sync.Mutex
	codingAgentClarifications   map[string]*pendingCodingAgentClarification
	// questionsRestored marks chats whose saved question cards this process has reloaded (sessionID -> true).
	questionsRestored sync.Map

	internalExternalBuilderTurn func(context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error)
	postSlackMessage            func(context.Context, string, string, string) (string, error) // test seam; production uses SlackService

	botExecutionSessions sync.Map // session -> trusted connector request context

	playwrightLive      playwrightLiveRegistry
	accessTokenSessions accessTokenSessionRegistry
	guidanceStoreMu     sync.Mutex
	guidanceStore       *guidancesessions.Store
	guidanceStorePath   string
	uiControlOnce       sync.Once
	uiControl           *uiControlBroker
	config              ServerConfig
	cliSecurityStore    *clisecurity.Store
	agentProfiles       *agentprofiles.Registry
	projectActions      *ProjectActionDispatcher
	gmailInbound        *gmailinbound.Service

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
	// internalPreparedAgent captures the finalized production assembly in tests.
	// Returning true ends the request before any turn or model invocation.
	internalPreparedAgent func(context.Context, *mcpagent.Agent) bool
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
	agentCancelFuncs    map[string]context.CancelFunc
	agentCancelMux      sync.RWMutex
	shutdownTurnCancels sync.Map // session -> context.CancelCauseFunc; Stop keeps its own cause

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
	conversationHistory  map[string][]llmtypes.MessageContent
	conversationMux      sync.RWMutex
	resumeHistorySources map[string]*resumeHistorySource

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
	liveAttach         *liveAttachManager
	nativeObserversMu  sync.Mutex
	nativeObservers    map[string]*nativeTerminalObserver
	nativeBackgroundMu sync.Mutex
	nativeBackground   map[string]map[string]llmtypes.NativeRecord
	nativeCostStopped  atomic.Bool
	nativeCostsMu      sync.Mutex
	nativeCosts        map[string]*nativeCostAccountant

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
	// Scheduled agents consume this marker so an interrupted turn is
	// never mistaken for a successfully completed (idle) turn and advanced.
	interruptedTurns   map[string]bool
	interruptedTurnsMu sync.Mutex
	// pendingTurnStops holds a Stop pressed while a turn was still starting (no cancel function yet); the turn
	// cancels itself as soon as it registers one. Cleared when the session stops being busy.
	pendingTurnStops   map[string]time.Time
	pendingTurnStopsMu sync.Mutex

	// Orchestrator objects in memory for guidance injection
	workflowOrchestrators    map[string]orchestrator.Orchestrator
	workflowOrchestratorsMux sync.RWMutex

	// Background agent registry for async delegation in multi-agent mode
	bgAgentRegistry *BackgroundAgentRegistry

	// Session busy tracking — prevents synthetic turns from overlapping with user turns
	sessionBusy      map[string]bool
	sessionBusySince map[string]time.Time
	sessionBusyMu    sync.RWMutex
	turnInactivityMu sync.Mutex
	turnInactivity   map[string]map[string]*turnInactivityState

	// retainedMainTurns tracks direct follow-up turns submitted to a persistent
	// coding-CLI tmux after the server-managed foreground Go turn has ended.
	// Their structured terminal events remain available (the same events used by
	// Terminal Center's Formatted view), so keep an explicit runtime lifecycle
	// instead of guessing activity solely from the latest rendered spinner text.
	retainedMainTurns            map[string]time.Time
	retainedMainTurnExecutionIDs map[string]string
	// A retained CLI can accept another message while its current response is
	// still running. Coding providers queue that input as a distinct turn, so
	// preserve the corresponding execution roots in submission order. Inputs
	// sharing a native turn finish together; later native turns keep their owner.
	retainedMainTurnPendingExecutionIDs map[string][]string
	retainedMainTurnCanonicalIDs        map[string]retainedTurnIdentity
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

	// Pending completions queue — background agent IDs that finished while session was busy
	pendingCompletions map[string][]string
	pendingMu          sync.RWMutex
	// completionRetryScheduled guards schedulePendingCompletionRetry so at most one
	// retry timer runs per session at a time. Guarded by pendingMu.
	completionRetryScheduled    map[string]bool
	completionRetryBusyAttempts map[string]int // pendingMu; busy retries back off without discarding results

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
	sessionWorkspaceFolders map[string]string // sessionID → conversation workspace
	sessionWorkspaceMu      sync.RWMutex

	// Stored agent instances for synthetic turns (plan mode only)
	// Reused directly via StreamWithEvents() instead of re-creating agents per synthetic turn
	sessionAgents    map[string]*agent.LLMAgentWrapper
	sessionAgentsMux sync.RWMutex

	// Running agent references for steer message injection (chat mode)
	// Written when agent starts, deleted when streaming completes
	runningAgents    map[string]*mcpagent.Agent
	runningAgentsMux sync.RWMutex
	// promptParts is each chat's last measured request parts (sessionID -> mcpagent.PromptPartSizes), kept after the turn ends.
	promptParts sync.Map

	// Per-session turn lane shared by chat, Crew, workflows and internal
	// producers. Busy retained coding-CLI input is persisted by the dispatcher
	// instead of relying on a provider-local tmux queue.
	sessionInputLanes   map[string]*sessionInputLane
	sessionInputLanesMu sync.Mutex
	// Durable turns waiting behind that shared lane. Every producer reaches the
	// same dispatcher; tmux remains an execution transport, never a queue.
	pendingChatUploadsMu          sync.Mutex
	pendingChatUploads            map[pendingChatUploadKey]*pendingChatUpload
	conversationTurnQueueMu       sync.Mutex
	conversationTurnQueueOwners   map[string]map[string]bool
	conversationTurnQueueDraining map[string]bool
	conversationTurnQueueWatching map[string]bool
	// Startup resume gate (conversation_turn_queue_startup.go), under conversationTurnQueueMu.
	startupResumeRunning map[string]bool
	startupResumeWaiting []string
	queuedTurnRunner     func(queuedConversationTurn) // tests only; nil runs the real turn
	// desktopThreadStates replaces the network read of a thread's request states (tests only; nil reads the network).
	desktopThreadStates          func(owner string, record ProductConversationRecord, thread string) (map[string]desktopTaskRemoteState, error)
	conversationTurnQueueWakes   map[string]*conversationTurnQueueWake
	conversationTurnQueueRetries map[string]*conversationQueueRetryState
	// Sessions whose queued message waits only to relaunch the CLI with a changed
	// runtime (definition or provider); see endIdleTurnForRuntimeChange.
	conversationTurnQueueRuntimeChange map[string]bool
	conversationTurnQueueWaiters       map[string]chan queuedConversationTurnResult
	conversationTurnQueueCallbacks     map[string]func(event *unifiedevents.AgentEvent)
	internalTurnQueueRead              func(context.Context, string) (string, bool, error)
	internalTurnQueueWrite             func(context.Context, string, string) error
	internalNativeGoalResume           func(context.Context, map[string]interface{}, string, string) error

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
	// watcherReadSessions are Watcher checks' read-only app bridge sessions (watcher_reads.go).
	watcherReadSessions sync.Map

	// Cron scheduler service for scheduled workflow executions
	scheduler           *SchedulerService
	webhookInvocations  sync.Map // session ID -> *step_based_workflow.WebhookInvocation; internal only
	scheduleInvocations sync.Map // session ID -> *step_based_workflow.ScheduleInvocation; internal only

	// Background completion loop tracking — prevents multiple loops per session
	completionLoopStarted   map[string]bool
	completionLoopStartedMu sync.Mutex

	toolStatus    map[string]ToolStatus
	toolStatusMux sync.RWMutex
	mcpConfig     *mcpclient.MCPConfig // shared; use the helpers in mcp_base_config.go
	mcpConfigMu   sync.RWMutex

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

	// In-process Team sync handler, built once (crewTeamServerHandler).
	crewTeamHandlerOnce sync.Once
	crewTeamHandler     http.Handler
}

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
	ServerCmd.AddCommand(rotateAuthSecretCmd)
	ServerCmd.AddCommand(addUserCmd)
	ServerCmd.AddCommand(setMCPAppCmd)
}

func runServer(cmd *cobra.Command, args []string) {
	// With per-user accounts, what the server creates must stay writable by the user's slot group.
	slots.ApplyServiceUmask()
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

	// A backend launched inside tmux must not route CLI launches to its parent
	// server while supervision uses the configured TMUX_TMPDIR socket.
	if clearInheritedTmuxEnvironment() {
		log.Printf("[STARTUP] cleared inherited TMUX and TMUX_PANE")
	}

	cliUpdateCtx, stopCLIUpdates := context.WithCancel(context.Background())
	defer stopCLIUpdates()
	cliUpdatesEnabled := !strings.EqualFold(strings.TrimSpace(os.Getenv("CLI_UPDATE_ENABLED")), "false") && !strings.EqualFold(strings.TrimSpace(os.Getenv("CLI_UPDATE_DRY_RUN")), "true")
	// A local install with no Node.js gets the app's own copy on first need.
	if localProductInstallation() {
		if stateRoot, err := workflowCLIStateRoot(); err == nil {
			node := noderuntime.New(filepath.Join(stateRoot, "node"))
			if node.Activate() {
				log.Printf("[NODE] using AgentWorks' own Node.js")
			}
			noderuntime.SetDefault(node)
			defer noderuntime.SetDefault(nil)
		}
	}
	if root, err := cliupdate.DefaultRoot(); err == nil {
		sdks, sdkErr := codingsdks.New(cliUpdateCtx, filepath.Join(filepath.Dir(root), "coding-sdks"), cliUpdatesEnabled && localProductInstallation(), log.Printf)
		if sdkErr != nil {
			log.Printf("[CODING SDK] setup unavailable; update the app and try again")
		} else {
			llmtypes.SetCodingSDKSetup(sdks.Ensure)
			defer llmtypes.SetCodingSDKSetup(nil)
			sdks.Start()
		}
	}
	if cliUpdatesEnabled {
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

	if ownerReport := migrateProductOwners(fsutil.WorkspaceDocsRoot()); ownerReport.Scanned > 0 || len(ownerReport.Failures) > 0 {
		log.Printf("[OWNER_BACKFILL] scanned=%d registered=%d already_registered=%d conflicts=%d stamped=%d current=%d skipped=%d manifest_mismatch=%d unsafe=%d shared_orphans=%d failures=%d",
			ownerReport.Scanned, ownerReport.Registered, ownerReport.Registry, ownerReport.Conflicts, ownerReport.Stamped, ownerReport.Current, ownerReport.Skipped, ownerReport.Mismatched, ownerReport.Unsafe, ownerReport.SharedOrphans, len(ownerReport.Failures))
		for _, failure := range ownerReport.Failures {
			log.Printf("[OWNER_BACKFILL] failure: %s", failure)
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
	// Cache the auth secret in memory, then clear it from the environment so
	// no child process (agent shells, CLIs, tmux panes, anything exec'd with
	// the default env) can inherit it. All readers go through GetAuthSecret
	// and the services key cache from here on. Mirrors the MCP token handling
	// below, but earlier, so even startup-time spawns never see the secret.
	InitAuthSecretCache()
	services.CacheSecretsKey(string(GetAuthSecret()))
	ClearAuthSecretFromEnv()
	if err := loadRevokedSessions(); err != nil {
		log.Printf("[AUTH] Signed-out sessions not loaded: %v", err)
	}
	// Open the server state database (its one-off import of the former
	// config/users.json reads through the workspace API), then import
	// AUTH_USERS and apply ADMIN_USERS. Retried briefly because the workspace
	// server usually starts in parallel with this one.
	go func() {
		for attempt := 0; attempt < 60; attempt++ {
			if _, err := openServerState(context.Background()); err == nil {
				bootstrapUserDirectory(context.Background())
				importAllAutomationRecords(context.Background())
				rewriteAutomationRecordScriptPaths(context.Background())
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
	startBrainAutoPush(context.Background())
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
	eventStore.EnableTurnPhase()
	eventStateRoot, err := workflowCLIStateRoot()
	if err != nil {
		log.Fatalf("Failed to resolve durable structured-event state: %v", err)
	}
	eventJournal, err := events.OpenSQLiteEventJournal(filepath.Join(eventStateRoot, "structured-chat-events-v2.sqlite"))
	if err != nil {
		log.Fatalf("Failed to initialize durable structured-event journal: %v", err)
	}
	eventStore.SetDurableJournal(eventJournal)
	if err := nativereplies.Configure(filepath.Join(eventStateRoot, "native-reply-claims")); err != nil {
		log.Printf("[NATIVE_RECORD] gap reason=claim_restore_failed")
	}
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
	// The AgentBridge mailbox is this process's own SQLite database; the first
	// start after the change imports the old JSON files once.
	if _, err := openAgentMailbox(fsutil.WorkspaceDocsRoot()); err != nil {
		log.Fatalf("Failed to open the AgentBridge mailbox: %v", err)
	}
	costledger.SetDefaultLedger(costLedger)
	// Cost days (daily totals, "today") follow the display time zone when the server sets one, else local time.
	if zone := strings.TrimSpace(os.Getenv("DISPLAY_TIME_ZONE")); zone != "" {
		if loc, err := time.LoadLocation(zone); err == nil {
			costledger.SetDayLocation(loc)
		} else {
			log.Printf("[COSTS] DISPLAY_TIME_ZONE %q is not a time zone; cost days use local time: %v", zone, err)
		}
	}
	defer costledger.SetDefaultLedger(nil)
	legacyCostPath := filepath.Join(fsutil.WorkspaceDocsRoot(), "_system", "costs.jsonl")
	if report, ran, migrateErr := costLedger.MigrateLegacyJSONLIfChanged(legacyCostPath); migrateErr != nil {
		// SQLite is already initialized and remains authoritative. A damaged or
		// unreadable compatibility file must not make every future startup fail.
		log.Printf("[COST_LEDGER] Legacy migration skipped after error: %v", migrateErr)
	} else if ran && (report.Imported > 0 || report.Duplicates > 0 || report.Quarantined > 0) {
		log.Printf("[COST_LEDGER] Legacy migration imported=%d duplicates=%d quarantined=%d",
			report.Imported, report.Duplicates, report.Quarantined)
	}
	// Cursor Auto calls recorded before Auto had an estimated price.
	if n, repriceErr := costLedger.RepriceUnpriced(estimateCursorAutoCost); repriceErr != nil {
		log.Printf("[COST_LEDGER] Repricing Cursor Auto calls skipped after error: %v", repriceErr)
	} else if n > 0 {
		log.Printf("[COST_LEDGER] Priced %d earlier Cursor Auto call(s) at the estimated average", n)
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
	var slackSvc *services.SlackService
	if os.Getenv("SLACK_ENABLED") != "false" && !IsLocalMode() {
		slackSvc, err = services.InitSlackService()
	}
	if err != nil {
		log.Printf("⚠️  Failed to initialize Slack service: %v (Slack integration will be disabled)", err)
	} else if slackSvc == nil {
		if IsLocalMode() {
			log.Printf("[SLACK] Slack bot disabled in local mode — skipping startup connections (Slack as me remains available)")
		} else {
			log.Printf("[SLACK] Disabled via SLACK_ENABLED=false — skipping startup connections on this instance")
		}
	} else {
		log.Printf("✅ Slack service initialized")
	}

	// Initialize Gmail service (single-user, backed by the `gws` CLI). Unlike
	// Slack/WhatsApp it is send-only, so it registers directly with the
	// NotificationManager rather than the BotConversationManager.
	gmailSvc, err := services.InitGmailService()
	if err != nil {
		log.Printf("⚠️  Failed to initialize Gmail service: %v (Gmail integration will be disabled)", err)
	} else {
		// Registration is independent of company defaults: an item's selected
		// personal sender is checked by the connector for each delivery.
		log.Printf("✅ Gmail service initialized")
		if notificationManager != nil {
			notificationManager.RegisterConnector(gmailSvc)
		}
	}

	chromeStateRoot, chromeStateErr := workflowCLIStateRoot()
	if chromeStateErr != nil {
		log.Fatalf("Cannot resolve Chrome selection state: %v", chromeStateErr)
	}
	chromeRelay, chromeStateErr := browserrelay.NewPersistent(filepath.Join(chromeStateRoot, "chrome-extension"))
	if chromeStateErr != nil {
		log.Fatalf("Cannot load Chrome selection state: %v", chromeStateErr)
	}
	browserrelay.Default = chromeRelay
	defer chromeRelay.Close()

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
	if productEnabled("agentworks") {
		// Scaffolding only: no bespoke tools yet, so no
		// RegisterAgentProfileRuntime call -- see product.yaml's own comment
		// for why this registration is inert until something explicitly
		// requests AgentProfileID="agentworks".
		if err := agentworksproduct.RegisterProductSkills(); err != nil {
			log.Printf("[SKILLS] AgentWorks skills: some were not registered; the server keeps running: %v", err)
		}
		for _, profile := range agentworksproduct.BuiltinAgentProfiles() {
			profile.Product = "agentworks"
			if err := profileRegistry.RegisterProfile(profile); err != nil {
				log.Fatalf("Failed to register AgentWorks agent profile: %v", err)
			}
		}
	}
	if productEnabled("mcp-gateway") && strings.TrimSpace(os.Getenv("CAPLAYER_SERVICE_URL")) != "" {
		profile := caplayerproduct.BuiltinAgentProfile()
		profile.Product = "mcp-gateway"
		if err := profileRegistry.RegisterProfile(profile); err != nil {
			log.Fatalf("Failed to register CapLayer profile: %v", err)
		}
	}
	if productEnabled(knowledgebaseproduct.ProfileID) {
		if err := profileRegistry.RegisterProfile(knowledgebaseproduct.BuiltinAgentProfile()); err != nil {
			log.Fatalf("Failed to register Brain profile: %v", err)
		}
		if err := knowledgebaseproduct.RegisterAgentProfileRuntime(profileRegistry, knowledgebaseAccessExecutor); err != nil {
			log.Fatalf("Failed to register Brain runtime: %v", err)
		}
		// The chat is told the caller's own role: without it the model could not tell an administrator from anyone else
		// and asked the owner whether they were one before it would set up backup (server A, 2026-10-06).
		if err := profileRegistry.RegisterPromptVariables(knowledgebaseproduct.ProfileID, func(_ context.Context, rt agentprofiles.RuntimeContext) (map[string]string, error) {
			return map[string]string{"CALLER": knowledgebaseCallerLine(rt.UserID)}, nil
		}); err != nil {
			log.Fatalf("Failed to register Brain prompt variables: %v", err)
		}
	}
	if productEnabled("work") {
		if err := workproduct.RegisterProductSkills(); err != nil {
			log.Printf("[SKILLS] Work skills: some were not registered; the server keeps running: %v", err)
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
		pendingCompletions:                  make(map[string][]string),
		completionRetryScheduled:            make(map[string]bool),
		pendingStartNotifications:           make(map[string][]string),
		startNotificationRetryScheduled:     make(map[string]bool),
		lastQueryRequests:                   make(map[string]QueryRequest),
		sessionWorkspaceFolders:             make(map[string]string),
		sessionAgents:                       make(map[string]*agent.LLMAgentWrapper),
		runningAgents:                       make(map[string]*mcpagent.Agent),
		sessionInputLanes:                   make(map[string]*sessionInputLane),
		conversationTurnQueueOwners:         make(map[string]map[string]bool),
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
	defer api.closeGuidanceSessions()
	workflowToolsAPI = api
	runningServerAPI = api
	startMemoryLog(eventStore, api)
	mcpagent.SetNativeRuntimeObserver(api.observeNativeHandle)
	defer mcpagent.SetNativeRuntimeObserver(nil)
	api.initNativeCostAccounting()
	initAccountBillingForCosts()
	defer api.stopNativeCostAccounting()
	defer api.stopNativeCodingObservers()
	api.restoreNativeCodingObservers()
	events.SetTurnDiagnostics(api.turnDiagnostics)
	mcpagent.SetAccountPhraseResolver(api.accountPhraseForSession)
	// An MCP connection's header secrets are the Vault secrets its Crew, Code or
	// workflow selects, lent by the owner (projectSecretValue).
	if productEnabled("mcp-gateway") && strings.TrimSpace(os.Getenv("CAPLAYER_SERVICE_URL")) != "" {
		if err := registerVaultManagementTools(profileRegistry, api); err != nil {
			log.Fatalf("Failed to register Vault management tools: %v", err)
		}
		if err := caplayerproduct.RegisterRuntime(profileRegistry, api.capLayerConnectionAccess); err != nil {
			log.Fatalf("Failed to register Vault tools: %v", err)
		}
	}
	projectSecretReader = api.projectSecretValue
	// Private MCP stores remain private; do not copy user logins into projects.
	// Terminal Center's Formatted view and the runtime coordinator now consume
	// the same accepted structured events. The terminal observer updates the
	// durable pane snapshot first; retained-turn reconciliation then uses that
	// authoritative result to settle only the matching main-agent turn.
	eventStore.SetEventPreparer(func(sessionID string, event *events.Event) {
		api.prepareNativeGoalEvent(sessionID, event)
		prepareUserMessageAttachments(event)
		tagRunCompactionEvent(sessionID, event)
	})
	eventStore.SetEventAddedCallback(func(sessionID string, event events.Event) {
		terminalEventObserver(sessionID, event)
		api.noteCrewTeamPreviewEvent(sessionID, event)
		// Runtime ownership starts observation. Opening a terminal is never
		// required for committed replies or native helper activity to arrive.
		api.observeNativeCodingRuntime(sessionID, event)
		api.observeAccountLimits(sessionID, event)
		api.observeAccountHealth(sessionID, event)
		api.observeTurnInactivity(sessionID, event)
		api.observeNativeBackgroundCommands(sessionID, event)
		api.observeUsageLimitFailure(sessionID, event)
		api.observeNativeGoalWakeCompletion(sessionID, event)
		api.observeRetainedMainTurnEvent(sessionID, event)
		api.settleNativeForeground(sessionID, event)
		api.notifyNeedsYou(sessionID, event)
		api.observeWaitingChange(sessionID, event)
		if event.Data != nil {
			if call, ok := unifiedevents.ToolCallRecordFromEvent(event.Data.Data); ok {
				api.logPersonalSlackCall(context.Background(), sessionID, call.ServerName, call.ToolName, call.Status)
			}
		}
	})

	// Some tool calls never produce a tool_call_end on the live stream, which
	// left the chat showing a finished command as unresolved. The provider's own
	// transcript has the result and the real runtime; this lets the event store
	// ask for them (PLAT-141).
	api.installToolResultRecovery()

	// BG-001: Wire the onDropped callback so a full notification channel re-queues
	// the completion instead of silently losing it permanently.
	api.bgAgentRegistry.onDropped = func(sessionID, agentID string) {
		api.queuePendingCompletion(sessionID, agentID)
		api.schedulePendingCompletionRetry(sessionID)
	}

	// Reap leftover agent-browser helpers every ~10 minutes (PLAT-662): orphaned
	// daemons, per-chat helpers whose chat ended, and day-old runtime files.
	browser.SetHelperLiveSessions(func() []string {
		var live []string
		for sessionID, browserSession := range common.BoundBrowserSessions() {
			if api.sessionHasActiveWork(sessionID) {
				live = append(live, browserSession)
			}
		}
		return live
	})
	browser.StartHelperReaper()

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
	if err := api.loadManagedGlobalSecrets(context.Background()); err != nil {
		log.Fatalf("Failed to load managed global secrets: %v", err)
	}
	// Register shared secret names with Vault so they are grantable and
	// revocable without an administrator opening Vault > Secrets first. Then move
	// any secret still kept in a project's own vault into its owner's Vault.
	startVaultSecretRegistration(api.startProjectVaultMove)

	// Setup routes
	router, apiRouter := api.newServerRouter()

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
	// Claude's own background tasks that outlive a turn are shown under the last reply (PLAT-872).
	api.wireClaudeBackgroundTasks()
	api.wireBackgroundOutput()
	api.wireCursorSubagentRows()

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
	if slackSvc != nil && !IsLocalMode() {
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
	BotRoutes(apiRouter, api)
	virtualtools.SetSlackMessageHandler(api.sendSlackMessageFromTool)
	virtualtools.SetSlackCLIHandler(api.slackCLIFromTool)
	virtualtools.SetGitHubToolHandler(api.githubFromTool)
	if slackSvc != nil && !IsLocalMode() {
		slackSvc.SetTriggerHandler(api.dispatchSlackTrigger)
	}
	if api.whatsappManager != nil {
		WhatsAppRoutes(apiRouter, api.whatsappManager, api.whatsappDefaultProfileResolver)
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
			svc := currentVoiceService()
			if !svc.Status().Installed {
				return "", services.ErrVoiceNotInstalled
			}
			samples, err := voicestt.DecodeFile(ctx, path)
			if err != nil {
				return "", fmt.Errorf("decode voice note: %w", err)
			}
			return svc.Transcribe(ctx, samples)
		})
	}
	// Set activity callback for event store to update session LastActivity when events are added
	eventStore.SetActivityCallback(func(sessionID string) {
		api.updateSessionActivity(sessionID)
	})

	// Start background cleanup goroutine to mark inactive sessions (10 minute timeout)
	goSafe("cleanup_inactive_sessions", api.cleanupInactiveSessions)
	goSafe("idle_compaction_sweeper", api.runIdleCompactionSweeper)
	musecli.SetMuseMSPReplyHandler(api.recordMuseWakeReply)
	goSafe("schedule_summary_live_feed", api.watchScheduleSummaryForLiveFeed)
	goSafe("warm_llm_config_caches", api.warmLLMConfigCaches)
	goSafe("preload_provider_account_settings", preloadProviderAccountSettings)

	if err := retireBrainSchedules(fsutil.WorkspaceDocsRoot()); err != nil {
		log.Printf("[BRAIN_SCHEDULES] event=retirement_failed error=%v", err)
	}
	rewritePersonalWatcherScriptPaths(fsutil.WorkspaceDocsRoot())

	// Initialize and start the cron scheduler
	// Set SCHEDULER_ENABLED=false in .env to disable on secondary machines sharing the same workspace files.
	schedulerCtx, schedulerCancel := context.WithCancel(context.Background())
	defer schedulerCancel()
	schedulerSvc := NewSchedulerService(api)
	api.scheduler = schedulerSvc
	if err := schedulerSvc.initializeRunLedger(); err != nil {
		log.Printf("[SCHEDULER] Run ledger initialization failed: %v", err)
	}
	productScheduleSvc := NewProjectActionDispatcher(api, profileRegistry)
	api.projectActions = productScheduleSvc
	// One-off, before any function can be called: a function's saved "fresh" becomes "new" (migration file; delete
	// once every server has started on this version).
	if err := renameFunctionFreshToNew(); err != nil {
		log.Printf("[CREW_FUNCTIONS] event=fresh_rename_failed error=%v", err)
	}
	// One-off, before any turn: a Crew's saved workflow links are removed, Crew links stay (migration file; delete
	// once every server has started on this version).
	if err := clearCrewWorkflowLinks(); err != nil {
		log.Printf("[CREW_LINKS] event=workflow_links_clear_failed error=%v", err)
	}
	// Recover accepted conversation turns before either scheduler can compete
	// for those same session lanes. Both services are already wired so a
	// recovered turn sees the normal runtime capabilities.
	api.recoverConversationTurnQueue(schedulerCtx)
	api.recoverAgentMessages()
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
	SchedulerRoutes(apiRouter, schedulerSvc)
	WorkflowWebhookRoutes(apiRouter, schedulerSvc)
	ProductWebhookRoutes(apiRouter, productScheduleSvc)
	UnifiedWebhookRoutes(apiRouter, schedulerSvc, productScheduleSvc)
	stopGmailInbound := api.initGmailInbound(apiRouter)
	defer stopGmailInbound()

	api.registerProductRoutes(router, apiRouter)

	// Pre-bind listener so we can support dynamic port (port 0) and report the actual port
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", config.Host, config.Port))
	if err != nil {
		log.Fatalf("Failed to listen on %s:%d: %v", config.Host, config.Port, err)
	}
	actualPort := listener.Addr().(*net.TCPAddr).Port
	desktopSocket, err := startDesktopSocket(listener.Addr().(*net.TCPAddr))
	if err != nil {
		log.Fatalf("Failed to start local desktop connection socket: %v", err)
	}
	if desktopSocket != nil {
		defer desktopSocket.Close()
		log.Printf("[DESKTOP] Separate-launch connection socket ready")
	}
	stopTeamSync := api.startCrewTeamSync()
	defer stopTeamSync()
	notifyCtx, stopNotifier := context.WithCancel(context.Background())
	defer stopNotifier()
	api.startNotifier(notifyCtx)
	stopAgentBridge := api.startDesktopAgentBridge()
	defer stopAgentBridge()
	stopDesktopCrewChats := api.startDesktopCrewChats()
	defer stopDesktopCrewChats()
	stopThisMacConnections := api.startThisMacConnections()
	defer stopThisMacConnections()
	stopMacWaiting := api.startCrewMacWaiting()
	defer stopMacWaiting()
	SetShareTunnelServerPort(actualPort)

	api.registerPublicRoutes(router, actualPort)

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

	// Keep the service's own tokens out of the tmux server that hosts the CLIs (PLAT-663).
	startTmuxServerEnvCheck()

	// Register the owner of every workflow that has none yet (whose Vault its selected secrets come from).
	go backfillWorkflowOwners(context.Background())

	if localSocket, err := startLocalAgentSocket(router, listener.Addr().(*net.TCPAddr)); err != nil {
		log.Printf("[AGENTBRIDGE] event=local_bootstrap result=failed reason=%q", err.Error())
	} else if localSocket != nil {
		defer localSocket.Close()
	}

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

	// Stop the schedule clock first: a schedule that fires while active work is
	// being cancelled starts a run that is killed at once and later shows as
	// interrupted.
	schedulerCancel()

	// Stop taking Slack messages next, so none start new work while active
	// work is cancelled and handlers drain.
	if slackSvc != nil {
		slackSvc.StopListening()
	}

	// Cancel active agents first. Coding-agent tmux sessions are owned by those
	// turns, so killing tmux before the handlers observe cancellation can race
	// with capture/poll commands and make shutdown hang.
	fmt.Println("⏹️ Canceling active agent work...")
	// Marked before cancelling: a cancelled turn kicks its chat's queue, and a queued message started now is killed
	// at once and saved as failed. Queued messages stay queued for the next startup instead.
	serverShuttingDown.Store(true)
	cancelStart := time.Now()
	api.cancelActiveWorkForShutdown()
	stopGmailInbound()
	fmt.Printf("✅ Active agent work canceled (%s)\n", time.Since(cancelStart).Round(time.Millisecond))

	// An internet share tunnel must not outlive the server it exposes.
	StopShareTunnel()
	closeVoiceService()

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
	serverShuttingDown.Store(true)
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

	// Close the durable stores last, once handlers and runs have stopped, so
	// their final writes land and SQLite checkpoints its write-ahead logs.
	schedulerSvc.Stop()
	if err := eventJournal.Close(); err != nil {
		log.Printf("Event journal close failed: %v", err)
	}
	closeAgentMailbox()
	closeServerState()

	fmt.Println("✅ Server shutdown complete")
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

// handleQuery runs current Crew/product chats and direct saved-Python executions.
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
	if !validChatDeliveryMode(req.DeliveryMode) {
		http.Error(w, "delivery_mode must be steer or queue", http.StatusBadRequest)
		return
	}
	principalContext, principalErr := api.revalidateExecutionPrincipal(r.Context(), req)
	if principalErr != nil {
		logQueryAdmissionRefusal(w, r, "execution_principal")
		http.Error(w, principalErr.Error(), http.StatusForbidden)
		return
	}
	r = r.WithContext(principalContext)
	req.oauthRedirectURI = deriveOAuthRedirectURI(r)
	if err := api.bindSlackInvocation(r.Context(), &req, r.Header.Get("X-Session-ID")); err != nil {
		logQueryAdmissionRefusal(w, r, "slack_invocation")
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	api.clearBotOriginForOwnerTurn(r.Header.Get("X-Session-ID"), GetUserFromContext(r.Context()), req)
	if target, ok := r.Context().Value(resolvedResumeTargetContextKey{}).(*resolvedResumeTarget); ok {
		req.resolvedResumeTarget = target
	}
	if ask, ok := r.Context().Value(crewMacSharedAskKey{}).(*crewMacSharedAsk); ok && req.SharedAgentSender != "" {
		req.sharedAsk = ask
	}
	// The queue retains server-owned delivery provenance separately from request
	// JSON and restores it only for its exact user/session. Keep replies on that inbox.
	if req.SharedAgentSender != "" {
		if delivery, ok := r.Context().Value(deliveredAgentMessageKey{}).(map[string]interface{}); ok {
			req.appAgentReplyTo, _ = delivery["app_agent_reply_to"].(string)
		}
	}
	if err := validateRequestedCDPPorts(req); err != nil {
		http.Error(w, "Invalid CDP configuration: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Preserve caller browser input before the profile adds current workspace settings.
	r = r.WithContext(withQueuedBrowserInput(r.Context(), req))
	input := r.Context().Value(queuedBrowserInputKey{}).(queuedBrowserInput)
	req.callerBrowserInput = &input

	// A workflow may keep durable read-only links in workflow.json. Merge those
	// with one-message # references before the common authorization boundary so
	// both forms receive identical access checks and folder guards.
	if contextErr := admitWorkflowBuilderContextPaths(r.Context(), &req); contextErr != nil {
		logQueryAdmissionRefusal(w, r, "workflow_context")
		http.Error(w, contextErr.Error(), http.StatusForbidden)
		return
	}

	// Handle alias: Map Message to Query if Query is empty
	if req.Query == "" && req.Message != "" {
		req.Query = req.Message
	}

	// Validate required fields
	if req.Query == "" && len(req.PendingAttachments) == 0 && req.RequestedSkillID == "" {
		errorMsg := "Query is required"
		http.Error(w, errorMsg, http.StatusBadRequest)
		return
	}

	// Record start time for duration calculation
	startTime := time.Now()

	// Generate query ID
	queryID := fmt.Sprintf("query_%d", time.Now().UnixNano())
	if ask := crewAskInputFromContext(r.Context()); ask != nil {
		queryID = ask.Query
	}

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
	if req.AgentMode != "workflow" && !isToolBackedChatMode(req.AgentMode) {
		http.Error(w, "Choose a project chat, or run a saved Python workflow.", http.StatusBadRequest)
		return
	}

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
		logQueryAdmissionRefusal(w, r, "session_access")
		http.Error(w, refusal, http.StatusForbidden)
		return
	}
	queryLogCtx := requestLogContext(r.Context(), req, sessionID)
	registerServerLogContext(queryLogCtx, sessionID, queryID)
	queryLogger := queryLogCtx.Logger(api.logger)
	queryPreview := truncateForLog(req.Query, 80)
	if command, known, _ := parseNativeGoalCommand(req.Query, sessionID); known {
		queryPreview = "native_goal:" + command.Action
	}
	events.TurnSpeedStart(sessionID, startTime)
	logfWithContext(queryLogCtx, "[LATENCY_DEBUG] T+0ms | Request received | query_preview=%q", queryPreview)
	logfWithContext(queryLogCtx, "[USER_ID_DEBUGGING] HTTP handler: currentUserID=%q (from auth context)", currentUserID)

	// PLAT-262: resolved fresh from this request's own live auth claims, not
	// cached anywhere — a demotion takes effect on the caller's very next
	// request. Gates mutating tool registration and workflow-folder write
	// access below; never gates WorkshopMode (that axis stays prompt-only
	// focus, per docs/design/agent_tool_surface_single_source.md).
	currentUserIsReadOnly := workflowAccessForClaims(GetUserFromContext(r.Context())) == WorkflowAccessRead

	resolvedProfile, access, admitErr := api.admitQueryTarget(r.Context(), &req, currentUserID, sessionID)
	if admitErr != nil {
		if writeWorkspaceAdmissionUnavailable(w, admitErr) {
			return
		}
		if errors.Is(admitErr, errCrewProjectUnavailable) {
			logQueryAdmissionRefusal(w, r, "project_access")
			externalError(w, http.StatusForbidden, "project_unavailable", errCrewProjectUnavailable.Error())
			return
		}
		status := http.StatusForbidden
		if admitErr.invalidProfile {
			status = http.StatusBadRequest
		}
		logQueryAdmissionRefusal(w, r, "query_target")
		http.Error(w, admitErr.Error(), status)
		return
	}
	if resolvedProfile != nil && resolvedProfile.Publication != nil {
		r = r.WithContext(context.WithValue(r.Context(), publicationExecutionKey{}, resolvedProfile.Publication))
	}

	if err := validateTeamQuery(r.Context(), req); err != nil {
		logQueryAdmissionRefusal(w, r, "team_admission")
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	currentUserIsReadOnly = readOnlyForRequest(access, req)
	if resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID) {
		if e := crewQueryTabWriteError(r.Context(), currentUserID, resolvedProfile.Definition.ID, req.SelectedFolder, sessionID); e != nil {
			logQueryAdmissionRefusal(w, r, "tab_owner")
			externalError(w, 403, "tab_owner", e.Error())
			return
		}
	}
	if err := api.validatePendingChatAttachments(currentUserID, sessionID, req); err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, err.Error())
		return
	}
	r = r.WithContext(api.bindReadyChatAttachments(r.Context(), currentUserID, sessionID, &req))
	// Provider accounts: every turn re-checks that this principal may use the
	// account the turn names here, before any retained CLI gets the message.
	// A denied account fails the turn; it never falls back to another one.
	accountScope := queryProviderAccountScope(req, currentUserID, resolvedProfile)
	// A Slack channel turn runs as the bot identity; its shared-account
	// tokens count toward, and are limited by, the target's owner.
	botTokenOwner := botRouteTokenOwner(r.Context(), currentUserID, accountScope.WorkspacePath)
	turnProvider, turnConnectionID := api.finalQueryTurnConnection(r.Context(), req, sessionID)
	personalTabAccount, accountScopeErr := crewChatUsesPersonalAccountScope(r.Context(), currentUserID, sessionID, req, resolvedProfile, turnProvider, turnConnectionID)
	if accountScopeErr != nil {
		http.Error(w, accountScopeErr.Error(), http.StatusConflict)
		return
	}
	// Inherited Crew accounts use its main owner. An explicit saved tab account
	// uses that tab's owner, including company-account limits and attribution.
	if coOwnerOf := coOwnerTurnOwner(r.Context(), currentUserID, accountScope.WorkspacePath); botTokenOwner == "" && coOwnerOf != "" && !personalTabAccount {
		botTokenOwner = coOwnerOf
		accountScope.Principal = coOwnerOf
		log.Printf("[CREW_OWNERS] session=%s turn by co_owner=%s runs as main owner=%s (accounts, limits)", sessionID, sanitizeUserIDForPath(currentUserID), coOwnerOf)
	}
	accountScope.TokenOwner = botTokenOwner
	req.tokenOwner = botTokenOwner
	// Allowed models follow the same person (PLAT-714): a person's override of
	// a shared account's list applies to their turns and to bot turns billed
	// to them.
	modelLimitPersonID := firstNonEmptyTrimmed(botTokenOwner, currentUserID)
	// The FINAL account of the turn: a workflow chat that does not override
	// the manifest runs on the workflow's saved model and account.
	var admittedQueryAccount *storedProviderConnection
	if turnProvider != "" {
		admitID := turnConnectionID
		if admitID == "" {
			// No account named: the turn runs on the server account. Checked
			// here too, so a retained CLI relaunch or live input is covered.
			admitID = serverDefaultConnectionID(turnProvider)
		}
		var accountErr error
		admittedQueryAccount, accountErr = api.admitProviderAccount(r.Context(), accountScope, turnProvider, admitID)
		if accountErr != nil {
			status := http.StatusForbidden
			var limitErr *sharedAccountTokenLimitError
			if botTokenOwner != "" && errors.As(accountErr, &limitErr) {
				// The bot posts this message in the thread (botSessionFailureMessage).
				status = http.StatusTooManyRequests
			}
			logQueryAdmissionRefusal(w, r, "provider_account")
			http.Error(w, accountErr.Error(), status)
			return
		}
	}
	crewGuest := crewGuestCallerForTurn(req, currentUserID)
	if crewGuest != "" {
		currentUserIsReadOnly = true
	}
	// One policy for every non-owner chat (in-app, MCP ask, dashboard Ask): read-only plus the owner's published functions.
	crewAskOnly := crewAskReadOnlyTurn(req, currentUserID, currentUserIsReadOnly)
	// A message of the other role (the owner's while someone else's shared ask runs, or the reverse) never steers into
	// the running turn and never changes its tools: it waits as its own turn (PLAT-856-509). The session's tool mode
	// belongs to the turn that is running.
	otherRoleTurn := false
	if !conversationTurnQueueExecution(r.Context()) && api.sessionTurnRunning(sessionID) {
		if cfg := common.GetSessionShellConfig(sessionID); cfg != nil && cfg.CrewAskReadOnly != crewAskOnly {
			otherRoleTurn = true
			log.Printf("[TURN_QUEUE] session=%s queued: a %s message waits for the running %s turn", sessionID, map[bool]string{true: "shared-ask", false: "owner"}[crewAskOnly], map[bool]string{true: "shared-ask", false: "owner"}[cfg.CrewAskReadOnly])
		}
	}
	var goalHandled bool
	r, goalHandled = api.prepareNativeGoalQuery(w, r, &req, sessionID, turnProvider, !otherRoleTurn, !currentUserIsReadOnly && !crewAskOnly)
	if goalHandled {
		return
	}
	var planHandled bool
	if r, planHandled = api.prepareNativePlanQuery(w, r, &req, sessionID, turnProvider); planHandled {
		return
	}
	if next, err := api.prepareRequestedSkill(r, &req, sessionID, turnProvider); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	} else {
		r = next
	}
	if !otherRoleTurn {
		common.SetSessionWorkflowReadOnly(sessionID, currentUserIsReadOnly)
		common.SetSessionCrewReader(sessionID, currentUserIsReadOnly && resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID))
		common.SetSessionCrewAskReadOnly(sessionID, crewAskOnly)
		common.SetSessionReadOnlyAccess(sessionID, access == WorkflowAccessRead)
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
		agentToolsMode = "full"
	}

	api.lastAgentToolsModeBySession[sessionID] = agentToolsMode
	api.conversationMux.Unlock()
	if api.internalAgentToolsModeDecided != nil && api.internalAgentToolsModeDecided(sessionID, agentToolsMode) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := api.validateQuerySecretSelection(r.Context(), currentUserID, req); err != nil {
		logQueryAdmissionRefusal(w, r, "secret_selection")
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
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
	bindConversationBrowserIsolation(sessionID, currentUserID, selected, resolvedProfile)
	common.SetSessionBrowserMode(sessionID, getBrowserMode(req))
	// Keep configured browser intent on the request. In auto mode,
	// agent_browser queries current CDP reachability at tool-call time.

	// Default maxTurns only when omitted (0). Negative values are preserved to mean "no turn cap".
	// Chat runs uncapped by default.
	if req.MaxTurns == 0 {
		if isToolBackedChatMode(req.AgentMode) {
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

	// Idle compaction (PLAT-872): a compaction turn runs only on an idle chat,
	// and a person's send waits (bounded) for a running one.
	idleRun := idleCompactRunFromContext(r.Context())
	if idleRun != nil && (api.conversationTurnOccupied(sessionID) || !idleRun.beforeQueued && api.conversationTurnHasBacklog(currentUserID, sessionID)) {
		http.Error(w, "turn_running", http.StatusConflict)
		return
	}
	if idleRun == nil && !req.IsAutoNotification {
		api.waitForIdleCompaction(r.Context(), sessionID, "query")
	}
	if idleRun == nil && !req.IsAutoNotification && !isScheduledSessionIdentity(sessionID, req.TriggeredBy) {
		var finishSubmission func()
		var accepted bool
		w, r, finishSubmission, accepted = api.beginChatSubmission(w, r, sessionID, req.SelectedFolder, pendingAttachmentSubmissionQuery(req))
		if !accepted {
			return
		}
		defer finishSubmission()
	}
	if isQueuedMessageSteer(r.Context()) {
		if _, canSteer := api.queuedMessageDeliveryState(sessionID, req); !canSteer {
			rejectUnavailableQueuedSteer(w)
			return
		}
	}
	// Admit pending-file messages to the existing durable lane immediately.
	// Its drainer waits for file commit; later messages cannot overtake it.
	if shouldQueuePendingAttachments(r.Context(), req) {
		api.queueConversationTurnResponse(w, r, currentUserID, sessionID, req)
		return
	}
	deferPendingSetup := api.deferPendingSetupForLiveInput(r.Context(), req, sessionID, otherRoleTurn)
	if req.RestartCodingCLI && deferPendingSetup {
		api.setConversationTurnRuntimeChange(sessionID, true)
		req.RestartCodingCLI = false
		log.Printf("[QUERY->LIVE] session=%s action=steering reason=setup_deferred_to_next_turn", sessionID)
	}
	if req.RestartCodingCLI {
		// The product chat's runtime changed while a turn was running (PLAT-676): this message waits
		// for that turn, then the CLI is relaunched for it. Closing it on arrival killed the running
		// Muse turn ("muse tmux session ... died before run completion", server B 2026-10-07).
		if api.queueOccupiedConversationTurnForRuntimeChange(w, r, currentUserID, sessionID, req) {
			return
		}
		retireProductCodingCLI(sessionID, "product chat: runtime configuration changed")
		req.RestartCodingCLI = false
	}
	if otherRoleTurn || api.shouldWaitForQueuedConversation(r.Context(), currentUserID, sessionID, req) {
		// Hold the existing lane across the backlog check and admission. If
		// the drainer acquired it first, admission still appends behind it.
		release, acquired := api.tryLockSessionInputLane(sessionID, 0)
		if acquired {
			defer release()
		}
		api.queueConversationTurnResponse(w, r, currentUserID, sessionID, req)
		return
	}
	preferRetainedDelivery := shouldTryRetainedDeliveryBeforeQueue(r.Context(), req, sessionID)
	if !preferRetainedDelivery && api.queueOccupiedConversationTurn(w, r, currentUserID, sessionID, req) {
		return
	}

	if err := api.resolvePendingChatAttachments(r.Context(), currentUserID, sessionID, &req); err != nil {
		log.Printf("[CHAT_ATTACHMENTS] caller=%q session=%q outcome=rejected reason=%q", currentUserID, sessionID, err.Error())
		writeAgentProfileError(w, http.StatusBadRequest, err.Error())
		return
	}

	r = r.WithContext(api.withNativeChatImages(r.Context(), currentUserID, sessionID, turnProvider, req))
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
	if !retainedProfileCompatible && deferPendingSetup {
		api.setConversationTurnRuntimeChange(sessionID, true)
		retainedProfileCompatible = true
		log.Printf("[QUERY->LIVE] session=%s action=steering reason=profile_refresh_deferred_to_next_turn", sessionID)
	}
	productDefinitionRefreshed := resolvedProfile != nil && !retainedProfileCompatible
	if isQueuedMessageSteer(r.Context()) && !retainedProfileCompatible {
		api.setConversationTurnRuntimeChange(sessionID, true)
		logTurnQueue("session %s: queued steer deferred because the retained product definition changed", sessionID)
		rejectUnavailableQueuedSteer(w)
		return
	}
	if productDefinitionRefreshed {
		log.Printf("[CHAT_HISTORY] Product definition changed for session %s; relaunching the coding CLI and resuming its native session where supported", sessionID)
	}
	// A different coding provider than the retained CLI's makes that CLI the wrong target, whatever the definition says: a message must never be steered into
	// (or fail against) the old provider's terminal. Before this a provider change was invisible here, so after switching Muse to Codex mid-chat the next sends went to the old Muse
	// record and answered 409 delivery_uncertain until a watchdog cleared it (Code on server B, 2026-10-04). Like any runtime change it waits for a running turn
	// and then relaunches on the selected provider (the native conversation resumes across providers).
	providerChanged := !req.IsAutoNotification && api.retainedCLIProviderDiffers(sessionID, api.effectiveProviderOf(r.Context(), req))
	if providerChanged {
		req.providerSwitchedFrom = api.retainedCLIProvider(sessionID)
		log.Printf("[CHAT_HISTORY] Provider changed for session %s: the retained CLI is not %s; relaunching on the selected provider", sessionID, api.effectiveProviderOf(r.Context(), req))
		retainedProfileCompatible = false
	}
	// Whether this turn is one the person typed in their own chat decides if it gets their own apps (#4165).
	markOwnChatTurn(sessionID, currentUserID, req.SelectedFolder, personTypedChatTurn(r.Context(), req, sessionID, crewAskOnly))
	// Vault apps changed since the chat started and it is time to pick them up (#3528): rebuild like a runtime change.
	vaultReload := !req.IsAutoNotification && retainedProfileCompatible && api.vaultAppsReloadDue(r.Context(), sessionID, req)
	if vaultReload {
		retainedProfileCompatible = false
	}
	if !retainedProfileCompatible && !req.DisableLiveInputDelivery && !req.IsAutoNotification && (providerChanged || vaultReload || !requestLLMConfigOverridesManifest(req)) {
		// A changed runtime (coding agent, model, reasoning effort, definition) applies between turns, never in the middle of one:
		// relaunching here used to cancel the running turn ("muse tmux session ... died before run completion" after changing the
		// reasoning effort mid-turn, server B 2026-10-03). While a turn is running, the message waits in the durable turn queue;
		// when it runs, this check sees no running turn and relaunches with the new runtime. A retained CLI that is idle at its
		// prompt does not count as mid-turn: the queue watcher ends that turn and the message runs (see endIdleTurnForRuntimeChange).
		if api.queueOccupiedConversationTurnForRuntimeChange(w, r, currentUserID, sessionID, req) {
			return
		}
		api.interruptWorkflowPolicySession(sessionID, req.Provider)
	}
	// Isolated automation, functions and webhooks preserve turn boundaries.
	// A scheduled Crew-chat message has trusted chat-routing intent and uses
	// the same compatible live-input attempt as a typed message.
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
	retainedWorkflowCompatible, runtimeChanged, policyErr := api.prepareWorkflowRetainedDelivery(r.Context(), sessionID, req, retainedDeliveryEligible)
	if policyErr != nil {
		if writeWorkspaceAdmissionUnavailable(w, policyErr) {
			return
		}
		logQueryAdmissionRefusal(w, r, "retained_tool_policy")
		http.Error(w, policyErr.Error(), http.StatusForbidden)
		return
	}
	if retainedDeliveryEligible && !retainedWorkflowCompatible {
		// Steer now may only deliver into the current compatible turn. A
		// changed policy must leave this message queued, never tear down the
		// CLI (and its in-flight tools) while handling a queued steer.
		if isQueuedMessageSteer(r.Context()) {
			api.setConversationTurnRuntimeChange(sessionID, true)
			logTurnQueue("session %s: queued steer deferred because retained workflow policy differs (runtime_changed=%t)", sessionID, runtimeChanged)
			log.Printf("[QUERY->LIVE] session=%s action=queued reason=workflow_policy_changed", sessionID)
			rejectUnavailableQueuedSteer(w)
			return
		}
		if runtimeChanged && api.queueOccupiedConversationTurnForRuntimeChange(w, r, currentUserID, sessionID, req) {
			return
		}
		api.interruptWorkflowPolicySession(sessionID, req.Provider)
	}
	if retainedWorkflowCompatible {
		r = r.WithContext(contextWithSessionMode(r.Context(), agentSessionModeForTurn(req, currentUserID, sessionID, resolvedProfile, currentUserIsReadOnly)))
	}
	if retainedWorkflowCompatible {
		// The turn this input starts is paid by the chat's owner; its usage rows name who sent it.
		costobserver.SetRetainedInputSource(sessionID, req.TriggeredBy, req.TriggeredByLabel)
		if !otherRoleTurn {
			// Refresh this turn's selected SECRET_* keys so retained coding CLIs
			// see newly selected/saved keys (and Run/Ask turns see none) in
			// shell and browser secret typing without a relaunch.
			turnSecrets := api.turnSelectedSecretEnv(r.Context(), sessionID, currentUserID, req, resolvedProfile, crewAskOnly, nil)
			virtualtools.ReplaceSessionShellSecrets(sessionID, turnSecrets)
		}
	}
	r = r.WithContext(context.WithValue(r.Context(), retainedQueueInputKey{}, retainedQueueInput{Request: req}))
	var recoveredInput []string
	// A /plan message never goes into a terminal as typed text; the turn's program refuses plan mode there.
	if retainedWorkflowCompatible && !llmtypes.NativePlanModeForSession(r.Context(), sessionID) && api.tryDeliverQueryAsLiveInput(w, r, sessionID, req.Query, queryID, &recoveredInput, requestReceivedAt) {
		return
	}
	if len(recoveredInput) > 0 {
		req.Query = strings.Join(append(recoveredInput, req.Query), "\n\n")
	}
	if isQueuedMessageSteer(r.Context()) {
		if pending, ok := r.Context().Value(queuedMessageRecoveredInputKey{}).(*[]string); ok {
			*pending = append(*pending, recoveredInput...)
		}
		// No compatible live target accepted delivery. Keep the original queue
		// item instead of silently starting a turn or inserting a second item.
		rejectUnavailableQueuedSteer(w)
		return
	}

	// A human message gets one attempt at a compatible retained CLI before it
	// becomes a new turn. If no target exists, this persisted queue owns the
	// message; an uncertain send above never falls through and double-dispatches.
	// Claimed queue workers and producers without trusted chat intent skip retained delivery.
	if preferRetainedDelivery && api.queueOccupiedConversationTurn(w, r, currentUserID, sessionID, req) {
		return
	}

	// Only genuinely new turns use the backend lane. Taking this before the
	// retained-live-input check made normal follow-ups wait behind slow CLI
	// startup and caused later messages/notifications to arrive in bursts.
	var releaseInputLane func()
	if shouldSerializeInteractiveQueryInput(req) {
		// User input has priority over a synthetic completion turn. Cancel it
		// before waiting for the shared turn lane; otherwise the user would wait
		// behind the very synthetic turn it is meant to interrupt.
		if !req.IsAutoNotification && api.isSyntheticTurn(sessionID) {
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
			laneWait := sessionInputLaneInteractiveWait
			if idleRun != nil {
				laneWait = 0 // an idle compaction never waits for a turn
			}
			releaseInputLane, acquired = api.tryLockSessionInputLane(sessionID, laneWait)
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
	}
	defer func() {
		if releaseInputLane != nil {
			releaseInputLane()
		}
	}()
	// Another request may have durably queued while this request acquired
	// the lane. Give the drainer its older turn before doing any agent work.
	if releaseInputLane != nil && shouldYieldToConversationBacklog(r.Context()) && shouldUseDurableConversationTurnQueue(req) && api.conversationTurnHasBacklog(currentUserID, sessionID) {
		if idleRun != nil {
			http.Error(w, "turn_running", http.StatusConflict)
			return
		}
		api.queueConversationTurnResponse(w, r, currentUserID, sessionID, req)
		// The deferred lane release kicks the drainer after durable admission.
		return
	}
	if _, delegated := r.Context().Value(agentMessageAdmissionKey{}).(func(string) (func(), error)); delegated {
		releaseAuthority, admissionErr := acquireAgentMessageAuthority(r.Context(), sessionID)
		if admissionErr != nil {
			logQueryAdmissionRefusal(w, r, "agent_message_authority")
			http.Error(w, admissionErr.Error(), http.StatusForbidden)
			return
		}
		releaseLane := releaseInputLane
		releaseInputLane = func() {
			releaseAuthority()
			if releaseLane != nil {
				releaseLane()
			}
		}
	}

	if receipt, ok := r.Context().Value(nativeGoalRestoreContextKey{}).(string); ok && !api.nativeGoalRestoreCurrent(r.Context(), sessionID, receipt) {
		log.Printf("[AGENT_GOAL] session=%s restore=%s action=skipped reason=restore_canceled_before_admission", sessionID, receipt)
		http.Error(w, "Goal restoration was canceled", http.StatusConflict)
		return
	}
	if previous, err := api.latestNativeGoal(r.Context(), sessionID); err == nil && previous != nil {
		if command, ok := llmtypes.NativeGoalCommandForSession(r.Context(), sessionID); ok && (command.Action == "set" || command.Action == "resume") {
			if err := api.projectNativeGoal(sessionID, *previous, "intent_"+command.Action); err != nil {
				http.Error(w, "Could not save goal intent", http.StatusServiceUnavailable)
				return
			}
		} else if !req.IsAutoNotification && turnProvider == "codex-cli" && previous.Provider == "codex-cli" && previous.Goal != nil {
			if err := api.projectNativeGoal(sessionID, *previous, "intent_message"); err != nil {
				http.Error(w, "Could not save goal intent", http.StatusServiceUnavailable)
				return
			}
		}
	}

	// This request starts a new turn: its user_message comes from the agent
	// library, so tell the event store which client id that echo carries.
	// Only once this request holds the input lane: two sends held by one
	// compaction used to register here together, the later overwrote the
	// earlier, and the loser's bubble never cleared (#3795).
	if clientMessageID := clientMessageIDFromContext(r.Context()); clientMessageID != "" && api.eventStore != nil {
		api.eventStore.ExpectClientUserMessage(sessionID, clientMessageID, req.Query)
	}

	// Chat sessions are in-memory only — tracked via activeSessions map
	// below. No persistent session metadata.

	if idleRun == nil && !req.IsAutoNotification && !strings.EqualFold(strings.TrimSpace(req.TriggeredBy), "cron") {
		api.clearSessionStopped(sessionID)
		delivered, _ := r.Context().Value(deliveredAgentMessageKey{}).(map[string]interface{})
		combinedPerson := delivered["source"] == "queued_batch" && delivered["combined_person_input"] == true
		if combinedPerson || (idleCompactPersonTurn(r, sessionID, req) && req.AgentMessageInbox == "" && len(delivered) == 0) {
			api.cancelNativeGoalRestore(sessionID, "person_message_admitted")
			api.cancelUsageLimitRecovery(sessionID, "person_message_admitted")
			backgroundFollowUps.personWrote(sessionID)
		}
		// Lift the MCP registry zombie-prevention flag only for that intentional
		// user resume, otherwise internal recovery can resurrect stopped bridges.
		mcpagent.ClearHTTPSessionStopped(sessionID)
	}

	// Track active session for page refresh recovery (no observer needed)
	api.trackActiveSession(sessionID, req.AgentMode, req.Query, currentUserID, req.BotPlatform, req.TriggeredBy, req.SessionTitle, req.ParentSessionID, req.SessionKind)
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
		configuredRequestBrowserMode == "extension" || configuredRequestBrowserMode == "auto" || configuredRequestBrowserMode == "headless" || configuredRequestBrowserMode == "cdp"
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
	events.TurnSpeedStep(sessionID, "setup")
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
		// The chat's own options (its reasoning effort) stay when the locked answer is the model it chose: rebuilding
		// without them dropped the effort picked in the chat, so Claude never got --effort (5 Nov).
		var keptOptions map[string]interface{}
		if req.LLMConfig != nil && strings.EqualFold(req.LLMConfig.Primary.Provider, finalProvider) && req.LLMConfig.Primary.ModelID == finalModelID {
			keptOptions = maps.Clone(req.LLMConfig.Primary.Options)
		}
		req.LLMConfig = &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: finalProvider, ModelID: finalModelID, ConnectionID: connectionID, Options: keptOptions}}
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

	// Refuse ambient server login where a product requires scoped authentication.
	// A selected account already admitted above may supply its own token or
	// private CLI login. Do not layer its environment into shared/default keys.
	if authErr := claudeCodeQueryAuthenticationError(resolvedProfile, finalProvider, admittedQueryAccount); authErr != nil {
		http.Error(w, authErr.Error(), http.StatusBadRequest)
		return
	}

	// Session config isn't persisted anymore — follow-up messages rely on the
	// frontend to pass the provider/model on every request.

	// Run saved Python workflows directly for webhooks, timers and run tools.
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
		allTools, allExecutors, toolCategories := createCustomTools(true, currentUserID, sessionID, req.SelectedFolder) // Workflow mode: session-aware

		bindWorkflowStepIdentity(r.Context(), sessionID, allExecutors, toolCategories)

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
			var messagingErr error
			allTools, messagingErr = api.appendWorkflowMessagingTools(allTools, allExecutors, toolCategories, currentUserID, sessionID, manifestWorkspacePath, req)
			if messagingErr != nil {
				http.Error(w, "Workflow messaging tools are unavailable", http.StatusInternalServerError)
				return
			}
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

				if runtime := caps.RuntimeServers(); len(runtime) > 0 {
					selectedServers = runtime
					serverList = strings.Join(selectedServers, ",")
				}

				// Global secrets from manifest — if explicit selection, use it; otherwise leave nil (= none)
				if caps.SelectedGlobalSecretNames != nil {
					req.SelectedGlobalSecrets = caps.SelectedGlobalSecretNames
				}
				// Checked against the owner who lends them, as the turn resolves them.
				secretsOwner := api.turnSecretsOwner(sessionID, currentUserID, manifestWorkspacePath)
				req.secretsWorkspacePath = manifestWorkspacePath
				// A bot session already carries its arrival connection, which
				// wins over the manifest selection for that conversation.
				if strings.TrimSpace(req.BotConnectionID) == "" {
					req.BotConnectionID = strings.TrimSpace(caps.SlackConnectionID)
				}
				if err := validateWorkspaceSecretSelection(r.Context(), secretsOwner, manifestWorkspacePath, req.SelectedGlobalSecrets); err != nil {
					logQueryAdmissionRefusal(w, r, "manifest_secret_selection")
					http.Error(w, err.Error(), http.StatusForbidden)
					return
				}

				// Browser mode from manifest
				if caps.BrowserMode != "" && caps.BrowserMode != "none" && req.BrowserMode == "" {
					req.BrowserMode = caps.BrowserMode
				}
			}
		}

		if !manifestLoaded && req.PresetQueryID != "" {
			// Manifest-only mode: workflow.json is the source of truth for workflow config.
			// If no manifest was found, log a warning. The workflow will run with request defaults only.
			log.Printf("[MANIFEST] WARNING: No workflow.json found for preset %s - workflow will run with request defaults only. Run migration: POST /api/workflows/migrate", req.PresetQueryID)
		}

		allTools = restrictWorkflowNotificationTools(allTools, allExecutors, toolCategories, workflowNotificationsForPath(manifestWorkspacePath))

		// --- Post-load processing: browser configuration ---
		// Runs after either manifest or preset loading has populated the config variables.

		// Resolve effective browser mode.
		workflowBrowserMode := req.BrowserMode
		// Store resolved browser mode on session for context-aware shell blocking
		if workflowBrowserMode != "" {
			common.SetSessionBrowserMode(sessionID, workflowBrowserMode)
		}
		if workflowBrowserMode == "extension" || workflowBrowserMode == "auto" || workflowBrowserMode == "headless" || workflowBrowserMode == "cdp" {
			wfCdpPorts := getCdpPorts(req)
			if workflowBrowserMode == "auto" {
				wfCdpPorts = configuredCDPPortsForMode(workflowBrowserMode, req.CdpPort, req.resolvedCDPPorts)
			}

			browserCategory := virtualtools.GetWorkspaceBrowserToolCategory()
			browserTools := virtualtools.CreateWorkspaceBrowserTools()
			browserRuntime := browser.NewBrowserRuntimeConfig(workflowBrowserMode, wfCdpPorts)
			browserExecutors := virtualtools.CreateWorkspaceBrowserToolExecutorsWithRuntime(sessionID, browserRuntime)
			browserExecutors = api.bindWorkflowBrowserExecutors(r.Context(), sessionID, req, currentUserIsReadOnly, browserExecutors)

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
		if workflowCLISecurityPolicy != nil {
			// PLAT-442: a Goal never runs as a person's slot: declared, not left to the folder rule.
			workflowRoot := codingAgentWorkspaceWorkingDir(manifestWorkspacePath)
			workflowCLISecurityPolicy.RunAs = llmtypes.RunAs{Declared: true, Root: workflowRoot}
			llmtypes.DeclareRunAs(workflowRoot, workflowCLISecurityPolicy.RunAs)
		}
		workflowOrchestrator.SetCLISecurityPolicy(workflowCLISecurityPolicy)

		// Set selected skills on the orchestrator
		if len(selectedSkills) > 0 {
			workflowOrchestrator.SetSelectedSkills(selectedSkills)
			log.Printf("[SKILLS] Applied %d skills to workflow orchestrator: %v", len(selectedSkills), selectedSkills)
		}

		// The selected Vault secrets, lent by the project's owner, set on the orchestrator
		allSecrets := api.mergeGlobalSecretsFor(context.Background(), api.turnSecretsOwner(sessionID, currentUserID, req.SelectedFolder), req.SelectedGlobalSecrets)
		if len(allSecrets) > 0 {
			entries := make([]orchestrator.SecretEntry, len(allSecrets))
			for i, s := range allSecrets {
				entries[i] = orchestrator.SecretEntry{Name: s.Name, Value: s.Value}
			}
			workflowOrchestrator.SetSecrets(entries)
			log.Printf("[SECRETS] Applied %d secrets to workflow orchestrator", len(entries))
		}

		// Replace workspace advanced executors with session-aware versions that include secrets.
		// This must happen AFTER secrets are merged so secrets are available as shell env vars.
		// createCustomTools uses the no-session CreateWorkspaceAdvancedToolExecutors(),
		// which means MCP_SESSION_ID won't be set and secrets won't be in the shell env.
		secretEnvVars := make(map[string]string, len(allSecrets))
		for _, s := range allSecrets {
			secretEnvVars["SECRET_"+s.Name] = s.Value
		}
		virtualtools.ReplaceSessionShellSecrets(sessionID, secretEnvVars)
		sessionAwareExecutors, workspaceEnv := virtualtools.CreateWorkspaceAdvancedToolExecutorsWithSessionAndEnv(currentUserID, sessionID, secretEnvVars)
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

		// PLAT-746-265: whole runs bypass the workshop chat that normally
		// registers each step's durable inspection owner. Reuse its recorder
		// for Crew children; the run itself owns the saved-chat notification.
		if child, bindingErr := findCrewExecutable(manifestWorkspacePath); bindingErr == nil && child != nil {
			// A preset-only request has no selected_folder at initial session
			// tracking. Durable logs/transcripts resolve their root from the
			// session, so bind the server-resolved child before any step starts.
			api.activeSessionsMux.Lock()
			mismatchedWorkspace := false
			if session := api.activeSessions[sessionID]; session != nil {
				mismatchedWorkspace = session.WorkspacePath != "" && crewExecutableWorkspace(session.WorkspacePath) != crewExecutableWorkspace(manifestWorkspacePath)
				if !mismatchedWorkspace {
					session.WorkspacePath = manifestWorkspacePath
				}
			}
			api.activeSessionsMux.Unlock()
			if mismatchedWorkspace {
				log.Printf("[CREW_EXECUTABLE] refused mismatched session workspace session=%s target=%s", sessionID, manifestWorkspacePath)
				http.Error(w, "Open this workflow's own chat to run it.", http.StatusConflict)
				return
			}
			// A script-only run has no model chat to persist its parent identity.
			// Save the actual request through the normal conversation store so
			// existing inspection ownership checks also work after restart.
			api.persistChatConversationToPathWithTerminalSession(sessionID, "", "workflow", currentUserID,
				[]llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: req.Query}}}},
				&ChatHistoryAgentRuntime{Kind: "workflow", WorkspacePath: manifestWorkspacePath, WorkshopMode: "run"}, nil,
				stableBuilderConversationLogPath(r.Context(), manifestWorkspacePath, currentUserID, sessionID))
			workflowOrchestrator.SetWorkshopExecutionNotifier(&workshopExecutionBgNotifier{
				api: api, sessionID: sessionID, workspacePath: manifestWorkspacePath,
				presetQueryID: req.PresetQueryID, userID: currentUserID,
				suppressAutoNotification: true,
			})
			log.Printf("[CREW_EXECUTABLE] inspection recording enabled workspace=%s session=%s", manifestWorkspacePath, sessionID)
		}

		// A server-owned run (scheduled, webhook, Slack: "schedule-…") registered its tools only in the global table, so a
		// scripted step calling a tool through its bridge session (a registered child of this run) got whichever session
		// wrote that table last; a Crew chat running at the same moment handed it its own executor and the ownership check
		// rejected the step (PLAT-514). Registering the run's tools under the run's session lets the step's lookup resolve
		// to its parent run. Only run sessions: a chat's session keeps its own registry, and CleanupSession below would
		// clear it.
		runToolSession := strings.HasPrefix(sessionID, "schedule-")
		if runToolSession {
			codeexec.InitRegistryForSession(sessionID, runToolExecutors(allExecutors), nil)
		}

		if workflowBrowserMode != "" && workflowBrowserMode != "none" {
			workflowOrchestrator.SetBrowserMode(workflowBrowserMode)
			log.Printf("[WORKFLOW] Set browser mode on orchestrator: %s", workflowBrowserMode)
		}

		// Propagate authorized CDP profiles for browser prompts and execution agents.
		orchestratorCDPPorts := getCdpPorts(req)
		if workflowBrowserMode == "auto" {
			orchestratorCDPPorts = configuredCDPPortsForMode(workflowBrowserMode, req.CdpPort, req.resolvedCDPPorts)
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
		if skill, ok := llmtypes.NativeSkillForSession(r.Context(), sessionID); ok {
			workflowBaseCtx = llmtypes.WithNativeSkill(workflowBaseCtx, sessionID, skill)
		}
		workflowBaseCtx = llmtypes.WithNativeImageInputs(workflowBaseCtx, sessionID, llmtypes.NativeImageInputsForSession(r.Context(), sessionID))
		if goal, ok := llmtypes.NativeGoalCommandForSession(r.Context(), sessionID); ok {
			workflowBaseCtx = llmtypes.WithNativeGoalCommand(workflowBaseCtx, goal)
		}
		if llmtypes.NativePlanModeForSession(r.Context(), sessionID) {
			workflowBaseCtx = llmtypes.WithNativePlanMode(workflowBaseCtx, sessionID)
		}
		if pause, ok := llmtypes.NativeGoalTurnPauseForSession(r.Context(), sessionID); ok {
			workflowBaseCtx = llmtypes.WithNativeGoalTurnPause(workflowBaseCtx, pause)
		}

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

		// Clone request with a non-canceled context before writing the response so
		// detached workflow work never inherits a canceled HTTP request context.
		workflowReq := r.Clone(context.WithoutCancel(r.Context()))

		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}

		// Execute workflow asynchronously
		go func(r *http.Request) {
			defer recoverDetachedGoroutine("workflow_query", sessionID)
			if directWebhookOptions != nil && directWebhookOptions.PythonResume != nil && directWebhookOptions.PythonResume.Release != nil {
				defer directWebhookOptions.PythonResume.Release()
			}
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
				api.workflowStepIDMux.Lock()
				if api.workflowStepIDs != nil {
					if storedStepID, exists := api.workflowStepIDs[req.PresetQueryID]; exists {
						stepID = storedStepID
						log.Printf("[WORKFLOW CHECK] Found step_id for preset: %s", stepID)
						// Clear it after retrieval (one-time use)
						delete(api.workflowStepIDs, req.PresetQueryID)
					}
				}
				api.workflowStepIDMux.Unlock()
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

			workflowCost := api.costTurnAttribution(r.Context(), req, currentUserID, workflowWorkspacePath, costobserver.ScopeWorkflowExecution)
			if costobserver.AttributionFromContext(r.Context()).Activity == "" {
				workflowCost.Activity = "workflow_run"
			}
			workflowCtx = costobserver.ContextWithAttribution(workflowCtx, workflowCost)
			workflowCtx = costSourceContext(workflowCtx, req)
			api.setSessionCostAttribution(sessionID, workflowCost)
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
				} else if req.ExecutionOptions.ResumeFromStep <= 0 {
					req.ExecutionOptions.SelectedRunFolder = ""
				}

				log.Printf("[EXECUTION_OPTIONS_DEBUG] [Backend] Execution input count: %d", len(req.ExecutionOptions.Inputs))
				log.Printf("[WORKFLOW EXECUTION] Frontend execution options provided: strategy=%s, run_folder=%s, resume_from_step=%d, enabled_group_names=%v, save_validation_responses=%v",
					req.ExecutionOptions.ExecutionStrategy, req.ExecutionOptions.SelectedRunFolder, req.ExecutionOptions.ResumeFromStep, req.ExecutionOptions.EnabledGroupNames, req.ExecutionOptions.SaveValidationResponses)

				// Convert to controller ExecutionOptions and pass to workflow orchestrator
				controllerOpts := &todo_creation_human.ExecutionOptions{
					SelectedRunFolder:    req.ExecutionOptions.SelectedRunFolder,
					ExecutionStrategy:    req.ExecutionOptions.ExecutionStrategy,
					ResumeFromStep:       req.ExecutionOptions.ResumeFromStep,
					AllowRunContinuation: !currentUserIsReadOnly && (access == WorkflowAccessOwner || access == WorkflowAccessWrite),
					PlanChangeAction:     req.ExecutionOptions.PlanChangeAction,
					InputPreset:          req.ExecutionOptions.InputPreset,
					Inputs:               req.ExecutionOptions.Inputs,
					EnabledGroupNames:    req.ExecutionOptions.EnabledGroupNames,
					RouteSelections:      req.ExecutionOptions.RouteSelections,
				}

				// Internal webhook bindings cannot be supplied by public request JSON.
				if directWebhookOptions != nil {
					controllerOpts = directWebhookOptions
				}
				// Resolve saved Crew agent settings and scripts through the run owner.
				controllerOpts.CrewResources = crewResourcesForRun(r.Context(), api.projectActions)
				controllerOpts.AgentFunctionTools = api.workflowAgentFunctionToolsForRun(r.Context())
				log.Printf("[WORKFLOW EXECUTION] Setting strategy=%s folder=%s", controllerOpts.ExecutionStrategy, controllerOpts.SelectedRunFolder)
				workflowOrchestrator.SetExecutionOptions(controllerOpts)
				log.Printf("[EXECUTION_OPTIONS_DEBUG] [Backend] Execution options set on orchestrator successfully")
			} else {
				log.Printf("[EXECUTION_OPTIONS_DEBUG] [Backend] No execution options provided in request - req.ExecutionOptions is nil")
				// Saved Crew resources are needed on every authenticated run path.
				if runner := crewResourcesForRun(r.Context(), api.projectActions); runner != nil {
					workflowOrchestrator.SetExecutionOptions(&todo_creation_human.ExecutionOptions{
						CrewResources:      runner,
						AgentFunctionTools: api.workflowAgentFunctionToolsForRun(r.Context()),
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
				if claims := GetUserFromContext(r.Context()); claims != nil && api.projectActions != nil {
					common.GrantSessionCrewAttachmentReads(sessionID, crewAttachmentReadRoots(workflowCtx, api.projectActions, claims.UserID, workflowWorkspacePath))
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
				// Check attached Crew access before any step can read an alias.
				err = preflightCrewAttachments(workflowCtx, api.projectActions, claims.UserID, workflowWorkspacePath)
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
				status := workflowQueryFailureStatus(err)
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
				if runToolSession {
					codeexec.CleanupSession(sessionID)
				}
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
				if runToolSession {
					codeexec.CleanupSession(sessionID)
				}
				// Kill headless browser processes for this session
				api.cleanupBrowserSessions(sessionID)
			}
		}(workflowReq)
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
	if idleRun == nil {
		if err := api.saveRestartContinueRequest(r.Context(), currentUserID, sessionID, queryID, queryRequestWithEffectiveRuntime(req, finalProvider, finalModelID)); err != nil {
			log.Printf("[RESTART_CONTINUE] session=%s turn=%s action=skip reason=config_write_failed", sessionID, queryID)
		}
	}
	api.trackConversationTurnStart(queryID, sessionID, req)
	if idleRun == nil {
		// An idle compaction changes no files; capturing them would show a
		// reply-files card for a turn nobody sent.
		endSpeedSpan5455 := events.TurnSpeedSpan(sessionID, "snapshot_start")
		api.startCrewTurnSnapshot(queryID, sessionID, req)
		endSpeedSpan5455()
	}

	// Return immediate response with query ID
	response := QueryResponse{
		QueryID:        queryID,
		SessionID:      sessionID, // Include the actual session ID used for conversation history
		Status:         "started",
		Message:        "Query processing started. Use polling API to get real-time updates.",
		OmittedContext: req.omittedWorkflowContextCount,
	}

	// Clone request with a non-canceled context before writing the response so
	// detached chat work never sees a canceled HTTP request context.
	chatReq := r.Clone(context.WithoutCancel(r.Context()))

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
	continuationReq := req
	continuationReq = queryRequestWithEffectiveRuntime(continuationReq, finalProvider, finalModelID)
	if idleRun == nil {
		api.rememberLastQueryRequest(sessionID, continuationReq)
		if idleCompactPersonTurn(r, sessionID, req) {
			idleCompacts.notePersonTurn(sessionID, currentUserID, continuationReq, time.Now())
		}
	}

	// Set user-facing busy state for regular chat turns.
	{
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
	if resolvedProfile != nil && resolvedProfile.APIKeys != nil {
		// resolveAgentProfileForQuery resolved a project-scoped credential for a
		// product surface from the encrypted per-workspace
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
		// folder must not inherit that workflow's credential.
		mergedAPIKeys = resolvedProfile.APIKeys
	}

	resolverScope := queryProviderAccountScope(req, currentUserID, resolvedProfile)
	resolverScope.TokenOwner = botTokenOwner
	if accountScope.Principal != resolverScope.Principal && botTokenOwner != "" && accountScope.Principal == botTokenOwner {
		resolverScope.Principal = botTokenOwner
	}
	mergedAPIKeys = api.withConnectionResolver(mergedAPIKeys, resolverScope)
	queryInputLaneRelease := releaseInputLane
	if queryInputLaneRelease != nil {
		// Ownership moves to the background turn for its full lifetime. Normal
		// follow-ups bypass this lane through confirmed provider live input; only a
		// true next turn waits here, preventing overlapping cleanup/state writes.
		releaseInputLane = nil
	}
	clearDeliveredSender := func() {}

	// Process the query in the background
	go func(r *http.Request) {
		defer recoverDetachedGoroutine("chat_query", sessionID)
		// The company-server Vault answers this message needs, asked once and early (#3723).
		api.prefetchServerVaultInventory(sessionID, currentUserID, req.SelectedFolder)
		serverVaultRound := withServerVaultSecretRound(context.Background())
		turnStatus := trackedExecutionStatusCanceled
		turnError := "conversation turn ended before completion was recorded"
		var agentCancel context.CancelFunc
		var idleTurnRuntime *ChatHistoryAgentRuntime
		defer func() {
			if idleRun != nil {
				// A compaction turn that ended before its compaction step.
				idleRun.finish(llmtypes.IdleCompactResult{Reason: llmtypes.IdleCompactReasonFailed}, errors.New(turnError))
			} else {
				idleCompacts.noteTurnEnd(sessionID, idleTurnRuntime, time.Now())
				goSafe("compact_early_after_turn", func() { api.compactEarlyAfterTurn(sessionID, currentUserID) })
			}
			if agentCancel != nil {
				agentCancel()
				api.agentCancelMux.Lock()
				delete(api.agentCancelFuncs, sessionID)
				api.agentCancelMux.Unlock()
			}
			// Publish completion before releasing the turn lane. A queued synthetic
			// turn can then acquire the lane and set running=true without an older
			// cleanup racing afterward and clearing its state.
			{
				api.setSyntheticTurn(sessionID, false)
				api.setSessionBusy(sessionID, false)
			}
			// Live input the CLI accepted during this turn can still be
			// running as a retained turn (see observeRetainedMainTurnEvent).
			// Its own completion settles it; do not show the chat idle now.
			// A native SDK input steered into this turn was answered by it: once
			// the Session is idle nothing else will settle it, so free the chat now.
			if turnStatus == trackedExecutionStatusCompleted && api.retainedMainTurnIsNative(sessionID) {
				api.releaseStaleRetainedMainTurn(sessionID, "foreground_turn_answered_native_input")
			}
			if turnStatus == trackedExecutionStatusCompleted && api.retainedMainTurnTracked(sessionID) {
				log.Printf("[RETAINED_TURN] Foreground turn ended with live input still being answered; keeping session=%s running", sessionID)
				api.setSessionBusy(sessionID, true)
				api.updateSessionStatus(sessionID, "running")
			}
			api.observeRuntimeSnapshot(sessionID)
			if !api.retainedMainTurnTracked(sessionID) {
				api.bindCrewSnapshotCompletion(queryID, 0)
			}
			clearDeliveredSender()
			if queryInputLaneRelease != nil {
				queryInputLaneRelease()
			}
			{
				// Drain only after releasing the lane; synthetic turns acquire it.
				api.drainPendingAutoNotificationsAfterTurn(sessionID)
			}
			api.completeTrackedExecution(queryID, turnStatus, turnError, nil)
		}()

		// Helper function to send error and continue (not terminate)
		sendError := func(errorMsg string, shouldTerminate bool) {
			if shouldTerminate && idleRun != nil {
				// A compaction turn fails silently: the log says why, the chat shows nothing.
				turnStatus, turnError = trackedExecutionStatusFailed, errorMsg
				return
			}
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
		streamCtx, cancel := context.WithCancel(productpolicy.WithSelection(context.WithoutCancel(r.Context()), builderProductSelection(GetUserFromContext(r.Context()))))
		checkTokenTurn, err := api.nativeTokenTurnCheck(streamCtx, sessionID, req)
		if err != nil {
			cancel()
			sendError(err.Error(), true)
			return
		}
		agentModeForScope := req.AgentMode

		costScope := chatTurnCostScope(req, agentModeForScope, "")
		turnCostAttribution := api.costTurnAttribution(r.Context(), req, currentUserID,
			req.SelectedFolder, costScope)
		api.setSessionCostAttribution(sessionID, turnCostAttribution)
		streamCtx = queryLogCtx.Context(streamCtx)
		streamCtx = costobserver.ContextWithAttribution(streamCtx, turnCostAttribution)
		streamCtx = costSourceContext(streamCtx, req)
		// Tools that start their own model (generate_text_llm)
		// resolve and admit accounts with this turn's scope.
		streamCtx = virtualtools.WithProviderAccountKeys(streamCtx, mergedAPIKeys)
		defer cancel()

		// All following turn reads belong to the execution, not the returned HTTP request.
		r = r.WithContext(streamCtx)

		// Resolve once for this turn, using its execution context rather than
		// the HTTP request that has already returned from this background job.
		// CLI sandbox, tool guards and instructions must use identical grants.
		var turnFolderGrants []workflowtypes.WorkflowFolderGrant
		var turnFolderNotice string
		if resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID) {
			endSpeedSpan5685 := events.TurnSpeedSpan(sessionID, "folder_grants")
			turnFolderGrants, turnFolderNotice, err = workFolderGrantsForTurn(streamCtx, GetUserFromContext(streamCtx), req.SelectedFolder)
			endSpeedSpan5685()
			if err != nil {
				logfWithContext(queryLogCtx, "[WORK FOLDER GUARD] event=resolve_failed session=%s reason=grant_store_unavailable", sessionID)
				sendError("Could not load your attached folders. Please retry this turn.", true)
				return
			}
			// The chat's mode (#3885): Auto-review hands approvals to the coding app's own reviewer. What the turn
			// reaches is the project's access above, never the mode (owner, 26 Nov). Only the chat owner's own turns
			// on their own Mac read it.
			if cliPersonalMac() && !currentUserIsReadOnly && !crewAskOnly && req.SharedAgentSender == "" {
				saved := chatAccessSavedMode(streamCtx, currentUserID, sessionID)
				accessMode := chatAccessEffective(saved, finalProvider, true)
				if accessMode == chatAccessAutoReview {
					streamCtx = llmtypes.WithCodingAgentAutoReview(streamCtx, sessionID)
				}
				if saved != "" {
					logfWithContext(queryLogCtx, "[CHAT_ACCESS] session=%s provider=%s saved=%q effective=%q", sessionID, finalProvider, saved, accessMode)
				}
			}
			logfWithContext(queryLogCtx, "[WORK FOLDER GUARD] event=resolved session=%s grants=%d", sessionID, len(turnFolderGrants))
			// Scratch belongs to this admitted turn, never to the person's attachments.
			common.SetSessionBrowserScratch(sessionID, "")
			// Only on a person's own Mac: a server accepts write paths inside its workspace only, so a /tmp folder
			// there made every extension browser command fail ("write path must be within the workspace boundary").
			if !productpolicy.Local() {
				// no scratch folder on a server
			} else if scratch, scratchErr := os.MkdirTemp("", "agentworks-browser-session-"); scratchErr == nil {
				common.SetSessionBrowserScratch(sessionID, scratch)
				defer os.RemoveAll(scratch)
			} else {
				logfWithContext(queryLogCtx, "[WORK FOLDER GUARD] event=scratch_unavailable session=%s", sessionID)
			}
		}

		if turnFolderNotice != "" {
			api.eventStore.AddEvent(sessionID, events.Event{
				ID:   fmt.Sprintf("work_folder_notice_%s", queryID),
				Type: "work_folder_notice", Timestamp: time.Now(), SessionID: sessionID,
				Data: unifiedevents.NewAgentEvent(&unifiedevents.GenericEventData{
					BaseEventData: unifiedevents.BaseEventData{Timestamp: time.Now()},
					Data:          map[string]interface{}{"message": turnFolderNotice},
				}),
			})
		}

		// Load selected tools and code execution mode from preset if available (for simple/ReAct agents)
		var selectedTools []string

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

		var resolvedPrimaryOptions map[string]interface{}
		if req.LLMConfig != nil {
			resolvedPrimaryOptions = req.LLMConfig.Primary.Options
		}

		if !isPublishedLLMProviderAllowed(finalProvider) {
			sendError(fmt.Sprintf("Selected provider %q is not supported; select an available provider", finalProvider), true)
			return
		}

		profileAgentToolsMode := ""
		profileApprovalsMode := ""
		// Native agent tools request Full CLI; applyCLILandlock below decides
		// whether it is confined, unconfined (a person's own Mac) or falls back
		// to mcp_only. mcpagent's bridge routing block stays in every mode.
		var profileBridgeRoutingInstructions *string
		if resolvedProfile != nil {
			profileAgentToolsMode = resolvedProfile.Definition.Runtime.AgentTools.Mode
			profileApprovalsMode = resolvedProfile.Definition.Runtime.Approvals.Mode
		} else if workflowNativeAgentTools {
			profileAgentToolsMode = "full"
		}
		if crewAskOnly {
			profileAgentToolsMode = "mcp_only"
		}
		allowPersistentInteractive := codingAgentRequestAllowsPersistentInteractive(&req)
		forceStructuredCodingAgent := codingAgentUsesStructuredTransportForChat(finalProvider, allowPersistentInteractive)
		claudeCodePersistentInteractive, codexPersistentInteractive, cursorPersistentInteractive, piPersistentInteractive, musePersistentInteractive, agyPersistentInteractive := codingAgentPersistentInteractiveFlags(finalProvider, allowPersistentInteractive, forceStructuredCodingAgent)
		claudeCodeTransport := codingAgentClaudeCodeChatTransport(finalProvider, chatClaudeCodeTransport(streamCtx, currentUserID, sessionID, req.ClaudeCodeTransport))
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
		if finalProvider == "codex-cli" {
			kind := "other"
			switch req.SessionKind {
			case "":
				kind = "chat"
			case "generic_agent":
				kind = "generic_agent"
			}
			log.Printf("[CODEX_CHAT_TRANSPORT] session=%s kind=%s has_parent=%t allows_persistent=%t structured=%t persistent=%t", sessionID, kind, req.ParentSessionID != "", allowPersistentInteractive, forceStructuredCodingAgent, codexPersistentInteractive)
		}
		chatWorkingFolder := perUserChatsFolder
		if resolvedProfile != nil {
			chatWorkingFolder = agentProfileRuntimeWorkspace(currentUserID, req.SelectedFolder)
		}

		workspace.SetSessionWorkingDir(sessionID, crewFilesPath(chatWorkingFolder))
		turnProfileID := ""
		if resolvedProfile != nil {
			turnProfileID = resolvedProfile.Definition.ID
		}
		cliWorkspaceFolder := chatWorkingFolder
		if source := publicationSourceForProfile(resolvedProfile); source != "" {
			cliWorkspaceFolder = source
		}
		chatWorkingDir, sharedChatWorkingDir, isolationErr := turnCLIWorkingDir(turnProfileID, false, cliWorkspaceFolder, currentUserID, sessionID, finalProvider, workflowCLIMode(&req, currentUserIsReadOnly), currentUserIsReadOnly, resolvedProfile != nil && (resolvedProfile.IsolateCodeCLI || resolvedProfile.Publication != nil))
		if isolationErr != nil {
			sendError(isolationErr.Error(), true)
			return
		}
		if resolvedProfile != nil && resolvedProfile.Definition.ID == caplayerproduct.ProfileID {
			if err := ensureVaultChatWorkspace(streamCtx, currentUserID); err != nil {
				sendError(err.Error(), true)
				return
			}
			var isolationErr error
			chatWorkingDir, isolationErr = linkedProjectCLIWorkingDir(chatWorkingFolder, currentUserID, sessionID, finalProvider, "vault")
			if isolationErr != nil {
				sendError(isolationErr.Error(), true)
				return
			}
		}
		cliReadPaths := []string{sharedChatWorkingDir}
		cliWritePaths := []string{sharedChatWorkingDir}
		chatRuntimeDir := ""
		if turnProfileID == crewProfileID {
			chatRuntimeDir, err = crewPrivateRuntimeDir(cliWorkspaceFolder, currentUserID, sessionID, finalProvider, currentUserIsReadOnly)
			if err != nil {
				sendError(err.Error(), true)
				return
			}
		}
		crewReaderCLI := currentUserIsReadOnly && resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID)
		if currentUserIsReadOnly && crewReaderCLI {
			cliWritePaths = nil
		}
		if resolvedProfile != nil && resolvedProfile.Publication != nil {
			p := resolvedProfile.Publication
			cliReadPaths = append(cliReadPaths, p.Root)
			if !crewReaderCLI {
				cliWritePaths = publishedRuntimeRoots(p.runtimeRoot())
			}
		}

		if chatWorkingDir != sharedChatWorkingDir {
			cliReadPaths = append(cliReadPaths, chatWorkingDir)
			cliWritePaths = append(cliWritePaths, chatWorkingDir)
		}
		if chatRuntimeDir != "" {
			cliReadPaths = append(cliReadPaths, chatRuntimeDir)
			cliWritePaths = append(cliWritePaths, chatRuntimeDir)
		}
		if resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID) {
			grantRead, grantWrite, _, _ := workproduct.ResolveGrants(turnFolderGrants)
			cliReadPaths = appendUniqueStrings(cliReadPaths, grantRead...)
			if !crewReaderCLI {
				cliWritePaths = appendUniqueStrings(cliWritePaths, grantWrite...)
			}
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
		// PLAT-442: the platform names the account this turn's CLI runs as (run_as.go) and declares it to the
		// provider for the CLI's folder; the launch policy carries it too.
		turnRunAs, runAsErr := declareTurnRunAs(r.Context(), turnRunAsInput{
			ProfileID:        turnProfileID,
			ConversationKey:  req.AgentProfileConversationKey,
			WorkingFolder:    chatWorkingFolder,
			CLIWorkingDir:    chatWorkingDir,
			SharedWorkingDir: sharedChatWorkingDir,
			CallerID:         currentUserID,
			SessionID:        sessionID,
			ConnectionID:     req.ConnectionID,
		})
		if runAsErr != nil {
			sendError(runAsErr.Error(), true)
			return
		}
		if cliSecurityPolicy != nil {
			cliSecurityPolicy.RunAs = turnRunAs
		}
		if piPersistentInteractive {
			// Another conversation's Pi turn in this folder finishes first:
			// Pi runs one session per folder, and closing it would cancel
			// that turn (issue #213, C7).
			stuck := api.waitForBusyPiCLISessions(r.Context(), sessionID, chatWorkingDir)
			closed := api.cleanupConflictingPiCLIInteractiveSessions(sessionID, chatWorkingDir, "starting chat agent", stuck)
			if closed > 0 {
				log.Printf("[PI_CLI_CONFLICT] Cleared %d conflicting Pi CLI session(s) before starting chat session %s in %s", closed, sessionID, chatWorkingDir)
			}
		}
		recordSessionSecretScope(sessionID, req.secretsWorkspacePath)
		common.ConfigureProcessBridgeCredential(sessionID, finalProvider == "codex-cli" && codexPersistentInteractive && req.ParentSessionID == "")
		if finalProvider == "codex-cli" && codexPersistentInteractive && req.ParentSessionID == "" {
			pendingToken := common.BridgeTokenForSession(sessionID)
			defer common.DiscardPendingProcessBridgeCredential(sessionID, pendingToken)
		}
		codingAgentSecretEnvironment := make(map[string]string)
		for _, name := range functionRunEnvironmentKeys {
			if value := common.GetSessionShellEnv(sessionID)[name]; value != "" {
				codingAgentSecretEnvironment[name] = value
			}
		}
		if resolvedProfile != nil {
			for name, value := range publicationShellEnvironment(resolvedProfile.Publication) {
				codingAgentSecretEnvironment[name] = value
			}
		}
		endSpeedSpan5926 := events.TurnSpeedSpan(sessionID, "secrets")
		codingAgentSecrets := api.mergeGlobalSecretsFor(context.Background(), api.turnSecretsOwner(sessionID, currentUserID, req.SelectedFolder), req.SelectedGlobalSecrets)
		endSpeedSpan5926()
		// On the Mac, a selected secret its Vault lacks comes from the lender's Vault on the Crew's company server.
		endSpeedSpan5928 := events.TurnSpeedSpan(sessionID, "server_secrets")
		codingAgentSecrets = api.addServerVaultSecrets(serverVaultRound, currentUserID, req.SelectedFolder, codingAgentSecrets, req.SelectedGlobalSecrets)
		endSpeedSpan5928()
		for _, secret := range codingAgentSecrets {
			codingAgentSecretEnvironment["SECRET_"+secret.Name] = secret.Value
		}
		// A shared agent's turn for someone else keeps them (owner, 4 Nov: "whatever the main agent has access to the
		// other users will have access if i share it").
		if crewAskOnly {
			// A guest's free-text ask gets no secrets (856-59).
			for name := range codingAgentSecretEnvironment {
				if strings.HasPrefix(name, "SECRET_") {
					delete(codingAgentSecretEnvironment, name)
				}
			}
		}
		// `mcpbridge secret NAME` resolves this same selection again when a command needs a value (session_value_read.go).
		secretSource := sessionSecretSource{User: currentUserID, Folder: req.SelectedFolder, WorkspacePath: req.secretsWorkspacePath,
			RunMode: crewAskOnly, Disabled: secretsTurnDisabled(resolvedProfile), Globals: req.SelectedGlobalSecrets}
		if resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID) {
			secretSource.CrewProfile, secretSource.ProfileID = true, resolvedProfile.Definition.ID
		}
		recordSessionSecretSource(sessionID, secretSource)
		// A connected GitHub account: git and gh in the CLI's own shell act as that person (PLAT-746-158 B8).
		gitcred.AddGitHub(codingAgentSecretEnvironment)
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

		toolGate.DenyWhere(func(name string) bool {
			return !builderProductSelection(GetUserFromContext(r.Context())).AllowsTool(name)
		})
		// Full CLI (owner, 5 Nov): the CLI's own Read, Edit, Write, Grep, Glob and Bash do these jobs, so it sees one
		// way to do each. Its native access is the same folder grants (Landlock reads and writes); running on the
		// person's laptop and the actions only AgentWorks has (copy, link, folders, snapshots, git) stay. Only
		// once the CLI really has Full CLI (set after applyCLILandlock): a chat that falls back to bridge tools only
		// keeps the bridge file tools, so it never ends up with neither. The bridge tool list is admitted at launch.
		fullCLIOn := false
		toolGate.DenyWhere(func(name string) bool { return fullCLIOn && fullCLIDuplicateTool(name) })
		claims := GetUserFromContext(r.Context())
		projectTokenChat := resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID) && claims != nil && claims.AccessToken != nil
		if (projectTokenChat && currentUserIsReadOnly) && claims != nil && claims.AccessToken != nil {
			// Apply the existing token denylist to registration and the core CLI
			// bridge as well as execution, so an unusable shell is not advertised.
			toolGate.DenyWhere(func(name string) bool { return accessTokenRunToolDenied(claims, name) })
		}
		if currentUserIsReadOnly && resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID) {
			toolGate.DenyReaderTools(crewReaderDeniedTools()...)
			if crewAskOnly {
				toolGate.DenyWhere(func(name string) bool { return !crewAskAllowedTools[strings.TrimSpace(name)] })
			}
		}
		if resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID) && crewSetupLocked(strings.TrimSpace(req.SelectedFolder), crewTurnSetupLockUser(strings.TrimSpace(req.SelectedFolder), currentUserID, req.AskedBy)) {
			// A project User (crew_setup_lock.go) keeps every working tool, not the setup ones.
			toolGate.DenyWhere(func(name string) bool { return crewSetupLockedTools[strings.TrimSpace(name)] })
		}
		if resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID) && macCrewWorkflowToolsDenied(req, claims) {
			toolGate.DenyWhere(func(name string) bool { return macCrewOwnerOnlyWorkflowTools[strings.TrimSpace(name)] })
		}
		if resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID) && crewHasNoUsableSlackBot(r.Context(), resolvedProfile.Definition.ID, req.SelectedFolder) {
			// A Slack tool that can only answer "bot is disabled" is not offered: the model would try it first.
			toolGate.DenyWhere(func(name string) bool {
				name = strings.TrimSpace(name)
				return name == "slack" || name == "send_slack_message"
			})
		}

		defer toolGate.logSurface(sessionID)
		bridgeProvider := usesMCPBridge(finalProvider, finalModelID)
		useCodeExecutionMode := true
		var platformBridgeTools []string
		if projectTokenChat {
			useCodeExecutionMode, platformBridgeTools = externalProjectTransport(claims, toolGate, bridgeProvider)
		}
		log.Printf("[CODE_EXECUTION] session=%s code_execution=%v browser_mode=%s direct_bridge_tools=%v", sessionID, useCodeExecutionMode, req.BrowserMode, platformBridgeTools)
		// Every website tab shows the agent's questions and waits for the person; the server decides from the
		// turn's origin (codingAgentTurnAttended). The card is a platform capability of that tab, not a product
		// tool, so it is declared on every product's surface. Run people ask in plain text.
		attendedTurn := codingAgentTurnAttended(GetUserFromContext(r.Context()), &req, sessionID) && !currentUserIsReadOnly &&
			isCodingAgentProvider(finalProvider, finalModelID)
		if attendedTurn {
			toolGate.Declare("request_clarification")
		}
		attendedTurn = attendedTurn && toolGate.Admit("request_clarification")
		log.Printf("[QUESTIONS] session=%s attended=%t trigger=%q agent_message=%t provider=%s", sessionID, attendedTurn, req.TriggeredBy, req.AgentMessageInbox != "", finalProvider)
		// The person's own new message answers for any question card kept from before a restart.
		if trigger := strings.TrimSpace(req.TriggeredBy); (trigger == "" || trigger == "interactive") && !req.IsAutoNotification && req.AgentMessageInbox == "" &&
			codingAgentTurnAttended(GetUserFromContext(r.Context()), &req, sessionID) {
			go func() {
				api.restoreQuestionsOnce(sessionID)
				api.supersedeRecoveredQuestions(sessionID)
			}()
		}
		clarificationAvailable := attendedTurn
		// Claude can wait on background tasks it started before it ends a reply (the chat's own task list).
		claudeBackgroundWait := strings.EqualFold(finalProvider, "claude-code") && sessionID != ""
		if claudeBackgroundWait {
			toolGate.Declare(claudeBackgroundWaitTool)
			claudeBackgroundWait = toolGate.Admit(claudeBackgroundWaitTool)
		}
		// Any chat can read its own saved history by line (the handoff notices point at those lines).
		chatHistoryRead := sessionID != ""
		if chatHistoryRead {
			toolGate.Declare(chatHistoryReadTool)
			chatHistoryRead = toolGate.Admit(chatHistoryReadTool)
		}
		chatVisual := sessionID != ""
		if chatVisual {
			toolGate.Declare(chatVisualToolName)
			chatVisual = toolGate.Admit(chatVisualToolName)
		}
		// A question in a turn another agent's message started gets the unattended answer after 5 minutes.
		questionsUnansweredAfter := time.Duration(0)
		if attendedTurn {
			questionsUnansweredAfter = codingAgentQuestionUnansweredAfter(&req)
		}
		// Agents ask with their program's own question tool (Claude, Codex, Muse, Pi, ACP forms), answered by
		// the handler below; request_clarification stays registered as the private answer route of Claude's
		// terminal hook but is not offered to the model (owner, 21 Nov, decisions log). Cursor is the one
		// exception: its SDK gives no way to answer its own AskQuestion, so it keeps the tool on attended turns.
		if clarificationAvailable && strings.EqualFold(finalProvider, "cursor-cli") {
			platformBridgeTools = append(platformBridgeTools, "request_clarification")
		}
		if clarificationAvailable {
			streamCtx = llmtypes.WithCodingAgentQuestionHandler(streamCtx, finalProvider, sessionID, func(ctx context.Context, questions map[string]any) (string, error) {
				return api.waitForCodingAgentClarification(withQuestionUnansweredAfter(ctx, questionsUnansweredAfter), sessionID, finalProvider, questions)
			})
			// A Codex app-server chat's async question gets its card when asked; the transcript reader would show
			// it only after the turn ends. Both paths make the same card, so it shows once.
			streamCtx = llmtypes.WithNativeAsyncQuestionHandler(streamCtx, func(record llmtypes.NativeRecord) bool {
				if err := api.observeNativeAsyncQuestion(sessionID, record); err != nil {
					log.Printf("[HUMAN_INPUT] session=%s native_call=%s status=card_failed transport=app_server err=%v", sessionID, record.ID, err)
					return false
				}
				return true
			})
		}
		if claudeBackgroundWait {
			platformBridgeTools = append(platformBridgeTools, claudeBackgroundWaitTool)
		}
		if chatHistoryRead {
			platformBridgeTools = append(platformBridgeTools, chatHistoryReadTool)
		}
		if chatVisual {
			platformBridgeTools = append(platformBridgeTools, chatVisualToolName)
		}
		if resolvedProfile != nil {
			for _, name := range resolvedProfile.Definition.Runtime.BridgeTools {
				if toolGate.Admit(name) {
					platformBridgeTools = append(platformBridgeTools, name)
				}
			}
		}
		// A guest's ask has no shell, and every non-core tool is otherwise reached by curl through the shell, so the
		// two tools it needs are native bridge tools: reading project files and replying (856-59).
		if crewAskOnly {
			platformBridgeTools = crewAskBridgeTools(platformBridgeTools)
		}

		resolvedConnectionID := req.ConnectionID
		if req.LLMConfig != nil && req.LLMConfig.Primary.ConnectionID != "" {
			resolvedConnectionID = req.LLMConfig.Primary.ConnectionID
		}
		// Allowed models: whatever settled finalModelID above (request, saved
		// project or workflow, product default), the account's list decides. A
		// saved model it does not allow runs on the first allowed one.
		if constrained, changed, constrainErr := resolveAccountModel(withModelLimitPerson(streamCtx, modelLimitPersonID), finalProvider, resolvedConnectionID, finalModelID); constrainErr != nil {
			sendError(constrainErr.Error(), true)
			return
		} else if changed {
			finalModelID = constrained
			req.ModelID = constrained
			if req.LLMConfig != nil {
				configCopy := *req.LLMConfig
				configCopy.Primary.ModelID = constrained
				req.LLMConfig = &configCopy
			}
		}
		agentConfig := agent.LLMAgentConfig{
			ConnectionID:          resolvedConnectionID,
			Name:                  "chat-agent",
			ServerName:            serverList, // Use full server list, not just first one
			ConfigPath:            api.mcpConfigPath,
			AdmitTool:             toolGate.Admit,
			Provider:              llm.Provider(finalProvider),
			ModelID:               finalModelID,
			Options:               resolvedPrimaryOptions,
			Temperature:           req.Temperature,
			MaxTurns:              req.MaxTurns,
			ToolChoice:            "auto",
			StreamingChunkSize:    50,
			Timeout:               0,             // No per-Invoke timeout; streamCtx is explicitly canceled when needed.
			SelectedTools:         selectedTools, // NEW: Pass selected tools
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
			CodingAgentUserAnswersNativeQuestions:  attendedTurn,
			BridgeVirtualToolScopeKey:              "host-main",
			ClaudeCodeTransport:                    claudeCodeTransport,
			ForceStructuredCodingAgent:             forceStructuredCodingAgent,
			CodingAgentWorkingDir:                  chatWorkingDir,
			CodingAgentRuntimeDir:                  chatRuntimeDir,
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
		// A Code chat: pin its person before anything connects. The Code's own
		// connections join below, exactly as a Crew's do.

		// A fixed access builder never mounts personal, attached or ambient MCP
		// servers, even if an old conversation saved a broader selection.
		// A guest's free-text ask mounts no MCP server: guests act only through the functions the owner exposes (856-59).
		guestAsk := crewAskOnly
		if guestAsk || (resolvedProfile != nil && resolvedProfile.Definition.ID == knowledgebaseproduct.ProfileID) {
			selectedServers = []string{mcpclient.NoServers}
			agentConfig.RuntimeOverrides = nil
		}
		if len(selectedServers) == 1 && selectedServers[0] == mcpclient.NoServers {
			serverList = mcpclient.NoServers
		} else {
			serverList = strings.Join(selectedServers, ",")
		}
		agentConfig.ServerName = serverList

		applySharedLLMAgentTuning(&agentConfig, &req, presetLLMConfig)
		api.applyCompactEarlyToClaude(&agentConfig, sessionID, currentUserID)

		// Set agent mode based on request
		agentConfig.AgentMode = mcpagent.SimpleAgent
		log.Printf("[AGENT DEBUG] Creating agent with mode: %s, servers: %s", agentConfig.AgentMode, serverList)
		events.TurnSpeedStep(sessionID, "config")
		events.TurnSpeedProvider(sessionID, finalProvider+"/"+finalModelID)
		logfWithContext(queryLogCtx, "[LATENCY_DEBUG] T+%dms | Agent config built, creating agent wrapper | provider=%s model=%s", time.Since(startTime).Milliseconds(), finalProvider, finalModelID)
		agentConfig.ToolExecutionContext = api.bindToolExecutionContext(r.Context(), sessionID, req, currentUserIsReadOnly)
		// Create LLM agent wrapper with trace using streamCtx
		llmAgent, err := agent.NewLLMAgentWrapperWithTrace(streamCtx, agentConfig, tracer, traceID, queryLogger)
		if err != nil {
			logfWithContext(queryLogCtx, "[AGENT DEBUG] Failed to create LLM agent wrapper: %v", err)
			sendError(fmt.Sprintf("Failed to create agent: %v", err), true)
			return
		}
		events.TurnSpeedStep(sessionID, "wrapper")
		logfWithContext(queryLogCtx, "[LATENCY_DEBUG] T+%dms | Agent wrapper created", time.Since(startTime).Milliseconds())

		// Prime MCP server configs in the session registry for chat mode.
		// Workflow mode does this inside the orchestrator.
		if api.mcpConfig != nil {
			registry := mcpclient.GetSessionRegistry()
			base := api.baseMCPConfig()
			for _, sName := range selectedServers {
				if sName == mcpclient.NoServers {
					continue
				}
				serverCfg, cfgErr := base.GetServer(sName)
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
		// When skill-creator is selected, ensure it's installed (auto-fetch from GitHub
		// if missing). This is the one piece of grant-specific logic that doesn't fit
		// the registry — it's an install-on-demand side effect unique to skill-creator.
		if resolvedGrants.HasGrant("skill-creator") {
			// The workspace API, not this server. GetAPIURL points at the agent
			// server, so the lookup could never find the skill and every turn
			// re-imported it from GitHub — an unauthenticated API call per
			// message, on its way to being rate limited.
			workspaceAPIURL := getWorkspaceAPIURL()
			_, err := skills.GetSkillIn(workspaceAPIURL, chatWorkingFolder, "skill-creator")
			if err != nil {
				log.Printf("[SKILL CREATOR] skill-creator not found, attempting import from GitHub...")
				_, err := skills.ImportGitHubSkillInto(workspaceAPIURL, "https://github.com/anthropics/skills/tree/main/skills/skill-creator", "", strings.TrimSuffix(chatWorkingFolder, "/")+"/skills")
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

			// Make sure the chat folders exist; one that already exists is the usual answer, not a warning.
			for _, folder := range []string{"Chats", "subagents", "subagents/custom", "memories"} {
				if err := skills.CreateFolder(folder); err != nil && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
					log.Printf("[WORKSPACE] Warning: Could not create %s/ folder: %v", folder, err)
				}
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
			// req.SelectedGlobalSecrets is loaded from the workflow manifest above
			// (workflow_phase), so it picks up secrets selected in earlier turns;
			// multi-agent chat without a selection yields an empty map (no-op).
			endSpeedSpan6306 := events.TurnSpeedSpan(sessionID, "secrets")
			chatAgentSecretEnv := api.turnSelectedSecretEnv(r.Context(), sessionID, currentUserID, req, resolvedProfile, crewAskOnly, serverVaultRound)
			endSpeedSpan6306()
			workspaceRegistry := virtualtools.CreateWorkspaceToolRegistry(virtualtools.WorkspaceToolRegistryConfig{
				WorkspaceAPIURL:      getWorkspaceAPIURL(),
				UserID:               currentUserID,
				SessionID:            sessionID,
				ExtraEnvVars:         chatAgentSecretEnv,
				GenerateTextLLMTiers: generateTextWorkflowTiers(presetLLMConfig),
				APIKeys:              mergedAPIKeys,
			})
			if len(chatAgentSecretEnv) > 0 {
				logfWithContext(queryLogCtx, "[SECRETS] Injected %d secret(s) into chat agent shell env (chat)", len(chatAgentSecretEnv))
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
			toolCategories := workspaceRegistry.Categories
			logfWithContext(queryLogCtx, "[USER_ID_DEBUGGING] Main agent workspace executors: created with explicit userID=%q sessionID=%q", currentUserID, sessionID)

			// Merge @context file paths into additional folder-guard write access.
			// workflowReadOnlyFolders was computed above.
			fileContextWriteFolders := extractFileContextWriteFolders(req.Query)
			if len(fileContextWriteFolders) > 0 {
				log.Printf("[FILE CONTEXT] Extracted write folder-guard paths from @context: %v", fileContextWriteFolders)
			}
			if len(workflowReadOnlyFolders) > 0 {
				log.Printf("[FILE CONTEXT] Extracted read-only folder-guard paths from #workflow: %v", workflowReadOnlyFolders)
			}

			// Apply folder guard to restrict writes based on mode.
			// Non-workflow plan/chat sessions write to the per-user Chats folder.
			// Workflow phase writes to the active workflow folder (plus
			// Downloads and chat_history) and keeps Chats read-only so
			// builder artifacts cannot drift into normal chat storage.
			{
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
					// Private account identity keeps its binding; shell and file guards use the real project folder.
					profileBindingRoot := agentProfileRuntimeWorkspace(currentUserID, req.SelectedFolder)
					profileRoot := canonicalCrewWorkspaceRoot(profileBindingRoot)
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
						grantRead, grantWrite, grantReadOnly, grantEnv := workproduct.ResolveGrants(turnFolderGrants)
						profileReadOnly = append(profileReadOnly, grantRead...)
						workGrantWrite = grantWrite
						workGrantReadOnly = grantReadOnly
						if len(grantEnv) > 0 {
							common.SetSessionShellEnv(sessionID, grantEnv)
						}
						log.Printf("[WORK FOLDER GUARD] Applied %d attached folder(s) (write=%v) %s", len(grantRead), grantWrite, "source=turn_snapshot")
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
					crewRunOutput, ownerOutput := "", ""
					// A trusted isolated function has one output folder. Run
					// mode can write that folder without widening project access.
					functionOutput := crewFunctionOutputFolder(sessionID, currentUserID, profileRoot)
					outputFolder := crewOutputFolder(profileRoot)
					if crewAskOnly {
						outputFolder = ""
					}
					if functionOutput != "" {
						outputFolder = functionOutput
					}

					if crewReadOnlyTurn {
						guardWriteRoot = ""
						guardReadOnly = append([]string{profileWrite}, profileReadOnly...)
						guardWrite = nil
						// Run mode runs what the owner built and saves what it produces in the Crew's output folder,
						// made here; the rest of the Crew stays unwritable (PLAT-756, PLAT-812). The project root is then
						// not a blocked-write root: blocked writes win in every layer and would close the folder.
						if folder := outputFolder; folder != "" {
							if err := createWorkspaceFolder(r.Context(), strings.TrimSuffix(folder, "/")); err != nil {
								log.Printf("[AGENT PROFILE FOLDER GUARD] Output folder %s not created: %v", folder, err)
							} else {
								crewRunOutput = folder
								guardWrite = []string{folder}
							}
						}
						if crewRunOutput == "" {
							guardBlocked = append(guardBlocked, profileWrite)
						}
					} else if isCrewProjectPath(profileRoot) {
						// The owner's turn names the same folder, so a function saves its output in one place for everyone.
						if folder := outputFolder; folder != "" {
							if err := createWorkspaceFolder(r.Context(), strings.TrimSuffix(folder, "/")); err != nil {
								log.Printf("[AGENT PROFILE FOLDER GUARD] Output folder %s not created: %v", folder, err)
							} else {
								ownerOutput = folder
							}
						}
					}
					common.SetSessionRunOutputPath(sessionID, crewRunOutput)
					common.ReplaceSessionShellEnvPrefix(sessionID, crewOutputDirEnv, crewOutputFolderEnv(firstNonEmptyString(crewRunOutput, ownerOutput)))
					common.ReplaceSessionShellEnvPrefix(sessionID, crewRunDirEnv, crewOutputFolderEnv(firstNonEmptyString(crewRunOutput, ownerOutput)))
					// PLAT-856-506: apps a Crew keeps running.
					// Ports people can open from outside and the per-person limit apps share, from the server's settings.
					for _, name := range []string{"AGENTWORKS_APP_PORTS", "AGENTWORKS_APP_LIMITS"} {
						if value := strings.TrimSpace(os.Getenv(name)); value != "" {
							common.ReplaceSessionShellEnvPrefix(sessionID, name, map[string]string{name: value})
						}
					}
					// Brain's chat: a person who owns the whole Brain also works in Brain's folder with a shell and git,
					// like any product's folder (PLAT-633); everyone else stays in their own Brain chat folder.
					brainGrant := ""
					var brainEnv map[string]string
					if strings.EqualFold(resolvedProfile.Definition.ID, knowledgebaseproduct.ProfileID) {
						if !brainChatAllowed(r) {
							sendError("The Brain chat is for people who can edit Brain. You can read your folders in the Brain tab.", true)
							return
						}
						if env, ok := brainShellGrant(r); ok {
							brainGrant, brainEnv = brainFolderName+"/", env
							guardWrite = append(guardWrite, brainGrant)
						}
					}
					if resolvedProfile.Publication != nil && !crewReadOnlyTurn {
						guardWriteRoot = ""
						guardReadOnly = append([]string{profileWrite, resolvedProfile.Publication.sourceRoot() + "/"}, guardReadOnly...)
						guardWrite = publishedRuntimeRoots(resolvedProfile.Publication.runtimeRoot())
						if functionOutput != "" {
							guardWrite = appendUniqueStrings(guardWrite, functionOutput)
						}
					}
					executorWrite := append(append(append([]string{}, chatHistoryGrants...), workGrantWrite...), crewRefWrite...)
					if resolvedProfile.Publication != nil {
						executorWrite = append(executorWrite, guardWrite...)
					}
					if crewRunOutput != "" {
						executorWrite = append(executorWrite, crewRunOutput)
					}
					if brainGrant != "" {
						executorWrite = append(executorWrite, brainGrant)
					}
					workspaceExecutors = wrapExecutorsWithPlanFolderGuard(workspaceExecutors, guardWriteRoot, guardReadOnly, executorWrite...)
					workspace.SetSessionWorkingDir(sessionID, crewFilesPath(profileRoot))
					if source := publicationSourceForProfile(resolvedProfile); source != "" {
						workspace.SetSessionWorkingDir(sessionID, source)
					}
					if p := resolvedProfile.Publication; p != nil {
						common.ReplaceSessionShellEnvPrefix(sessionID, "AGENTWORKS_PUBLICATION_", publicationShellEnvironment(p))
					}
					if brainGrant != "" {
						syncBrainAfterFileTools(workspaceExecutors, currentUserID)
						workspace.SetSessionWorkingDir(sessionID, brainFolderName)
						common.SetSessionShellEnv(sessionID, brainEnv)
					}

					guardRead := append(append(append([]string{profileWrite}, chatHistoryGrants...), profileReadOnly...), crewRefWrite...)
					if roots, bound, rootErr := localCrewRoots(profileRoot); bound || rootErr != nil {
						if rootErr != nil {
							sendError(rootErr.Error(), true)
							return
						}
						guardRead = append(guardRead, roots.FilesPath())
						if !crewReadOnlyTurn {
							guardWrite = append(guardWrite, roots.FilesPath())
						}
					}

					// Private desktop captures belong to this interactive owner chat, never the project.
					if !crewReadOnlyTurn && computerUseAllowed(GetUserFromContext(r.Context())) && websiteDeviceClaims(GetUserFromContext(r.Context())) && laptopBuilderTurn(req, resolvedProfile) && crewProjectOwnedByCaller(currentUserID, profileBindingRoot) {
						if captures, err := computerUseChatDirectory(chatWorkingFolder, sessionID); err == nil {
							if err := os.MkdirAll(captures, 0700); err == nil {
								guardRead = append(guardRead, captures)
								cliSecurityPolicy = computerUseCaptureReadPolicy(cliSecurityPolicy, captures)
							}
						}
					}
					if brainGrant != "" {
						guardRead = append(guardRead, brainGrant)
					}
					workspace.SetSessionFolderGuard(sessionID,
						guardRead,
						append(append(append(guardWrite, chatHistoryGrants...), workGrantWrite...), crewRefWrite...),
					)
					guardBlocked = append(guardBlocked, workGrantReadOnly...)
					if isProjectProfileID(resolvedProfile.Definition.ID) {
						// A Crew writes only its own database; other Crews' db/
						// stays readable but never writable.
						guardBlocked = append(guardBlocked, foreignCrewDBWriteBlockedPaths(profileRoot, workflowReadOnlyFolders)...)
						// Scheduled callers change only through typed schedule tools,
						// including in owner Builder shells and attached Crew folders.
						guardBlocked = append(guardBlocked, profileRoot+"/schedules.json", profileRoot+"/webhooks.json", profileRoot+"/schedule-runs.json")
						// A project User (crew_setup_lock.go) cannot write the setup files (crew_setup_lock.go).
						guardBlocked = append(guardBlocked, crewSetupLockedWritePaths(profileRoot, crewTurnSetupLockUser(profileRoot, currentUserID, req.AskedBy))...)
						for _, root := range crewRefWrite {
							guardBlocked = append(guardBlocked, strings.TrimSuffix(root, "/")+"/schedules.json", strings.TrimSuffix(root, "/")+"/webhooks.json", strings.TrimSuffix(root, "/")+"/schedule-runs.json")
						}
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
					additionalFolders := append([]string{}, resolvedGrants.WriteFolders...)
					additionalFolders = append(additionalFolders, fileContextWriteFolders...)
					additionalFolders = append(additionalFolders, perUserChatHistory)
					workspaceExecutors = wrapExecutorsWithPlanFolderGuard(workspaceExecutors, perUserChatsFolder, workflowReadOnlyFolders, additionalFolders...)
					workspace.SetSessionWorkingDir(sessionID, crewFilesPath(chatWorkingFolder))
					// Other workflows are readable only when attached to this chat
					// (workflowReadOnlyFolders below), never the whole Workflow/ tree.
					readPaths := append([]string{perUserChatsWrite, perUserChatHistory, "subagents/"}, additionalFolders...)
					readPaths = append(readPaths, resolvedGrants.ReadOnlyExtra...)
					readPaths = append(readPaths, workflowReadOnlyFolders...)
					workspace.SetSessionFolderGuard(sessionID,
						readPaths,
						append([]string{perUserChatsWrite, perUserChatHistory}, additionalFolders...),
					)
					if hostDownloads := common.GrantSessionCDPHostDownloadsReadWrite(sessionID, hostDownloadsBrowserMode(req)); hostDownloads != "" {
						log.Printf("[MULTI-AGENT FOLDER GUARD] Added read-write CDP host Downloads: %s", hostDownloads)
					}
					log.Printf("[MULTI-AGENT FOLDER GUARD] Applied per-user folder restriction (chats: %s, write: %v, read-only: %v, grants: %v)", perUserChatsWrite, additionalFolders, workflowReadOnlyFolders, resolvedGrants.AppliedNames)
				}
			}

			// Coding-agent adapters project system instructions, selected skills,
			// MCP config and provider metadata into the working directory. Keep
			// those readable for the CLI but outside the agent's write authority.
			// Generated Crew projections live in its private runtime; similarly
			// named project files belong to the person.
			if resolvedProfile == nil || !isProjectProfileID(resolvedProfile.Definition.ID) {
				protectManagedCodingAgentProjectionWrites(sessionID, chatWorkingFolder)
			}
			// PLAT-364: the guard above is final, so the CLI can be confined to it.
			applyCLILandlock(llmAgent, sessionID, currentUserID, finalProvider, chatWorkingDir, cliSecurityPolicy, chatRuntimeDir)
			if llmAgent.CodingAgentToolsMode() == "full" && isCodingAgentProvider(finalProvider, finalModelID) {
				// `mcpbridge secret NAME` in the CLI's own shell (session_value_read.go).
				api.writeSecretReadFile(sessionID, cliPrivateHome(finalProvider, chatWorkingDir, chatRuntimeDir))
			}
			if fullCLIOn = profileAgentToolsMode == "full" && isCodingAgentProvider(finalProvider, finalModelID) && llmAgent.CodingAgentToolsMode() == "full"; fullCLIOn {
				log.Printf("[PRODUCT_TOOL_GATE] session=%s full_cli=true duplicate_file_and_terminal_actions=off", sessionID)
				// Owner, 5 Nov: "files should be removed for sure in full cli". Its remaining actions
				// (copy, link, folders, snapshots, git views) stay reachable through search_tools/call_tool.
				if err := llmAgent.OmitCoreBridgeTools("files"); err != nil {
					log.Printf("[PRODUCT_TOOL_GATE] session=%s full_cli files bridge omit failed: %v", sessionID, err)
				}
			}
			if resolvedProfile != nil && resolvedProfile.IsolateCodeCLI {
				log.Printf("[PROJECT_CLI] session=%s actor=%s profile=code mode=%s cwd=%s linked_project=%s", sessionID, sanitizeUserIDForPath(currentUserID), llmAgent.CodingAgentToolsMode(), chatWorkingDir, sharedChatWorkingDir)
			}

			// Report the selected filesystem skills, not a restriction. Every
			// branch above grants its workspace's skills, and this list is used
			// nowhere else — the old wording ("only selected skills accessible")
			// described a guard that is not applied, which sent me looking for a
			// permission problem when a skill failed to attach.
			// Runtime-only skills such as agent-browser are exposed through tools/prompts, not skills/<name>/SKILL.md.
			if filesystemSkills := filesystemSelectedSkills(req.SelectedSkills); len(filesystemSkills) > 0 {
				log.Printf("[SKILLS] Filesystem skills selected for this session: %v (skills/ is readable in full)", filesystemSkills)
			}

			workspaceToolModeLabel := "chat mode"

			log.Printf("[WORKSPACE TOOLS] Registering %d workspace tools for %s", len(workspaceTools), workspaceToolModeLabel)

			for _, tool := range workspaceTools {
				if tool.Function == nil {
					log.Printf("[WORKSPACE TOOLS] Warning: Skipping tool with nil Function")
					continue
				}
				toolName := tool.Function.Name
				if profileDisablesVirtualTool(resolvedProfile, toolName) {
					log.Printf("[AGENT_PROFILE] Skipping disabled virtual tool %s for profile %s", toolName, resolvedProfile.Definition.ID)
					continue
				}
				if executor, exists := workspaceExecutors[toolName]; exists {
					// Enhance tool description based on mode
					var enhancedDescription string
					{
						descriptionRoot := perUserChatsFolder
						if resolvedProfile != nil {
							descriptionRoot = agentProfileRuntimeWorkspace(currentUserID, req.SelectedFolder)
						}
						enhancedDescription = enhanceToolDescriptionForMultiAgentMode(toolName, tool.Function.Description, descriptionRoot, resolvedProfile)
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
					{
						if resolvedProfile != nil && !isGlobalScopedProfile(resolvedProfile) {
							profileRoot := canonicalCrewWorkspaceRoot(agentProfileRuntimeWorkspace(currentUserID, req.SelectedFolder))
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

				}
				browserPorts := getCdpPorts(req)
				if getBrowserMode(req) == "auto" {
					browserPorts = configuredCDPPortsForMode(getBrowserMode(req), req.CdpPort, req.resolvedCDPPorts)
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
			if clarificationAvailable && codingAgentNeedsClarificationRoute(finalProvider, claudeCodeTransport) {
				if err := api.registerCodingAgentClarificationTool(llmAgent, sessionID, finalProvider, questionsUnansweredAfter); err != nil {
					sendError(fmt.Sprintf("Failed to register clarification tool: %v", err), true)
					return
				}
			}
			if chatHistoryRead {
				if err := api.registerChatHistoryReadTool(llmAgent, currentUserID, sessionID); err != nil {
					sendError(fmt.Sprintf("Failed to register chat history tool: %v", err), true)
					return
				}
			}
			if chatVisual {
				if err := registerChatVisualTool(llmAgent); err != nil {
					sendError(fmt.Sprintf("Failed to register visual authoring tool: %v", err), true)
					return
				}
			}
			if claudeBackgroundWait {
				if err := api.registerClaudeBackgroundWaitTool(llmAgent, sessionID); err != nil {
					sendError(fmt.Sprintf("Failed to register background wait tool: %v", err), true)
					return
				}
			}
			// Create custom tools for the agent. Workflow-phase (workshop) agents need
			// the full applicable human-tool set registered including summary tools.
			// Chat mode stays minimal (workflowMode=false). Without
			// this, the notification implementation was not registered on the serving agent, so it never landed
			// in a.customTools and was invisible to CLI agents via get_api_spec.
			endSpeedSpan6752 := events.TurnSpeedSpan(sessionID, "custom_tools")
			allTools, allExecutors, toolCategories := createCustomTools(false, currentUserID, sessionID, req.SelectedFolder) // session-aware
			endSpeedSpan6752()
			if knowledgeProjectBuilderQuery(req, GetUserFromContext(r.Context()), sessionID, sessionID, currentUserIsReadOnly) {
				projectTools, projectExecs, projectCategories := createKnowledgeProjectBuilderTools()
				// Replace content-only browse/access schemas for this root Builder.
				for _, tool := range projectTools {
					replaced := false
					for i, old := range allTools {
						if old.Function != nil && old.Function.Name == tool.Function.Name {
							allTools[i] = tool
							replaced = true
							break
						}
					}
					if !replaced {
						allTools = append(allTools, tool)
					}
				}
				for name, fn := range projectExecs {
					allExecutors[name] = fn
					toolCategories[name] = projectCategories[name]
				}
			}

			// Register each custom tool with the agent
			// This updates the custom-tool registry and invalidates affected API specifications.
			// Note: Workspace tools are already registered above, skip them in allTools
			registeredCount := 0
			for _, tool := range allTools {
				if tool.Function != nil {
					toolName := tool.Function.Name
					toolCategory := toolCategories[toolName]
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
			if resolvedProfile != nil {
				resolvedProfile.ToolCallerClaims = GetUserFromContext(r.Context())
			}
			if err := api.registerAgentProfileTools(llmAgent, toolGate, resolvedProfile, currentUserID, sessionID, req.SelectedFolder, crewReadOnly, req); err != nil {
				logfWithContext(queryLogCtx, "[AGENT PROFILE] Failed to register tools: %v", err)
				sendError(fmt.Sprintf("Failed to register agent profile tools: %v", err), true)
				return
			}
			// Whoever may manage Vault can connect and share its MCPs from any chat (Code, Crew, workflows); the
			// tool is only registered for an active administrator and rechecked on every call (PLAT-503).
			if resolvedProfile == nil || resolvedProfile.Definition.ID != caplayerproduct.ProfileID {
				if err := api.registerVaultAccessChatTool(llmAgent, currentUserID); err != nil {
					sendError(fmt.Sprintf("Failed to register Vault access tool: %v", err), true)
					return
				}
				if err := api.registerMyVaultsTool(llmAgent, currentUserID); err != nil {
					sendError(fmt.Sprintf("Failed to register vault tool: %v", err), true)
					return
				}
			}
			// A Code chat's agent connects the person's own MCP servers.

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
				crewReadOnly || currentUserIsReadOnly,
			); err != nil {
				logfWithContext(queryLogCtx, "[AGENT PROFILE] Failed to register workflow runtime: %v", err)
				sendError(fmt.Sprintf("Failed to register agent profile workflow runtime: %v", err), true)
				return
			}
			if err := api.registerWorkDashboardTools(llmAgent, resolvedProfile, sessionID, currentUserID, req.SelectedFolder, crewReadOnly || currentUserIsReadOnly); err != nil {
				logfWithContext(queryLogCtx, "[WORK DASHBOARD] Failed to register tools: %v", err)
				sendError(fmt.Sprintf("Failed to register project data tools: %v", err), true)
				return
			}
			// Laptop setup (computer, folders, commands switch) is available from the Crew owner's own chat, as in the panel.
			if resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID) {
				if err := api.registerCrewSettingsChatTools(llmAgent, toolGate, resolvedProfile, GetUserFromContext(r.Context()), crewReadOnly || currentUserIsReadOnly, sessionID, chatWorkingFolder, getBaseURL(r)); err != nil {
					sendError(fmt.Sprintf("Failed to register project settings tools: %v", err), true)
					return
				}
				if err := api.registerLaptopAccessChatTool(llmAgent, toolGate, GetUserFromContext(r.Context()), crewReadOnly || currentUserIsReadOnly, sessionID, chatWorkingFolder); err != nil {
					sendError(fmt.Sprintf("Failed to register laptop setup tool: %v", err), true)
					return
				}
				teamSourceKey := ""
				if ask := turnSharedAsk(r.Context(), &req); ask != nil {
					teamSourceKey = ask.Owner.ConversationKey
				}
				if err := api.registerCrewTeamChatTool(llmAgent, toolGate, GetUserFromContext(r.Context()), crewReadOnly || currentUserIsReadOnly, sessionID, chatWorkingFolder, req.SharedAgentSender, teamSourceKey); err != nil {
					sendError(fmt.Sprintf("Failed to register Team Sync: %v", err), true)
					return
				}
				if err := api.registerCrewOwnersChatTool(llmAgent, toolGate, GetUserFromContext(r.Context()), crewReadOnly || currentUserIsReadOnly, chatWorkingFolder); err != nil {
					sendError("Failed to register project owners tool", true)
					return
				}
				if err := api.registerProviderAccountsChatTool(llmAgent, toolGate, GetUserFromContext(r.Context()), crewReadOnly || currentUserIsReadOnly, getBaseURL(r)); err != nil {
					sendError("Failed to register AI accounts tool", true)
					return
				}
				if err := api.registerCrewSyncChatTool(llmAgent, toolGate, GetUserFromContext(r.Context()), crewReadOnly || currentUserIsReadOnly, sessionID, chatWorkingFolder); err != nil {
					sendError(fmt.Sprintf("Failed to register project sync tool: %v", err), true)
					return
				}
				if err := api.registerComputerUseTool(llmAgent, toolGate, GetUserFromContext(r.Context()), crewReadOnly || currentUserIsReadOnly, sessionID, chatWorkingFolder); err != nil {
					sendError("Failed to register Computer use", true)
					return
				}
				// WhatsApp status, routing and unpairing, as in the WhatsApp card (pairing itself stays in the app).
				if !crewReadOnly && !currentUserIsReadOnly {
					if err := api.registerWorkspacePanelChatTools(llmAgent, toolGate, currentUserID, sessionID, chatWorkingFolder, "work"); err != nil {
						sendError(fmt.Sprintf("Failed to register panel settings tools: %v", err), true)
						return
					}
				}

			}
			if resolvedProfile == nil && laptopBuilderTurn(req, nil) {
				if err := api.registerLaptopAccessChatTool(llmAgent, toolGate, GetUserFromContext(r.Context()), crewReadOnly || currentUserIsReadOnly, sessionID, chatWorkingFolder); err != nil {
					sendError(fmt.Sprintf("Failed to register laptop setup tool: %v", err), true)
					return
				}
			}
			// Local folder grants belong to the owner's interactive website turn;
			// scheduled, connector and unattended/background adapters need separate consent.
			if req.AgentProfileID == "work" && laptopBuilderTurn(req, resolvedProfile) {
				if roots, found, err := crewStorageRoots(req.SelectedFolder); err == nil && found {
					if binding, err := crewroot.ReadSync(roots.ID); err == nil && binding != nil {
						location := "the server"
						if IsLocalMode() {
							location = "this Mac"
						}
						_ = llmAgent.AddInstructions(fmt.Sprintf("Paired project: this turn runs on %s; its default shell and file tools use %q. Project editing is on %s. For explicitly requested work on the connected Mac from the server, use the Local laptop tools; they do not change the editing location or sync files automatically.", location, roots.Files, binding.Editing))
					}
				}
			}
			if req.LaptopConnection != nil {
				if err := api.registerLaptopTools(llmAgent, toolGate, GetUserFromContext(r.Context()), crewReadOnly || currentUserIsReadOnly, sessionID, chatWorkingFolder, req.LaptopConnection); err != nil {
					sendError(fmt.Sprintf("Failed to register local file tools: %v", err), true)
					return
				}
			}
			isToolBackedChat := true
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
				// Every product manages skills in its own workspace.
				projectSkillsDir := strings.TrimSuffix(chatWorkingFolder, "/") + "/skills"

				if err := api.registerMultiAgentSkillToolsIn(llmAgent, func(toolName string) bool {
					return profileDisablesVirtualTool(resolvedProfile, toolName)
				}, skillProductID, projectSkillsDir); err != nil {
					logfWithContext(queryLogCtx, "[SKILL TOOLS] Failed to register multi-agent skill tools: %v", err)
					sendError(fmt.Sprintf("Failed to register multi-agent skill tools: %v", err), true)
					return
				}
				logfWithContext(queryLogCtx, "[SKILL TOOLS] Registered multi-agent skill tools")
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
				afterSecretUpsert := func(ctx context.Context, name, vaultName, value string) error {
					if resolvedProfile != nil && !isGlobalScopedProfile(resolvedProfile) && secretWorkflowPath != "" {
						if err := selectProjectSecret(ctx, resolvedProfile.Definition.ID, secretWorkflowPath, vaultName); err != nil {
							return err
						}
					}
					updated := virtualtools.SetSessionShellEnv(sessionID, "SECRET_"+name, value)
					logfWithContext(queryLogCtx, "[SECRET TOOLS] Made SECRET_%s available to %d live shell client(s) for session %s", name, updated, sessionID)
					return nil
				}
				afterSecretDelete := func(ctx context.Context, name string) error {
					if resolvedProfile != nil && !isGlobalScopedProfile(resolvedProfile) && secretWorkflowPath != "" {
						if err := unselectProjectSecret(ctx, resolvedProfile.Definition.ID, secretWorkflowPath, name); err != nil {
							return err
						}
					}
					name = vaultSecretShortName(name)
					updated := virtualtools.DeleteSessionShellEnv(sessionID, "SECRET_"+name)
					logfWithContext(queryLogCtx, "[SECRET TOOLS] Removed SECRET_%s from %d live shell client(s) for session %s", name, updated, sessionID)
					return nil
				}
				if err := api.registerSecretManagementTools(llmAgent, currentUserID, secretWorkflowPath, "secret_tools", currentUserIsReadOnly, afterSecretUpsert, afterSecretDelete); err != nil {
					logfWithContext(queryLogCtx, "[SECRET TOOLS] Failed to register multi-agent secret tools: %v", err)
					sendError(fmt.Sprintf("Failed to register multi-agent secret tools: %v", err), true)
					return
				}
				logfWithContext(queryLogCtx, "[SECRET TOOLS] Registered multi-agent secret tools with live session injection (project secret tools; Vault management tools admitted for administrators)")
			}
			if isAgentWorksChat {
				if err := api.registerCustomCommandTools(llmAgent, currentUserID, ""); err != nil {
					sendError(fmt.Sprintf("Failed to register custom command tools: %v", err), true)
					return
				}
				// Generic AgentWorks chat can create projects and inspect live
				// workflow activity. Product-owned agents opt into their own tools.
				if userAccessForClaims(GetUserFromContext(r.Context())).CanCreateIn("agentworks") {
					if err := api.registerProjectTools(llmAgent, toolGate, currentUserID, GetUserFromContext(r.Context())); err != nil {
						logfWithContext(queryLogCtx, "[PROJECT CREATOR] Failed to register create_project tool: %v", err)
						sendError(fmt.Sprintf("Failed to register create_project tool: %v", err), true)
						return
					}
					logfWithContext(queryLogCtx, "[PROJECT CREATOR] Registered create_project tool")
				} else {
					logfWithContext(queryLogCtx, "[PROJECT CREATOR] create_project omitted: account cannot create projects")
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
				if isProjectProfileID(resolvedProfile.Definition.ID) && (chatWorkingDir != sharedChatWorkingDir || chatRuntimeDir != "") {
					if err := llmAgent.AddInstructions(crewCLIWorkspaceInstructions(chatWorkingFolder)); err != nil {
						sendError(fmt.Sprintf("Failed to apply project runtime instructions: %v", err), true)
						return
					}
				}
			} else {
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
			if strings.EqualFold(finalProvider, "claude-code") && claudeSummarisesPreToolText(finalModelID) {
				if err := llmAgent.AddInstructions(claudeReplyHintInstruction); err != nil {
					logfWithContext(queryLogCtx, "[CLAUDE_REPLY_HINT] Failed to add instruction: %v", err)
				}
			}
			if claudeBackgroundWait {
				if err := llmAgent.AddInstructions(claudeBackgroundWaitInstruction); err != nil {
					logfWithContext(queryLogCtx, "[CLAUDE_BG_WAIT] Failed to add instruction: %v", err)
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
				// installs skills into its project (a product installs its
				// managed HyperFrames set there) was invisible to the unscoped
				// lookup, so those skills never attached — and read_skill, which
				// serves only ATTACHED skills, then had nothing to read. That is
				// what surfaced in chat as "the skill reader was blocked".
				// read_skill can also serve a skill installed in this workspace
				// but not attached, so a router skill names the specialists and
				// the agent asks by name instead of shelling out to a path.
				if err := llmAgent.SetInstalledSkillResolver(chatWorkingFolder, installedSkillResolver(chatWorkingFolder)); err != nil {
					logfWithContext(queryLogCtx, "[SKILLS] Failed to configure installed-skill resolver: %v", err)
				}
				attached := skills.LoadAttachableIn(getWorkspaceAPIURL(), chatWorkingFolder, identitySkillNames)
				if resolvedProfile != nil {
					attached = agentprofiles.FeatureSkillsForSession(resolvedProfile.Definition, attached)
					attached = append(attached, browserinstructions.ProjectMemorySkill(currentUserIsReadOnly))
				}
				attached = slices.DeleteFunc(attached, func(s *llmtypes.Skill) bool { return s != nil && skillWithoutItsTools(s.Name, req) })
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

			if resolvedProfile != nil {
				if err := llmAgent.AttachSkill(browserinstructions.ProjectMemorySkill(currentUserIsReadOnly)); err != nil {
					sendError(fmt.Sprintf("Failed to attach project memory guidance: %v", err), true)
					return
				}
			}

			// 3. INSTRUCTION SECTIONS — workspace map, capabilities, workflow
			//    context, channel formatting, browser pointer, reference docs,
			//    grants, and the CLI tool environment. Each is a named section
			//    with its condition beside every other section's, and the
			//    assembler logs what it applied. See prompt_sections.go for why
			//    these stopped being inline ifs.
			promptCtx := promptContext{
				// The CLI that actually runs: a workflow's manifest LLM replaces the
				// requested one (the log named the request's CLI, not Muse/Codex).
				Provider:              finalProvider,
				HasProfile:            resolvedProfile != nil,
				CrewReadOnly:          crewReaderCLI,
				MemoryReadOnly:        currentUserIsReadOnly,
				ProjectMemoryDisabled: resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID) && !agentprofiles.HasFeature(resolvedProfile.Definition, "memory"),
				ShellRoot:             shellRoot,
				PerUserChatsFolder:    perUserChatsFolder,
				ProfileWorkspace:      agentProfileRuntimeWorkspace(currentUserID, req.SelectedFolder),
				CapabilitySection:     buildLLMCapabilityPromptSection(r.Context()),
				AuthenticatedUser:     authenticatedChatUserPrompt(GetUserFromContext(r.Context())),
				// Capability details are loaded through this tool, so do not
				// advertise its pointer merely because auth setup is admitted.
				HasLLMCapabilityTools: toolGate.Admit("list_llm_capabilities"),
				ChannelFormatting:     buildChannelFormattingInstructions(req.BotPlatform),
				GrantSections:         resolvedGrants.PromptSections,
				SharedServer:          !IsLocalMode(),
			}
			// The mode the chat really starts with, after confinement (a full
			// request that could not be confined is mcp_only), for workflow,
			// Crew and Code chats alike.
			promptCtx.NativeCodingTools = strings.HasPrefix(llmAgent.CodingAgentToolsMode(), "full")
			if resolvedProfile != nil {
				promptCtx.ProfileID = resolvedProfile.Definition.ID
				promptCtx.FeatureExtensions = agentprofiles.FeaturePromptExtensions(resolvedProfile.Definition)
			}
			if len(req.WorkflowContextPaths) > 0 {
				referenceSkillName := "work-workflow-files"
				if resolvedProfile != nil {
					referenceSkillName = agentprofiles.FeatureSkillName(resolvedProfile.Definition.ID, referenceSkillName)
				} else {
					// Workflow chats have no project profile to attach feature skills.
					// Supply the same canonical reference guide before pointing to it.
					referenceSkills := skills.LoadAttachableIn(getWorkspaceAPIURL(), chatWorkingFolder, []string{referenceSkillName})
					if len(referenceSkills) == 0 {
						sendError("Project reference guidance is unavailable", true)
						return
					}
					for _, referenceSkill := range referenceSkills {
						if err := llmAgent.AttachSkill(referenceSkill); err != nil {
							sendError(fmt.Sprintf("Failed to attach project reference guidance: %v", err), true)
							return
						}
					}
				}
				promptPaths := req.authorizedWorkflowContextReadPaths
				if len(promptPaths) == 0 {
					promptPaths = req.WorkflowContextPaths
				}
				promptCtx.WorkflowContext = buildWorkflowContextPromptWithLabels(promptPaths, workflowContextLabels(req.WorkflowContextRefs), referenceSkillName)
			}
			if note := omittedWorkflowContextNote(req.omittedWorkflowContextCount); note != "" {
				promptCtx.WorkflowContext += note
			}
			if resolvedProfile != nil && isProjectProfileID(resolvedProfile.Definition.ID) {
				attachedFolders := workproduct.BuildAttachedFoldersPrompt(turnFolderGrants)
				macFolders := laptopFoldersPrompt(GetUserFromContext(r.Context()), req.LaptopConnection, req.SelectedFolder)
				if macFolders != "" && len(workproduct.AvailableAttachedGrants(turnFolderGrants)) == 0 {
					attachedFolders = "\n### Attached Folders\n" // not "No folders are attached": the computer's folders follow
				}
				promptCtx.WorkFolders = crewTeamFilesLine(GetUserFromContext(r.Context())) + attachedFolders + macFolders + "\n" + turnFolderNotice
				promptCtx.ConnectedTools = api.buildConnectedToolsPrompt(r.Context(), resolvedProfile.Definition.ID, GetUserFromContext(r.Context()), req.SelectedFolder, crewAskOnly, crewReaderCLI)
			}
			// The full browser guide is a ~10KB on-demand skill; this is only a
			// pointer so the agent knows the surface exists.
			chatBrowserCfg := buildChatBrowserConfig(req)
			if chatBrowserCfg.HasAgentBrowser {
				browserPrompt := "\n## Browser\n\nA browser tool is set up (mode=" + chatBrowserCfg.Mode + "). Before first use run its `status` command and follow its `effective_mode` and endpoints; the `agent-browser` skill covers tabs, files and safety. "
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
			// all, so a CLI-provider product chat can
			// correctly run its one allowlisted tool and then, in the very same
			// turn, tell the user it has no working tool because this stale text
			// contradicted what actually happened.
			hasProductAllowlist := resolvedProfile != nil && resolvedProfile.Definition.ToolPolicy.IsAllowlist()
			if common.IsCLIProvider(req.Provider) && !hasProductAllowlist {
				promptCtx.CLIToolEnvironment = virtualtools.BuildCLIToolEnvironmentPrompt(req.Provider)
			}

			endSpeedSpan7255 := events.TurnSpeedSpan(sessionID, "prompt")
			includedSections, skippedSections, sectionErr := assemblePromptSections(llmAgent, promptCtx)
			endSpeedSpan7255()
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

		}

		// Secret values stay in the tool environment; only their names belong in
		// the immutable identity. Assemble that section before finalization.
		endSpeedSpan7274 := events.TurnSpeedSpan(sessionID, "secrets")
		identitySecrets := api.mergeGlobalSecretsFor(context.Background(), api.turnSecretsOwner(sessionID, currentUserID, req.SelectedFolder), req.SelectedGlobalSecrets)
		endSpeedSpan7274()
		if len(identitySecrets) > 0 {
			if underlyingAgent := llmAgent.GetUnderlyingAgent(); underlyingAgent != nil {
				_ = llmAgent.AddInstructions(buildSecretNamesPrompt(identitySecrets))
			}
		}
		// The project owner's PROJECT_INSTRUCTIONS.md goes last, below every platform section, so it
		// adds to the platform's instructions and never replaces them (PLAT-692).
		if resolvedProfile != nil && !isGlobalScopedProfile(resolvedProfile) && resolvedProfile.ProjectInstructions != "" {
			if err := llmAgent.AddInstructions(resolvedProfile.ProjectInstructions); err != nil {
				sendError(fmt.Sprintf("Failed to apply project instructions: %v", err), true)
				return
			}
		}

		// Observers are runtime construction inputs, so attach them to the draft
		// before finalization instead of mutating the live agent afterward.
		eventObserver := events.NewEventObserverWithLogger(api.eventStore, sessionID, queryLogger, queryID)
		costObs := newCostObserver(
			api.costLedger,
			sessionID,
			currentUserID,
			req.AgentMode,
			withCostModel(finalProvider, finalModelID),
			// The account the turn runs on, as admission saw it: a follow-up that names none runs on
			// the session's last account (queryTurnConnection alone recorded the server account).
			costobserver.WithAccount(costobserver.AccountIDFor(finalProvider, func() string { _, id := api.queryTurnConnectionForSession(req, sessionID); return id }())),
			withCostAttribution(
				costScope,
				req.SelectedFolder,
				"",
				queryID,
			),
			withCostSourcePlatform(req.BotPlatform),
			withCostBillingUser(botTokenOwner),
			costSourceOption(req),
			costobserver.WithProductAttribution(turnCostAttribution),
		)
		if err := llmAgent.AddObserver(&restartAwareEventObserver{EventObserver: eventObserver}); err != nil {
			sendError(fmt.Sprintf("Failed to attach event observer: %v", err), true)
			return
		}
		if err := llmAgent.AddObserver(costObs); err != nil {
			sendError(fmt.Sprintf("Failed to attach cost observer: %v", err), true)
			return
		}

		if projectTokenChat && !useCodeExecutionMode {
			// Include names declared by profile factories during registration,
			// rather than freezing the bridge from the initial policy alone.
			_, platformBridgeTools = externalProjectTransport(claims, toolGate, false)
			if err := llmAgent.SetAdditionalBridgeTools(platformBridgeTools); err != nil {
				sendError(fmt.Sprintf("Failed to set project bridge tools: %v", err), true)
				return
			}
			log.Printf("[CODE_EXECUTION] session=%s code_execution=false registered_direct_bridge_tools=%v", sessionID, platformBridgeTools)
		}

		// Freeze the incrementally assembled chat identity and runtime before any
		// turn can run. Finalization constructs the sole live Agent instance.
		events.TurnSpeedStep(sessionID, "tools_prompt")
		logfWithContext(queryLogCtx, "[LATENCY_DEBUG] T+%dms | Workspace, tools and prompt prepared, finalizing agent", time.Since(startTime).Milliseconds())
		if err := llmAgent.FinalizeDefinition(streamCtx); err != nil {
			sendError(fmt.Sprintf("Failed to finalize agent definition: %v", err), true)
			return
		}
		events.TurnSpeedStep(sessionID, "finalize")
		logfWithContext(queryLogCtx, "[LATENCY_DEBUG] T+%dms | Agent finalized", time.Since(startTime).Milliseconds())

		// Preserve the finalized prompt for its owner's read-only workspace
		// inspector, including API providers and chats restored after a restart.
		if api.eventStore != nil && api.eventStore.IsDurableChatSession(sessionID) {
			if err := saveSessionInstructions(GetUserIDFromContext(r.Context()), sessionID, req.SelectedFolder, mcpagent.ReadAgentSystemPrompt(streamCtx, llmAgent.GetUnderlyingAgent())); err != nil {
				log.Printf("[INSTRUCTIONS] Failed to save prompt snapshot for session %s: %v", sessionID, err)
			}
		}

		if api.internalPreparedAgent != nil && api.internalPreparedAgent(streamCtx, llmAgent.GetUnderlyingAgent()) {
			_ = llmAgent.Close()
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
		// allow-list reflect the previous mode. Drop them; the new mode starts
		// empty and gets the chat as the same handoff notice a program switch gets.
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

		modeChangedThisTurn := false
		modeChangePrevMode := ""
		modeChangeConversationPath := ""
		// Snapshot of the pre-mode-change history. When the user toggles mode
		// mid-session the agent starts empty and gets the chat as a handoff notice
		// (so stale tool calls/prompts do not replay into the new mode), but the on-disk record keeps the full conversation.
		// This snapshot is merged with the new turn's exchange at save time below.
		var preModeChangeSnapshot []llmtypes.MessageContent
		preModeChangeBase := 0
		if newWorkshopMode != "" {
			activeForPolicy, _ := api.getActiveSession(sessionID)
			// After a restart an auto-notification has no remembered role: take it from the saved runtime.
			if _, known := chatPolicyRoleSnapshotFor(sessionID); !known && common.IsCLIProvider(finalProvider) && (req.IsAutoNotification || strings.EqualFold(strings.TrimSpace(req.TriggeredBy), "auto_notification")) {
				if saved, _, err := ReadChatHistoryRuntimeForSession(currentUserID, sessionID, ""); err == nil && saved != nil {
					seedChatPolicyRole(sessionID, saved.ChatPolicyRoleOrigin, saved.ChatPolicyRoleAuthority)
				}
			}
			currentPolicy := resolveWorkflowChatPolicy(sessionID, req, activeForPolicy, currentUserIsReadOnly)
			policyWorkflowKind := ""

			policyKey := api.chatPolicySessionKey(currentPolicy, policyWorkflowKind)
			policyRoleKey := currentPolicy.sessionKey()
			codingProvider := common.IsCLIProvider(finalProvider)
			api.conversationMux.RLock()
			_, knownInMemory := api.lastChatPolicyBySession[sessionID]
			api.conversationMux.RUnlock()
			var savedRuntime *ChatHistoryAgentRuntime
			if codingProvider && !knownInMemory {
				var readErr error
				savedRuntime, _, readErr = ReadChatHistoryRuntimeForSession(currentUserID, sessionID, "")
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
			cause := []string{}
			if roleChanged {
				cause = append(cause, "role")
			}
			toolsWas, toolsNow := "", ""
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
					cause = append(cause, "tools")
					toolsWas, toolsNow = normalizeAgentToolsMode(previousTools), currentTools
				}
			}
			definitionChanged := !codingProviderReloadsInstructionsOnResume(finalProvider) && (savedRuntime == nil || !savedRuntime.NativeHandoff) && chatPolicyRequiresReconnect(codingProvider, previousPolicy, policyKey, knownPolicy, savedRuntime)
			currentDescription := currentPolicy.describe()
			previousDescription := "unknown"
			if was, ok := lastChatPolicyDescription.Load(sessionID); ok {
				previousDescription, _ = was.(string)
			}
			lastChatPolicyDescription.Store(sessionID, currentDescription)
			if definitionChanged {
				cause = append(cause, "definition")
			}
			if hadPrev && prevMode != "" && prevMode != newWorkshopMode {
				cause = append(cause, "mode")
			}
			if roleChanged || definitionChanged || hadPrev && prevMode != "" && prevMode != newWorkshopMode {
				modeChangedThisTurn = true
				modeChangePrevMode = prevMode
				log.Printf("[CHAT_POLICY] Policy refresh for session %s (mode %q -> %q); starting a fresh native coding-agent session with recent conversation context cause=%s role_known=%t policy_was={%s} policy_now={%s} tools_was=%q tools_now=%q triggered_by=%q profile=%q", sessionID, prevMode, newWorkshopMode, strings.Join(cause, "+"), knownRole, previousDescription, currentDescription, toolsWas, toolsNow, req.TriggeredBy, req.AgentProfileID)
				// Snapshot existing history before replacing — the on-disk persisted
				// record reuses this so the user sees a complete conversation log.
				if existing, ok := api.conversationHistory[sessionID]; ok && len(existing) > 0 {
					preModeChangeSnapshot = make([]llmtypes.MessageContent, len(existing))
					copy(preModeChangeSnapshot, existing)
					if source := api.resumeHistorySources[sessionID]; source != nil {
						preModeChangeBase = source.target.Base
					}
					delete(api.conversationHistory, sessionID)
					delete(api.resumeHistorySources, sessionID)
				} else if req.resolvedResumeTarget != nil && len(req.resolvedResumeTarget.History) > 0 {
					preModeChangeSnapshot = make([]llmtypes.MessageContent, len(req.resolvedResumeTarget.History))
					copy(preModeChangeSnapshot, req.resolvedResumeTarget.History)
					preModeChangeBase = req.resolvedResumeTarget.Base
				}
			}
			api.lastWorkshopModeBySession[sessionID] = newWorkshopMode
			api.conversationMux.Unlock()
		}
		if modeChangedThisTurn {
			// The new mode gets the chat as a handoff notice with this turn (below), read from the saved history.
			if path, ok, err := api.liveConversationPath(currentUserID, sessionID, productConversationRuntimeWorkspace(currentUserID, req.SelectedFolder)); err == nil && ok {
				modeChangeConversationPath = path
			}

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
		historyRestoredThisTurn := false
		if !modeChangedThisTurn {
			if target := req.resolvedResumeTarget; target != nil {
				historyRestoredThisTurn = api.restoreResolvedResumeTarget(sessionID, target, newWorkshopMode)
			} else {
				endSpeedSpan7527 := events.TurnSpeedSpan(sessionID, "history_restore")
				historyRestoredThisTurn = api.restorePersistedConversationHistory(
					sessionID,
					currentUserID,
					"",
					newWorkshopMode,
				)
				endSpeedSpan7527()
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
		// The recent dialogue a restarted coding CLI is seeded with (PLAT-700).
		// After a policy refresh historyForAgent holds only the handoff, so use
		// the pre-refresh conversation.
		continuityHistory := historyForAgent
		handoffBudget := continuityBudget(finalProvider, finalModelID, req.Query)
		continuityBase := api.resumeWindowBase(sessionID)
		if modeChangedThisTurn && len(preModeChangeSnapshot) > 0 {
			continuityHistory = preModeChangeSnapshot
			continuityBase = preModeChangeBase
		}
		// Memory may hold only the chat's recent window (#4080). A handover
		// (switch back, another agent, a fresh session, a mode change) quotes
		// saved-history line numbers, so it reads the whole chat, once, here.
		var fullContinuity []llmtypes.MessageContent
		continuityFor := func() []llmtypes.MessageContent {
			if continuityBase == 0 {
				return continuityHistory
			}
			if fullContinuity == nil {
				fullContinuity = continuityHistory
				historySession, workspace := sessionID, req.SelectedFolder
				if target := req.resolvedResumeTarget; target != nil {
					historySession, workspace = target.SessionID, target.WorkspacePath
				}
				started := time.Now()
				full, ok, err := readRestoredChatHistoryPersistTargetForSession(currentUserID, historySession, productConversationRuntimeWorkspace(currentUserID, normalizeConversationWorkspace(workspace)))
				if err == nil && ok && full != nil && len(full.History) >= len(continuityHistory) {
					fullContinuity = full.History
				}
				logfWithContext(queryLogCtx, "[CHAT_RESUME_LOAD] session=%s reason=handover elapsed_ms=%d messages=%d window_base=%d error=%t", sessionID, time.Since(started).Milliseconds(), len(fullContinuity), continuityBase, err != nil)
			}
			return fullContinuity
		}

		// Note: User message is added by StreamWithEvents internally, no need to add it here

		log.Printf("[AGENT DEBUG] Starting agent processing for query %s", queryID)

		// Create a cancellable context for agent execution using background context
		// This prevents the agent from being canceled when the HTTP request ends
		agentCtx, cancelTurn := context.WithCancelCause(streamCtx)
		agentCancel = func() { cancelTurn(context.Canceled) }
		api.shutdownTurnCancels.Store(sessionID, cancelTurn)
		defer api.shutdownTurnCancels.Delete(sessionID)
		stopIdleWatch := api.watchTurnInactivity(agentCtx, sessionID, queryID, time.Now(), nil, func(reason error) bool {
			if releaseAnsweredCodingTurn(sessionID) {
				return true
			}
			api.keepStreamedAnswerOnIdle(sessionID, finalProvider, req.Query)
			cancelTurn(reason)
			return true
		})
		defer stopIdleWatch()
		agentCtx = queryLogCtx.Context(agentCtx)
		agentCtx = withConversationTurnExecutionID(agentCtx, queryID)
		agentCtx = api.withNextPrompt(agentCtx, sessionID, finalProvider, queryID, attendedTurn, costObs)

		// Inject user ID into the agent context
		agentCtx = context.WithValue(agentCtx, common.UserIDKey, currentUserID)
		agentCtx = costobserver.ContextWithSourcePlatform(agentCtx, req.BotPlatform)
		agentCtx = costobserver.ContextWithAttribution(agentCtx, turnCostAttribution)
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
		if api.takePendingTurnStop(sessionID) {
			log.Printf("[SESSION STOP] applying Stop pressed while the turn was starting session=%s", sessionID)
			agentCancel()
		}

		// Merge global secrets with user-supplied secrets, then inject into system prompt (not user message)
		chatQuery := req.Query
		if llmtypes.NativePlanModeForSession(r.Context(), sessionID) {
			chatQuery = nativePlanPrompt(chatQuery)
		}

		endSpeedSpan7603 := events.TurnSpeedSpan(sessionID, "secrets")
		allChatSecrets := api.mergeGlobalSecretsFor(context.Background(), api.turnSecretsOwner(sessionID, currentUserID, req.SelectedFolder), req.SelectedGlobalSecrets)
		allChatSecrets = api.withOwnChatKeys(context.Background(), currentUserID, req.SelectedFolder, personTypedChatTurn(r.Context(), req, sessionID, crewAskOnly), allChatSecrets)
		endSpeedSpan7603()
		{
			endSpeedSpan7605 := events.TurnSpeedSpan(sessionID, "server_secrets")
			allChatSecrets = api.addServerVaultSecrets(serverVaultRound, currentUserID, req.SelectedFolder, allChatSecrets, req.SelectedGlobalSecrets)
			endSpeedSpan7605()
		}
		if len(allChatSecrets) > 0 {
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
		// switchBackRuntime is an earlier agent's own session resumed because
		// the chat switched back to it (#3140).
		var switchBackRuntime *ChatHistoryAgentRuntime
		codingFallbackConversationPath := ""
		codingFallbackWorkspace := ""
		codingFallbackHistoryBase := -1
		codingFallbackInjectedMessages := 0
		if underlyingAgent := llmAgent.GetUnderlyingAgent(); underlyingAgent != nil {
			restoredNativeCodingResume := false
			restoredConversationPath := strings.TrimSpace(req.RestoredConversationPath)
			restoredConversationSessionID := strings.TrimSpace(req.RestoredConversationSessionID)
			// Product-owned chats locate native resume handles in their trusted runtime workspace.
			restoredConversationWorkspace := productConversationRuntimeWorkspace(currentUserID, req.SelectedFolder)
			restoredConversationPathForFallback := restoredConversationPath
			var restoredRuntime *ChatHistoryAgentRuntime
			if modeChangedThisTurn {
				// A role change (Builder <-> Run) must never seed the previous native session handle.
			} else if target := req.resolvedResumeTarget; target != nil {
				restoredConversationPathForFallback = target.ConversationPath
				restoredRuntime = target.Runtime
				endSpeedSpan7641 := events.TurnSpeedSpan(sessionID, "slot_transcript")
				api.carryClaudeTranscriptIntoSlot(currentUserID, restoredRuntime, underlyingAgent)
				endSpeedSpan7641()
				restoredNativeCodingResume = api.seedCodingAgentRuntimeFromRestoredConversation(sessionID, finalProvider, newWorkshopMode, restoredRuntime, underlyingAgent)
			} else if runtime, ok, err := ReadChatHistoryRuntimeFromPath(currentUserID, restoredConversationPath); err != nil {
				logfWithContext(queryLogCtx, "[CHAT_HISTORY] Failed to read restored runtime from %s: %v", restoredConversationPath, err)
			} else if ok {
				restoredRuntime = pickSavedRuntime(runtime, finalProvider)
				endSpeedSpan7647 := events.TurnSpeedSpan(sessionID, "slot_transcript")
				api.carryClaudeTranscriptIntoSlot(currentUserID, restoredRuntime, underlyingAgent)
				endSpeedSpan7647()
				restoredNativeCodingResume = api.seedCodingAgentRuntimeFromRestoredConversation(sessionID, finalProvider, newWorkshopMode, restoredRuntime, underlyingAgent)
			}
			if !modeChangedThisTurn && req.resolvedResumeTarget == nil && !restoredNativeCodingResume && restoredConversationPath == "" && restoredConversationSessionID != "" {
				if runtime, ok, err := ReadChatHistoryRuntimeForSession(currentUserID, restoredConversationSessionID, restoredConversationWorkspace); err != nil {
					logfWithContext(queryLogCtx, "[CHAT_HISTORY] Failed to read restored runtime for session %s: %v", restoredConversationSessionID, err)
				} else if ok {
					restoredRuntime = pickSavedRuntime(runtime, finalProvider)
					endSpeedSpan7655 := events.TurnSpeedSpan(sessionID, "slot_transcript")
					api.carryClaudeTranscriptIntoSlot(currentUserID, restoredRuntime, underlyingAgent)
					endSpeedSpan7655()
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
					if currentRuntime != nil && currentRuntime.switchedBackFrom != "" {
						switchBackRuntime = currentRuntime
					}
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
					// agent's next item was already in this same handleQuery
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
			if restoredNativeCodingResume {
				// The transcript may sit under another runtime folder of this Crew (a turn in the other mode wrote it).
				carryClaudeTranscriptBetweenRuntimes(underlyingAgent)
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
						// observed on an server A Latency cron run as two consecutive ~32-minute
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
				chatQuery = cleanChatHistoryQuery(chatQuery)
				if switchBackRuntime == nil && restoredRuntime != nil && restoredRuntime.switchedBackFrom != "" {
					switchBackRuntime = restoredRuntime
				}
				if switchBackRuntime != nil {
					archivePath := restoredConversationPathForFallback
					if archivePath == "" {
						if path, ok, err := FindChatHistoryConversationPathForSession(currentUserID, sessionID, restoredConversationWorkspace); err == nil && ok {
							archivePath = path
						}
					}
					rememberContinuityArchive(sessionID, archivePath)
					chatQuery = prependSwitchBackNotice(queryLogCtx, switchBackRuntime.switchedBackFrom, switchBackRuntime, chatQuery, archivePath, continuityFor(), handoffBudget)
				}
				// Owner 2 Nov (replaces 27 Oct): a resumed native thread already holds the conversation, and changed
				// instructions reach it through the current-instructions block, so it gets no continuity notice. On two
				// servers 70-85% of notices went to chats whose native resume had worked. The notice stays for a fresh
				// native session and a provider switch below.
				if historyRestoredThisTurn || productDefinitionRefreshed {
					logfWithContext(queryLogCtx, "[CHAT_HISTORY] Native resume retained; no continuity notice (restored=%t definition_changed=%t)", historyRestoredThisTurn, productDefinitionRefreshed)
				}
			} else if req.resolvedResumeTarget != nil && req.resolvedResumeTarget.crossesProvider(finalProvider) && restoredConversationPathForFallback != "" {
				// Provider-native handles cannot cross CLI implementations. Keep the
				// AgentWorks conversation stable and make the newly selected coding
				// provider read the same trusted, project-owned archive used for a
				// same-provider profile reconnect.
				chatQuery = handOffToCodingAgent(&agentCtx, queryLogCtx,
					fmt.Sprintf("agent_switch from=%s to=%s", req.resolvedResumeTarget.provider(), strings.ToLower(strings.TrimSpace(finalProvider))),
					finalProvider, sessionID, chatQuery, restoredConversationPathForFallback, continuityFor(), handoffBudget)
				replayHistoryToAgent = false
				logfWithContext(queryLogCtx, "[CHAT_HISTORY] Resuming across providers saved=%s current=%s with required complete conversation archive %s", req.resolvedResumeTarget.provider(), strings.ToLower(strings.TrimSpace(finalProvider)), restoredConversationPathForFallback)
			} else if restoredConversationPathForFallback != "" && isCodingAgentProvider(finalProvider, finalModelID) && (historyRestoredThisTurn || productDefinitionRefreshed) {
				// A new native session on the same provider (Muse after a definition change, a changed CLI runtime
				// folder) gets what a provider switch gets: the recent turns and the conversation archive. The bounded
				// replayed tail alone lost everything older (PLAT-872-80).
				chatQuery = handOffToCodingAgent(&agentCtx, queryLogCtx, "fresh_session", finalProvider, sessionID, chatQuery, restoredConversationPathForFallback, continuityFor(), handoffBudget)
				replayHistoryToAgent = false
				logfWithContext(queryLogCtx, "[CHAT_HISTORY] Fresh native session; supplied conversation archive %s", restoredConversationPathForFallback)
			} else if restoredConversationPathForFallback != "" {
				// A conversation JSON is UI state. Never attach it as an ad-hoc
				// prompt file: native resume is preferred, and an unavailable native
				// resume falls back to the bounded in-memory tail below.
				chatQuery = cleanChatHistoryQuery(chatQuery)
				logfWithContext(queryLogCtx, "[CHAT_HISTORY] Native resume unavailable; refusing unbounded conversation-file prompt fallback")
			}

			if isCodingAgentProvider(finalProvider, finalModelID) {
				// A pane launched with an older definition does not own the context after a definition change; a
				// seeded native resume does. Only a pane or handle of THIS turn's provider owns anything.
				nativeHandle := codingAgentHasNativeResume(finalProvider, underlyingAgent)
				livePane := !productDefinitionRefreshed && api.sessionHasLiveMainCodingTmuxOf(sessionID, finalProvider)
				if restoredNativeCodingResume || nativeHandle || livePane {
					replayHistoryToAgent = false
					logfWithContext(queryLogCtx, "[CONVERSATION] Coding-agent context is owned by its transport/native continuation; UI history will not be replayed (restored_native=%t native_handle=%t live_pane=%t)", restoredNativeCodingResume, nativeHandle, livePane)
				}
			}
			codingFallbackConversationPath = restoredConversationPathForFallback
			codingFallbackWorkspace = restoredConversationWorkspace
		}

		if idleRun != nil {
			// Idle compaction (PLAT-872): the agent is built as for the person's
			// next message; only the CLI's own compaction runs.
			api.compactIdleTurn(agentCtx, sessionID, idleRun, llmAgent.GetUnderlyingAgent())
			api.runningAgentsMux.Lock()
			if api.runningAgents[sessionID] == registeredRunningAgent {
				delete(api.runningAgents, sessionID)
			}
			api.runningAgentsMux.Unlock()
			_ = llmAgent.Close()
			turnStatus, turnError = trackedExecutionStatusCompleted, ""
			api.updateSessionStatus(sessionID, "completed")
			return
		}

		// A coding agent that does not own the context reads the archive even when
		// nothing is in memory: after a server restart a provider switch found an
		// empty in-memory history and the new CLI started blind (PLAT-872-31).
		codingArchiveOnly := len(historyForAgent) == 0 && codingFallbackConversationPath != "" && isCodingAgentProvider(finalProvider, finalModelID)
		if (len(historyForAgent) > 0 || codingArchiveOnly) && replayHistoryToAgent {
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
					reason := "resume_unavailable to=" + strings.ToLower(strings.TrimSpace(finalProvider))
					if req.providerSwitchedFrom != "" {
						reason = fmt.Sprintf("agent_switch from=%s to=%s", req.providerSwitchedFrom, strings.ToLower(strings.TrimSpace(finalProvider)))
					}
					chatQuery = handOffToCodingAgent(&agentCtx, queryLogCtx,
						reason,
						finalProvider, sessionID,
						chatQuery,
						codingFallbackConversationPath,
						continuityFor(), handoffBudget,
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
			if req.providerSwitchedFrom != "" {
				logfWithContext(queryLogCtx, "[CONVERSATION] provider_switch from=%s to=%s seeded_msgs=%d archive=%t", req.providerSwitchedFrom, strings.ToLower(strings.TrimSpace(finalProvider)), len(historyForAgent), codingFallbackConversationPath != "")
			}
			for _, msg := range historyToReplay {
				llmAgent.AppendMessage(msg)
				if isCodingAgentProvider(finalProvider, finalModelID) {
					codingFallbackInjectedMessages++
				}
			}
		}

		if modeChangedThisTurn {
			chatQuery = handOffToCodingAgent(&agentCtx, queryLogCtx, "mode_change", finalProvider, sessionID, chatQuery, modeChangeConversationPath, continuityFor(), handoffBudget)
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
		chatQuery = withSessionMode(agentSessionModeForTurn(req, currentUserID, sessionID, resolvedProfile, currentUserIsReadOnly), chatQuery)
		clearDeliveredSender = api.expectDeliveredAgentMessage(r.Context(), sessionID, chatQuery)
		agentCtx = automaticContinuePresentation(agentCtx, req, chatQuery)
		textChan, err := llmAgent.StreamWithEvents(agentCtx, chatQuery)
		if err != nil {
			logfWithContext(queryLogCtx, "[AGENT DEBUG] llmAgent.StreamWithEvents() error: %v", err)
			sendError(fmt.Sprintf("Failed to start streaming: %v", err), true)
			return
		}
		events.TurnSpeedStep(sessionID, "history_stream")
		logfWithContext(queryLogCtx, "[LATENCY_DEBUG] T+%dms | StreamWithEvents channel opened | queryID=%s", time.Since(startTime).Milliseconds(), queryID)
		log.Printf("[AGENT DEBUG] llmAgent.StreamWithEvents() started successfully for query %s", queryID)

		// Stream response chunks with enhanced error handling
		chunkCount := 0
		streamWaitStartedAt := time.Now()

		log.Printf("[AGENT DEBUG] Entering streaming loop for query %s", queryID)
		log.Printf("[COMPLETION_TRACE] stage=agentworks_stream_wait_started session=%q query=%q", sessionID, queryID)
		_, _ = drainTurnTextStream(agentCtx, textChan, func(chunk string) {
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
			api.replaceConversationHistoryLocked(sessionID, incrementalHistory)
			api.conversationMux.Unlock()

		})
		if agentCtx.Err() != nil {
			turnStatus = trackedExecutionStatusCanceled
			turnError = context.Cause(agentCtx).Error()
			if errors.Is(context.Cause(agentCtx), errTurnInactive) {
				turnStatus = trackedExecutionStatusFailed
				api.updateSessionStatus(sessionID, "error")
				api.emitIdleTurnFailure(sessionID, queryID)
			} else if errors.Is(context.Cause(agentCtx), errAppRestart) && api.eventStore != nil &&
				(api.eventStore.TurnInProgress(sessionID) || api.storedRestartInterruption(sessionID)) {
				api.emitInterruptedTurnEnd(sessionID, "app_restart")
			} else if api.eventStore != nil && api.eventStore.TurnInProgress(sessionID) {
				// Prefer the SDK's own terminal event. If cancellation released
				// the consumer first, record an interrupted end so the UI settles.
				api.emitInterruptedTurnEnd(sessionID, "turn_context_canceled")
			}
			discardAbandonedSyntheticStream(sessionID, textChan)
		}

		log.Printf("[COMPLETION_TRACE] stage=agentworks_stream_closed session=%q query=%q chunks=%d elapsed=%s", sessionID, queryID, chunkCount, time.Since(streamWaitStartedAt).Round(time.Millisecond))
		log.Printf("[STREAMING_LIFECYCLE] StreamWithEvents completed | session=%s chunks=%d duration=%dms", sessionID, chunkCount, time.Since(startTime).Milliseconds())
		log.Printf("[AGENT DEBUG] After streaming loop, streamCtx.Err(): %v", streamCtx.Err())
		if agentCtx.Err() == nil {
			// The reply is complete: the changed-files card need not wait for
			// the transcript save below.
			api.markCrewTurnOutputDone(queryID)
		}

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
		// Every answer Claude finished inside this turn, also when the turn was
		// released or cancelled before its final reply (PLAT-872).
		finalHistory = api.historyWithNativeTurnAnswers(sessionID, finalHistory)
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
		api.replaceConversationHistoryLocked(sessionID, persistedHistory)
		api.conversationMux.Unlock()

		runtimeWorkspacePath := strings.TrimSpace(req.SelectedFolder)

		var chatRuntime *ChatHistoryAgentRuntime
		if underlyingAgent := llmAgent.GetUnderlyingAgent(); underlyingAgent != nil {
			chatRuntime = api.captureChatHistoryAgentRuntime(sessionID, finalProvider, finalModelID, runtimeWorkspacePath, underlyingAgent)
			if chatRuntime != nil {
				chatRuntime.WorkshopMode = newWorkshopMode
				api.carryAgentSessionsIntoSave(currentUserID, sessionID, productConversationRuntimeWorkspace(currentUserID, req.SelectedFolder), chatRuntime, persistedHistory, continuityBase)
			}
			idleTurnRuntime = chatRuntime
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
				normalizeConversationWorkspace(req.SelectedFolder),
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
			// One file per chat (PLAT-872): a chat already saved in this project keeps its file. Only today's date
			// was used before, so each day copied the whole chat into a new day folder (one chat had 6 copies), and
			// a copy seeded from the wrong day lost messages (872-77).
			if persistConversationPath == "" {
				if existing, found := newestProjectConversationFile(workConversationPath); found {
					persistConversationPath = existing
				}
			}
			if sameProjectConversationFile(persistConversationPath, workConversationPath) {
				workConversationPath = strings.Trim(strings.TrimSpace(persistConversationPath), "/")
			}
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
		{
			if err := checkTokenTurn(context.WithoutCancel(agentCtx)); err != nil {
				turnStatus, turnError = trackedExecutionStatusCanceled, err.Error()
				return
			}
			api.persistChatConversationToPathWithTerminalSession(persistSessionID, sessionID, req.AgentMode, currentUserID, persistedHistoryForDisk, chatRuntime, uiEvents, persistConversationPath, botHistoryMeta)
			if req.AgentProfileID == crewProfileID {
				api.queueCrewOfflineAppTurn(currentUserID, persistSessionID, runtimeWorkspacePath)
			}
			if isCrewReaderTurn(req, currentUserID) {
				mirrorCrewReaderConversation(currentUserID, req.SelectedFolder, persistSessionID, persistConversationPath, time.Now())
			}
		}

		// Store resolved "" so synthetic turns can persist builder conversations

		// Save builder conversation log + token_usage.json for workflow phase sessions.
		// One file per session — overwrites on each follow-up with the full cumulative history.
		// Resolve workspace-docs root so files are visible in the UI.

		// Store agent for reuse by synthetic turns (multi-agent chat and workflow phase chat).
		// The stored agent retains all tools, prompts, observers, and conversation history.
		{
			api.storeSessionAgent(sessionID, llmAgent)
			log.Printf("[BG AGENT] Stored agent for session %s for synthetic turn reuse", sessionID)
		}

		// Cancellation also runs the normal removal/history/runtime capture above.
		// It must not be overwritten as a successful turn after persistence.
		if agentCtx.Err() != nil {
			turnStatus = trackedExecutionStatusCanceled
			turnError = context.Cause(agentCtx).Error()
			if errors.Is(context.Cause(agentCtx), errTurnInactive) {
				turnStatus = trackedExecutionStatusFailed
			} else if !api.isSessionMarkedStopped(sessionID) {
				api.updateSessionStatus(sessionID, "canceled")
			}
			tracer.EndTrace(traceID, map[string]interface{}{"status": turnStatus})
			return
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

		// A turn whose every LLM attempt failed records that only as events and
		// would otherwise read as completed. Use the scheduler's rule so chat tabs
		// can show Failed from the server's own phase (PLAT-746-158).
		if failure := scheduledTurnFailure(api.eventStore, sessionID, streamWaitStartedAt); failure != "" {
			turnStatus = trackedExecutionStatusFailed
			turnError = failure
			log.Printf("[COMPLETION] Turn produced no response; marking session %s failed", sessionID)
			api.updateSessionStatus(sessionID, "error")
			tracer.EndTrace(traceID, map[string]interface{}{"status": "error"})
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
	}(chatReq)
}

func (api *StreamingAPI) verifySessionAccess(r *http.Request, sessionID string) error {
	if !api.crewSessionCanControl(r, sessionID) {
		return fmt.Errorf("Only this tab's owner can control it")
	}
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

// truncateForLog truncates a string for logging purposes
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func recoverDetachedGoroutine(name, scope string) {
	if r := recover(); r != nil {
		log.Printf("[PANIC_RECOVER] goroutine=%s scope=%s panic=%v\n%s", name, scope, r, debug.Stack())
	}
}

func goSafe(name string, fn func()) {
	go func() {
		defer recoverDetachedGoroutine(name, "")
		fn()
	}()
}

// logCustomToolDuration logs when a custom tool call (the shell, send_message and the like) finishes and how long it
// took, so a call that never returns shows as a START with no END (a hung shell showed only its start before).
func logCustomToolDuration(tool, sessionID string, run func()) {
	started := time.Now()
	defer func() {
		log.Printf("[CUSTOM_TOOL] END tool=%s session=%s duration=%s", tool, sessionID, time.Since(started).Round(time.Millisecond))
	}()
	run()
}

// A correlation label, never an authority or an unbounded log payload.
func clientSwitchID(r *http.Request) string {
	value := r.Header.Get("X-Client-Switch")
	if len(value) > 96 {
		return ""
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == ':' || c == '.') {
			return ""
		}
	}
	return value
}
