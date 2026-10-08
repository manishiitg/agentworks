package agentworksproduct

import (
	"slices"
	"strings"
	"testing"
)

// The builder slash commands moved from hardcoded frontend builtins into
// product.yaml. This locks the manifest contract the workflow surface menu
// renders from: every command resolves a non-empty prompt carrying the
// {{context}} placeholder, kind-backed commands name their guidance kind,
// and the retained pulse-review focus shortcuts stay executable but hidden.
func TestAgentWorksCommandsResolvePrompts(t *testing.T) {
	manifest, err := AgentWorksManifest()
	if err != nil {
		t.Fatalf("AgentWorksManifest: %v", err)
	}
	byName := map[string]string{}
	hidden := map[string]bool{}
	aliases := map[string][]string{}
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
		for _, alias := range cmd.Aliases {
			aliases[alias] = append(aliases[alias], cmd.Name)
		}
		aliases[cmd.Name] = append(aliases[cmd.Name], cmd.Name)
	}
	for name, wantKind := range map[string]string{
		"design-plan":      `kind="design-plan"`,
		"run-plan-drift":   `kind="review-artifact-drift"`,
		"design-dashboard": `kind="design-reporting-ui"`,
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
	for _, name := range []string{"backup", "publish", "notify"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("command %q missing from product.yaml", name)
		}
	}
	// /pulse stays a hardcoded builtin: it is an async frontend API call no
	// static prompt can express.
	if _, ok := byName["pulse"]; ok {
		t.Fatal("command pulse must stay a hardcoded builtin, not a yaml prompt")
	}
	if !hidden["run-plan-drift"] {
		t.Fatal("run-plan-drift must be hidden from the slash menu: the Pulse tab starts it")
	}
	// Owner 2026-10-08: one Pulse, no separate reviewers. The old reviewer
	// commands and their aliases are gone; #pulse in the Builder chat asks Pulse.
	for _, retired := range []string{"run-goal-work", "strategy-auditor", "goal-advisor", "run-technical-review", "pulse-review",
		"run-architecture-review", "pulse-review-execution-health", "pulse-review-database", "pulse-fixer", "merge-pulse-issues",
		// Saved-code quality is part of Workflow Review (owner, 2026-10-08).
		"review-code"} {
		if len(aliases[retired]) > 0 {
			t.Fatalf("%s is retired with the old reviewers but is still a command", retired)
		}
	}
	if !slices.Equal(aliases["define-success"], []string{"setup-goals"}) {
		t.Fatalf("setup-goals alias define-success = %v", aliases["define-success"])
	}
	if len(byName) != 7 {
		t.Fatalf("product.yaml carries %d commands, want 7", len(byName))
	}
}
