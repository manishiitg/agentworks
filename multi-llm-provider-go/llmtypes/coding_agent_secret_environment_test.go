package llmtypes

import "testing"

func TestCodingAgentSecretEnvironmentIsScopedAndDoesNotMutateBase(t *testing.T) {
	base := []string{"PATH=/bin", "SECRET_PRESENT=old", "OTHER=value", "STEP_OUTPUT_DIR=/old/output", "PYTHONPATH=/old/imports"}
	opts := &CallOptions{}
	WithCodingAgentSecretEnvironment(map[string]string{
		"SECRET_PRESENT":     "new",
		"SECRET_ONLY":        "value",
		"MCP_CUSTOM":         "http://127.0.0.1/s/session/tools/custom",
		"PATH":               "must-not-pass",
		"STEP_OUTPUT_DIR":    "/run/current/output",
		"WORKFLOW_CODE_ROOT": "/crew/.agentworks/code/workflow",
		"SCRIPT_SOURCE_DIR":  "/run/code",
		"PYTHONPATH":         "/crew/.agentworks/code/workflow",
		"WORKFLOW_KB_FACTS":  "/knowledge/facts",
		"UNRELATED":          "must-not-pass",
	})(opts)

	got := MergeCodingAgentSecretEnvironment(base, opts)
	if base[1] != "SECRET_PRESENT=old" {
		t.Fatal("base environment was mutated")
	}
	joined := ""
	for _, item := range got {
		joined += item + "\n"
	}
	for _, want := range []string{"PATH=/bin", "SECRET_PRESENT=new", "SECRET_ONLY=value", "MCP_CUSTOM=http://127.0.0.1/s/session/tools/custom", "STEP_OUTPUT_DIR=/run/current/output", "WORKFLOW_CODE_ROOT=/crew/.agentworks/code/workflow", "SCRIPT_SOURCE_DIR=/run/code", "WORKFLOW_KB_FACTS=/knowledge/facts", "PYTHONPATH=/crew/.agentworks/code/workflow"} {
		if !containsEnvironmentEntry(joined, want) {
			t.Fatalf("merged environment missing %q: %q", want, joined)
		}
	}
	if containsEnvironmentEntry(joined, "PATH=must-not-pass") || containsEnvironmentEntry(joined, "UNRELATED=must-not-pass") || containsEnvironmentEntry(joined, "STEP_OUTPUT_DIR=/old/output") || containsEnvironmentEntry(joined, "PYTHONPATH=/old/imports") {
		t.Fatalf("non-secret environment entry escaped: %q", joined)
	}
}

func containsEnvironmentEntry(environment, entry string) bool {
	for _, line := range splitEnvironment(environment) {
		if line == entry {
			return true
		}
	}
	return false
}

func splitEnvironment(environment string) []string {
	var out []string
	start := 0
	for i := 0; i < len(environment); i++ {
		if environment[i] == '\n' {
			out = append(out, environment[start:i])
			start = i + 1
		}
	}
	return out
}
