package mcpagent

// generateToolArgsParsingFeedback generates simple feedback for tool argument parsing errors
func generateToolArgsParsingFeedback(toolName, arguments string, err error) string {
	return "Tool argument parsing error: " + err.Error() + ". Please retry with valid JSON arguments."
}

// generateEmptyToolNameFeedback generates feedback for empty tool name errors
func generateEmptyToolNameFeedback(arguments string) string {
	return "Error: Tool call missing tool name. Please retry with a valid tool name from the available tools list."
}
