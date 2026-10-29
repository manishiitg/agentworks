package server

import (
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func historyMessage(role llmtypes.ChatMessageType, text string) llmtypes.MessageContent {
	return llmtypes.MessageContent{Role: role, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}}}
}

func historyShape(history []llmtypes.MessageContent) string {
	rows := make([]string, 0, len(history))
	for _, m := range history {
		rows = append(rows, string(m.Role)+":"+historyText(m))
	}
	return strings.Join(rows, " | ")
}

// PLAT-872 (laptop, 2026-10-29): Claude answered several steers inside one long
// held turn; the turn was then cancelled and the saved history kept the steers
// but none of the answers. Each answer is saved after the message it replies
// to, a joined final reply is split the same way, and a second save adds nothing.
func TestNativeTurnAnswersAreSavedAfterTheirMessages(t *testing.T) {
	answers := []nativeTurnAnswer{{Text: "A0", Answers: "start the app"}, {Text: "A1", Answers: "use port 4000"}, {Text: "A2", Answers: "add a login page"}}
	want := "system:sys | human:start the app | ai:A0 | human:use port 4000 | ai:A1 | human:add a login page | ai:A2"

	canceled := []llmtypes.MessageContent{
		historyMessage(llmtypes.ChatMessageTypeSystem, "sys"),
		historyMessage(llmtypes.ChatMessageTypeHuman, "start the app"),
		historyMessage(llmtypes.ChatMessageTypeHuman, "use port 4000"),
		historyMessage(llmtypes.ChatMessageTypeHuman, "add a login page"),
		historyMessage(llmtypes.ChatMessageTypeAI, canceledNoResponseText),
	}
	got, added := mergeNativeTurnAnswers(canceled, answers)
	if historyShape(got) != want || added != 3 {
		t.Fatalf("cancelled turn: added=%d\n got %s\nwant %s", added, historyShape(got), want)
	}
	if again, added := mergeNativeTurnAnswers(got, answers); historyShape(again) != want || added != 0 {
		t.Fatalf("second save changed history: added=%d %s", added, historyShape(again))
	}

	completed := append(append([]llmtypes.MessageContent(nil), canceled[:4]...), historyMessage(llmtypes.ChatMessageTypeAI, "A0\n\nA1\n\nA2"))
	if got, _ := mergeNativeTurnAnswers(completed, answers); historyShape(got) != want {
		t.Fatalf("completed turn:\n got %s\nwant %s", historyShape(got), want)
	}
}
