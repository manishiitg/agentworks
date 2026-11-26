// tool_loop_detector.go
//
// This file contains the tool loop detection logic to prevent infinite loops
// when the LLM repeatedly calls the same tool with the same arguments and receives the same response.
//
// Exported:
//   - ToolLoopDetector
//   - NewToolLoopDetector
//   - CheckAndHandleLoop

package mcpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// ToolLoopDetector tracks recent tool calls to detect infinite loops
type ToolLoopDetector struct {
	threshold      int
	recentCalls    []toolCallRecord
	maxHistorySize int
}

// toolCallRecord represents a single tool call with normalized arguments and response
type toolCallRecord struct {
	toolName string
	args     string // normalized arguments
	response string // normalized response
}

// LoopDetectionResult contains information about a detected loop
type LoopDetectionResult struct {
	Detected          bool
	ToolName          string
	Repetitions       int
	ArgsPreview       string
	ResponsePreview   string
	CorrectionMessage string
}

const (
	// DefaultLoopDetectionThreshold is the default number of identical tool calls to trigger loop detection
	DefaultLoopDetectionThreshold = 5
	// MaxResponseLengthForComparison is the maximum length of response to use for comparison
	MaxResponseLengthForComparison = 500
	// MaxPreviewLength is the maximum length for preview strings in logs
	MaxPreviewLength = 200
)

// NewToolLoopDetector creates a new tool loop detector with the specified threshold
func NewToolLoopDetector(threshold int) *ToolLoopDetector {
	if threshold <= 0 {
		threshold = DefaultLoopDetectionThreshold
	}
	return &ToolLoopDetector{
		threshold:      threshold,
		recentCalls:    make([]toolCallRecord, 0, threshold+1),
		maxHistorySize: threshold + 1,
	}
}

// normalizeArgs normalizes tool arguments for comparison by parsing JSON and re-marshaling
// This removes any non-deterministic fields and ensures consistent comparison
func normalizeArgs(args string) string {
	// Parse JSON and remove non-deterministic fields
	var argsMap map[string]interface{}
	if err := json.Unmarshal([]byte(args), &argsMap); err != nil {
		// If parsing fails, return as-is
		return args
	}
	// Re-marshal to normalize the structure
	normalized, _ := json.Marshal(argsMap)
	return string(normalized)
}

// normalizeResponse normalizes tool response for comparison by truncating long responses
func normalizeResponse(response string) string {
	// Truncate very long responses to first N chars for comparison
	// This helps detect loops even if responses have minor variations
	if len(response) > MaxResponseLengthForComparison {
		return response[:MaxResponseLengthForComparison]
	}
	return response
}

// CheckAndHandleLoop checks if a tool call represents a loop and returns detection result
// If a loop is detected, the history is preserved to continue tracking for continued loops
func (d *ToolLoopDetector) CheckAndHandleLoop(toolName string, args string, response string) *LoopDetectionResult {
	if toolName == "" {
		return &LoopDetectionResult{Detected: false}
	}

	normalizedArgs := normalizeArgs(args)
	normalizedResponse := normalizeResponse(response)
	currentRecord := toolCallRecord{
		toolName: toolName,
		args:     normalizedArgs,
		response: normalizedResponse,
	}

	// Add current call to recent history
	d.recentCalls = append(d.recentCalls, currentRecord)
	// Keep only last N+1 records for comparison
	if len(d.recentCalls) > d.maxHistorySize {
		d.recentCalls = d.recentCalls[1:]
	}

	// Check if we have enough records and if they all match
	if len(d.recentCalls) >= d.threshold {
		allMatch := true
		for i := 1; i < len(d.recentCalls); i++ {
			if d.recentCalls[i].toolName != d.recentCalls[0].toolName ||
				d.recentCalls[i].args != d.recentCalls[0].args ||
				d.recentCalls[i].response != d.recentCalls[0].response {
				allMatch = false
				break
			}
		}

		if allMatch {
			// Loop detected!
			argsPreview := normalizedArgs
			if len(argsPreview) > MaxPreviewLength {
				argsPreview = argsPreview[:MaxPreviewLength]
			}
			responsePreview := normalizedResponse
			if len(responsePreview) > MaxPreviewLength {
				responsePreview = responsePreview[:MaxPreviewLength]
			}

			correctionMessage := "You are looping the same tool and response. You have called the same tool with the same arguments and received the same response multiple times. Please correct yourself and try a different approach."

			// DO NOT reset history - keep tracking to detect if looping continues
			// The correction message will be sent to LLM to auto-correct itself
			// If looping continues, we want to detect it quickly

			return &LoopDetectionResult{
				Detected:          true,
				ToolName:          toolName,
				Repetitions:       d.threshold,
				ArgsPreview:       argsPreview,
				ResponsePreview:   responsePreview,
				CorrectionMessage: correctionMessage,
			}
		}
	}

	return &LoopDetectionResult{Detected: false}
}

// HandleLoopDetection handles a detected loop by logging, emitting events, and injecting correction message
func HandleLoopDetection(
	a *Agent,
	ctx context.Context,
	result *LoopDetectionResult,
	lastUserMessage string,
	turn int,
	conversationStartTime time.Time,
	messages *[]llmtypes.MessageContent,
	logger loggerv2.Logger,
) {
	if !result.Detected {
		return
	}

	logger.Warn("🔄 Loop detected: same tool call repeated multiple times",
		loggerv2.String("tool_name", result.ToolName),
		loggerv2.Int("repetitions", result.Repetitions),
		loggerv2.String("args_preview", result.ArgsPreview),
		loggerv2.String("response_preview", result.ResponsePreview))

	// Emit loop detection event
	loopEvent := events.NewConversationErrorEvent(
		lastUserMessage,
		fmt.Sprintf("Loop detected: tool '%s' called %d times with same arguments and response", result.ToolName, result.Repetitions),
		turn,
		"loop_detection",
		time.Since(conversationStartTime))
	a.emitTypedEvent(ctx, loopEvent)

	// Inject user message asking LLM to correct itself
	*messages = append(*messages, llmtypes.MessageContent{
		Role:  llmtypes.ChatMessageTypeHuman,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: result.CorrectionMessage}},
	})

	// Emit user message event so frontend can display it in conversation history
	// Use "user" role to match how it appears in the conversation (as ChatMessageTypeHuman)
	userMessageEvent := events.NewUserMessageEvent(turn, result.CorrectionMessage, "user")
	a.emitTypedEvent(ctx, userMessageEvent)

	logger.Info("🔄 Emitted user_message event for loop correction",
		loggerv2.String("content", result.CorrectionMessage),
		loggerv2.Int("turn", turn))

	logger.Info("🔄 Injected loop correction message, continuing conversation")
}
