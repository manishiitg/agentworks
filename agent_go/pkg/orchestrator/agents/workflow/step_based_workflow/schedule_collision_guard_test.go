package step_based_workflow

import (
	"context"
	"fmt"
	"testing"
)

func TestScheduleGuardStopsSideEffectsAndPublishesOverride(t *testing.T) {
	draft := newWorkshopDefinitionDraft()
	called := false
	r := GuardScheduleTools(draft, func(_ context.Context, _ string, args map[string]interface{}) error {
		if args["force"] == true {
			return nil
		}
		return fmt.Errorf("schedule_running")
	})
	for _, name := range []string{"migrate_agent_steps", "execute_step", "run_full_workflow", "update_scripted_step", "update_step_config", "get_workflow_config"} {
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
		if tool.InputSchema["properties"].(map[string]interface{})["force"] == nil {
			t.Fatal("missing override schema")
		}
		_, err = tool.Execute(context.Background(), map[string]interface{}{"force": true})
		if err != nil || !called {
			t.Fatalf("%s force failed: %v", name, err)
		}
	}
}
