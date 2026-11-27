package server

import (
	"fmt"
	"net/http"
	"strings"
)

// /btw side questions. The person asks something on the side while a chat may still be working. The server forks the
// chat into a hidden copy (a btw- key, hidden from the tab strip and agent lists like a user-view chat) and runs one
// read-only turn there. The source chat never sees the question. The app shows the answer above the message box;
// "Open as tab" forks the copy into a normal chat under the source, and Dismiss removes the copy.

const (
	btwChatPrefix       = "btw-"
	btwQuestionMaxRunes = 4000
	btwNameMaxRunes     = 48
)

func isBtwConversationKey(key string) bool {
	_, chatID, ok := strings.Cut(strings.TrimSpace(key), codeChatSideMarker)
	return ok && strings.HasPrefix(chatID, btwChatPrefix)
}

// btwChatName names the copy after its question: "btw: <first words>".
func btwChatName(question string) string {
	words := strings.Join(strings.Fields(question), " ")
	runes := []rune(words)
	if len(runes) > btwNameMaxRunes {
		words = strings.TrimSpace(string(runes[:btwNameMaxRunes])) + "…"
	}
	return "btw: " + words
}

// btwFirstTurnNote is shown to the copy's agent with the question (the fork note, hidden from the person's bubble).
func btwFirstTurnNote(parentName string) string {
	return fmt.Sprintf("[Side question (/btw). This is a hidden copy of %q, made so the person can ask something on the side while %q keeps working; %q does not see this question or your answer. Answer the question below directly and briefly from the conversation so far. You may read files to check a detail, but do not edit files, run commands that change anything, or carry on %q's work.]", parentName, parentName, parentName, parentName)
}

// startBtwTurn sends the question to the copy as one plan-mode (read-only) turn. The query handler returns as soon
// as the turn starts; the app follows the copy's session for the answer.
func (api *StreamingAPI) startBtwTurn(r *http.Request, profileID, conversationKey, question string) error {
	vars := map[string]string{"id": profileID}
	status, body := scheduleHandler(r, api.handleAgentProfileChatQuery, http.MethodPost, "/api/agent-profiles/"+profileID+"/query", vars, nil,
		AgentProfileChatRequest{Message: question, ConversationKey: conversationKey, PlanMode: true})
	if status >= 400 {
		return fmt.Errorf("the side question could not start: %s", firstNonEmptyTrimmed(strings.TrimSpace(string(body)), http.StatusText(status)))
	}
	return nil
}
