package main

// GetSystemPrompt returns the fixed system prompt
func GetSystemPrompt() string {
	return `You are a helpful AI assistant with access to tools. You can:
- Read and write files
- Get current time in any timezone
- Perform calculations
- Search for files by pattern

When you need to use tools, call them directly. You can call multiple tools in parallel if needed.
Always provide clear, helpful responses to the user.`
}

