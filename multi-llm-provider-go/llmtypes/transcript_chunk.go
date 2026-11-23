package llmtypes

// Keep the original text-only progress API stable for clients that expect
// narration. Stateful Session observers opt into the complete native trail.
func TranscriptProgressText(messages []MessageContent, thinkingAsText bool, preserveThinking ...bool) []MessageContent {
	var out []MessageContent
	for _, message := range messages {
		if message.Role != ChatMessageTypeAI {
			continue
		}
		for _, part := range message.Parts {
			switch part := part.(type) {
			case TextContent:
				out = append(out, MessageContent{Role: ChatMessageTypeAI, Parts: []ContentPart{part}})
			case ThinkingContent:
				if thinkingAsText {
					out = append(out, TextPart(ChatMessageTypeAI, part.Thinking))
				} else if len(preserveThinking) > 0 && preserveThinking[0] {
					out = append(out, MessageContent{Role: ChatMessageTypeAI, Parts: []ContentPart{part}})
				}
			}
		}
	}
	return out
}

// TranscriptChunkMessage preserves native tools when a retained turn is read
// through the message API rather than GenerateContent's streaming channel.
func TranscriptChunkMessage(chunk StreamChunk) (MessageContent, bool) {
	switch chunk.Type {
	case StreamChunkTypeContent:
		return TextPart(ChatMessageTypeAI, chunk.Content), chunk.Content != ""
	case StreamChunkTypeReasoning:
		return MessageContent{Role: ChatMessageTypeAI, Parts: []ContentPart{ThinkingContent{Thinking: chunk.Content}}}, chunk.Content != ""
	case StreamChunkTypeToolCallStart:
		return MessageContent{Role: ChatMessageTypeAI, Parts: []ContentPart{ToolCall{ID: chunk.ToolCallID, Type: "function", FunctionCall: &FunctionCall{Name: chunk.ToolName, Arguments: chunk.ToolArgs}}}}, chunk.ToolCallID != ""
	case StreamChunkTypeToolCallEnd:
		return MessageContent{Role: ChatMessageTypeTool, Parts: []ContentPart{ToolCallResponse{ToolCallID: chunk.ToolCallID, Name: chunk.ToolName, Content: chunk.ToolResult}}}, chunk.ToolCallID != ""
	}
	return MessageContent{}, false
}
