package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

// Context options of one chat tab (PLAT-872-71), shown in the composer's model menu and offered to the chat's
// own models/sessions tools and MCP: how full the context is, the account's plan windows, "1M context",
// "Auto-compact when idle", "Compact now" and "Fresh chat". The server decides every value and what is
// offered; the browser only shows it. Only the tab's owner reads or changes them.

// chatContextTab is a chat tab as its owner's conversation registry records it.
type chatContextTab struct {
	ProfileID string
	Record    ProductConversationRecord
	Identity  ProductAgentIdentity
}

// chatContextTabForSession finds the caller's own tab for a chat session. Another owner's tab is not in the
// caller's registry, so it is never found.
func chatContextTabForSession(ctx context.Context, owner, sessionID string) (chatContextTab, bool) {
	owner, sessionID = strings.TrimSpace(owner), strings.TrimSpace(sessionID)
	if owner == "" || sessionID == "" {
		return chatContextTab{}, false
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(owner))
	if err != nil {
		return chatContextTab{}, false
	}
	for key, record := range doc.Entries {
		if record.SessionID == sessionID {
			return chatContextTab{ProfileID: record.ProfileID, Record: record, Identity: doc.Agents[key]}, true
		}
	}
	return chatContextTab{}, false
}

// idleCompactTabEnabled is false when the chat's tab owner turned "Auto-compact when idle" off.
func idleCompactTabEnabled(owner, sessionID string) bool {
	tab, found := chatContextTabForSession(context.Background(), owner, sessionID)
	return !found || !tab.Identity.IdleCompactionOff
}

type chatUsageLimit struct {
	Label       string  `json:"label"` // "5h", "7d"
	UsedPercent float64 `json:"used_percent"`
	ResetsAt    string  `json:"resets_at,omitempty"` // RFC3339; absent when the provider did not say
}

type chatContextSwitch struct {
	On bool `json:"on"`
}

type chatContextView struct {
	SessionID string `json:"session_id"`
	// UsedTokens and WindowTokens are the context at the chat's last model call; 0 means not reported yet.
	UsedTokens   int `json:"used_tokens,omitempty"`
	WindowTokens int `json:"window_tokens,omitempty"`
	// Limits are the plan windows the provider reported for this chat's own account; empty when unknown.
	Limits []chatUsageLimit `json:"limits,omitempty"`
	// A switch is present only when this chat's model and provider offer it.
	Context1M      *chatContextSwitch `json:"context_1m,omitempty"`
	IdleCompaction *chatContextSwitch `json:"idle_compaction,omitempty"`
	CanCompact     bool               `json:"can_compact"`
	CanFresh       bool               `json:"can_fresh"`
	Hint           string             `json:"idle_compaction_hint,omitempty"`
}

const chatContextIdleHint = "Long chats are summarised after about 50 minutes idle, so your next message costs less."

// chatContextRuntime is the provider, model and transport the chat last ran with, and whether a native
// conversation is saved to resume.
func chatContextRuntime(owner string, tab chatContextTab) (provider, model, transport string, hasHandle bool) {
	provider, model = strings.TrimSpace(tab.Record.Provider), strings.TrimSpace(tab.Record.ModelID)
	if target, found, err := resolveProductResumeTarget(owner, tab.Record); err == nil && found && target != nil && target.Runtime != nil {
		runtime := target.Runtime
		provider = firstNonEmptyTrimmed(runtime.Provider, provider)
		model = firstNonEmptyTrimmed(runtime.ModelID, model)
		transport = strings.TrimSpace(runtime.Transport)
		hasHandle = strings.TrimSpace(runtime.ExternalSessionID) != ""
		if handle := runtime.AgentSessionHandle; handle != nil && !handle.Provider.Empty() {
			transport = firstNonEmptyTrimmed(handle.Provider.Transport, transport)
			hasHandle = hasHandle || strings.TrimSpace(handle.Provider.NativeSessionID) != ""
		}
	}
	return provider, model, transport, hasHandle
}

// chatContextUsage is the chat's context at its last model call and, for its owner, the plan windows of its
// account: from the native transcript reader (Codex, Pi), else from the chat's main coding terminal (the CLI's
// statusline and stream usage, Claude). A window the CLI did not report comes from the model's metadata.
func (api *StreamingAPI) chatContextUsage(sessionID, provider, model string, withLimits bool) (used, window int, limits []chatUsageLimit) {
	api.nativeCostsMu.Lock()
	a := api.nativeCosts[sessionID]
	api.nativeCostsMu.Unlock()
	if a != nil {
		a.mu.Lock()
		usage := a.usage
		a.mu.Unlock()
		used, window = int(usage.ContextUsed), int(usage.ContextWindow)
		if withLimits {
			for _, limit := range usage.Limits {
				name := limit.Name
				if limit.WindowMinutes > 0 {
					name = "window_" + strconv.FormatInt(limit.WindowMinutes, 10) + "m"
				}
				var resets time.Time
				if limit.ResetsAt > 0 {
					resets = time.Unix(limit.ResetsAt, 0)
				}
				limits = appendChatUsageLimit(limits, name, limit.UsedPercent, resets)
			}
		}
	}
	if api.terminalStore != nil && (used == 0 || (withLimits && len(limits) == 0)) {
		if meta, tmux, ok := api.terminalStore.MainAgentStatusMeta(sessionID); ok {
			merged := map[string]interface{}{}
			for key, value := range meta {
				merged[key] = value
			}
			if tmux != "" {
				for key, value := range cachedTerminalCLIUsage(tmux, time.Now()).meta {
					merged[key] = value
				}
			}
			if used == 0 {
				used, window = chatMetaInt(merged[llmtypes.ContextUsedTokensMetaKey]), chatMetaInt(merged[llmtypes.ContextWindowTokensMetaKey])
			}
			if withLimits && len(limits) == 0 {
				for _, w := range (&llmtypes.StatusLine{Metadata: merged}).RateLimitWindows() {
					limits = appendChatUsageLimit(limits, w.Name, w.UsedPercent, w.ResetsAt)
				}
			}
		}
	}
	if used > 0 && window <= 0 && strings.EqualFold(strings.TrimSpace(provider), "claude-code") {
		if meta, err := (&claudecode.ClaudeCodeAdapter{}).GetModelMetadata(model); err == nil && meta != nil {
			window = meta.ContextWindow
		}
	}
	return used, window, limits
}

func chatMetaInt(value interface{}) int {
	n, err := strconv.ParseFloat(fmt.Sprint(value), 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return 0
	}
	return int(n)
}

// appendChatUsageLimit keeps the 5-hour and 7-day windows under short labels. Other windows are left out
// rather than named by a guess.
func appendChatUsageLimit(limits []chatUsageLimit, name string, percent float64, resets time.Time) []chatUsageLimit {
	label := ""
	switch name {
	case "five_hour", "window_300m":
		label = "5h"
	case "seven_day", "window_10080m":
		label = "7d"
	}
	if label == "" || math.IsNaN(percent) {
		return limits
	}
	for _, existing := range limits {
		if existing.Label == label {
			return limits
		}
	}
	limit := chatUsageLimit{Label: label, UsedPercent: math.Round(math.Max(0, math.Min(percent, 100)))}
	if !resets.IsZero() {
		limit.ResetsAt = resets.UTC().Format(time.RFC3339)
	}
	return append(limits, limit)
}

// productChatTab reports whether a tab's turns run through the product chat path (Crew and Code tabs), where
// the per-tab model, "1M context" and "Fresh chat" apply.
func productChatTab(tab chatContextTab) bool {
	return limitsSideChats(tab.ProfileID)
}

func (api *StreamingAPI) chatContextViewFor(owner string, tab chatContextTab) chatContextView {
	sessionID := tab.Record.SessionID
	provider, model, transport, _ := chatContextRuntime(owner, tab)
	if own := tab.Identity; own.Engine != "" && own.ModelID != "" {
		model = own.ModelID // chosen for the next message, before it ran
	}
	if tab.Identity.Context1M {
		model = claudecode.With1MContext(model)
	}
	view := chatContextView{SessionID: sessionID}
	view.UsedTokens, view.WindowTokens, view.Limits = api.chatContextUsage(sessionID, provider, model, true)
	claude := strings.EqualFold(provider, "claude-code")
	if productChatTab(tab) && claude && claudecode.Supports1MContext(model) {
		view.Context1M = &chatContextSwitch{On: tab.Identity.Context1M}
	}
	if llmproviders.IdleCompactSupported(llmproviders.Provider(strings.ToLower(provider)), transport) {
		view.IdleCompaction = &chatContextSwitch{On: !tab.Identity.IdleCompactionOff}
		view.Hint = chatContextIdleHint
		view.CanCompact = true
	}
	view.CanFresh = productChatTab(tab) && isCodingAgentProvider(provider, model)
	return view
}

// chatContextBusy explains why a chat cannot be compacted or started fresh right now; "" when it can.
func (api *StreamingAPI) chatContextBusy(owner, sessionID string) string {
	switch {
	case api.sessionTurnRunning(sessionID), api.conversationTurnHasBacklog(owner, sessionID):
		return "This chat is busy. Try again when its reply has finished."
	case idleCompacts.running(sessionID) != nil:
		return "This chat is already being compacted."
	case api.sessionAwaitsBackgroundResults(sessionID) || len(claudeBackgroundTasks(sessionID)) > 0:
		return "This chat still has background work running. Try again when it has finished."
	}
	return ""
}

// compactChatNow runs the idle compaction for one chat now, whatever its idle time and size: the same turn,
// owner, lock and chat row as the idle sweep. It answers at once; the chat shows the compaction.
func (api *StreamingAPI) compactChatNow(owner string, tab chatContextTab, afterTurn bool) (int, string) {
	sessionID := tab.Record.SessionID
	idleCompacts.mu.Lock()
	followed := idleCompacts.chats[sessionID]
	var chat idleCompactChat
	if followed != nil {
		chat = *followed
	}
	idleCompacts.mu.Unlock()
	if followed == nil || chat.lastPerson.IsZero() || chat.lastTurnEnd.IsZero() {
		return http.StatusConflict, "Send a message in this chat first; then it can be compacted."
	}
	if !llmproviders.IdleCompactSupported(llmproviders.Provider(strings.ToLower(chat.provider)), chat.transport) {
		return http.StatusConflict, "This chat's model cannot be compacted from here."
	}
	if !chat.hasHandle {
		return http.StatusConflict, "This chat has no saved conversation to compact yet."
	}
	if busy := api.chatContextBusy(owner, sessionID); busy != "" {
		if afterTurn && api.sessionTurnRunning(sessionID) && idleCompacts.running(sessionID) == nil {
			// Asked from inside a reply (the chat's own tool): compact once this reply and anything queued behind it end.
			go api.compactChatAfterTurn(owner, tab)
			return http.StatusAccepted, "This chat will be compacted when this reply finishes."
		}
		return http.StatusConflict, busy
	}
	tokens, _, _ := api.chatContextUsage(sessionID, chat.provider, "", false)
	run := newIdleCompactRun()
	run.manual = true
	// A person asked: only the chat's own lock applies, not the sweep's concurrency cap.
	if !idleCompacts.start(sessionID, run, math.MaxInt, time.Now()) {
		return http.StatusConflict, "This chat is already being compacted."
	}
	// The turn runs exactly as the sweep's: as the chat's tab owner (the caller, whose own tab this is).
	runAs := api.idleCompactOwner(sessionID, chat.personUser)
	log.Printf("[IDLE_COMPACT] session=%s provider=%s owner=%s tokens=%d action=compact reason=compact_now", sessionID, chat.provider, runAs, tokens)
	go api.runIdleCompaction(sessionID, runAs, chat, tokens, 0, run)
	return http.StatusAccepted, "Compacting this chat now."
}

// compactChatAfterTurn waits (up to 15 minutes) for the chat's turns to end, then compacts it once.
func (api *StreamingAPI) compactChatAfterTurn(owner string, tab chatContextTab) {
	sessionID := tab.Record.SessionID
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		if api.chatContextBusy(owner, sessionID) == "" {
			status, message := api.compactChatNow(owner, tab, false)
			log.Printf("[IDLE_COMPACT] session=%s owner=%s compact_now after turn status=%d %s", sessionID, owner, status, message)
			return
		}
	}
	log.Printf("[IDLE_COMPACT] session=%s owner=%s action=skip reason=compact_now_wait_exceeded", sessionID, owner)
}

// freshChat makes the tab's next message start a new native conversation. The visible chat stays; the new
// conversation receives the usual continuity notice: the last turns (shortened) and where the full chat is saved.
// The idea is the same as an account change in prepareProductConversationTurn, which already does exactly this.
func (api *StreamingAPI) freshChat(ctx context.Context, owner string, tab chatContextTab) (int, string) {
	sessionID := tab.Record.SessionID
	if !productChatTab(tab) {
		return http.StatusConflict, "Fresh chat is not available for this chat."
	}
	if err := defaultProductConversationRegistryStore().setFreshNative(ctx, owner, tab.ProfileID, tab.Record.ConversationKey, true); err != nil {
		return http.StatusUnprocessableEntity, err.Error()
	}
	if api.conversationTurnOccupied(sessionID) || idleCompacts.running(sessionID) != nil {
		// Asked from inside a reply (the chat's own tool): the next message after it starts fresh.
		log.Printf("[CHAT_CONTEXT] fresh chat requested owner=%s session=%s key=%q after_running_turn=true", owner, sessionID, tab.Record.ConversationKey)
		return http.StatusOK, "After this reply, the next message starts a fresh conversation with a short summary of this one. This chat's history stays here."
	}
	retireProductCodingCLI(sessionID, "fresh chat")
	idleCompacts.mu.Lock()
	if c := idleCompacts.chats[sessionID]; c != nil && c.running == nil {
		delete(idleCompacts.chats, sessionID) // the next person message follows the new conversation
	}
	idleCompacts.mu.Unlock()
	log.Printf("[CHAT_CONTEXT] fresh chat requested owner=%s session=%s key=%q", owner, sessionID, tab.Record.ConversationKey)
	return http.StatusOK, "Your next message starts a fresh conversation with a short summary of this one. This chat's history stays here."
}

// chatContextRequest is the body of POST /api/chat-context. Each field is optional; action runs after the
// switches are saved.
type chatContextRequest struct {
	SessionID      string `json:"session_id"`
	Context1M      *bool  `json:"context_1m,omitempty"`
	IdleCompaction *bool  `json:"idle_compaction,omitempty"`
	Action         string `json:"action,omitempty"` // "compact" or "fresh"
}

// applyChatContext changes one of the caller's own chats and answers with its view. The status is the HTTP status.
// afterTurn is set for the chat's own tool, called from inside the reply it would otherwise wait for.
func (api *StreamingAPI) applyChatContext(ctx context.Context, owner string, input chatContextRequest, afterTurn bool) (int, map[string]any) {
	tab, found := chatContextTabForSession(ctx, owner, input.SessionID)
	if !found {
		return http.StatusNotFound, map[string]any{"error": "Only this chat's owner can change its context options."}
	}
	view := api.chatContextViewFor(owner, tab)
	if input.Context1M != nil && view.Context1M == nil && *input.Context1M {
		return http.StatusUnprocessableEntity, map[string]any{"error": "1M context is available only for Claude Opus and Sonnet chats."}
	}
	if input.Context1M != nil || input.IdleCompaction != nil {
		identity, err := defaultProductConversationRegistryStore().setChatContextOptions(ctx, owner, tab.ProfileID, tab.Record.ConversationKey, input.Context1M, input.IdleCompaction, owner)
		if err != nil {
			return http.StatusUnprocessableEntity, map[string]any{"error": err.Error()}
		}
		tab.Identity = identity
	}
	status, message := http.StatusOK, ""
	switch strings.TrimSpace(input.Action) {
	case "":
		if input.Context1M != nil {
			message = "Applies from the next message."
		}
	case "compact":
		status, message = api.compactChatNow(owner, tab, afterTurn)
	case "fresh":
		status, message = api.freshChat(ctx, owner, tab)
	default:
		return http.StatusBadRequest, map[string]any{"error": "Choose compact or fresh."}
	}
	out := map[string]any{"context": api.chatContextViewFor(owner, tab)}
	if status >= http.StatusBadRequest {
		out["error"] = message
	} else if message != "" {
		out["message"] = message
	}
	return status, out
}

// handleChatContext reads (GET ?session_id=) or changes (POST) the context options of the caller's own chat.
func (api *StreamingAPI) handleChatContext(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	owner := productWorkspaceUserID(r.Context())
	if r.Method == http.MethodGet {
		tab, found := chatContextTabForSession(r.Context(), owner, r.URL.Query().Get("session_id"))
		if !found {
			writeAgentProfileError(w, http.StatusNotFound, "Only this chat's owner can see its context options.")
			return
		}
		writeAgentProfileJSON(w, http.StatusOK, map[string]any{"context": api.chatContextViewFor(owner, tab)})
		return
	}
	var input chatContextRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes)).Decode(&input); err != nil || strings.TrimSpace(input.SessionID) == "" {
		writeAgentProfileError(w, http.StatusBadRequest, "send the chat's session_id and what to change")
		return
	}
	status, out := api.applyChatContext(r.Context(), owner, input, false)
	writeAgentProfileJSON(w, status, out)
}

// chatContextArgs reads the context fields of a models/sessions tool call: booleans, or "true"/"false" text
// from callers that pass every argument as a string.
func chatContextArgs(args map[string]any) (context1M, idleCompaction *bool, err error) {
	read := func(key string) (*bool, error) {
		value, exists := args[key]
		if !exists || value == nil {
			return nil, nil
		}
		switch v := value.(type) {
		case bool:
			return &v, nil
		case string:
			if strings.TrimSpace(v) == "" {
				return nil, nil
			}
			parsed, perr := strconv.ParseBool(strings.TrimSpace(v))
			if perr != nil {
				return nil, fmt.Errorf("%s must be true or false", key)
			}
			return &parsed, nil
		}
		return nil, fmt.Errorf("%s must be true or false", key)
	}
	if context1M, err = read("context_1m"); err != nil {
		return nil, nil, err
	}
	idleCompaction, err = read("idle_compaction")
	return context1M, idleCompaction, err
}

// externalChatContext answers an MCP call that changes one of the caller's chats, with the same checks as the screen.
func (api *StreamingAPI) externalChatContext(w http.ResponseWriter, r *http.Request, sessionID string, input chatContextRequest) {
	input.SessionID = sessionID
	status, out := api.applyChatContext(r.Context(), productWorkspaceUserID(r.Context()), input, false)
	if status >= http.StatusBadRequest {
		message, _ := out["error"].(string)
		externalError(w, status, "chat_context", message)
		return
	}
	externalJSON(w, out)
}
