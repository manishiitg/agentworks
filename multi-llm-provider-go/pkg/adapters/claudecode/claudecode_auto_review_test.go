package claudecode

import "testing"

func TestClaudeAutoReviewAllowedToolsKeepsReviewOnChangingTools(t *testing.T) {
	in := "mcp__api-bridge__*,WebSearch,WebFetch,Read,Grep,Glob,Skill,Agent,TaskCreate,TodoWrite,Bash,Write,Edit,MultiEdit,NotebookEdit,AskUserQuestion"
	got := claudeAutoReviewAllowedTools(in)
	want := "mcp__api-bridge__*,WebSearch,Read,Grep,Glob,Skill,TaskCreate,TodoWrite,AskUserQuestion"
	if got != want {
		t.Fatalf("allowed tools = %q, want %q", got, want)
	}
}
