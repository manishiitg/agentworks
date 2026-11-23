package prompt

import "strings"

// SystemPromptTemplate is the complete system prompt template with placeholders
const SystemPromptTemplate = `<session_info>
**Date**: {{CURRENT_DATE}}
</session_info>

{{CORE_PRINCIPLES}}

{{TOOL_USAGE}}

<virtual_tools>
{{VIRTUAL_TOOLS_SECTION}}
</virtual_tools>`

// Placeholder constants for easy replacement
const (
	ToolsPlaceholder               = "{{TOOLS}}"
	VirtualToolsSectionPlaceholder = "{{VIRTUAL_TOOLS_SECTION}}"
	CurrentDatePlaceholder         = "{{CURRENT_DATE}}"
	CorePrinciplesPlaceholder      = "{{CORE_PRINCIPLES}}"
	ToolUsagePlaceholder           = "{{TOOL_USAGE}}"
)

// NormalizeForAppend tidies a system prompt before another block is appended
// to it: collapses the blank-line runs that concatenation tends to leave
// behind and trims surrounding whitespace.
//
// This replaced RemoveAIStaffEngineerText, which stripped a "# AI Staff
// Engineer" persona header that SystemPromptTemplate no longer emits. The
// system prompt now describes the product context and leaves the role to
// whatever the caller sets, so there is no persona to strip — but callers
// still relied on the trimming this did as a side effect.
func NormalizeForAppend(prompt string) string {
	prompt = strings.ReplaceAll(prompt, "\n\n\n", "\n\n")
	return strings.TrimSpace(prompt)
}
