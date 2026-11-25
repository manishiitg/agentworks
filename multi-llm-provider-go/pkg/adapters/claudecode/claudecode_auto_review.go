package claudecode

import "strings"

// claudeAutoReviewUnreviewed are Claude's own tools that only read, plan or
// talk to the person. Under Auto-review they stay pre-approved; every other
// native tool (Bash, Write, Edit, WebFetch, Agent, ...) leaves the allowed list
// so Claude's classifier reviews each call. The AgentWorks bridge tools
// (mcp__*) stay: the bridge checks every action against the chat's folders.
var claudeAutoReviewUnreviewed = map[string]bool{
	"Read": true, "Grep": true, "Glob": true, "WebSearch": true, "Skill": true,
	"TaskCreate": true, "TaskGet": true, "TaskUpdate": true, "TaskList": true, "TodoWrite": true,
	"AskUserQuestion": true,
}

func claudeAutoReviewAllowedTools(allowed string) string {
	var kept []string
	for _, tool := range strings.Split(allowed, ",") {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if strings.HasPrefix(tool, "mcp__") || claudeAutoReviewUnreviewed[tool] {
			kept = append(kept, tool)
		}
	}
	return strings.Join(kept, ",")
}
