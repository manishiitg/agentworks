package claudecode

import (
	"log"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/instructiondiff"
)

// A kept Claude Agent SDK process holds the instructions it was started with
// (its system prompt; a started or resumed process takes the current ones, as
// Claude Code's own prompt snapshots show) plus any changes sent to it since.
// It is sent only what changed: nothing when it already holds the current
// instructions, else the changed lines, else (a large change) the whole set.

// resentInstructionsChangedStart opens the changed lines of the instructions; it
// ends with resentInstructionsEnd like the whole set.
const resentInstructionsChangedStart = "[AgentWorks Instructions Changed]"

// launchedWith records the instructions this process's system prompt carries;
// "" when it carries none (they reach it another way).
func (s *claudeSDKSession) launchedWith(instructions string) {
	s.mu.Lock()
	s.launchInstructions = instructions
	s.heldInstructions = instructions
	s.mu.Unlock()
}

// compacted is called after Claude compacted this process's conversation: the
// changes sent in messages may be gone; its system prompt stays. It reports
// whether the process still holds current.
func (s *claudeSDKSession) compacted(opts *llmtypes.CallOptions, id, current string) bool {
	s.mu.Lock()
	s.heldInstructions = s.launchInstructions
	kept := s.launchInstructions != "" && s.launchInstructions == current
	s.mu.Unlock()
	if kept {
		log.Printf("[CLI_INSTRUCTIONS] provider=claude-code session=%s native=%s action=skip reason=compacted_process_has_current", claudeInteractiveSessionIDFromOptions(opts), id)
	}
	return kept
}

// currentInstructionsPrompt adds to prompt what this process lacks of current,
// and returns the receipt to run once the turn succeeded. When the process's
// instructions are unknown it falls back to the whole-set refresh.
func (s *claudeSDKSession) currentInstructionsPrompt(opts *llmtypes.CallOptions, cwd, id, current, prompt string, resumed bool) (string, func()) {
	s.mu.Lock()
	held := s.heldInstructions
	s.mu.Unlock()
	if held == "" || strings.TrimSpace(current) == "" || strings.TrimSpace(prompt) == "" || id == "" {
		return claudeCurrentResumeInstructions(opts, cwd, id, current, prompt, resumed)
	}
	// The receipt keeps the delivery marker the other Claude paths read.
	_, writeMarker := claudeCurrentResumeInstructions(opts, cwd, id, current, prompt, false)
	owner := claudeInteractiveSessionIDFromOptions(opts)
	if held == current {
		return prompt, writeMarker
	}
	diff, ok := instructiondiff.Lines(held, current)
	if ok {
		log.Printf("[CLI_INSTRUCTIONS] provider=claude-code session=%s native=%s action=started reason=changed_lines_needed chars=%d of=%d", owner, id, len(diff), len(current))
		prompt = resentInstructionsChangedStart + "\n" + instructiondiff.Note + "\n\n" + diff + "\n\n" + resentInstructionsEnd + "\n" + prompt
	} else {
		log.Printf("[CLI_INSTRUCTIONS] provider=claude-code session=%s native=%s action=started reason=current_instructions_needed", owner, id)
		prompt = resentInstructionsStart + "\n" + current + "\n\n" + resentInstructionsEnd + "\n" + prompt
	}
	return prompt, func() {
		s.mu.Lock()
		s.heldInstructions = current
		s.mu.Unlock()
		writeMarker()
		log.Printf("[CLI_INSTRUCTIONS] provider=claude-code session=%s native=%s action=done reason=current_instructions_delivered", owner, id)
	}
}
