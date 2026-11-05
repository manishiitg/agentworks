package step_based_workflow

import (
	"context"
	"fmt"
	"testing"
)

func TestScheduleGuardStopsSideEffects(t *testing.T) {
	draft := newWorkshopDefinitionDraft()
	called := false
	r := GuardScheduleTools(draft, func(context.Context, string, map[string]interface{}) error {
		return fmt.Errorf("bot grant changed")
	})
	for _, name := range []string{"execute_step", "run_full_workflow", "update_scripted_step", "update_step_config", "get_workflow_config"} {
		err := r.RegisterCustomTool(name, "test", map[string]interface{}{"type": "object"}, func(context.Context, map[string]interface{}) (string, error) { called = true; return "ok", nil }, "workflow")
		if err != nil {
			t.Fatal(err)
		}
		tool := draft.tools[name]
		called = false
		_, err = tool.Execute(context.Background(), map[string]interface{}{})
		if name == "get_workflow_config" {
			if err != nil || !called {
				t.Fatal("read blocked")
			}
			continue
		}
		if err == nil || called {
			t.Fatalf("%s ran before warning", name)
		}
	}
}
