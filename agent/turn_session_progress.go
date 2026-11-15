package mcpagent

import (
	"context"
	"strings"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Retained sends bypass GenerateContent and its stream channel. Publish their
// committed narration through the same transcript events used by normal turns.
// The final-response reader remains the only authority for completion.
type retainedNativeTool struct {
	args        string
	name        string
	startedAt   time.Time
	bridgeOwned bool
}

func (s *Session) emitRetainedProgress(lifecycle *canonicalTurnLifecycle, seq uint64, provider llm.Provider, reader func(llm.Provider, string) []llmtypes.MessageContent, chunkIndex *int, toolMaps ...map[string]retainedNativeTool) {
	tools := map[string]retainedNativeTool{}
	if len(toolMaps) > 0 {
		tools = toolMaps[0]
	}
	// Check and read under stateMu, never sendMu. Send holds sendMu while a
	// busy CLI has not yet taken a steered message (Claude queues it until the
	// running tool returns), so waiting on it held narration written before a
	// long tool call until that tool finished. stateMu still orders the read
	// against the watcher replacement in startRetainedCompletionWatch: the
	// provider cursor is keyed by turn start, so a stale watcher must not read
	// after a new one exists. Emit after unlocking; listeners may re-enter.
	s.stateMu.Lock()
	current := !s.closed && s.retainedActive && s.retainedSeq == seq
	var messages []llmtypes.MessageContent
	if current {
		messages = reader(provider, s.agent.sessionID)
	}
	s.stateMu.Unlock()
	for _, message := range messages {
		if message.Role != llmtypes.ChatMessageTypeAI && message.Role != llmtypes.ChatMessageTypeTool {
			continue
		}
		for _, part := range message.Parts {
			var content string
			switch text := part.(type) {
			case llmtypes.ToolCall:
				if text.FunctionCall != nil {
					bridgeOwned := s.retainedBridgeOwnsTool(text.FunctionCall.Name)
					tools[text.ID] = retainedNativeTool{name: text.FunctionCall.Name, args: text.FunctionCall.Arguments, startedAt: time.Now(), bridgeOwned: bridgeOwned}
					if bridgeOwned {
						continue
					}
					event := events.NewToolCallStartEvent(0, text.FunctionCall.Name, events.ToolParams{Arguments: text.FunctionCall.Arguments}, "native", text.ID)
					event.ToolCallID = text.ID
					s.agent.emitTypedEvent(withCanonicalTurnLifecycle(context.Background(), lifecycle), event)
				}
				continue
			case llmtypes.ToolCallResponse:
				tool, known := tools[text.ToolCallID]
				name := text.Name
				if name == "" {
					name = tool.name
				}
				if name == "" {
					continue
				} // A result without its invocation cannot be paired.
				if tool.bridgeOwned || s.retainedBridgeOwnsTool(name) {
					delete(tools, text.ToolCallID)
					continue
				}
				var duration time.Duration
				if known {
					duration = time.Since(tool.startedAt)
					delete(tools, text.ToolCallID)
				}
				if text.IsError {
					event := events.NewToolCallErrorEvent(0, name, text.Content, "native", duration)
					event.ToolCallID = text.ToolCallID
					s.agent.emitTypedEvent(withCanonicalTurnLifecycle(context.Background(), lifecycle), event)
					continue
				}
				if plan, ok := (&streamingManager{}).successfulToolPlan(withCanonicalTurnLifecycle(context.Background(), lifecycle), name, tool.args, text.Content); ok {
					plan.Provider = string(provider)
					(&streamingManager{}).emitCodingAgentPlan(withCanonicalTurnLifecycle(context.Background(), lifecycle), s.agent, plan)
				}
				event := events.NewToolCallEndEvent(0, name, text.Content, "native", duration, text.ToolCallID)
				event.ToolCallID = text.ToolCallID
				s.agent.emitTypedEvent(withCanonicalTurnLifecycle(context.Background(), lifecycle), event)
				continue
			case llmtypes.TextContent:
				content = text.Text
			case *llmtypes.TextContent:
				if text != nil {
					content = text.Text
				}
			case llmtypes.ThinkingContent:
				s.emitRetainedThinking(lifecycle, text.Thinking)
				continue
			case *llmtypes.ThinkingContent:
				if text != nil {
					s.emitRetainedThinking(lifecycle, text.Thinking)
				}
				continue
			}
			content = strings.TrimSpace(content)
			if content == "" {
				continue
			}
			*chunkIndex++
			s.agent.emitTypedEvent(withCanonicalTurnLifecycle(context.Background(), lifecycle), &events.StreamingChunkEvent{
				BaseEventData: events.BaseEventData{Timestamp: time.Now()},
				Content:       content,
				ChunkIndex:    *chunkIndex,
				Source:        "transcript",
			})
		}
	}
}

// During a retained turn the observed bridge executor owns direct tools'
// arguments, result and duration. Their transcript echo must not emit a second
// receipt. Native CLI tools and unobserved MCP tools still need transcript events.
func (s *Session) retainedBridgeOwnsTool(name string) bool {
	if !s.agent.directToolExecutionEvents {
		return false
	}
	name = strings.TrimPrefix(name, "mcp__api-bridge__")
	name = strings.TrimPrefix(name, "mcp__api_bridge__")
	_, direct := s.agent.lookupDirectTool(name)
	return direct
}

// emitRetainedThinking sends thinking as the same event the live stream uses
// for reasoning (Cursor, Pi), so the UI folds it as thinking and bots never
// forward it as a reply.
func (s *Session) emitRetainedThinking(lifecycle *canonicalTurnLifecycle, thinking string) {
	thinking = strings.TrimSpace(thinking)
	if thinking == "" {
		return
	}
	s.agent.emitTypedEvent(withCanonicalTurnLifecycle(context.Background(), lifecycle), &events.ConversationThinkingEvent{
		BaseEventData: events.BaseEventData{Timestamp: time.Now()},
		Thinking:      thinking,
	})
}
