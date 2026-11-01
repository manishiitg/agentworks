package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	agentevents "github.com/manishiitg/mcpagent/events"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Idle compaction (PLAT-872). The server closes an idle coding CLI after 15
// minutes and the SDK transport ends with every turn, so a CLI's own idle
// compaction never fires here. The provider's prompt cache is server-side,
// keyed on content and lives about an hour: a fresh process resuming the same
// conversation within that hour still reads it from cache. So a long chat
// that a person left about 50 minutes ago is compacted now, cheaply, with the
// CLI's own trigger, instead of the person's next message paying a full cache
// write of the long history.
//
// One sweep looks at chats a person used recently. A chat is compacted once
// per break, as its tab owner, on its own account and model, through the same
// turn path as a person's message (account, sandbox and launch options are
// identical, so the cached prompt prefix matches). Every decision inside the
// idle window logs one [IDLE_COMPACT] line; no message content is logged.

// The thresholds live here; the env overrides exist for testing.
const (
	envIdleCompactMinTokens    = "AGENTWORKS_IDLE_COMPACT_MIN_TOKENS"
	envIdleCompactMinPercent   = "AGENTWORKS_IDLE_COMPACT_MIN_PERCENT"
	envIdleCompactIdleFromMin  = "AGENTWORKS_IDLE_COMPACT_IDLE_FROM_MINUTES"
	envIdleCompactIdleToMin    = "AGENTWORKS_IDLE_COMPACT_IDLE_TO_MINUTES"
	envIdleCompactSweepSeconds = "AGENTWORKS_IDLE_COMPACT_SWEEP_SECONDS"

	idleCompactActivity = "idle_compaction"
)

type idleCompactConfig struct {
	MinTokens     int           // compact at or above this context size...
	MinPercent    int           // ...or at or above this share of the model's window
	IdleFrom      time.Duration // since the chat's last turn ended
	IdleTo        time.Duration // past this the cache has likely expired
	PersonWithin  time.Duration // a person sent a turn this recently
	Sweep         time.Duration
	SendWait      time.Duration // a person's send waits this long for a running compaction
	MaxConcurrent int
}

func idleCompactConfigFromEnv() idleCompactConfig {
	cfg := idleCompactConfig{MinTokens: 200_000, MinPercent: 60, IdleFrom: 50 * time.Minute, IdleTo: 58 * time.Minute,
		PersonWithin: 3 * time.Hour, Sweep: 5 * time.Minute, SendWait: 90 * time.Second, MaxConcurrent: 2}
	positive := func(key string) (int, bool) {
		n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
		return n, err == nil && n > 0
	}
	if n, ok := positive(envIdleCompactMinTokens); ok {
		cfg.MinTokens = n
	}
	if n, ok := positive(envIdleCompactMinPercent); ok {
		cfg.MinPercent = n
	}
	if n, ok := positive(envIdleCompactIdleFromMin); ok {
		cfg.IdleFrom = time.Duration(n) * time.Minute
	}
	if n, ok := positive(envIdleCompactIdleToMin); ok {
		cfg.IdleTo = time.Duration(n) * time.Minute
	}
	if n, ok := positive(envIdleCompactSweepSeconds); ok {
		cfg.Sweep = time.Duration(n) * time.Second
	}
	return cfg
}

// idleCompactCandidate is what the sweep knows about one chat.
type idleCompactCandidate struct {
	Interactive    bool // a person's chat, not a schedule/webhook/function/agent-only session
	Supported      bool // the chat's provider transport has a native compaction trigger
	HasHandle      bool // a native session to resume
	TurnRunning    bool
	Queued         bool
	BackgroundWork bool
	Stopped        bool
	LimitReached   bool // the tab owner is at a token limit
	TabOff         bool // the tab's owner turned "Auto-compact when idle" off (PLAT-872-71)
	LastActivity   time.Time
	LastPerson     time.Time
	AttemptedAt    time.Time // last idle compaction attempt
	ContextTokens  int
	ContextWindow  int
}

// idleCompactDecide reports whether the chat is inside its idle window (only
// those decisions are logged) and whether to compact it now.
func idleCompactDecide(c idleCompactCandidate, cfg idleCompactConfig, now time.Time) (inWindow, compact bool, reason string) {
	if c.LastActivity.IsZero() {
		return false, false, "no_turn"
	}
	idle := now.Sub(c.LastActivity)
	if idle < cfg.IdleFrom || idle > cfg.IdleTo {
		return false, false, "outside_idle_window"
	}
	switch {
	case c.TabOff:
		return true, false, "disabled"
	case !c.Interactive:
		return true, false, "not_interactive"
	case c.LastPerson.IsZero() || now.Sub(c.LastPerson) > cfg.PersonWithin:
		return true, false, "no_recent_person_turn"
	case !c.AttemptedAt.IsZero() && !c.AttemptedAt.Before(c.LastPerson):
		return true, false, "already_compacted"
	case c.Stopped:
		return true, false, "stopped"
	case c.TurnRunning:
		return true, false, "turn_running"
	case c.Queued:
		return true, false, "queued_input"
	case c.BackgroundWork:
		return true, false, "background_work"
	case !c.Supported:
		return true, false, "unsupported"
	case !c.HasHandle:
		return true, false, "no_native_session"
	case c.ContextTokens < cfg.MinTokens && (c.ContextWindow <= 0 || c.ContextTokens*100 < cfg.MinPercent*c.ContextWindow):
		return true, false, "small_context"
	case c.LimitReached:
		return true, false, "limit"
	}
	return true, true, "idle_cache_warm"
}

// idleCompactRun is one compaction turn. done closes when it ends. A person's
// send stops it: before the compaction started it is skipped, after that its
// CLI call is cancelled (the adapter closes the session).
type idleCompactRun struct {
	once    sync.Once
	done    chan struct{}
	result  llmtypes.IdleCompactResult
	err     error
	mu      sync.Mutex
	started bool
	stopped bool
	cancel  context.CancelFunc
	manual  bool // "Compact now" from the tab's model menu, not the idle sweep
	// startTerminal: the same internal turn starts the chat's Claude Code terminal
	// instead of compacting (the person turned "Native Claude Code terminal" on).
	startTerminal bool
}

func newIdleCompactRun() *idleCompactRun { return &idleCompactRun{done: make(chan struct{})} }

// begin marks the compaction as started; false when a person's send already stopped it.
func (r *idleCompactRun) begin(cancel context.CancelFunc) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return false
	}
	r.started, r.cancel = true, cancel
	return true
}

func (r *idleCompactRun) isStarted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started
}

func (r *idleCompactRun) stop() {
	r.mu.Lock()
	r.stopped = true
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *idleCompactRun) finish(result llmtypes.IdleCompactResult, err error) {
	r.once.Do(func() {
		r.result, r.err = result, err
		close(r.done)
	})
}

type idleCompactRunKey struct{}

func idleCompactRunFromContext(ctx context.Context) *idleCompactRun {
	run, _ := ctx.Value(idleCompactRunKey{}).(*idleCompactRun)
	return run
}

type idleCompactChat struct {
	personReq   QueryRequest // the last person turn's request, replayed for the compaction turn
	personUser  string
	lastPerson  time.Time
	lastTurnEnd time.Time
	attemptedAt time.Time
	provider    string
	transport   string
	hasHandle   bool
	running     *idleCompactRun
}

type idleCompactTracker struct {
	mu     sync.Mutex
	chats  map[string]*idleCompactChat
	active int
}

var idleCompacts = &idleCompactTracker{chats: map[string]*idleCompactChat{}}

func (t *idleCompactTracker) chat(sessionID string) *idleCompactChat {
	c := t.chats[sessionID]
	if c == nil {
		c = &idleCompactChat{}
		t.chats[sessionID] = c
	}
	return c
}

// idleCompactPersonTurn reports whether a turn was sent by a person: not a
// background notification, schedule, webhook, function, external agent or
// another agent's message.
func idleCompactPersonTurn(r *http.Request, sessionID string, req QueryRequest) bool {
	if req.IsAutoNotification || isScheduledSessionIdentity(sessionID, req.TriggeredBy) || strings.TrimSpace(req.SessionKind) != "" {
		return false
	}
	if _, delegated := r.Context().Value(agentMessageAdmissionKey{}).(func(string) (func(), error)); delegated {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(req.TriggeredBy)) {
	case "", "interactive", "manual":
		return true
	}
	return false
}

func (t *idleCompactTracker) notePersonTurn(sessionID, userID string, req QueryRequest, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.chat(sessionID)
	c.personReq, c.personUser, c.lastPerson = req, userID, now
}

func (t *idleCompactTracker) noteTurnEnd(sessionID string, runtime *ChatHistoryAgentRuntime, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.chats[sessionID]
	if c == nil {
		return // only chats a person used are followed
	}
	c.lastTurnEnd = now
	if runtime != nil {
		c.provider = strings.TrimSpace(runtime.Provider)
		c.transport = strings.TrimSpace(runtime.Transport)
		c.hasHandle = strings.TrimSpace(runtime.ExternalSessionID) != ""
		if handle := runtime.AgentSessionHandle; handle != nil && !handle.Provider.Empty() {
			if transport := strings.TrimSpace(handle.Provider.Transport); transport != "" {
				c.transport = transport
			}
			c.hasHandle = c.hasHandle || strings.TrimSpace(handle.Provider.NativeSessionID) != ""
		}
	}
}

func (t *idleCompactTracker) running(sessionID string) *idleCompactRun {
	t.mu.Lock()
	defer t.mu.Unlock()
	if c := t.chats[sessionID]; c != nil {
		return c.running
	}
	return nil
}

// start claims a slot for a compaction of sessionID; false at the cap.
func (t *idleCompactTracker) start(sessionID string, run *idleCompactRun, limit int, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.chat(sessionID)
	if c.running != nil || t.active >= limit {
		return false
	}
	t.active++
	c.running, c.attemptedAt = run, now
	return true
}

func (t *idleCompactTracker) end(sessionID string, run *idleCompactRun) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if c := t.chats[sessionID]; c != nil && c.running == run {
		c.running = nil
		t.active--
	}
}

type idleCompactSnapshot struct {
	sessionID string
	chat      idleCompactChat
}

func (t *idleCompactTracker) snapshot(now time.Time, keep time.Duration) []idleCompactSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]idleCompactSnapshot, 0, len(t.chats))
	for id, c := range t.chats {
		if c.running == nil && now.Sub(c.lastPerson) > keep {
			delete(t.chats, id) // nobody used it for hours: stop following it
			continue
		}
		out = append(out, idleCompactSnapshot{sessionID: id, chat: *c})
	}
	return out
}

func (api *StreamingAPI) runIdleCompactionSweeper() {
	cfg := idleCompactConfigFromEnv()
	log.Printf("[IDLE_COMPACT] sweeper started every=%s idle=%s-%s min_tokens=%d min_percent=%d", cfg.Sweep, cfg.IdleFrom, cfg.IdleTo, cfg.MinTokens, cfg.MinPercent)
	ticker := time.NewTicker(cfg.Sweep)
	defer ticker.Stop()
	for now := range ticker.C {
		api.sweepIdleCompactions(cfg, now)
	}
}

func (api *StreamingAPI) sweepIdleCompactions(cfg idleCompactConfig, now time.Time) {
	for _, item := range idleCompacts.snapshot(now, cfg.PersonWithin+cfg.IdleTo) {
		sessionID, chat := item.sessionID, item.chat
		if chat.running != nil {
			continue
		}
		if chat.lastTurnEnd.IsZero() {
			continue
		}
		candidate := idleCompactCandidate{LastActivity: api.idleCompactLastActivity(sessionID, chat.lastTurnEnd), LastPerson: chat.lastPerson, AttemptedAt: chat.attemptedAt}
		if inWindow, _, _ := idleCompactDecide(candidate, cfg, now); !inWindow {
			continue
		}
		owner := api.idleCompactOwner(sessionID, chat.personUser)
		candidate = api.idleCompactCandidateFor(sessionID, owner, chat, candidate)
		_, compact, reason := idleCompactDecide(candidate, cfg, now)
		idleMin := int(now.Sub(candidate.LastActivity).Minutes())
		if !compact {
			log.Printf("[IDLE_COMPACT] session=%s provider=%s owner=%s tokens=%d idle_min=%d action=skip reason=%s", sessionID, chat.provider, owner, candidate.ContextTokens, idleMin, reason)
			continue
		}
		run := newIdleCompactRun()
		if !idleCompacts.start(sessionID, run, cfg.MaxConcurrent, now) {
			log.Printf("[IDLE_COMPACT] session=%s provider=%s owner=%s tokens=%d idle_min=%d action=skip reason=concurrency_cap", sessionID, chat.provider, owner, candidate.ContextTokens, idleMin)
			continue
		}
		log.Printf("[IDLE_COMPACT] session=%s provider=%s owner=%s tokens=%d idle_min=%d action=compact reason=%s", sessionID, chat.provider, owner, candidate.ContextTokens, idleMin, reason)
		go api.runIdleCompaction(sessionID, owner, chat, candidate.ContextTokens, idleMin, run)
	}
}

// idleCompactOwner is the chat's tab owner (a Crew tab's owner, not the last
// person who typed in it), else the chat's recorded owner.
func (api *StreamingAPI) idleCompactOwner(sessionID, personUser string) string {
	caller := personUser
	if api.eventStore != nil {
		if owner := strings.TrimSpace(api.eventStore.GetSessionOwner(sessionID)); owner != "" {
			caller = owner
		}
	}
	if owner, ok := crewTabOwnerForSession(context.Background(), caller, sessionID); ok && owner != "" {
		return owner
	}
	return caller
}

func (api *StreamingAPI) idleCompactCandidateFor(sessionID, owner string, chat idleCompactChat, c idleCompactCandidate) idleCompactCandidate {
	c.Interactive = !chat.lastPerson.IsZero() && !isScheduledSessionIdentity(sessionID, chat.personReq.TriggeredBy)
	c.Supported = llmproviders.IdleCompactSupported(llmproviders.Provider(strings.ToLower(chat.provider)), chat.transport)
	c.HasHandle = chat.hasHandle
	c.TurnRunning = api.sessionTurnRunning(sessionID)
	c.Queued = api.conversationTurnHasBacklog(owner, sessionID)
	c.BackgroundWork = api.sessionAwaitsBackgroundResults(sessionID) || len(claudeBackgroundTasks(sessionID)) > 0
	c.Stopped = api.isSessionMarkedStopped(sessionID)
	c.ContextTokens, c.ContextWindow, _ = api.chatContextUsage(sessionID, chat.provider, chat.personReq.ModelID, false)
	c.LimitReached = api.sharedAccountTokenLimitHit(context.Background(), owner, chat.provider) != nil
	c.TabOff = !idleCompactTabEnabled(owner, sessionID)
	return c
}

// idleCompactLastActivity is when the chat last did anything: its last turn's
// end or its newest event (a reply to live input typed into a running CLI
// arrives as events, without a new turn).
func (api *StreamingAPI) idleCompactLastActivity(sessionID string, lastTurnEnd time.Time) time.Time {
	api.activeSessionsMux.RLock()
	defer api.activeSessionsMux.RUnlock()
	if session := api.activeSessions[sessionID]; session != nil && session.LastActivity.After(lastTurnEnd) {
		return session.LastActivity
	}
	return lastTurnEnd
}

// runIdleCompaction replays the chat's last person request as a
// compaction-only turn through handleQuery, as the tab owner.
func (api *StreamingAPI) runIdleCompaction(sessionID, owner string, chat idleCompactChat, tokens, idleMin int, run *idleCompactRun) {
	defer idleCompacts.end(sessionID, run)
	logSkip := func(reason string) {
		log.Printf("[IDLE_COMPACT] session=%s provider=%s owner=%s tokens=%d idle_min=%d action=skip reason=%s", sessionID, chat.provider, owner, tokens, idleMin, reason)
	}
	ctx := context.WithValue(context.Background(), idleCompactRunKey{}, run)
	ctx = costActivityContext(ctx, owner, idleCompactActivity, "")
	ctx = internalBotRequestContext(ctx, owner)

	req := sanitizedQueuedConversationRequest(ctx, chat.personReq)
	req.IsAutoNotification = false
	req.DisableLiveInputDelivery = true
	req.RestartCodingCLI = false
	req.DeliveryMode = ""
	req.SessionTitle = ""
	payload, err := json.Marshal(req)
	if err != nil {
		logSkip("request_unavailable")
		return
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/query", bytes.NewReader(payload))
	if err != nil {
		logSkip("request_unavailable")
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Session-ID", sessionID)
	httpReq.Header.Set("X-User-ID", owner)
	recorder := &internalResponseCapture{header: make(http.Header)}
	startedAt := time.Now()
	api.handleQuery(recorder, httpReq)
	if recorder.status >= http.StatusBadRequest {
		logSkip("turn_refused_" + strconv.Itoa(recorder.status))
		run.finish(llmtypes.IdleCompactResult{}, nil)
		return
	}
	select {
	case <-run.done:
	case <-time.After(30 * time.Minute):
		// The turn never reached its end (a path that returned without the
		// compaction branch); stop following it.
		run.stop()
		run.finish(llmtypes.IdleCompactResult{Reason: llmtypes.IdleCompactReasonFailed}, context.DeadlineExceeded)
	}
	result, runErr := run.result, run.err
	if run.startTerminal {
		if runErr != nil {
			log.Printf("[CLAUDE_TERMINAL] session=%s owner=%s action=start result=failed reason=%q", sessionID, owner, runErr.Error())
		} else {
			log.Printf("[CLAUDE_TERMINAL] session=%s owner=%s action=start result=started", sessionID, owner)
		}
		return
	}
	action, reason := "compacted", result.Reason
	if runErr != nil {
		action, reason = "failed", runErr.Error()
	} else if !result.Compacted {
		action = "skip"
	}
	before := result.TokensBefore
	if before == 0 {
		before = tokens
	}
	trigger := "idle"
	if run.manual {
		trigger = "manual"
	}
	log.Printf("[IDLE_COMPACT] session=%s provider=%s owner=%s tokens=%d idle_min=%d trigger=%s action=%s reason=%q before=%d after=%d cache_read=%d cache_write=%d",
		sessionID, chat.provider, owner, tokens, idleMin, trigger, action, reason, before, result.TokensAfter, result.CacheReadTokens, result.CacheWriteTokens)
	if action == "compacted" {
		api.recordIdleCompactionInChat(sessionID, chat.provider, trigger, before, result.TokensAfter, startedAt)
	}
}

// recordIdleCompactionInChat adds the chat row for a finished compaction: a context_compaction end with trigger
// idle ("tidied while you were away") or manual ("Compact now"), the shape the transcript already renders. The
// CLI's own compaction events do not reliably reach the chat from a compaction-only call, so the server records
// the result itself. For an idle one, an end the CLI stream already recorded is kept and nothing is added. For
// "Compact now" the server's row is always added, with the stream's compaction id, so the transcript collapses
// the stream's rows into this one (one row per compaction, never "While you were away").
func (api *StreamingAPI) recordIdleCompactionInChat(sessionID, provider, trigger string, before, after int, startedAt time.Time) {
	if api.eventStore == nil {
		return
	}
	streamID, streamEnded := idleCompactionEventSince(api.eventStore.LastEventsRaw(sessionID, 200), startedAt, trigger)
	if streamEnded && (trigger == "idle" || streamID == "") {
		log.Printf("[IDLE_COMPACT] session=%s chat row already recorded by the CLI stream", sessionID)
		return
	}
	now := time.Now()
	data := &agentevents.ContextCompactionEvent{
		BaseEventData: agentevents.BaseEventData{Timestamp: now},
		Provider:      provider,
		Phase:         "end",
		CompactionID:  streamID,
		Trigger:       trigger,
		Outcome:       "success",
		TokensBefore:  before,
		TokensAfter:   after,
		StartedAt:     startedAt.UTC().Format(time.RFC3339Nano),
		EndedAt:       now.UTC().Format(time.RFC3339Nano),
		DurationMs:    now.Sub(startedAt).Milliseconds(),
	}
	wrapped := agentevents.NewAgentEvent(data)
	wrapped.SessionID = sessionID
	id := "idle-compaction-" + sessionID + "-" + strconv.FormatInt(now.UnixNano(), 10)
	err := api.eventStore.AddEventChecked(sessionID, storeevents.Event{ID: id, Type: string(agentevents.ContextCompaction), Timestamp: now, SessionID: sessionID, Data: wrapped})
	log.Printf("[IDLE_COMPACT] session=%s chat row recorded event=%s trigger=%s stream_compaction=%q err=%v", sessionID, id, trigger, streamID, err)
}

// idleCompactionEventSince looks at the chat's compaction events since the compaction started: the id of the
// newest one with this trigger (the stream tags a "Compact now" run manual, see tagManualCompactionEvent) and
// whether an end is among them.
func idleCompactionEventSince(events []storeevents.Event, since time.Time, trigger string) (compactionID string, ended bool) {
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.Timestamp.Before(since) {
			break
		}
		if event.Type != string(agentevents.ContextCompaction) || event.Data == nil {
			continue
		}
		c, ok := event.Data.Data.(*agentevents.ContextCompactionEvent)
		if !ok || c.Trigger != trigger {
			continue
		}
		if compactionID == "" {
			compactionID = c.CompactionID
		}
		ended = ended || c.Phase == "end"
	}
	return compactionID, ended
}

// tagManualCompactionEvent runs on every chat event before it is stored: while a "Compact now" runs for the chat,
// the CLI's compaction events (which the adapters tag idle) are tagged manual, so the chat never says "While you
// were away" for a compaction the person asked for.
func tagManualCompactionEvent(sessionID string, event *storeevents.Event) {
	if event == nil || event.Type != string(agentevents.ContextCompaction) || event.Data == nil {
		return
	}
	c, ok := event.Data.Data.(*agentevents.ContextCompactionEvent)
	if !ok || c.Trigger != "idle" {
		return
	}
	if run := idleCompacts.running(strings.TrimSpace(sessionID)); run != nil && run.manual {
		c.Trigger = "manual"
	}
}

// compactIdleTurn is the compaction-only end of handleQuery's turn: the agent
// was built exactly as for the person's next message; only the CLI's own
// compaction runs, and no message is sent or recorded.
func (api *StreamingAPI) compactIdleTurn(ctx context.Context, sessionID string, run *idleCompactRun, agent *mcpagent.Agent) {
	if agent == nil {
		run.finish(llmtypes.IdleCompactResult{Reason: llmtypes.IdleCompactReasonNoSession}, nil)
		return
	}
	compactCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if !run.begin(cancel) {
		run.finish(llmtypes.IdleCompactResult{Reason: "person_message"}, nil)
		return
	}
	if run.startTerminal {
		// Built exactly as for the next message (its tools registered first), so the terminal starts on the
		// same conversation with everything the chat offers; the next message goes to it.
		handle, err := mcpagent.StartAgentTransportSession(compactCtx, agent)
		if err == nil && handle != nil && strings.TrimSpace(handle.TmuxSession) != "" {
			runtime := &ChatHistoryAgentRuntime{Provider: strings.TrimSpace(handle.Provider)}
			if _, started, reason := api.materializeRestoredTmuxTerminal(compactCtx, sessionID, runtime, handle.TmuxSession); !started {
				log.Printf("[CLAUDE_TERMINAL] session=%s terminal started but not shown: %s", sessionID, reason)
			}
		}
		run.finish(llmtypes.IdleCompactResult{Reason: "terminal_started"}, err)
		return
	}
	if api.eventStore != nil {
		// The Working line says the chat is being tidied, not "running".
		api.eventStore.BeginIdleCompaction(sessionID, run.manual)
		defer api.eventStore.EndIdleCompaction(sessionID)
	}
	result, err := mcpagent.CompactAgentSessionIdle(compactCtx, agent)
	if err != nil && compactCtx.Err() != nil {
		log.Printf("[IDLE_COMPACT] session=%s compaction stopped before it finished: %v", sessionID, err)
	}
	run.finish(result, err)
}

// waitForIdleCompaction holds a person's send while the chat is being
// compacted, so the message runs on the compacted session. Past the bound
// the compaction is treated as failed and closed; the send then runs on the
// uncompacted session.
func (api *StreamingAPI) waitForIdleCompaction(ctx context.Context, sessionID, source string) {
	run := idleCompacts.running(sessionID)
	if run == nil {
		return
	}
	wait := idleCompactConfigFromEnv().SendWait
	started := time.Now()
	if !run.isStarted() {
		// The person came back before the compaction started: skip it.
		run.stop()
		log.Printf("[IDLE_COMPACT] session=%s action=skip reason=person_message source=%s", sessionID, source)
		select {
		case <-run.done:
		case <-ctx.Done():
		case <-time.After(wait):
		}
		return
	}
	log.Printf("[IDLE_COMPACT] session=%s send waits for the running compaction source=%s", sessionID, source)
	select {
	case <-run.done:
		log.Printf("[IDLE_COMPACT] session=%s send continues after compaction waited_ms=%d", sessionID, time.Since(started).Milliseconds())
		return
	case <-ctx.Done():
		return
	case <-time.After(wait):
	}
	log.Printf("[IDLE_COMPACT] session=%s action=failed reason=send_wait_exceeded waited=%s; closing the compaction and sending on the uncompacted session", sessionID, wait)
	run.stop()
	select {
	case <-run.done:
	case <-time.After(15 * time.Second):
	}
}
