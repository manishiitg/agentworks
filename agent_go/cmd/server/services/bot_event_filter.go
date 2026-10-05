package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	orchestrator_events "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/mcpagent/events"
)

// BlockingEventCallback is called when a blocking event is received.
// The eventType is "plan_approval" or "blocking_human_feedback".
// requestID is set for blocking_human_feedback events when available.
type BlockingEventCallback func(eventType string, requestID string)

// SessionDoneCallback is called when the event filter determines the session is truly complete.
// This fires when: a main-level completion arrives, no delegations are pending, and no blocking events are active.
type SessionDoneCallback func()

type BotNotificationKind string

const (
	BotNotifyNone          BotNotificationKind = ""
	BotNotifyBuilderText   BotNotificationKind = "builder_message"
	BotNotifyStepStarted   BotNotificationKind = "workflow_step_started"
	BotNotifyStepCompleted BotNotificationKind = "workflow_step_completed"
	BotNotifyHumanInput    BotNotificationKind = "human_input_required"
	BotNotifyError         BotNotificationKind = "error"
)

// BotEventFilter filters agent events and posts updates to a platform thread.
// It also tracks session lifecycle: delegations, blocking events, and completion.
type BotEventFilter struct {
	connector  BotConnector
	threadID   ThreadID
	sessionID  string // unified chat session id (folder name + thread index key)
	appBaseURL string // public app URL for shareable links (e.g., "https://app.example.com")
	userID     string // workspace user ID for shareable links (e.g., "default")

	progressMu         sync.Mutex // serialize progress sends with terminal cleanup
	progressMessageID  string
	mu                 sync.Mutex
	delegationNames    map[string]string // correlationID -> instruction (sub-agent name)
	startedAgents      map[string]bool   // correlationID/name keys already announced as started
	endedAgents        map[string]bool   // correlationID/name keys already announced as ended
	pendingDelegations int
	awaitingInput      bool // set on any blocking event, cleared externally
	completionReceived bool // set when unified_completion, agent_end, or conversation_end arrives
	sessionFailed      bool // authoritative main-level error completion
	sessionDone        bool // idempotency guard — ensures onSessionDone fires at most once
	baseHierarchy      int  // the minimum hierarchy level seen — treated as "main" level
	baseHierarchySet   bool // true once baseHierarchy has been calibrated
	// sawUserMessage is set by the first user_message event. A calibration
	// made before it came from preamble events (a restored session's relaunch
	// replays terminal output at another level) and is redone from the turn.
	sawUserMessage       bool
	lastActivity         string // human-friendly description of current activity
	toolCallCount        int    // total tool calls seen (for progress feel)
	mainTextSent         bool   // true once we've sent main-level text via llm_generation_end
	lastErrorText        string // prevents duplicate error/completion notifications
	lastMainText         string // last main-level text sent to the bot thread this turn
	streamingReplyPrefix string // cumulative text already posted before a follow-up
	streamingMessageID   string // channel message ID for the in-place streamed reply
	streamingText        string // accumulated clean assistant text for the current streamed reply
	streamingSentText    string // last accumulated text actually visible in Slack
	lastStreamingUpdate  time.Time
	sendFullDetails      bool // opt-in: forward workflow runtime chatter to bot channel
	onBlockingEvent      BlockingEventCallback
	onSessionDone        SessionDoneCallback
}

// NewBotEventFilter creates a new event filter
func NewBotEventFilter(connector BotConnector, threadID ThreadID, sessionID, appBaseURL, userID string) *BotEventFilter {
	return &BotEventFilter{
		connector:       connector,
		threadID:        threadID,
		sessionID:       sessionID,
		appBaseURL:      strings.TrimSuffix(appBaseURL, "/"),
		userID:          userID,
		delegationNames: make(map[string]string),
		startedAgents:   make(map[string]bool),
		endedAgents:     make(map[string]bool),
	}
}

// SetBlockingEventCallback sets the callback for blocking events (plan_approval, human feedback, etc.)
func (f *BotEventFilter) SetBlockingEventCallback(cb BlockingEventCallback) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onBlockingEvent = cb
}

// SetSessionDoneCallback sets the callback invoked when the session is determined to be complete
func (f *BotEventFilter) SetSessionDoneCallback(cb SessionDoneCallback) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onSessionDone = cb
}

// SetSendFullDetails enables the verbose bot stream for sessions whose route
// explicitly opts in. The default is concise: only builder-facing messages,
// blocking prompts, and errors are sent to Slack/WhatsApp.
func (f *BotEventFilter) SetSendFullDetails(enabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendFullDetails = enabled
}

// ResetForNewTurn clears the one-shot completion state so a follow-up user
// message (injected while the session is still held open, e.g. because a
// background workflow is running) starts a fresh turn that can be tracked.
// Without this, sessionDone latches on the first turn and later turns fire
// no onSessionDone — the parent can then be canceled mid-reply.
func (f *BotEventFilter) ResetForNewTurn() {
	f.clearProgressMessage("Request finished.")
	f.mu.Lock()
	f.sessionDone = false
	f.sessionFailed = false
	f.completionReceived = false
	f.mainTextSent = false
	f.lastMainText = ""
	f.lastErrorText = ""
	f.streamingReplyPrefix = ""
	f.streamingMessageID = ""
	f.streamingText = ""
	f.streamingSentText = ""
	f.lastStreamingUpdate = time.Time{}
	f.mu.Unlock()
	log.Printf("[BOT_FILTER] Reset for new turn")
}

// beginMessageReply changes the visible reply boundary without resetting the
// agent turn: a message sent while running is still steering that same turn.
// The accepted user event serializes this with streaming events, so subsequent
// output appears below the new user message instead of editing an older reply.
func (f *BotEventFilter) beginMessageReply() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.streamingMessageID == "" {
		return
	}
	f.streamingReplyPrefix += f.streamingText
	f.streamingMessageID = ""
	f.streamingText = ""
	f.streamingSentText = ""
	f.lastStreamingUpdate = time.Time{}
}

// SessionFailed reports whether the current turn ended with a terminal error.
func (f *BotEventFilter) SessionFailed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessionFailed
}

// HasSentMainText reports whether this turn already forwarded a main builder
// reply. The synthetic-turn fallback uses this to avoid duplicate bot messages
// when the normal event stream already delivered the final text.
func (f *BotEventFilter) HasSentMainText() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mainTextSent
}

// ShouldSendSyntheticFinal reports whether a persisted final builder message
// should still be forwarded. Synthetic turns may emit an earlier builder text
// event and later persist a more complete final answer after tool reads; only
// suppress true duplicates, not materially different final replies.
func (f *BotEventFilter) ShouldSendSyntheticFinal(message string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.mainTextSent {
		return true
	}
	return !sameBotMainText(message, f.lastMainText)
}

// MarkMainTextSent marks the current turn as forwarding builder text. It may
// be called just before the connector send starts so fallback delivery can see
// the in-flight send and avoid duplicating it.
func (f *BotEventFilter) MarkMainTextSent(message string) {
	f.mu.Lock()
	f.mainTextSent = true
	f.lastMainText = strings.TrimSpace(message)
	f.mu.Unlock()
}

// ClearBlockingState clears the awaiting input flag (called when user responds to a blocking event).
// Also resets completionReceived so the session stays alive for the follow-up agent's completion.
func (f *BotEventFilter) ClearBlockingState() {
	f.mu.Lock()
	f.awaitingInput = false
	f.completionReceived = false
	f.mainTextSent = false // reset so follow-up agent's completion will be shown
	f.lastMainText = ""
	f.streamingMessageID = ""
	f.streamingText = ""
	f.streamingSentText = ""
	f.lastStreamingUpdate = time.Time{}
	f.mu.Unlock()
	log.Printf("[BOT_FILTER] Blocking state cleared, completion reset")
}

func sameBotMainText(a, b string) bool {
	a = normalizeBotMainText(a)
	b = normalizeBotMainText(b)
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.Contains(b, a)
}

func normalizeBotMainText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.NewReplacer("`", "", "*", "", "_", "", "~", "").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// Start begins listening to events for a session and forwarding filtered updates to the thread
func (f *BotEventFilter) Start(ctx context.Context, subscriber BotEventSubscriber, sessionID string) {
	log.Printf("[BOT_FILTER] Start: subscribing to events for session %s", sessionID)
	ch, unsubscribe := subscriber.SubscribeBot(sessionID)
	defer func() {
		log.Printf("[BOT_FILTER] Start: exiting event loop for session %s", sessionID)
		f.clearProgressMessage("Request stopped.")
		unsubscribe()
	}()

	// Heartbeat — escalating intervals, minimal Slack-native style
	heartbeatIntervals := []time.Duration{30 * time.Second, 60 * time.Second, 90 * time.Second}
	heartbeatCount := 0
	heartbeat := time.NewTicker(heartbeatIntervals[0])
	defer heartbeat.Stop()
	lastSendTime := time.Now()
	lastEventTime := time.Time{} // zero until first event received
	heartbeatSkipLogged := false // log the post-reply pause once per turn, not every tick

	for {
		select {
		case <-ctx.Done():
			log.Printf("[BOT_FILTER] Start: context canceled for session %s", sessionID)
			return

		case <-heartbeat.C:
			f.mu.Lock()
			done := f.sessionDone
			awaiting := f.awaitingInput
			activity := f.lastActivity
			toolCount := f.toolCallCount
			pendingDel := f.pendingDelegations
			// Once this turn's reply is out, remaining events are background
			// work the reply already announced ("I'll update this thread");
			// a placeholder then only clutters the thread (RTS 2026-09-25).
			replied := f.mainTextSent
			f.mu.Unlock()
			// Only send heartbeat if: session active, reply not yet sent, events flowing recently, and no message sent recently
			if replied && !heartbeatSkipLogged {
				log.Printf("[BOT_FILTER] heartbeat: paused for thread=%s, reply already sent", f.threadID.Key())
				heartbeatSkipLogged = true
			} else if !replied {
				heartbeatSkipLogged = false
			}
			if !done && !awaiting && !replied && f.capabilities().ProgressUpdates && !lastEventTime.IsZero() &&
				time.Since(lastEventTime) < 60*time.Second &&
				time.Since(lastSendTime) > 20*time.Second {
				msg := f.buildHeartbeatMessage(activity, toolCount, pendingDel, heartbeatCount)
				f.sendProgressMessage(ctx, msg)
				lastSendTime = time.Now()
			}
			// Escalate interval: 30s → 60s → 90s (then stay at 90s)
			heartbeatCount++
			if heartbeatCount < len(heartbeatIntervals) {
				heartbeat.Reset(heartbeatIntervals[heartbeatCount])
			}

		case event, ok := <-ch:
			if !ok {
				log.Printf("[BOT_FILTER] Start: channel closed for session %s", sessionID)
				return
			}
			lastEventTime = time.Now()
			sent := f.processEvent(ctx, event)
			if sent {
				lastSendTime = time.Now()
			}
		}
	}
}

// isMainLevel returns true if the event is at the base hierarchy level for this session.
// Bot sessions may run at hierarchy > 0 (e.g., handleQuery wraps the agent), so we
// calibrate the base level from the first event and treat it as "main".
func (f *BotEventFilter) isMainLevel(event BotEventData) bool {
	if event.Data == nil {
		return false
	}
	f.mu.Lock()
	if !f.baseHierarchySet {
		f.baseHierarchy = event.Data.HierarchyLevel
		f.baseHierarchySet = true
		log.Printf("[BOT_FILTER] Calibrated base hierarchy level to %d from %s (thread=%s after_user_message=%v)",
			f.baseHierarchy, event.Type, f.threadID.Key(), f.sawUserMessage)
	}
	base := f.baseHierarchy
	f.mu.Unlock()
	return event.Data.HierarchyLevel <= base
}

// processEvent handles a single event. Returns true if a message was sent to the thread.
func (f *BotEventFilter) processEvent(ctx context.Context, event BotEventData) bool {
	if event.Type == "user_message" {
		f.mu.Lock()
		if !f.sawUserMessage {
			f.sawUserMessage = true
			// Events before the turn's own user message are preamble (RTS
			// 2026-09-25: a restored session's relaunch replayed terminal
			// chunks at level 0, the turn ran at level 3, and its reply was
			// skipped as a sub-agent). Recalibrate from the turn itself.
			if f.baseHierarchySet {
				log.Printf("[BOT_FILTER] Dropping pre-turn hierarchy calibration %d for thread=%s; recalibrating from the turn", f.baseHierarchy, f.threadID.Key())
				f.baseHierarchySet = false
			}
		}
		f.mu.Unlock()
		f.beginMessageReply()
		return false
	}
	if event.Data == nil {
		return false
	}

	switch event.Type {
	case "orchestrator_agent_start":
		if kind := f.classifyBotNotification(event); kind == BotNotifyStepStarted {
			if f.suppressWorkflowRuntimeChatter(event) {
				log.Printf("[BOT_FILTER] orchestrator_agent_start: suppressed workflow step notice for %s", f.threadID.Platform)
				return false
			}
			msg := f.formatOrchestratorAgentStart(event)
			if msg != "" {
				f.sendMessage(ctx, msg)
				return true
			}
		}

	case "orchestrator_agent_end":
		if kind := f.classifyBotNotification(event); kind == BotNotifyStepCompleted {
			if f.suppressWorkflowRuntimeChatter(event) {
				log.Printf("[BOT_FILTER] orchestrator_agent_end: suppressed workflow step notice for %s", f.threadID.Platform)
				return false
			}
			msg := f.formatOrchestratorAgentEnd(event)
			if msg != "" {
				f.sendMessage(ctx, msg)
				return true
			}
		}

	case "delegation_start":
		f.trackDelegationName(event)
		f.mu.Lock()
		f.pendingDelegations++
		pending := f.pendingDelegations
		// Set activity to sub-agent name if available
		if name := f.getDelegationName(event); name != "" {
			f.lastActivity = fmt.Sprintf("Delegated: %s", name)
		} else {
			f.lastActivity = "Delegating to sub-agent"
		}
		f.mu.Unlock()
		log.Printf("[BOT_FILTER] delegation_start: pending=%d", pending)

	case "tool_call_start":
		// Track activity for heartbeat — translate tool names to user-friendly descriptions
		if toolDesc := f.describeToolCall(event); toolDesc != "" {
			f.mu.Lock()
			f.toolCallCount++
			f.lastActivity = toolDesc
			f.mu.Unlock()
		}

	case "streaming_chunk":
		return f.sendStreamingChunk(ctx, event)

	case "llm_generation_end":
		kind := f.classifyBotNotification(event)
		if kind == BotNotifyBuilderText {
			msg := f.formatGenerationEnd(event)
			if f.flushStreamingMessage(ctx, msg) {
				log.Printf("[BOT_FILTER] llm_generation_end: flushed streamed reply")
				return true
			}
			// The progressive-text poller (see SendProgressiveText) may have
			// already forwarded this exact reply straight from durable
			// conversation history, ahead of this event; a genuinely
			// different reply (a synthetic turn's earlier text was this
			// generation's actual predecessor) still goes out.
			if !f.ShouldSendSyntheticFinal(msg) {
				log.Printf("[BOT_FILTER] llm_generation_end: already forwarded via progressive text, skipping")
				return false
			}
			log.Printf("[BOT_FILTER] llm_generation_end: sending %d chars (level=%d)", len(msg), event.Data.HierarchyLevel)
			return f.sendMainText(ctx, msg)
		} else {
			log.Printf("[BOT_FILTER] llm_generation_end: skipped by policy (kind=%q mainLevel=%v workflowScoped=%v level=%d)", kind, f.isMainLevel(event), f.isWorkflowScopedEvent(event), event.Data.HierarchyLevel)
		}

	case "delegation_end":
		f.mu.Lock()
		if f.pendingDelegations > 0 {
			f.pendingDelegations--
		}
		pending := f.pendingDelegations
		awaiting := f.awaitingInput
		f.mu.Unlock()
		log.Printf("[BOT_FILTER] delegation_end: pending=%d awaitingInput=%v", pending, awaiting)
		f.checkSessionDone("delegation_end")

	case "unified_completion":
		// A terminal failure must report its error and release the turn, even
		// without a separate error event. A nested failure does not finish its parent.
		if uc, ok := event.Data.Data.(*events.UnifiedCompletionEvent); ok && uc.Status == "error" {
			if f.isMainLevel(event) {
				message := strings.TrimSpace(uc.Error)
				if message == "" {
					message = "The previous turn failed."
				}
				sent := f.sendErrorMessage(ctx, "Error: "+message)
				f.mu.Lock()
				f.sessionFailed = true
				f.completionReceived = true
				f.awaitingInput = false
				f.pendingDelegations = 0
				f.mu.Unlock()
				f.checkSessionDone("unified_completion_error")
				return sent
			}
			return false
		}
		sent := false
		isMain := f.isMainLevel(event)
		if uc, ok := event.Data.Data.(*events.UnifiedCompletionEvent); ok {
			log.Printf("[BOT_FILTER] unified_completion: isMain=%v level=%d result_len=%d", isMain, event.Data.HierarchyLevel, len(uc.FinalResult))
		}
		kind := f.classifyBotNotification(event)
		if kind != BotNotifyBuilderText {
			// Sub-agent completions are intentionally NOT forwarded to the thread —
			// they were spamming Slack with per-step technical dumps (PR lists, log
			// excerpts, etc.) when each nested sub-agent reported back. The user
			// only cares about the main agent's synthesized reply, which arrives
			// via llm_generation_end / main-level unified_completion. Sub-agent
			// detail still lives in workflow logs and the run folder.
			f.mu.Lock()
			base := f.baseHierarchy
			f.mu.Unlock()
			log.Printf("[BOT_FILTER] unified_completion: skipped by policy (kind=%q mainLevel=%v workflowScoped=%v level=%d base=%d thread=%s)", kind, isMain, f.isWorkflowScopedEvent(event), event.Data.HierarchyLevel, base, f.threadID.Key())
		} else {
			// Main-level completion — only send if this exact text wasn't
			// already forwarded (via llm_generation_end, or the progressive
			// poller reading it straight from conversation history). A
			// content check, not a one-shot flag: the poller may have sent
			// an earlier, different reply for this same turn, and the real
			// final text must still go out. Dedup on the raw result, not
			// the "**Result:**\n"-wrapped message about to be sent — that
			// wrapper alone made an otherwise-identical reply look new and
			// sent it a second time (caught live: a normal reply, then the
			// same thing again a second later with a "*Result:*" label).
			uc, _ := event.Data.Data.(*events.UnifiedCompletionEvent)
			msg := f.formatUnifiedCompletion(event)
			summary := ""
			if uc != nil {
				summary = cleanBotStreamingContent(uc.FinalResult, false)
			}
			if summary != "" && f.flushStreamingMessage(ctx, summary) {
				log.Printf("[BOT_FILTER] unified_completion: flushed streamed reply")
				sent = true
			} else if msg != "" && uc != nil && f.ShouldSendSyntheticFinal(uc.FinalResult) {
				log.Printf("[BOT_FILTER] unified_completion: sending main-level result")
				sent = f.sendMainText(ctx, msg)
			} else {
				log.Printf("[BOT_FILTER] unified_completion: skipping main-level (already forwarded)")
			}
		}
		// Only main-level completions signal that the session is done.
		if isMain {
			f.mu.Lock()
			f.completionReceived = true
			f.mu.Unlock()
			f.checkSessionDone("unified_completion")
		}
		return sent

	case "plan_approval":
		f.setBlocking("plan_approval", "")
		if kind := f.classifyBotNotification(event); kind != BotNotifyHumanInput {
			log.Printf("[BOT_FILTER] plan_approval: skipped by policy (kind=%q)", kind)
			return false
		}
		msg := f.formatPlanApproval(event)
		if msg != "" {
			f.sendMessage(ctx, msg)
			return true
		}

	case "blocking_human_feedback":
		f.setBlocking("blocking_human_feedback", blockingFeedbackRequestID(event))
		if kind := f.classifyBotNotification(event); kind != BotNotifyHumanInput {
			log.Printf("[BOT_FILTER] blocking_human_feedback: skipped by policy (kind=%q)", kind)
			return false
		}
		msg := f.formatBlockingFeedback(event)
		if msg != "" {
			f.sendMessage(ctx, msg)
			return true
		}

	case "agent_end", "conversation_end":
		// Only main-level events signal session completion
		if f.isMainLevel(event) {
			f.mu.Lock()
			f.completionReceived = true
			f.mu.Unlock()
			f.checkSessionDone(event.Type)
		}

	case "agent_error", "conversation_error", "orchestrator_agent_error":
		if kind := f.classifyBotNotification(event); kind == BotNotifyError {
			return f.sendErrorMessage(ctx, f.formatErrorEvent(event))
		}
	}
	return false
}

func (f *BotEventFilter) classifyBotNotification(event BotEventData) BotNotificationKind {
	if event.Data == nil || event.Data.Data == nil {
		return BotNotifyNone
	}

	switch event.Type {
	case "orchestrator_agent_start":
		if f.isWorkflowStepLifecycleEvent(event) {
			return BotNotifyStepStarted
		}

	case "orchestrator_agent_end":
		if f.isWorkflowStepLifecycleEvent(event) {
			return BotNotifyStepCompleted
		}

	case "llm_generation_end":
		if f.isBuilderGenerationEvent(event) {
			return BotNotifyBuilderText
		}

	case "unified_completion":
		if f.isBuilderCompletionEvent(event) {
			return BotNotifyBuilderText
		}

	case "plan_approval", "blocking_human_feedback":
		return BotNotifyHumanInput

	case "agent_error", "conversation_error", "orchestrator_agent_error":
		return BotNotifyError
	}

	return BotNotifyNone
}

func (f *BotEventFilter) capabilities() ChannelCapabilities {
	if f.connector == nil {
		return ChannelCapabilities{}
	}
	return f.connector.Capabilities()
}

func (f *BotEventFilter) suppressWorkflowRuntimeChatter(event BotEventData) bool {
	f.mu.Lock()
	full := f.sendFullDetails
	f.mu.Unlock()
	return f.isWorkflowScopedEvent(event) && (!f.capabilities().WorkflowProgress || !full)
}

func (f *BotEventFilter) isWorkflowScopedEvent(event BotEventData) bool {
	if event.Data == nil || event.Data.Data == nil {
		return false
	}

	meta := eventMetadata(event)
	if len(meta) == 0 {
		return false
	}

	for _, key := range []string{
		"current_step_id",
		"orchestrator_step_id",
		"workflow_step_id",
		"step_id",
		"route_id",
		"workshop_step_id",
	} {
		if strings.TrimSpace(metadataString(meta, key)) != "" {
			return true
		}
	}
	return false
}

func (f *BotEventFilter) isBuilderGenerationEvent(event BotEventData) bool {
	if event.Data == nil || event.Data.Data == nil || !f.isMainLevel(event) {
		return false
	}
	if f.suppressWorkflowRuntimeChatter(event) {
		return false
	}
	gen, ok := event.Data.Data.(*events.LLMGenerationEndEvent)
	return ok && strings.TrimSpace(gen.Content) != ""
}

func (f *BotEventFilter) isBuilderCompletionEvent(event BotEventData) bool {
	if event.Data == nil || event.Data.Data == nil || !f.isMainLevel(event) {
		return false
	}
	if f.suppressWorkflowRuntimeChatter(event) {
		return false
	}
	uc, ok := event.Data.Data.(*events.UnifiedCompletionEvent)
	return ok && uc.Status != "error" && strings.TrimSpace(uc.FinalResult) != ""
}

func (f *BotEventFilter) isWorkflowStepLifecycleEvent(event BotEventData) bool {
	if event.Data == nil || event.Data.Data == nil {
		return false
	}
	switch d := event.Data.Data.(type) {
	case *orchestrator_events.OrchestratorAgentStartEvent:
		return isWorkflowStepAgent(d.AgentType, d.AgentName)
	case *orchestrator_events.OrchestratorAgentEndEvent:
		return isWorkflowStepAgent(d.AgentType, d.AgentName)
	default:
		return false
	}
}

func eventMetadata(event BotEventData) map[string]interface{} {
	if event.Data == nil || event.Data.Data == nil {
		return nil
	}
	type baseDataCarrier interface {
		GetBaseEventData() *events.BaseEventData
	}
	if carrier, ok := event.Data.Data.(baseDataCarrier); ok {
		base := carrier.GetBaseEventData()
		if base != nil {
			return base.Metadata
		}
	}
	return nil
}

func metadataString(meta map[string]interface{}, key string) string {
	if meta == nil {
		return ""
	}
	v, ok := meta[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprint(t)
	}
}

func isWorkflowStepAgent(agentType, agentName string) bool {
	agentType = strings.ToLower(strings.TrimSpace(agentType))
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return false
	}

	if agentName == "Full Workflow Execution" ||
		strings.Contains(agentType, "chat") ||
		strings.Contains(agentName, "chat-agent") ||
		strings.Contains(agentType, "learning") ||
		strings.Contains(agentType, "validation") ||
		strings.Contains(agentType, "organizer") {
		return false
	}

	return strings.Contains(agentType, "step") ||
		strings.Contains(agentType, "execution") ||
		strings.Contains(agentType, "routing")
}

func workflowStepDisplayName(agentName string) string {
	displayName := strings.TrimSpace(strings.TrimPrefix(agentName, "Step:"))
	if displayName == "" {
		displayName = strings.TrimSpace(agentName)
	}
	return displayName
}

func workflowStepGroup(inputData map[string]string) string {
	if inputData == nil {
		return ""
	}
	group := strings.TrimSpace(inputData["group_name"])
	if group == "" {
		group = strings.TrimSpace(inputData["GroupName"])
	}
	return group
}

// setBlocking sets the blocking state and notifies the callback
func (f *BotEventFilter) setBlocking(eventType, requestID string) {
	f.mu.Lock()
	f.awaitingInput = true
	cb := f.onBlockingEvent
	f.mu.Unlock()

	f.clearProgressMessage("Waiting for your reply.")
	log.Printf("[BOT_FILTER] Blocking event: %s request_id=%s", eventType, requestID)
	if cb != nil {
		cb(eventType, requestID)
	}
}

// checkSessionDone checks if the session is truly complete:
// completionReceived AND no pending delegations AND not awaiting user input.
// The sessionDone flag ensures onSessionDone fires at most once.
func (f *BotEventFilter) checkSessionDone(eventType string) {
	f.mu.Lock()
	if f.sessionDone {
		f.mu.Unlock()
		return
	}
	pending := f.pendingDelegations
	awaiting := f.awaitingInput
	completed := f.completionReceived
	cb := f.onSessionDone
	if pending == 0 && !awaiting && completed {
		f.sessionDone = true
	}
	f.mu.Unlock()

	if pending > 0 {
		log.Printf("[BOT_FILTER] %s: %d delegations still pending", eventType, pending)
		return
	}
	if awaiting {
		log.Printf("[BOT_FILTER] %s: awaiting user input", eventType)
		return
	}
	if !completed {
		log.Printf("[BOT_FILTER] %s: no completion event yet", eventType)
		return
	}
	f.clearProgressMessage("Request finished.")
	log.Printf("[BOT_FILTER] %s: session done", eventType)
	if cb != nil {
		cb()
	}
}

// trackDelegationName extracts the sub-agent instruction from delegation_start events.
func (f *BotEventFilter) trackDelegationName(event BotEventData) {
	if event.Data == nil || event.Data.Data == nil {
		return
	}

	raw, err := json.Marshal(event.Data.Data)
	if err != nil {
		return
	}
	var parsed struct {
		DelegationID string `json:"delegation_id"`
		Instruction  string `json:"instruction"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return
	}

	corrID := event.Data.CorrelationID
	if corrID == "" {
		corrID = parsed.DelegationID
	}
	if corrID != "" && parsed.Instruction != "" {
		// Store a short name — first line capped at 60 chars
		name := strings.SplitN(parsed.Instruction, "\n", 2)[0]
		if len(name) > 60 {
			name = name[:60] + "..."
		}
		f.mu.Lock()
		f.delegationNames[corrID] = name
		f.mu.Unlock()
	}
}

// formatOrchestratorAgentStart formats workflow sub-agent starts for bot connectors.
// It intentionally skips the main chat/builder agent and internal helper agents so
// WhatsApp/Slack get step-level breadcrumbs without tool-call noise.
func (f *BotEventFilter) formatOrchestratorAgentStart(event BotEventData) string {
	if event.Data == nil || event.Data.Data == nil {
		return ""
	}

	start, ok := event.Data.Data.(*orchestrator_events.OrchestratorAgentStartEvent)
	if !ok {
		return ""
	}

	agentType := strings.ToLower(strings.TrimSpace(start.AgentType))
	agentName := strings.TrimSpace(start.AgentName)
	if !isWorkflowStepAgent(agentType, agentName) {
		return ""
	}

	displayName := workflowStepDisplayName(agentName)

	key := workflowStepDedupeKey(event, agentType, displayName)
	f.mu.Lock()
	if f.startedAgents[key] {
		f.mu.Unlock()
		return ""
	}
	f.startedAgents[key] = true
	f.lastActivity = fmt.Sprintf("Step started: %s", displayName)
	f.mu.Unlock()

	group := workflowStepGroup(start.InputData)
	var msg string
	if group != "" {
		msg = fmt.Sprintf("Step started (%s): running now [%s].", displayName, group)
	} else {
		msg = fmt.Sprintf("Step started (%s): running now.", displayName)
	}

	f.mu.Lock()
	sendFullDetails := f.sendFullDetails
	f.mu.Unlock()
	if sendFullDetails {
		if userMessage := strings.TrimSpace(start.UserMessage); userMessage != "" {
			msg += "\n\nUser message sent to agent:\n\n" + userMessage
		}
	}
	return msg
}

// formatOrchestratorAgentEnd formats workflow step ends without forwarding the
// step result body. Tool calls and detailed completions remain in the web UI/logs.
func (f *BotEventFilter) formatOrchestratorAgentEnd(event BotEventData) string {
	if event.Data == nil || event.Data.Data == nil {
		return ""
	}

	end, ok := event.Data.Data.(*orchestrator_events.OrchestratorAgentEndEvent)
	if !ok {
		return ""
	}

	agentType := strings.ToLower(strings.TrimSpace(end.AgentType))
	agentName := strings.TrimSpace(end.AgentName)
	if !isWorkflowStepAgent(agentType, agentName) {
		return ""
	}

	displayName := workflowStepDisplayName(agentName)
	key := workflowStepDedupeKey(event, agentType, displayName)
	f.mu.Lock()
	if f.endedAgents[key] {
		f.mu.Unlock()
		return ""
	}
	f.endedAgents[key] = true
	f.lastActivity = fmt.Sprintf("Step ended: %s", displayName)
	f.mu.Unlock()

	group := workflowStepGroup(end.InputData)
	duration := formatStepDuration(end.Duration)
	status := "completed"
	if !end.Success {
		status = "failed"
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("Step %s (%s)", status, displayName))
	if duration != "" {
		parts = append(parts, duration)
	}
	suffix := ""
	if group != "" {
		suffix = fmt.Sprintf(" [%s]", group)
	}
	return strings.Join(parts, ": ") + suffix + "."
}

func workflowStepDedupeKey(event BotEventData, agentType, displayName string) string {
	meta := eventMetadata(event)
	for _, key := range []string{
		"current_step_id",
		"orchestrator_step_id",
		"workflow_step_id",
		"step_id",
		"route_id",
	} {
		if value := strings.TrimSpace(metadataString(meta, key)); value != "" {
			return agentType + ":" + value + ":" + displayName
		}
	}
	if event.Data != nil && strings.TrimSpace(event.Data.CorrelationID) != "" {
		return agentType + ":" + event.Data.CorrelationID + ":" + displayName
	}
	return agentType + ":" + displayName
}

func formatStepDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

// formatUnifiedCompletion formats a unified_completion event.
func (f *BotEventFilter) formatUnifiedCompletion(event BotEventData) string {
	if event.Data == nil || event.Data.Data == nil {
		return ""
	}

	uc, ok := event.Data.Data.(*events.UnifiedCompletionEvent)
	if !ok {
		return ""
	}

	isSubAgent := !f.isMainLevel(event)

	summary := cleanBotStreamingContent(uc.FinalResult, false)

	if isSubAgent {
		name := ""
		corrID := event.Data.CorrelationID
		if corrID != "" {
			f.mu.Lock()
			name = f.delegationNames[corrID]
			f.mu.Unlock()
		}
		if name == "" {
			name = "Sub-agent"
		}

		if summary != "" {
			return fmt.Sprintf("**[%s]**\n%s", name, summary)
		}
		return "" // no useful info to show
	}

	// Main-level completion — only show if there's actual content
	if summary != "" {
		return fmt.Sprintf("**Result:**\n%s", summary)
	}
	// No summary — skip (Session completed message covers this)
	return ""
}

// formatPlanApproval formats a plan_approval event.
func (f *BotEventFilter) formatPlanApproval(event BotEventData) string {
	if event.Data == nil || event.Data.Data == nil {
		return "**Plan ready for review.**\nReply **approve** to execute or **reject** to cancel."
	}

	raw, err := json.Marshal(event.Data.Data)
	if err != nil {
		return "**Plan ready for review.**\nReply **approve** to execute or **reject** to cancel."
	}
	var parsed struct {
		Question string `json:"question"`
		Context  string `json:"context"` // plan markdown
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "**Plan ready for review.**\nReply **approve** to execute or **reject** to cancel."
	}

	var sb strings.Builder
	sb.WriteString("**Plan ready for review:**\n\n")

	planContent := parsed.Context
	if planContent != "" {
		sb.WriteString(planContent)
		sb.WriteString("\n\n")
	}

	sb.WriteString("Reply **approve** to execute or **reject** to cancel.")
	return sb.String()
}

// formatBlockingFeedback formats a blocking_human_feedback event.
func (f *BotEventFilter) formatBlockingFeedback(event BotEventData) string {
	if event.Data == nil || event.Data.Data == nil {
		return "The agent needs your input to continue."
	}

	parsed, ok := parseBlockingFeedbackPayload(event)
	if !ok {
		return "The agent needs your input to continue."
	}

	question := parsed.Question
	if question == "" {
		question = parsed.Message
	}
	if question == "" {
		question = parsed.Prompt
	}
	if question == "" {
		return "The agent needs your input to continue."
	}

	var sb strings.Builder
	sb.WriteString("**Waiting for input:**\n")
	sb.WriteString(question)
	if parsed.Context != "" {
		sb.WriteString("\n\n")
		sb.WriteString(parsed.Context)
	}
	if parsed.YesNoOnly {
		yes := strings.TrimSpace(parsed.YesLabel)
		if yes == "" {
			yes = "Approve"
		}
		no := strings.TrimSpace(parsed.NoLabel)
		if no == "" {
			no = "Reject"
		}
		sb.WriteString(fmt.Sprintf("\n\nReply with **%s** or **%s**.", yes, no))
	} else if len(parsed.Options) > 0 {
		sb.WriteString("\n\nOptions:")
		for i, opt := range parsed.Options {
			sb.WriteString(fmt.Sprintf("\n%d. %s", i+1, opt))
		}
		sb.WriteString("\n\nReply with the option text or `option0`, `option1`, etc.")
	} else {
		sb.WriteString("\n\nReply with your answer.")
	}
	return sb.String()
}

type blockingFeedbackPayload struct {
	RequestID          string   `json:"request_id"`
	Question           string   `json:"question"`
	Message            string   `json:"message"`
	Prompt             string   `json:"prompt"`
	Context            string   `json:"context"`
	YesNoOnly          bool     `json:"yes_no_only"`
	YesLabel           string   `json:"yes_label"`
	NoLabel            string   `json:"no_label"`
	Options            []string `json:"options"`
	RoutedToParentChat bool     `json:"routed_to_parent_chat"`
}

func parseBlockingFeedbackPayload(event BotEventData) (blockingFeedbackPayload, bool) {
	if event.Data == nil || event.Data.Data == nil {
		return blockingFeedbackPayload{}, false
	}
	raw, err := json.Marshal(event.Data.Data)
	if err != nil {
		return blockingFeedbackPayload{}, false
	}
	var parsed blockingFeedbackPayload
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return blockingFeedbackPayload{}, false
	}
	return parsed, true
}

func blockingFeedbackRequestID(event BotEventData) string {
	parsed, ok := parseBlockingFeedbackPayload(event)
	if !ok {
		return ""
	}
	return strings.TrimSpace(parsed.RequestID)
}

// getDelegationName extracts the sub-agent name from a delegation event (must hold lock or call before lock).
func (f *BotEventFilter) getDelegationName(event BotEventData) string {
	if event.Data == nil || event.Data.Data == nil {
		return ""
	}
	raw, err := json.Marshal(event.Data.Data)
	if err != nil {
		return ""
	}
	var parsed struct {
		DelegationID string `json:"delegation_id"`
		Instruction  string `json:"instruction"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ""
	}
	name := strings.SplitN(parsed.Instruction, "\n", 2)[0]
	if len(name) > 50 {
		name = name[:50] + "…"
	}
	return name
}

func (f *BotEventFilter) sendErrorMessage(ctx context.Context, message string) bool {
	f.mu.Lock()
	if f.lastErrorText == message {
		f.mu.Unlock()
		return false
	}
	f.lastErrorText = message
	f.mu.Unlock()
	f.sendMessage(ctx, message)
	return true
}

// formatErrorEvent renders an error event for display in the bot thread.
// Pulls the actual error message from AgentErrorEvent / ConversationErrorEvent /
// OrchestratorAgentErrorEvent so users see what failed instead of a generic line.
// Falls back to the generic message if the typed payload is unavailable.
func (f *BotEventFilter) formatErrorEvent(event BotEventData) string {
	const fallback = "An error occurred during processing."
	if event.Data == nil || event.Data.Data == nil {
		return fallback
	}
	var errMsg, agent string
	switch d := event.Data.Data.(type) {
	case *events.AgentErrorEvent:
		errMsg = d.Error
	case *events.ConversationErrorEvent:
		errMsg = d.Error
	case *orchestrator_events.OrchestratorAgentErrorEvent:
		errMsg = d.Error
		agent = d.AgentName
	}
	errMsg = strings.TrimSpace(errMsg)
	if errMsg == "" {
		return fallback
	}
	const maxLen = 1500
	if len(errMsg) > maxLen {
		errMsg = errMsg[:maxLen] + "…"
	}
	if agent != "" {
		return fmt.Sprintf("Error in `%s`: %s", agent, errMsg)
	}
	return "Error: " + errMsg
}

// describeToolCall translates a tool_call_start event into a user-friendly activity description.
func (f *BotEventFilter) describeToolCall(event BotEventData) string {
	if event.Data == nil || event.Data.Data == nil {
		return ""
	}
	raw, err := json.Marshal(event.Data.Data)
	if err != nil {
		return ""
	}
	var parsed struct {
		ToolName string `json:"tool_name"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed.ToolName == "" {
		return ""
	}

	// Strip MCP routing prefixes so heartbeat shows the underlying tool's
	// friendly name instead of leaking the bridge wiring. Supported shapes:
	//   mcp_<server>_<tool>     (single underscore — e.g. mcp_api-bridge_execute_shell_command)
	//   mcp__<server>__<tool>   (double underscore — used by some clients)
	name := parsed.ToolName
	switch {
	case strings.HasPrefix(name, "mcp__"):
		if idx := strings.Index(name[5:], "__"); idx > 0 {
			name = name[5+idx+2:]
		}
	case strings.HasPrefix(name, "mcp_"):
		if idx := strings.Index(name[4:], "_"); idx > 0 {
			name = name[4+idx+1:]
		}
	}

	// Map tool names to user-friendly descriptions
	switch {
	case name == "execute_shell_command":
		return "Working on the request"
	case name == "delegate" || name == "call_sub_agent" || name == "call_scripted_sub_agent" || name == "call_generic_agent":
		return "Delegating to a sub-agent"
	case name == "agent_browser" || strings.HasPrefix(name, "browser_") || name == "web_search":
		return "Browsing the web"
	case strings.HasPrefix(name, "read_") || name == "resources_list":
		return "Reading files"
	case strings.HasPrefix(name, "write_") || strings.HasPrefix(name, "create_"):
		return "Writing files"
	case strings.HasPrefix(name, "edit_") || strings.HasPrefix(name, "diff_patch_"):
		return "Editing files"
	case strings.HasPrefix(name, "search_") || strings.HasPrefix(name, "find_"):
		return "Searching"
	case strings.HasPrefix(name, "git_"):
		return "Working with git"
	case strings.HasPrefix(name, "image_") || strings.HasPrefix(name, "generate_") || strings.HasPrefix(name, "video_") || name == "read_image":
		return "Working with media"
	case name == "execute_step" || name == "create_plan" ||
		strings.HasPrefix(name, "add_") || strings.HasPrefix(name, "update_") || strings.HasPrefix(name, "delete_") ||
		name == "debug_step" || name == "query_step" ||
		name == "cleanup_orphan_step_configs":
		return "Updating the workflow"
	case name == "human_feedback":
		return "Asking the user"
	case name == "generate_text_llm":
		return "Thinking"
	default:
		// Generic fallback — never leak a raw tool name into the chat thread.
		return "Working on the next step"
	}
}

// buildHeartbeatMessage constructs a context-aware heartbeat message.
//
// Goal: a single short status line in the user's chat thread, not a metrics
// dashboard. We surface what's happening (current activity) and whether
// sub-tasks are in flight, but skip the raw tool-call count — it reads as
// noise once it's high ("16 steps completed" doesn't tell the user anything
// actionable in a chat context).
func (f *BotEventFilter) buildHeartbeatMessage(activity string, toolCount int, pendingDelegations int, beatIndex int) string {
	var parts []string

	if pendingDelegations > 0 {
		noun := "sub-task"
		if pendingDelegations > 1 {
			noun = "sub-tasks"
		}
		parts = append(parts, fmt.Sprintf("_%d %s in progress_", pendingDelegations, noun))
	}

	if activity != "" {
		parts = append(parts, fmt.Sprintf("_%s…_", activity))
	}

	if len(parts) == 0 {
		return "_Still working on it…_"
	}

	return strings.Join(parts, "  •  ")
}

// formatGenerationEnd formats an llm_generation_end event to show LLM response text.
// Only shows for main agent (level 0) — sub-agent results come via unified_completion.
// Skips pure tool-call turns (no text content).
func (f *BotEventFilter) formatGenerationEnd(event BotEventData) string {
	// Skip sub-agent generations — their results are shown via unified_completion
	if event.Data == nil || event.Data.Data == nil || !f.isMainLevel(event) {
		return ""
	}

	gen, ok := event.Data.Data.(*events.LLMGenerationEndEvent)
	if !ok {
		return ""
	}

	content := strings.TrimSpace(cleanBotStreamingContent(gen.Content, false))
	if content == "" {
		return "" // pure tool-call turn, no text to show
	}

	return content
}

var botStreamUpdateInterval = 1200 * time.Millisecond

func (f *BotEventFilter) sendStreamingChunk(ctx context.Context, event BotEventData) bool {
	if caps := f.capabilities(); !caps.StreamingReplies || !caps.MessageEdits {
		return false
	}
	if event.Data == nil || event.Data.Data == nil || !f.isMainLevel(event) {
		return false
	}
	if f.suppressWorkflowRuntimeChatter(event) {
		log.Printf("[BOT_FILTER] streaming_chunk: suppressed workflow runtime chunk for %s", f.threadID.Platform)
		return false
	}

	chunk, ok := event.Data.Data.(*events.StreamingChunkEvent)
	if !ok {
		return false
	}
	if chunk.IsToolCall || strings.EqualFold(strings.TrimSpace(chunk.Source), events.StreamingChunkSourceTerminal) {
		return false
	}

	content := cleanBotStreamingContent(chunk.Content, chunk.IsDelta)
	if content == "" {
		return false
	}

	f.mu.Lock()
	if !chunk.IsDelta && f.streamingReplyPrefix != "" {
		content = strings.TrimPrefix(content, f.streamingReplyPrefix)
	}
	nextText := appendBotStreamingText(f.streamingText, content, chunk.IsDelta)
	if strings.TrimSpace(nextText) == "" || sameBotMainText(nextText, f.streamingText) {
		f.mu.Unlock()
		return false
	}
	messageID := f.streamingMessageID
	f.streamingText = nextText
	if messageID != "" && time.Since(f.lastStreamingUpdate) < botStreamUpdateInterval {
		f.mu.Unlock()
		return false
	}
	f.mu.Unlock()

	if messageID == "" {
		f.clearProgressMessage("Request finished.")
		sentID, err := f.connector.SendThreadMessage(ctx, f.threadID, nextText)
		if err != nil {
			log.Printf("[BOT_FILTER] Failed to send streaming message: %v", err)
			return false
		}
		f.mu.Lock()
		if f.streamingMessageID == "" {
			f.streamingMessageID = sentID
		}
		f.streamingSentText = nextText
		f.lastStreamingUpdate = time.Now()
		f.mainTextSent = true
		f.lastMainText = strings.TrimSpace(nextText)
		f.mu.Unlock()
		log.Printf("[BOT_FILTER] streaming_chunk: started streamed reply (%d chars)", len(nextText))
		return true
	}

	if err := f.connector.UpdateMessage(ctx, f.threadID, messageID, nextText); err != nil {
		log.Printf("[BOT_FILTER] Failed to update streaming message: %v", err)
		return false
	}
	f.mu.Lock()
	f.streamingSentText = nextText
	f.lastStreamingUpdate = time.Now()
	f.mainTextSent = true
	f.lastMainText = strings.TrimSpace(nextText)
	f.mu.Unlock()
	log.Printf("[BOT_FILTER] streaming_chunk: updated streamed reply (%d chars)", len(nextText))
	return true
}

func (f *BotEventFilter) flushStreamingMessage(ctx context.Context, finalText string) bool {
	f.mu.Lock()
	messageID := f.streamingMessageID
	if messageID == "" {
		f.mu.Unlock()
		return false
	}
	text := strings.TrimSpace(strings.TrimPrefix(finalText, f.streamingReplyPrefix))
	if text == "" {
		text = strings.TrimSpace(f.streamingText)
	}
	if text == "" {
		f.mu.Unlock()
		return false
	}
	// Only an identical message is left as is. sameBotMainText also matches
	// when the streamed text merely contains the answer, which kept the turn's
	// running narration ("I'll check…", "Switching to us-east-2…") in front
	// of the final answer in Slack, while the web chat showed the answer
	// alone (RTS 2026-09-25, QA bot).
	if normalizeBotMainText(text) == normalizeBotMainText(f.streamingSentText) {
		f.mainTextSent = true
		f.lastMainText = strings.TrimSpace(f.streamingSentText)
		f.mu.Unlock()
		return true
	}
	f.streamingText = text
	f.mu.Unlock()

	if err := f.connector.UpdateMessage(ctx, f.threadID, messageID, text); err != nil {
		log.Printf("[BOT_FILTER] Failed to flush streaming message: %v", err)
		return false
	}
	f.mu.Lock()
	f.streamingSentText = text
	f.lastStreamingUpdate = time.Now()
	f.mainTextSent = true
	f.lastMainText = strings.TrimSpace(text)
	f.mu.Unlock()
	log.Printf("[BOT_FILTER] streaming_chunk: flushed streamed reply (%d chars)", len(text))
	return true
}

func cleanBotStreamingContent(content string, preserveLeading bool) string {
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if isBotStreamingStatusLine(line) {
			continue
		}
		kept = append(kept, line)
	}
	cleaned := strings.Join(kept, "\n")
	if preserveLeading {
		return cleaned
	}
	return strings.TrimLeft(cleaned, "\n\r\t ")
}

func isBotStreamingStatusLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "running shell command") ||
		strings.Contains(lower, "running shell commands") ||
		strings.Contains(lower, "claude code is working") ||
		strings.Contains(lower, "calling api-bridge") ||
		strings.Contains(lower, "called api-bridge") ||
		strings.Contains(lower, "using api-bridge") {
		return true
	}
	if strings.Contains(trimmed, "(MCP)") || strings.Contains(trimmed, "(tool)") {
		return true
	}
	return false
}

func appendBotStreamingText(currentText, incoming string, isDelta bool) string {
	if incoming == "" {
		return currentText
	}
	if currentText == "" {
		return incoming
	}
	if isDelta {
		return currentText + botDeltaSeparator(currentText, incoming) + incoming
	}
	// Some providers publish cumulative snapshots instead of separate blocks.
	if strings.HasPrefix(incoming, currentText) {
		return incoming
	}
	if strings.HasSuffix(currentText, incoming) {
		return currentText
	}
	return currentText + botBlockSeparator(currentText, incoming) + incoming
}

func botDeltaSeparator(currentText, incoming string) string {
	prev := lastRune(currentText)
	next := firstRune(incoming)
	if (prev == '.' || prev == '!' || prev == '?') && next >= 'A' && next <= 'Z' {
		return " "
	}
	return ""
}

func botBlockSeparator(currentText, incoming string) string {
	if strings.HasSuffix(currentText, "\n\n") || strings.HasPrefix(incoming, "\n\n") {
		return ""
	}
	if strings.HasSuffix(currentText, "\n") {
		return "\n"
	}
	return "\n\n"
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

func lastRune(s string) rune {
	var last rune
	for _, r := range s {
		last = r
	}
	return last
}

// SendProgressiveText forwards one completed assistant reply, read straight
// from durable conversation history while a turn is still running (see
// BotConversationManager's progressive-text poller), as its own message —
// not an edit, not a fragment of live token streaming, a genuinely finished
// turn. content-deduped against whatever was sent last (ShouldSendSyntheticFinal)
// so a slow poll tick and the turn's own final llm_generation_end/
// unified_completion never both send the same reply.
func (f *BotEventFilter) SendProgressiveText(ctx context.Context, content string) bool {
	if strings.TrimSpace(content) == "" || !f.ShouldSendSyntheticFinal(content) {
		return false
	}
	return f.sendMainText(ctx, content)
}

// ClearProgress removes the "Working on…" placeholder before a reply lands.
func (f *BotEventFilter) ClearProgress() {
	f.clearProgressMessage("Request finished.")
}

func (f *BotEventFilter) sendMainText(ctx context.Context, content string) bool {
	f.clearProgressMessage("Request finished.")
	// Mark before the connector call starts. WhatsApp sends can take long enough
	// that the synthetic-final fallback may run while this goroutine is still
	// inside SendThreadMessage; marking afterward lets the fallback send the
	// exact same assistant text a second time.
	f.MarkMainTextSent(content)
	return f.sendMessage(ctx, content)
}

func (f *BotEventFilter) sendMessage(ctx context.Context, content string) bool {
	content = f.replaceWorkspacePaths(content)
	_, err := f.connector.SendThreadMessage(ctx, f.threadID, content)
	if err != nil {
		log.Printf("[BOT_FILTER] Failed to send message: %v", err)
		return false
	}
	return true
}

// workspacePathPattern is the core pattern for workspace file paths.
// Matches Chats/xxx/yyy.ext or Downloads/xxx/yyy.ext
// (at least one subfolder + file with extension).
const workspacePathPattern = `(?:Chats|Downloads)/[\w][\w. -]*/[\w][\w./ -]*\.\w+`

// mdLinkWithWorkspacePath matches markdown links whose URL is a workspace path: [text](Chats/xxx/file.md)
var mdLinkWithWorkspacePath = regexp.MustCompile(`\[([^\]]+)\]\((` + workspacePathPattern + `)\)`)

// bareWorkspacePath matches workspace file paths not inside markdown link syntax.
// Uses a negative lookbehind for '(' to avoid matching paths already wrapped in markdown links.
var bareWorkspacePath = regexp.MustCompile(`(?:^|[^(])(` + workspacePathPattern + `)`)

// replaceWorkspacePaths converts workspace file paths in message text to shareable URLs.
// Paths inside markdown links get their URL replaced; bare paths become new markdown links.
func (f *BotEventFilter) replaceWorkspacePaths(text string) string {
	if f.appBaseURL == "" {
		return text
	}

	// Pass 1: Replace workspace paths already inside markdown links [text](Chats/xxx/file.md)
	text = mdLinkWithWorkspacePath.ReplaceAllStringFunc(text, func(match string) string {
		parts := mdLinkWithWorkspacePath.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		return fmt.Sprintf("[%s](%s)", parts[1], f.buildShareableURL(parts[2]))
	})

	// Pass 2: Wrap bare workspace paths as markdown links
	// After pass 1, all markdown-linked paths are already converted to full URLs,
	// so the bare path regex won't double-match them.
	text = bareWorkspacePath.ReplaceAllStringFunc(text, func(match string) string {
		parts := bareWorkspacePath.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		filePath := parts[1]
		prefix := strings.TrimSuffix(match, filePath)
		return prefix + fmt.Sprintf("[%s](%s)", filePath, f.buildShareableURL(filePath))
	})

	return text
}

// buildShareableURL constructs a shareable URL for a workspace file path.
func (f *BotEventFilter) buildShareableURL(filePath string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(filePath))
	url := fmt.Sprintf("%s/file?path=%s", f.appBaseURL, encoded)
	if f.userID != "" {
		url += "&uid=" + f.userID
	}
	return url
}

func (f *BotEventFilter) sendProgressMessage(ctx context.Context, text string) {
	f.progressMu.Lock()
	defer f.progressMu.Unlock()
	f.mu.Lock()
	inactive := f.sessionDone || f.awaitingInput
	f.mu.Unlock()
	caps := f.capabilities()
	if inactive || !caps.ProgressUpdates || (!caps.MessageEdits && !caps.MessageDeletion) || f.connector == nil {
		return
	}
	if f.progressMessageID != "" {
		if !caps.MessageEdits {
			return
		}
		if err := f.connector.UpdateMessage(ctx, f.threadID, f.progressMessageID, text); err != nil {
			log.Printf("[BOT_FILTER] Failed to update progress message: %v", err)
		}
		return
	}
	id, err := f.connector.SendThreadMessage(ctx, f.threadID, text)
	if err != nil {
		log.Printf("[BOT_FILTER] Failed to send progress message: %v", err)
		return
	}
	f.progressMessageID = id
}

func (f *BotEventFilter) clearProgressMessage(fallback string) {
	f.progressMu.Lock()
	defer f.progressMu.Unlock()
	id := f.progressMessageID
	if id == "" || f.connector == nil {
		return
	}
	f.progressMessageID = ""
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if deleter, ok := f.connector.(BotMessageDeleter); ok && f.capabilities().MessageDeletion {
		if err := deleter.DeleteMessage(ctx, f.threadID, id); err == nil {
			return
		} else {
			log.Printf("[BOT_FILTER] Failed to remove progress message: %v", err)
		}
	}
	if !f.capabilities().MessageEdits {
		return
	}
	if err := f.connector.UpdateMessage(ctx, f.threadID, id, fallback); err != nil {
		log.Printf("[BOT_FILTER] Failed to finalize progress message: %v", err)
	}
}
