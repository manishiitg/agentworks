package server

import (
	"context"
	"encoding/json"
	"log"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// QueryRequest represents an agent query request
type QueryRequest struct {
	RequestedSkillID    string                   `json:"requested_skill_id,omitempty"`
	requestedSkillName  string                   // server-resolved display only; never caller authority
	admittedAttachments []admittedChatAttachment // server-only; never request JSON
	PendingAttachments  []pendingChatAttachment  `json:"pending_attachments,omitempty"`
	// DeliveryMode is a person’s choice for this message; empty preserves steering.
	DeliveryMode string `json:"delivery_mode,omitempty"`
	// secretsWorkspacePath is the workflow/project whose selected secrets this turn loaded (PLAT-658): a retained CLI
	// launched with them is not steered once they change.
	secretsWorkspacePath string
	ConnectionID         string   `json:"connection_id,omitempty"`
	Query                string   `json:"query"`
	Message              string   `json:"message,omitempty"`           // Alias for Query (used by frontend)
	SessionTitle         string   `json:"session_title,omitempty"`     // Short UI label for backend-started sessions; never use the full prompt here.
	ParentSessionID      string   `json:"parent_session_id,omitempty"` // Internal child-session ownership used by refresh recovery.
	SessionKind          string   `json:"session_kind,omitempty"`      // Stable runtime kind such as generic_agent; never infer this from titles.
	Servers              []string `json:"servers,omitempty"`
	EnabledServers       []string `json:"enabled_servers,omitempty"`
	SelectedTools        []string `json:"selected_tools,omitempty"` // Array of "server:tool" strings
	Provider             string   `json:"provider,omitempty"`
	ModelID              string   `json:"model_id,omitempty"`
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
	// Code source selection is a hint, never file authority.
	LaptopConnection *laptopConnectionTarget `json:"laptop_connection,omitempty"`
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
	// CrewAskReadOnly marks a guest's free-text `ask` to a Crew: the turn only reads (files, memory, skills, functions,
	// MCPs); typed functions are how a guest acts (856-59, owner decision 2026-10-12). It only narrows access.
	CrewAskReadOnly bool `json:"crew_ask_read_only,omitempty"`
	// SharedAgentSender is the person whose message this turn answers in the shared agent's own chat (#3469; owner,
	// 3 Nov: a shared agent runs as its owner set it up). The turn keeps the agent's own tools and the owner's secrets
	// (4 Nov), but none of the owner-only tools (sharing, settings, access, the Crew itself). It is always queued,
	// never live input into a running owner turn. It only narrows.
	SharedAgentSender string `json:"shared_agent_sender,omitempty"`
	// Execution options from frontend (for workflow execution phase)
	ExecutionOptions *ExecutionOptions `json:"execution_options,omitempty"`
	// Workspace access configuration (legacy field, ignored — workspace is always enabled)
	EnableWorkspaceAccess *bool `json:"enable_workspace_access,omitempty"`
	// Browser automation access configuration
	EnableBrowserAccess *bool `json:"enable_browser_access,omitempty"` // Enable/disable browser automation tool (nil = inherit default, true/false = explicit override)
	// Explicit browser choice: none|extension|headless|cdp
	BrowserMode string `json:"browser_mode,omitempty"`
	// Caller input survives in-memory continuation copies, never client JSON.
	callerBrowserInput *queuedBrowserInput `json:"-"`
	// Captured from an authenticated HTTP request, never accepted in client JSON.
	oauthRedirectURI string `json:"-"`
	// CDP port for connecting to an existing Chrome browser (local mode only)
	CdpPort *int `json:"cdp_port,omitempty"` // When set and > 0, connect to Chrome via CDP on this port instead of launching headless
	// Browser candidates resolved by the backend, never a request field.
	resolvedCDPPorts []int
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
	// Selected global secret names to include (if nil/absent, no global secrets are included)
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
	// omittedWorkflowContextCount is how many saved attachments turn admission
	// left out (deleted, or not readable by the sender). Count only: the agent
	// is told something is missing, never what.
	omittedWorkflowContextCount int `json:"-"`
	// Conversation JSON selected from /resume or previous chats. Used to seed
	// native coding-agent resume state from its saved runtime metadata.
	RestoredConversationPath string `json:"restored_conversation_path,omitempty"`
	// Previous persisted chat session to resume from when callers do not know
	// the conversation JSON path (for example Slack/WhatsApp bot channels).
	RestoredConversationSessionID string `json:"restored_conversation_session_id,omitempty"`
	// resolvedResumeTarget is attached only by a trusted server-owned product
	// route. It is never accepted from or serialized back to a browser.
	resolvedResumeTarget *resolvedResumeTarget `json:"-"`
	// sharedAsk is another person's message to a shared Mac agent; it runs in the agent's one chat (#3469).
	sharedAsk *crewMacSharedAsk `json:"-"`
	// appAgentReplyTo is stamped by authenticated inbox delivery, never request JSON.
	appAgentReplyTo string `json:"-"`
	// providerSwitchedFrom is the coding provider of the retained CLI this turn replaced (the person changed the agent
	// mid-chat); empty otherwise. It only feeds the log line that proves the conversation was carried over.
	providerSwitchedFrom string `json:"-"`

	// Workflow phase chat: phase ID for running a phase as a conversational chat session
	// When agent_mode is "workflow_phase", this specifies which phase to run (e.g., "planning", "plan-improvement")
	PhaseID string `json:"phase_id,omitempty"`

	// Workspace path passed directly (used by scheduler to bypass preset lookup)
	SelectedFolder string `json:"selected_folder,omitempty"`
	// Set only after target admission, or by an internal scheduled run.
	notificationItemWorkspace         string
	notificationMailConnectionID      string
	notificationMailBlockedRecipients []string

	// Triggered by: "manual", "cron" — for tracking execution source
	TriggeredBy string `json:"triggered_by,omitempty"`
	// TriggeredByLabel names who or what started the run, for display only
	// ("Called by server A Flow Tester", "Schedule: Daily digest").
	TriggeredByLabel string `json:"triggered_by_label,omitempty"`
	// CostSource* name the product schedule, reminder or trigger that sent
	// this turn (job id, display name, automation run id). Set only by the
	// product schedule runner; the turn and its sub-agents are then recorded
	// under cost scope "schedule" instead of chat (PLAT-702).
	CostSourceID    string `json:"cost_source_id,omitempty"`
	CostSourceLabel string `json:"cost_source_label,omitempty"`
	CostSourceRunID string `json:"cost_source_run_id,omitempty"`
	// Auto-notification flag: when true, this is a background agent completion notification
	// (not user-initiated). Backend treats it as a synthetic turn so frontend doesn't block input.
	IsAutoNotification bool `json:"is_auto_notification,omitempty"`
	// AgentMessageInbox marks another agent's message delivered into this chat (agent_messaging.go). It survives
	// the turn queue, and makes the turn attended: the tab shows question cards (codingAgentTurnAttended).
	AgentMessageInbox string `json:"agent_message_inbox,omitempty"`
	// AgentMessageID is the delivered message this turn answers; the asker's Stop finds its queued turn by it (#3661).
	AgentMessageID string `json:"agent_message_id,omitempty"`
	// AgentMessageFromOtherPerson marks an agent message whose sender belongs to another person: that turn works for
	// them, so it never gets the receiving person's laptop.
	AgentMessageFromOtherPerson bool `json:"agent_message_from_other_person,omitempty"`
	// AskedBy is the person behind another person's agent message (set by agent_messaging.go). It only adds the
	// "When others ask" note to the turn (askerIsNotAgentOwner), and locks the project's setup when that person is one of
	// its Users (crewTurnSetupLockUser); it can only narrow access, never widen it.
	AskedBy string `json:"asked_by,omitempty"`
	// Internal/system callers can force a real server-tracked turn instead of the
	// retained coding-CLI live-input shortcut. Scheduler sequences rely on this so
	// the next cron message waits for turn completion instead of racing a tmux
	// snapshot that may not have flipped to busy yet.
	DisableLiveInputDelivery bool `json:"disable_live_input_delivery,omitempty"`
	// PlanMode: the person's Plan switch was on when they sent this message (#3699); the turn runs in the
	// program's own plan mode, like "/plan". Honoured only for a person's own message, never an agent's.
	PlanMode bool `json:"plan_mode,omitempty"`
	// RestartCodingCLI asks for the chat's coding CLI to be closed and relaunched when this
	// message's turn starts: a product chat's runtime selection changed while a turn was running
	// (PLAT-676). It is applied between turns, never mid-turn; the flag survives the turn queue.
	RestartCodingCLI bool `json:"restart_coding_cli,omitempty"`
	// ClaudeCodeTransport is the chat's own choice for Claude Code: "tmux" (the native terminal, driven by the
	// AgentWorks mod) or "sdk" (the chat view). Set by the server from the chat's saved choice; empty uses the
	// server setting.
	ClaudeCodeTransport    string `json:"-"`
	KeepNativeSessionAlive bool   `json:"keep_native_session_alive,omitempty"`
	// UserInteractiveContinuation promotes an observed schedule/bot conversation
	// into an interactive chat without changing its session or native resume ID.
	UserInteractiveContinuation bool `json:"user_interactive_continuation,omitempty"`
	// Internal: user ID for synthetic turn reconstruction (not from JSON)
	userID string `json:"-"`
	// Internal: the owner a Slack channel turn's shared-account tokens count
	// toward (botRouteTokenOwner); sub-agents bill the same person.
	tokenOwner string `json:"-"`
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
	if req.notificationItemWorkspace != "" {
		dest.Gmail = &services.GmailDest{Workspace: req.notificationItemWorkspace, BlockedRecipients: append([]string(nil), req.notificationMailBlockedRecipients...)}
		if req.notificationMailConnectionID != "" {
			dest.Gmail.ConnectionIDs = []string{req.notificationMailConnectionID}
		}
	}
	if dest.UserID == "" && dest.WorkspacePath == "" && dest.Slack == nil && dest.WhatsApp == nil && dest.Gmail == nil {
		return nil
	}
	return dest
}

func applyMultiAgentCapabilitiesToRequest(req *QueryRequest, caps WorkflowCapabilities) {
	if req == nil {
		return
	}

	req.EnabledServers = caps.RuntimeServers()
	req.Servers = nil
	req.SelectedTools = append([]string(nil), caps.SelectedTools...)
	req.SelectedSkills = append([]string(nil), caps.SelectedSkills...)
	req.UseCodeExecutionMode = caps.UseCodeExecutionMode
	req.BrowserMode = strings.ToLower(strings.TrimSpace(caps.BrowserMode))
	if req.BrowserMode == "cdp" && caps.BrowserCDPPort > 0 {
		port := caps.BrowserCDPPort
		req.CdpPort = &port
	}
	if req.BrowserMode == "" {
		req.BrowserMode = browser.DefaultMode()
	}

	if req.BrowserMode == "cdp" && !browser.CDPEnabled() {
		req.BrowserMode = "headless"
	}

	enableBrowser := req.BrowserMode == "extension" || req.BrowserMode == "auto" || req.BrowserMode == "headless" || req.BrowserMode == "cdp"
	req.EnableBrowserAccess = &enableBrowser

	if caps.SelectedGlobalSecretNames != nil {
		copied := append([]string(nil), (*caps.SelectedGlobalSecretNames)...)
		req.SelectedGlobalSecrets = &copied
	}
	if cfg := queryLLMConfigFromPreset(lockedPresetLLMConfig(caps.LLMConfig)); cfg != nil {
		req.LLMConfig = cfg
	}
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
	// OmittedContext counts attached workflows or projects left out of this turn because the sender cannot open them.
	OmittedContext int `json:"omitted_context,omitempty"`
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
	return isToolBackedChatMode(mode)
}

func shouldTryRetainedDeliveryBeforeQueue(ctx context.Context, req QueryRequest, sessionID string) bool {
	trigger := strings.ToLower(strings.TrimSpace(req.TriggeredBy))
	claims := GetUserFromContext(ctx)
	// PLAT-746-973/974: a steer that waited for foreground cleanup still
	// targets the retained CLI. A claimed queue row must not force a new SDK
	// launch (and stop its background tasks) just because it was persisted.
	if (conversationTurnQueueExecution(ctx) && req.DeliveryMode != "steer") || req.DisableLiveInputDelivery || req.DeliveryMode == "queue" ||
		req.IsAutoNotification ||
		(claims != nil && claims.AccessToken != nil) {
		return false
	}
	if isScheduledCrewChatDelivery(ctx) {
		return true
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
		(trigger == "" || trigger == "manual" || trigger == "interactive" || trigger == "workflow_builder" || (trigger == "agent_message" && ownAgentDeliveryForSteering(ctx, req)))
}

func (api *StreamingAPI) queueOccupiedConversationTurn(w http.ResponseWriter, r *http.Request, userID, sessionID string, req QueryRequest) bool {
	if !shouldUseDurableConversationTurnQueue(req) || conversationTurnQueueExecution(r.Context()) ||
		!(api.conversationTurnOccupied(sessionID) || api.conversationTurnHasBacklog(userID, sessionID)) {
		return false
	}
	return api.queueConversationTurnResponse(w, r, userID, sessionID, req)
}

func (api *StreamingAPI) queueConversationTurnResponse(w http.ResponseWriter, r *http.Request, userID, sessionID string, req QueryRequest) bool {
	if isQueuedMessageSteer(r.Context()) {
		rejectUnavailableQueuedSteer(w)
		return true
	}
	// An idle compaction replays the last person request only to build the
	// agent. Queued, the drainer would run that request again as a real turn.
	if idleCompactRunFromContext(r.Context()) != nil {
		http.Error(w, "turn_running", http.StatusConflict)
		return true
	}
	occupant, backlog := api.conversationTurnOccupiedBy(sessionID), api.conversationTurnHasBacklog(userID, sessionID)
	turn, position, err := api.enqueueConversationTurn(r.Context(), userID, sessionID, req)
	if err != nil {
		http.Error(w, "Cannot durably queue conversation turn: "+err.Error(), http.StatusServiceUnavailable)
		return true
	}
	api.pendingMu.RLock()
	pendingResults := len(api.pendingCompletions[sessionID])
	api.pendingMu.RUnlock()
	// Delivery intent is metadata only: distinguish an explicit next-turn send
	// from a steer that could not reach the current transport (PLAT-746).
	queueReason, canSteer := api.queuedMessageDeliveryState(sessionID, req)
	if meta, ok := r.Context().Value(deliveredAgentMessageKey{}).(map[string]interface{}); ok && meta["queue_kind"] == true {
		queueReason, canSteer = "queue_kind", false
	}
	log.Printf("[TURN_QUEUE] message queued session=%s position=%d occupant=%q backlog=%t pending_results=%d busy_by=%q delivery_mode=%q disable_live_input=%t pending_attachments=%d queue_reason=%q can_steer=%t",
		sessionID, position, occupant, backlog, pendingResults, api.autoNotificationBusyReason(sessionID),
		req.DeliveryMode, req.DisableLiveInputDelivery, len(req.PendingAttachments), queueReason, canSteer)
	log.Printf("[QUERY->LIVE] session=%s query=%s action=queued reason=%s delivery_mode=%s backlog=%t occupant=%s", sessionID, turn.ID, queueReason, req.DeliveryMode, backlog, occupant)
	api.recordQueuedConversationUserMessage(sessionID, turn)
	api.kickConversationTurnQueue(sessionID)
	_ = json.NewEncoder(w).Encode(QueryResponse{
		QueryID: turn.ID, SessionID: sessionID, Status: "accepted",
		Message:        "Queued behind the active conversation turn",
		DeliveryStatus: "queued_for_turn", MessageID: turn.ID, QueuePosition: position,
	})
	return true
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
