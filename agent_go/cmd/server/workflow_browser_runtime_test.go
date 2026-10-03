package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestWorkflowBrowserRuntimeRefreshesPersistentTool(t *testing.T) {
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	mode := "none"
	readErr := error(nil)
	exec := workflowBrowserExecutors("browser-refresh-test", "test-workflow", func(context.Context, string) (*WorkflowManifest, bool, error) {
		return &WorkflowManifest{Capabilities: WorkflowCapabilities{BrowserMode: mode}}, true, readErr
	})["agent_browser"]
	for _, configured := range []string{"none", "auto", "headless", "cdp", "none", ""} {
		mode = configured
		result, err := exec(context.Background(), map[string]interface{}{"command": "status"})
		if err != nil {
			t.Fatal(err)
		}
		var status struct {
			EffectiveMode string `json:"effective_mode"`
		}
		if err := json.Unmarshal([]byte(result), &status); err != nil {
			t.Fatal(err)
		}
		want := "headless"

		if status.EffectiveMode != want {
			t.Fatalf("mode %q: got %q, want %q", configured, status.EffectiveMode, want)
		}
	}

	readErr = errors.New("manifest unavailable")
	if _, err := exec(context.Background(), map[string]interface{}{"command": "status"}); !errors.Is(err, readErr) {
		t.Fatalf("manifest failure must fail closed: %v", err)
	}
}
