package workproduct

import (
	"strings"
	"testing"
)

// Same rule as Code's prompt: platform tools are bridge tools found with search_tools, not the CLI's own tool listing.
func TestCrewPromptNamesTheBridgeToolDiscovery(t *testing.T) {
	prompt := renderProductPrompt()
	for _, want := range []string{"search_tools", "bridge tool routing", "is not missing"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the Crew system prompt does not contain %q", want)
		}
	}
	if strings.Contains(prompt, "current runtime's tool discovery") {
		t.Error("the ambiguous 'current runtime's tool discovery' wording is back")
	}
}
