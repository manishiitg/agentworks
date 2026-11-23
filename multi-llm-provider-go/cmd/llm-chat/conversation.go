package main

import (
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Conversation manages the conversation history
type Conversation struct {
	messages     []llmtypes.MessageContent
	systemPrompt string
}

// NewConversation creates a new conversation with a system prompt
func NewConversation(systemPrompt string) *Conversation {
	conv := &Conversation{
		messages:     []llmtypes.MessageContent{},
		systemPrompt: systemPrompt,
	}
	// Add system message
	conv.messages = append(conv.messages, llmtypes.TextParts(llmtypes.ChatMessageTypeSystem, systemPrompt))
	return conv
}

// AddUserMessage adds a user message to the conversation
func (c *Conversation) AddUserMessage(text string) {
	c.messages = append(c.messages, llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, text))
}

// AddAssistantMessage adds an assistant message with optional tool calls
func (c *Conversation) AddAssistantMessage(content string, toolCalls []llmtypes.ToolCall) {
	parts := []llmtypes.ContentPart{}
	if content != "" {
		parts = append(parts, llmtypes.TextContent{Text: content})
	}
	for _, tc := range toolCalls {
		parts = append(parts, tc)
	}
	c.messages = append(c.messages, llmtypes.MessageContent{
		Role:  llmtypes.ChatMessageTypeAI,
		Parts: parts,
	})
}

// AddToolResults adds tool call results to the conversation
// CRITICAL: Bedrock requires all tool results for tool calls from a single assistant message
// to be in ONE tool message, not separate messages. This function groups all results together.
func (c *Conversation) AddToolResults(toolResults []ToolResult) {
	if len(toolResults) == 0 {
		return
	}

	// Collect all tool results as parts of a single message
	parts := make([]llmtypes.ContentPart, 0, len(toolResults))
	for _, tr := range toolResults {
		resultContent := tr.Result
		if tr.Error != nil {
			resultContent = tr.Error.Error()
		}
		parts = append(parts, llmtypes.ToolCallResponse{
			ToolCallID: tr.ToolCallID,
			Name:       tr.Name,
			Content:    resultContent,
		})
	}

	// Add all tool results as a single message (required by Bedrock)
	c.messages = append(c.messages, llmtypes.MessageContent{
		Role:  llmtypes.ChatMessageTypeTool,
		Parts: parts,
	})
}

// GetMessages returns all messages in the conversation
func (c *Conversation) GetMessages() []llmtypes.MessageContent {
	return c.messages
}

// Clear clears the conversation but keeps the system prompt
func (c *Conversation) Clear() {
	c.messages = []llmtypes.MessageContent{
		llmtypes.TextParts(llmtypes.ChatMessageTypeSystem, c.systemPrompt),
	}
}
