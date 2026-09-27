package step_based_workflow

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestGoalWorkPlatformToolsFollowPermissions(t *testing.T) {
	if !goalWorkToolAllowed("search_platform", goalWorkPermissions{}) {
		t.Fatal("platform search is read-only context and must always be allowed")
	}
	if goalWorkToolAllowed("ask_platform_crew", goalWorkPermissions{}) {
		t.Fatal("Crew work must need the Run permission")
	}
	if !goalWorkToolAllowed("ask_platform_crew", goalWorkPermissions{Run: true}) {
		t.Fatal("Crew work must be allowed with the Run permission")
	}
}

// Background agents other than Goal Work lose ask_platform_crew without
// touching the shared parent bundle.
func TestWithoutBackgroundToolLeavesTheParentBundleIntact(t *testing.T) {
	parent := []llmtypes.Tool{
		{Type: "function", Function: &llmtypes.FunctionDefinition{Name: "search_platform"}},
		{Type: "function", Function: &llmtypes.FunctionDefinition{Name: "ask_platform_crew"}},
	}
	executors := map[string]interface{}{"search_platform": 1, "ask_platform_crew": 2}
	tools, handlers := withoutBackgroundTool(parent, executors, "ask_platform_crew")
	if len(tools) != 1 || tools[0].Function.Name != "search_platform" || handlers["ask_platform_crew"] != nil {
		t.Fatalf("ask_platform_crew must be removed, got %v %v", tools, handlers)
	}
	if len(parent) != 2 || executors["ask_platform_crew"] == nil {
		t.Fatal("the parent bundle must be unchanged")
	}
}

// A background agent's tool session maps to its workflow and chat only while
// it is registered.
func TestWorkshopToolSessionOwnerLivesWithTheSession(t *testing.T) {
	release := RegisterWorkshopToolSession("tool-1", "/Workflow/a/", "chat-1")
	owner, ok := LookupWorkshopToolSession("tool-1")
	if !ok || owner.WorkspacePath != "Workflow/a" || owner.ChatSessionID != "chat-1" {
		t.Fatalf("got %+v %v", owner, ok)
	}
	release()
	if _, ok := LookupWorkshopToolSession("tool-1"); ok {
		t.Fatal("the owner must be forgotten when the session ends")
	}
}
