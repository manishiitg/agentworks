package server

import (
	"context"
	"strings"
	"testing"
)

func TestRelayPublishRequiresSavedPythonTools(t *testing.T) {
	files := map[string]string{
		"workflow.json":             `{"kind":"relay"}`,
		"planning/plan.json":        `{"steps":[]}`,
		"planning/step_config.json": `{"steps":[{"id":"answer","agent_configs":{"enabled_custom_tools":["python_tools:lookup_customer"]}}]}`,
	}
	if err := validateRelayPythonTools(context.Background(), files); err == nil || !strings.Contains(err.Error(), "tool.json") {
		t.Fatalf("missing definition: %v", err)
	}
	files["code/tools/lookup_customer/tool.json"] = `{"description":"Lookup","parameters":{"type":"object"}}`
	if err := validateRelayPythonTools(context.Background(), files); err == nil || !strings.Contains(err.Error(), "main.py") {
		t.Fatalf("missing source: %v", err)
	}
	files["code/tools/lookup_customer/main.py"] = "def run(input): return input\n"
	if err := validateRelayPythonTools(context.Background(), files); err != nil {
		t.Fatal(err)
	}
	files["planning/step_config.json"] = `{"steps":[]}`
	files["workflow.json"] = `{"execution_defaults":{"enabled_custom_tools":["python_tools:missing_default"]}}`
	if err := validateRelayPythonTools(context.Background(), files); err == nil || !strings.Contains(err.Error(), "missing_default") {
		t.Fatalf("default tool ignored: %v", err)
	}
}
