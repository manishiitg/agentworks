package picli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/nativeinput"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/toolclock"
)

var errPiRPCAborted = errors.New("Pi turn aborted")

type piRPCUsage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	Total      int `json:"totalTokens"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
	Cost       struct {
		Input      float64 `json:"input"`
		Output     float64 `json:"output"`
		Total      float64 `json:"total"`
		CacheRead  float64 `json:"cacheRead"`
		CacheWrite float64 `json:"cacheWrite"`
	} `json:"cost"`
}
type piRPCNativeMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	Timestamp    int64           `json:"timestamp"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
	ToolCallID   string          `json:"toolCallId"`
	ToolName     string          `json:"toolName"`
	IsError      bool            `json:"isError"`
	Usage        *piRPCUsage     `json:"usage"`
}

func piRPCParts(raw json.RawMessage) []piTranscriptContent {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []piTranscriptContent{{Type: "text", Text: text}}
	}
	var parts []piTranscriptContent
	_ = json.Unmarshal(raw, &parts)
	return parts
}
func (u *piRPCUsage) add(v *piRPCUsage) {
	if v == nil {
		return
	}
	u.Input += v.Input
	u.Output += v.Output
	total := v.Total
	if total == 0 {
		total = v.Input + v.Output + v.CacheRead + v.CacheWrite
	}
	u.Total += total
	u.CacheRead += v.CacheRead
	u.CacheWrite += v.CacheWrite
	u.Cost.Input += v.Cost.Input
	u.Cost.Output += v.Cost.Output
	u.Cost.Total += v.Cost.Total
	u.Cost.CacheRead += v.Cost.CacheRead
	u.Cost.CacheWrite += v.Cost.CacheWrite
}

type piRPCAccepted struct {
	Disposition string `json:"disposition"`
	Err         error  `json:"-"`
}

func (p *PiCLIAdapter) consumeRPCTurn(ctx context.Context, s *piRPCSession, r *piRPC, emit func(llmtypes.StreamChunk), accepted <-chan piRPCAccepted) (*llmtypes.ContentResponse, error) {
	var questions nativeinput.Requests
	defer questions.Close()
	var usage piRPCUsage
	var messages []llmtypes.MessageContent
	var compaction piCompactionTracker
	var content strings.Builder
	final, stopReason, modelError := "", "", ""
	calls := map[string]piStructuredToolCall{}
	started := map[string]time.Time{}
	lastEvent := time.Now()
	poll := piStallPollInterval
	if threshold := piWedgeSilenceThreshold() / 2; threshold < poll {
		poll = threshold
	}
	if poll <= 0 {
		poll = time.Millisecond
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	streamedMessage := false
	emitText := func(text string) {
		if text == "" {
			return
		}
		metadata := piRPCMetadata(s)
		metadata[llmtypes.ContentDeltaMetadataKey] = true
		emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: text, Metadata: metadata})
	}
	for {
		select {
		case reply := <-accepted:
			if reply.Err != nil {
				return nil, piRPCRequestFailure(s.model, s.nativeID, reply.Err, false)
			}
			emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypePromptAccepted, Metadata: piRPCMetadata(s)})
			if reply.Disposition == "handled" {
				return piRPCResponse(s, "", nil, nil), nil
			}
		case <-ctx.Done():
			r.close()
			return nil, ctx.Err()
		case <-r.done:
			return nil, r.failure()
		case <-ticker.C:
			if questions.Waiting() {
				lastEvent = time.Now()
				continue
			}
			if len(calls) == 0 && time.Since(lastEvent) >= piWedgeSilenceThreshold() {
				r.close()
				return nil, fmt.Errorf("Pi RPC stalled without an active tool (PLAT-153)")
			}
		case record := <-r.events:
			lastEvent = time.Now()
			event := record.piJSONEvent
			switch record.Type {
			case "auto_retry_start", "auto_retry_end":
				var retry struct {
					Attempt     int    `json:"attempt"`
					MaxAttempts int    `json:"maxAttempts"`
					DelayMS     int64  `json:"delayMs"`
					Success     bool   `json:"success"`
					FinalError  string `json:"finalError"`
				}
				if json.Unmarshal(record.Raw, &retry) != nil {
					continue
				}
				phase := "start"
				if event.Type == "auto_retry_end" {
					phase = "end"
				}
				if retry.Attempt < 1 {
					retry.Attempt = 1
				}
				if retry.MaxAttempts < retry.Attempt {
					retry.MaxAttempts = retry.Attempt
				}
				if retry.DelayMS < 0 {
					retry.DelayMS = 0
				}
				metadata := piRPCMetadata(s)
				native := &llmtypes.NativeRetry{Provider: "pi-cli", NativeSessionID: s.nativeID, Phase: phase, Attempt: retry.Attempt, MaxAttempts: retry.MaxAttempts, DelayMS: retry.DelayMS, Success: retry.Success}
				native.NativeTurnID, _ = metadata["pi_turn_id"].(string)
				emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeNativeRetry, NativeRetry: native, Metadata: metadata})
				log.Printf("[PI_RETRY] session=%s turn=%s phase=%s attempt=%d max=%d delay_ms=%d success=%t", native.NativeSessionID, native.NativeTurnID, phase, retry.Attempt, retry.MaxAttempts, retry.DelayMS, retry.Success)
				if phase == "end" {
					if retry.Success {
						if stopReason == "error" {
							stopReason = ""
						}
						modelError = ""
					} else {
						stopReason = "error"
						modelError = retry.FinalError
						if modelError == "" {
							modelError = "Pi could not recover after retrying the provider request."
						}
					}
				}
			case "extension_ui_request":
				var request struct {
					Method string `json:"method"`
				}
				_ = json.Unmarshal(record.Raw, &request)
				switch request.Method {
				case "select", "confirm", "input", "editor":
					questions.Start(ctx, record.ID, func(questionCtx context.Context) {
						response, err := piNativeQuestion(questionCtx, record.Raw)
						if err == nil && questionCtx.Err() == nil {
							if err = r.write(response); err != nil {
								r.finish(err)
							}
						}
					})
				}

			case "message_start":
				var envelope struct {
					Message piRPCNativeMessage `json:"message"`
				}
				_ = json.Unmarshal(record.Raw, &envelope)
				if envelope.Message.Role == "assistant" {
					content.Reset()
					streamedMessage = false
				}
			case "message_update":
				if event.AssistantMessageEvt == nil {
					continue
				}
				e := event.AssistantMessageEvt
				if e.Type == "thinking_delta" {
					metadata := piRPCMetadata(s)
					metadata[llmtypes.ContentDeltaMetadataKey] = true
					emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeReasoning, Content: e.Delta, Metadata: metadata})
				} else if delta := piApplyAssistantEvent(&content, e); delta != "" {
					if !streamedMessage && len(messages) > 0 {
						emitText("\n\n")
					}
					streamedMessage = true
					emitText(delta)
				}
			case "message_end":
				var envelope struct {
					Message piRPCNativeMessage `json:"message"`
				}
				if err := json.Unmarshal(record.Raw, &envelope); err != nil {
					return nil, err
				}
				m := envelope.Message
				parts := piRPCParts(m.Content)
				switch m.Role {
				case "user":
					text := piTranscriptText(parts)
					s.mu.Lock()
					for _, receipt := range s.receipts {
						if !receipt.confirmed && receipt.text == text && m.Timestamp >= receipt.at.UnixMilli() {
							receipt.timestamp = m.Timestamp
							receipt.occurrence = 1
							for _, prior := range s.receipts {
								if prior == receipt {
									break
								}
								if prior.confirmed && prior.text == text && prior.timestamp == m.Timestamp {
									receipt.occurrence++
								}
							}
							receipt.confirmed = true
							close(receipt.observed)
							break
						}
					}
					s.mu.Unlock()
				case "assistant":
					usage.add(m.Usage)
					stopReason = m.StopReason
					modelError = m.ErrorMessage
					final = piTranscriptText(parts)
					if !streamedMessage && final != "" {
						if len(messages) > 0 {
							emitText("\n\n")
						}
						emitText(final)
					}
					messages = append(messages, llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeAI, Parts: piTranscriptParts(parts)})
					status := &llmtypes.StatusLine{Provider: "pi-cli", Model: s.model, Metadata: piRPCMetadata(s)}
					status.InputTokens = usage.Input
					status.OutputTokens = usage.Output
					status.CacheReadInputTokens = usage.CacheRead
					status.CacheCreationInputTokens = usage.CacheWrite
					status.CostUSD = usage.Cost.Total
					s.mu.Lock()
					s.status = status
					s.mu.Unlock()
					emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeStatusLine, StatusLine: status})
				case "toolResult":
					messages = append(messages, llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: m.ToolCallID, Name: m.ToolName, Content: piTranscriptText(parts), IsError: m.IsError}}})
				}
			case "tool_execution_start":
				started[event.ToolCallID] = time.Now()
				chunk, call := piStructuredToolStartChunk(event)
				calls[event.ToolCallID] = call
				chunk.Metadata = piRPCMetadata(s)
				emit(chunk)
			case "tool_execution_end":
				chunk := piStructuredToolEndChunk(event, calls[event.ToolCallID], toolclock.Elapsed(started, event.ToolCallID))
				chunk.Metadata = piRPCMetadata(s)
				emit(chunk)
				delete(calls, event.ToolCallID)
			case "compaction_start", "compaction_end":
				if chunk, ok := compaction.fromStructuredEvent(event, time.Now()); ok {
					emit(chunk)
				}
			case "agent_settled":
				if event.Aborted || stopReason == "aborted" {
					return nil, errPiRPCAborted
				}
				if stopReason == "error" {
					return nil, piRPCRequestFailure(s.model, s.nativeID, fmt.Errorf("Pi model request failed: %s", modelError), true)
				}
				if len(calls) > 0 {
					return nil, fmt.Errorf("Pi settled with incomplete tool calls")
				}
				if strings.TrimSpace(final) == "" {
					return nil, fmt.Errorf("Pi settled without an assistant reply")
				}
				return piRPCResponse(s, final, &usage, messages), nil
			}
		}
	}
}
