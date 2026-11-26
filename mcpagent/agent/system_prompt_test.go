package mcpagent

import (
	"strings"
	"testing"
)

// TestSetInstructionsReAppendsSupplementaryPrompts verifies that calling
// SetInstructions (overwrite) does NOT lose prompts added via AddInstructions.
// This is the core contract that execution-only agents rely on: supplementary
// prompts (CDP, browser, skills, secrets) are appended once during setup, then
// SetInstructions is called with a new base prompt during Execute().
func TestSetInstructionsReAppendsSupplementaryPrompts(t *testing.T) {
	a := &Agent{}

	// Step 1: Initial system prompt set during agent initialization
	a.setInstructions("Initial MCP base prompt")

	// Step 2: Supplementary prompts appended after setup (simulates appendSupplementaryPrompts)
	a.appendInstructions("## Skills\nYou have access to agent-browser skill")
	a.appendInstructions("## Browser Mode: CDP\nuse host.docker.internal:9222")
	a.appendInstructions("## Secrets\nAPI_KEY=***")

	// Verify all appended prompts are present
	prompt := a.instructions()
	for _, expected := range []string{"Skills", "CDP", "Secrets"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("after AddInstructions, expected prompt to contain %q, got:\n%s", expected, prompt)
		}
	}

	// Step 3: Execute() calls SetInstructions with a completely new base prompt (overwrite=true)
	a.setInstructions("# Execution-Only Agent\n## Code Execution Mode\nCODE EXECUTION MODE")

	// The new base prompt must be present
	final := a.instructions()
	if !strings.Contains(final, "Execution-Only Agent") {
		t.Fatal("new base prompt not found after SetInstructions overwrite")
	}

	// The old base prompt must be gone
	if strings.Contains(final, "Initial MCP base prompt") {
		t.Fatal("old base prompt should have been replaced")
	}

	// All supplementary prompts must survive the overwrite
	for _, expected := range []string{
		"## Skills\nYou have access to agent-browser skill",
		"## Browser Mode: CDP\nuse host.docker.internal:9222",
		"## Secrets\nAPI_KEY=***",
	} {
		if !strings.Contains(final, expected) {
			t.Fatalf("supplementary prompt lost after SetInstructions overwrite.\nExpected to contain: %q\nGot:\n%s", expected, final)
		}
	}
}

func TestAddInstructionsRecordsSupplements(t *testing.T) {
	a := &Agent{}
	a.setInstructions("base")

	a.appendInstructions("prompt-A")
	a.appendInstructions("prompt-B")
	a.appendInstructions("prompt-C")

	appended := a.appendedSystemPrompts
	if len(appended) != 3 {
		t.Fatalf("expected 3 appended prompts, got %d", len(appended))
	}
	if appended[0] != "prompt-A" || appended[1] != "prompt-B" || appended[2] != "prompt-C" {
		t.Fatalf("appended prompts mismatch: %v", appended)
	}

	// SetInstructions should NOT clear the appended list
	a.setInstructions("new base")
	appended = a.appendedSystemPrompts
	if len(appended) != 3 {
		t.Fatalf("SetInstructions cleared appended prompts, expected 3, got %d", len(appended))
	}
}

// TestResetInstructions verifies that after clearing, SetInstructions
// no longer re-appends anything.
func TestResetInstructions(t *testing.T) {
	a := &Agent{}
	a.setInstructions("base")
	a.appendInstructions("## CDP\nhost.docker.internal:9222")

	a.resetInstructions("clean base")

	final := a.instructions()
	if strings.Contains(final, "CDP") {
		t.Fatal("cleared prompt should not be re-appended")
	}
	if final != "clean base" {
		t.Fatalf("expected exactly 'clean base', got: %q", final)
	}
}

// Clearing is itself sufficient: supplements are no longer materialized into
// the base prompt, so callers do not have to remember a SetInstructions re-base.
func TestResetInstructionsTakesEffectWithoutRebase(t *testing.T) {
	a := &Agent{}
	a.setInstructions("base")
	a.appendInstructions("## CDP\nhost.docker.internal:9222")

	a.resetInstructions("base")

	if got := a.instructions(); got != "base" {
		t.Fatalf("clearing supplements left stale materialized text: %q", got)
	}
}

// TestAddInstructionsEmpty verifies that appending an empty string is a no-op.
func TestAddInstructionsEmpty(t *testing.T) {
	a := &Agent{}
	a.setInstructions("base")
	a.appendInstructions("")

	if len(a.appendedSystemPrompts) != 0 {
		t.Fatal("empty append should be a no-op")
	}
	if a.instructions() != "base" {
		t.Fatalf("prompt changed after empty append: %q", a.instructions())
	}
}

// TestMultipleSetInstructionsKeepsAppended verifies that calling SetInstructions
// multiple times (e.g., across retry attempts) always preserves the appended prompts.
func TestMultipleSetInstructionsKeepsAppended(t *testing.T) {
	a := &Agent{}
	a.setInstructions("init")
	a.appendInstructions("## CDP\nport 9222")

	// Simulate multiple execution retries, each calling SetInstructions
	for i := 0; i < 3; i++ {
		a.setInstructions("execution attempt " + string(rune('1'+i)))
		final := a.instructions()
		if !strings.Contains(final, "## CDP\nport 9222") {
			t.Fatalf("retry %d: CDP prompt lost after SetInstructions", i+1)
		}
	}

	// Appended list should still have exactly 1 entry, not duplicated
	if len(a.appendedSystemPrompts) != 1 {
		t.Fatalf("expected 1 appended prompt, got %d", len(a.appendedSystemPrompts))
	}
}
