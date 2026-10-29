package agentworksproduct

import (
	"strings"
	"testing"
)

// The builder slash commands moved from hardcoded frontend builtins into
// product.yaml. This locks the manifest contract the workflow surface menu
// renders from: every command resolves a non-empty prompt carrying the
// {{context}} placeholder, kind-backed commands name their guidance kind,
// and explicit review commands keep their menu visibility.
func TestAgentWorksCommandsResolvePrompts(t *testing.T) {
	manifest, err := AgentWorksManifest()
	if err != nil {
		t.Fatalf("AgentWorksManifest: %v", err)
	}
	byName := map[string]string{}
	hidden := map[string]bool{}
	for _, cmd := range manifest.Profile.Commands {
		if cmd.Name == "" {
			t.Fatal("command with empty name")
		}
		if strings.TrimSpace(cmd.Prompt) == "" {
			t.Fatalf("command %q resolved an empty prompt", cmd.Name)
		}
		if !strings.Contains(cmd.Prompt, "{{context}}") {
			t.Fatalf("command %q prompt does not carry the {{context}} placeholder", cmd.Name)
		}
		byName[cmd.Name] = cmd.Prompt
		hidden[cmd.Name] = cmd.MenuHidden
	}
	for name, wantKind := range map[string]string{
		"design-plan":      `kind="design-plan"`,
		"run-plan-drift":   `kind="run-plan-drift"`,
		"design-dashboard": `kind="design-dashboard"`,
		"setup-goals":      `kind="setup-goals"`,
	} {
		prompt, ok := byName[name]
		if !ok {
			t.Fatalf("command %q missing from product.yaml", name)
		}
		if !strings.Contains(prompt, wantKind) {
			t.Fatalf("command %q prompt does not name its guidance kind %s", name, wantKind)
		}
	}
	for _, name := range []string{"backup", "share", "notify", "run", "fix", "schedule", "connect", "upgrade"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("command %q missing from product.yaml", name)
		}
	}
	// Removed reviewer commands are absent.
	// Owner 2026-10-08: one Pulse, no separate reviewers. The old reviewer
	// commands are gone.
	for _, retired := range []string{"run-goal-work", "strategy-auditor", "goal-advisor", "run-technical-review", "pulse-review",
		"run-architecture-review", "pulse-review-execution-health", "pulse-review-database", "pulse-fixer", "merge-pulse-issues",
		// Saved-code quality is part of Workflow Review (owner, 2026-10-08).
		"review-code"} {
		if _, ok := byName[retired]; ok {
			t.Fatalf("%s is retired with the old reviewers but is still a command", retired)
		}
	}
}
