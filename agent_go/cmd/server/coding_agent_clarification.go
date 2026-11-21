package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/pkg/codingtimeout"
)

const clarificationPromptPrefix = "clarification:"

type pendingCodingAgentClarification struct {
	sessionID      string
	provider       string
	promptID       string
	questions      []unifiedevents.CodingAgentQuestionPrompt
	answers        chan []unifiedevents.CodingAgentQuestionAnswer
	ctx            context.Context
	askedAt        time.Time
	expiresAt      time.Time
	ask            *crewAskInputScope // Non-nil only for an external, attended Crew Ask.
	turnID         string
	askedTurn      string // the turn that asked, as clarificationTurnToken saw it
	recovered      bool   // ordinary question restored as new-message input, never a native approval
	requestKind    string // trusted provider/tool request type, not inferred from its prose
	nativeAsync    bool   // transcript question; answer continues the admitted chat instead of a waiting tool
	closureMessage string // Optional server explanation shown when a card closes.
}

func codingAgentClarificationSchema() map[string]interface{} {
	option := map[string]interface{}{"type": "object", "required": []string{"label"}, "additionalProperties": false, "properties": map[string]interface{}{
		"label":       map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 500},
		"description": map[string]interface{}{"type": "string", "maxLength": 1000},
	}}
	question := map[string]interface{}{"type": "object", "required": []string{"id", "question", "options"}, "additionalProperties": false, "properties": map[string]interface{}{
		"id":             map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 100},
		"header":         map[string]interface{}{"type": "string", "maxLength": 100},
		"question":       map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 20000},
		"options":        map[string]interface{}{"type": "array", "minItems": 0, "maxItems": 12, "items": option},
		"multi_select":   map[string]interface{}{"type": "boolean"},
		"allow_other":    map[string]interface{}{"type": "boolean"},
		"min_selections": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 12},
		"max_selections": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 12},
	}}
	return map[string]interface{}{"type": "object", "required": []string{"questions"}, "additionalProperties": false, "properties": map[string]interface{}{
		"questions": map[string]interface{}{"type": "array", "minItems": 1, "maxItems": 12, "items": question,
			"description": "Each question: id, question, options as objects like {\"label\": \"Yes\", \"description\": \"optional\"} (no id on an option), optional multi_select and allow_other."},
	}}
}

// codingAgentNeedsClarificationRoute: registering request_clarification also
// makes it findable through search_tools, so it is registered only where
// something calls it: Cursor (offered on attended turns; its SDK cannot answer
// its own AskQuestion) and Claude's terminal, whose question hook posts the
// answer to this route. Claude's chat view (SDK), Codex, Pi, Muse and ACP
// agents ask with their own question tool and never see it (decisions log,
// 21 Nov).
func codingAgentNeedsClarificationRoute(provider, claudeTransport string) bool {
	switch {
	case strings.EqualFold(strings.TrimSpace(provider), "cursor-cli"):
		return true
	case strings.EqualFold(strings.TrimSpace(provider), "claude-code"):
		return !strings.EqualFold(strings.TrimSpace(claudeTransport), llmproviders.ClaudeCodeTransportSDK)
	}
	return false
}

// The platform bridge provides the same question/answer lifecycle to Claude,
// Codex and Muse. Only the attended Builder turn registers this tool; it must
// never leave an unattended workflow, bot or scheduled run waiting for a UI.
// unansweredAfter is codingAgentQuestionUnansweredAfter for the turn: 0 waits for the person.
func (api *StreamingAPI) registerCodingAgentClarificationTool(registrar definitionToolRegistrar, sessionID, provider string, unansweredAfter time.Duration) error {
	return registrar.RegisterCustomToolWithTimeout("request_clarification",
		"Ask the user to choose options in this chat and wait for their answers. Put related questions in a single call; give every question a unique id. Supports single choice, multi_select with selection bounds, and a custom text answer when allow_other is true. The user explicitly submits all answers together; subsequent calls create separate prompts. A cancelled or expired prompt returns an error, never an assumed answer. Wait for the result before doing anything else, and do not end your turn while the question is open; if you must end it, say that you will continue when the person answers.",
		codingAgentClarificationSchema(), func(ctx context.Context, args map[string]interface{}) (string, error) {
			// This registered tool is ordinary clarification, never a native permission callback.
			typed := make(map[string]interface{}, len(args)+1)
			for key, value := range args {
				typed[key] = value
			}
			typed["native_request_kind"] = "question"
			return api.waitForCodingAgentClarification(withQuestionUnansweredAfter(ctx, unansweredAfter), sessionID, provider, typed)
		}, codingtimeout.LongRunningMCPToolTimeout(), "human_tools")
}

func parseCodingAgentClarification(args map[string]interface{}) ([]unifiedevents.CodingAgentQuestionPrompt, error) {
	data, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("invalid clarification questions")
	}
	var input struct {
		Questions []unifiedevents.CodingAgentQuestionPrompt `json:"questions"`
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, fmt.Errorf("invalid clarification questions")
	}
	if len(input.Questions) == 0 || len(input.Questions) > 12 {
		return nil, fmt.Errorf("provide 1 to 12 questions")
	}
	ids := map[string]bool{}
	for i := range input.Questions {
		q := &input.Questions[i]
		q.ID, q.Question = strings.TrimSpace(q.ID), strings.TrimSpace(q.Question)
		if q.ID == "" || len(q.ID) > 100 || ids[q.ID] || q.Question == "" || len(q.Question) > 20000 || (len(q.Options) == 0 && !q.AllowOther) || len(q.Options) > 12 {
			return nil, fmt.Errorf("each question needs a unique id, text, and options or a custom answer field")
		}
		ids[q.ID] = true
		labels := map[string]bool{}
		for j := range q.Options {
			o := &q.Options[j]
			o.Label = strings.TrimSpace(o.Label)
			if o.Label == "" || len(o.Label) > 500 || len(o.Description) > 1000 || labels[o.Label] {
				return nil, fmt.Errorf("option labels must be nonempty and unique")
			}
			labels[o.Label] = true
		}
		if !q.MultiSelect && (q.MinSelections > 1 || q.MaxSelections > 1) {
			return nil, fmt.Errorf("selection bounds require multi_select")
		}
		min, max := clarificationSelectionBounds(*q)
		available := len(q.Options)
		if q.AllowOther {
			available++
		}
		if q.MinSelections < 0 || q.MaxSelections < 0 || min > max || min > available || max > available {
			return nil, fmt.Errorf("invalid selection bounds for %s", q.ID)
		}
	}
	return input.Questions, nil
}

func clarificationSelectionBounds(q unifiedevents.CodingAgentQuestionPrompt) (int, int) {
	if !q.MultiSelect {
		return 1, 1
	}
	min, max := q.MinSelections, q.MaxSelections
	if min == 0 {
		min = 1
	}
	if max == 0 {
		max = len(q.Options)
		if q.AllowOther {
			max++
		}
	}
	return min, max
}

func (api *StreamingAPI) emitCodingAgentClarification(p *pendingCodingAgentClarification, kind, outcome string, answers []unifiedevents.CodingAgentQuestionAnswer) error {
	if p.ask != nil {
		return nil // External questions belong only to their asking connection.
	}
	now := time.Now()
	data := &unifiedevents.CodingAgentQuestionEvent{
		BaseEventData: unifiedevents.BaseEventData{Timestamp: now, SessionID: p.sessionID, Metadata: map[string]interface{}{"turn_id": p.turnID}},
		Provider:      p.provider, PromptID: p.promptID, Kind: kind, Outcome: outcome, Answers: answers,
	}
	if p.requestKind != "" {
		data.Metadata["native_request_kind"] = p.requestKind
	}
	if p.recovered {
		data.Metadata["response_mode"] = "message"
	}
	if !p.expiresAt.IsZero() {
		data.Metadata["expires_at"] = p.expiresAt.UTC().Format(time.RFC3339Nano)
	}
	if kind == "requested" || kind == "recovered" {
		data.Questions = p.questions
	}
	if p.closureMessage != "" {
		data.Metadata["message"] = p.closureMessage
	}
	return api.eventStore.AddEventChecked(p.sessionID, events.Event{
		ID: p.promptID + ":" + kind, Type: "coding_agent_question", Timestamp: now, SessionID: p.sessionID,
		Data: &unifiedevents.AgentEvent{Type: unifiedevents.CodingAgentQuestion, Timestamp: now, SessionID: p.sessionID, Component: "system", Data: data},
	})
}

// pendingCodingAgentQuestion reports a question, plan or command approval that is waiting for the person in this
// chat. It is the live waiter itself, so the tab and sidebar show "Needs you" for as long as the agent is really
// stopped on it, whoever started the turn. External Ask questions belong to their asking connection, not the tab.
func (api *StreamingAPI) pendingCodingAgentQuestion(sessionID string) (time.Time, string, bool) {
	if api == nil || sessionID == "" {
		return time.Time{}, "", false
	}
	api.codingAgentClarificationsMu.Lock()
	defer api.codingAgentClarificationsMu.Unlock()
	var p *pendingCodingAgentClarification
	for _, candidate := range api.codingAgentClarifications {
		if candidate != nil && candidate.sessionID == sessionID && candidate.ask == nil && (p == nil || candidate.askedAt.Before(p.askedAt) || candidate.askedAt.Equal(p.askedAt) && candidate.promptID < p.promptID) {
			p = candidate
		}
	}
	if p == nil || p.ask != nil {
		return time.Time{}, "", false
	}
	message := ""
	if len(p.questions) > 0 {
		message = p.questions[0].Question
		if runes := []rune(message); len(runes) > 160 {
			message = string(runes[:157]) + "..."
		}
	}
	return p.askedAt, message, true
}

// agentMessageQuestionTimeout bounds a question asked in a turn another agent's message started (owner, 2026-10-28:
// "much faster like 5 min"). Nobody may be at the tab, so after this the question gets the unattended answer and the
// chat stops showing "Needs you". A turn the person started keeps waiting until answered or stopped. Var for tests.
var agentMessageQuestionTimeout = 5 * time.Minute

// clarificationOutcomeUnanswered settles a card nobody answered in time; the chat says it was answered automatically.
const clarificationOutcomeUnanswered = "unanswered"

// errNoAttendingUser is the answer an unattended turn's question gets today (llmtypes.AskCodingAgentQuestion without
// a handler): each provider then declines it, rejects the plan or denies the command, as in a schedule.
var errNoAttendingUser = errors.New("no attending user; ask in your reply")

// codingAgentQuestionUnansweredAfter is how long this turn's questions wait for the person: only a turn delivered by
// another agent (AgentMessageInbox) is bounded.
func codingAgentQuestionUnansweredAfter(req *QueryRequest) time.Duration {
	if req != nil && strings.TrimSpace(req.AgentMessageInbox) != "" {
		return agentMessageQuestionTimeout
	}
	return 0
}

type questionUnansweredAfterKey struct{}

func withQuestionUnansweredAfter(ctx context.Context, after time.Duration) context.Context {
	if after <= 0 {
		return ctx
	}
	return context.WithValue(ctx, questionUnansweredAfterKey{}, after)
}

func formatQuestionWait(d time.Duration) string {
	if d > 0 && d%time.Minute == 0 {
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return d.String()
}

func (api *StreamingAPI) waitForCodingAgentClarification(ctx context.Context, sessionID, provider string, args map[string]interface{}) (string, error) {
	questions, err := parseCodingAgentClarification(args)
	if err != nil {
		return "", err
	}
	ask := crewAskInputFromContext(ctx)
	if ask != nil {
		if ask.Session != sessionID || api.currentConversationTurnExecutionID(sessionID) != ask.Query {
			return "", fmt.Errorf("this Ask turn has ended; read your chat for its current status")
		}
		if err := validateCrewAskQuestions(args); err != nil {
			return "", err
		}
	}
	p := &pendingCodingAgentClarification{sessionID: sessionID, provider: provider, promptID: clarificationPromptPrefix + uuid.NewString(), questions: questions, answers: make(chan []unifiedevents.CodingAgentQuestionAnswer, 1), ctx: ctx, askedAt: time.Now(), ask: ask, turnID: api.currentConversationTurnExecutionID(sessionID), askedTurn: api.clarificationTurnToken(sessionID)}
	if after, _ := ctx.Value(questionUnansweredAfterKey{}).(time.Duration); after > 0 && ask == nil {
		p.expiresAt = p.askedAt.Add(after)
	}
	if kind, _ := args["native_request_kind"].(string); kind == "question" || kind == "command_approval" || kind == "plan_approval" || kind == "form" {
		p.requestKind = kind
	}
	api.codingAgentClarificationsMu.Lock()
	if api.codingAgentClarifications == nil {
		api.codingAgentClarifications = map[string]*pendingCodingAgentClarification{}
	}
	if api.codingAgentClarifications[sessionID] != nil {
		api.codingAgentClarificationsMu.Unlock()
		return "", fmt.Errorf("answer the pending clarification before asking another")
	}
	api.codingAgentClarifications[sessionID] = p
	if ask != nil {
		log.Printf("[CREW_ASK_INPUT] session=%s query=%s prompt=%s status=pending", sessionID, ask.Query, p.promptID)
	}
	err = api.emitCodingAgentClarification(p, "requested", "", nil)
	if err != nil {
		delete(api.codingAgentClarifications, sessionID)
	}
	api.codingAgentClarificationsMu.Unlock()
	if err != nil {
		return "", err
	}
	var unanswered <-chan time.Time
	after, _ := ctx.Value(questionUnansweredAfterKey{}).(time.Duration)
	if after > 0 && ask == nil {
		timer := time.NewTimer(after)
		defer timer.Stop()
		unanswered = timer.C
	}
	select {
	case <-unanswered:
		api.codingAgentClarificationsMu.Lock()
		if api.codingAgentClarifications[sessionID] == p {
			delete(api.codingAgentClarifications, sessionID)
			_ = api.emitCodingAgentClarification(p, "settled", clarificationOutcomeUnanswered, nil)
			api.codingAgentClarificationsMu.Unlock()
			log.Printf("[QUESTIONS] auto-answered after=%s session=%s reason=unanswered agent_message=true prompt=%s provider=%s", formatQuestionWait(after), sessionID, p.promptID, provider)
			return "", errNoAttendingUser
		}
		api.codingAgentClarificationsMu.Unlock()
		// The entry is gone: a person's answer was recorded first, or Stop, a newer question or teardown removed it.
		select {
		case answers := <-p.answers:
			result, err := json.Marshal(map[string]interface{}{"status": "answered", "prompt_id": p.promptID, "answers": answers})
			return string(result), err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	case answers := <-p.answers:
		log.Printf("[HUMAN_INPUT] prompt=%s session=%s provider=%s waiter=consumed after_ms=%d", p.promptID, sessionID, provider, time.Since(p.askedAt).Milliseconds())
		result, err := json.Marshal(map[string]interface{}{"status": "answered", "prompt_id": p.promptID, "answers": answers})
		return string(result), err
	case <-ctx.Done():
		api.codingAgentClarificationsMu.Lock()
		// A submitted answer wins if it was durably recorded before cancellation.
		if api.codingAgentClarifications[sessionID] == p {
			delete(api.codingAgentClarifications, sessionID)
			preserve := errors.Is(context.Cause(ctx), errAppRestart) && p.requestKind == "question" && p.ask == nil && (provider == "claude-code" || provider == "codex-cli")
			if !preserve {
				_ = api.emitCodingAgentClarification(p, "settled", "interrupted", nil)
			}
			api.codingAgentClarificationsMu.Unlock()
			if preserve {
				log.Printf("[HUMAN_INPUT] prompt=%s session=%s status=pending reason=restart_question_preserved", p.promptID, sessionID)
			}
			_, hasDeadline := ctx.Deadline()
			log.Printf("[HUMAN_INPUT] prompt=%s session=%s provider=%s waiter=interrupted after_ms=%d err=%v deadline_set=%t", p.promptID, sessionID, provider, time.Since(p.askedAt).Milliseconds(), ctx.Err(), hasDeadline)
			return "", ctx.Err()
		}
		api.codingAgentClarificationsMu.Unlock()
		answers := <-p.answers
		log.Printf("[HUMAN_INPUT] prompt=%s session=%s provider=%s waiter=consumed_after_cancel after_ms=%d", p.promptID, sessionID, provider, time.Since(p.askedAt).Milliseconds())
		result, err := json.Marshal(map[string]interface{}{"status": "answered", "prompt_id": p.promptID, "answers": answers})
		return string(result), err
	}
}

func clarificationAnswers(questions []unifiedevents.CodingAgentQuestionPrompt, auto bool, input []codingAgentQuestionAnswerInput) ([]unifiedevents.CodingAgentQuestionAnswer, error) {
	if !auto && len(input) != len(questions) {
		return nil, fmt.Errorf("answer every question")
	}
	answers := make([]unifiedevents.CodingAgentQuestionAnswer, 0, len(questions))
	for i, q := range questions {
		min, max := clarificationSelectionBounds(q)
		var labels []string
		var other string
		if auto {
			if len(q.Options) < min {
				return nil, fmt.Errorf("type an answer for %s", q.ID)
			}
			for _, option := range q.Options[:min] {
				labels = append(labels, option.Label)
			}
		} else {
			if input[i].ID != q.ID {
				return nil, fmt.Errorf("question order changed")
			}
			labels = input[i].SelectedLabels
			other = strings.TrimSpace(input[i].OtherText)
			if len(labels) == 0 && input[i].SelectedLabel != "" {
				labels = []string{input[i].SelectedLabel}
			}
		}
		count := len(labels)
		if other != "" {
			if !q.AllowOther || len(other) > 2000 {
				return nil, fmt.Errorf("custom answer is not allowed or too long for %s", q.ID)
			}
			count++
		}
		if count < min || count > max {
			return nil, fmt.Errorf("choose %d to %d options for %s", min, max, q.ID)
		}
		seen := map[string]bool{}
		for _, label := range labels {
			valid := false
			for _, option := range q.Options {
				if label == option.Label {
					valid = true
					break
				}
			}
			if !valid || seen[label] {
				return nil, fmt.Errorf("invalid option for %s", q.ID)
			}
			seen[label] = true
		}
		answers = append(answers, unifiedevents.CodingAgentQuestionAnswer{ID: q.ID, SelectedLabels: append([]string(nil), labels...), OtherText: other})
	}
	return answers, nil
}

// clarificationOutcome says what a submitted answer needs next: when the turn that asked has already ended, the
// waiting tool call can no longer hand the answer to the agent, so the server continues the chat itself.
type clarificationOutcome struct {
	askingTurnEnded bool
	message         string
}

func (api *StreamingAPI) submitCodingAgentClarification(sessionID, provider, promptID string, auto bool, input []codingAgentQuestionAnswerInput) error {
	_, err := api.submitCodingAgentClarificationOutcome(sessionID, provider, promptID, auto, input)
	return err
}

func (api *StreamingAPI) submitCodingAgentClarificationOutcome(sessionID, provider, promptID string, auto bool, input []codingAgentQuestionAnswerInput) (clarificationOutcome, error) {
	api.codingAgentClarificationsMu.Lock()
	defer api.codingAgentClarificationsMu.Unlock()
	p := api.codingAgentClarificationLocked(sessionID, promptID)
	if p == nil || p.promptID != promptID || p.provider != provider || p.ctx.Err() != nil {
		return clarificationOutcome{}, fmt.Errorf("clarification is no longer pending")
	}
	if p.ask != nil {
		return clarificationOutcome{}, fmt.Errorf("answer this question through the Crew Ask connection that started it")
	}
	answers, err := clarificationAnswers(p.questions, auto, input)
	if err != nil {
		return clarificationOutcome{}, err
	}
	if p.nativeAsync && !p.recovered {
		api.lastQueryMu.RLock()
		_, admitted := api.lastQueryRequests[sessionID]
		api.lastQueryMu.RUnlock()
		if !admitted {
			return clarificationOutcome{}, fmt.Errorf("send a message in this chat before answering this question")
		}
	}
	if err := api.settleCodingAgentClarificationLocked(p, auto, input); err != nil {
		return clarificationOutcome{}, err
	}
	// One line per answer, ids and numbers only: was the agent's turn still running when the person answered?
	// An answer that arrives after its turn ended reaches a waiter nobody is reading (PLAT-825-35).
	current := api.clarificationTurnToken(sessionID)
	ended := clarificationAskingTurnEnded(p.askedTurn, current)
	log.Printf("[HUMAN_INPUT] prompt=%s session=%s provider=%s answered turn=%s asked_turn=%s asking_turn_ended=%t after_ms=%d", promptID, sessionID, provider, turnTokenKind(current), turnTokenKind(p.askedTurn), ended, time.Since(p.askedAt).Milliseconds())
	message := clarificationFollowUpMessage(p.questions, answers)
	if p.recovered {
		ended = true
		message = recoveredQuestionReply(p.questions, answers)
	} else if p.nativeAsync {
		ended = true // Async tools already returned an acknowledgement, never an answer waiter.
		message = nativeAsyncQuestionReply(p.questions, answers)
	}
	return clarificationOutcome{askingTurnEnded: ended, message: message}, nil
}

// clarificationTurnToken names the turn running in a chat: what occupies it ("" when idle) and, for a retained CLI
// session, that turn's id, joined with "|".
func (api *StreamingAPI) clarificationTurnToken(sessionID string) string {
	occupant := api.conversationTurnOccupiedBy(sessionID)
	if occupant == "" && api.isSessionBusy(sessionID) {
		occupant = "busy_flag"
	}
	id := ""
	if retained, ok := mcpagent.LookupSession(sessionID); ok {
		id = retained.ActiveTurnID()
	}
	return occupant + "|" + id
}

func splitTurnToken(token string) (occupant, id string) {
	occupant, id, _ = strings.Cut(token, "|")
	return occupant, id
}

func turnTokenKind(token string) string {
	if occupant, _ := splitTurnToken(token); occupant != "" {
		return occupant
	}
	return "none"
}

// clarificationAskingTurnEnded: the turn that asked has ended when the chat is now idle, or when it has a different
// turn than the one that asked (a retained session's turn id changed).
func clarificationAskingTurnEnded(asked, current string) bool {
	occupant, id := splitTurnToken(current)
	if occupant == "" && id == "" {
		return true
	}
	_, askedID := splitTurnToken(asked)
	return askedID != "" && id != askedID
}

// clarificationFollowUpMessage is the follow-up in the person's words: what they chose.
func clarificationFollowUpMessage(questions []unifiedevents.CodingAgentQuestionPrompt, answers []unifiedevents.CodingAgentQuestionAnswer) string {
	parts := make([]string, 0, len(answers))
	for i, answer := range answers {
		chosen := append([]string(nil), answer.SelectedLabels...)
		if other := strings.TrimSpace(answer.OtherText); other != "" {
			chosen = append(chosen, other)
		}
		text := strings.Join(chosen, ", ")
		if len(answers) > 1 && i < len(questions) {
			label := strings.TrimSpace(questions[i].Header)
			if label == "" {
				label = strings.TrimSpace(questions[i].Question)
			}
			if len([]rune(label)) > 60 {
				label = string([]rune(label)[:60])
			}
			text = label + ": " + text
		}
		parts = append(parts, text)
	}
	return "You chose: " + strings.Join(parts, "; ")
}

// clarificationFollowUps remembers which prompts already started a follow-up turn, so an answer continues the chat
// once and never twice.
var clarificationFollowUps sync.Map

// startClarificationFollowUp continues the chat with the person's answer when the turn that asked has already ended.
// It sends "You chose: ..." the way the person typing it would (the same /api/query path and the same person), using
// the chat's last request for its settings. Logged either way, once per prompt id (PLAT-825-35).
func (api *StreamingAPI) startClarificationFollowUp(r *http.Request, sessionID, promptID, message string, recovered ...QueryRequest) {
	if _, already := clarificationFollowUps.LoadOrStore(promptID, true); already {
		return
	}
	api.lastQueryMu.RLock()
	prior, ok := api.lastQueryRequests[sessionID]
	api.lastQueryMu.RUnlock()
	if len(recovered) == 1 {
		prior, ok = recovered[0], true
	}
	if !ok {
		log.Printf("[HUMAN_INPUT] prompt=%s session=%s followup=skipped reason=no_prior_request", promptID, sessionID)
		return
	}
	prior.Query = message
	body, err := json.Marshal(prior)
	if err != nil {
		log.Printf("[HUMAN_INPUT] prompt=%s session=%s followup=skipped reason=encode_failed", promptID, sessionID)
		return
	}
	ctx := context.WithoutCancel(r.Context())
	go func() {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/query", bytes.NewReader(body))
		if err != nil {
			log.Printf("[HUMAN_INPUT] prompt=%s session=%s followup=skipped reason=request_failed", promptID, sessionID)
			return
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Session-ID", sessionID)
		recorder := httptest.NewRecorder()
		api.handleQuery(recorder, request)
		log.Printf("[HUMAN_INPUT] prompt=%s session=%s followup=started status=%d", promptID, sessionID, recorder.Code)
	}()
}

// Caller holds codingAgentClarificationsMu. Blocking tools have one waiter per
// chat; independent native async cards are keyed by their stable prompt ID.
func (api *StreamingAPI) codingAgentClarificationLocked(sessionID, promptID string) *pendingCodingAgentClarification {
	if p := api.codingAgentClarifications[sessionID]; p != nil && p.sessionID == sessionID && p.promptID == promptID {
		return p
	}
	if p := api.codingAgentClarifications[promptID]; p != nil && (p.nativeAsync || p.recovered) && p.sessionID == sessionID {
		return p
	}
	return nil
}

// Caller holds codingAgentClarificationsMu. Choice validation and consuming the
// waiter are shared by native UI cards and scoped external Ask answers.
func (api *StreamingAPI) settleCodingAgentClarificationLocked(p *pendingCodingAgentClarification, auto bool, input []codingAgentQuestionAnswerInput) error {
	answers, err := clarificationAnswers(p.questions, auto, input)
	if err != nil {
		return err
	}
	if err := api.emitCodingAgentClarification(p, "settled", "answered", answers); err != nil {
		return err
	}
	if p.nativeAsync || p.recovered {
		delete(api.codingAgentClarifications, p.promptID)
	} else {
		delete(api.codingAgentClarifications, p.sessionID)
	}
	p.answers <- answers
	return nil
}

// The relay derives sessionID from the authenticated sender's own shared thread.
// Never follow an ended question into a new turn under the owner's identity.
func (api *StreamingAPI) submitSharedAgentQuestion(sessionID, provider, promptID string, auto bool, input []codingAgentQuestionAnswerInput) error {
	api.codingAgentClarificationsMu.Lock()
	defer api.codingAgentClarificationsMu.Unlock()
	p := api.codingAgentClarifications[sessionID]
	if p == nil || p.promptID != promptID || p.provider != provider || p.ctx.Err() != nil || p.ask != nil || p.nativeAsync || p.recovered || clarificationAskingTurnEnded(p.askedTurn, api.clarificationTurnToken(sessionID)) {
		return fmt.Errorf("this question is no longer pending")
	}
	return api.settleCodingAgentClarificationLocked(p, auto, input)
}

func recoveredQuestionReply(questions []unifiedevents.CodingAgentQuestionPrompt, answers []unifiedevents.CodingAgentQuestionAnswer) string {
	var lines []string
	for i, answer := range answers {
		values := append([]string(nil), answer.SelectedLabels...)
		if answer.OtherText != "" {
			values = append(values, answer.OtherText)
		}
		lines = append(lines, questions[i].Question+"\nAnswer: "+strings.Join(values, ", "))
	}
	return "[Answer to your earlier question after the app restarted]\n" + strings.Join(lines, "\n\n")
}

// The person answers now, with their current claims. Reuse only that same
// person's durable runtime selectors and re-enter ordinary query admission.
func (api *StreamingAPI) recoveredQuestionRequest(r *http.Request, sessionID, promptID string) (*QueryRequest, error) {
	api.codingAgentClarificationsMu.Lock()
	p := api.codingAgentClarificationLocked(sessionID, promptID)
	recovered := p != nil && p.recovered
	api.codingAgentClarificationsMu.Unlock()
	if !recovered {
		return nil, nil
	}
	user := GetUserIDFromContext(r.Context())
	if !websiteDeviceClaims(GetUserFromContext(r.Context())) || api.eventStore == nil || api.eventStore.GetSessionOwner(sessionID) != user {
		return nil, fmt.Errorf("Answer this question in your own chat")
	}
	lock := productConversationRegistryMutex(restartContinuePath(user))
	lock.Lock()
	document, err := api.readRestartContinueRecords(r.Context(), user)
	lock.Unlock()
	if err != nil {
		return nil, fmt.Errorf("Could not restore this chat's settings; send a message in the chat and try again")
	}
	record, ok := document.Chats[sessionID]
	if !ok || record.SessionID != sessionID || record.QueryID == "" {
		return nil, fmt.Errorf("Send a message in this chat before answering this earlier question")
	}
	req := record.Request
	req.Query, req.Message = "", ""
	req.IsAutoNotification, req.DisableLiveInputDelivery = false, false
	req.TriggeredBy, req.TriggeredByLabel, req.DeliveryMode = "interactive", "", ""
	req.CostSourceID, req.CostSourceLabel, req.CostSourceRunID = "", "", ""
	req.AgentMessageFromOtherPerson, req.AskedBy = false, ""
	req.BotPlatform, req.BotChannelID, req.BotThreadTS = "", "", ""
	req.BotConnectionID, req.BotUserID, req.BotUserName, req.BotUserEmail = "", "", "", ""
	req.RequestedSkillID, req.AgentMessageInbox = "", ""
	req.ExecutionOptions, req.PendingAttachments = nil, nil
	return &req, nil
}

func (api *StreamingAPI) hasRecoveredQuestion(sessionID string) bool {
	api.codingAgentClarificationsMu.Lock()
	defer api.codingAgentClarificationsMu.Unlock()
	for _, p := range api.codingAgentClarifications {
		if p.sessionID == sessionID && p.recovered && (p.expiresAt.IsZero() || p.expiresAt.After(time.Now())) {
			return true
		}
	}
	return false
}
