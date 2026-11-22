package claudecode

import (
	"log"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/instructions"
)

// claudeInstructionsPrompt puts at the top of prompt what a kept process lacks of current (see
// internal/instructions), and returns the receipt to run once the turn succeeded.
func claudeInstructionsPrompt(t *instructions.Tracker, owner, native, current, prompt string) (string, func()) {
	d, receipt := t.Next(current)
	if d.Kind == instructions.None {
		return prompt, receipt
	}
	reason := "changed_lines_needed"
	if d.Kind == instructions.Full {
		reason = "current_instructions_needed"
	}
	log.Printf("[CLI_INSTRUCTIONS] provider=claude-code session=%s native=%s action=started reason=%s chars=%d of=%d", owner, native, reason, len(d.Text), len(current))
	return instructions.InMessage(d, prompt), func() {
		receipt()
		log.Printf("[CLI_INSTRUCTIONS] provider=claude-code session=%s native=%s action=done reason=current_instructions_delivered", owner, native)
	}
}
