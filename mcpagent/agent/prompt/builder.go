package prompt

import (
	"strings"
	"time"
)

// GetCodeExecutionInstructions returns the code execution mode instructions section.
// Product environment and file rules belong to caller instructions; current
// routing is composed by the Agent.
func GetCodeExecutionInstructions() string {
	return "## Code execution\n\nFind authorized tools with search_tools and load their exact schemas and routes with get_api_spec. Use this session's declared tools and runtime routing instructions."
}

// BuildSystemPromptWithoutTools builds the system prompt without including tool descriptions
// This is useful when tools are passed via llmtypes.WithTools() to avoid prompt length issues
func BuildSystemPromptWithoutTools(useCodeExecutionMode bool, enableParallelToolExecution bool) string {

	// Date-only context remains stable between turns; a ticking system prompt
	// forces native CLI sessions to relaunch on every message.
	now := time.Now()
	currentDate := now.Format("2006-01-02")

	// Build core principles section based on mode
	var corePrinciplesSection string
	autonomousNote := `
**Finish what you start this turn:** Do not stop mid-action — complete all tool calls you have initiated before generating a text response. If you delegated work, ending your turn IS the completion of your action for this turn.`

	if useCodeExecutionMode {
		corePrinciplesSection = `<core_principles>
**Your Goal:** Complete the user's request.

**Operating Rules:**
1. **Be Proactive:** Do not ask for permission to use tools. Just use them.
2. **Chain Actions:** If a tool output leads to a next step, take it immediately.
3. **Solve Fully:** Strive to reach the final answer or state before returning control.
` + autonomousNote + `
</core_principles>`
	} else {
		corePrinciplesSection = `<core_principles>
**Your Goal:** Complete the user's request.

**Operating Rules:**
1. **Be Proactive:** Do not ask for permission to use tools. Just use them.
2. **Chain Actions:** If a tool output leads to a next step, take it immediately. Do not stop to report intermediate progress unless asked.
3. **Solve Fully:** Strive to reach the final answer or state before returning control.
` + autonomousNote + `
</core_principles>`
	}

	// Build tool usage section based on mode
	var toolUsageSection string
	if useCodeExecutionMode {
		codeExecutionInstructions := GetCodeExecutionInstructions()

		toolUsageSection = `<code_usage>
` + codeExecutionInstructions + `
</code_usage>`
	} else {
		var parallelToolHint string
		if enableParallelToolExecution {
			parallelToolHint = `

**Parallel Execution:**
- You can call multiple tools in a single response — they will execute concurrently
- Use this to speed up independent operations (e.g., reading multiple files, querying multiple APIs)
- Only parallelize independent calls — if one tool's output is needed as input for another, call them sequentially`
		}
		toolUsageSection = `<tool_usage>
**Guidelines:**
- Use tools when they can help answer the question
- Use tool discovery for detailed API contracts when relevant
- Provide clear responses based on tool results` + parallelToolHint + `

**Best Practices:**
- Use virtual tools to access detailed knowledge when relevant
- **If a tool call fails, retry with different arguments or parameters**
- **Try alternative approaches when tools return errors or unexpected results**
- **Modify search terms, file paths, or query parameters to overcome failures**
</tool_usage>`
	}

	// Always use Simple system prompt template
	prompt := SystemPromptTemplate

	// Replace all placeholders
	prompt = strings.ReplaceAll(prompt, CorePrinciplesPlaceholder, corePrinciplesSection)
	prompt = strings.ReplaceAll(prompt, ToolUsagePlaceholder, toolUsageSection)
	prompt = strings.ReplaceAll(prompt, VirtualToolsSectionPlaceholder, "")
	prompt = strings.ReplaceAll(prompt, CurrentDatePlaceholder, currentDate)

	return prompt
}
