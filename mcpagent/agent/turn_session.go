package mcpagent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/agent/retainedturn"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

type turnPolicyContextKey struct{}
type turnStreamingCallbackContextKey struct{}

// ToolPolicy is the authorization view for one turn. An empty AllowedTools
// slice means every tool in the immutable definition is allowed.
type ToolPolicy struct {
	AllowedTools []string
}

// Turn contains the user input, optional prior history, and runtime policy for
// one model turn. Runtime policy is deliberately not part of AgentDefinition.
type Turn struct {
	// ID is the stable provider-neutral identity for this message. Session.Run
	// creates one when omitted.
	ID                string
	Input             string
	History           []llmtypes.MessageContent
	ToolPolicy        ToolPolicy
	StreamingCallback func(llmtypes.StreamChunk)
}

// Usage is the structured accounting returned with every completed turn.
type Usage struct {
	PromptTokens         int
	CompletionTokens     int
	TotalTokens          int
	CacheTokens          int
	CacheReadTokens      int
	CacheWriteTokens     int
	ReasoningTokens      int
	LLMCalls             int
	CacheEnabledLLMCalls int
	InputCostUSD         float64
	OutputCostUSD        float64
	ReasoningCostUSD     float64
	CacheCostUSD         float64
	TotalCostUSD         float64
	ContextUsagePercent  float64
}

// Result contains the completed output and continuation state for a turn.
type Result struct {
	TurnID  string
	Text    string
	History []llmtypes.MessageContent
	Handle  *AgentSessionHandle
	Usage   Usage
}

// DeliveryResult reports whether input was delivered into an active turn or
// queued for the next provider boundary.
type DeliveryResult struct {
	TurnID    string
	Queued    bool
	Status    UserMessageDeliveryStatus
	Provider  llm.Provider
	Transport llm.CodingAgentTransport
}

// ToolDefinitionView is the read-only diagnostic form of one registered tool.
type ToolDefinitionView struct {
	Name         string
	Description  string
	Source       string
	DisplayGroup string
}

// AgentDefinitionView exposes identity without mutable schemas or executors.
type AgentDefinitionView struct {
	Instructions     string
	Skills           []string
	SkillDefinitions []*llmtypes.Skill
	Tools            []ToolDefinitionView
}

// Session owns history, continuation, steering, and event access. Run calls on
// one session are serialized; callers use separate sessions for concurrency.
type Session struct {
	agent   *Agent
	runMu   sync.Mutex
	sendMu  sync.Mutex
	stateMu sync.Mutex
	history []llmtypes.MessageContent
	closed  bool

	// runActive distinguishes true mid-turn steering from a message submitted
	// between turns. retainedActive covers a directly-injected warm tmux turn:
	// it has no Session.Run caller, but it still owns one canonical completion
	// lifecycle and accepts later messages as steering into that same turn.
	runActive              bool
	retainedStarting       bool
	retainedActive         bool
	retainedSeq            uint64
	activeTurn             *canonicalTurnLifecycle
	watchCtx               context.Context
	watchCancel            context.CancelFunc
	museBackgroundWatching bool
	museBackgroundRequests uint64
	museBackgroundNativeID string
	museBackgroundSequence int64
	museQuestionWatching   bool

	// runLiveInputs tracks tmux live input sent while a Session.Run is active.
	// A CLI may queue that input and answer it only after the running response
	// completes, when Run has already returned and nothing reads the CLI. Run
	// hands the tracked inputs to a follow-up retained watch on exit, so the
	// reply still reaches the host as a canonical completion.
	runLiveInputs *runLiveInputState

	// Tests replace this on an individual Session. Production always reads the
	// provider adapter's authoritative retained transcript/sidecar.
	retainedFinalResponse func(llm.Provider, string, time.Time) string
	// retainedEndedWithoutReply reads whether the CLI's own record shows the turn ended with no reply (interrupted or
	// failed): a turn that settles that way has no final text for retainedFinalResponse to find.
	retainedEndedWithoutReply func(llm.Provider, string, time.Time) (string, bool)
	retainedProgressMessages  func(llm.Provider, string) []llmtypes.MessageContent
	// retainedUsage reads the usage the CLI recorded for a retained turn; nil reads the provider adapter's transcript.
	retainedUsage func(llm.Provider, string, string, time.Time) []llmtypes.RetainedTurnUsage
}

// Start opens a stateful session over this immutable agent definition.
func (a *Agent) Start(context.Context) (*Session, error) {
	if a == nil {
		return nil, fmt.Errorf("agent is nil")
	}
	watchCtx, watchCancel := context.WithCancel(context.Background())
	session := &Session{
		agent:       a,
		watchCtx:    watchCtx,
		watchCancel: watchCancel,
	}
	registerTurnSession(a.sessionID, session)
	// Question cards exist only where a person answers them (the builder
	// chat); everywhere else Muse auto-answers and no card is shown.
	if a.provider == llm.ProviderMuseCLI {
		waits := a.museWaitsForUserChoice()
		if a.logger != nil {
			a.logger.Info(fmt.Sprintf("Muse native questions enabled=%t session=%s conditions=persistent=%t user_answers=%t structured=%t",
				waits, a.sessionID, a.musePersistentInteractiveSession, a.userAnswersNativeQuestions, a.wantsStructuredTransport()))
		}
		if waits {
			session.startMuseQuestionWatcher()
		}
	}
	// A restored Muse session may have task rows committed while this server
	// was down. Re-scan its native journal on attach; stable native event IDs
	// let the durable event store discard rows already published before restart.
	if a.provider == llm.ProviderMuseCLI {
		if handle := a.currentAgentSessionHandle(); handle != nil {
			session.startMuseBackgroundWatcher(handle.Provider.NativeSessionID, 0)
		}
	}
	return session, nil
}

// startMuseQuestionWatcher emits requested and settled records directly from
// the native journal, independently of the foreground turn stream.
func (s *Session) startMuseQuestionWatcher() {
	s.stateMu.Lock()
	if s.closed || s.museQuestionWatching {
		s.stateMu.Unlock()
		return
	}
	s.museQuestionWatching = true
	s.stateMu.Unlock()
	reader := musecli.NewQuestionReaderForOwner(s.agent.sessionID)
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			rows, err := reader.Poll()
			if err != nil && s.agent.logger != nil {
				s.agent.logger.Warn(fmt.Sprintf("Muse question journal read failed: %v", err))
			}
			for _, row := range rows {
				if s.watchCtx.Err() != nil {
					return
				}
				if s.agent.logger != nil {
					s.agent.logger.Info(fmt.Sprintf("Muse question card event session=%s native=%s seq=%d kind=%s", s.agent.sessionID, row.NativeSessionID, row.Sequence, row.Kind))
				}
				s.agent.emitTypedEvent(context.Background(), codingAgentQuestionFromMuse(row))
			}
			select {
			case <-s.watchCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func codingAgentQuestionFromMuse(row musecli.QuestionEvent) *events.CodingAgentQuestionEvent {
	kind := "requested"
	if row.Kind == "user_input_prompt_settled" {
		kind = "settled"
	}
	questions := make([]events.CodingAgentQuestionPrompt, 0, len(row.Questions))
	for _, question := range row.Questions {
		options := make([]events.CodingAgentQuestionOption, 0, len(question.Options))
		for _, option := range question.Options {
			options = append(options, events.CodingAgentQuestionOption{Label: option.Label, Description: option.Description})
		}
		prompt := events.CodingAgentQuestionPrompt{ID: question.ID, Header: question.Header, Question: question.Question, Options: options, MultiSelect: question.Multiple()}
		if question.Multiple() {
			prompt.MinSelections, prompt.MaxSelections = question.Selection.MinSelections, question.Selection.MaxSelections
		}
		questions = append(questions, prompt)
	}
	answers := make([]events.CodingAgentQuestionAnswer, 0, len(row.Answers))
	for _, answer := range row.Answers {
		labels := answer.SelectedLabels
		if answer.SelectedLabel != "" {
			labels = []string{answer.SelectedLabel}
		}
		answers = append(answers, events.CodingAgentQuestionAnswer{ID: answer.ID, SelectedLabels: labels})
	}
	return &events.CodingAgentQuestionEvent{
		BaseEventData: events.BaseEventData{EventID: fmt.Sprintf("muse:question:%s:%d", row.NativeSessionID, row.Sequence)},
		Provider:      "muse-cli", NativeSessionID: row.NativeSessionID,
		RunID: row.RunID, NativeSequence: row.Sequence, PromptID: row.PromptID,
		Kind: kind, Questions: questions, Answers: answers, Outcome: row.Outcome,
	}
}

// Run is the one-turn convenience API. Use Start when history must persist
// across multiple turns.
func (a *Agent) Run(ctx context.Context, turn Turn) (Result, error) {
	session, err := a.Start(ctx)
	if err != nil {
		return Result{}, err
	}
	// Run is explicitly the one-turn convenience API. It must not leave the
	// durable session registry holding this Agent after the result returns;
	// callers that need continuation use Start and own Session.Close.
	defer session.Close()
	return session.Run(ctx, turn)
}

// Definition returns a read-only snapshot of the current immutable identity.
func (a *Agent) Definition() AgentDefinitionView {
	instructions := a.systemPrompt
	if a.definition != nil {
		instructions = a.definition.Instructions
	}
	extensions := make([]string, 0, len(a.appendedSystemPrompts))
	composed := instructions
	for _, supplement := range a.appendedSystemPrompts {
		if strings.TrimSpace(supplement) == "" || strings.Contains(composed, supplement) {
			continue
		}
		extensions = append(extensions, supplement)
		composed = composeInstructionExtensions(composed, supplement)
	}
	instructions = composeInstructionExtensions(instructions, extensions...)
	view := AgentDefinitionView{Instructions: instructions}
	for _, skill := range a.attachedSkills {
		if skill != nil && skill.Name != "" {
			view.Skills = append(view.Skills, skill.Name)
			view.SkillDefinitions = append(view.SkillDefinitions, cloneSkill(skill))
		}
	}
	registry, err := a.canonicalRegistry()
	if err == nil {
		for _, tool := range registry.snapshot() {
			description := ""
			if tool.Definition.Function != nil {
				description = tool.Definition.Function.Description
			}
			view.Tools = append(view.Tools, ToolDefinitionView{
				Name:         tool.Name,
				Description:  description,
				Source:       tool.Source,
				DisplayGroup: tool.DisplayGroup,
			})
		}
	}
	sort.Strings(view.Skills)
	return view
}

// Run executes one turn using the supplied runtime policy.
func (s *Session) Run(ctx context.Context, turn Turn) (result Result, err error) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		return Result{}, fmt.Errorf("session is closed")
	}
	if s.retainedStarting || s.retainedActive {
		s.stateMu.Unlock()
		return Result{}, ErrTurnAlreadyInFlight
	}
	s.runActive = true
	history := append([]llmtypes.MessageContent(nil), s.history...)
	s.stateMu.Unlock()
	if !s.agent.tryClaimTurnInFlight() {
		s.stateMu.Lock()
		s.runActive = false
		s.stateMu.Unlock()
		return Result{}, ErrTurnAlreadyInFlight
	}
	lifecycle := newCanonicalTurnLifecycle(turn.ID)
	turn.ID = lifecycle.id
	ctx = withCanonicalTurnLifecycle(ctx, lifecycle)
	s.stateMu.Lock()
	s.activeTurn = lifecycle
	s.runLiveInputs = &runLiveInputState{lifecycle: lifecycle}
	s.stateMu.Unlock()
	var museBackgroundSessionID string
	var museBackgroundBaseline int64
	defer func() {
		result.TurnID = lifecycle.id
		if !lifecycle.isTerminal() {
			status := "completed"
			completion := events.NewUnifiedCompletionEvent(
				"session", string(s.agent.agentMode), turn.Input, result.Text,
				status, time.Since(lifecycle.startedAt), 1,
			)
			completion.Metadata["source"] = "mcpagent_session"
			if err != nil {
				completion.Status = "error"
				completion.Error = err.Error()
			}
			s.agent.annotateUnifiedCompletionEvent(completion)
			if err != nil {
				completion.SetProviderFailure(err)
			}
			s.agent.emitTypedEvent(ctx, completion)
		}
		s.agent.setTurnInFlight(false)
		s.stateMu.Lock()
		s.runActive = false
		if s.activeTurn == lifecycle {
			s.activeTurn = nil
		}
		var followup *retainedWatch
		if liveInputs := s.runLiveInputs; liveInputs != nil && liveInputs.lifecycle == lifecycle {
			s.runLiveInputs = nil
			liveInputs.ended = true
			liveInputs.runText = result.Text
			followup = s.takeRunLiveInputFollowupLocked(liveInputs)
		}
		s.stateMu.Unlock()
		if followup != nil {
			s.runRetainedCompletionWatch(*followup)
		}
		if museBackgroundSessionID != "" {
			s.startMuseBackgroundWatcher(museBackgroundSessionID, museBackgroundBaseline)
		}
	}()
	policy, err := normalizeToolPolicy(turn.ToolPolicy)
	if err != nil {
		return Result{}, err
	}
	ctx = context.WithValue(ctx, turnPolicyContextKey{}, policy)
	if turn.StreamingCallback != nil {
		ctx = context.WithValue(ctx, turnStreamingCallbackContextKey{}, turn.StreamingCallback)
	}

	if len(history) == 0 && len(turn.History) > 0 {
		history = append([]llmtypes.MessageContent(nil), turn.History...)
	}
	if strings.TrimSpace(turn.Input) != "" {
		history = append(history, llmtypes.MessageContent{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: turn.Input}},
		})
	}
	if len(history) == 0 {
		return Result{}, fmt.Errorf("turn input or history is required")
	}

	allowed := policy.allowedMap()
	if allowed != nil && s.agent.isIntrinsicIdentityTool(readSkillToolName) {
		allowed[readSkillToolName] = true
	}
	if s.agent.sessionID != "" {
		codeexec.SetSessionToolAllowList(s.agent.sessionID, allowed)
	}

	var text string
	var updated []llmtypes.MessageContent
	handleBeforeTurn := s.agent.currentAgentSessionHandle()
	nativeSessionID := ""
	if handleBeforeTurn != nil {
		nativeSessionID = handleBeforeTurn.Provider.NativeSessionID
	}
	if s.agent.provider == llm.ProviderMuseCLI && nativeSessionID != "" {
		museBackgroundBaseline = musecli.LatestNativeSequenceForOwner(s.agent.sessionID, nativeSessionID)
	}
	providerStartedAt := time.Now()
	if s.agent.logger != nil {
		s.agent.logger.Debug(fmt.Sprintf("[COMPLETION_TRACE] stage=mcpagent_provider_call_started session=%q provider=%q native_session=%q", s.agent.sessionID, s.agent.provider, nativeSessionID))
	}
	if handleBeforeTurn != nil && !handleBeforeTurn.Provider.Empty() {
		text, updated, _, err = s.agent.continueAgentSessionWithHistory(ctx, handleBeforeTurn, history)
	} else {
		text, updated, err = s.agent.askWithHistory(ctx, history)
	}
	if s.agent.logger != nil {
		outcome := "completed"
		if err != nil {
			outcome = "error"
		}
		handleAfterTurn := s.agent.currentAgentSessionHandle()
		if handleAfterTurn != nil && handleAfterTurn.Provider.NativeSessionID != "" {
			nativeSessionID = handleAfterTurn.Provider.NativeSessionID
		}
		s.agent.logger.Debug(fmt.Sprintf("[COMPLETION_TRACE] stage=mcpagent_provider_call_returned session=%q provider=%q native_session=%q outcome=%s elapsed=%s", s.agent.sessionID, s.agent.provider, nativeSessionID, outcome, time.Since(providerStartedAt).Round(time.Millisecond)))
	}
	if len(updated) > 0 {
		s.stateMu.Lock()
		s.history = append([]llmtypes.MessageContent(nil), updated...)
		s.stateMu.Unlock()
	}
	prompt, completion, total, cache, reasoning, calls, cacheCalls,
		inputCost, outputCost, reasoningCost, cacheCost, totalCost, contextUsage := s.agent.getTokenUsageWithPricing()
	result = Result{
		TurnID:  lifecycle.id,
		Text:    text,
		History: append([]llmtypes.MessageContent(nil), updated...),
		Handle:  s.agent.currentAgentSessionHandle(),
		Usage: Usage{
			PromptTokens:         prompt,
			CompletionTokens:     completion,
			TotalTokens:          total,
			CacheTokens:          cache,
			ReasoningTokens:      reasoning,
			LLMCalls:             calls,
			CacheEnabledLLMCalls: cacheCalls,
			InputCostUSD:         inputCost,
			OutputCostUSD:        outputCost,
			ReasoningCostUSD:     reasoningCost,
			CacheCostUSD:         cacheCost,
			TotalCostUSD:         totalCost,
			ContextUsagePercent:  contextUsage,
		},
	}
	if result.Handle != nil && s.agent.provider == llm.ProviderMuseCLI {
		museBackgroundSessionID = result.Handle.Provider.NativeSessionID
	}
	return result, err
}

// startMuseBackgroundWatcher observes the native journal under the Session
// lifetime. It never writes to a turn's StreamChan, which closes on return.
func (s *Session) startMuseBackgroundWatcher(nativeSessionID string, baseline int64) {
	if nativeSessionID == "" {
		return
	}
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		return
	}
	s.museBackgroundRequests++
	if s.museBackgroundWatching {
		s.stateMu.Unlock()
		return
	}
	if s.museBackgroundNativeID != nativeSessionID {
		s.museBackgroundNativeID, s.museBackgroundSequence = nativeSessionID, 0
	}
	if s.museBackgroundSequence > baseline {
		baseline = s.museBackgroundSequence
	}
	s.museBackgroundSequence = baseline
	s.museBackgroundWatching = true
	s.stateMu.Unlock()
	reader := musecli.NewBackgroundTaskReaderForOwner(s.agent.sessionID, nativeSessionID, baseline)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		readFailureReported := false
		pending := map[string]bool{}
		ended := map[string]bool{}
		for {
			s.stateMu.Lock()
			generation := s.museBackgroundRequests
			s.stateMu.Unlock()
			rows, readErr := reader.Poll()
			if readErr != nil {
				if !readFailureReported && s.agent.logger != nil {
					s.agent.logger.Warn(fmt.Sprintf("Muse background task journal read failed: %v", readErr))
				}
				readFailureReported = true
			} else {
				readFailureReported = false
			}
			for _, row := range rows {
				key := row.RunID + "\x00" + row.TaskID
				switch row.Kind {
				case "completed", "failed", "cancelled", "rejected":
					delete(pending, key)
					ended[key] = true
				default:
					if !ended[key] {
						pending[key] = true
					}
				}
				if s.watchCtx.Err() != nil {
					return
				}
				s.agent.emitTypedEvent(context.Background(), &events.CodingAgentBackgroundTaskEvent{
					BaseEventData: events.BaseEventData{EventID: fmt.Sprintf("muse:%s:%d", nativeSessionID, row.Sequence)},
					Provider:      "muse-cli", NativeSessionID: nativeSessionID,
					RunID: row.RunID, TaskID: row.TaskID, NativeSequence: row.Sequence,
					Kind: row.Kind, Message: row.Message,
				})
				s.stateMu.Lock()
				if s.museBackgroundNativeID == nativeSessionID && row.Sequence > s.museBackgroundSequence {
					s.museBackgroundSequence = row.Sequence
				}
				s.stateMu.Unlock()
			}
			// Stop polling after the turn and all known native tasks end. In
			// particular a restored session with no journal must not glob forever.
			s.stateMu.Lock()
			idle := !s.runActive && !s.retainedActive && !s.retainedStarting && len(pending) == 0 && generation == s.museBackgroundRequests && readErr == nil
			if idle {
				s.museBackgroundWatching = false
			}
			s.stateMu.Unlock()
			if idle {
				return
			}
			select {
			case <-s.watchCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// Send submits input to the durable provider conversation. During Session.Run
// it steers the active turn. Between Runs it starts a retained tmux turn and
// owns that turn through its canonical final-response event; callers receive a
// fast delivery acknowledgement and do not need to scrape provider panes.
func (s *Session) Send(ctx context.Context, input string) (DeliveryResult, error) {
	return s.SendIdentified(ctx, input, QueuedMessageIdentity{})
}

// SendIdentified is Send for a message a person sent: if it has to wait for the turn's next step, the turn records
// it once under this identity, so the chat matches the bubble the person already sees.
func (s *Session) SendIdentified(ctx context.Context, input string, identity QueuedMessageIdentity) (DeliveryResult, error) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		return DeliveryResult{}, fmt.Errorf("session is closed")
	}
	runWasActive := s.runActive
	wasActive := runWasActive || s.retainedActive
	lifecycle := s.activeTurn
	if !wasActive {
		s.retainedStarting = true
		lifecycle = newCanonicalTurnLifecycle("")
		s.activeTurn = lifecycle
	}
	// Live input into a running Session.Run through a tmux CLI. Mark the Run's
	// lifecycle before delivery: the CLI can accept the text just as the
	// running response completes, and the completion must already say that a
	// follow-up owns this input (hosts then keep the input's turn open).
	var runInputs *runLiveInputState
	if runWasActive && s.runLiveInputs != nil && s.agent.liveInputUsesTmux() {
		runInputs = s.runLiveInputs
		runInputs.inflight++
		runInputs.lifecycle.markLiveInputFollowup()
	}
	s.stateMu.Unlock()
	sentAt := time.Now()
	if lifecycle != nil {
		ctx = withCanonicalTurnLifecycle(ctx, lifecycle)
	}
	clearStarting := func() {
		s.stateMu.Lock()
		s.retainedStarting = false
		if !s.runActive && !s.retainedActive && s.activeTurn == lifecycle {
			s.activeTurn = nil
		}
		s.stateMu.Unlock()
	}
	intent := UserMessageDeliveryIntentAuto
	if wasActive {
		intent = UserMessageDeliveryIntentLiveInput
	}
	delivery, err := s.agent.deliverUserMessage(ctx, UserMessageDeliveryRequest{
		SessionID:       s.agent.sessionID,
		Message:         input,
		Intent:          intent,
		MessageID:       identity.ID,
		ClientMessageID: identity.ClientID,
	})
	turnID := ""
	if lifecycle != nil {
		turnID = lifecycle.id
	}
	result := DeliveryResult{
		TurnID:    turnID,
		Queued:    delivery.DeliveryStatus == UserMessageDeliveryStatusQueuedForInjection,
		Status:    delivery.DeliveryStatus,
		Provider:  delivery.Provider,
		Transport: delivery.Transport,
	}
	if runInputs != nil {
		sent := err == nil && delivery.DeliveryStatus == UserMessageDeliveryStatusSentToCLI && delivery.Transport == llm.CodingAgentTransportTmux
		s.stateMu.Lock()
		runInputs.inflight--
		if sent {
			runInputs.record(input, sentAt, delivery.Provider, delivery.Transport)
		}
		// The Run may have returned while this delivery was in flight (the
		// muse durable ack can land after the completion). Then this Send owns
		// the hand-off the Run's exit could not make.
		followup := s.takeRunLiveInputFollowupLocked(runInputs)
		s.stateMu.Unlock()
		if followup != nil {
			s.runRetainedCompletionWatch(*followup)
		}
	}
	if err != nil {
		clearStarting()
		return result, err
	}
	refreshRetainedWatcher := !runWasActive && providerRefreshesRetainedWatcher(delivery.Provider)
	if (!wasActive || refreshRetainedWatcher) && delivery.DeliveryStatus == UserMessageDeliveryStatusSentToCLI && delivery.Transport == llm.CodingAgentTransportTmux {
		// Replace the retained reader generation after every CLI send, so a
		// final read in flight before delivery cannot close the newer input.
		s.startRetainedCompletionWatchFor(lifecycle, input, delivery.Provider, delivery.Transport, wasActive, sentAt)
	} else if !wasActive {
		clearStarting()
	}
	return result, nil
}

func providerRefreshesRetainedWatcher(provider llm.Provider) bool {
	switch provider {
	case llm.ProviderClaudeCode, llm.ProviderCodexCLI, llm.ProviderCursorCLI, llm.ProviderMuseCLI, llm.ProviderPiCLI, llm.ProviderAgyCLI:
		return true
	}
	return false
}

const retainedCompletionPollInterval = 100 * time.Millisecond

// retainedEndedWithoutReplyGrace is how long the CLI's record must keep showing a turn ended with no reply before the
// watcher settles it, so a record being written (an abort followed at once by a retry) is not mistaken for the end.
var retainedEndedWithoutReplyGrace = 1500 * time.Millisecond

// retainedLiveInputFinalGrace is how long a final response must stay final
// before a watcher refreshed for live input accepts it. Claude writes the
// running response's end_turn first and only ~100ms later appends the
// follow-ups it queued meanwhile as user messages; a single read in that gap
// took the OLD answer as the follow-up's completion, ended the turn, and left
// the CLI's real work on the follow-up unwatched (RTS 2026-09-24 04:19:45:
// 4.5 minutes of narration never reached the chat).
var retainedLiveInputFinalGrace = 1500 * time.Millisecond

func (s *Session) startRetainedCompletionWatch(lifecycle *canonicalTurnLifecycle, input string, provider llm.Provider, transport llm.CodingAgentTransport) {
	s.startRetainedCompletionWatchFor(lifecycle, input, provider, transport, false)
}

// startRetainedCompletionWatchFor starts the retained watcher. liveInput marks
// a watcher refreshed for a message sent into a running response: its final
// must survive retainedLiveInputFinalGrace, re-read every poll, so queued
// follow-ups appended after the old end_turn cancel it.
func (s *Session) startRetainedCompletionWatchFor(lifecycle *canonicalTurnLifecycle, input string, provider llm.Provider, transport llm.CodingAgentTransport, liveInput bool, acceptedAt ...time.Time) {
	startedAt := time.Now()
	if len(acceptedAt) > 0 && !acceptedAt[0].IsZero() {
		// A native tool can commit before delivery's acknowledgement returns.
		// Reading only after the acknowledgement silently dropped that tool.
		startedAt = acceptedAt[0]
	}
	s.stateMu.Lock()
	s.retainedStarting = false
	if s.closed {
		s.stateMu.Unlock()
		return
	}
	watch := s.beginRetainedWatchLocked(lifecycle)
	s.stateMu.Unlock()
	watch.input, watch.provider, watch.transport = input, provider, transport
	watch.liveInput, watch.startedAt = liveInput, startedAt
	s.runRetainedCompletionWatch(watch)
}

// retainedWatch is one retained completion watcher's configuration.
type retainedWatch struct {
	lifecycle *canonicalTurnLifecycle
	seq       uint64
	input     string
	provider  llm.Provider
	transport llm.CodingAgentTransport
	liveInput bool
	// startedAt bounds the provider transcript read: only a response after
	// this instant can complete the watch.
	startedAt time.Time
	// previousResponse is set for a follow-up of live input sent into a
	// Session.Run. A final equal to (contained in) that Run's own response
	// means the input steered the Run and was already answered.
	previousResponse string
	followup         bool
}

// beginRetainedWatchLocked makes a new retained watcher current. Callers hold
// stateMu and have checked s.closed.
func (s *Session) beginRetainedWatchLocked(lifecycle *canonicalTurnLifecycle) retainedWatch {
	s.retainedActive = true
	if lifecycle == nil {
		lifecycle = newCanonicalTurnLifecycle("")
	}
	s.activeTurn = lifecycle
	s.retainedSeq++
	return retainedWatch{lifecycle: lifecycle, seq: s.retainedSeq}
}

func (s *Session) runRetainedCompletionWatch(watch retainedWatch) {
	lifecycle, seq, input, provider, transport := watch.lifecycle, watch.seq, watch.input, watch.provider, watch.transport
	liveInput, startedAt := watch.liveInput, watch.startedAt
	s.stateMu.Lock()
	watchCtx := s.watchCtx
	reader := s.retainedFinalResponse
	endedReader := s.retainedEndedWithoutReply
	progressReader := s.retainedProgressMessages
	s.stateMu.Unlock()
	if watchCtx == nil {
		watchCtx = context.Background()
	}
	if reader == nil {
		reader = func(provider llm.Provider, sessionID string, start time.Time) string {
			text, nativeID, roots := retainedturn.FinalResponseWithMetadata(provider, sessionID, start)
			lifecycle.setNativePublication(nativeID, roots)
			return text
		}
	}
	if endedReader == nil {
		endedReader = llmproviders.ReadCodingAgentRetainedTurnEndedWithoutReply
	}
	// Direct sends and adopted terminal input share this retained-turn path.
	// Idle observers must restart here as well as after Session.Run.
	if provider == llm.ProviderMuseCLI {
		if handle := s.agent.currentAgentSessionHandle(); handle != nil {
			s.startMuseBackgroundWatcher(handle.Provider.NativeSessionID, 0)
		}
	}
	if progressReader == nil {
		progressReader = func(provider llm.Provider, ownerSessionID string) []llmtypes.MessageContent {
			return llmproviders.ReadCodingAgentRetainedTurnStructuredProgressMessages(provider, ownerSessionID, startedAt)
		}
	}

	go func() {
		ticker := time.NewTicker(retainedCompletionPollInterval)
		defer ticker.Stop()
		chunkIndex := 0
		tools := map[string]retainedNativeTool{}
		var lastProgressRead time.Time
		var finalSeenAt, endedSeenAt time.Time
		finalGrace := retainedLiveInputFinalGrace
		if watch.followup {
			// Everything the CLI wrote before the Run returned was streamed by
			// that Run. Advance the progress cursor past it so the follow-up
			// streams only the CLI's reply to the live input.
			s.stateMu.Lock()
			if !s.closed && s.retainedActive && s.retainedSeq == seq {
				_ = progressReader(provider, s.agent.sessionID)
			}
			s.stateMu.Unlock()
			lastProgressRead = time.Now()
		}
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
				s.stateMu.Lock()
				current := !s.closed && s.retainedActive && s.retainedSeq == seq
				s.stateMu.Unlock()
				if !current {
					return
				}
				if time.Since(lastProgressRead) >= 400*time.Millisecond {
					s.emitRetainedProgress(lifecycle, seq, provider, progressReader, &chunkIndex, tools)
					lastProgressRead = time.Now()
				}
				finalResult := strings.TrimSpace(reader(provider, s.agent.sessionID, startedAt))
				if finalResult == "" {
					finalSeenAt = time.Time{}
					// The turn may have ended with no reply (the person interrupted it): the CLI's own record says so,
					// and without this the watch would wait for a reply that never comes and the chat stay Working.
					if reason, ended := endedReader(provider, s.agent.sessionID, startedAt); ended {
						if endedSeenAt.IsZero() {
							endedSeenAt = time.Now()
						}
						if time.Since(endedSeenAt) >= retainedEndedWithoutReplyGrace {
							s.emitRetainedProgress(lifecycle, seq, provider, progressReader, &chunkIndex, tools)
							s.completeRetainedTurnInterrupted(lifecycle, seq, input, reason, provider, transport, startedAt)
							return
						}
					} else {
						endedSeenAt = time.Time{}
					}
					continue
				}
				endedSeenAt = time.Time{}
				if liveInput {
					if finalSeenAt.IsZero() {
						finalSeenAt = time.Now()
					}
					if time.Since(finalSeenAt) < finalGrace {
						continue
					}
				}
				if watch.followup && responseAlreadyContains(watch.previousResponse, finalResult) {
					s.completeRetainedTurnAnsweredByPrevious(lifecycle, seq, input, provider, transport, startedAt)
					return
				}
				// The final read can see a commit newer than the last progress poll.
				// Flush it before completion closes this watcher.
				s.emitRetainedProgress(lifecycle, seq, provider, progressReader, &chunkIndex, tools)
				s.completeRetainedTurn(lifecycle, seq, input, finalResult, provider, transport, startedAt)
				return
			}
		}
	}()
}

// runLiveInputState is the live input a tmux CLI accepted during one
// Session.Run. Guarded by Session.stateMu.
type runLiveInputState struct {
	lifecycle *canonicalTurnLifecycle
	inflight  int
	inputs    []string
	firstSent time.Time
	provider  llm.Provider
	transport llm.CodingAgentTransport
	ended     bool
	runText   string
	handedOff bool
}

func (r *runLiveInputState) record(input string, sentAt time.Time, provider llm.Provider, transport llm.CodingAgentTransport) {
	r.inputs = append(r.inputs, input)
	if r.firstSent.IsZero() || sentAt.Before(r.firstSent) {
		r.firstSent = sentAt
	}
	r.provider, r.transport = provider, transport
}

// takeRunLiveInputFollowupLocked hands the live input a finished Run received
// to exactly one follow-up watch: once the Run has returned and no delivery
// into it is still in flight. Both Run's exit and a late Send call it; the
// handedOff flag makes the second caller a no-op. The input is never sent
// again -- the watch only reads the CLI's transcript for its reply.
func (s *Session) takeRunLiveInputFollowupLocked(r *runLiveInputState) *retainedWatch {
	if r == nil || r.handedOff || !r.ended || r.inflight > 0 || len(r.inputs) == 0 || s.closed {
		return nil
	}
	r.handedOff = true
	if s.retainedActive || s.runActive {
		// A newer turn (a Run, or a retained watch started by a later Send)
		// already owns the CLI's next output; it reads the queued reply.
		return nil
	}
	watch := s.beginRetainedWatchLocked(newCanonicalTurnLifecycle(""))
	watch.input = strings.Join(r.inputs, "\n\n")
	watch.provider, watch.transport = r.provider, r.transport
	watch.liveInput = true
	watch.startedAt = r.firstSent
	watch.previousResponse = r.runText
	watch.followup = true
	return &watch
}

// responseAlreadyContains reports whether final is the previous response (or
// part of it), compared on whitespace-normalized text.
func responseAlreadyContains(previous, final string) bool {
	previous, final = strings.Join(strings.Fields(previous), " "), strings.Join(strings.Fields(final), " ")
	return previous != "" && final != "" && strings.Contains(previous, final)
}

// completeRetainedTurnAnsweredByPrevious closes a follow-up whose live input
// the Run's own response already answered. The completion has no final text,
// so no host renders or persists the previous answer a second time.
func (s *Session) completeRetainedTurnAnsweredByPrevious(lifecycle *canonicalTurnLifecycle, seq uint64, input string, provider llm.Provider, transport llm.CodingAgentTransport, startedAt time.Time) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.stateMu.Lock()
	if s.closed || !s.retainedActive || s.retainedSeq != seq {
		s.stateMu.Unlock()
		return
	}
	s.retainedActive = false
	if s.activeTurn == lifecycle {
		s.activeTurn = nil
	}
	s.stateMu.Unlock()
	s.emitRetainedTurnUsage(lifecycle, provider, startedAt) // the input was answered by the Run's response, but its runs spent tokens
	completion := events.NewUnifiedCompletionEvent("coding_agent", "retained", input, "", "completed", time.Since(startedAt), 1)
	completion.Metadata["source"] = "mcpagent_session"
	completion.Metadata["provider"] = string(provider)
	completion.Metadata["transport"] = string(transport)
	completion.Metadata[AnsweredByPreviousResponseMetadataKey] = true
	endedAt := time.Now()
	s.agent.emitTypedEvent(withCanonicalTurnLifecycle(context.Background(), lifecycle), completion)
	s.emitRetainedStreamEnd(lifecycle, provider, startedAt, endedAt)
}

// completeRetainedTurnInterrupted closes a retained turn the CLI's own record shows ended with no reply (interrupted
// or failed). It settles like any completion, with no final text and outcome "interrupted", so the host ends the turn
// and the chat returns to idle.
func (s *Session) completeRetainedTurnInterrupted(lifecycle *canonicalTurnLifecycle, seq uint64, input, reason string, provider llm.Provider, transport llm.CodingAgentTransport, startedAt time.Time) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.stateMu.Lock()
	if s.closed || !s.retainedActive || s.retainedSeq != seq {
		s.stateMu.Unlock()
		return
	}
	s.retainedActive = false
	if s.activeTurn == lifecycle {
		s.activeTurn = nil
	}
	s.stateMu.Unlock()
	s.emitRetainedTurnUsage(lifecycle, provider, startedAt)
	completion := events.NewUnifiedCompletionEvent("coding_agent", "retained", input, "", "completed", time.Since(startedAt), 1)
	completion.Metadata["source"] = "mcpagent_session"
	completion.Metadata["provider"] = string(provider)
	completion.Metadata["transport"] = string(transport)
	completion.Metadata[RetainedTurnOutcomeMetadataKey] = "interrupted"
	completion.Metadata["stop_reason"] = reason
	endedAt := time.Now()
	s.agent.emitTypedEvent(withCanonicalTurnLifecycle(context.Background(), lifecycle), completion)
	s.emitRetainedStreamEnd(lifecycle, provider, startedAt, endedAt)
}

func (s *Session) completeRetainedTurn(lifecycle *canonicalTurnLifecycle, seq uint64, input, finalResult string, provider llm.Provider, transport llm.CodingAgentTransport, startedAt time.Time) {
	// A follow-up may arrive while the previous transcript read is in flight.
	// Let delivery replace the watcher before accepting that old completion.
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.stateMu.Lock()
	if s.closed || !s.retainedActive || s.retainedSeq != seq {
		s.stateMu.Unlock()
		return
	}
	s.retainedActive = false
	if s.activeTurn == lifecycle {
		s.activeTurn = nil
	}
	s.history = append(s.history,
		llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: input}}},
		llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: finalResult}}},
	)
	s.stateMu.Unlock()

	s.emitRetainedTurnUsage(lifecycle, provider, startedAt)
	completion := events.NewUnifiedCompletionEvent("coding_agent", "retained", input, finalResult, "completed", time.Since(startedAt), 1)
	completion.Metadata["source"] = "mcpagent_session"
	completion.Metadata["provider"] = string(provider)
	completion.Metadata["transport"] = string(transport)
	endedAt := time.Now()
	s.agent.emitTypedEvent(withCanonicalTurnLifecycle(context.Background(), lifecycle), completion)
	s.emitRetainedStreamEnd(lifecycle, provider, startedAt, endedAt)
}

func (s *Session) Snapshot() *AgentSessionHandle {
	return s.agent.currentAgentSessionHandle()
}

// ActiveTurnID reports the ID of whichever turn owns this session's canonical
// completion right now -- a Session.Run in flight, or a retained tmux turn
// started by Send between Runs -- or "" if none is active. Hosts that tag
// per-tool-call events (e.g. tool_call_start/tool_call_end) with a turn ID
// must call this at the moment each event fires rather than caching a turn ID
// from an earlier setup: a retained delivery never calls Run again, so an ID
// captured once at the first turn goes stale the moment a later retained turn
// starts (PLAT-180).
func (s *Session) ActiveTurnID() string {
	lifecycle := s.currentTurnLifecycle()
	if lifecycle == nil {
		return ""
	}
	return lifecycle.id
}

// currentTurnLifecycle returns the same lifecycle object ActiveTurnID reads
// its ID from, for package-internal callers that need to attach it to a
// context (e.g. withCanonicalTurnLifecycle) rather than just read its ID.
func (s *Session) currentTurnLifecycle() *canonicalTurnLifecycle {
	if s == nil {
		return nil
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.activeTurn
}

func (s *Session) Events() <-chan *events.AgentEvent {
	stream, _ := s.agent.getEventStream()
	return stream
}

func (s *Session) Close() error {
	if s.agent != nil {
		defer s.agent.releaseScopedMCPClients()
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.closed = true
	if s.watchCancel != nil {
		s.watchCancel()
	}
	s.retainedActive = false
	s.retainedStarting = false
	s.activeTurn = nil
	s.history = nil
	unregisterTurnSession(s)
	return nil
}

type normalizedToolPolicy struct {
	allowed map[string]bool
}

func normalizeToolPolicy(policy ToolPolicy) (normalizedToolPolicy, error) {
	if len(policy.AllowedTools) == 0 {
		return normalizedToolPolicy{}, nil
	}
	allowed := make(map[string]bool, len(policy.AllowedTools))
	for _, raw := range policy.AllowedTools {
		name := strings.TrimSpace(raw)
		if name == "" {
			return normalizedToolPolicy{}, fmt.Errorf("tool policy contains an empty name")
		}
		if name != raw {
			return normalizedToolPolicy{}, fmt.Errorf("tool policy name %q has surrounding whitespace", raw)
		}
		allowed[name] = true
	}
	return normalizedToolPolicy{allowed: allowed}, nil
}

func (p normalizedToolPolicy) allows(name string) bool {
	return p.allowed == nil || p.allowed[name]
}

func (p normalizedToolPolicy) allowedMap() map[string]bool {
	if p.allowed == nil {
		return nil
	}
	result := make(map[string]bool, len(p.allowed))
	for name := range p.allowed {
		result[name] = true
	}
	return result
}

func toolPolicyFromContext(ctx context.Context) (normalizedToolPolicy, bool) {
	if ctx == nil {
		return normalizedToolPolicy{}, false
	}
	policy, ok := ctx.Value(turnPolicyContextKey{}).(normalizedToolPolicy)
	return policy, ok
}
