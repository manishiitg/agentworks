package musecli

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type museMSPItem struct {
	ItemID        string `json:"itemId"`
	Kind          string `json:"kind"`
	TurnID        string `json:"turnId"`
	Status        string `json:"status"`
	Text          string `json:"text"`
	Tool          string `json:"tool"`
	Args          string `json:"args"`
	VisibleOutput string `json:"visibleOutput"`
	FailureReason string `json:"failureReason"`
	TokensBefore  int    `json:"tokensBefore"`
	// WakeSourceDescriptions names the Monitors that woke Muse for this reply.
	WakeSourceDescriptions []string `json:"wakeSourceDescriptions"`
	TokensAfter            int      `json:"tokensAfter"`
	Outcome                string   `json:"outcome"`
	Reason                 string   `json:"reason"`
	Trigger                string   `json:"trigger"`
}

type museMSPTokenUsage struct {
	TurnID       string `json:"turnId"`
	ModelID      string `json:"modelId"`
	PromptTokens int    `json:"promptTokens"`
	TotalTokens  int    `json:"totalTokens"`
	Usage        struct {
		InputTokens      int `json:"inputTokens"`
		OutputTokens     int `json:"outputTokens"`
		CacheReadTokens  int `json:"cacheReadTokens"`
		CacheWriteTokens int `json:"cacheWriteTokens"`
	} `json:"usage"`
}

// consumeMuseMSPTurn follows one turn to its end: text and reasoning stream as
// they are written, each tool call is reported when it starts and when it
// ends, and the host's questions and approvals are answered as the tmux lane
// answers them. turn/completed ends it.
func (a *MuseCLIAdapter) consumeMuseMSPTurn(ctx context.Context, s *museMSPSession, events <-chan museRPCMessage, turnID string, totals *museMSPTurnTotals, opts *llmtypes.CallOptions, emit func(llmtypes.StreamChunk)) (*llmtypes.ContentResponse, error) {
	meta := func(extra map[string]interface{}) map[string]interface{} {
		m := map[string]interface{}{"muse_session_id": s.sessionID, "muse_cli_stream_source": "transcript"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	items := map[string]*museMSPItem{}
	toolStarts := map[string]time.Time{}
	// retry is Muse's scheduled retry of a failed model attempt, shown until
	// the model answers again or the turn ends (Muse sends no "recovered").
	var retry *llmtypes.NativeRetry
	endRetry := func(success bool) {
		if retry == nil {
			return
		}
		end := *retry
		end.Phase, end.Success, retry = "end", success, nil
		emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeNativeRetry, NativeRetry: &end, Metadata: meta(nil)})
	}
	// Each Muse message is its own reply block, so text written before a
	// message the person sent mid-turn stays above it and the rest goes below.
	blocks := map[string]bool{}
	blockID := func(itemID string) string { return "muse:" + s.sessionID + ":" + turnID + ":" + itemID }
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.rpc.done:
			return nil, s.rpc.failure()
		case msg := <-events:
			if len(msg.ID) > 0 && msg.Method != "" {
				a.answerMuseMSPRequest(ctx, s, opts, msg)
				continue
			}
			switch msg.Method {
			case "session/goalChanged":
				if update, ok := observeMuseMSPGoal(s, msg); ok {
					emit(llmtypes.NativeGoalChunk(update))
				}
			case "session/todoListChanged":
				var owner struct {
					SessionID string `json:"sessionId"`
					TurnID    string `json:"turnId"`
				}
				if json.Unmarshal(msg.Params, &owner) != nil || (owner.SessionID != "" && owner.SessionID != s.sessionID) || (owner.TurnID != "" && owner.TurnID != turnID) {
					continue
				}
				if plan, ok := llmtypes.ParseCodingAgentPlan(msg.Params, "items"); ok {
					plan.Provider, plan.NativeTurnID, plan.PlanID = "muse-cli", turnID, "muse:"+turnID
					emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeCodingAgentPlan, CodingAgentPlan: plan, Metadata: meta(nil)})
				}
			case "item/started", "item/updated", "item/completed":
				var p struct {
					Item museMSPItem `json:"item"`
				}
				if json.Unmarshal(msg.Params, &p) != nil {
					continue
				}
				if p.Item.TurnID != "" && p.Item.TurnID != turnID {
					// A turn Muse runs on its own (a Monitor) while this one runs.
					a.handleIdleMuseMSP(s, msg)
					continue
				}
				if p.Item.Kind == "agentMessage" || p.Item.Kind == "reasoning" || p.Item.Kind == "toolCall" {
					endRetry(true)
				}
				item := p.Item
				items[item.ItemID] = &item
				switch item.Kind {
				case "toolCall":
					if msg.Method == "item/started" {
						toolStarts[item.ItemID] = time.Now()
						emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallStart, ToolName: item.Tool, ToolCallID: item.ItemID, ToolArgs: item.Args, Metadata: meta(nil)})
					} else if msg.Method == "item/completed" {
						result := item.VisibleOutput
						if result == "" {
							result = item.FailureReason
						}
						chunk := llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallEnd, ToolCallID: item.ItemID, ToolResult: result, Metadata: meta(nil)}
						if started, ok := toolStarts[item.ItemID]; ok {
							chunk.ToolDuration = time.Since(started)
						}
						emit(chunk)
					}
				case "agentMessage":
					if msg.Method == "item/completed" && blocks[item.ItemID] {
						emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeAssistantBlock, Content: item.Text, Metadata: meta(map[string]interface{}{llmtypes.AssistantBlockIDMetadataKey: blockID(item.ItemID), llmtypes.AssistantBlockPhaseMetadataKey: "end"})})
					}
					if msg.Method == "item/completed" && strings.TrimSpace(item.Text) != "" {
						totals.lastAnswer = strings.TrimSpace(item.Text)
						totals.answers = append(totals.answers, totals.lastAnswer)
					}
				case "compaction":
					if msg.Method == "item/completed" {
						if item.Outcome == "" || item.Outcome == "compacted" {
							s.instructions.Compacted()
						}
						emit(museMSPCompactionChunk(item))
					}
				}
			case "item/delta":
				var p struct {
					ItemID string `json:"itemId"`
					Field  string `json:"field"`
					Delta  string `json:"delta"`
				}
				if json.Unmarshal(msg.Params, &p) != nil || p.Delta == "" {
					continue
				}
				item := items[p.ItemID]
				if item == nil {
					continue
				}
				endRetry(true)
				switch {
				case item.Kind == "agentMessage" && (p.Field == "" || p.Field == "text"):
					if !blocks[p.ItemID] {
						blocks[p.ItemID] = true
						emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeAssistantBlock, Metadata: meta(map[string]interface{}{llmtypes.AssistantBlockIDMetadataKey: blockID(p.ItemID), llmtypes.AssistantBlockPhaseMetadataKey: "start"})})
					}
					emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: p.Delta, Metadata: meta(map[string]interface{}{llmtypes.ContentDeltaMetadataKey: true, llmtypes.AssistantBlockIDMetadataKey: blockID(p.ItemID)})})
				case item.Kind == "reasoning":
					emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeReasoning, Content: p.Delta, Metadata: meta(map[string]interface{}{"thinking_active": true, llmtypes.ContentDeltaMetadataKey: true})})
				}
			case "session/tokenUsage":
				var p museMSPTokenUsage
				if json.Unmarshal(msg.Params, &p) == nil && (p.TurnID == "" || p.TurnID == turnID) {
					totals.input += p.Usage.InputTokens
					totals.output += p.Usage.OutputTokens
					totals.cacheRead += p.Usage.CacheReadTokens
					totals.cacheWrite += p.Usage.CacheWriteTokens
					if p.ModelID != "" {
						totals.model = p.ModelID
					}
					status := s.statusLine(totals.model, totals.input, totals.output, totals.cacheRead, totals.cacheWrite)
					emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeStatusLine, StatusLine: status, Metadata: meta(nil)})
				}
			case "session/contextUsage":
				var p struct {
					UsedTokens   int `json:"usedTokens"`
					WindowTokens int `json:"windowTokens"`
				}
				if json.Unmarshal(msg.Params, &p) == nil {
					s.mu.Lock()
					s.context = museMSPContext{used: p.UsedTokens, window: p.WindowTokens}
					s.mu.Unlock()
				}
			case "usage/changed":
				var p museMSPPlanUsage
				if json.Unmarshal(msg.Params, &p) == nil {
					s.mu.Lock()
					s.usage = &p
					s.mu.Unlock()
				}
			case "turn/retryScheduled", "turn/modelRetryScheduled":
				var p struct {
					TurnID       string `json:"turnId"`
					Attempt      int    `json:"attempt"`
					NextAttempt  int    `json:"nextAttempt"`
					MaxAttempts  int    `json:"maxAttempts"`
					RetryDelayMs int64  `json:"retryDelayMs"`
				}
				if json.Unmarshal(msg.Params, &p) != nil || (p.TurnID != "" && p.TurnID != turnID) {
					continue
				}
				next := p.NextAttempt
				if next <= 0 {
					next = p.Attempt + 1
				}
				// Muse can report one retry at both the turn and the model layer.
				if retry != nil && retry.Attempt == next {
					continue
				}
				endRetry(false)
				retry = &llmtypes.NativeRetry{Provider: "muse-cli", NativeSessionID: s.sessionID, NativeTurnID: turnID, Phase: "start", Attempt: next, MaxAttempts: p.MaxAttempts, DelayMS: p.RetryDelayMs}
				emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeNativeRetry, NativeRetry: retry, Metadata: meta(nil)})
			case "turn/started":
				var p struct {
					TurnID string `json:"turnId"`
				}
				if json.Unmarshal(msg.Params, &p) == nil && p.TurnID != "" && p.TurnID != turnID {
					a.handleIdleMuseMSP(s, msg)
				}

			case "turn/completed":
				var p struct {
					TurnID   string `json:"turnId"`
					Terminal string `json:"terminal"`
					Reason   string `json:"reason"`
					Error    *struct {
						Kind    string `json:"kind"`
						Message string `json:"message"`
						Quota   *struct {
							LimitKind  string `json:"limitKind"`
							ResetsAtMs int64  `json:"resetsAtMs"`
						} `json:"quota"`
					} `json:"error"`
				}
				if json.Unmarshal(msg.Params, &p) != nil {
					continue
				}
				if p.TurnID != turnID {
					a.handleIdleMuseMSP(s, msg)
					continue
				}
				endRetry(p.Terminal == "completed")
				switch p.Terminal {
				case "completed":
				case "cancelled":
					return nil, context.Canceled
				default:
					if p.Error != nil && p.Error.Quota != nil {
						// The host says it is the quota wall, with its reset time.
						return nil, &llmerrors.Error{Kind: llmerrors.KindQuotaExhausted, Provider: "muse-cli", Model: a.modelID, RetryAt: time.UnixMilli(p.Error.Quota.ResetsAtMs).UTC(), Err: fmt.Errorf("Muse usage limit reached (%s)", p.Error.Quota.LimitKind)}
					}
					reason := p.Reason
					if p.Error != nil && p.Error.Message != "" {
						reason = p.Error.Message
					}
					return nil, fmt.Errorf("muse turn %s: %s", p.Terminal, strings.TrimSpace(reason))
				}
				turnText := strings.Join(totals.answers, "\n\n")
				answer := totals.lastAnswer
				if totals.followups > 0 {
					// Each reply answers a different message: keep both in the saved chat.
					answer = turnText
				}
				return a.museMSPResponse(s, answer, turnText, totals.model, totals.input, totals.output, totals.cacheRead, totals.cacheWrite), nil
			}
		}
	}
}

// answerMuseMSPRequest answers what the host asks of the client. Approvals get
// today's answer: approved (the session runs with allowAll; Muse's own shell
// and writes are off under a tool allowlist), never a new person-facing gate.
// Questions go to the chat's question card when the chat lets its person
// choose, else the first option is taken, as the tmux lane does.
func (a *MuseCLIAdapter) answerMuseMSPRequest(ctx context.Context, s *museMSPSession, opts *llmtypes.CallOptions, msg museRPCMessage) {
	_ = s.rpc.reply(msg.ID, map[string]any{})
	switch msg.Method {
	case "approval/request":
		var p struct {
			ApprovalID       string `json:"approvalId"`
			AvailableChoices []struct {
				ChoiceID string `json:"choiceId"`
				Decision string `json:"decision"`
			} `json:"availableChoices"`
		}
		if json.Unmarshal(msg.Params, &p) != nil {
			return
		}
		for _, choice := range p.AvailableChoices {
			if choice.Decision == "approved" {
				_ = s.rpc.call(ctx, "approval/decide", map[string]any{"commandId": museCommandID(), "approvalId": p.ApprovalID, "choiceId": choice.ChoiceID}, nil)
				return
			}
		}
	case "userInput/request":
		var p struct {
			UserInputID string     `json:"userInputId"`
			Questions   []Question `json:"questions"`
		}
		if json.Unmarshal(msg.Params, &p) != nil {
			return
		}
		if museMSPUserChoice(opts) {
			// The chat's question card answers it; if the person has not
			// answered by museUserChoiceWait, the first option is taken.
			go func(id string, questions []Question) {
				select {
				case <-time.After(museUserChoiceWait):
				case <-s.rpc.done:
					return
				}
				if prompt, err := pendingQuestionPrompt(s.owner); err == nil && prompt != nil && prompt.PromptID == id {
					_ = museMSPAnswer(context.Background(), s, id, FirstOptionAnswers(questions))
				}
			}(p.UserInputID, p.Questions)
			return
		}
		_ = museMSPAnswer(ctx, s, p.UserInputID, FirstOptionAnswers(p.Questions))
	}
}

func museMSPAnswer(ctx context.Context, s *museMSPSession, userInputID string, answers []QuestionAnswer) error {
	wire := make([]map[string]any, 0, len(answers))
	for _, answer := range answers {
		entry := map[string]any{"questionId": answer.ID}
		if len(answer.SelectedLabels) > 0 {
			entry["selectedLabels"] = answer.SelectedLabels
		} else {
			entry["selectedLabel"] = answer.SelectedLabel
		}
		wire = append(wire, entry)
	}
	return s.rpc.call(ctx, "userInput/answer", map[string]any{"commandId": museCommandID(), "sessionId": s.sessionID, "userInputId": userInputID, "answers": wire}, nil)
}

func museMSPCompactionChunk(item museMSPItem) llmtypes.StreamChunk {
	now := time.Now().UTC()
	end := llmtypes.ContextCompaction{Provider: "muse-cli", Phase: llmtypes.ContextCompactionPhaseEnd, ID: item.ItemID, Trigger: item.Trigger, TokensBefore: item.TokensBefore, TokensAfter: item.TokensAfter, EndedAt: now, Outcome: llmtypes.ContextCompactionOutcomeSuccess}
	if item.Outcome != "" && item.Outcome != "compacted" {
		end.Outcome = llmtypes.ContextCompactionOutcomeFailed
	}
	return llmtypes.ContextCompactionChunk(end)
}

func (a *MuseCLIAdapter) museMSPResponse(s *museMSPSession, answer, turnText, model string, input, output, cacheRead, cacheWrite int) *llmtypes.ContentResponse {
	// Muse's inputTokens already includes the cached part (as in the sidecar lane, museAttachTurnCost). Adding
	// cacheRead again counted it twice: a chat's turns showed 100.9M prompt tokens against 50.8M in Muse's own
	// session file, and the cache share fell from ~95% to ~50% (server B, 3 Nov).
	prompt := input
	if prompt < cacheRead+cacheWrite {
		prompt = input + cacheRead + cacheWrite
	}
	total := prompt + output
	gi := &llmtypes.GenerationInfo{Additional: map[string]interface{}{"provider": "muse-cli", "muse_cli_mode": "msp", "muse_session_id": s.sessionID}}
	if total > 0 {
		gi.PromptTokens, gi.CompletionTokens, gi.TotalTokens = &prompt, &output, &total
		if cacheRead > 0 {
			gi.CachedContentTokens = &cacheRead
			gi.Additional["cache_read_input_tokens"] = cacheRead
			gi.Additional["prompt_tokens_include_cache"] = true
		}
		if cacheWrite > 0 {
			gi.Additional["cache_creation_input_tokens"] = cacheWrite
		}
	}
	if model == "" {
		model = a.modelID
	}
	s.mu.Lock()
	if windows := s.windowsLocked(); len(windows) > 0 {
		gi.Additional[llmtypes.RateLimitWindowsMetaKey] = windows
	}
	s.mu.Unlock()
	if turnText != "" && turnText != answer {
		gi.Additional["assistant_turn_text"] = turnText
	}
	llmtypes.AttachCodingProviderSessionHandle(gi, llmtypes.CodingProviderSessionHandle{
		Provider:        "muse-cli",
		Transport:       llmtypes.CodingProviderTransportMSP,
		NativeSessionID: s.sessionID,
		WorkingDir:      s.workdir,
		Model:           model,
		Status:          llmtypes.CodingProviderSessionStatusIdle,
	})
	return &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{Content: answer, StopReason: "stop", GenerationInfo: gi}}}
}

// compactMuseMSP runs one compaction on the chat's session: an idle
// compaction, or Compact now.
func (a *MuseCLIAdapter) compactMuseMSP(ctx context.Context, s *museMSPSession, emit func(llmtypes.StreamChunk)) (*llmtypes.ContentResponse, error) {
	started := time.Now().UTC()
	id := fmt.Sprintf("muse-compact-%d", started.UnixMilli())
	emit(llmtypes.ContextCompactionChunk(llmtypes.ContextCompaction{Provider: "muse-cli", Phase: llmtypes.ContextCompactionPhaseStart, ID: id, StartedAt: started}))
	events := s.listen()
	defer s.stopListening()
	if err := s.rpc.call(ctx, "session/compact", map[string]any{"commandId": museCommandID(), "sessionId": s.sessionID}, nil); err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.rpc.done:
			return nil, s.rpc.failure()
		case msg := <-events:
			if msg.Method != "item/completed" {
				continue
			}
			var p struct {
				Item museMSPItem `json:"item"`
			}
			if json.Unmarshal(msg.Params, &p) != nil || p.Item.Kind != "compaction" {
				continue
			}
			if p.Item.Outcome == "" || p.Item.Outcome == "compacted" {
				s.instructions.Compacted()
			}
			end := museMSPCompactionChunk(p.Item)
			emit(end)
			result := llmtypes.IdleCompactResult{Compacted: p.Item.Outcome == "compacted", Reason: llmtypes.IdleCompactReasonCompacted, TokensBefore: p.Item.TokensBefore, TokensAfter: p.Item.TokensAfter}
			if !result.Compacted {
				result.Reason = llmtypes.IdleCompactReasonNoSession
				if p.Item.Outcome == "failed" || p.Item.Outcome == "cancelled" {
					return nil, fmt.Errorf("Muse compaction %s: %s", p.Item.Outcome, p.Item.Reason)
				}
			}
			gi := &llmtypes.GenerationInfo{Additional: map[string]interface{}{"provider": "muse-cli", "muse_cli_mode": "msp", "muse_session_id": s.sessionID}}
			llmtypes.AttachIdleCompactResult(gi, result)
			llmtypes.AttachCodingProviderSessionHandle(gi, llmtypes.CodingProviderSessionHandle{Provider: "muse-cli", Transport: llmtypes.CodingProviderTransportMSP, NativeSessionID: s.sessionID, WorkingDir: s.workdir, Status: llmtypes.CodingProviderSessionStatusIdle})
			return &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{GenerationInfo: gi}}}, nil
		}
	}
}

// SendMuseMSPInput adds a message to the chat's running turn (turn/steer). The
// host's answer does not prove the turn will read it: runMuseMSPTurns checks
// that when the turn ends.
func SendMuseMSPInput(ctx context.Context, owner, message string) (err error) {
	s := museMSPSessionFor(owner)
	if s == nil {
		return fmt.Errorf("%w: no Muse serve session", llmtypes.ErrCodingAgentInputNotSent)
	}
	base := map[string]any{"input": []map[string]any{{"type": "text", "text": message}}}
	params, closeImages, err := museParamsWithImages(ctx, owner, base, nil)
	if err != nil {
		return fmt.Errorf("%w: image preparation did not complete", llmtypes.ErrCodingAgentInputNotSent)
	}
	defer closeImages()
	defer func() { finishMuseImageDelivery(owner, params, err) }()
	// Decode/access checks can outlive a turn: use only the current native ID.
	if museMSPSessionFor(owner) != s {
		return fmt.Errorf("%w: Muse session changed", llmtypes.ErrCodingAgentInputNotSent)
	}
	s.mu.Lock()
	turnID, sessionID := s.turnID, s.sessionID
	if turnID == "" {
		s.mu.Unlock()
		return fmt.Errorf("%w: no Muse turn is running", llmtypes.ErrCodingAgentInputNotSent)
	}
	select {
	case <-s.rpc.done:
		s.mu.Unlock()
		return fmt.Errorf("%w: Muse process ended", llmtypes.ErrCodingAgentInputNotSent)
	default:
	}
	steer := s.addSteerLocked(message)
	if imageParams, ok := params.(*museImageParams); ok {
		steer.images = append([]llmtypes.NativeImageInput(nil), imageParams.sources...)
	}
	s.mu.Unlock()
	base["commandId"] = steer.commandID
	base["sessionId"] = sessionID
	base["expectedTurnId"] = turnID
	err = s.rpc.call(ctx, "turn/steer", params, nil)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finishSteerLocked(steer, err == nil)
	if err != nil {
		return err
	}
	if s.steered == nil {
		s.steered = map[string]time.Time{}
	}
	s.steered[message] = time.Now()
	return nil
}

// InterruptMuseMSP stops the chat's running turn.
func InterruptMuseMSP(ctx context.Context, owner string) error {
	s := museMSPSessionFor(owner)
	if s == nil {
		return fmt.Errorf("no Muse serve session for %s", owner)
	}
	s.mu.Lock()
	turnID, sessionID := s.turnID, s.sessionID
	s.mu.Unlock()
	if turnID == "" {
		return nil
	}
	return s.rpc.call(ctx, "turn/interrupt", map[string]any{"commandId": museCommandID(), "sessionId": sessionID, "turnId": turnID}, nil)
}

// MuseMSPSessionActive reports whether a chat runs on `muse serve` now.
func MuseMSPSessionActive(owner string) bool { return museMSPSessionFor(owner) != nil }

// MuseMSPUsage is the chat's plan windows and context as its host last
// reported them; ok is false when the chat runs no serve session.
func MuseMSPUsage(owner string) (windows []llmtypes.RateLimitWindow, contextUsed, contextWindow int, ok bool) {
	s := museMSPSessionFor(owner)
	if s == nil {
		return nil, 0, 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.windowsLocked(), s.context.used, s.context.window, true
}

// windowsLocked turns the host's plan usage into the shared window shape.
func (s *museMSPSession) windowsLocked() []llmtypes.RateLimitWindow {
	u := s.usage
	if u == nil {
		return nil
	}
	var windows []llmtypes.RateLimitWindow
	if u.Window != nil {
		name := "five_hour"
		if m := u.Window.WindowDurationMins; m > 0 && m != 300 {
			name = fmt.Sprintf("%d_minute", m)
		}
		windows = append(windows, llmtypes.RateLimitWindow{Name: name, UsedPercent: float64(u.Window.UsedPercent), ResetsAt: time.UnixMilli(u.Window.ResetsAtMs).UTC()})
	}
	if u.Weekly != nil {
		windows = append(windows, llmtypes.RateLimitWindow{Name: "seven_day", UsedPercent: float64(u.Weekly.UsedPercent), ResetsAt: time.UnixMilli(u.Weekly.ResetsAtMs).UTC()})
	}
	return windows
}

func (s *museMSPSession) statusLine(model string, input, output, cacheRead, cacheWrite int) *llmtypes.StatusLine {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := &llmtypes.StatusLine{Provider: "muse-cli", Model: model, InputTokens: input, OutputTokens: output, CacheReadInputTokens: cacheRead, CacheCreationInputTokens: cacheWrite, Metadata: map[string]interface{}{"muse_session_id": s.sessionID}}
	if s.context.window > 0 {
		status.SetContextUsage(s.context.used, s.context.window)
	}
	if windows := s.windowsLocked(); len(windows) > 0 {
		status.Metadata[llmtypes.RateLimitWindowsMetaKey] = windows
	}
	return status
}

func museMSPUserChoice(opts *llmtypes.CallOptions) bool {
	if opts == nil || opts.Metadata == nil {
		return false
	}
	choice, _ := opts.Metadata.Custom[metadataMuseUserChoice].(bool)
	return choice
}

// listen hands the host's events to the caller until stopListening.
func (s *museMSPSession) listen() <-chan museRPCMessage {
	ch := make(chan museRPCMessage, 4096)
	s.mu.Lock()
	s.listener = ch
	s.mu.Unlock()
	return ch
}

func (s *museMSPSession) stopListening() {
	s.mu.Lock()
	s.listener = nil
	s.mu.Unlock()
}

// pumpMuseMSP reads every event the host sends for the life of the session.
// During one of the chat's own turns it passes them on; between turns, Muse
// can still run turns of its own (a Monitor waking it, a background task
// finishing): their approvals and questions are answered as a turn answers
// them (a question takes its first option, nobody is waiting), the usage stays
// current, and the reply is logged. Unanswered, such a request would hold that
// turn and every message queued behind it.
func (a *MuseCLIAdapter) pumpMuseMSP(s *museMSPSession) {
	for {
		select {
		case <-s.rpc.done:
			return
		case msg := <-s.rpc.events:
			if msg.Method == "view/gap" {
				// Muse dropped events of this session (its own delivery queue fell behind): read the missed
				// ones back and pass them on in order, so a turn does not lose its text, tools or its end.
				for _, missed := range a.museMSPGapEvents(s, msg) {
					if !a.dispatchMuseMSP(s, missed) {
						return
					}
				}
				continue
			}
			if !a.dispatchMuseMSP(s, msg) {
				return
			}
		}
	}
}

// dispatchMuseMSP hands one host event to the running turn, or handles it
// between turns. false: the host has gone.
func (a *MuseCLIAdapter) dispatchMuseMSP(s *museMSPSession, msg museRPCMessage) bool {
	observeMuseMSPSubagent(s, msg)
	s.mu.Lock()
	listener := s.listener
	s.mu.Unlock()
	if listener != nil {
		// Never drop an event of a running turn: its turn/completed may be the one.
		select {
		case listener <- msg:
		case <-s.rpc.done:
			return false
		}
		return true
	}
	a.handleIdleMuseMSP(s, msg)
	return true
}

// museMSPGapPages bounds one recovery read (1000 events a page).
const museMSPGapPages = 20

// museMSPGapEvents reads back the events a `view/gap` names: those after
// `after` and before `next` (cursors are opaque, so the page stops at `next`).
func (a *MuseCLIAdapter) museMSPGapEvents(s *museMSPSession, gap museRPCMessage) []museRPCMessage {
	var p struct {
		SessionID string `json:"sessionId"`
		After     string `json:"after"`
		Next      string `json:"next"`
	}
	s.mu.Lock()
	sessionID := s.sessionID
	s.mu.Unlock()
	if json.Unmarshal(gap.Params, &p) != nil || p.After == "" || p.Next == "" || (p.SessionID != "" && p.SessionID != sessionID) {
		return nil
	}
	if p.SessionID == "" {
		p.SessionID = sessionID
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var missed []museRPCMessage
	cursor := p.After
	for page := 0; page < museMSPGapPages; page++ {
		var result struct {
			Events     []museRPCMessage `json:"events"`
			NextCursor *string          `json:"nextCursor"`
		}
		if err := s.rpc.call(ctx, "view/page", map[string]any{"sessionId": p.SessionID, "cursor": cursor, "direction": "forward", "limit": 1000}, &result); err != nil {
			if a.logger != nil {
				a.logger.Infof("[MUSE_MSP_GAP] session=%s action=read_failed recovered=%d error=%v", p.SessionID, len(missed), err)
			}
			return missed
		}
		for _, event := range result.Events {
			var at struct {
				ViewCursor string `json:"viewCursor"`
			}
			_ = json.Unmarshal(event.Params, &at)
			if at.ViewCursor == p.Next {
				if a.logger != nil {
					a.logger.Infof("[MUSE_MSP_GAP] session=%s action=recovered events=%d", p.SessionID, len(missed))
				}
				return missed
			}
			missed = append(missed, event)
		}
		if result.NextCursor == nil || *result.NextCursor == "" || len(result.Events) == 0 {
			break
		}
		cursor = *result.NextCursor
	}
	if a.logger != nil {
		a.logger.Infof("[MUSE_MSP_GAP] session=%s action=recovered_partial events=%d", p.SessionID, len(missed))
	}
	return missed
}

func (a *MuseCLIAdapter) handleIdleMuseMSP(s *museMSPSession, msg museRPCMessage) {
	if len(msg.ID) > 0 && msg.Method != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		a.answerMuseMSPRequest(ctx, s, nil, msg)
		cancel()
		return
	}
	switch msg.Method {
	case "session/goalChanged":
		observeMuseMSPGoal(s, msg)
	case "usage/changed":
		var p museMSPPlanUsage
		if json.Unmarshal(msg.Params, &p) == nil {
			s.mu.Lock()
			s.usage = &p
			s.mu.Unlock()
		}
	case "session/contextUsage":
		var p struct {
			UsedTokens   int `json:"usedTokens"`
			WindowTokens int `json:"windowTokens"`
		}
		if json.Unmarshal(msg.Params, &p) == nil {
			s.mu.Lock()
			s.context = museMSPContext{used: p.UsedTokens, window: p.WindowTokens}
			s.mu.Unlock()
		}
	case "turn/started", "turn/completed":
		var p struct {
			TurnID   string `json:"turnId"`
			Terminal string `json:"terminal"`
		}
		if json.Unmarshal(msg.Params, &p) != nil || strings.TrimSpace(p.TurnID) == "" {
			return
		}
		s.mu.Lock()
		foreground := p.TurnID == s.turnID || p.TurnID == s.lastForegroundTurnID
		if foreground {
			delete(s.wake, p.TurnID)
		}
		s.mu.Unlock()
		if foreground {
			return
		}
		log.Printf("[MUSE_MSP] session=%s own turn %s %s %s (between chat turns)", s.sessionID, p.TurnID, msg.Method, p.Terminal)
		if msg.Method == "turn/started" {
			s.mu.Lock()
			if s.wake == nil {
				s.wake = map[string]*museMSPWake{}
			}
			if s.wake[p.TurnID] == nil {
				s.wake[p.TurnID] = &museMSPWake{}
			}
			s.mu.Unlock()
		} else {
			s.mu.Lock()
			wake := s.wake[p.TurnID]
			delete(s.wake, p.TurnID)
			s.mu.Unlock()
			if wake != nil {
				if handler := museMSPReplyHandler.Load(); handler != nil {
					(*handler)(MuseMSPReply{Owner: s.owner, NativeSessionID: s.sessionID, TurnID: p.TurnID, Terminal: p.Terminal, Text: strings.Join(wake.texts, "\n\n"), Sources: wake.sources})
				}
			}
		}
	case "item/completed":
		var p struct {
			Item museMSPItem `json:"item"`
		}
		if json.Unmarshal(msg.Params, &p) == nil && p.Item.Kind == "agentMessage" && strings.TrimSpace(p.Item.Text) != "" {
			log.Printf("[MUSE_MSP] session=%s reply between chat turns (turn %s): %d chars", s.sessionID, p.Item.TurnID, len(p.Item.Text))
			s.mu.Lock()
			if p.Item.TurnID == s.turnID || p.Item.TurnID == s.lastForegroundTurnID {
				s.mu.Unlock()
				return
			}
			if s.wake == nil {
				s.wake = map[string]*museMSPWake{}
			}
			wake := s.wake[p.Item.TurnID]
			if wake == nil {
				wake = &museMSPWake{}
				s.wake[p.Item.TurnID] = wake
			}
			wake.texts = append(wake.texts, strings.TrimSpace(p.Item.Text))
			wake.sources = append(wake.sources, p.Item.WakeSourceDescriptions...)
			s.mu.Unlock()
		}
	}
}

// MuseMSPReply is a reply Muse wrote between the chat's turns: a turn it ran
// on its own, usually because a Monitor fired. Text may be empty; the completed
// turn still needs host handoff. Sources names those Monitors.
type MuseMSPReply struct {
	Owner           string
	NativeSessionID string
	TurnID          string
	// Terminal preserves cancellation/failure so the host does not continue a stopped wake.
	Terminal string
	Text     string
	Sources  []string
}

type museMSPWake struct {
	texts   []string
	sources []string
}

var museMSPReplyHandler atomic.Pointer[func(MuseMSPReply)]

// SetMuseMSPReplyHandler gives the host the replies Muse writes between the
// chat's turns, so they reach the chat like any reply.
func SetMuseMSPReplyHandler(handler func(MuseMSPReply)) {
	if handler == nil {
		museMSPReplyHandler.Store(nil)
		return
	}
	museMSPReplyHandler.Store(&handler)
}
