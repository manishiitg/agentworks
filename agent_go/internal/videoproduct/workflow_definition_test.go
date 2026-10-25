package videoproduct

import "testing"

func TestNewVideoStudioProjectDefaultsToClaudeCode(t *testing.T) {
	manifest := videoStudioWorkflowManifest("test-project", "Test project")
	capabilities, ok := manifest["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatal("workflow manifest capabilities missing")
	}
	config, ok := capabilities["llm_config"].(map[string]interface{})
	if !ok {
		t.Fatal("workflow manifest llm_config missing")
	}
	for _, role := range []string{"builder_llm", "background_llm"} {
		agent, ok := config[role].(map[string]interface{})
		if !ok || agent["provider"] != "claude-code" || agent["model_id"] != DefaultClaudeModel {
			t.Fatalf("%s = %#v, want claude-code/%s", role, agent, DefaultClaudeModel)
		}
	}
}
