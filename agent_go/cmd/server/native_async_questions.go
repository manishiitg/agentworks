package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-764: accepted:true acknowledges the native async call. It does not
// answer the questions. Keep its card independent of the asking turn's context.
func (api *StreamingAPI) observeNativeAsyncQuestion(sessionID string, record llmtypes.NativeRecord) error {
	var input struct {
		Questions []struct {
			Title   string   `json:"title"`
			Options []string `json:"options"`
		} `json:"questions"`
	}
	if err := json.Unmarshal([]byte(record.Text), &input); err != nil {
		return fmt.Errorf("decode native async question: %w", err)
	}
	questions := make([]unifiedevents.CodingAgentQuestionPrompt, 0, len(input.Questions))
	for i, q := range input.Questions {
		id, _ := json.Marshal([]any{"request_user_input_async", record.ID, i})
		prompt := unifiedevents.CodingAgentQuestionPrompt{ID: string(id), Question: q.Title, AllowOther: true}
		for _, label := range q.Options {
			prompt.Options = append(prompt.Options, unifiedevents.CodingAgentQuestionOption{Label: label})
		}
		questions = append(questions, prompt)
	}
	questions, err := parseCodingAgentClarification(map[string]interface{}{"questions": questions})
	if err != nil {
		return err
	}
	identity := sha256.Sum256([]byte(record.SessionID + "\x00" + record.ID))
	promptID := clarificationPromptPrefix + fmt.Sprintf("async-%x", identity[:16])
	api.codingAgentClarificationsMu.Lock()
	defer api.codingAgentClarificationsMu.Unlock()
	if p := api.codingAgentClarifications[sessionID]; p != nil {
		if p.promptID == promptID {
			return nil
		}
		return fmt.Errorf("another clarification is pending")
	}
	// Replay must not resurrect an answered/expired card. This is only done on
	// question records, never on routine activity/status polls.
	for _, event := range api.eventStore.GetAllEventsRaw(sessionID) {
		if event.ID == promptID+":requested" || event.ID == promptID+":settled" {
			return nil
		}
	}
	when := record.Timestamp
	if when.IsZero() {
		when = time.Now()
	}
	p := &pendingCodingAgentClarification{
		sessionID: sessionID, provider: "codex-cli", promptID: promptID,
		questions: questions, answers: make(chan []unifiedevents.CodingAgentQuestionAnswer, 1),
		ctx: context.Background(), askedAt: when, turnID: record.TurnID, nativeAsync: true,
	}
	if api.codingAgentClarifications == nil {
		api.codingAgentClarifications = make(map[string]*pendingCodingAgentClarification)
	}
	if err := api.emitCodingAgentClarification(p, "requested", "", nil); err != nil {
		return err
	}
	api.codingAgentClarifications[sessionID] = p
	log.Printf("[HUMAN_INPUT] session=%s prompt=%s native_call=%s status=pending transport=native_async", sessionID, promptID, record.ID)
	// Expiry is explicitly unanswered, never permission or an assumed choice.
	time.AfterFunc(max(time.Until(when.Add(24*time.Hour)), 0), func() {
		api.codingAgentClarificationsMu.Lock()
		defer api.codingAgentClarificationsMu.Unlock()
		if api.codingAgentClarifications[sessionID] != p {
			return
		}
		if err := api.emitCodingAgentClarification(p, "settled", clarificationOutcomeUnanswered, nil); err != nil {
			log.Printf("[HUMAN_INPUT] session=%s prompt=%s status=expiry_failed", sessionID, promptID)
			return
		}
		delete(api.codingAgentClarifications, sessionID)
	})
	return nil
}

func nativeAsyncQuestionReply(questions []unifiedevents.CodingAgentQuestionPrompt, answers []unifiedevents.CodingAgentQuestionAnswer) string {
	type reply struct {
		QuestionItemID string `json:"questionItemId"`
		Question       string `json:"question"`
		Answer         string `json:"answer"`
	}
	items := make([]reply, 0, len(answers))
	for i, answer := range answers {
		parts := append([]string(nil), answer.SelectedLabels...)
		if answer.OtherText != "" {
			parts = append(parts, answer.OtherText)
		}
		items = append(items, reply{answer.ID, questions[i].Question, strings.Join(parts, ", ")})
	}
	data, _ := json.Marshal(items)
	return "<send_user_message_question_reply>\n" + string(data) + "\n</send_user_message_question_reply>"
}
