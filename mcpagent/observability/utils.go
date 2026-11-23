package observability

import (
	"fmt"
	"strings"

	"github.com/manishiitg/mcpagent/events"
)

// cleanQuestionForName cleans and formats a question string for use in trace/span names
func cleanQuestionForName(question string) string {
	// Clean up the question: remove extra whitespace, convert to lowercase, replace spaces with underscores
	cleanQuestion := strings.TrimSpace(strings.ToLower(question))

	// Replace common punctuation and multiple spaces with single underscores
	cleanQuestion = strings.ReplaceAll(cleanQuestion, "?", "")
	cleanQuestion = strings.ReplaceAll(cleanQuestion, "!", "")
	cleanQuestion = strings.ReplaceAll(cleanQuestion, ".", "")
	cleanQuestion = strings.ReplaceAll(cleanQuestion, ",", "")
	cleanQuestion = strings.ReplaceAll(cleanQuestion, ";", "")
	cleanQuestion = strings.ReplaceAll(cleanQuestion, ":", "")

	// Replace multiple spaces with single space, then spaces with underscores
	words := strings.Fields(cleanQuestion)
	cleanQuestion = strings.Join(words, "_")

	// Truncate to a reasonable length for trace names (max 80 chars)
	if len(cleanQuestion) > 80 {
		// Try to truncate at word boundary if possible
		if strings.Contains(cleanQuestion[:80], "_") {
			lastUnderscore := strings.LastIndex(cleanQuestion[:80], "_")
			if lastUnderscore > 50 { // Only truncate at underscore if we have a reasonable length
				cleanQuestion = cleanQuestion[:lastUnderscore]
			} else {
				cleanQuestion = cleanQuestion[:77] + "..."
			}
		} else {
			cleanQuestion = cleanQuestion[:77] + "..."
		}
	}

	return cleanQuestion
}

// GenerateTraceName generates a meaningful name for traces based on user query
func GenerateTraceName(eventData interface{}) string {
	var question string

	// Type switch for concrete event types
	switch data := eventData.(type) {
	case *events.ConversationStartEvent:
		question = data.Question
	case *events.ConversationTurnEvent:
		question = data.Question
	case *events.UserMessageEvent:
		question = data.Content
	case *events.AgentStartEvent:
		// AgentStart usually doesn't have the question yet
		return "agent_conversation"
	}

	if question != "" {
		cleanQuestion := cleanQuestionForName(question)
		return fmt.Sprintf("query_%s", cleanQuestion)
	}

	return "agent_conversation"
}

// GenerateAgentSpanName creates an informative name for agent spans
func GenerateAgentSpanName(eventData interface{}) string {
	var modelID string
	var availableTools int

	switch data := eventData.(type) {
	case *events.AgentStartEvent:
		modelID = data.ModelID
		availableTools = 0 // AgentStartEvent doesn't have AvailableTools
	default:
		modelID = "unknown"
		availableTools = 0
	}

	if modelID == "" {
		modelID = "unknown"
	}

	// Extract model name (e.g., "gpt-4.1" from full model ID)
	modelParts := strings.Split(modelID, "/")
	shortModel := modelParts[len(modelParts)-1]

	return fmt.Sprintf("agent_%s_%d_tools", shortModel, availableTools)
}

// GenerateConversationSpanName creates an informative name for conversation spans
func GenerateConversationSpanName(eventData interface{}) string {
	var question string

	switch data := eventData.(type) {
	case *events.ConversationStartEvent:
		question = data.Question
	case *events.ConversationTurnEvent:
		question = data.Question
	case *events.UserMessageEvent:
		question = data.Content
	}

	if question != "" {
		cleanQuestion := cleanQuestionForName(question)
		// Truncate to a reasonable length for span names (max 50 chars)
		if len(cleanQuestion) > 50 {
			if strings.Contains(cleanQuestion[:50], "_") {
				lastUnderscore := strings.LastIndex(cleanQuestion[:50], "_")
				if lastUnderscore > 30 {
					cleanQuestion = cleanQuestion[:lastUnderscore]
				} else {
					cleanQuestion = cleanQuestion[:47] + "..."
				}
			} else {
				cleanQuestion = cleanQuestion[:47] + "..."
			}
		}
		return fmt.Sprintf("conversation_%s", cleanQuestion)
	}

	return "conversation_execution"
}

// GenerateLLMSpanName creates an informative name for LLM generation spans
func GenerateLLMSpanName(eventData interface{}) string {
	var turn int
	var modelID string
	var toolsCount int

	switch data := eventData.(type) {
	case *events.LLMGenerationStartEvent:
		turn = data.Turn
		modelID = data.ModelID
		toolsCount = data.ToolsCount
	case *events.LLMGenerationEndEvent:
		turn = data.Turn
		modelID = "unknown"
		toolsCount = 0
	default:
		turn = 0
		modelID = "unknown"
		toolsCount = 0
	}

	if modelID == "" {
		modelID = "unknown"
	}

	modelParts := strings.Split(modelID, "/")
	shortModel := modelParts[len(modelParts)-1]

	return fmt.Sprintf("llm_generation_turn_%d_%s_%d_tools", turn, shortModel, toolsCount)
}

// GenerateToolSpanName creates an informative name for tool call spans
func GenerateToolSpanName(eventData interface{}) string {
	var turn int
	var toolName string
	var serverName string

	switch data := eventData.(type) {
	case *events.ToolCallStartEvent:
		turn = data.Turn
		toolName = data.ToolName
		serverName = data.ServerName
	case *events.ToolCallEndEvent:
		turn = data.Turn
		toolName = data.ToolName
		serverName = data.ServerName
	default:
		turn = 0
		toolName = "unknown"
		serverName = "unknown"
	}

	if toolName == "" {
		toolName = "unknown"
	}
	if serverName == "" {
		serverName = "unknown"
	}

	return fmt.Sprintf("tool_%s_%s_turn_%d", serverName, toolName, turn)
}

// ExtractFinalResult extracts the final result from event data
func ExtractFinalResult(eventData interface{}) string {
	switch data := eventData.(type) {
	case *events.ConversationEndEvent:
		if data.Result != "" {
			return data.Result
		}
	case *events.UnifiedCompletionEvent:
		if data.FinalResult != "" {
			return data.FinalResult
		}
	case *events.AgentEndEvent:
		// AgentEndEvent doesn't have a direct result field
	case *events.LLMGenerationEndEvent:
		if data.Content != "" {
			return data.Content
		}
	}
	return ""
}
