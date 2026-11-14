package codexcli

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/nativeinput"
)

// codexSteerFollowupWait is how long a finished turn waits for Codex to start
// the next turn for a steer the finished turn did not consume.
var codexSteerFollowupWait = 3 * time.Second

func (c *CodexCLIAdapter) consumeAppTurn(ctx context.Context, s *codexAppSession, r *codexRPC, thread, turn string, emit func(llmtypes.StreamChunk)) (response *llmtypes.ContentResponse, err error) {
	var questions nativeinput.Requests
	defer questions.Close()
	completed := false
	defer func() {
		if err != nil || !completed || response == nil {
			return
		}
		if codexGoalActive(s) {
			llmtypes.NextPromptFor(ctx, "codex-cli", s.owner).Finish(llmtypes.NextPromptResult{Reason: "native_goal_running"})
			return
		}
		s.mu.Lock()
		nativeTurn := s.turnID
		s.mu.Unlock()
		sampleCodexNextPrompt(ctx, s, r, thread, nativeTurn)
	}()
	// Protocol/read errors must not leave an unobserved native turn running.
	defer func() {
		if err != nil && !completed {
			r.close()
		}
	}()
	var final string
	var usage, baseline codexAppUsage
	baselineSet := false
	// Compaction counters belong to this native item, not cumulative billing.
	var compact *llmtypes.ContextCompaction
	lastContext := 0
	s.mu.Lock()
	if s.status != nil {
		lastContext, _ = s.status.Metadata[llmtypes.ContextUsedTokensMetaKey].(int)
	}
	s.mu.Unlock()
	toolStarts := map[string]time.Time{}
	streamed := map[string]bool{}
	seenUserItems := map[string]bool{}
	blocksStarted := map[string]bool{}
	blocksCompleted := map[string]bool{}
	blockID := func(id string) string {
		if id == "" {
			return ""
		}
		return "codex:" + thread + ":" + turn + ":" + id
	}
	startBlock := func(id string) {
		id = blockID(id)
		if id == "" || blocksStarted[id] {
			return
		}
		blocksStarted[id] = true
		emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeAssistantBlock, Metadata: map[string]any{llmtypes.AssistantBlockIDMetadataKey: id, llmtypes.AssistantBlockPhaseMetadataKey: "start"}})
	}
	lastContentItem := ""
	trailingNewlines := 0
	emitContent := func(id, text string, metadata map[string]any) {
		if text == "" {
			return
		}
		metadata[llmtypes.ContentDeltaMetadataKey] = true
		metadata["codex_item_id"] = id
		startBlock(id)
		metadata[llmtypes.AssistantBlockIDMetadataKey] = blockID(id)
		// Native messages are separate paragraphs. Token deltas within one
		// message must stay byte-exact (PLAT-872-19 agentic streaming failure).
		if lastContentItem != "" && lastContentItem != id && trailingNewlines < 2 {
			emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: strings.Repeat("\n", 2-trailingNewlines), Metadata: metadata})
		}
		lastContentItem = id
		streamed[id] = true
		emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: text, Metadata: metadata})
		for _, ch := range text {
			if ch == '\n' {
				trailingNewlines++
			} else {
				trailingNewlines = 0
			}
		}
	}
	// A steer Codex accepted after the turn's last model step is not lost: it
	// runs as the next turn on the same thread. Keep this call (and so the
	// session's turn id, Stop and queue) on that turn until it ends.
	var followupC <-chan time.Time
	waitingForGoal := false
	var finishedResponse *llmtypes.ContentResponse
	// Replies of the turns already finished in this call; the returned text keeps them all.
	var carried string
	joined := func() string {
		if carried == "" {
			return final
		}
		if final == "" {
			return carried
		}
		return carried + "\n\n" + final
	}
	s.mu.Lock()
	if s.goalStateChanged == nil {
		s.goalStateChanged = make(chan struct{}, 1)
	}
	goalStateChanged := s.goalStateChanged
	s.mu.Unlock()

	for {
		select {
		case <-goalStateChanged:
			// A confirmed control RPC may race ahead of (or after) its native
			// notification. Once the turn ended, observe that pause immediately.
			if waitingForGoal && !codexGoalActive(s) {
				s.mu.Lock()
				status := "cleared"
				if s.goal != nil {
					status = s.goal.Status
				}
				s.mu.Unlock()
				log.Printf("[AGENT_GOAL] session=%s native=%s action=done status=%s reason=confirmed_control_observed", s.owner, thread, status)
				completed = true
				return codexAppResponse(s, joined(), usage), nil
			}

		case <-followupC:
			if waitingForGoal {
				pauseCtx, pauseCancel := context.WithTimeout(ctx, 10*time.Second)
				_, pauseErr := setCodexGoalStatus(pauseCtx, s, r, thread, "paused", emit, "native_continuation_not_started")
				pauseCancel()
				if pauseErr != nil {
					return nil, pauseErr
				}
				// A native turn can start while the pause RPC is in flight.
				// End this process before releasing ownership; the paused
				// native thread can reopen normally on the next input.
				r.close()
				log.Printf("[AGENT_GOAL] session=%s native=%s action=done status=paused reason=native_gap_process_closed", s.owner, thread)
				completed = true
				return codexAppResponse(s, joined(), usage), nil
			}
			completed = true
			if c.logger != nil {
				c.logger.Infof("codex app-server steered input: no follow-up turn within %s thread=%s turn=%s", codexSteerFollowupWait, thread, turn)
			}
			return finishedResponse, nil
		case <-ctx.Done():
			// Do not let cancellation leave an unobserved turn executing. The persisted
			// native thread can be resumed even when the process needs termination.
			cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if codexGoalActive(s) {
				_, _ = setCodexGoalStatus(cancelCtx, s, r, thread, "paused", emit, "turn_canceled")
			}
			_ = r.call(cancelCtx, "turn/interrupt", map[string]any{"threadId": thread, "turnId": turn}, nil)
			cancel()
			r.close()
			return nil, ctx.Err()
		case <-r.done:
			return nil, r.failure()
		case msg := <-r.events:
			if len(msg.ID) > 0 {
				questions.Start(ctx, string(msg.ID), func(questionCtx context.Context) {
					result, requestErr := codexNativeQuestion(questionCtx, msg.Method, msg.Params, thread, turn)
					reply := map[string]any{"id": msg.ID, "result": result}
					if requestErr != nil {
						delete(reply, "result")
						reply["error"] = map[string]any{"code": -32602, "message": requestErr.Error()}
					}
					if questionCtx.Err() == nil {
						if err := r.write(reply); err != nil {
							r.finish(err)
						}
					}
				})
				continue
			}
			if msg.Method == "serverRequest/resolved" {
				var resolved struct {
					ThreadID  string          `json:"threadId"`
					RequestID json.RawMessage `json:"requestId"`
				}
				if json.Unmarshal(msg.Params, &resolved) == nil && resolved.ThreadID == thread {
					questions.Cancel(string(resolved.RequestID))
				}
				continue
			}

			if handled, goalErr := handleCodexGoalNotification(s, msg.Method, msg.Params, thread, emit); handled {
				if goalErr != nil {
					return nil, goalErr
				}
				if waitingForGoal && !codexGoalActive(s) {
					completed = true
					return codexAppResponse(s, joined(), usage), nil
				}
				continue
			}

			if msg.Method == "account/rateLimits/updated" {
				var payload struct {
					RateLimits json.RawMessage `json:"rateLimits"`
				}
				if json.Unmarshal(msg.Params, &payload) == nil {
					s.mu.Lock()
					s.rateLimits = codexAppRateLimits(payload.RateLimits)
					s.mu.Unlock()
				}
				continue
			}
			var event codexAppEvent
			if err := json.Unmarshal(msg.Params, &event); err != nil {
				return nil, fmt.Errorf("decode codex %s: %w", msg.Method, err)
			}
			if c.logger != nil && (msg.Method == "turn/started" || msg.Method == "turn/completed") {
				c.logger.Debugf("codex app-server event method=%s thread=%s turn=%s nested_turn=%s", msg.Method, event.ThreadID, event.TurnID, event.Turn.ID)
			}
			if event.ThreadID != thread {
				continue
			}
			if followupC != nil && msg.Method == "turn/started" {
				next := event.Turn.ID
				if next == "" {
					next = event.TurnID
				}
				if next != "" && next != turn {
					if waitingForGoal {
						log.Printf("[AGENT_GOAL] session=%s native=%s turn=%s action=started status=active reason=native_continuation", s.owner, thread, next)
					}
					carried = joined()
					compact = nil
					turn, followupC, finishedResponse, final = next, nil, nil, ""
					waitingForGoal = false
					s.mu.Lock()
					if s.turnID != next {
						s.promptEpoch++
					}
					s.turnID = next
					s.mu.Unlock()
					emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypePromptAccepted, Metadata: map[string]any{"codex_thread_id": thread, "codex_turn_id": next}})
					if c.logger != nil {
						c.logger.Infof("codex app-server steered input runs as its own turn thread=%s turn=%s", thread, next)
					}
					continue
				}
			}
			if msg.Method == "turn/completed" {
				event.TurnID = event.Turn.ID
			}
			if event.TurnID != turn {
				if msg.Method == "turn/completed" && c.logger != nil {
					c.logger.Infof("[CODEX_TURN] owner=%s thread=%s turn=%s event_turn=%s action=ignored reason=other_turn", s.owner, thread, turn, event.TurnID)
				}
				continue
			}
			metadata := map[string]any{"codex_thread_id": thread, "codex_turn_id": turn, "codex_stream_source": "app-server"}
			switch msg.Method {
			case "turn/plan/updated":
				if plan, ok := llmtypes.ParseCodingAgentPlan(msg.Params, "plan"); ok {
					plan.Provider, plan.NativeTurnID, plan.PlanID = "codex-cli", turn, thread+":"+turn
					emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeCodingAgentPlan, CodingAgentPlan: plan, Metadata: metadata})
				}
			case "item/agentMessage/delta":
				emitContent(event.ItemID, event.Delta, metadata)
			case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
				emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeReasoning, Content: event.Delta, Metadata: metadata})
			case "thread/tokenUsage/updated":
				current := event.TokenUsage.Total
				// Codex reports the installed compacted context as zero-input last usage
				// with totalTokens carrying the new context size (same as rollout reader).
				if compact != nil && compact.TokensAfter == 0 && event.TokenUsage.Last.Input == 0 && event.TokenUsage.Last.Total > 0 {
					compact.TokensAfter = event.TokenUsage.Last.Total
					if compact.Phase == llmtypes.ContextCompactionPhaseEnd {
						emit(llmtypes.ContextCompactionChunk(*compact))
					}
				}
				if event.TokenUsage.Last.Total > 0 {
					lastContext = event.TokenUsage.Last.Total
				}
				if !baselineSet {
					baseline = codexAppUsage{Input: current.Input - event.TokenUsage.Last.Input, Cached: current.Cached - event.TokenUsage.Last.Cached, CacheWrite: current.CacheWrite - event.TokenUsage.Last.CacheWrite, Output: current.Output - event.TokenUsage.Last.Output, Reasoning: current.Reasoning - event.TokenUsage.Last.Reasoning, Total: current.Total - event.TokenUsage.Last.Total}
					baselineSet = true
				}
				usage = codexAppUsage{Input: current.Input - baseline.Input, Cached: current.Cached - baseline.Cached, CacheWrite: current.CacheWrite - baseline.CacheWrite, Output: current.Output - baseline.Output, Reasoning: current.Reasoning - baseline.Reasoning, Total: current.Total - baseline.Total}
				s.mu.Lock()
				status := &llmtypes.StatusLine{Provider: "codex-cli", Model: s.model, InputTokens: usage.Input - usage.Cached, OutputTokens: usage.Output, CacheReadInputTokens: usage.Cached, CacheCreationInputTokens: usage.CacheWrite, TotalInputTokens: current.Input, TotalOutputTokens: current.Output, Metadata: metadata}
				if event.TokenUsage.Window > 0 {
					status.SetContextUsage(event.TokenUsage.Last.Total, event.TokenUsage.Window)
				}
				if windows := codexStructuredRateLimitWindows(s.rateLimits); len(windows) > 0 {
					status.Metadata[llmtypes.RateLimitWindowsMetaKey] = windows
				}
				s.status = status
				s.mu.Unlock()
				emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeStatusLine, StatusLine: status, Metadata: metadata})
			case "item/started", "item/completed":
				var item struct {
					ClientID string `json:"clientId"`
					ID       string `json:"id"`
					Type     string `json:"type"`
					Text     string `json:"text"`
					Phase    string `json:"phase"`
					Content  []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				}
				if err := json.Unmarshal(event.Item, &item); err != nil {
					return nil, err
				}
				if item.Type == "contextCompaction" {
					phase := llmtypes.ContextCompactionPhaseStart
					outcome := ""
					if msg.Method == "item/completed" {
						phase = llmtypes.ContextCompactionPhaseEnd
						outcome = llmtypes.ContextCompactionOutcomeSuccess
					}
					if compact == nil || compact.ID != item.ID || phase == llmtypes.ContextCompactionPhaseStart {
						compact = &llmtypes.ContextCompaction{Provider: "codex-cli", ID: item.ID, TokensBefore: lastContext}
					}
					compact.Phase, compact.Outcome = phase, outcome
					emit(llmtypes.ContextCompactionChunk(*compact))
				}
				if item.Type == "agentMessage" && msg.Method == "item/started" {
					startBlock(item.ID)
				}
				if item.Type == "agentMessage" && msg.Method == "item/completed" {
					if item.Phase == "final_answer" || item.Phase == "" {
						final = item.Text
					}
					if !streamed[item.ID] {
						emitContent(item.ID, item.Text, metadata)
					}
					id := blockID(item.ID)
					if id != "" && !blocksCompleted[id] {
						blocksCompleted[id] = true
						emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeAssistantBlock, Content: item.Text, Metadata: map[string]any{llmtypes.AssistantBlockIDMetadataKey: id, llmtypes.AssistantBlockPhaseMetadataKey: "end"}})
					}
				}
				if item.Type == "userMessage" && !seenUserItems[item.ID] {
					seenUserItems[item.ID] = true
					var texts []string
					for _, part := range item.Content {
						if part.Type == "text" {
							texts = append(texts, part.Text)
						}
					}
					text := strings.Join(texts, "\n")
					s.mu.Lock()
					for id, steered := range s.steers {
						if (item.ClientID != "" && id == item.ClientID) || (item.ClientID == "" && steered == text) {
							delete(s.steers, id)
							break
						}
					}
					for _, receipt := range s.receipts {
						if !receipt.confirmed && ((item.ClientID != "" && receipt.id == item.ClientID) || (item.ClientID == "" && receipt.text == text)) {
							receipt.confirmed = true
							close(receipt.durable)
							break
						}
					}
					s.mu.Unlock()
				}
				tool := codexAppToolItem(event.Item)
				if tool != nil {
					kind := llmtypes.StreamChunkTypeToolCallStart
					duration := time.Duration(0)
					if msg.Method == "item/started" {
						toolStarts[tool.ID] = time.Now()
					} else {
						kind = llmtypes.StreamChunkTypeToolCallEnd
						if started, ok := toolStarts[tool.ID]; ok {
							duration = time.Since(started)
							delete(toolStarts, tool.ID)
						}
					}
					emit(llmtypes.StreamChunk{Type: kind, Content: codexToolItemLabel(tool), ToolName: codexToolItemName(tool), ToolCallID: tool.ID, ToolArgs: codexToolItemArgs(tool), ToolResult: codexToolItemResult(tool), ToolDuration: duration, Metadata: metadata})
				}
			case "turn/completed":
				if event.Turn.Status == "completed" && codexGoalActive(s) {
					finishedResponse = codexAppResponse(s, joined(), usage)
					followup := time.NewTimer(codexGoalFollowupWait)
					defer followup.Stop()
					followupC = followup.C
					waitingForGoal = true
					continue
				}
				if event.Turn.Status == "completed" {
					s.mu.Lock()
					steered := len(s.steers) > 0
					s.mu.Unlock()
					if steered && followupC == nil {
						finishedResponse = codexAppResponse(s, joined(), usage)
						followup := time.NewTimer(codexSteerFollowupWait)
						defer followup.Stop()
						followupC = followup.C
						continue
					}
				}
				completed = true
				switch event.Turn.Status {
				case "completed":
					return codexAppResponse(s, joined(), usage), nil
				case "interrupted":
					return nil, context.Canceled
				default:
					message := "Codex turn failed"
					if event.Turn.Error != nil {
						message = event.Turn.Error.Message
					}
					info := ""
					if event.Turn.Error != nil {
						info = codexAppErrorInfo(event.Turn.Error.Info)
					}
					return nil, &CodexTurnError{Message: message, Info: info}
				}
			}
		}
	}
}

// Normalize protocol camelCase into the existing semantic tool representation.
func codexAppToolItem(raw json.RawMessage) *codexExecItem {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	var kind string
	_ = json.Unmarshal(fields["type"], &kind)
	types := map[string]string{"commandExecution": "command_execution", "mcpToolCall": "mcp_tool_call", "webSearch": "web_search", "fileChange": "file_change", "collabAgentToolCall": "collab_tool_call"}
	converted, ok := types[kind]
	if !ok {
		return nil
	}
	fields["type"], _ = json.Marshal(converted)
	for camel, snake := range map[string]string{"aggregatedOutput": "aggregated_output", "exitCode": "exit_code", "receiverThreadIds": "receiver_thread_ids", "senderThreadId": "sender_thread_id", "agentsStates": "agents_states"} {
		if v, ok := fields[camel]; ok {
			fields[snake] = v
		}
	}
	data, _ := json.Marshal(fields)
	var item codexExecItem
	if json.Unmarshal(data, &item) != nil {
		return nil
	}
	return &item
}

func awaitCodexAppInputDurable(ctx context.Context, owner, message string, timeout time.Duration) (CodexDurableAck, error) {
	s := codexAppSessionForOwner(owner)
	if s == nil {
		return CodexDurableAck{}, fmt.Errorf("no Codex app-server session")
	}
	s.mu.Lock()
	var receipt *codexAppReceipt
	for i, v := range s.receipts {
		if v.text == message {
			receipt = v
			_ = i
			break
		}
	}
	r := s.rpc
	thread := s.threadID
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, v := range s.receipts {
			if v == receipt {
				s.receipts = append(s.receipts[:i], s.receipts[i+1:]...)
				break
			}
		}
	}()
	if receipt == nil {
		return CodexDurableAck{}, fmt.Errorf("no Codex app-server send receipt")
	}
	if timeout <= 0 {
		timeout = codexDurableAckBudget()
	}
	wait, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case <-receipt.durable:
		return CodexDurableAck{Outcome: CodexDurableAckConfirmed, Latency: time.Since(receipt.accepted), ProofPath: "app-server/thread/" + thread, RowTimestamp: receipt.accepted}, nil
	case <-wait.Done():
		return CodexDurableAck{Outcome: CodexDurableAckUnflushed, Latency: time.Since(receipt.accepted)}, wait.Err()
	case <-r.done:
		return CodexDurableAck{}, r.failure()
	}
}

func codexAppRateLimits(raw json.RawMessage) *codexRateLimits {
	var v struct {
		Primary, Secondary *struct {
			UsedPercent   float64 `json:"usedPercent"`
			WindowMinutes int     `json:"windowDurationMins"`
			ResetsAt      int64   `json:"resetsAt"`
		}
		PlanType string `json:"planType"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	r := &codexRateLimits{PlanType: v.PlanType}
	if v.Primary != nil {
		r.Primary = &codexRateLimitWindow{UsedPercent: v.Primary.UsedPercent, WindowMinutes: v.Primary.WindowMinutes, ResetsAt: v.Primary.ResetsAt}
	}
	if v.Secondary != nil {
		r.Secondary = &codexRateLimitWindow{UsedPercent: v.Secondary.UsedPercent, WindowMinutes: v.Secondary.WindowMinutes, ResetsAt: v.Secondary.ResetsAt}
	}
	return r
}

// compactAppThread runs Codex's own compaction on the resumed thread (idle
// compaction, PLAT-872): thread/compact/start starts it as a native turn, which
// is then followed like any turn (its contextCompaction item, token usage and
// turn/completed). No person message is sent.
func (c *CodexCLIAdapter) compactAppThread(ctx context.Context, s *codexAppSession, r *codexRPC, thread string, emit func(llmtypes.StreamChunk)) (*llmtypes.ContentResponse, error) {
	s.mu.Lock()
	s.compacting = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.compacting = false; s.mu.Unlock() }()
	if err := r.call(ctx, "thread/compact/start", map[string]any{"threadId": thread}, nil); err != nil {
		r.close()
		return nil, err
	}
	turn := ""
	for turn == "" {
		select {
		case <-ctx.Done():
			r.close()
			return nil, ctx.Err()
		case <-r.done:
			return nil, r.failure()
		case msg := <-r.events:
			if msg.Method != "turn/started" {
				continue
			}
			var event codexAppEvent
			if json.Unmarshal(msg.Params, &event) != nil || event.ThreadID != thread {
				continue
			}
			turn = event.Turn.ID
			if turn == "" {
				turn = event.TurnID
			}
		}
	}
	s.mu.Lock()
	if s.turnID != turn {
		s.promptEpoch++
	}
	s.turnID = turn
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.turnID = ""; s.mu.Unlock() }()
	if c.logger != nil {
		c.logger.Infof("codex app-server idle compaction started thread=%s turn=%s", thread, turn)
	}
	compacted := false
	var before, after int
	resp, err := c.consumeAppTurn(ctx, s, r, thread, turn, func(chunk llmtypes.StreamChunk) {
		if chunk.Type == llmtypes.StreamChunkTypeContextCompaction && chunk.ContextCompaction != nil {
			compaction := *chunk.ContextCompaction
			compaction.Trigger = "idle"
			chunk.ContextCompaction = &compaction
			if compaction.Phase == llmtypes.ContextCompactionPhaseEnd {
				compacted = true
				before, after = compaction.TokensBefore, compaction.TokensAfter
			}
		}
		emit(chunk)
	})
	if err != nil {
		return nil, err
	}
	result := llmtypes.IdleCompactResult{Compacted: compacted, TokensBefore: before, TokensAfter: after, Reason: llmtypes.IdleCompactReasonCompacted}
	if !compacted {
		result.Reason = llmtypes.IdleCompactReasonNothing
	}
	if len(resp.Choices) > 0 && resp.Choices[0].GenerationInfo != nil {
		info := resp.Choices[0].GenerationInfo
		result.CacheReadTokens, _ = info.Additional["cache_read_input_tokens"].(int)
		result.CacheWriteTokens, _ = info.Additional["cache_creation_input_tokens"].(int)
		llmtypes.AttachIdleCompactResult(info, result)
	}
	return resp, nil
}
