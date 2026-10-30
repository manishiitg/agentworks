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
	"sync"
	"time"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/nativeusage"
)

// Context options of one chat tab (PLAT-872-71), shown in the composer's model menu and offered to the chat's
// own models/sessions tools and MCP: how full the context is, the account's plan windows,
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
	// Limits are the plan windows of this chat's own account: as its session reported them, else as its
	// provider answered a direct quota request for that account (chat_usage_probe.go); empty when unknown.
	Limits []chatUsageLimit `json:"limits,omitempty"`
	// A switch is present only when this chat's model and provider offer it.
	IdleCompaction *chatContextSwitch `json:"idle_compaction,omitempty"`
	CanCompact     bool               `json:"can_compact"`
	CanFresh       bool               `json:"can_fresh"`
	Hint           string             `json:"idle_compaction_hint,omitempty"`
}

// idleCompactHint says when "Auto-compact when idle" acts, from the idle sweep's own setting.
func idleCompactHint() string {
	return fmt.Sprintf("Long chats are summarised after about %d minutes idle, so the next message costs less.", int(idleCompactConfigFromEnv().IdleFrom.Minutes()))
}

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

// chatProviderKey reduces the names one provider goes by ("claude-code", "claudecode", "codex-cli") to one key,
// so usage is matched to the provider that reported it.
func chatProviderKey(provider string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(provider) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	key := b.String()
	for _, suffix := range []string{"cli", "code"} {
		key = strings.TrimSuffix(key, suffix)
	}
	return key
}

func sameChatProvider(a, b string) bool {
	return chatProviderKey(a) != "" && chatProviderKey(a) == chatProviderKey(b)
}

// chatTmuxProvider is the provider a coding CLI's tmux session belongs to ("mlp-claude-code-…"); "" when unknown.
func chatTmuxProvider(tmux string) string {
	for _, provider := range []string{"claude-code", "codex-cli", "cursor-cli", "pi-cli", "muse-cli", "agy-cli", "gemini-cli"} {
		if strings.HasPrefix(tmux, "mlp-"+provider+"-") {
			return provider
		}
	}
	return ""
}

// chatContextUsage is what a provider reported for a chat: the context at its last model call and the plan
// windows of its account. Callers that show a chat's figures use chatUsage, which applies the current-provider
// rule; this is its source (and the compaction's size check for the provider a chat last ran on). Figures come // from the native transcript reader (Codex, Pi), else from the chat's main coding terminal (the CLI's
// statusline and stream usage, Claude). A window the CLI did not report stays unknown.
// Only figures reported by this provider count: after a switch, the previous provider's numbers stay hidden
// until the new one reports its own.
func (api *StreamingAPI) chatContextUsage(sessionID, provider, model string, withLimits bool) (used, window int, limits []chatUsageLimit) {
	api.nativeCostsMu.Lock()
	a := api.nativeCosts[sessionID]
	api.nativeCostsMu.Unlock()
	if a != nil {
		a.mu.Lock()
		usage, source := a.usage, a.state.Provider
		a.mu.Unlock()
		if !sameChatProvider(source, provider) {
			usage = nativeusage.PrivateUsage{}
		}
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
	if api.terminalStore != nil && (used == 0 || window == 0 || (withLimits && len(limits) == 0)) {
		if meta, tmux, source, ok := api.terminalStore.MainAgentStatusMeta(sessionID); ok {
			merged := map[string]interface{}{}
			if sameChatProvider(source, provider) {
				for key, value := range meta {
					merged[key] = value
				}
			}
			if tmux != "" && sameChatProvider(chatTmuxProvider(tmux), provider) {
				for key, value := range cachedTerminalCLIUsage(tmux, time.Now()).meta {
					merged[key] = value
				}
			}
			if used == 0 {
				used = chatMetaInt(merged[llmtypes.ContextUsedTokensMetaKey])
			}
			if window == 0 {
				window = chatMetaInt(merged[llmtypes.ContextWindowTokensMetaKey])
			}
			if withLimits && len(limits) == 0 {
				for _, w := range (&llmtypes.StatusLine{Metadata: merged}).RateLimitWindows() {
					limits = appendChatUsageLimit(limits, w.Name, w.UsedPercent, w.ResetsAt)
				}
			}
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

// appendChatUsageLimit keeps the 5-hour, 7-day and monthly windows under short labels. Other windows are left
// out rather than named by a guess.
func appendChatUsageLimit(limits []chatUsageLimit, name string, percent float64, resets time.Time) []chatUsageLimit {
	label := ""
	switch name {
	case "five_hour", "window_300m":
		label = "5h"
	case "seven_day", "window_10080m":
		label = "7d"
	case "month":
		label = "month"
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
// the per-tab model and "Fresh chat" apply.
func productChatTab(tab chatContextTab) bool {
	return limitsSideChats(tab.ProfileID)
}

// chatEngineProvider is the provider of a chat's own engine choice on the Crew's Models page.
func (api *StreamingAPI) chatEngineProvider(owner, profileID, engine string) string {
	if api.agentProfiles != nil {
		if profile, err := api.agentProfiles.Resolve(profileID, 0, owner); err == nil {
			if option, ok := findProviderOptionByID(profile.Runtime.ProviderOptions, engine); ok && strings.TrimSpace(option.Provider) != "" {
				return strings.TrimSpace(option.Provider)
			}
		}
	}
	return strings.TrimSpace(engine) // the product's engine ids are its provider ids
}

// chatNextModel is the provider, model and account the chat's next message will use. A Crew/Code tab resolves it
// exactly as its model chip and Models page do (effectiveProfileChatModel): its own choice, else the Crew's model.
// Other chats only know their own choice. ok is false when nothing is known beyond the last run.
func (api *StreamingAPI) chatNextModel(owner string, tab chatContextTab) (AgentProfileEffectiveChatModel, bool) {
	own := tab.Identity
	if productChatTab(tab) && api.agentProfiles != nil {
		if profile, err := api.agentProfiles.Resolve(tab.ProfileID, 0, owner); err == nil {
			projectID, _, _ := strings.Cut(strings.TrimSpace(tab.Record.ConversationKey), ":")
			if binding, _, err := resolveConversationBindingForUser(context.Background(), owner, profile, projectID); err == nil {
				if next, err := effectiveProfileChatModel(profile, binding, tab.Record, own); err == nil && strings.TrimSpace(next.Provider) != "" {
					return next, true
				}
			}
		}
	}
	if own.Engine != "" && own.ModelID != "" {
		return AgentProfileEffectiveChatModel{Provider: api.chatEngineProvider(owner, tab.ProfileID, own.Engine),
			AgentProfileChatModel: AgentProfileChatModel{Engine: own.Engine, ModelID: own.ModelID, ConnectionID: own.ConnectionID}}, true
	}
	return AgentProfileEffectiveChatModel{}, false
}

// chatUsageTarget is the provider and model whose usage the chat shows: the ones its next message will use.
// switched is true when that provider, or its account, is not the one the chat last ran on; nothing has been
// reported for it yet, so no figures are shown rather than the previous provider's or account's.
func chatUsageTarget(lastProvider, lastModel, lastConnection string, next AgentProfileEffectiveChatModel, known bool) (provider, model string, switched bool) {
	if !known {
		return lastProvider, lastModel, false
	}
	provider, model = lastProvider, firstNonEmptyTrimmed(next.ModelID, lastModel)
	if !sameChatProvider(next.Provider, lastProvider) {
		return strings.TrimSpace(next.Provider), model, true
	}
	connection, last := strings.TrimSpace(next.ConnectionID), strings.TrimSpace(lastConnection)
	return provider, model, connection != "" && last != "" && connection != last
}

// chatUsageConnection is the account the chat's next message uses: its own choice, else the one it last ran on.
// After a switch to another provider with no account named, it is unknown ("").
func chatUsageConnection(lastConnection string, next AgentProfileEffectiveChatModel, known, switched bool) string {
	if known {
		if connection := strings.TrimSpace(next.ConnectionID); connection != "" {
			return connection
		}
		if switched {
			return ""
		}
	}
	return strings.TrimSpace(lastConnection)
}

// chatUsageSubject is whose figures a chat shows: the provider, model and account its next message uses.
type chatUsageSubject struct {
	owner, provider, model, connection string
	// switched: that provider or account has not reported for this chat yet, so its own figures are unknown.
	switched bool
}

// chatUsageSubjectForTab resolves the subject of an owner's tab, and the transport the chat last ran with.
func (api *StreamingAPI) chatUsageSubjectForTab(owner string, tab chatContextTab) (chatUsageSubject, string) {
	provider, model, transport, _ := chatContextRuntime(owner, tab)
	next, known := api.chatNextModel(owner, tab)
	provider, model, switched := chatUsageTarget(provider, model, tab.Record.ConnectionID, next, known)
	return chatUsageSubject{owner: owner, provider: provider, model: model, switched: switched,
		connection: chatUsageConnection(tab.Record.ConnectionID, next, known, switched)}, transport
}

const chatUsageSubjectTTL = 15 * time.Second

var chatUsageSubjects = struct {
	sync.Mutex
	bySession map[string]cachedChatUsageSubject
}{bySession: map[string]cachedChatUsageSubject{}}

type cachedChatUsageSubject struct {
	subject chatUsageSubject
	expires time.Time
}

// chatUsageSubjectForSession is the subject for callers that know only the session (terminal status, polled
// often, so it is kept for a few seconds). A chat with no tab in its owner's registry (a workflow chat) shows
// the provider that reported last.
func (api *StreamingAPI) chatUsageSubjectForSession(sessionID string) chatUsageSubject {
	chatUsageSubjects.Lock()
	cached, found := chatUsageSubjects.bySession[sessionID]
	chatUsageSubjects.Unlock()
	if found && time.Now().Before(cached.expires) {
		return cached.subject
	}
	owner := GetDefaultUserID()
	if IsMultiUserMode() {
		owner = api.idleCompactOwner(sessionID, "") // the chat's tab owner, as for its compaction
	}
	subject := chatUsageSubject{owner: owner, provider: api.chatReportedProvider(sessionID)}
	if tab, found := chatContextTabForSession(context.Background(), owner, sessionID); found {
		subject, _ = api.chatUsageSubjectForTab(owner, tab)
	}
	chatUsageSubjects.Lock()
	chatUsageSubjects.bySession[sessionID] = cachedChatUsageSubject{subject: subject, expires: time.Now().Add(chatUsageSubjectTTL)}
	chatUsageSubjects.Unlock()
	return subject
}

// chatReportedProvider is the provider that last reported usage for a chat; "" when none did.
func (api *StreamingAPI) chatReportedProvider(sessionID string) string {
	api.nativeCostsMu.Lock()
	a := api.nativeCosts[sessionID]
	api.nativeCostsMu.Unlock()
	if a != nil {
		a.mu.Lock()
		provider := a.state.Provider
		a.mu.Unlock()
		if strings.TrimSpace(provider) != "" {
			return provider
		}
	}
	if api.terminalStore != nil {
		if _, tmux, source, ok := api.terminalStore.MainAgentStatusMeta(sessionID); ok {
			return firstNonEmptyTrimmed(source, chatTmuxProvider(tmux))
		}
	}
	return ""
}

// chatUsage is the one reader of a chat's context and plan use, for every place that shows them (the chat's
// usage chip and model menu, its terminal status). Only the subject's provider and account count: after a
// switch nothing is shown until the new one reports. Plan limits are read only withLimits, which only the
// chat's owner gets (/api/chat-context); when the chat has reported none, its provider is asked directly.
func (api *StreamingAPI) chatUsage(sessionID string, subject chatUsageSubject, withLimits bool) (used, window int, limits []chatUsageLimit) {
	if !subject.switched {
		used, window, limits = api.chatContextUsage(sessionID, subject.provider, subject.model, withLimits)
	}
	if withLimits && len(limits) == 0 {
		limits = api.probedChatLimits(subject.owner, subject.provider, subject.connection)
	}
	return used, window, limits
}

// chatCompactRefusal is why a chat cannot be compacted from here ("" when it can), apart from it being busy
// right now: the one check behind both offering "Compact now" and doing it.
func chatCompactRefusal(sessionID string) (idleCompactChat, int, string) {
	idleCompacts.mu.Lock()
	followed := idleCompacts.chats[sessionID]
	var chat idleCompactChat
	if followed != nil {
		chat = *followed
	}
	idleCompacts.mu.Unlock()
	switch {
	case followed == nil || chat.lastPerson.IsZero() || chat.lastTurnEnd.IsZero():
		return chat, http.StatusConflict, "Send a message in this chat first; then it can be compacted."
	case !llmproviders.IdleCompactSupported(llmproviders.Provider(strings.ToLower(chat.provider)), chat.transport):
		return chat, http.StatusConflict, "This chat's model cannot be compacted from here."
	case !chat.hasHandle:
		return chat, http.StatusConflict, "This chat has no saved conversation to compact yet."
	}
	return chat, 0, ""
}

func (api *StreamingAPI) chatContextViewFor(owner string, tab chatContextTab) chatContextView {
	sessionID := tab.Record.SessionID
	subject, transport := api.chatUsageSubjectForTab(owner, tab)
	view := chatContextView{SessionID: sessionID}
	view.UsedTokens, view.WindowTokens, view.Limits = api.chatUsage(sessionID, subject, true)
	if llmproviders.IdleCompactSupported(llmproviders.Provider(strings.ToLower(subject.provider)), transport) {
		view.IdleCompaction = &chatContextSwitch{On: !tab.Identity.IdleCompactionOff}
		view.Hint = idleCompactHint()
	}
	_, _, refusal := chatCompactRefusal(sessionID)
	view.CanCompact = refusal == ""
	view.CanFresh = productChatTab(tab) && isCodingAgentProvider(subject.provider, subject.model)
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
	chat, status, refusal := chatCompactRefusal(sessionID)
	if refusal != "" && chat.lastPerson.IsZero() {
		return status, refusal
	}
	// The compaction turn runs, and is charged, as the chat's tab owner: only that person may ask for it.
	runAs := api.idleCompactOwner(sessionID, chat.personUser)
	if runAs != strings.TrimSpace(owner) {
		log.Printf("[IDLE_COMPACT] session=%s owner=%s caller=%s action=refuse reason=compact_now_not_owner", sessionID, runAs, owner)
		return http.StatusForbidden, "Only this chat's owner can compact it."
	}
	if refusal != "" {
		return status, refusal
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
	// The turn runs exactly as the sweep's: as the chat's tab owner, the caller.
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
	if input.IdleCompaction != nil {
		identity, err := defaultProductConversationRegistryStore().setChatContextOptions(ctx, owner, tab.ProfileID, tab.Record.ConversationKey, *input.IdleCompaction, owner)
		if err != nil {
			return http.StatusUnprocessableEntity, map[string]any{"error": err.Error()}
		}
		tab.Identity = identity
	}
	status, message := http.StatusOK, ""
	switch strings.TrimSpace(input.Action) {
	case "":
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

// chatContextArgs reads the idle_compaction field of a models/sessions tool call: a boolean, or "true"/"false"
// text from callers that pass every argument as a string.
func chatContextArgs(args map[string]any) (idleCompaction *bool, err error) {
	value, exists := args["idle_compaction"]
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
			return nil, fmt.Errorf("idle_compaction must be true or false")
		}
		return &parsed, nil
	}
	return nil, fmt.Errorf("idle_compaction must be true or false")
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
