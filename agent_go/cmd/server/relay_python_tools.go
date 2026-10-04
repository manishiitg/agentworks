package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pythontools"
)

// Validate declarations from the exact snapshot, including step config and
// workflow defaults. No draft read or Python execution is allowed here.
func validateRelayPythonTools(ctx context.Context, files map[string]string) error {
	var selections []string
	var collect func(interface{})
	collect = func(value interface{}) {
		switch value := value.(type) {
		case map[string]interface{}:
			for key, child := range value {
				if key == "enabled_custom_tools" {
					if items, ok := child.([]interface{}); ok {
						for _, item := range items {
							if name, ok := item.(string); ok {
								selections = append(selections, name)
							}
						}
					}
				}
				collect(child)
			}
		case []interface{}:
			for _, child := range value {
				collect(child)
			}
		}
	}
	for _, filename := range []string{"workflow.json", "planning/plan.json", "planning/step_config.json"} {
		if raw, exists := files[filename]; exists {
			var value interface{}
			if err := json.Unmarshal([]byte(raw), &value); err != nil {
				return fmt.Errorf("Relay %s is invalid JSON: %w", filename, err)
			}
			collect(value)
		}
	}
	_, err := pythontools.Load(ctx, selections, func(_ context.Context, filename string) (string, error) {
		if content, exists := files[filename]; exists {
			return content, nil
		}
		return "", fmt.Errorf("saved %s is required before publishing", filename)
	})
	return err
}
