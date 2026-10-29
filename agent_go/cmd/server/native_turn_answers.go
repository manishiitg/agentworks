package server

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

// Answers Claude finished inside one long chat turn (PLAT-872): the turn's own
// answer and the answers to steers sent while it ran. The chat history used to
// learn them only from the turn's final reply, so when that turn was released
// or cancelled the answers were never saved. The provider now reports each one
// as it finishes; the turn's normal history save (the same save path, also run
// on cancel) puts each answer right after the message it replies to.

type nativeTurnAnswer struct {
	Text    string
	Answers string
	At      time.Time
}

var nativeTurnAnswers = struct {
	sync.Mutex
	bySession map[string][]nativeTurnAnswer
}{bySession: map[string][]nativeTurnAnswer{}}

func recordNativeTurnAnswer(answer claudecode.SDKAnswer) {
	sessionID := strings.TrimSpace(answer.Owner)
	if sessionID == "" || strings.TrimSpace(answer.Text) == "" {
		return
	}
	nativeTurnAnswers.Lock()
	nativeTurnAnswers.bySession[sessionID] = append(nativeTurnAnswers.bySession[sessionID], nativeTurnAnswer{Text: answer.Text, Answers: answer.Answers, At: answer.At})
	count := len(nativeTurnAnswers.bySession[sessionID])
	nativeTurnAnswers.Unlock()
	log.Printf("[NATIVE_ANSWER] recorded session=%s answer=%d chars=%d", sessionID, count, len(answer.Text))
}

func takeNativeTurnAnswers(sessionID string) []nativeTurnAnswer {
	nativeTurnAnswers.Lock()
	defer nativeTurnAnswers.Unlock()
	answers := nativeTurnAnswers.bySession[sessionID]
	delete(nativeTurnAnswers.bySession, sessionID)
	return answers
}

// historyWithNativeTurnAnswers is the turn's history save with every answer
// Claude finished in it. An answer already saved (an assistant text equal to
// it) is not added again. A final reply that is just the turn's answers joined
// is replaced by the separate answers, each after the message it replies to.
func (api *StreamingAPI) historyWithNativeTurnAnswers(sessionID string, history []llmtypes.MessageContent) []llmtypes.MessageContent {
	answers := takeNativeTurnAnswers(sessionID)
	if len(answers) == 0 {
		return history
	}
	merged, added := mergeNativeTurnAnswers(history, answers)
	log.Printf("[NATIVE_ANSWER] saved session=%s answers=%d added=%d history=%d->%d", sessionID, len(answers), added, len(history), len(merged))
	return merged
}

const canceledNoResponseText = "[Request canceled — no response generated]"

func historyText(message llmtypes.MessageContent) string {
	var b strings.Builder
	for _, part := range message.Parts {
		if text, ok := part.(llmtypes.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

func normalizedHistoryText(text string) string { return strings.Join(strings.Fields(text), " ") }

func mergeNativeTurnAnswers(history []llmtypes.MessageContent, answers []nativeTurnAnswer) ([]llmtypes.MessageContent, int) {
	out := append([]llmtypes.MessageContent(nil), history...)
	if len(answers) > 1 {
		texts := make([]string, 0, len(answers))
		for _, a := range answers {
			texts = append(texts, strings.TrimSpace(a.Text))
		}
		joined := normalizedHistoryText(strings.Join(texts, "\n\n"))
		for i := len(out) - 1; i >= 0; i-- {
			if out[i].Role == llmtypes.ChatMessageTypeAI {
				if normalizedHistoryText(historyText(out[i])) == joined {
					out = append(out[:i], out[i+1:]...)
				}
				break
			}
		}
	}
	added, last := 0, -1
	for _, a := range answers {
		text := normalizedHistoryText(a.Text)
		existing := -1
		for i, m := range out {
			if m.Role == llmtypes.ChatMessageTypeAI && normalizedHistoryText(historyText(m)) == text {
				existing = i
				break
			}
		}
		if existing >= 0 {
			last = max(last, existing)
			continue
		}
		at := len(out)
		if prompt := normalizedHistoryText(a.Answers); prompt != "" {
			for i := len(out) - 1; i > last; i-- {
				if out[i].Role == llmtypes.ChatMessageTypeHuman && normalizedHistoryText(historyText(out[i])) == prompt {
					at = i + 1
					break
				}
			}
		}
		if at <= last {
			at = last + 1
		}
		message := llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: a.Text}}}
		out = append(out[:at], append([]llmtypes.MessageContent{message}, out[at:]...)...)
		last = at
		added++
	}
	// The cancel placeholder is not needed once the turn's answers are saved.
	if added > 0 && len(out) > 1 && out[len(out)-1].Role == llmtypes.ChatMessageTypeAI && historyText(out[len(out)-1]) == canceledNoResponseText &&
		out[len(out)-2].Role == llmtypes.ChatMessageTypeAI {
		out = out[:len(out)-1]
	}
	return out, added
}
