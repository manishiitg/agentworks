package codeproduct

import (
	"strings"
	"testing"
)

// A Codex chat in Code said "the UI control tools aren't available" because the prompt told it to "discover tools through the current runtime's tool search", which for Codex
// is its own tool listing, not the platform's search_tools (server B, 2026-10-04). The prompt must name search_tools and say platform tools are bridge tools.
func TestCodePromptNamesTheBridgeToolDiscovery(t *testing.T) {
	prompt := renderProductPrompt()
	for _, want := range []string{"search_tools", "bridge tool routing", "is not missing"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the Code system prompt does not contain %q", want)
		}
	}
	if strings.Contains(prompt, "current runtime's tool search") {
		t.Error("the ambiguous 'current runtime's tool search' wording is back")
	}
}
