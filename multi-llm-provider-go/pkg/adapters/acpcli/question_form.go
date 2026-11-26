package acpcli

import (
	"context"
	"encoding/json"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/nativeinput"
)

// OpenCode asks its questions as an elicitation form: a "qN" field with
// choices, plus an optional "qN_custom" companion for a typed answer. Shown
// as a generic form, the companion would be a second question; here each
// pair is one question whose typed answer fills the companion.
type questionForm struct {
	questions []nativeinput.Question
	values    []map[string]string // per question: choice label -> const
	companion []bool
}

var questionFieldID = regexp.MustCompile(`^q(\d+)$`)

func parseQuestionForm(raw json.RawMessage) (questionForm, bool) {
	var schema struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			Title       string `json:"title"`
			Description string `json:"description"`
			OneOf       []struct {
				Const       string `json:"const"`
				Title       string `json:"title"`
				Description string `json:"description"`
			} `json:"oneOf"`
			Enum  json.RawMessage `json:"enum"`
			AnyOf json.RawMessage `json:"anyOf"`
			Items json.RawMessage `json:"items"`
		} `json:"properties"`
	}
	if json.Unmarshal(raw, &schema) != nil || len(schema.Properties) == 0 {
		return questionForm{}, false
	}
	var ids []string
	for id, field := range schema.Properties {
		if questionFieldID.MatchString(id) {
			ids = append(ids, id)
			continue
		}
		owner, isCompanion := strings.CutSuffix(id, "_custom")
		if _, ownerExists := schema.Properties[owner]; !isCompanion || !questionFieldID.MatchString(owner) || !ownerExists {
			return questionForm{}, false
		}
		if field.Type != "string" || (field.Description != "Type your own answer" && field.Description != "Add your own answer") {
			return questionForm{}, false
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, _ := strconv.Atoi(ids[i][1:])
		b, _ := strconv.Atoi(ids[j][1:])
		return a < b
	})
	var form questionForm
	choices := 0
	for _, id := range ids {
		field := schema.Properties[id]
		if field.Type != "string" || strings.TrimSpace(field.Title) == "" || len(field.Enum) > 0 || len(field.AnyOf) > 0 || len(field.Items) > 0 {
			return questionForm{}, false
		}
		_, hasCompanion := schema.Properties[id+"_custom"]
		text := strings.TrimSpace(field.Description)
		if text == "" {
			text = field.Title
		}
		q := nativeinput.Question{ID: id, Header: field.Title, Question: text}
		values := map[string]string{}
		for _, choice := range field.OneOf {
			if strings.TrimSpace(choice.Title) == "" {
				return questionForm{}, false
			}
			q.Options = append(q.Options, nativeinput.Option{Label: choice.Title, Description: strings.TrimSpace(choice.Description)})
			values[choice.Title] = choice.Const
		}
		if len(q.Options) > 0 {
			choices++
		}
		q.AllowOther = hasCompanion || len(q.Options) == 0
		form.questions = append(form.questions, q)
		form.values = append(form.values, values)
		form.companion = append(form.companion, hasCompanion)
	}
	return form, choices > 0
}

func (f questionForm) ask(ctx context.Context, agent string) nativeinput.FormResult {
	answers, err := nativeinput.AskWithKind(ctx, nativeinput.FormRequest, f.questions)
	if err != nil {
		log.Printf("[NATIVE_FORM] provider=%s agent=%s questions=%d action=cancel reason=not_answered", ProviderID, agent, len(f.questions))
		return nativeinput.FormResult{Action: "cancel"}
	}
	content := map[string]any{}
	for i, q := range f.questions {
		a := answers[i]
		switch {
		case len(a.SelectedLabels) == 1 && a.OtherText == "":
			content[q.ID] = f.values[i][a.SelectedLabels[0]]
		case len(a.SelectedLabels) == 0 && strings.TrimSpace(a.OtherText) != "":
			if f.companion[i] {
				content[q.ID+"_custom"] = a.OtherText
			} else {
				content[q.ID] = a.OtherText
			}
		default:
			log.Printf("[NATIVE_FORM] provider=%s agent=%s questions=%d action=cancel reason=invalid_answer field=%s", ProviderID, agent, len(f.questions), q.ID)
			return nativeinput.FormResult{Action: "cancel"}
		}
	}
	log.Printf("[NATIVE_FORM] provider=%s agent=%s questions=%d action=accept", ProviderID, agent, len(f.questions))
	return nativeinput.FormResult{Action: "accept", Content: content}
}
