package server

import (
	"context"
	"strings"
	"testing"
)

// Goal Work's platform search is read-only; Crew work is its own tool.
func TestPulsePlatformToolsKeepReadAndCrewWorkApart(t *testing.T) {
	tools, executors, _ := createPulsePlatformTools()
	if len(tools) != 3 || executors["read_crew_calls"] == nil {
		t.Fatalf("want search_platform, ask_platform_crew and read_crew_calls, got %d tools", len(tools))
	}
	search := executors["search_platform"].(func(context.Context, map[string]interface{}) (string, error))
	for _, op := range []string{"ask_crew", "call_crew_function", "run_step", "write_file", "execute_step"} {
		if _, err := search(context.Background(), map[string]interface{}{"workspace_path": "Workflow/x", "operation": op}); err == nil || !strings.Contains(err.Error(), "not available") {
			t.Errorf("search_platform must refuse %s, got %v", op, err)
		}
	}
	crew := executors["ask_platform_crew"].(func(context.Context, map[string]interface{}) (string, error))
	if _, err := crew(context.Background(), map[string]interface{}{"workspace_path": "Workflow/x", "operation": "list_workflows"}); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Errorf("ask_platform_crew must only do Crew work, got %v", err)
	}
	for op := range pulsePlatformReadOperations {
		catalog, err := externalTools()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, tool := range catalog {
			if tool.Name == op {
				found = true
				if tool.mutates {
					t.Errorf("read operation %s mutates", op)
				}
			}
		}
		if !found {
			t.Errorf("read operation %s is not in the external catalog", op)
		}
	}
}
