package services

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/mcpclient"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// newBotSessionID mints a session ID for a bot-initiated chat. Encoding the
// source platform in the ID makes it easy to tell, just from the builder/
// filename, where a conversation originated (slack, discord, telegram, …).
func newBotSessionID(platform string) string {
	p := strings.TrimSpace(platform)
	if p == "" {
		p = "unknown"
	}
	return fmt.Sprintf("bot-%s--%s", p, uuid.New().String())
}

func logBotOutboundMessage(platform string, threadID ThreadID, kind string, message string, parts int, blockCount int) {
	if parts <= 0 {
		parts = 1
	}
	platform = strings.TrimSpace(platform)
	if platform == "" {
		platform = threadID.Platform
	}
	log.Printf("[BOT_SEND] platform=%s kind=%s channel=%s thread=%s parts=%d blocks=%d chars=%d preview=%q",
		platform,
		kind,
		threadID.ChannelID,
		threadID.ThreadTS,
		parts,
		blockCount,
		len(message),
		botMessageLogPreview(message),
	)
}

func botMessageLogPreview(message string) string {
	const maxPreviewRunes = 240
	preview := strings.Join(strings.Fields(message), " ")
	runes := []rune(preview)
	if len(runes) > maxPreviewRunes {
		preview = string(runes[:maxPreviewRunes]) + "..."
	}
	return preview
}

func multiAgentChatLLMConfigForRequest(preset *workflowtypes.PresetLLMConfig) map[string]interface{} {
	if preset == nil {
		return nil
	}
	primary := multiAgentChatPrimaryLLM(preset)
	if primary == nil || strings.TrimSpace(primary.Provider) == "" || strings.TrimSpace(primary.ModelID) == "" {
		return nil
	}
	result := map[string]interface{}{
		"primary": map[string]interface{}{
			"provider": primary.Provider,
			"model_id": primary.ModelID,
		},
	}

	return result
}

func multiAgentChatPrimaryLLM(preset *workflowtypes.PresetLLMConfig) *workflowtypes.AgentLLMConfig {
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

// ChannelRoute maps a bot-visible route key to a specific workflow, including
// the workspace path so the bot can read the workflow manifest without scanning
// all workspaces.
type ChannelRoute struct {
	BlockedEmails []string      `json:"blocked_emails,omitempty"`
	Trigger       *SlackTrigger `json:"trigger,omitempty"`
	WorkflowID    string        `json:"workflow_id,omitempty"`
	WorkspacePath string        `json:"workspace_path"`
	// Product routes share the connector and conversation manager with
	// workflow routes, but enter a keyed product conversation directly.
	ProfileID       string `json:"profile_id,omitempty"`
	ConversationKey string `json:"conversation_key,omitempty"`
	ProfileLabel    string `json:"profile_label,omitempty"`
	WorkspaceUserID string `json:"workspace_user_id,omitempty"`
	// WorkshopMode overrides the bot workflow route mode. Valid: "workshop" | "run".
	// UI/API aliases like "build" normalize to "workshop".
	WorkshopMode    string `json:"workshop_mode,omitempty"`
	BotGrant        string `json:"bot_grant,omitempty"`
	SendFullDetails bool   `json:"send_full_details,omitempty"`
}

// NormalizeBotWorkshopMode canonicalizes bot route mode input. Slack exposes
// this as Run/Build in the UI; the workflow runtime expects run/workshop.
func NormalizeBotWorkshopMode(mode string) string {
	trimmed := strings.ToLower(strings.TrimSpace(mode))
	switch trimmed {
	case "":
		return ""
	case "run", "runner", "ask", "debugger":
		return "run"
	case "build", "builder", "workshop":
		return "workshop"
	default:
		return ""
	}
}

func NormalizeBotRouteGrant(grant, workshopMode string) string {
	switch strings.ToLower(strings.TrimSpace(grant)) {
	case "owner", "workshop", "build", "builder", "write", "admin":
		return "owner"
	case "run", "runner", "read", "reader", "view", "viewer":
		return "run"
	}
	if NormalizeBotWorkshopMode(workshopMode) == "workshop" {
		return "owner"
	}
	return "run"
}

func WorkshopModeForBotGrant(grant string) string {
	if NormalizeBotRouteGrant(grant, "") == "owner" {
		return "workshop"
	}
	return "run"
}

func BotPrincipalIDForRoute(platform string, route ChannelRoute) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(platform)) + "|" +
		strings.ToLower(strings.TrimSpace(route.WorkflowID)) + "|" +
		strings.ToLower(strings.TrimSpace(route.WorkspacePath)) + "|" +
		strings.ToLower(strings.TrimSpace(route.ProfileID)) + "|" +
		strings.ToLower(strings.TrimSpace(route.ConversationKey))))
	return fmt.Sprintf("bot-%s-%x", strings.ToLower(strings.TrimSpace(platform)), sum[:8])
}

// ThreadID identifies a conversation thread on a platform
type ThreadID struct {
	Platform  string // "slack", "discord", "telegram", "whatsapp"
	ChannelID string
	ThreadTS  string // platform-specific thread root ID
	// ConnectionID names the Slack app connection the thread lives on.
	// Empty means the default connection (the root listener).
	ConnectionID string
}

// Key returns a unique string key for this thread. Each Slack app keeps
// its own session in a shared thread, so a non-default connection is part
// of the key; the default connection keeps the legacy three-part key.
func (t ThreadID) Key() string {
	key := fmt.Sprintf("%s:%s:%s", t.Platform, t.ChannelID, t.ThreadTS)
	if connectionID := strings.TrimSpace(t.ConnectionID); connectionID != "" {
		key += "#" + connectionID
	}
	return key
}

// sameThread reports whether two IDs name the same platform thread,
// whichever app they arrived on.
func (t ThreadID) sameThread(other ThreadID) bool {
	return t.Platform == other.Platform && t.ChannelID == other.ChannelID && t.ThreadTS == other.ThreadTS
}

// BotIncomingMessage represents a message received from a platform
type BotIncomingMessage struct {
	ResumeSessionID string // trusted server-selected durable conversation

	Platform        string
	UserID          string
	UserName        string
	UserEmail       string // resolved by platform connector (e.g. Slack users.info)
	WorkspaceUserID string // pre-resolved workspace user ID (set by simulator from HTTP auth)
	ChannelID       string
	// ConnectionID names the Slack app connection the message arrived on.
	// Empty means the default connection. Replies inherit it.
	ConnectionID  string
	ThreadTS      string // empty = new conversation, set = existing thread
	Text          string // @mention stripped
	MessageTS     string // platform timestamp of the incoming message (used to add/remove reactions)
	Timestamp     time.Time
	IsThreadReply bool
	IsMention     bool // true when the bot was @mentioned (vs plain thread reply)
	// senderNoted: withBotSender already put the sender line on Text.
	senderNoted bool
	// DirectMessage: a 1:1 Slack DM, set only by the Slack service after it
	// proved the sender (slack_dm.go) and put their account in
	// WorkspaceUserID. The turn runs as that account, not as the route.
	DirectMessage bool
	// MentionsOtherUser is true when the message tags another platform user
	// but not the bot (Slack <@U...> of someone else). Thread replies with
	// this set are addressed to a colleague: the bot stays silent no matter
	// how many people are in the thread.
	MentionsOtherUser bool
	ThreadHistory     []ThreadMessage // populated when tagged in existing thread
	// PresetWorkflow, when set, overrides channel-based workflow routing
	// for this message. WhatsApp uses it after its @<slug> router has
	// selected a workflow explicitly.
	PresetWorkflow *ChannelRoute
	// PresetProfile, when set, sends this message to one of the default
	// product's own profiles (an @token the product resolved, e.g. a
	// tutor's per-activity conversation) instead of the pairing's default
	// profile. Built into the turn by ProfileTurnFunc.
	PresetProfile *ProfileRoute
}

// BotSessionBinding is the durable pointer from a thread-less platform chat
// to the AgentWorks conversation that chat is currently using. Connectors
// that persist their own platform state (WhatsApp) can implement
// botSessionBindingStore so this pointer survives a server restart.
type BotSessionBinding struct {
	SessionID string
	RouteKey  string
	UpdatedAt time.Time
}

// botSessionBindingStore is optional. A connector that implements it lets the
// conversation manager restore a thread-less chat after process restart while
// keeping the persistence scoped to the connector's own account/device store.
type botSessionBindingStore interface {
	LoadBotSessionBinding(ctx context.Context, threadID ThreadID, routeKey string) (BotSessionBinding, bool, error)
	SaveBotSessionBinding(ctx context.Context, threadID ThreadID, binding BotSessionBinding) error
	ClearBotSessionBinding(ctx context.Context, threadID ThreadID) error
}

// ProfileRoute is a product profile a channel message was routed to by the
// product's own @token: the profile, its conversation key when keyed, where
// attachments go, and a label for the activation acknowledgement.
type ProfileRoute struct {
	ProfileID       string
	ConversationKey string
	UploadFolder    string
	Label           string
	WorkspaceUserID string
}

// botMessageRouteKey identifies the destination a message names, so a
// thread-less platform starts a fresh conversation when it changes: a
// workflow route, a product profile route, or "" for the default chat.
func botMessageRouteKey(msg BotIncomingMessage) string {
	if msg.PresetProfile != nil {
		return "profile|" + strings.ToLower(strings.TrimSpace(msg.PresetProfile.ProfileID)) + "|" + strings.ToLower(strings.TrimSpace(msg.PresetProfile.ConversationKey))
	}
	return botRouteKey(msg.PresetWorkflow)
}

func botMetaFromMsg(msg BotIncomingMessage, threadID ThreadID) *chathistory.BotMetadata {
	return &chathistory.BotMetadata{
		Platform:  msg.Platform,
		ChannelID: msg.ChannelID,
		ThreadTS:  threadID.ThreadTS,
		UserID:    msg.UserID,
		UserName:  msg.UserName,
		UserEmail: msg.UserEmail,
		// Only the Slack service sets DirectMessage, after proving the sender.
		DirectMessage: msg.DirectMessage && msg.Platform == "slack",
	}
}

func applyBotQueryRequestMetadata(req map[string]interface{}, meta *chathistory.BotMetadata) {
	if req == nil || meta == nil {
		return
	}
	if value := strings.TrimSpace(meta.Platform); value != "" {
		req["bot_platform"] = value
		req["triggered_by"] = "bot:" + value
	}
	if value := strings.TrimSpace(meta.ChannelID); value != "" {
		req["bot_channel_id"] = value
	}
	if value := strings.TrimSpace(meta.ThreadTS); value != "" {
		req["bot_thread_ts"] = value
	}
	if value := strings.TrimSpace(meta.UserID); value != "" {
		req["bot_user_id"] = value
	}
	if value := strings.TrimSpace(meta.UserName); value != "" {
		req["bot_user_name"] = value
	}
	if value := strings.TrimSpace(meta.UserEmail); value != "" {
		req["bot_user_email"] = value
	}
	if meta.DirectMessage && meta.Platform == "slack" {
		// Internal-only key: the server's bot context reads it, and the
		// public query contract has no such field.
		req["_trusted_slack_dm"] = true
	}
}

// ThreadMessage represents a single message in a thread's history
type ThreadMessage struct {
	UserID    string    `json:"user_id"`
	UserName  string    `json:"user_name"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
	IsBot     bool      `json:"is_bot"`
	// IsSelf marks messages posted by this bot connection itself (its own
	// replies and status lines). IsBot is also true for other apps' posts
	// (Sentry, GitHub, other bots), which are real thread content.
	IsSelf bool `json:"is_self,omitempty"`
	// TS is the platform's own message id (Slack ts), when it has one.
	TS string `json:"ts,omitempty"`
}

// MessageBlock represents a rich message block (buttons, sections, etc.)
type MessageBlock struct {
	Type    string          `json:"type"` // "section", "actions", "divider"
	Text    string          `json:"text,omitempty"`
	Buttons []MessageButton `json:"buttons,omitempty"`
}

// MessageButton represents an interactive button in a message
type MessageButton struct {
	Text     string `json:"text"`
	Value    string `json:"value"`
	Style    string `json:"style,omitempty"` // "primary", "danger"
	ActionID string `json:"action_id"`
}

// BotMessageHandler is the callback invoked when a bot receives a message
type BotMessageHandler func(msg BotIncomingMessage)

// BotInteractionHandler is the callback invoked when a user clicks a button
// threadID carries the app connection the click arrived on.
type BotInteractionHandler func(threadID ThreadID, actionID, value, userID string)

// MessageFormatter converts standard Markdown to platform-specific format
type MessageFormatter interface {
	FormatMessage(markdown string) string
	MaxMessageLength() int
	SplitLongMessage(text string) []string
}

// ChannelCapabilities declares presentation features without coupling shared
// session handling to platform names. Zero values opt out of each feature.
type ChannelCapabilities struct {
	ChannelHistory   bool
	Threads          bool
	MessageEdits     bool
	StreamingReplies bool
	Reactions        bool
	MessageDeletion  bool
	ProgressUpdates  bool
	WorkflowProgress bool
}

// BotMessageDeleter is implemented by connectors advertising MessageDeletion.
// Other editable channels finalize temporary progress with a terminal status.
type BotMessageDeleter interface {
	DeleteMessage(context.Context, ThreadID, string) error
}

// BotConnector is the per-platform bidirectional bot interface
type BotConnector interface {
	NotificationConnector // embeds: Name(), IsEnabled(), SendNotification()

	Capabilities() ChannelCapabilities
	StartListening(ctx context.Context) error
	StopListening()
	SendThreadMessage(ctx context.Context, threadID ThreadID, message string) (string, error)
	SendThreadMessageWithBlocks(ctx context.Context, threadID ThreadID, message string, blocks []MessageBlock) (string, error)
	UpdateMessage(ctx context.Context, threadID ThreadID, messageID string, newText string) error
	AddReaction(ctx context.Context, channelID, messageTS, emoji string) error    // no-op for platforms without reactions
	RemoveReaction(ctx context.Context, channelID, messageTS, emoji string) error // no-op for platforms without reactions
	GetThreadHistory(ctx context.Context, threadID ThreadID) ([]ThreadMessage, error)
	GetChannelName(ctx context.Context, channelID string) string // returns "" when unavailable (unsupported platform, API error, etc.)
	SetMessageHandler(handler BotMessageHandler)
	SetInteractionHandler(handler BotInteractionHandler)
	GetFormatter() MessageFormatter
}

// connectionScopedConnector is an optional BotConnector extension for
// platforms that serve several identities (Slack app connections) behind
// one platform name. The manager prefers these ThreadID-carrying variants
// so reactions and lookups land on the connection the thread lives on;
// connectors without it keep the legacy channel-only behavior.
type connectionScopedConnector interface {
	AddReactionOnConnection(ctx context.Context, threadID ThreadID, messageTS, emoji string) error
	RemoveReactionOnConnection(ctx context.Context, threadID ThreadID, messageTS, emoji string) error
	GetChannelNameOnConnection(ctx context.Context, threadID ThreadID) string
}

func botConnectorAddReaction(ctx context.Context, connector BotConnector, threadID ThreadID, messageTS, emoji string) error {
	if scoped, ok := connector.(connectionScopedConnector); ok {
		return scoped.AddReactionOnConnection(ctx, threadID, messageTS, emoji)
	}
	return connector.AddReaction(ctx, threadID.ChannelID, messageTS, emoji)
}

func botConnectorRemoveReaction(ctx context.Context, connector BotConnector, threadID ThreadID, messageTS, emoji string) error {
	if scoped, ok := connector.(connectionScopedConnector); ok {
		return scoped.RemoveReactionOnConnection(ctx, threadID, messageTS, emoji)
	}
	return connector.RemoveReaction(ctx, threadID.ChannelID, messageTS, emoji)
}

func botConnectorChannelName(ctx context.Context, connector BotConnector, threadID ThreadID) string {
	if scoped, ok := connector.(connectionScopedConnector); ok {
		return scoped.GetChannelNameOnConnection(ctx, threadID)
	}
	return connector.GetChannelName(ctx, threadID.ChannelID)
}

// BotEventSubscriber abstracts event subscription so services doesn't import internal/events.
// The server layer provides a concrete implementation wrapping EventStore.
type BotEventSubscriber interface {
	SubscribeBot(sessionID string) (<-chan BotEventData, func())
}

// BotEventData is a minimal event representation for bot filtering
type BotEventData struct {
	Type      string
	Timestamp time.Time
	Data      *events.AgentEvent
}

// SessionStartFunc is the function signature for starting an internal chat session.
// It is set by the server layer to avoid import cycles.
type SessionStartFunc func(ctx context.Context, req map[string]interface{}, sessionID string, userID string, eventCallback func(event *events.AgentEvent)) error

// ProfileTurnFunc builds the turn for a message that names no workflow route
// when the account has a default product profile for its platform: the query
// request and the profile conversation's own session id, so the message runs
// in the same conversation the product's app shows. handled=false means no
// default applies and the message runs as the generic chat instead. Set by
// the server layer, which owns profiles and their conversations.
type ProfileTurnFunc func(ctx context.Context, userID string, msg BotIncomingMessage, threadID ThreadID) (req map[string]interface{}, sessionID string, handled bool, err error)

// SessionFollowUpFunc is the function signature for injecting a follow-up message into a running session.
// It calls handleQuery with the same session ID but does NOT block on completion.
// The reqMap contains the full session config (servers, skills, delegation mode, API keys, etc.)
// so the follow-up agent is configured identically to the initial session.
type SessionFollowUpFunc func(ctx context.Context, reqMap map[string]interface{}, sessionID string, userID string) error

// BotRunningWorkflow is the minimal workflow-run view needed for bot status
// replies. The server layer maps this from its execution tracker.
type BotRunningWorkflow struct {
	WorkflowLabel     string
	WorkspacePath     string
	Status            string
	CurrentStepTitle  string
	PhaseName         string
	Title             string
	SessionID         string
	StartedAt         time.Time
	BackgroundAgents  int
	HasBackgroundWork bool
}

type RunningWorkflowsFunc func(userID string) []BotRunningWorkflow

// BotWorkflowAccessFunc checks whether a bot user may use a routed workflow.
// The server layer owns the real user directory and workflow manifest access
// helpers, so the manager receives this as an injection to avoid an import cycle.
// It may return a canonical workspace user ID; Slack often starts with an email
// slug, while server permissions are stored against AgentWorks user IDs.
type BotWorkflowAccessFunc func(ctx context.Context, workspaceUserID, userEmail string, route ChannelRoute) (resolvedUserID string, allowed bool, err error)

type BotResumeTarget struct {
	ProfileRoute *ProfileRoute

	SessionID     string
	UserID        string
	AgentMode     string
	Status        string
	Query         string
	WorkspacePath string
	PresetQueryID string
	PhaseID       string
	WorkshopMode  string
	// Display-only fields for the resume picker / ack. WorkflowName is the
	// human-friendly workflow or preset name (never the raw session id);
	// Activity is the current step/phase or background work.
	WorkflowName          string
	Activity              string
	HasBackgroundActivity bool
}

type BotResumeFilter struct {
	ProfileID       string
	ConversationKey string
	WorkspaceUserID string

	WorkspacePath string
	PresetQueryID string
}

type BotResumeTargetFunc func(ctx context.Context, userID, selector string, filter BotResumeFilter) (*BotResumeTarget, error)
type BotResumeListFunc func(ctx context.Context, userID string, filter BotResumeFilter) ([]BotResumeTarget, error)

type BotThreadStatus struct {
	HasSession bool
	Status     string
	DetailMode string
}

type BotThreadStatusFunc func(threadID ThreadID) BotThreadStatus

type botThreadStatusReceiver interface {
	SetBotThreadStatusProvider(BotThreadStatusFunc)
}

// BotConversationManager is the platform-agnostic orchestrator for bot sessions.
// Bot conversations are unified with regular chat sessions: each Slack thread
// / DM / web-simulator tab maps to a chat session folder with a BotMetadata
// block attached to its manifest.
type BotConversationManager struct {
	mu         sync.RWMutex
	connectors map[string]BotConnector      // platform name -> connector
	sessions   map[string]*activeBotSession // threadKey -> active session tracking
	// threadSeen is the per-thread watermark of thread messages already given
	// to the agent, so follow-ups add only what was posted since.
	threadSeen threadSeenTracker

	chatStore       chathistory.Store
	eventSubscriber BotEventSubscriber

	// Function references set by server layer (to avoid import cycles)
	startSession    SessionStartFunc
	followUpSession SessionFollowUpFunc
	profileTurn     ProfileTurnFunc
	// workflowTurn builds a Slack workflow turn; dmUserID is the account a
	// 1:1 DM runs as ("" for channels, which run as the route).
	workflowTurn func(ctx context.Context, query string, route ChannelRoute, thread ThreadID, dmUserID string) (map[string]interface{}, error)
	// userWorkflowChat names a user's own chat of a workflow — the one their
	// web Builder restores — so a DM continues it ("" when they have none).
	userWorkflowChat func(ctx context.Context, userID string, route ChannelRoute) string
	chatHistory      ChatHistoryReaderFunc
	runningWorkflows RunningWorkflowsFunc
	workflowAccess   BotWorkflowAccessFunc
	resumeTarget     BotResumeTargetFunc
	resumeList       BotResumeListFunc
	mcpConfigPath    string
	workspaceURL     string
}

// activeBotSession tracks an in-progress bot conversation. In the unified
// model there is exactly one ID — the chat session ID (folder name).
type activeBotSession struct {
	mu                sync.Mutex
	SessionID         string // unified chat session id
	UserID            string // workspace user ID for secrets loading
	Status            string
	Platform          string
	ThreadID          ThreadID
	RouteKey          string
	Metadata          *chathistory.BotMetadata // platform/user info for the conversation
	cancel            context.CancelFunc
	eventFilter       *BotEventFilter
	awaitingUserInput bool       // set by event filter on any blocking event
	blockingEventType string     // "blocking_human_feedback"
	blockingRequestID string     // feedback request_id for blocking_human_feedback
	ackChannelID      string     // channel of the message the bot reacted to (for removal)
	ackMessageTS      string     // timestamp of the message the bot reacted to
	followUpAcks      []ThreadID // every accepted follow-up reaction is cleared on completion
	LastActivity      time.Time  // updated on any send/receive; used to prune stale completed sessions

	// Background workflow tracking — when the builder agent fires
	// run_full_workflow (or any tool that registers a parent chat), we bump
	// pendingWorkflows so the session context isn't canceled until the
	// workflow drains. Workflow step events publish to this parent session's
	// own event stream (routed by the orchestrator's ContextAwareBridge), so
	// the existing BotEventFilter forwards them to Slack — no separate mirror
	// subscription is needed. Access under mu.
	builderDone      bool            // event filter signaled the parent session finished its own turn
	pendingWorkflows int             // live workflows attached via SpawnListener (>0 defers cancel)
	activeWorkflows  map[string]bool // wfSessionID set, for idempotent NotifyWorkflowEnded
	sendFullDetails  bool
	// profileTurn marks a session running in a product profile's own
	// conversation (ProfileTurnFunc). Every later turn in it is built the
	// same way, and none of the generic bot-runtime preambles apply.
	profileTurn bool
	// profileRoute is the product profile the thread was routed to by an
	// @token (nil: the pairing's default profile); later turns keep it.
	profileRoute  *ProfileRoute
	BotRouteGrant string
	AgentMode     string
	PresetQueryID string
	WorkspacePath string
	PhaseID       string
	WorkshopMode  string
}

// NewBotConversationManager creates a new manager.
func NewBotConversationManager(chatStore chathistory.Store, mcpConfigPath, workspaceURL string) *BotConversationManager {
	m := &BotConversationManager{
		connectors:    make(map[string]BotConnector),
		sessions:      make(map[string]*activeBotSession),
		chatStore:     chatStore,
		mcpConfigPath: mcpConfigPath,
		workspaceURL:  workspaceURL,
	}
	go m.runSessionJanitor()
	return m
}

// runSessionJanitor prunes completed/failed sessions older than 7 days. Running
// sessions are never pruned from here — they clean up via runSession's exit.
func (m *BotConversationManager) runSessionJanitor() {
	const maxAge = 7 * 24 * time.Hour
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-maxAge)
		m.mu.Lock()
		for key, active := range m.sessions {
			active.mu.Lock()
			prune := (active.Status == chathistory.BotSessionStatusCompleted ||
				active.Status == chathistory.BotSessionStatusFailed) &&
				active.LastActivity.Before(cutoff)
			active.mu.Unlock()
			if prune {
				delete(m.sessions, key)
			}
		}
		m.mu.Unlock()
	}
}

// SetEventSubscriber sets the event subscriber (injected by server layer)
func (m *BotConversationManager) SetEventSubscriber(sub BotEventSubscriber) {
	m.eventSubscriber = sub
}

// SetStartSessionFunc sets the function used to start internal chat sessions
func (m *BotConversationManager) SetStartSessionFunc(fn SessionStartFunc) {
	m.startSession = fn
}

// SetFollowUpFunc sets the function used to inject follow-up messages into running sessions
func (m *BotConversationManager) SetFollowUpFunc(fn SessionFollowUpFunc) {
	m.followUpSession = fn
}

// SetRunningWorkflowsFunc sets the server-backed running workflow snapshot used by status commands.
func (m *BotConversationManager) SetRunningWorkflowsFunc(fn RunningWorkflowsFunc) {
	m.runningWorkflows = fn
}

// SetWorkflowAccessFunc installs the server-backed workflow visibility check
// used by routed bot messages.
func (m *BotConversationManager) SetWorkflowAccessFunc(fn BotWorkflowAccessFunc) {
	m.workflowAccess = fn
}

func (m *BotConversationManager) SetResumeTargetFunc(fn BotResumeTargetFunc) {
	m.resumeTarget = fn
}

func (m *BotConversationManager) SetResumeListFunc(fn BotResumeListFunc) {
	m.resumeList = fn
}

func (m *BotConversationManager) BotThreadStatus(threadID ThreadID) BotThreadStatus {
	m.mu.RLock()
	active := m.sessions[threadID.Key()]
	if active == nil && threadID.ThreadTS == "" && threadID.ChannelID != "" {
		threadID.ThreadTS = threadID.ChannelID
		active = m.sessions[threadID.Key()]
	}
	m.mu.RUnlock()
	if active == nil {
		return BotThreadStatus{DetailMode: "concise"}
	}

	active.mu.Lock()
	defer active.mu.Unlock()
	detailMode := "concise"
	if active.sendFullDetails {
		detailMode = "full"
	}
	return BotThreadStatus{
		HasSession: true,
		Status:     active.Status,
		DetailMode: detailMode,
	}
}

// SetProfileTurnFunc sets the builder for turns in an account's default
// product profile conversation (see ProfileTurnFunc).
func (m *BotConversationManager) SetProfileTurnFunc(fn ProfileTurnFunc) {
	m.profileTurn = fn
}

// ChatHistoryReaderFunc returns, in order, the text of every completed
// assistant ("ai" role) turn in a session's durable conversation_history —
// the exact record the UI's own chat-restore path trusts, updated
// incrementally while the turn is still running. A turn with only a tool
// call (no text) is omitted. Set by the server layer, which owns chat-history
// persistence and parsing; nil disables progressive text.
type ChatHistoryReaderFunc func(ctx context.Context, userID, sessionID string) ([]string, error)

// SetChatHistoryReader installs the durable-history reader that drives
// progressive text (see pollProgressiveText).
func (m *BotConversationManager) SetChatHistoryReader(fn ChatHistoryReaderFunc) {
	m.chatHistory = fn
}

// RegisterConnector registers a bot connector
func (m *BotConversationManager) RegisterConnector(connector BotConnector) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := connector.Name()
	m.connectors[name] = connector
	GetNotificationManager().RegisterConnector(connector)
	if receiver, ok := connector.(botThreadStatusReceiver); ok {
		receiver.SetBotThreadStatusProvider(m.BotThreadStatus)
	}

	// Set up message handler
	connector.SetMessageHandler(m.HandleIncomingMessage)
	connector.SetInteractionHandler(m.HandleInteraction)

	log.Printf("[BOT_MANAGER] Registered connector: %s", name)
}

// GetConnector returns a connector by platform name
func (m *BotConversationManager) GetConnector(platform string) BotConnector {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connectors[platform]
}

func (m *BotConversationManager) persistBotSessionBinding(active *activeBotSession, updatedAt time.Time) {
	if active == nil {
		return
	}
	active.mu.Lock()
	platform := active.Platform
	threadID := active.ThreadID
	binding := BotSessionBinding{
		SessionID: active.SessionID,
		RouteKey:  active.RouteKey,
		UpdatedAt: updatedAt,
	}
	active.mu.Unlock()
	store, ok := m.GetConnector(platform).(botSessionBindingStore)
	if !ok || binding.SessionID == "" {
		return
	}
	if binding.UpdatedAt.IsZero() {
		binding.UpdatedAt = time.Now()
	}
	if err := store.SaveBotSessionBinding(context.Background(), threadID, binding); err != nil {
		log.Printf("[BOT_MANAGER] Failed to persist conversation binding for thread %s: %v", threadID.Key(), err)
	}
}

// loadMCPServerNames reads server names from the MCP config.
func (m *BotConversationManager) loadMCPServerNames() []string {
	cfg, err := mcpclient.LoadMergedConfig(m.mcpConfigPath, nil)
	if err != nil || cfg == nil {
		return nil
	}
	return cfg.ListServers()
}

// LoadAvailableCapabilities returns all available MCP servers and skills.
// Used by the config API endpoint.
func (m *BotConversationManager) LoadAvailableCapabilities(workspacePath ...string) (servers []string, discoveredSkills []skills.Skill) {
	servers = m.loadMCPServerNames()
	if m.workspaceURL != "" && len(workspacePath) > 0 && workspacePath[0] != "" {
		if inventory, err := skills.DiscoverSkillsIn(m.workspaceURL, workspacePath[0]); err == nil {
			discoveredSkills = inventory.Skills
		}
	}
	return
}

// emailToUserID slugifies an email into a filesystem-safe userID segment
// that matches the chathistory sanitizeUserID regex (^[a-zA-Z0-9_-]+$).
// Example: "Alice.Jones+work@Company.com" → "alice-jones-work-company-com"
func emailToUserID(email string) string {
	s := strings.ToLower(strings.TrimSpace(email))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	out := b.String()
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return strings.Trim(out, "-")
}

// resolveWorkspaceUserID maps a bot message to a workspace user ID for
// per-user chat history, memory, and schedules. The web simulator pre-resolves
// this from HTTP auth; async platforms (Slack, Discord, etc.) derive it from
// the user's email. Returns "" when neither source is available — the caller
// must reject the message rather than falling through to a shared folder.
func (m *BotConversationManager) resolveWorkspaceUserID(msg BotIncomingMessage) string {
	if msg.WorkspaceUserID != "" {
		return msg.WorkspaceUserID
	}
	if msg.UserEmail != "" {
		return emailToUserID(msg.UserEmail)
	}
	return ""
}

func (m *BotConversationManager) workflowRouteForMessage(msg BotIncomingMessage, active *activeBotSession) *ChannelRoute {
	if msg.PresetWorkflow != nil {
		return msg.PresetWorkflow
	}
	if active != nil && strings.EqualFold(strings.TrimSpace(msg.Platform), "slack") {
		if route := m.resolveThreadRoute(botMessageThread(msg)); route != nil {
			return route
		}
		active.mu.Lock()
		wasRouted := strings.TrimSpace(active.RouteKey) != "" || strings.TrimSpace(active.PresetQueryID) != "" || active.profileRoute != nil
		active.mu.Unlock()
		if wasRouted {
			return RevokedSlackRoute()
		}
	}
	if active != nil {
		return botRouteFromActive(active)
	}
	return m.resolveThreadRoute(botMessageThread(msg))
}

// botMessageThread is the platform thread a message belongs to.
func botMessageThread(msg BotIncomingMessage) ThreadID {
	thread := ThreadID{Platform: msg.Platform, ChannelID: msg.ChannelID, ThreadTS: msg.ThreadTS, ConnectionID: msg.ConnectionID}
	if thread.ThreadTS == "" {
		thread.ThreadTS = msg.ChannelID
	}
	return thread
}

// routeChangeKeepsSession reports whether granting this route must leave the
// existing session untouched: on thread-less platforms a changed route is a
// hard conversation boundary, and handleExistingSession detects it by
// comparing the incoming route against the session's stored key. Overwriting
// the stored key here would put the new route on both sides of that
// comparison, miss the switch, and reuse the previous workflow's conversation
// (history, files, resume state) for an unrelated workflow. msg must already
// carry the granted route so the comparison matches what
// handleExistingSession will see.
func (m *BotConversationManager) routeChangeKeepsSession(msg BotIncomingMessage, active *activeBotSession) bool {
	if active == nil {
		return false
	}
	connector := m.GetConnector(msg.Platform)
	if connector == nil || connector.Capabilities().Threads {
		// Threaded platforms have no route-change boundary, and an unknown
		// platform keeps the established refresh behavior.
		return false
	}
	active.mu.Lock()
	prev := active.RouteKey
	sessionID := active.SessionID
	active.mu.Unlock()
	incoming := botMessageRouteKey(msg)
	if incoming == prev {
		return false
	}
	log.Printf("[BOT_MANAGER] Thread-less route changed for session %s (%q → %q) — leaving the session untouched so a fresh conversation starts",
		sessionID, prev, incoming)
	return true
}

// slackBotSwitchExplicitlyOff reports whether a Slack connector config exists
// with the platform switch off. Unknown state (no store, no config) counts
// as on: the switch-off state must be explicit to silence traffic.
func (m *BotConversationManager) slackBotSwitchExplicitlyOff() bool {
	if m == nil || m.chatStore == nil {
		return false
	}
	cfg, err := m.chatStore.GetBotConnectorConfig(context.Background(), "slack")
	if err != nil || cfg == nil {
		return false
	}
	return !(cfg.Enabled && cfg.BotMode)
}

func (m *BotConversationManager) authorizeWorkflowRouteForMessage(ctx context.Context, msg *BotIncomingMessage, threadID ThreadID, active *activeBotSession) bool {
	if msg == nil {
		return true
	}
	route := m.workflowRouteForMessage(*msg, active)
	if route != nil && route.BotGrant == revokedBotGrant {
		log.Printf("[BOT_MANAGER] Routed Slack session no longer has an active route for channel=%s", msg.ChannelID)
		if msg.IsMention {
			m.sendWorkflowAccessDenied(msg.Platform, threadID, "This Slack route is no longer configured. Ask a workflow owner to re-enable it.")
		}
		return false
	}
	if route == nil || (strings.TrimSpace(route.WorkflowID) == "" && strings.TrimSpace(route.ProfileID) == "") {
		// Slack channel mentions need a route; direct messages can still use generic chat.
		channelID := strings.ToUpper(strings.TrimSpace(msg.ChannelID))
		if strings.EqualFold(msg.Platform, "slack") && (strings.HasPrefix(channelID, "C") || strings.HasPrefix(channelID, "G")) {
			if msg.IsMention {
				m.sendWorkflowAccessDenied(msg.Platform, threadID, unroutedBotAccessMessage(msg.Platform, ""))
			}
			return false
		}
		// Slack direct messages may use generic chat unless the platform
		// switch is explicitly off. Other platforms keep their own enablement.
		if strings.EqualFold(msg.Platform, "slack") && m.slackBotSwitchExplicitlyOff() {
			return false
		}
		return true
	}
	if msg.Platform == "slack" && !SlackRouteAllowsEmail(*route, msg.UserEmail) {
		if msg.IsMention {
			m.sendWorkflowAccessDenied(msg.Platform, threadID, "This route blocks your email, or your email could not be verified.")
		}
		return false
	}
	if strings.TrimSpace(route.WorkflowID) == "" && strings.TrimSpace(route.ProfileID) != "" {
		workspaceUserID := routeWorkspaceUserID(*route, strings.TrimSpace(msg.WorkspaceUserID))
		if workspaceUserID == "" {
			log.Printf("[BOT_MANAGER] Profile route on %s channel=%s has no workspace owner", msg.Platform, msg.ChannelID)
			if msg.IsMention {
				m.sendWorkflowAccessDenied(msg.Platform, threadID, "This Slack route is missing its product owner. Ask a product owner to save the route again.")
			}
			return false
		}
		// The owner holds the crew's conversations. A channel turn also
		// runs as the owner (in Run mode). A 1:1 DM or WhatsApp message runs
		// as its sender (mapped or paired into WorkspaceUserID) — never as
		// the owner of someone else's crew.
		if !msg.runsAsSender() {
			msg.WorkspaceUserID = workspaceUserID
		}
		if msg.PresetWorkflow == nil {
			msg.PresetWorkflow = route
		}
		if active != nil && !m.routeChangeKeepsSession(*msg, active) {
			active.mu.Lock()
			active.UserID = strings.TrimSpace(msg.WorkspaceUserID)
			active.WorkspacePath = strings.TrimSpace(route.WorkspacePath)
			active.WorkshopMode = WorkshopModeForBotGrant(route.BotGrant)
			active.BotRouteGrant = NormalizeBotRouteGrant(route.BotGrant, route.WorkshopMode)
			active.RouteKey = botRouteKey(route)
			active.profileTurn = true
			active.profileRoute = &ProfileRoute{
				ProfileID:       strings.TrimSpace(route.ProfileID),
				ConversationKey: strings.TrimSpace(route.ConversationKey),
				UploadFolder:    strings.TrimSpace(route.WorkspacePath),
				Label:           firstNonEmptyString(strings.TrimSpace(route.ProfileLabel), strings.TrimSpace(route.ProfileID)),
				WorkspaceUserID: workspaceUserID,
			}
			active.mu.Unlock()
		}
		return true
	}
	// A routed workflow without a wired access check is a misconfigured server,
	// not an open door. Refuse rather than fall through ungated.
	if m.workflowAccess == nil {
		log.Printf("[BOT_MANAGER] No workflow access check installed — refusing routed workflow message on %s", msg.Platform)
		if !strings.EqualFold(strings.TrimSpace(msg.Platform), "slack") {
			return true
		}
		if msg.IsMention {
			m.sendWorkflowAccessDenied(msg.Platform, threadID, "Workflow access checks are not configured on this server. Ask an administrator to enable them.")
		}
		return false
	}
	botUserID := BotPrincipalIDForRoute(msg.Platform, *route)
	userEmail := ""
	if msg.runsAsSender() {
		// WhatsApp is paired to a workspace account, and a 1:1 Slack DM is
		// mapped to its sender's account. The route identifies a
		// destination, not an independent permission grant.
		botUserID = strings.TrimSpace(msg.WorkspaceUserID)
		userEmail = strings.TrimSpace(msg.UserEmail)
	}
	resolvedUserID, allowed, err := m.workflowAccess(ctx, botUserID, userEmail, *route)
	if strings.TrimSpace(resolvedUserID) != "" {
		msg.WorkspaceUserID = strings.TrimSpace(resolvedUserID)
	}
	if err != nil {
		log.Printf("[BOT_MANAGER] Workflow access check failed for %s user=%s: %v", msg.Platform, msg.UserID, err)
		if msg.IsMention {
			m.sendWorkflowAccessDenied(msg.Platform, threadID, "I can't verify your workflow access right now. Please try again in a moment.")
		}
		return false
	}
	if allowed {
		if msg.PresetWorkflow == nil {
			msg.PresetWorkflow = route
		}
		if active != nil && !m.routeChangeKeepsSession(*msg, active) {
			active.mu.Lock()
			active.UserID = strings.TrimSpace(msg.WorkspaceUserID)
			active.PresetQueryID = strings.TrimSpace(route.WorkflowID)
			active.WorkspacePath = strings.TrimSpace(route.WorkspacePath)
			active.WorkshopMode = WorkshopModeForBotGrant(route.BotGrant)
			active.BotRouteGrant = NormalizeBotRouteGrant(route.BotGrant, route.WorkshopMode)
			active.RouteKey = botRouteKey(route)
			active.mu.Unlock()
		}
		return true
	}
	log.Printf("[BOT_MANAGER] Workflow access denied for %s user=%s email=%s workflow=%s", msg.Platform, msg.UserID, msg.UserEmail, strings.TrimSpace(route.WorkflowID))
	if msg.IsMention {
		m.sendWorkflowAccessDenied(msg.Platform, threadID, "This channel's bot route cannot access its configured workflow. Ask a workflow owner to check the route and workflow settings.")
	}
	return false
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (m *BotConversationManager) sendWorkflowAccessDenied(platform string, threadID ThreadID, message string) {
	connector := m.GetConnector(platform)
	if connector == nil {
		return
	}
	if threadID.ThreadTS == "" {
		threadID.ThreadTS = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	_, _ = connector.SendThreadMessage(context.Background(), threadID, message)
}

// botThreadPeerStore is optional: a connector serving several app
// identities reports durable sessions other apps hold in a thread, so the
// multi-bot rule survives a restart.
type botThreadPeerStore interface {
	HasOtherBotSessionBinding(ctx context.Context, thread ThreadID, ownRouteKey string) bool
}

// threadHasOtherBot reports whether a different app connection holds a
// session in the same platform thread.
func (m *BotConversationManager) threadHasOtherBot(thread ThreadID, ownRouteKey string) bool {
	m.mu.RLock()
	for _, active := range m.sessions {
		if active == nil {
			continue
		}
		// ThreadID is fixed at session creation; reading it without
		// active.mu keeps the m.mu -> active.mu lock order out of play.
		other := active.ThreadID
		if other.sameThread(thread) && other.ConnectionID != thread.ConnectionID {
			m.mu.RUnlock()
			return true
		}
	}
	m.mu.RUnlock()
	peers, ok := m.GetConnector(thread.Platform).(botThreadPeerStore)
	return ok && peers.HasOtherBotSessionBinding(context.Background(), thread, ownRouteKey)
}

// HandleIncomingMessage processes a message from any platform (async path)
func (m *BotConversationManager) HandleIncomingMessage(msg BotIncomingMessage) {
	// Non-mention messages should only be processed if there's an active session
	// in the thread. A channel route is NOT enough on its own: attaching a route
	// to every message in the channel (see SlackService.handleSlackBotMessage)
	// would otherwise make any reply in any thread — including two colleagues
	// talking to each other — start a fresh workflow run. Opening a session is
	// always an explicit @mention.
	// Skip access checks and don't reply with "no access" for messages that didn't tag the bot.
	if !msg.IsMention {
		// Quick check: is there even a session entry for this thread? The key
		// must match ThreadID.Key() format (platform-prefixed) — not just
		// channel:ts — otherwise the lookup always misses and non-mention
		// replies are dropped even when a prior session exists.
		probeThreadID := ThreadID{Platform: msg.Platform, ChannelID: msg.ChannelID, ThreadTS: msg.ThreadTS, ConnectionID: msg.ConnectionID}
		if probeThreadID.ThreadTS == "" {
			probeThreadID.ThreadTS = msg.ChannelID
		}
		m.mu.Lock()
		_, hasSession := m.sessions[probeThreadID.Key()]
		m.mu.Unlock()
		routeKey := ""
		if strings.EqualFold(msg.Platform, "slack") {
			routed := msg
			if routed.PresetWorkflow == nil {
				routed.PresetWorkflow = m.resolveThreadRoute(probeThreadID)
			}
			routeKey = botMessageRouteKey(routed)
		}
		if !hasSession {
			// A durable binding admits replies after restart, but never opens an
			// unrelated thread merely because its channel has a route.
			if store, ok := m.GetConnector(msg.Platform).(botSessionBindingStore); ok {
				_, hasSession, _ = store.LoadBotSessionBinding(context.Background(), probeThreadID, routeKey)
			}
			if !hasSession {
				return
			}
		}
		// With several bots in one thread a plain reply has no addressee:
		// every bot stays silent and the user tags the one they mean.
		if m.threadHasOtherBot(probeThreadID, routeKey) {
			log.Printf("[BOT_MANAGER] Plain reply in multi-bot thread %s ignored; tag a bot", probeThreadID.Key())
			return
		}
	}

	connector := m.GetConnector(msg.Platform)
	if connector == nil {
	}

	threadID := ThreadID{
		Platform:     msg.Platform,
		ChannelID:    msg.ChannelID,
		ThreadTS:     msg.ThreadTS,
		ConnectionID: msg.ConnectionID,
	}

	// Determine thread key based on whether platform supports threads
	supportsThreads := connector != nil && connector.Capabilities().Threads
	if !supportsThreads {
		threadID.ThreadTS = msg.ChannelID
	} else if threadID.ThreadTS == "" {
		threadID.ThreadTS = fmt.Sprintf("%d", time.Now().UnixNano())
	}

	threadKey := threadID.Key()
	log.Printf("[BOT_MANAGER] Incoming message from %s user=%s thread=%s: %s", msg.Platform, msg.UserID, threadKey, botTruncate(msg.Text, 100))

	m.mu.RLock()
	active, exists := m.sessions[threadKey]
	m.mu.RUnlock()

	routedMessage := false
	if route := m.workflowRouteForMessage(msg, active); route != nil && (strings.TrimSpace(route.WorkflowID) != "" || strings.TrimSpace(route.ProfileID) != "" || active != nil && strings.EqualFold(strings.TrimSpace(msg.Platform), "slack") && strings.TrimSpace(active.RouteKey) != "") {
		routedMessage = true
	}

	// Reject generic chat if we can't link the user to a workspace identity.
	// Configured routes use the route-scoped bot principal instead; the Slack
	// sender stays audit metadata and does not own execution.
	if !routedMessage && msg.UserEmail == "" && msg.WorkspaceUserID == "" {
		log.Printf("[BOT_MANAGER] Rejected message from %s — no email available to link account", msg.UserID)
		if msg.IsMention {
			if connector := m.GetConnector(msg.Platform); connector != nil {
				connector.SendThreadMessage(context.Background(), threadID,
					unroutedBotAccessMessage(msg.Platform, "Can't link your account — no email available from this platform. Please contact your administrator."))
			}
		}
		return
	}

	// Check allowed_emails for generic chat only. A configured route is invoked
	// under its bot grant; channel membership and route configuration are the
	// authorization boundary, while sender identity is audit metadata.
	if !routedMessage && msg.UserEmail != "" {
		var allowedEmails []string

		// 1. Load from filesystem-backed bot connector config
		if m.chatStore != nil {
			globalCfg, _ := m.chatStore.GetBotConnectorConfig(context.Background(), "_global")
			if globalCfg != nil && globalCfg.ConfigJSON != "" {
				var cfgData map[string]json.RawMessage
				if err := json.Unmarshal([]byte(globalCfg.ConfigJSON), &cfgData); err == nil {
					if raw, ok := cfgData["allowed_emails"]; ok {
						json.Unmarshal(raw, &allowedEmails)
					}
				}
			}
		}

		// 2. Merge with BOT_ALLOWED_EMAILS env var (comma-separated)
		if envEmails := os.Getenv("BOT_ALLOWED_EMAILS"); envEmails != "" {
			for _, e := range strings.Split(envEmails, ",") {
				e = strings.TrimSpace(e)
				if e != "" {
					allowedEmails = append(allowedEmails, e)
				}
			}
		}

		// 3. If any allowed emails are configured, enforce the filter
		if len(allowedEmails) > 0 {
			allowed := false
			for _, email := range allowedEmails {
				if strings.EqualFold(email, msg.UserEmail) {
					allowed = true
					break
				}
			}
			if !allowed {
				log.Printf("[BOT_MANAGER] Rejected message from %s (%s) — not in allowed_emails", msg.UserID, msg.UserEmail)
				// Only reply with rejection for direct @mentions
				if msg.IsMention {
					if connector := m.GetConnector(msg.Platform); connector != nil {
						threadID := ThreadID{Platform: msg.Platform, ChannelID: msg.ChannelID, ThreadTS: msg.ThreadTS, ConnectionID: msg.ConnectionID}
						if threadID.ThreadTS == "" {
							threadID.ThreadTS = fmt.Sprintf("%d", time.Now().UnixNano())
						}
						connector.SendThreadMessage(context.Background(), threadID, unroutedBotAccessMessage(msg.Platform, "Sorry, you don't have access to use this bot. Please contact your administrator to get access."))
					}
				}
				return
			}
		}
	}

	if !m.authorizeWorkflowRouteForMessage(context.Background(), &msg, threadID, active) {
		return
	}

	if selector, ok := parseBotResumeCommand(msg.Text, !supportsThreads); ok {
		m.handleBotResumeCommand(msg, threadID, threadKey, selector, botResumeFilterFromRoute(msg.PresetWorkflow))
		return
	}

	if exists {
		m.handleExistingSession(active, msg, supportsThreads)
		return
	}

	if m.handleBotControlWithoutSession(msg, threadID, supportsThreads) {
		return
	}

	// WhatsApp persists the current chat → session pointer in its per-device
	// SQLite store. Restore it after a process restart, subject to the same
	// one-hour inactivity boundary used by the in-memory path.
	if !supportsThreads || msg.Platform == "slack" {
		// A reply addressed to someone else must not revive a conversation
		// after restart either. Status/resume commands were already matched
		// above, and there is no live blocking state on this path.
		if m.ignoreOtherUserMention(active, msg) {
			return
		}
		if store, ok := connector.(botSessionBindingStore); ok {
			binding, found, err := store.LoadBotSessionBinding(context.Background(), threadID, botMessageRouteKey(msg))
			if err != nil {
				log.Printf("[BOT_MANAGER] Failed to load conversation binding for thread %s: %v", threadKey, err)
			} else if found {
				idle := time.Since(binding.UpdatedAt)
				if supportsThreads || msg.PresetProfile != nil || binding.UpdatedAt.IsZero() || idle <= threadlessSessionIdleLimit {
					log.Printf("[BOT_MANAGER] Restoring persisted session %s for thread %s after server restart", binding.SessionID, threadKey)
					go m.startNewSessionDirect(msg, threadID, binding.SessionID)
					return
				}
				history := m.loadRecentChatTurns(context.Background(), m.resolveWorkspaceUserID(msg), binding.SessionID, staleContextTurns)
				msg.Text = buildStaleSessionPreamble(history, idle) + msg.Text
				log.Printf("[BOT_MANAGER] Persisted session %s for thread %s idle %s — starting new conversation with %d context turn(s)",
					binding.SessionID, threadKey, humanDuration(idle), len(history))
			}
		}
	}

	if m.ignoreOtherUserMention(active, msg) {
		return
	}
	go m.startNewSessionDirect(msg, threadID)
}

// HandleInteraction processes a button click from a platform
func (m *BotConversationManager) HandleInteraction(threadID ThreadID, actionID, value, userID string) {
	threadKey := threadID.Key()

	m.mu.RLock()
	active, exists := m.sessions[threadKey]
	m.mu.RUnlock()

	if !exists {
		log.Printf("[BOT_MANAGER] Interaction for unknown thread: %s", threadKey)
		return
	}

	switch value {
	case "cancel":
		m.cancelSession(active, "Canceled by user")
	default:
		log.Printf("[BOT_MANAGER] Unknown interaction value: %s for thread %s", value, threadKey)
	}
}

// threadlessSessionIdleLimit is how long a thread-less chat (WhatsApp,
// Telegram, …) can sit idle before the next message is treated as a new
// conversation rather than a continuation. Tuned for messaging-app habit:
// within an hour it's almost always "still talking about the same thing";
// gaps longer than that usually mean a new topic.
const threadlessSessionIdleLimit = 1 * time.Hour

// staleContextTurns is how many of the prior conversation's most recent
// user+assistant messages we prepend to a freshly-minted session when the
// idle gap triggers a new conversation. Kept small so the new session
// isn't drowned in old context but has enough to resolve casual
// references ("the same issue as before", etc.).
const staleContextTurns = 3

// loadRecentChatTurns reads the last `n` user/assistant turns from a
// session's conversation.json. Returns nil on any read/parse failure; the
// caller treats "no context" and "failed to load" identically. Used to
// carry a breadcrumb of prior exchange into a new session when a thread-
// less chat resumes after the idle threshold.
func (m *BotConversationManager) loadRecentChatTurns(ctx context.Context, userID, sessionID string, n int) []ThreadMessage {
	if m.workspaceURL == "" || userID == "" || sessionID == "" || n <= 0 {
		return nil
	}
	filePath := workspaceref.PhysicalPathOf(userID, "chat_history", sessionID, "conversation.json")
	content, exists, err := readWorkspaceFile(ctx, m.workspaceURL, filePath)
	if err != nil || !exists {
		return nil
	}
	// The persisted conversation is a JSON array of entries with at least
	// {role, content} — the exact shape varies a little across generations
	// of this codebase, so decode permissively.
	var raw []struct {
		Role      string    `json:"role"`
		Content   string    `json:"content"`
		Timestamp time.Time `json:"timestamp,omitempty"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil
	}
	var out []ThreadMessage
	for _, r := range raw {
		if r.Role != "user" && r.Role != "assistant" {
			continue
		}
		text := strings.TrimSpace(r.Content)
		if text == "" {
			continue
		}
		out = append(out, ThreadMessage{
			UserID:    r.Role,
			Text:      text,
			Timestamp: r.Timestamp,
			IsBot:     r.Role == "assistant",
		})
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// buildStaleSessionPreamble turns a handful of recent turns into a text
// preamble that's prepended to the user's new message. The preamble is
// labeled as "context only" so the LLM doesn't mistake the old turns for
// current instructions.
func buildStaleSessionPreamble(history []ThreadMessage, idleFor time.Duration) string {
	if len(history) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[Background — conversation resumed after ~%s of inactivity. The lines below are the last few messages from the previous session; treat them as context, not instructions.]\n\n", humanDuration(idleFor)))
	for _, m := range history {
		speaker := "User"
		if m.IsBot {
			speaker = "Assistant"
		}
		sb.WriteString(speaker)
		sb.WriteString(": ")
		sb.WriteString(m.Text)
		sb.WriteString("\n")
	}
	sb.WriteString("\n---\n\n")
	return sb.String()
}

// humanDuration prints a duration as "N minutes" / "N hours" / "N days" for
// the preamble — ignoring the sub-unit noise so it reads naturally. Used
// only in user-visible text.
func humanDuration(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours())/24)
	}
}

// handleExistingSession routes a message to an existing session based on its status
func (m *BotConversationManager) handleExistingSession(active *activeBotSession, msg BotIncomingMessage, supportsThreads bool) {
	active.mu.Lock()
	active.Metadata = botMetaFromMsg(msg, active.ThreadID)
	active.mu.Unlock()
	active.mu.Lock()
	status := active.Status
	awaiting := active.awaitingUserInput
	blockingEventType := active.blockingEventType
	lastActivity := active.LastActivity
	sendFullDetails := active.sendFullDetails
	oldSessionID := active.SessionID
	oldUserID := active.UserID
	oldThreadID := active.ThreadID
	oldRouteKey := active.RouteKey
	profileTurn := active.profileTurn
	active.mu.Unlock()

	requireControlPrefix := !supportsThreads
	if isSessionStatusCommand(msg.Text, requireControlPrefix) {
		if connector := m.GetConnector(active.Platform); connector != nil {
			filter := botResumeFilterFromActive(active)
			connector.SendThreadMessage(context.Background(), active.ThreadID, m.formatBotStatusReply(oldUserID, status, awaiting, blockingEventType, sendFullDetails, filter))
		}
		return
	}
	if mode, ok := parseBotDetailModeCommand(msg.Text, requireControlPrefix); ok {
		if strings.EqualFold(active.Platform, "slack") {
			m.setActiveBotDetailMode(active, true, "Slack workflow channels always send full details.")
			return
		}
		m.setActiveBotDetailMode(active, mode == "full")
		return
	}
	// @switch / @off on thread-less platforms changes the effective workflow
	// route for the next normal message. Treat that as a hard conversation
	// boundary: continuing the same session would mix workflow files, workshop
	// mode, and native coding-agent resume state across unrelated routes.
	if !supportsThreads && !awaiting {
		incomingRouteKey := botMessageRouteKey(msg)
		if incomingRouteKey != oldRouteKey {
			log.Printf("[BOT_MANAGER] Thread-less route changed for session %s (%q → %q) — starting fresh conversation",
				oldSessionID, oldRouteKey, incomingRouteKey)
			if status == chathistory.BotSessionStatusRunning || status == chathistory.BotSessionStatusAwaitingPlanApproval {
				m.cancelSession(active, "")
			}
			go m.startNewSessionDirect(msg, oldThreadID)
			return
		}
	}

	// Thread-less platforms (WhatsApp, Telegram) use a 1-hour inactivity
	// window to decide whether this message continues the prior
	// conversation or kicks off a new one. Within the window we fall
	// through to the Running/Completed reuse paths below. Past the window
	// we mint a fresh sessionID and prepend a few lines of the prior
	// conversation as context, so the agent has some memory of "last
	// time" without inheriting the whole history.
	if !supportsThreads && !awaiting {
		idle := time.Since(lastActivity)
		if idle > threadlessSessionIdleLimit {
			if profileTurn {
				// The product conversation is durable and continues as itself;
				// there is no "last time" to summarise into the message.
				log.Printf("[BOT_MANAGER] Thread-less session %s idle %s — continuing the profile conversation", oldSessionID, humanDuration(idle))
			} else {
				history := m.loadRecentChatTurns(context.Background(), oldUserID, oldSessionID, staleContextTurns)
				msg.Text = buildStaleSessionPreamble(history, idle) + msg.Text
				log.Printf("[BOT_MANAGER] Thread-less session %s idle %s — starting new conversation with %d context turn(s)",
					oldSessionID, humanDuration(idle), len(history))
			}
			// If the prior session somehow is still marked Running (hung
			// agent, crash mid-turn), cancel it before starting fresh so
			// its resources free up.
			if status == chathistory.BotSessionStatusRunning || status == chathistory.BotSessionStatusAwaitingPlanApproval {
				m.cancelSession(active, "")
			}
			go m.startNewSessionDirect(msg, oldThreadID) // no resumeSessionID → mint fresh
			return
		}
	}

	// The message is continuing this conversation. Refresh the durable clock
	// so a later server restart applies the inactivity window from the latest
	// real chat activity rather than from when the session was first created.
	if !supportsThreads {
		m.persistBotSessionBinding(active, time.Now())
	}

	switch status {
	case chathistory.BotSessionStatusRunning, chathistory.BotSessionStatusAwaitingPlanApproval:
		// Check if session is waiting for user input (any blocking event)
		if awaiting {
			m.handleBlockingResponse(active, msg)
			return
		}
		if m.ignoreOtherUserMention(active, msg) {
			return
		}

		if !supportsThreads {
			active.mu.Lock()
			sid := active.SessionID
			uid := active.UserID
			active.mu.Unlock()
			// Share the app's /api/query entry point. It chooses retained tmux
			// steering or a normal turn from actual execution state. A bot-local
			// busy flag must never hold a user's steering message behind a turn.
			if !m.startFollowUpTurn(active, msg, sid, uid, "threadless") {
				if connector := m.GetConnector(active.Platform); connector != nil {
					connector.SendThreadMessage(context.Background(), active.ThreadID, "Couldn't deliver your message: the chat session is unavailable. Please try again.")
				}
			}
			return
		}
		// For @mentions, always inject as follow-up.
		// For plain thread replies, only inject if it's a single-user thread (the bot's original requester).
		// In multi-user threads, require @mention to avoid accidental triggers.
		if !msg.IsMention {
			if m.ignoreMultiUserNonMention(active, msg) {
				return
			}
			log.Printf("[BOT_MANAGER] Accepting non-mention reply from %s (single-user thread) in session %s", msg.UserID, active.SessionID)
		}
		// Inject follow-up message into the running session
		active.mu.Lock()
		sid := active.SessionID
		uid := active.UserID
		active.mu.Unlock()
		if m.followUpSession != nil && sid != "" {
			m.startFollowUpTurn(active, msg, sid, uid, "threaded", m.threadCatchupContext(msg, active.ThreadID))
		} else {
			log.Printf("[BOT_MANAGER] Cannot send follow-up: followUpSession=%v sessionID=%s", m.followUpSession != nil, sid)
		}
	case chathistory.BotSessionStatusCompleted, chathistory.BotSessionStatusFailed:
		// An outstanding blocking prompt still gets its answer: like the
		// running branch, blocking responses bypass the mention guard below.
		if awaiting {
			m.handleBlockingResponse(active, msg)
			return
		}
		if m.ignoreOtherUserMention(active, msg) {
			return
		}
		if m.ignoreMultiUserNonMention(active, msg) {
			return
		}
		threadID := active.ThreadID
		if msg.PresetWorkflow == nil {
			msg.PresetWorkflow = botRouteFromActive(active)
		}
		// A completed turn is not a new conversation. Keep the durable
		// session identity for both channel threads and thread-less chats:
		// private CLI directories and native resume handles are keyed to it.
		// Each query still has its own execution ID and run logs.
		if oldSessionID != "" {
			withBotSender(&msg)
			msg.Text = m.withBotRuntimeState(active, msg.Text)
			if supportsThreads {
				// The conversation continues, so the first-turn history
				// prepend does not run: add what was posted since.
				msg.Text = withThreadCatchup(m.threadCatchupContext(msg, threadID), msg.Text)
			}
			go m.startNewSessionDirect(msg, threadID, oldSessionID)
			return
		}
		go m.startNewSessionDirect(msg, threadID)
	}
}

func (m *BotConversationManager) handleBotControlWithoutSession(msg BotIncomingMessage, threadID ThreadID, supportsThreads bool) bool {
	requireControlPrefix := !supportsThreads
	if !isSessionStatusCommand(msg.Text, requireControlPrefix) {
		if _, ok := parseBotDetailModeCommand(msg.Text, requireControlPrefix); !ok {
			return false
		}
	}
	connector := m.GetConnector(msg.Platform)
	if connector == nil {
		return true
	}
	reply := "No active bot session in this thread."
	if _, ok := parseBotDetailModeCommand(msg.Text, requireControlPrefix); ok {
		if strings.EqualFold(msg.Platform, "slack") {
			reply = "Slack workflow channels always send full details. Start a workflow run first."
		} else {
			reply = "No active bot session in this thread. Start a workflow run first, then use `@full` or `@concise` to change message detail for that run."
		}
	} else if isSessionStatusCommand(msg.Text, requireControlPrefix) {
		reply = m.formatBotStatusReply(m.resolveWorkspaceUserID(msg), "", false, "", false, botResumeFilterFromRoute(msg.PresetWorkflow))
	}
	connector.SendThreadMessage(context.Background(), threadID, reply)
	return true
}

func (m *BotConversationManager) handleBotResumeCommand(msg BotIncomingMessage, threadID ThreadID, threadKey, selector string, filter BotResumeFilter) {
	connector := m.GetConnector(msg.Platform)
	if connector == nil {
		return
	}
	workspaceUserID := m.resolveWorkspaceUserID(msg)

	// Bare `@resume` opens the picker instead of silently binding to the most
	// recent session — the user chooses which running session to connect to.
	if strings.TrimSpace(selector) == "" {
		connector.SendThreadMessage(context.Background(), threadID, m.formatBotResumePicker(workspaceUserID, filter))
		return
	}

	if m.resumeTarget == nil {
		connector.SendThreadMessage(context.Background(), threadID, "Resume is not available for this bot connector.")
		return
	}
	target, err := m.resumeTarget(context.Background(), workspaceUserID, selector, filter)
	if err != nil {
		connector.SendThreadMessage(context.Background(), threadID, fmt.Sprintf("Could not resume that chat: %v", err))
		return
	}
	if target == nil || strings.TrimSpace(target.SessionID) == "" {
		connector.SendThreadMessage(context.Background(), threadID, "No matching active chat found. Use `@resume <session-id>` or open a chat in the dashboard first.")
		return
	}

	if target.ProfileRoute != nil {
		if m.profileTurn == nil {
			connector.SendThreadMessage(context.Background(), threadID, "Product resume is unavailable.")
			return
		}
		msg.PresetProfile = target.ProfileRoute
		msg.ResumeSessionID = target.SessionID
		_, resolvedSession, handled, err := m.profileTurn(context.Background(), workspaceUserID, msg, threadID)
		if err != nil || !handled || resolvedSession != target.SessionID {
			connector.SendThreadMessage(context.Background(), threadID, "Cannot restore that product conversation.")
			return
		}
	}
	status := botSessionStatusFromActiveStatus(target.Status)
	active := &activeBotSession{
		SessionID:     strings.TrimSpace(target.SessionID),
		UserID:        firstNonEmpty(strings.TrimSpace(target.UserID), workspaceUserID),
		Status:        status,
		Platform:      msg.Platform,
		ThreadID:      threadID,
		RouteKey:      botMessageRouteKey(msg),
		Metadata:      botMetaFromMsg(msg, threadID),
		LastActivity:  time.Now(),
		AgentMode:     strings.TrimSpace(target.AgentMode),
		PresetQueryID: strings.TrimSpace(target.PresetQueryID),
		WorkspacePath: strings.TrimSpace(target.WorkspacePath),
		PhaseID:       strings.TrimSpace(target.PhaseID),
		WorkshopMode:  strings.TrimSpace(target.WorkshopMode),
	}

	if target.ProfileRoute != nil {
		active.profileTurn = true
		active.profileRoute = target.ProfileRoute
	}
	if msg.PresetWorkflow != nil {
		active.BotRouteGrant = NormalizeBotRouteGrant(msg.PresetWorkflow.BotGrant, msg.PresetWorkflow.WorkshopMode)
	}
	// Connecting to a live session means the user wants to watch it, so default
	// to full detail — otherwise concise mode suppresses every workflow-scoped
	// event (step starts/ends, runtime progress) and the thread stays silent
	// until the final answer.
	live := botResumeTargetIsLive(target)
	if live || strings.EqualFold(msg.Platform, "slack") {
		active.sendFullDetails = true
	}

	m.mu.Lock()
	if previous := m.sessions[threadKey]; previous != nil {
		previous.mu.Lock()
		cancel := previous.cancel
		previous.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	m.sessions[threadKey] = active
	m.mu.Unlock()
	m.persistBotSessionBinding(active, time.Now())

	// Start streaming live events to this thread whenever there's anything in
	// flight to mirror — a running/awaiting workflow or background agents still
	// working. This is what turns @resume into "I get notifications here".
	if status == chathistory.BotSessionStatusRunning || status == chathistory.BotSessionStatusAwaitingPlanApproval || target.HasBackgroundActivity {
		m.startMirroringExistingSession(active)
	}

	connector.SendThreadMessage(context.Background(), threadID, formatBotResumeAck(target))
	log.Printf("[BOT_MANAGER] Bound %s thread %s to existing session %s (status=%s mode=%s)",
		msg.Platform, threadKey, active.SessionID, target.Status, target.AgentMode)
}

func botSessionStatusFromActiveStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "inactive", "dismissed", "stopped":
		return chathistory.BotSessionStatusCompleted
	case "failed", "error":
		return chathistory.BotSessionStatusFailed
	case "awaiting_plan_approval":
		return chathistory.BotSessionStatusAwaitingPlanApproval
	default:
		return chathistory.BotSessionStatusRunning
	}
}

func formatBotResumeAck(target *BotResumeTarget) string {
	label := botResumeTargetLabel(*target)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Connected to *%s*", label))
	if status := strings.TrimSpace(target.Status); status != "" {
		sb.WriteString(fmt.Sprintf(" (%s)", status))
	}
	sb.WriteString(".")
	if activity := strings.TrimSpace(target.Activity); activity != "" {
		sb.WriteString(fmt.Sprintf("\nCurrently: %s.", activity))
	}
	if botResumeTargetIsLive(target) {
		sb.WriteString("\nYou'll get live progress here. Send a message any time to chat with it.")
	} else {
		sb.WriteString("\nSend your next message here to continue this chat.")
	}
	sb.WriteString(fmt.Sprintf("\n_session `%s`_", target.SessionID))
	return sb.String()
}

// botResumeTargetIsLive reports whether the target is actively doing work, so
// resuming it will stream notifications rather than just bind a finished chat.
func botResumeTargetIsLive(target *BotResumeTarget) bool {
	if target == nil {
		return false
	}
	if target.HasBackgroundActivity {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(target.Status)) {
	case "running", "paused", "waiting_for_input", "awaiting_plan_approval":
		return true
	default:
		return false
	}
}

func (m *BotConversationManager) setActiveBotDetailMode(active *activeBotSession, full bool, customReply ...string) {
	active.mu.Lock()
	active.sendFullDetails = full
	filter := active.eventFilter
	threadID := active.ThreadID
	platform := active.Platform
	active.LastActivity = time.Now()
	active.mu.Unlock()

	if filter != nil {
		filter.SetSendFullDetails(full)
	}
	mode := "concise"
	reply := "Concise mode on. I'll send only the workflow-builder answer plus required prompts/errors."
	if full {
		mode = "full"
		reply = "Full mode on. I'll include workflow runtime details for this session."
	}
	if len(customReply) > 0 && strings.TrimSpace(customReply[0]) != "" {
		reply = strings.TrimSpace(customReply[0])
	}
	log.Printf("[BOT_MANAGER] Bot detail mode set to %s for session %s", mode, active.SessionID)
	if connector := m.GetConnector(platform); connector != nil {
		connector.SendThreadMessage(context.Background(), threadID, reply)
	}
}

// threadContext, when set, is a catch-up block of thread messages posted
// since the agent's last turn; it is prepended to the turn text.
func (m *BotConversationManager) startFollowUpTurn(active *activeBotSession, msg BotIncomingMessage, sessionID, userID, source string, threadContext ...string) bool {
	catchup := ""
	if len(threadContext) > 0 {
		catchup = threadContext[0]
	}
	if m.followUpSession == nil || sessionID == "" {
		log.Printf("[BOT_MANAGER] Cannot send follow-up: followUpSession=%v sessionID=%s", m.followUpSession != nil, sessionID)
		return false
	}
	if source == "" {
		source = "follow-up"
	}
	log.Printf("[BOT_MANAGER] Sending %s to session %s: %s", source, sessionID, botTruncate(msg.Text, 80))
	active.mu.Lock()
	builderDone := active.builderDone
	active.mu.Unlock()
	// Steering belongs to the current turn. Preserve its completion tracking
	// and already-forwarded text; only a completed builder starts a new turn.
	if builderDone {
		m.resetActiveForNewTurn(active)
	}
	active.mu.Lock()
	active.LastActivity = time.Now()
	if msg.ChannelID != "" && msg.MessageTS != "" {
		ack := ThreadID{Platform: msg.Platform, ChannelID: msg.ChannelID, ThreadTS: msg.MessageTS, ConnectionID: msg.ConnectionID}
		found := false
		for _, previous := range active.followUpAcks {
			if previous == ack {
				found = true
				break
			}
		}
		if !found {
			active.followUpAcks = append(active.followUpAcks, ack)
		}
	}
	active.mu.Unlock()
	m.acknowledgeBotMessage(ThreadID{Platform: msg.Platform, ChannelID: msg.ChannelID, ConnectionID: msg.ConnectionID}, msg.MessageTS)
	go func() {
		followCtx, followCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer followCancel()
		active.mu.Lock()
		threadID := active.ThreadID
		platform := active.Platform
		active.mu.Unlock()
		withBotSender(&msg)
		err := m.followUpSession(followCtx, m.turnRequestForActive(active, withThreadCatchup(catchup, m.withBotRuntimeState(active, msg.Text)), userID, platform, threadID), sessionID, userID)
		if err != nil {
			log.Printf("[BOT_MANAGER] Follow-up failed: %v", err)
			if connector := m.GetConnector(platform); connector != nil {
				connector.SendThreadMessage(context.Background(), threadID, botFollowUpFailureMessage(err))
			}
		} else {
		}
	}()
	return true
}

func (m *BotConversationManager) withBotRuntimeState(active *activeBotSession, userText string) string {
	if active == nil {
		return userText
	}
	active.mu.Lock()
	status := active.Status
	builderDone := active.builderDone
	pending := active.pendingWorkflows
	profileTurn := active.profileTurn
	active.mu.Unlock()
	if profileTurn {
		// A product conversation reads as the product's own chat; the
		// workflow-runtime preamble would be noise there.
		return userText
	}

	if pending > 0 {
		return fmt.Sprintf("## Bot Connector Runtime State\n%d workflow/sub-agent item(s) are still pending for this bot conversation. If the user asks to wait or asks for status, answer based on this pending state. Treat the message as a runtime request and follow the deployed-channel guidance.\n\n---\n\n## Current Message\n%s", pending, userText)
	}
	if builderDone || status == chathistory.BotSessionStatusCompleted || status == chathistory.BotSessionStatusFailed {
		return fmt.Sprintf("## Bot Connector Runtime State\nNo workflow/sub-agent work is currently pending for this bot conversation. The previous builder/workflow turn status is %s. If the user asks to wait, asks for status, or asks what happened, answer from the existing conversation/results instead of promising a future ping. Treat the message as a runtime request and follow the deployed-channel guidance.\n\n---\n\n## Current Message\n%s", status, userText)
	}
	return userText
}

// handleBlockingResponse handles a user response to a blocking event (plan_approval, human feedback, etc.)
func (m *BotConversationManager) handleBlockingResponse(active *activeBotSession, msg BotIncomingMessage) {
	text := botNormalizeText(msg.Text)

	active.mu.Lock()
	blockingEvt := active.blockingEventType
	blockingRequestID := active.blockingRequestID
	sid := active.SessionID
	uid := active.UserID
	threadID := active.ThreadID
	platform := active.Platform
	active.mu.Unlock()

	switch blockingEvt {
	case "plan_approval":
		if isPlanApprovalResponse(text) {
			log.Printf("[BOT_MANAGER] Plan approved for session %s", sid)
			m.clearBlockingState(active)
			if m.followUpSession != nil && sid != "" {
				go func() {
					followCtx, followCancel := context.WithTimeout(context.Background(), 5*time.Minute)
					defer followCancel()
					err := m.followUpSession(followCtx, m.turnRequestForActive(active, "Approved. Execute the plan.", uid, platform, threadID), sid, uid)
					if err != nil {
						log.Printf("[BOT_MANAGER] Plan approval follow-up failed: %v", err)
					}
				}()
			}
			return
		} else if isPlanRejectionResponse(text) {
			log.Printf("[BOT_MANAGER] Plan rejected for session %s", sid)
			m.clearBlockingState(active)
			m.cancelSession(active, "Plan rejected. Let me know if you'd like to try a different approach!")
			return
		}
		// Not a clear approve/reject — send as feedback to the agent
		if m.followUpSession != nil && sid != "" {
			go func() {
				followCtx, followCancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer followCancel()
				err := m.followUpSession(followCtx, m.turnRequestForActive(active, msg.Text, uid, platform, threadID), sid, uid)
				if err != nil {
					log.Printf("[BOT_MANAGER] Plan feedback follow-up failed: %v", err)
				}
			}()
		}

	default:
		if blockingEvt == "blocking_human_feedback" && m.submitBlockingHumanFeedbackResponse(active, msg, blockingRequestID) {
			return
		}
		// Unknown blocking event, or an older human-feedback event without a
		// request ID — preserve the prior follow-up behavior as a fallback.
		log.Printf("[BOT_MANAGER] Responding to %s for session %s: %s", blockingEvt, sid, botTruncate(msg.Text, 80))
		m.clearBlockingState(active)
		if m.followUpSession != nil && sid != "" {
			go func() {
				followCtx, followCancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer followCancel()
				err := m.followUpSession(followCtx, m.turnRequestForActive(active, msg.Text, uid, platform, threadID), sid, uid)
				if err != nil {
					log.Printf("[BOT_MANAGER] Blocking response follow-up failed: %v", err)
				}
			}()
		}
	}
}

func (m *BotConversationManager) submitBlockingHumanFeedbackResponse(active *activeBotSession, msg BotIncomingMessage, requestID string) bool {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return false
	}
	platform := strings.TrimSpace(msg.Platform)
	if platform == "" {
		active.mu.Lock()
		platform = active.Platform
		active.mu.Unlock()
	}
	if platform == "" {
		platform = "bot"
	}
	if err := GetNotificationManager().ReceiveNotification(requestID, msg.Text, platform); err != nil {
		log.Printf("[BOT_MANAGER] Failed to submit blocking human feedback request %s via %s: %v", requestID, platform, err)
		return false
	}
	log.Printf("[BOT_MANAGER] Submitted blocking human feedback request %s via %s", requestID, platform)
	m.clearBlockingState(active)
	return true
}

// handleBlockingResponseSync handles a user response to a blocking event synchronously (web simulator)
func (m *BotConversationManager) handleBlockingResponseSync(ctx context.Context, active *activeBotSession, msg BotIncomingMessage, threadID ThreadID) (*SyncMessageResult, error) {
	text := botNormalizeText(msg.Text)

	active.mu.Lock()
	blockingEvt := active.blockingEventType
	blockingRequestID := active.blockingRequestID
	sid := active.SessionID
	uid := active.UserID
	activeThreadID := active.ThreadID
	platform := active.Platform
	active.mu.Unlock()

	switch blockingEvt {
	case "plan_approval":
		if isPlanApprovalResponse(text) {
			log.Printf("[BOT_MANAGER] HandleMessageSync: plan approved for session %s", sid)
			m.clearBlockingState(active)
			if m.followUpSession != nil {
				err := m.followUpSession(ctx, m.turnRequestForActive(active, "Approved. Execute the plan.", uid, platform, activeThreadID), sid, uid)
				if err != nil {
					return nil, fmt.Errorf("plan approval follow-up failed: %w", err)
				}
			}
			return &SyncMessageResult{
				Type:         "follow_up",
				ThreadID:     threadID.ThreadTS,
				SessionID:    sid,
				ThreadOffset: m.getThreadOffset(threadID),
			}, nil
		} else if isPlanRejectionResponse(text) {
			log.Printf("[BOT_MANAGER] HandleMessageSync: plan rejected for session %s", sid)
			m.clearBlockingState(active)
			rejectMsg := "Plan rejected. Let me know if you'd like to try a different approach!"
			m.cancelSession(active, rejectMsg)
			return &SyncMessageResult{
				Type:     "conversation",
				Response: rejectMsg,
				ThreadID: threadID.ThreadTS,
			}, nil
		}
		// Not a clear approve/reject — send as feedback
		if m.followUpSession != nil {
			err := m.followUpSession(ctx, m.turnRequestForActive(active, msg.Text, uid, platform, activeThreadID), sid, uid)
			if err != nil {
				return nil, fmt.Errorf("plan feedback follow-up failed: %w", err)
			}
		}
		return &SyncMessageResult{
			Type:         "follow_up",
			ThreadID:     threadID.ThreadTS,
			SessionID:    sid,
			ThreadOffset: m.getThreadOffset(threadID),
		}, nil

	default:
		if blockingEvt == "blocking_human_feedback" && m.submitBlockingHumanFeedbackResponse(active, msg, blockingRequestID) {
			return &SyncMessageResult{
				Type:         "follow_up",
				ThreadID:     threadID.ThreadTS,
				SessionID:    sid,
				ThreadOffset: m.getThreadOffset(threadID),
			}, nil
		}
		// Unknown blocking event, or an older human-feedback event without a
		// request ID — preserve the prior follow-up behavior as a fallback.
		log.Printf("[BOT_MANAGER] HandleMessageSync: responding to %s for session %s", blockingEvt, sid)
		m.clearBlockingState(active)
		if m.followUpSession != nil {
			err := m.followUpSession(ctx, m.turnRequestForActive(active, msg.Text, uid, platform, activeThreadID), sid, uid)
			if err != nil {
				return nil, fmt.Errorf("blocking response follow-up failed: %w", err)
			}
		}
		return &SyncMessageResult{
			Type:         "follow_up",
			ThreadID:     threadID.ThreadTS,
			SessionID:    sid,
			ThreadOffset: m.getThreadOffset(threadID),
		}, nil
	}
}

// clearBlockingState resets the blocking state on the active session and event filter
func (m *BotConversationManager) clearBlockingState(active *activeBotSession) {
	active.mu.Lock()
	active.awaitingUserInput = false
	active.blockingEventType = ""
	active.blockingRequestID = ""
	active.Status = chathistory.BotSessionStatusRunning
	ef := active.eventFilter
	active.mu.Unlock()
	if ef != nil {
		ef.ClearBlockingState()
	}
}

func isSessionStatusCommand(text string, requirePrefix bool) bool {
	normalized, ok := botControlText(text, requirePrefix)
	if !ok {
		return false
	}
	return normalized == "status"
}

func parseBotDetailModeCommand(text string, requirePrefix bool) (string, bool) {
	normalized, ok := botControlText(text, requirePrefix)
	if !ok {
		return "", false
	}
	switch normalized {
	case "full", "verbose", "details", "details on", "full details", "full on", "big", "big messages":
		return "full", true
	case "concise", "short", "brief", "summary", "details off", "full off", "small", "small messages":
		return "concise", true
	default:
		return "", false
	}
}

func parseBotResumeCommand(text string, requirePrefix bool) (string, bool) {
	normalized, ok := botControlText(text, requirePrefix)
	if !ok {
		return "", false
	}
	// Bare `@resume` / `@continue` (no selector) opens the picker: list the
	// running sessions and let the user choose one. An explicit selector
	// (`@resume 2`, `@resume <session-id>`) connects directly.
	if normalized == "resume" || normalized == "continue" {
		return "", true
	}
	for _, prefix := range []string{"resume ", "continue "} {
		if strings.HasPrefix(normalized, prefix) {
			selector := strings.TrimSpace(strings.TrimPrefix(normalized, prefix))
			return selector, true
		}
	}
	return "", false
}

func botControlText(text string, requirePrefix bool) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if requirePrefix && !strings.HasPrefix(trimmed, "@") {
		return "", false
	}
	trimmed = strings.TrimPrefix(trimmed, "@")
	return botNormalizeText(trimmed), true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (m *BotConversationManager) formatBotStatusReply(userID, status string, awaiting bool, blockingEventType string, sendFullDetails bool, filter BotResumeFilter) string {
	detail := "concise"
	if sendFullDetails {
		detail = "full"
	}
	suffix := fmt.Sprintf("\nDetail mode: %s. Use `@full` or `@concise` to switch.", detail)
	resumeList := m.formatResumeList(userID, filter)
	if m.runningWorkflows != nil {
		if workflows := m.runningWorkflows(userID); len(workflows) > 0 {
			return formatRunningWorkflowsStatus(workflows) + resumeList + suffix
		}
	}
	return formatThreadlessSessionStatus(status, awaiting, blockingEventType) + resumeList + suffix
}

func (m *BotConversationManager) formatResumeList(userID string, filter BotResumeFilter) string {
	if m.resumeList == nil || strings.TrimSpace(userID) == "" {
		return ""
	}
	targets, err := m.resumeList(context.Background(), userID, filter)
	if err != nil {
		log.Printf("[BOT_MANAGER] Failed to list resumable bot sessions: %v", err)
		return ""
	}
	if len(targets) == 0 {
		return ""
	}
	var sb strings.Builder
	if strings.TrimSpace(filter.WorkspacePath) != "" || strings.TrimSpace(filter.PresetQueryID) != "" {
		sb.WriteString("\n\nResumable chats for this workflow:")
	} else {
		sb.WriteString("\n\nResumable chats:")
	}
	for i, target := range targets {
		if i >= 5 {
			sb.WriteString(fmt.Sprintf("\n+%d more", len(targets)-i))
			break
		}
		label := botResumeTargetLabel(target)
		status := strings.TrimSpace(target.Status)
		if status == "" {
			status = "active"
		}
		sb.WriteString(fmt.Sprintf("\n%d. %s - %s", i+1, label, status))
	}
	sb.WriteString("\nUse `@resume 1` to continue one here.")
	return sb.String()
}

func botResumeTargetLabel(target BotResumeTarget) string {
	// Prefer a human-friendly workflow/preset name over the raw query or the
	// session id — the session id alone looks like an opaque "schedule id".
	label := strings.TrimSpace(target.WorkflowName)
	if label == "" {
		label = strings.TrimSpace(target.Query)
	}
	if label == "" {
		label = workflowNameFromBotWorkspacePath(target.WorkspacePath)
	}
	if label == "" {
		label = strings.TrimSpace(target.AgentMode)
	}
	if label == "" {
		label = strings.TrimSpace(target.SessionID)
	}
	runes := []rune(label)
	if len(runes) > 72 {
		label = string(runes[:72]) + "..."
	}
	return label
}

// workflowNameFromBotWorkspacePath extracts the trailing workflow folder name
// from a workspace path like "Workflow/report" -> "report".
func workflowNameFromBotWorkspacePath(workspacePath string) string {
	trimmed := strings.Trim(strings.TrimSpace(workspacePath), "/")
	if trimmed == "" {
		return ""
	}
	parts := strings.Split(trimmed, "/")
	return parts[len(parts)-1]
}

// botResumeTargetDescription renders one picker line: workflow name, status,
// and what it's currently doing.
func botResumeTargetDescription(target BotResumeTarget) string {
	status := strings.TrimSpace(target.Status)
	if status == "" {
		status = "active"
	}
	desc := fmt.Sprintf("*%s* — %s", botResumeTargetLabel(target), status)
	if activity := strings.TrimSpace(target.Activity); activity != "" {
		desc += fmt.Sprintf(" · %s", activity)
	}
	return desc
}

// formatBotResumePicker lists the sessions the user can connect to. Bare
// `@resume` sends this so the user picks a session instead of being silently
// bound to whichever ran most recently.
func (m *BotConversationManager) formatBotResumePicker(userID string, filter BotResumeFilter) string {
	if m.resumeList == nil || strings.TrimSpace(userID) == "" {
		return "Resume is not available for this bot connector."
	}
	targets, err := m.resumeList(context.Background(), userID, filter)
	if err != nil {
		log.Printf("[BOT_MANAGER] Failed to list resumable bot sessions: %v", err)
		return "Couldn't list sessions right now. Please try again."
	}
	if len(targets) == 0 {
		return "No active sessions to connect to right now. Start a workflow or open a chat in the dashboard, then send `@resume`."
	}
	var sb strings.Builder
	if strings.TrimSpace(filter.WorkspacePath) != "" || strings.TrimSpace(filter.PresetQueryID) != "" {
		sb.WriteString("Sessions you can connect to for this workflow:")
	} else {
		sb.WriteString("Sessions you can connect to:")
	}
	const maxPickerItems = 9
	for i, target := range targets {
		if i >= maxPickerItems {
			sb.WriteString(fmt.Sprintf("\n+%d more", len(targets)-i))
			break
		}
		sb.WriteString(fmt.Sprintf("\n%d. %s", i+1, botResumeTargetDescription(target)))
	}
	sb.WriteString("\n\nReply `@resume <number>` to connect — you'll get live updates here and can chat with it.")
	return sb.String()
}

func formatRunningWorkflowsStatus(workflows []BotRunningWorkflow) string {
	var sb strings.Builder
	count := len(workflows)
	if count == 1 {
		sb.WriteString("1 workflow running")
	} else {
		sb.WriteString(fmt.Sprintf("%d workflows running", count))
	}
	for i, wf := range workflows {
		if i >= 6 {
			sb.WriteString(fmt.Sprintf("\n+%d more", count-i))
			break
		}
		label := strings.TrimSpace(wf.WorkflowLabel)
		if label == "" && wf.WorkspacePath != "" {
			label = strings.TrimPrefix(wf.WorkspacePath, "Workflow/")
		}
		if label == "" {
			label = "workflow"
		}
		detail := strings.TrimSpace(wf.CurrentStepTitle)
		if detail == "" {
			detail = strings.TrimSpace(wf.PhaseName)
		}
		if detail == "" {
			detail = strings.TrimSpace(wf.Title)
		}
		if detail == "" {
			detail = strings.TrimSpace(wf.Status)
		}
		if detail == "" {
			detail = "running"
		}
		sb.WriteString(fmt.Sprintf("\n%d. %s - %s", i+1, label, detail))
		if !wf.StartedAt.IsZero() {
			sb.WriteString(fmt.Sprintf(" (%s)", shortElapsed(time.Since(wf.StartedAt))))
		}
		if wf.BackgroundAgents > 0 {
			sb.WriteString(fmt.Sprintf(", %d bg agent", wf.BackgroundAgents))
			if wf.BackgroundAgents != 1 {
				sb.WriteString("s")
			}
		} else if wf.HasBackgroundWork {
			sb.WriteString(", bg work")
		}
	}
	return sb.String()
}

func formatThreadlessSessionStatus(status string, awaiting bool, blockingEventType string) string {
	if awaiting {
		if blockingEventType != "" {
			return fmt.Sprintf("Session waiting for input (%s).", blockingEventType)
		}
		return "Session waiting for input."
	}
	switch status {
	case chathistory.BotSessionStatusRunning, chathistory.BotSessionStatusAwaitingPlanApproval:
		return "Session running."
	case chathistory.BotSessionStatusCompleted:
		return "Session completed."
	case chathistory.BotSessionStatusFailed:
		return "Session failed."
	default:
		if strings.TrimSpace(status) == "" {
			return "No active session."
		}
		return fmt.Sprintf("Session status: %s.", status)
	}
}

func shortElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	}
}

// SyncMessageResult is the synchronous result of HandleMessageSync
type SyncMessageResult struct {
	Type         string `json:"type"`               // "conversation" or "follow_up"
	Response     string `json:"response,omitempty"` // text reply for conversation
	ThreadID     string `json:"thread_id"`
	SessionID    string `json:"session_id,omitempty"`     // internal chat session ID (for follow_up)
	BotSessionID string `json:"bot_session_id,omitempty"` // set when awaiting confirmation
	ThreadOffset int    `json:"thread_offset,omitempty"`  // current thread message count (for polling init)
}

// HandleMessageSync processes a message synchronously (web simulator).
// Every message either routes to an existing session or starts a new one immediately.
func (m *BotConversationManager) HandleMessageSync(ctx context.Context, msg BotIncomingMessage, threadID ThreadID) (*SyncMessageResult, error) {
	// Check for existing session on this thread
	m.mu.RLock()
	active, exists := m.sessions[threadID.Key()]
	m.mu.RUnlock()

	if !m.authorizeWorkflowRouteForMessage(ctx, &msg, threadID, active) {
		return &SyncMessageResult{
			Type:     "conversation",
			Response: "You don't have access to this workflow.",
			ThreadID: threadID.ThreadTS,
		}, nil
	}

	// Snapshot active session fields under lock for safe access
	var status, sessionID string
	var awaitingInput bool
	if exists {
		active.mu.Lock()
		status = active.Status
		sessionID = active.SessionID
		awaitingInput = active.awaitingUserInput
		active.mu.Unlock()
	}

	// 1. Running session awaiting user input (blocking event) → handle response
	if exists && awaitingInput && sessionID != "" {
		return m.handleBlockingResponseSync(ctx, active, msg, threadID)
	}

	// 2. Running session (not awaiting input) → inject follow-up
	if exists && status == chathistory.BotSessionStatusRunning && sessionID != "" && !awaitingInput {
		active.mu.Lock()
		uid := active.UserID
		active.mu.Unlock()
		log.Printf("[BOT_MANAGER] HandleMessageSync: found active session %s (status=%s) for thread %s", sessionID, status, threadID.Key())
		if m.followUpSession != nil {
			log.Printf("[BOT_MANAGER] HandleMessageSync: injecting follow-up into session %s: %s", sessionID, botTruncate(msg.Text, 80))
			m.resetActiveForNewTurn(active)
			err := m.followUpSession(ctx, m.turnRequestForActive(active, msg.Text, uid, msg.Platform, threadID), sessionID, uid)
			if err != nil {
				return nil, fmt.Errorf("follow-up failed: %w", err)
			}
		}
		return &SyncMessageResult{
			Type:         "follow_up",
			ThreadID:     threadID.ThreadTS,
			SessionID:    sessionID,
			ThreadOffset: m.getThreadOffset(threadID),
		}, nil
	}

	// 3. No active session (or completed/failed) → start new session immediately
	log.Printf("[BOT_MANAGER] HandleMessageSync: starting new session for thread %s", threadID.Key())
	restoredConversationSessionID := ""
	if exists && (status == chathistory.BotSessionStatusCompleted || status == chathistory.BotSessionStatusFailed) {
		restoredConversationSessionID = sessionID
	}

	// Resolve workspace user ID for scoped secret resolution and access checks
	workspaceUserID := m.resolveWorkspaceUserID(msg)

	newSessionID := newBotSessionID(msg.Platform)
	botMeta := botMetaFromMsg(msg, threadID)

	// Load thread history for context continuity (e.g., user replies after hours)
	queryWithHistory := msg.Text
	if restoredConversationSessionID == "" {
		queryWithHistory = m.buildQueryWithThreadHistory(msg.Text, msg.Platform, threadID)
	}
	if msg.PresetWorkflow == nil {
		msg.PresetWorkflow = botRouteFromActive(active)
	}
	queryReq := m.buildQueryRequest(queryWithHistory, workspaceUserID, msg.ChannelID, msg.PresetWorkflow, msg.Platform, "", threadID)
	applyBotQueryRequestMetadata(queryReq, botMeta)
	addRestoredConversationSessionID(queryReq, restoredConversationSessionID)
	sendFullDetails := botFullDetailsFromRequest(queryReq)

	// Track the live session in memory. Connectors with durable bindings also
	// receive the session pointer below so it can be recovered after restart.
	m.mu.Lock()
	activeTask := &activeBotSession{
		SessionID:       newSessionID,
		UserID:          workspaceUserID,
		Status:          chathistory.BotSessionStatusRunning,
		Platform:        msg.Platform,
		ThreadID:        threadID,
		Metadata:        botMeta,
		LastActivity:    time.Now(),
		sendFullDetails: sendFullDetails,
		RouteKey:        botMessageRouteKey(msg),
	}
	startedAt := activeTask.LastActivity
	applyBotRequestMetadata(activeTask, queryReq)
	m.sessions[threadID.Key()] = activeTask
	m.mu.Unlock()
	m.persistBotSessionBinding(activeTask, startedAt)

	// Start the session in background
	go m.runSession(activeTask, queryReq)

	return &SyncMessageResult{
		Type:         "follow_up",
		ThreadID:     threadID.ThreadTS,
		SessionID:    newSessionID,
		ThreadOffset: m.getThreadOffset(threadID),
	}, nil
}

// startNewSessionDirect creates a unified bot chat session and starts it
// immediately (async path, used by Slack / @mention flows). The first optional
// resumeSessionID reuses an existing chat session; thread-less platforms
// (WhatsApp, Telegram) use this to carry conversation history between messages.
// The second optional resumeSessionID starts a fresh chat session but restores
// native coding-agent runtime from the previous persisted session.
func (m *BotConversationManager) startNewSessionDirect(msg BotIncomingMessage, threadID ThreadID, resumeSessionID ...string) {
	if len(resumeSessionID) > 0 {
		msg.ResumeSessionID = resumeSessionID[0]
	}
	// A thread keeps the title of its first message across resumes. A DM
	// continues the sender's own chat, which keeps its own name.
	sessionTitle := ""
	if connector := m.GetConnector(msg.Platform); connector != nil && msg.ResumeSessionID == "" && !msg.DirectMessage {
		sessionTitle = botSessionTitle(msg, botConnectorChannelName(context.Background(), connector, threadID))
	}
	withBotSender(&msg)
	workspaceUserID := m.resolveWorkspaceUserID(msg)
	profilePresetRoute := msg.PresetWorkflow
	if route := msg.PresetWorkflow; route != nil && strings.TrimSpace(route.ProfileID) != "" {
		label := strings.TrimSpace(route.ProfileLabel)
		if label == "" {
			label = strings.TrimSpace(route.ProfileID)
		}
		msg.PresetProfile = &ProfileRoute{
			ProfileID:       strings.TrimSpace(route.ProfileID),
			ConversationKey: strings.TrimSpace(route.ConversationKey),
			UploadFolder:    strings.TrimSpace(route.WorkspacePath),
			Label:           label,
			WorkspaceUserID: routeWorkspaceUserID(*route, strings.TrimSpace(msg.WorkspaceUserID)),
		}
		msg.PresetWorkflow = nil
	}

	// Timing of the steps before the agent starts, so a slow bot turn shows which step was slow.
	botStart := time.Now()
	botStep := func(step string) {
		if elapsed := time.Since(botStart); elapsed > 2*time.Second {
			log.Printf("[BOT_TIMING] %s: %s after the message reached the bot manager", step, elapsed.Round(100*time.Millisecond))
		}
	}
	sessionID := newBotSessionID(msg.Platform)
	if len(resumeSessionID) > 0 && resumeSessionID[0] != "" {
		sessionID = resumeSessionID[0]
		log.Printf("[BOT_MANAGER] Reusing session %s for thread %s (history preserved)", sessionID, threadID.Key())
	}
	restoredConversationSessionID := ""
	if len(resumeSessionID) > 1 {
		restoredConversationSessionID = strings.TrimSpace(resumeSessionID[1])
	}
	botMeta := botMetaFromMsg(msg, threadID)

	// An unrouted message on an account with a default product profile runs
	// in that profile's own conversation: its request and session id come
	// from the server layer, and the generic restore plumbing does not apply.
	var queryReq map[string]interface{}
	profileTurn := false
	if msg.PresetWorkflow == nil && m.profileTurn != nil {
		// A Slack thread starts its own chat in the crew, which has seen
		// nothing of the thread yet: give its first turn the thread, as a
		// workflow's first turn gets it. WhatsApp runs in the profile's
		// main conversation, which already holds every prior turn.
		if msg.Platform == "slack" && msg.PresetProfile != nil && msg.ResumeSessionID == "" {
			msg.Text = m.buildQueryWithThreadHistory(msg.Text, msg.Platform, threadID)
		}
		req, profileSessionID, handled, err := m.profileTurn(context.Background(), workspaceUserID, msg, threadID)
		botStep("product conversation prepared")
		if err != nil {
			log.Printf("[BOT_MANAGER] Profile turn failed for thread %s: %v", threadID.Key(), err)
			if connector := m.GetConnector(msg.Platform); connector != nil {
				connector.SendThreadMessage(context.Background(), threadID, fmt.Sprintf("Couldn't start that chat: %v", err))
			}
			return
		}
		if handled {
			queryReq, sessionID, profileTurn = req, profileSessionID, true
			applyProfileBotRouteMetadata(queryReq, profilePresetRoute, msg.Platform)
			log.Printf("[BOT_MANAGER] Thread %s runs in default profile conversation %s", threadID.Key(), sessionID)
		}
	}
	if queryReq == nil && msg.PresetProfile != nil {
		if connector := m.GetConnector(msg.Platform); connector != nil {
			connector.SendThreadMessage(context.Background(), threadID, "The configured product conversation is unavailable.")
		}
		return
	}
	if queryReq == nil && msg.runsAsSender() && msg.PresetWorkflow != nil && strings.TrimSpace(msg.PresetWorkflow.WorkflowID) != "" && m.userWorkflowChat != nil {
		// One user, one chat: a DM or WhatsApp message continues the
		// sender's own chat of the workflow, the one their web Builder
		// restores — even over a chat this thread used before, since the
		// web's current chat is the one that counts.
		own := strings.TrimSpace(m.userWorkflowChat(context.Background(), workspaceUserID, *msg.PresetWorkflow))
		botStep("workflow chat looked up")
		if own != "" && own != sessionID {
			sessionID = own
			restoredConversationSessionID = ""
			log.Printf("[BOT_MANAGER] %s thread %s continues user %s's workflow chat %s", msg.Platform, threadID.Key(), workspaceUserID, sessionID)
		}
	}
	if queryReq == nil {
		// Load thread history for context continuity (e.g., user replies after hours)
		queryWithHistory := msg.Text
		if len(resumeSessionID) == 0 || resumeSessionID[0] == "" {
			queryWithHistory = m.buildQueryWithThreadHistory(msg.Text, msg.Platform, threadID)
			botStep("thread history loaded")
		}
		queryReq = m.buildQueryRequest(queryWithHistory, workspaceUserID, msg.ChannelID, msg.PresetWorkflow, msg.Platform, botDMUserID(msg, workspaceUserID), threadID)
		botStep("query request built")
		addRestoredConversationSessionID(queryReq, restoredConversationSessionID)
	}
	if existing, _ := queryReq["session_title"].(string); strings.TrimSpace(existing) == "" && sessionTitle != "" {
		queryReq["session_title"] = sessionTitle
	}
	applyBotQueryRequestMetadata(queryReq, botMeta)
	sendFullDetails := botFullDetailsFromRequest(queryReq)

	// Track the live session in memory. The durable connector binding is saved
	// immediately afterwards, before the agent run begins.
	m.mu.Lock()
	active := &activeBotSession{
		SessionID:       sessionID,
		UserID:          workspaceUserID,
		Status:          chathistory.BotSessionStatusRunning,
		Platform:        msg.Platform,
		ThreadID:        threadID,
		Metadata:        botMeta,
		ackChannelID:    msg.ChannelID,
		ackMessageTS:    msg.MessageTS,
		LastActivity:    time.Now(),
		sendFullDetails: sendFullDetails,
		profileTurn:     profileTurn,
		profileRoute:    msg.PresetProfile,
		RouteKey:        botMessageRouteKey(msg),
	}
	startedAt := active.LastActivity
	applyBotRequestMetadata(active, queryReq)
	m.sessions[threadID.Key()] = active
	m.mu.Unlock()
	botStep("before saving the session binding")
	m.persistBotSessionBinding(active, startedAt)
	botStep("session binding saved")

	// Long-running indicator: if the agent hasn't replied within ~10s, layer an
	// hourglass reaction on top of the "eyes" ack so the user knows the bot is
	// still thinking, not stuck. Quick responses never see the hourglass.
	if msg.ChannelID != "" && msg.MessageTS != "" {
		go func(threadID ThreadID, messageTS string) {
			time.Sleep(10 * time.Second)
			active.mu.Lock()
			stillRunning := active.Status == chathistory.BotSessionStatusRunning
			active.mu.Unlock()
			if !stillRunning {
				return
			}
			if connector := m.GetConnector(threadID.Platform); connector != nil && connector.Capabilities().Reactions {
				if err := botConnectorAddReaction(context.Background(), connector, threadID, messageTS, "hourglass_flowing_sand"); err != nil {
					log.Printf("[BOT_MANAGER] Failed to add hourglass reaction: %v", err)
				}
			}
		}(ThreadID{Platform: msg.Platform, ChannelID: msg.ChannelID, ConnectionID: msg.ConnectionID}, msg.MessageTS)
	}

	// No "Starting session..." announcement — the agent's first streamed
	// response appears quickly enough that a status preamble is just noise.
	if m.GetConnector(msg.Platform) == nil {
		log.Printf("[BOT_MANAGER] WARNING: no connector for platform %s", msg.Platform)
	}

	m.runSession(active, queryReq)
}

func (m *BotConversationManager) startMirroringExistingSession(active *activeBotSession) {
	if active == nil {
		return
	}
	connector := m.GetConnector(active.Platform)
	if connector == nil {
		return
	}
	sessionCtx, cancel := context.WithCancel(context.Background())
	active.mu.Lock()
	if active.cancel != nil {
		active.cancel()
	}
	active.cancel = cancel
	active.eventFilter = NewBotEventFilter(connector, active.ThreadID, active.SessionID, os.Getenv("PUBLIC_URL"), active.UserID)
	active.eventFilter.SetSendFullDetails(active.sendFullDetails)
	sessionID := active.SessionID
	active.mu.Unlock()

	active.eventFilter.SetBlockingEventCallback(func(eventType, requestID string) {
		active.mu.Lock()
		active.awaitingUserInput = true
		active.blockingEventType = eventType
		active.blockingRequestID = requestID
		active.Status = chathistory.BotSessionStatusRunning
		active.mu.Unlock()
	})
	active.eventFilter.SetSessionDoneCallback(func() {
		pending := m.markBotBuilderDone(active)
		if pending == 0 {
			cancel()
		}
	})

	if m.eventSubscriber != nil {
		go active.eventFilter.Start(sessionCtx, m.eventSubscriber, sessionID)
	}
	go func() {
		<-sessionCtx.Done()
		active.mu.Lock()
		if active.Status != chathistory.BotSessionStatusFailed {
			active.Status = chathistory.BotSessionStatusCompleted
		}
		active.LastActivity = time.Now()
		completedAt := active.LastActivity
		active.mu.Unlock()
		m.persistBotSessionBinding(active, completedAt)
	}()
}

// markBotBuilderDone releases the bot's turn state, including stale prompts
// after a terminal failure. Background workflows retain their own lifecycle.
func (m *BotConversationManager) markBotBuilderDone(active *activeBotSession) int {
	active.mu.Lock()
	defer active.mu.Unlock()
	active.builderDone = true
	active.awaitingUserInput = false
	active.blockingEventType = ""
	active.blockingRequestID = ""
	if active.eventFilter != nil && active.eventFilter.SessionFailed() {
		active.Status = chathistory.BotSessionStatusFailed
	}
	return active.pendingWorkflows
}

// runSession runs the agent session with event filtering and lifecycle management
func (m *BotConversationManager) runSession(active *activeBotSession, queryReq map[string]interface{}) {
	ctx := context.Background()
	if active != nil {
		active.mu.Lock()
		active.mu.Unlock()
	}

	connector := m.GetConnector(active.Platform)
	if connector == nil {
		return
	}
	m.acknowledgeBotMessage(ThreadID{Platform: active.Platform, ChannelID: active.ackChannelID, ConnectionID: active.ThreadID.ConnectionID}, active.ackMessageTS)

	// Set up event filter for streaming updates to thread
	sessionCtx, cancel := context.WithCancel(ctx)
	active.mu.Lock()
	active.cancel = cancel
	active.eventFilter = NewBotEventFilter(connector, active.ThreadID, active.SessionID, os.Getenv("PUBLIC_URL"), active.UserID)
	active.eventFilter.SetSendFullDetails(active.sendFullDetails)
	sessionID := active.SessionID
	userID := active.UserID
	active.mu.Unlock()

	// Wire up blocking event callback — any blocking event (human feedback, etc.)
	active.eventFilter.SetBlockingEventCallback(func(eventType, requestID string) {
		active.mu.Lock()
		log.Printf("[BOT_MANAGER] Blocking event %s for session %s request_id=%s", eventType, active.SessionID, requestID)
		active.awaitingUserInput = true
		active.blockingEventType = eventType
		active.blockingRequestID = requestID
		active.mu.Unlock()
	})

	// Wire up session done callback — event filter signals when the builder's
	// own turn is complete. If the builder launched background workflows via
	// run_full_workflow, we hold off on canceling the session context (and
	// therefore on clearing Slack reactions) until every mirrored workflow has
	// drained. The last mirror to finish will call cancel() itself.
	active.eventFilter.SetSessionDoneCallback(func() {
		pending := m.markBotBuilderDone(active)
		if pending > 0 {
			log.Printf("[BOT_MANAGER] Builder done for %s but %d workflow(s) still running — deferring cancel", active.SessionID, pending)
			return
		}
		log.Printf("[BOT_MANAGER] Session done callback for %s", active.SessionID)
		cancel()
	})

	if elapsed := time.Since(active.LastActivity); elapsed > 2*time.Second {
		log.Printf("[BOT_TIMING] before starting the event filter: %s after the session was created", elapsed.Round(100*time.Millisecond))
	}
	if m.eventSubscriber != nil {
		log.Printf("[BOT_MANAGER] Starting event filter goroutine for session %s", sessionID)
		go active.eventFilter.Start(sessionCtx, m.eventSubscriber, sessionID)
	} else {
		log.Printf("[BOT_MANAGER] WARNING: eventSubscriber is nil, event filter NOT started for session %s", sessionID)
	}

	if m.chatHistory != nil {
		go m.pollProgressiveText(sessionCtx, active)
	}

	// Start the actual session in background — don't block on it.
	if m.startSession != nil {
		go func() {
			err := m.startSession(sessionCtx, queryReq, sessionID, userID, func(event *events.AgentEvent) {})
			if errors.Is(err, ErrBotDryRunAdmitted) {
				// A dry run stops at admission: no failure message.
				cancel()
				return
			}
			if err != nil && sessionCtx.Err() == nil {
				log.Printf("[BOT_MANAGER] Session error: %v", err)
				connector.SendThreadMessage(ctx, active.ThreadID, botSessionFailureMessage(err))
				active.mu.Lock()
				active.Status = chathistory.BotSessionStatusFailed
				active.mu.Unlock()
				cancel()
			} else if err == nil {
			}
		}()
	} else {
	}

	// Block until event filter signals session is done (or context is canceled)
	<-sessionCtx.Done()

	// Session completed or was canceled. The agent's final response has already
	// been posted to the thread by the event filter, so no extra "Session
	// completed." status message is sent — it's noise for end users.
	active.mu.Lock()
	alreadyFailed := active.Status == chathistory.BotSessionStatusFailed
	if !alreadyFailed {
		active.Status = chathistory.BotSessionStatusCompleted
	}
	acks := append([]ThreadID(nil), active.followUpAcks...)
	acks = append(acks, ThreadID{Platform: active.Platform, ChannelID: active.ackChannelID, ThreadTS: active.ackMessageTS, ConnectionID: active.ThreadID.ConnectionID})
	active.followUpAcks = nil
	active.mu.Unlock()

	m.clearBotMessageReactions(ctx, acks)

	// Keep the session entry in the map with Completed status so a subsequent
	// non-mention reply in the same thread flows through handleExistingSession
	// and auto-starts a new session (rather than being silently ignored).
	// Entries are pruned by the background janitor after 7 days of inactivity.
	active.mu.Lock()
	active.LastActivity = time.Now()
	completedAt := active.LastActivity
	active.mu.Unlock()
	m.persistBotSessionBinding(active, completedAt)
}

func botSessionFailureMessage(err error) string {
	if err == nil {
		return "I couldn't start this request. Please try again."
	}
	detail := strings.ToLower(err.Error())
	if strings.Contains(detail, "workflow access denied") {
		return "You don't currently have access to this workflow. Ask a workflow owner to share it with your AgentWorks account, then try again."
	}
	// Other refusals are about the bot's setup (route, grant, app binding),
	// not the person's access; don't send them chasing a sharing problem.
	if strings.Contains(detail, "status 403") {
		return "This bot isn't set up to start this conversation. Ask the workflow owner to check its Slack setup in AgentWorks (Setup > Bots)."
	}
	return "I couldn't start this request. Please try again. If it keeps failing, ask an administrator to check the connector logs."
}

// Acknowledge only messages accepted for an agent turn, never raw channel events.
func (m *BotConversationManager) acknowledgeBotMessage(threadID ThreadID, messageTS string) {
	connector := m.GetConnector(threadID.Platform)
	if connector == nil || !connector.Capabilities().Reactions || threadID.ChannelID == "" || messageTS == "" {
		return
	}
	if err := botConnectorAddReaction(context.Background(), connector, threadID, messageTS, "eyes"); err != nil {
		log.Printf("[BOT_MANAGER] Failed to acknowledge accepted message: %v", err)
	}
}

func unroutedBotAccessMessage(platform, fallback string) string {
	if strings.EqualFold(strings.TrimSpace(platform), "slack") {
		return "This channel isn't connected to a workflow. Use the bot in a configured channel, or ask a workflow owner to add this channel in Setup > Connectors > Slack. Inviting the bot alone doesn't connect the channel."
	}
	return fallback
}

func (m *BotConversationManager) clearBotMessageReactions(ctx context.Context, acks []ThreadID) {
	// Clear both ack reactions: "eyes" (immediate) and "hourglass_flowing_sand"
	// (added only if the session ran past the long-running threshold). Missing
	// reactions are treated as non-fatal by the connector impl.
	for _, ack := range acks {
		if ack.ChannelID == "" || ack.ThreadTS == "" {
			continue
		}
		connector := m.GetConnector(ack.Platform)
		if connector != nil && connector.Capabilities().Reactions {
			for _, emoji := range []string{"eyes", "hourglass_flowing_sand"} {
				if err := botConnectorRemoveReaction(ctx, connector, ack, ack.ThreadTS, emoji); err != nil {
					log.Printf("[BOT_MANAGER] Failed to remove %s reaction: %v", emoji, err)
				}
			}
		}
	}

}

// progressiveTextPollInterval is a var, not a const, so a test can shorten
// it rather than waiting on real time.
var progressiveTextPollInterval = 4 * time.Second

// pollProgressiveText periodically reads a session's durable conversation
// history — the exact record the app's own chat-restore path already
// trusts, kept current while the turn runs — and forwards each newly
// completed assistant reply to the bot channel as its own message, as soon
// as it appears rather than waiting for the whole turn to finish. Not live
// token streaming: only genuinely finished replies, so nothing sends more
// often than the agent actually produces a new one. Stops when ctx (the
// turn's own context) is canceled.
func (m *BotConversationManager) pollProgressiveText(ctx context.Context, active *activeBotSession) {
	active.mu.Lock()
	sessionID := active.SessionID
	userID := active.UserID
	filter := active.eventFilter
	active.mu.Unlock()
	if filter == nil {
		return
	}
	// conversation_history is the whole session's lifetime, not just this
	// turn — a long-lived WhatsApp conversation already has every earlier
	// turn's replies in it, already sent back then. Baseline against what's
	// there right now (before this turn produces anything), so only turns
	// genuinely new since this poller started go out; skipping the baseline
	// would replay the entire past conversation as "new" on every turn.
	sent := 0
	if baseline, err := m.chatHistory(ctx, userID, sessionID); err == nil {
		sent = len(baseline)
	}
	ticker := time.NewTicker(progressiveTextPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sent = m.pollProgressiveTextOnce(ctx, filter, userID, sessionID, sent)
		}
	}
}

// pollProgressiveTextOnce does one poll tick's work: read the session's
// current durable text turns, send whichever are new since `sent`, and
// return the updated count. Split out from pollProgressiveText's ticker loop
// so a test can drive it directly without waiting on real time.
func (m *BotConversationManager) pollProgressiveTextOnce(ctx context.Context, filter *BotEventFilter, userID, sessionID string, sent int) int {
	texts, err := m.chatHistory(ctx, userID, sessionID)
	if err != nil || len(texts) <= sent {
		return sent
	}
	for _, text := range texts[sent:] {
		if filter.SendProgressiveText(ctx, text) {
			log.Printf("[BOT_MANAGER] Progressive text sent for session %s (%d chars)", sessionID, len(text))
		}
	}
	return len(texts)
}

// resetActiveForNewTurn clears state that would otherwise latch from a prior
// builder turn. Must be called before injecting a follow-up message into an
// active session so the new turn's completion gets detected properly.
func (m *BotConversationManager) resetActiveForNewTurn(active *activeBotSession) {
	active.mu.Lock()
	active.builderDone = false
	filter := active.eventFilter
	active.mu.Unlock()
	if filter != nil {
		filter.ResetForNewTurn()
	}
}

// PrepareSyntheticTurn clears one-shot completion state before a workflow
// auto-notification is injected back into an active bot session.
func (m *BotConversationManager) PrepareSyntheticTurn(sessionID string) {
	active := m.findActiveBySessionID(sessionID)
	if active == nil {
		return
	}
	m.resetActiveForNewTurn(active)
}

func botFullDetailsFromRequest(req map[string]interface{}) bool {
	if req == nil {
		return false
	}
	v, ok := req["bot_send_full_details"]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(strings.TrimSpace(t), "true")
	default:
		return false
	}
}

// SendSyntheticTurnFinalIfNeeded forwards the final assistant text produced by
// a background auto-notification turn back to the originating bot thread.
//
// Most normal turns are delivered by BotEventFilter from llm_generation_end /
// unified_completion events. Synthetic background turns can finish by only
// persisting builder history, so this is a narrow fallback. It checks the
// filter's per-turn sent text first to avoid duplicate messages when events
// were delivered normally while still allowing a later, fuller synthetic final.
func (m *BotConversationManager) SendSyntheticTurnFinalIfNeeded(sessionID, message string) bool {
	message = strings.TrimSpace(message)
	if sessionID == "" || message == "" {
		return false
	}

	active := m.findActiveBySessionID(sessionID)
	if active == nil {
		log.Printf("[BOT_MANAGER] Synthetic final fallback skipped for %s: no active bot session", sessionID)
		return false
	}

	active.mu.Lock()
	platform := active.Platform
	threadID := active.ThreadID
	filter := active.eventFilter
	active.LastActivity = time.Now()
	active.mu.Unlock()

	// This is a fallback path. Give the normal event filter a brief chance to
	// mark text that was already emitted via llm_generation_end/unified_completion
	// before deciding whether we need to send anything ourselves.
	if filter != nil {
		time.Sleep(250 * time.Millisecond)
	}
	if filter != nil && !filter.ShouldSendSyntheticFinal(message) {
		log.Printf("[BOT_MANAGER] Synthetic final fallback skipped for %s: same builder text already sent", sessionID)
		return false
	}

	connector := m.GetConnector(platform)
	if connector == nil {
		log.Printf("[BOT_MANAGER] Synthetic final fallback skipped for %s: no connector for platform %s", sessionID, platform)
		return false
	}

	if filter != nil {
		// The reply supersedes the "Working on…" placeholder; left in place,
		// it sat above the answer and the heartbeat kept editing it.
		filter.ClearProgress()
	}
	if _, err := connector.SendThreadMessage(context.Background(), threadID, message); err != nil {
		log.Printf("[BOT_MANAGER] Synthetic final fallback failed for %s: %v", sessionID, err)
		return false
	}
	if filter != nil {
		filter.MarkMainTextSent(message)
	}
	log.Printf("[BOT_MANAGER] Synthetic final fallback sent for %s (%d chars)", sessionID, len(message))
	return true
}

// PendingWorkflowCount returns how many mirrored workflow/sub-agent sessions
// are still attached to a bot conversation. It is used by auto-notification
// prompts to decide whether a completion is just progress or the final result.
func (m *BotConversationManager) PendingWorkflowCount(sessionID string) int {
	active := m.findActiveBySessionID(sessionID)
	if active == nil {
		return 0
	}
	active.mu.Lock()
	defer active.mu.Unlock()
	return active.pendingWorkflows
}

// findActiveBySessionID returns the active bot session whose SessionID matches,
// or nil. Linear scan — the in-flight session count is small (a few per thread).
func (m *BotConversationManager) findActiveBySessionID(sessionID string) *activeBotSession {
	if sessionID == "" {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	// One chat can be reached from several threads (a person's Slack DM and
	// WhatsApp both continue their own chat): prefer the one used last.
	var best *activeBotSession
	var bestAt time.Time
	for _, active := range m.sessions {
		active.mu.Lock()
		match := active.SessionID == sessionID
		at := active.LastActivity
		active.mu.Unlock()
		if match && (best == nil || at.After(bestAt)) {
			best, bestAt = active, at
		}
	}
	return best
}

// OnChildSpawned implements virtualtools.SpawnListener. Called whenever a
// background sub-session is attached to a parent chat. Delegates to
// NotifyWorkflowStarted, which no-ops when the parent isn't a bot session.
func (m *BotConversationManager) OnChildSpawned(parentSessionID, childSessionID string) {
	m.NotifyWorkflowStarted(parentSessionID, childSessionID)
}

// OnChildEnded implements virtualtools.SpawnListener. Called whenever a
// background sub-session is detached. Delegates to NotifyWorkflowEnded.
func (m *BotConversationManager) OnChildEnded(parentSessionID, childSessionID string) {
	m.NotifyWorkflowEnded(parentSessionID, childSessionID)
}

// NotifyWorkflowStarted marks a background workflow as attached to a parent
// bot session. The orchestrator's ContextAwareBridge already routes workflow
// step events onto the parent's event stream, so the parent's existing
// BotEventFilter forwards them to Slack — we only need to defer the session's
// eventual cancel until every attached workflow has finished. No-op when the
// parent isn't an active bot session (e.g. chat UI workflows).
func (m *BotConversationManager) NotifyWorkflowStarted(parentSessionID, wfSessionID string) {
	if parentSessionID == "" || wfSessionID == "" {
		return
	}
	active := m.findActiveBySessionID(parentSessionID)
	if active == nil {
		return
	}
	active.mu.Lock()
	if active.activeWorkflows == nil {
		active.activeWorkflows = make(map[string]bool)
	}
	if active.activeWorkflows[wfSessionID] {
		active.mu.Unlock()
		log.Printf("[BOT_MANAGER] Workflow already tracked for wf=%s (parent=%s)", wfSessionID, parentSessionID)
		return
	}
	active.activeWorkflows[wfSessionID] = true
	active.pendingWorkflows++
	log.Printf("[BOT_MANAGER] Workflow attached: parent=%s wf=%s (pending=%d)",
		parentSessionID, wfSessionID, active.pendingWorkflows)
	active.mu.Unlock()
}

// NotifyWorkflowEnded marks a background workflow as drained. When the last
// outstanding workflow finishes AND the builder's own turn already ended, the
// parent session context is canceled so reactions get cleared and the
// session is marked completed. Safe to call more than once.
func (m *BotConversationManager) NotifyWorkflowEnded(parentSessionID, wfSessionID string) {
	if parentSessionID == "" || wfSessionID == "" {
		return
	}
	active := m.findActiveBySessionID(parentSessionID)
	if active == nil {
		return
	}
	active.mu.Lock()
	if !active.activeWorkflows[wfSessionID] {
		active.mu.Unlock()
		return
	}
	delete(active.activeWorkflows, wfSessionID)
	if active.pendingWorkflows > 0 {
		active.pendingWorkflows--
	}
	pending := active.pendingWorkflows
	builderDone := active.builderDone
	parentCancel := active.cancel
	active.mu.Unlock()

	log.Printf("[BOT_MANAGER] Workflow detached: parent=%s wf=%s (pending=%d, builderDone=%v)",
		parentSessionID, wfSessionID, pending, builderDone)

	if pending == 0 && builderDone && parentCancel != nil {
		log.Printf("[BOT_MANAGER] All workflows drained after builder done — canceling parent session %s", active.SessionID)
		parentCancel()
	}
}

// cancelSession cancels a bot session
func (m *BotConversationManager) cancelSession(active *activeBotSession, reason string) {
	ctx := context.Background()

	active.mu.Lock()
	cancelFn := active.cancel
	active.Status = chathistory.BotSessionStatusFailed
	active.mu.Unlock()

	if cancelFn != nil {
		cancelFn()
	}

	if reason != "" {
		connector := m.GetConnector(active.Platform)
		if connector != nil {
			connector.SendThreadMessage(ctx, active.ThreadID, reason)
		}
	}

	m.mu.Lock()
	delete(m.sessions, active.ThreadID.Key())
	m.mu.Unlock()
}

// IsBotSession reports whether a session id matches any bot conversation
// currently tracked in memory.
func (m *BotConversationManager) IsBotSession(sessionID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.sessions {
		if s != nil && s.SessionID == sessionID {
			return true
		}
	}
	return false
}

// buildQueryWithThreadHistory loads thread history from the platform and prepends it as context
// to the user's current message. This ensures continuity when a user replies to an existing thread
// after the previous session has completed (e.g., hours later). Called only on new-session starts;
// live sessions inject follow-ups as raw text since the LLM retains prior turns in its own context.
func (m *BotConversationManager) buildQueryWithThreadHistory(query string, platform string, threadID ThreadID) string {
	connector := m.GetConnector(platform)
	if connector == nil || !connector.Capabilities().Threads || threadIsWholeChat(threadID) {
		return query
	}

	ctx := context.Background()
	channelName := botConnectorChannelName(ctx, connector, threadID)

	history, err := connector.GetThreadHistory(ctx, threadID)
	if err != nil {
		log.Printf("[BOT_MANAGER] Failed to load thread history: %v", err)
		return query
	}
	// Everything loaded here reaches the agent in this turn; later
	// follow-ups add only messages posted after it.
	m.markThreadHistorySeen(threadID, history)

	// Filter out bot status messages (Starting session..., Session completed., etc.)
	// and keep only meaningful conversation turns
	var meaningful []ThreadMessage
	for _, msg := range history {
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			continue
		}
		// Skip bot status/progress messages
		if msg.IsBot && (strings.HasPrefix(text, "Starting session...") || text == "Session completed." ||
			strings.HasPrefix(text, "Session failed:")) {
			continue
		}
		meaningful = append(meaningful, msg)
	}

	// No prior history: still prepend a channel header when available so the LLM knows
	// where the conversation is happening; skip entirely when channel name is unknown.
	if len(meaningful) <= 1 {
		if channelName == "" {
			return query
		}
		return fmt.Sprintf("## Channel\n#%s\n\n---\n\n## Current Message\n%s", channelName, query)
	}

	// Build conversation context from history (exclude the last message which is the current one)
	var parts []string
	if channelName != "" {
		parts = append(parts, fmt.Sprintf("## Channel\n#%s\n", channelName))
	}
	parts = append(parts, "## Previous Conversation in This Thread\n")
	for _, msg := range meaningful[:len(meaningful)-1] {
		role := "User"
		if msg.IsBot && !msg.IsSelf && msg.UserName != "" {
			// Another app's post (e.g. a Sentry alert), not this agent's reply.
			role = msg.UserName + " (app)"
		} else if msg.IsBot {
			role = "Agent"
		} else if msg.UserName != "" {
			role = msg.UserName
		}
		tsLabel := msg.Timestamp.UTC().Format("2006-01-02 15:04 UTC")
		parts = append(parts, fmt.Sprintf("**%s** (%s): %s\n", role, tsLabel, msg.Text))
	}
	parts = append(parts, "---\n\n## Current Message\n")
	parts = append(parts, query)

	combined := strings.Join(parts, "\n")
	log.Printf("[BOT_MANAGER] Prepended %d messages of thread history to query (channel=%s)", len(meaningful)-1, channelName)
	return combined
}

// resolveChannelWorkflow looks up the ChannelRoute for a channel on the given
// platform. Returns nil if no routing is configured for the channel.
//
// Connector configs are keyed by platform, and this read used to ask for
// "slack" whatever the message arrived on, so channel routing configured for
// any other platform was never found and those messages fell through to
// generic chat, silently losing the workflow's own tools and LLM config.
// This is the fallback path only: a platform that resolves its own route
// first — WhatsApp maps an "@slug" to a workflow in WhatsAppService.Resolve —
// arrives here already routed and never reaches this lookup. Slack uses this
// only for channel-ID routing; the historical empty-platform key is disabled.
func (m *BotConversationManager) resolveChannelWorkflow(platform, channelID string) *ChannelRoute {
	if m.chatStore == nil {
		return nil
	}
	configID := strings.ToLower(strings.TrimSpace(platform))
	if configID == "" {
		return nil
	}
	botCfg, err := m.chatStore.GetBotConnectorConfig(context.Background(), configID)
	if err != nil || botCfg == nil {
		return nil
	}
	return ResolveChannelRoute(botCfg.AllowedChannels, channelID)
}

// resolveThreadRoute routes one thread: on Slack the thread's
// bound target while its channel still allows it, else the channel's default
// (slack_targets.go).
func (m *BotConversationManager) resolveThreadRoute(thread ThreadID) *ChannelRoute {
	if !strings.EqualFold(strings.TrimSpace(thread.Platform), "slack") {
		return m.resolveChannelWorkflow(thread.Platform, thread.ChannelID)
	}
	return ResolveSlackThreadRoute(context.Background(), thread, func() *ChannelRoute {
		return m.resolveChannelWorkflow(thread.Platform, thread.ChannelID)
	})
}

// buildQueryRequest constructs a request map for startSessionInternal.
// userID is the workspace user ID used for scoped secret resolution and access checks.
// channelID is used for Slack-style channel→workflow routing: pass the
// incoming message's ChannelID for new sessions, or "" for follow-ups into
// existing sessions (routing is ignored for follow-ups).
// presetRoute, when non-nil, bypasses channel lookup entirely — used by
// WhatsApp's @<slug> prefix parser to pick a workflow from in-message text.
// platform is the bot channel ("slack", "whatsapp", …) so the server can
// inject channel-specific formatting rules into the agent's system prompt.
// Pass "" for non-bot callers. For follow-ups, keep passing the platform and
// threadID so human tools can notify the same bot conversation.
func (m *BotConversationManager) buildQueryRequest(query string, userID string, channelID string, presetRoute *ChannelRoute, platform string, dmUserID string, threadIDs ...ThreadID) map[string]interface{} {
	if platform == "slack" && m.workflowTurn != nil {
		thread := ThreadID{Platform: platform, ChannelID: channelID}
		if len(threadIDs) > 0 {
			thread = threadIDs[0]
		}
		route := presetRoute
		if route == nil {
			route = m.resolveThreadRoute(thread)
		}
		if route != nil && route.WorkflowID != "" {
			req, err := m.workflowTurn(context.Background(), query, *route, thread, dmUserID)
			if err != nil {
				return map[string]interface{}{"_bot_prepare_error": err.Error()}
			}
			return req
		}
	}

	req := map[string]interface{}{
		"query": query,
	}
	var threadID ThreadID
	if len(threadIDs) > 0 {
		threadID = threadIDs[0]
	}
	if platform != "" || len(threadIDs) > 0 {
		ApplyBotThreadFields(req, platform, threadID)
	}
	if platform != "" {
		req["triggered_by"] = "bot:" + platform
	}

	// Resolve the workflow route: an explicit preset wins over channel lookup,
	// which wins over "no routing at all" (default multi-agent chat).
	route := presetRoute
	if route == nil && channelID != "" {
		thread := ThreadID{Platform: platform, ChannelID: channelID}
		if len(threadIDs) > 0 {
			thread = threadIDs[0]
			thread.Platform = platform
			thread.ChannelID = channelID
		}
		route = m.resolveThreadRoute(thread)
	}
	if route != nil {
		req["preset_query_id"] = route.WorkflowID
		grant := NormalizeBotRouteGrant(route.BotGrant, route.WorkshopMode)
		req["bot_route_grant"] = grant
		if strings.TrimSpace(route.WorkspacePath) != "" {
			req["selected_folder"] = strings.TrimSpace(route.WorkspacePath)
		}
		if route.SendFullDetails || strings.EqualFold(platform, "slack") {
			req["bot_send_full_details"] = true
		}

		// Prefer a per-channel override on the route. Deployed bot workflow
		// traffic defaults to Run mode; do not inherit a manifest authoring
		// default unless there is no bot platform involved. The server access
		// check still pins read-only users to Run even if a route asks for Build.
		workshopMode := WorkshopModeForBotGrant(grant)
		if workshopMode == "" && platform != "" {
			workshopMode = "run"
		}
		via := "channel " + channelID
		if presetRoute != nil {
			via = "preset (workflow " + route.WorkflowID + ")"
		}
		if workshopMode != "" {
			// Workshop mode — use the conversational Workflow Builder agent.
			// workshop_mode must live inside execution_options so the workshop
			// session picks it up via SetWorkshopModeOverride (server.go:4409);
			// a top-level req["workshop_mode"] is ignored and the agent falls
			// back to auto-detection from step-optimization state.
			req["agent_mode"] = "workflow_phase"
			req["phase_id"] = "workflow-builder"
			req["workshop_mode"] = workshopMode
			req["execution_options"] = map[string]interface{}{
				"workshop_mode": workshopMode,
			}
			log.Printf("[BOT_MANAGER] Routed via %s → workflow %s (workshop_mode=%s)", via, route.WorkflowID, workshopMode)
		} else {
			// No workshop mode — use the full step-based orchestrator (Execution mode)
			req["agent_mode"] = "workflow"
			log.Printf("[BOT_MANAGER] Routed via %s → workflow %s (execution mode)", via, route.WorkflowID)
		}
	} else if channelID != "" {
	}

	// Capabilities: prefer the user's saved multi-agent chat config
	// (_users/<id>/multiagent-config.json) so bot sessions match the UI setup.
	// Falls back to no servers or implicit skill attachments
	// when the user hasn't saved a config.
	var savedCaps *MultiAgentChatCapabilities
	if m.workspaceURL != "" && userID != "" {
		if caps, exists, err := LoadMultiAgentChatCapabilities(context.Background(), m.workspaceURL, userID); err != nil {
			log.Printf("[BOT_MANAGER] Warning: failed to load multiagent-config for user %s: %v", userID, err)
		} else if exists {
			savedCaps = caps
		}
	}

	if savedCaps != nil {
		req["servers"] = savedCaps.SelectedServers
		req["selected_skills"] = savedCaps.SelectedSkills
		if len(savedCaps.SelectedTools) > 0 {
			req["selected_tools"] = savedCaps.SelectedTools
		}
		if savedCaps.SelectedGlobalSecretNames != nil {
			req["selected_global_secrets"] = savedCaps.SelectedGlobalSecretNames
		}
		if savedCaps.Notifications != nil {
			if secretName := strings.TrimSpace(savedCaps.Notifications.SlackWebhookSecretName); secretName != "" {
				req["notification_slack_webhook_secret_name"] = secretName
			}
		}
		if savedCaps.BrowserMode != "" {
			req["browser_mode"] = savedCaps.BrowserMode
		}
		if len(savedCaps.CDPPorts) > 0 {
			req["cdp_ports"] = savedCaps.CDPPorts
		}
		req["use_code_execution_mode"] = savedCaps.UseCodeExecutionMode
		if llmConfig := multiAgentChatLLMConfigForRequest(savedCaps.LLMConfig); llmConfig != nil {
			req["llm_config"] = llmConfig
		}
		log.Printf("[BOT_MANAGER] Loaded saved chat capabilities for user %s: skills=%d servers=%d", userID, len(savedCaps.SelectedSkills), len(savedCaps.SelectedServers))
	} else {
		// A bot must not inherit every skill from an account-wide library.
		req["servers"] = []string{}
		req["selected_skills"] = []string{}

	}

	// Load delegation tier config from workspace file — same source as multiagent chat.
	// server.go resolves the orchestrator model from this at request time via resolveDelegationTierConfig.
	if m.workspaceURL != "" {
		if tierConfig, exists, err := LoadDelegationTierConfig(context.Background(), m.workspaceURL); err != nil {
			log.Printf("[BOT_MANAGER] Warning: failed to load tier config from workspace: %v", err)
		} else if exists && len(tierConfig) > 0 {
			req["delegation_tier_config"] = tierConfig
			log.Printf("[BOT_MANAGER] Loaded delegation tier config from workspace file")
		}

		// Load provider API keys from the workspace encrypted file and inject
		// them into llm_config so handleQuery can use them for all providers.
		if apiKeys, exists, err := LoadProviderKeys(context.Background(), m.workspaceURL); err != nil {
			log.Printf("[BOT_MANAGER] Warning: failed to load provider keys from workspace: %v", err)
		} else if exists && len(apiKeys) > 0 {
			llmConfig, _ := req["llm_config"].(map[string]interface{})
			if llmConfig == nil {
				llmConfig = map[string]interface{}{}
			}
			llmConfig["api_keys"] = apiKeys
			req["llm_config"] = llmConfig
			log.Printf("[BOT_MANAGER] Loaded %d provider API keys from workspace file", len(apiKeys))
		}
	}

	log.Printf("[BOT_MANAGER] buildQueryRequest: query=%s skills=%v servers=%v",
		botTruncate(query, 60), req["selected_skills"], req["servers"])

	return req
}

func (m *BotConversationManager) buildQueryRequestForActive(active *activeBotSession, query, userID, platform string, threadID ThreadID) map[string]interface{} {
	if platform == "slack" && m.workflowTurn != nil {
		route := m.resolveThreadRoute(threadID)
		if route != nil && route.WorkflowID != "" {
			var meta *chathistory.BotMetadata
			if active != nil {
				active.mu.Lock()
				meta = active.Metadata
				active.mu.Unlock()
			}
			dmUserID := ""
			if meta != nil && meta.DirectMessage {
				dmUserID = userID
			}
			req := m.buildQueryRequest(query, userID, threadID.ChannelID, route, platform, dmUserID, threadID)
			applyBotQueryRequestMetadata(req, meta)
			return req
		}
		return map[string]interface{}{"_bot_prepare_error": "Slack route was removed or changed"}
	}

	req := m.buildQueryRequest(query, userID, "", nil, platform, "", threadID)
	if active == nil {
		return req
	}
	active.mu.Lock()
	agentMode := strings.TrimSpace(active.AgentMode)
	presetQueryID := strings.TrimSpace(active.PresetQueryID)
	workspacePath := strings.TrimSpace(active.WorkspacePath)
	phaseID := strings.TrimSpace(active.PhaseID)
	workshopMode := strings.TrimSpace(active.WorkshopMode)
	botMeta := active.Metadata
	routeGrant := ""
	if agentMode == "workflow_phase" && workspacePath != "" {
		if strings.EqualFold(strings.TrimSpace(platform), "slack") {
			// Slack route grants are authoritative and re-read below. A blank
			// mode must not escalate: buildQueryRequest starts Slack bot traffic
			// in Run, so defaulting a follow-up to Workshop would hand the
			// thread authoring authority on its second message.
			workshopMode = NormalizeBotWorkshopMode(workshopMode)
			if workshopMode == "" {
				workshopMode = "run"
			}
		} else {
			// WhatsApp has not been moved to the route-grant model. Preserve its
			// existing workflow-phase follow-up behavior until that migration is
			// explicit and covered separately.
			workshopMode = "workshop"
		}
	}
	if strings.EqualFold(strings.TrimSpace(platform), "slack") {
		if route := m.resolveThreadRoute(threadID); route != nil {
			routeGrant = NormalizeBotRouteGrant(route.BotGrant, route.WorkshopMode)
			workshopMode = WorkshopModeForBotGrant(routeGrant)
		}
	}
	active.mu.Unlock()
	applyBotQueryRequestMetadata(req, botMeta)

	if agentMode != "" {
		req["agent_mode"] = agentMode
	}
	if presetQueryID != "" {
		req["preset_query_id"] = presetQueryID
	}
	if workspacePath != "" {
		req["selected_folder"] = workspacePath
	}
	if phaseID != "" {
		req["phase_id"] = phaseID
	}
	if workshopMode != "" {
		req["workshop_mode"] = workshopMode
		execOpts, _ := req["execution_options"].(map[string]interface{})
		if execOpts == nil {
			execOpts = map[string]interface{}{}
		}
		execOpts["workshop_mode"] = workshopMode
		req["execution_options"] = execOpts
	}
	if routeGrant != "" {
		req["bot_route_grant"] = routeGrant
	}
	return req
}

// turnRequestForActive builds a later turn for an active session: through the
// profile-turn builder when the session runs in a product profile's
// conversation (so every turn carries that profile), else the generic bot
// request. If the profile builder cannot serve the turn the generic request
// is the fallback, logged, rather than dropping the message.
func (m *BotConversationManager) turnRequestForActive(active *activeBotSession, query, userID, platform string, threadID ThreadID) map[string]interface{} {
	if active != nil && m.profileTurn != nil {
		active.mu.Lock()
		isProfileTurn := active.profileTurn
		profileRoute := active.profileRoute
		routeGrant := active.BotRouteGrant
		sessionID := active.SessionID
		directMessage := active.Metadata != nil && active.Metadata.DirectMessage
		active.mu.Unlock()
		if isProfileTurn {
			// A DM thread keeps running in its sender's own chat.
			msg := BotIncomingMessage{Platform: platform, WorkspaceUserID: userID, ChannelID: threadID.ChannelID, ThreadTS: threadID.ThreadTS, Text: query, IsMention: true, PresetProfile: profileRoute, ResumeSessionID: sessionID, DirectMessage: directMessage && platform == "slack"}
			req, _, handled, err := m.profileTurn(context.Background(), userID, msg, threadID)
			if err == nil && handled {
				active.mu.Lock()
				botMeta := active.Metadata
				active.mu.Unlock()
				if profileRoute != nil {
					applyProfileBotRouteMetadata(req, &ChannelRoute{
						ProfileID:       profileRoute.ProfileID,
						ConversationKey: profileRoute.ConversationKey,
						WorkspacePath:   profileRoute.UploadFolder,
						WorkspaceUserID: profileRoute.WorkspaceUserID,
						BotGrant:        routeGrant,
					}, platform)
				}
				applyBotQueryRequestMetadata(req, botMeta)
				return req
			}
			log.Printf("[BOT_MANAGER] Profile turn unavailable for session %s (handled=%v err=%v)", sessionID, handled, err)
			return map[string]interface{}{"_bot_prepare_error": fmt.Sprintf("profile conversation unavailable (handled=%v): %v", handled, err)}
		}
	}
	return m.buildQueryRequestForActive(active, query, userID, platform, threadID)
}

func applyBotRequestMetadata(active *activeBotSession, req map[string]interface{}) {
	if active == nil || req == nil {
		return
	}
	active.AgentMode = stringValue(req["agent_mode"])
	active.PresetQueryID = stringValue(req["preset_query_id"])
	active.WorkspacePath = stringValue(req["selected_folder"])
	active.PhaseID = stringValue(req["phase_id"])
	active.WorkshopMode = stringValue(req["workshop_mode"])
	active.BotRouteGrant = stringValue(req["bot_route_grant"])
	if active.WorkshopMode == "" {
		if execOpts, ok := req["execution_options"].(map[string]interface{}); ok {
			active.WorkshopMode = stringValue(execOpts["workshop_mode"])
		}
	}
}

func applyProfileBotRouteMetadata(req map[string]interface{}, route *ChannelRoute, platform string) {
	if req == nil || route == nil {
		return
	}
	if strings.TrimSpace(platform) != "" {
		req["bot_platform"] = platform
		req["triggered_by"] = "bot:" + platform
	}
	req["bot_route_grant"] = NormalizeBotRouteGrant(route.BotGrant, route.WorkshopMode)
	if profileID := strings.TrimSpace(route.ProfileID); profileID != "" {
		req["agent_profile_id"] = profileID
	}
	// The turn builder may already have chosen one of the project's own chats
	// (a Slack thread's "<project>:slack-<hash>"); keep it. The route names
	// only the project.
	if conversationKey := strings.TrimSpace(route.ConversationKey); conversationKey != "" {
		if existing, _ := req["agent_profile_conversation_key"].(string); strings.TrimSpace(existing) == "" {
			req["agent_profile_conversation_key"] = conversationKey
		}
	}
	if workspacePath := strings.TrimSpace(route.WorkspacePath); workspacePath != "" {
		req["selected_folder"] = workspacePath
	}
	if route.SendFullDetails || strings.EqualFold(platform, "slack") {
		req["bot_send_full_details"] = true
	}
}

func stringValue(v interface{}) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	default:
		return ""
	}
}

func addRestoredConversationSessionID(req map[string]interface{}, sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if req == nil || sessionID == "" {
		return
	}
	req["restored_conversation_session_id"] = sessionID
}

func botRouteKey(route *ChannelRoute) string {
	if route == nil {
		return ""
	}
	parts := []string{
		strings.ToLower(strings.TrimSpace(route.WorkflowID)),
		strings.ToLower(strings.TrimSpace(route.WorkspacePath)),
		NormalizeBotWorkshopMode(route.WorkshopMode),
		NormalizeBotRouteGrant(route.BotGrant, route.WorkshopMode),
		strings.ToLower(strings.TrimSpace(route.ProfileID)),
		strings.ToLower(strings.TrimSpace(route.ConversationKey)),
	}
	return strings.Join(parts, "|")
}

func botResumeFilterFromRoute(route *ChannelRoute) BotResumeFilter {
	if route == nil {
		return BotResumeFilter{}
	}
	return BotResumeFilter{
		ProfileID: route.ProfileID, ConversationKey: route.ConversationKey, WorkspaceUserID: route.WorkspaceUserID,
		WorkspacePath: strings.TrimSpace(route.WorkspacePath),
		PresetQueryID: strings.TrimSpace(route.WorkflowID),
	}
}

func botResumeFilterFromActive(active *activeBotSession) BotResumeFilter {
	if active == nil {
		return BotResumeFilter{}
	}
	active.mu.Lock()
	defer active.mu.Unlock()
	profile := active.profileRoute
	filter := BotResumeFilter{
		WorkspacePath: strings.TrimSpace(active.WorkspacePath),
		PresetQueryID: strings.TrimSpace(active.PresetQueryID),
	}
	if profile != nil {
		filter.ProfileID = profile.ProfileID
		filter.ConversationKey = profile.ConversationKey
		filter.WorkspaceUserID = profile.WorkspaceUserID
	}
	return filter
}

func botRouteFromActive(active *activeBotSession) *ChannelRoute {
	if active == nil {
		return nil
	}
	active.mu.Lock()
	defer active.mu.Unlock()
	workflowID := strings.TrimSpace(active.PresetQueryID)
	workspacePath := strings.TrimSpace(active.WorkspacePath)
	workshopMode := NormalizeBotWorkshopMode(active.WorkshopMode)
	botGrant := NormalizeBotRouteGrant(active.BotRouteGrant, workshopMode)
	profileRoute := active.profileRoute
	if workflowID == "" && workspacePath == "" && profileRoute == nil {
		return nil
	}
	route := &ChannelRoute{
		WorkflowID:      workflowID,
		WorkspacePath:   workspacePath,
		WorkshopMode:    workshopMode,
		BotGrant:        botGrant,
		SendFullDetails: active.sendFullDetails,
	}
	if profileRoute != nil {
		route.ProfileID = strings.TrimSpace(profileRoute.ProfileID)
		route.ConversationKey = strings.TrimSpace(profileRoute.ConversationKey)
		route.WorkspaceUserID = strings.TrimSpace(profileRoute.WorkspaceUserID)
		if route.WorkspacePath == "" {
			route.WorkspacePath = strings.TrimSpace(profileRoute.UploadFolder)
		}
	}
	return route
}

func routeWorkspaceUserID(route ChannelRoute, fallback string) string {
	if userID := strings.TrimSpace(route.WorkspaceUserID); userID != "" {
		return userID
	}
	// A Crew at the shared root, or an old spelling of a migrated one, has its owner in the server's registry
	// (PLAT-442 step 4); the path of an unmigrated crew names it.
	if ref := workspaceref.MustParse(route.WorkspacePath); ref.IsShared() || ref.HasOwner() {
		if owner := strings.TrimSpace(crewOwnerFor(route.WorkspacePath)); owner != "" {
			return owner
		}
	}
	if owner := strings.TrimSpace(workspaceref.MustParse(route.WorkspacePath).Owner()); owner != "" {
		return owner
	}
	return strings.TrimSpace(fallback)
}

// isMultiUserThread checks if a thread has multiple distinct human users.
// If only the current message sender has posted (besides the bot), treat it as single-user.
func (m *BotConversationManager) isMultiUserThread(active *activeBotSession, msg BotIncomingMessage) bool {
	connector := m.GetConnector(active.Platform)
	if connector == nil || !connector.Capabilities().Threads {
		return false
	}
	history, err := connector.GetThreadHistory(context.Background(), active.ThreadID)
	if err != nil {
		// Can't determine — be conservative, require @mention
		return true
	}
	users := make(map[string]bool)
	for _, m := range history {
		if !m.IsBot && m.UserID != "" {
			users[m.UserID] = true
		}
	}
	return len(users) > 1
}

// ignoreMultiUserNonMention stays out of multi-user threads unless the bot
// was addressed: plain replies there are usually colleagues talking to each
// other, not to the bot. It reports whether the message was ignored.
// Single-user threads and thread-less platforms never ignore. Callers that
// may hold an outstanding blocking prompt must route awaiting messages to
// handleBlockingResponse first so an answer is never mistaken for chatter.
func (m *BotConversationManager) ignoreMultiUserNonMention(active *activeBotSession, msg BotIncomingMessage) bool {
	if active == nil || msg.IsMention || !m.isMultiUserThread(active, msg) {
		return false
	}
	log.Printf("[BOT_MANAGER] Ignoring non-mention thread reply from %s in multi-user session %s", msg.UserID, active.SessionID)
	// Reaction-only acknowledgement so the user can see the bot
	// noticed the message but is intentionally staying out of the
	// thread (no @mention in a multi-user channel). Without this
	// the message just falls into a silent void and looks like a
	// bug. Best-effort: log and move on if the platform doesn't
	// support reactions or the call fails.
	if connector := m.GetConnector(active.Platform); connector != nil && connector.Capabilities().Reactions && msg.ChannelID != "" && msg.MessageTS != "" {
		thread := ThreadID{Platform: msg.Platform, ChannelID: msg.ChannelID, ConnectionID: msg.ConnectionID}
		if err := botConnectorAddReaction(context.Background(), connector, thread, msg.MessageTS, "zipper_mouth_face"); err != nil {
			log.Printf("[BOT_MANAGER] Failed to add ignore-reaction on %s: %v", active.SessionID, err)
		}
	}
	return true
}

// ignoreOtherUserMention stays silent when a thread reply tags another
// platform user but not the bot: the message is addressed to a colleague,
// not to it. Unlike the multi-user guard above, this applies no matter how
// few people are in the thread, and it leaves no reaction — even an ack
// would be a visible act on someone else's conversation. Callers that may
// hold an outstanding blocking prompt must route awaiting messages to
// handleBlockingResponse first so an answer is never mistaken for chatter,
// and control commands must be matched before this for the same reason.
// active may be nil on paths without a live session (restore/fresh start).
func (m *BotConversationManager) ignoreOtherUserMention(active *activeBotSession, msg BotIncomingMessage) bool {
	if msg.IsMention || !msg.IsThreadReply || !msg.MentionsOtherUser {
		return false
	}
	session := "none"
	if active != nil {
		session = active.SessionID
	}
	log.Printf("[BOT_MANAGER] Ignoring thread reply from %s (session %s): tags another user, not the bot", msg.UserID, session)
	return true
}

// Helper functions (prefixed with bot to avoid collisions)

func botTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func botNormalizeText(s string) string {
	s = strings.TrimSpace(s)
	return strings.ToLower(s)
}

func isPlanApprovalResponse(text string) bool {
	switch text {
	case "approve", "approved", "execute", "go", "yes", "y", "ok", "proceed", "do it", "run it", "start", "lgtm":
		return true
	}
	return false
}

func isPlanRejectionResponse(text string) bool {
	switch text {
	case "reject", "rejected", "no", "n", "cancel", "stop", "nope", "nah", "abort":
		return true
	}
	return false
}

// getThreadOffset returns the current message count for a thread. Only the
// (since removed) web simulator ever tracked one; every real platform starts
// at 0.
func (m *BotConversationManager) getThreadOffset(_ ThreadID) int {
	return 0
}

func (m *BotConversationManager) SetWorkflowTurnFunc(fn func(ctx context.Context, query string, route ChannelRoute, thread ThreadID, dmUserID string) (map[string]interface{}, error)) {
	m.workflowTurn = fn
}

// SetUserWorkflowChatFunc installs the lookup of a user's own workflow chat.
func (m *BotConversationManager) SetUserWorkflowChatFunc(fn func(ctx context.Context, userID string, route ChannelRoute) string) {
	m.userWorkflowChat = fn
}
