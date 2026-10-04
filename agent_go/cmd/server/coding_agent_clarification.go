package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

const clarificationPromptPrefix = "clarification:"

type pendingCodingAgentClarification struct {
	sessionID string
	provider  string
	promptID  string
	questions []unifiedevents.CodingAgentQuestionPrompt
	answers   chan []unifiedevents.CodingAgentQuestionAnswer
	ctx       context.Context
}

func codingAgentClarificationSchema() map[string]interface{} {
	option := map[string]interface{}{"type": "object", "required": []string{"label"}, "additionalProperties": false, "properties": map[string]interface{}{
		"label":       map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 500},
		"description": map[string]interface{}{"type": "string", "maxLength": 1000},
	}}
	question := map[string]interface{}{"type": "object", "required": []string{"id", "question", "options"}, "additionalProperties": false, "properties": map[string]interface{}{
		"id":             map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 100},
		"header":         map[string]interface{}{"type": "string", "maxLength": 100},
		"question":       map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 2000},
		"options":        map[string]interface{}{"type": "array", "minItems": 1, "maxItems": 12, "items": option},
		"multi_select":   map[string]interface{}{"type": "boolean"},
		"allow_other":    map[string]interface{}{"type": "boolean"},
		"min_selections": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 12},
		"max_selections": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 12},
	}}
	return map[string]interface{}{"type": "object", "required": []string{"questions"}, "additionalProperties": false, "properties": map[string]interface{}{
		"questions": map[string]interface{}{"type": "array", "minItems": 1, "maxItems": 12, "items": question},
	}}
}

// The platform bridge provides the same question/answer lifecycle to Claude,
// Codex and Muse. Only the attended Builder turn registers this tool; it must
// never leave an unattended workflow, bot or scheduled run waiting for a UI.
func (api *StreamingAPI) registerCodingAgentClarificationTool(registrar definitionToolRegistrar, sessionID, provider string) error {
	return registrar.RegisterCustomToolWithTimeout("request_clarification",
		"Ask the user to choose options in this chat and wait for their answers. Put related questions in a single call; give every question a unique id. Supports single choice, multi_select with selection bounds, and a custom text answer when allow_other is true. The user explicitly submits all answers together; subsequent calls create separate prompts. A cancelled or expired prompt returns an error, never an assumed answer.",
		codingAgentClarificationSchema(), func(ctx context.Context, args map[string]interface{}) (string, error) {
			return api.waitForCodingAgentClarification(ctx, sessionID, provider, args)
		}, 30*time.Minute, "human_tools")
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
		if q.ID == "" || len(q.ID) > 100 || ids[q.ID] || q.Question == "" || len(q.Question) > 2000 || len(q.Options) == 0 || len(q.Options) > 12 {
			return nil, fmt.Errorf("each question needs a unique id, text and 1 to 12 options")
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
		if q.MinSelections < 0 || q.MaxSelections < 0 || min > max || min > len(q.Options) || max > available {
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
	now := time.Now()
	data := &unifiedevents.CodingAgentQuestionEvent{
		BaseEventData: unifiedevents.BaseEventData{Timestamp: now, SessionID: p.sessionID},
		Provider:      p.provider, PromptID: p.promptID, Kind: kind, Outcome: outcome, Answers: answers,
	}
	if kind == "requested" {
		data.Questions = p.questions
	}
	return api.eventStore.AddEventChecked(p.sessionID, events.Event{
		ID: p.promptID + ":" + kind, Type: "coding_agent_question", Timestamp: now, SessionID: p.sessionID,
		Data: &unifiedevents.AgentEvent{Type: unifiedevents.CodingAgentQuestion, Timestamp: now, SessionID: p.sessionID, Component: "system", Data: data},
	})
}

func (api *StreamingAPI) waitForCodingAgentClarification(ctx context.Context, sessionID, provider string, args map[string]interface{}) (string, error) {
	questions, err := parseCodingAgentClarification(args)
	if err != nil {
		return "", err
	}
	p := &pendingCodingAgentClarification{sessionID: sessionID, provider: provider, promptID: clarificationPromptPrefix + uuid.NewString(), questions: questions, answers: make(chan []unifiedevents.CodingAgentQuestionAnswer, 1), ctx: ctx}
	api.codingAgentClarificationsMu.Lock()
	if api.codingAgentClarifications == nil {
		api.codingAgentClarifications = map[string]*pendingCodingAgentClarification{}
	}
	if api.codingAgentClarifications[sessionID] != nil {
		api.codingAgentClarificationsMu.Unlock()
		return "", fmt.Errorf("answer the pending clarification before asking another")
	}
	api.codingAgentClarifications[sessionID] = p
	err = api.emitCodingAgentClarification(p, "requested", "", nil)
	if err != nil {
		delete(api.codingAgentClarifications, sessionID)
	}
	api.codingAgentClarificationsMu.Unlock()
	if err != nil {
		return "", err
	}
	select {
	case answers := <-p.answers:
		result, err := json.Marshal(map[string]interface{}{"status": "answered", "prompt_id": p.promptID, "answers": answers})
		return string(result), err
	case <-ctx.Done():
		api.codingAgentClarificationsMu.Lock()
		// A submitted answer wins if it was durably recorded before cancellation.
		if api.codingAgentClarifications[sessionID] == p {
			delete(api.codingAgentClarifications, sessionID)
			_ = api.emitCodingAgentClarification(p, "settled", "interrupted", nil)
			api.codingAgentClarificationsMu.Unlock()
			return "", ctx.Err()
		}
		api.codingAgentClarificationsMu.Unlock()
		answers := <-p.answers
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

func (api *StreamingAPI) submitCodingAgentClarification(sessionID, provider, promptID string, auto bool, input []codingAgentQuestionAnswerInput) error {
	api.codingAgentClarificationsMu.Lock()
	defer api.codingAgentClarificationsMu.Unlock()
	p := api.codingAgentClarifications[sessionID]
	if p == nil || p.promptID != promptID || p.provider != provider || p.ctx.Err() != nil {
		return fmt.Errorf("clarification is no longer pending")
	}
	answers, err := clarificationAnswers(p.questions, auto, input)
	if err != nil {
		return err
	}
	if err := api.emitCodingAgentClarification(p, "settled", "answered", answers); err != nil {
		return err
	}
	delete(api.codingAgentClarifications, sessionID)
	p.answers <- answers
	return nil
}
