package mcpagent

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func regWith(names ...string) *canonicalToolRegistry {
	r := newCanonicalToolRegistry()
	for _, n := range names {
		_ = r.register(registeredTool{
			Name:       n,
			Definition: llmtypes.Tool{Function: &llmtypes.FunctionDefinition{Name: n}},
		})
	}
	return r
}

// A guessed name used to be answered with "use the exact tool names from the
// tool index in your system prompt", which costs a turn to re-read something
// the model already has. Name the alternatives instead.
func TestUnavailableToolsErrorListsRegisteredAndNearest(t *testing.T) {
	a := &Agent{}
	registry := regWith("execute_shell_command", "diff_patch_workspace_file", "agent_browser")
	err := a.unavailableToolsError(context.Background(), registry, "", []string{"diff_patch"}, nil)
	msg := err.Error()

	if !strings.Contains(msg, "diff_patch_workspace_file") {
		t.Fatalf("near-miss not suggested: %s", msg)
	}
	if !strings.Contains(msg, "execute_shell_command") || !strings.Contains(msg, "agent_browser") {
		t.Fatalf("registered tools not listed: %s", msg)
	}
	// An empty permission-denied list is noise; it must not be printed.
	if strings.Contains(msg, "not_allowed=[]") {
		t.Fatalf("empty not_allowed should be omitted: %s", msg)
	}
}

// A real permission denial still has to be attributed.
func TestUnavailableToolsErrorKeepsNonEmptyNotAllowed(t *testing.T) {
	a := &Agent{}
	err := a.unavailableToolsError(context.Background(), regWith("execute_shell_command"), "", []string{"nope"}, []string{"blocked_tool"})
	if !strings.Contains(err.Error(), "not_allowed=[blocked_tool]") {
		t.Fatalf("real denial dropped: %s", err.Error())
	}
}

func TestUnavailableToolsErrorDoesNotSuggestToolsBlockedForTheTurn(t *testing.T) {
	a := &Agent{}
	policy, err := normalizeToolPolicy(ToolPolicy{AllowedTools: []string{"allowed_tool"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), turnPolicyContextKey{}, policy)
	err = a.unavailableToolsError(ctx, regWith("allowed_tool", "blocked_tool"), "", []string{"missing_tool"}, nil)
	msg := err.Error()
	if !strings.Contains(msg, "allowed_tool") {
		t.Fatalf("allowed tool missing from recovery message: %s", msg)
	}
	if strings.Contains(msg, "blocked_tool") {
		t.Fatalf("turn-blocked tool leaked into recovery suggestions: %s", msg)
	}
}
