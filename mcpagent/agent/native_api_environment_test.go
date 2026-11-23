package mcpagent

import "testing"

func TestNativeCodingAgentEnvironmentKeepsOnlyScopedMCPAPIValues(t *testing.T) {
	a := &Agent{}
	withCodingAgentSecretEnvironment(map[string]string{
		"SECRET_PEXELS_API_KEY": "secret",
		"MCP_CUSTOM":            "http://127.0.0.1/s/session/tools/custom",
		"MCP_AUTH":              "Authorization: Bearer session-token",
		"STEP_OUTPUT_DIR":       "/run/current/output",
		"WORKFLOW_CODE_ROOT":    "/crew/.agentworks/code/workflow",
		"WORKFLOW_KB_FACTS":     "/knowledge/facts",
		"PYTHONPATH":            "/crew/.agentworks/code/workflow",
		"PATH":                  "must-not-pass",
		"UNRELATED":             "must-not-pass",
	})(a)
	for _, key := range []string{"SECRET_PEXELS_API_KEY", "MCP_CUSTOM", "MCP_AUTH", "STEP_OUTPUT_DIR", "WORKFLOW_CODE_ROOT", "WORKFLOW_KB_FACTS", "PYTHONPATH"} {
		if a.codingAgentSecretEnvironment[key] == "" {
			t.Fatalf("native coding agent environment lost %s: %#v", key, a.codingAgentSecretEnvironment)
		}
	}
	for _, key := range []string{"PATH", "UNRELATED"} {
		if _, present := a.codingAgentSecretEnvironment[key]; present {
			t.Fatalf("native coding agent environment admitted %s: %#v", key, a.codingAgentSecretEnvironment)
		}
	}
}
