package codexcli

import "testing"

// Captured live from Codex 0.153.4 (2026-09-06) a few seconds after a prompt
// was submitted: the transcript echoes the prompt, the activity line has no
// bullet, and the EMPTY composer shows the new fixed placeholder below it.
const codex0153WorkingPane = `╰────────────────────────────────────────────────────────╯
› Reply with exactly: astra medium works.
• astra medium works.
• You have 2 usage limit resets available. Run /usage to use one.
› Count from 1 to 5, one number per line.
Working (0s • esc to interrupt)
› Ask Codex to do anything
  gpt-6-astra medium · ~/Library/Application Support/sparkquill-desktop/workspace-docs/_users/default/Chats/SparkQuill
`

// The same session idle, before the prompt.
const codex0153IdlePane = `╰────────────────────────────────────────────────────────╯
› Reply with exactly: astra medium works.
• astra medium works.
• You have 2 usage limit resets available. Run /usage to use one.
› Ask Codex to do anything
  gpt-6-astra medium · ~/Library/Application Support/sparkquill-desktop/workspace-docs/_users/default/Chats/SparkQuill
`

func TestCodex0153PlaceholderIsAnEmptyComposer(t *testing.T) {
	if !isCodexGhostPlaceholderPromptLine("› Ask Codex to do anything") {
		t.Fatal("codex 0.153's fixed placeholder must read as an empty composer")
	}
	if isCodexPromptWithInputLine("› Ask Codex to do anything") {
		t.Fatal("the placeholder must not count as typed input")
	}
	if !isCodexPromptWithInputLine("› Count from 1 to 5, one number per line.") {
		t.Fatal("a real prompt line must still count as input")
	}
}

func TestCodex0153WorkingPaneCountsAsActivity(t *testing.T) {
	if !hasCodexActivity(codex0153WorkingPane) {
		t.Fatal("the 0.153 pane with 'Working (… esc to interrupt)' above the empty composer must register as activity; this is what confirms the submission")
	}
	if hasCodexActivity(codex0153IdlePane) {
		t.Fatal("an idle 0.153 pane must not register as activity")
	}
	if !codexPaneHasEmptyComposer(codex0153IdlePane) {
		t.Fatal("the idle 0.153 pane shows an empty composer")
	}
	if !hasCodexReadyPrompt(codex0153IdlePane) {
		t.Fatal("the idle 0.153 pane must read as a ready prompt")
	}
}
