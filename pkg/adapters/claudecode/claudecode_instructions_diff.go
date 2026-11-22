package claudecode

import (
	"log"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A kept Claude Agent SDK process holds the instructions it was started with
// (its system prompt; a started or resumed process takes the current ones, as
// Claude Code's own prompt snapshots show) plus any changes sent to it since.
// It is sent only what changed: nothing when it already holds the current
// instructions, else the changed lines, else (a large change) the whole set.

// resentInstructionsChangedStart opens the changed lines of the instructions; it
// ends with resentInstructionsEnd like the whole set.
const resentInstructionsChangedStart = "[AgentWorks Instructions Changed]"

const resentInstructionsChangedNote = `Your AgentWorks instructions changed. Below are only the changed lines, as a diff: "-" lines are removed, "+" lines are added, other lines are unchanged context. Apply them to the instructions you already have; everything else stays as it was.`

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
	diff, ok := claudeInstructionsDiff(held, current)
	if ok {
		log.Printf("[CLI_INSTRUCTIONS] provider=claude-code session=%s native=%s action=started reason=changed_lines_needed chars=%d of=%d", owner, id, len(diff), len(current))
		prompt = resentInstructionsChangedStart + "\n" + resentInstructionsChangedNote + "\n\n" + diff + "\n\n" + resentInstructionsEnd + "\n" + prompt
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

// claudeInstructionsDiff is the changed lines from old to current with two lines
// of context, as a unified diff without line numbers. ok is false when the diff
// is not much smaller than current, so the whole set is sent instead.
func claudeInstructionsDiff(old, current string) (string, bool) {
	a, b := strings.Split(old, "\n"), strings.Split(current, "\n")
	// Trim the common head and tail so the table covers only the changed middle.
	head := 0
	for head < len(a) && head < len(b) && a[head] == b[head] {
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}
	midA, midB := a[head:len(a)-tail], b[head:len(b)-tail]
	if len(midA)*len(midB) > 4_000_000 {
		return "", false
	}
	// ops: ' ' kept, '-' removed, '+' added, over the whole of a and b.
	type op struct {
		kind byte
		line string
	}
	ops := make([]op, 0, len(a)+len(b))
	for _, line := range a[:head] {
		ops = append(ops, op{' ', line})
	}
	n, m := len(midA), len(midB)
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if midA[i] == midB[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	for i, j := 0, 0; i < n || j < m; {
		switch {
		case i < n && j < m && midA[i] == midB[j]:
			ops = append(ops, op{' ', midA[i]})
			i, j = i+1, j+1
		case j < m && (i == n || lcs[i][j+1] >= lcs[i+1][j]):
			ops = append(ops, op{'+', midB[j]})
			j++
		default:
			ops = append(ops, op{'-', midA[i]})
			i++
		}
	}
	for _, line := range a[len(a)-tail:] {
		ops = append(ops, op{' ', line})
	}
	const context = 2
	show := make([]bool, len(ops))
	changed := false
	for i, o := range ops {
		if o.kind == ' ' {
			continue
		}
		changed = true
		for k := max(0, i-context); k <= min(len(ops)-1, i+context); k++ {
			show[k] = true
		}
	}
	if !changed {
		return "", false
	}
	var out strings.Builder
	for i, o := range ops {
		if !show[i] {
			continue
		}
		if i == 0 || !show[i-1] {
			out.WriteString("@@\n")
		}
		out.WriteByte(o.kind)
		out.WriteString(o.line)
		out.WriteByte('\n')
	}
	diff := strings.TrimRight(out.String(), "\n")
	return diff, len(diff) <= len(current)/2
}
