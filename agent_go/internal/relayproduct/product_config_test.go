package relayproduct

import (
	"strings"
	"testing"
)

func TestBuilderPromptLoadsProductManifest(t *testing.T) {
	prompt, err := BuilderPrompt()
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"planning/plan.json", "authored_prompt", "script_only", "value_path"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("Relay product prompt is missing %q", required)
		}
	}
}
