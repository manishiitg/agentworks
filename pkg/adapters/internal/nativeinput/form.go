package nativeinput

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// FormRequest is an MCP server's request for input (elicitation). Its answer
// belongs to the waiting server request, so the host must never recover it as
// a later chat message the way it recovers ordinary questions.
const FormRequest RequestKind = "form"

// FormResult is the MCP elicitation answer: action is accept, decline or
// cancel; content is set only for an accepted form.
type FormResult struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content,omitempty"`
}

const formLeaveEmpty = "(leave empty)"

// formMaxOptions keeps one field within the card's limit of 12 choices,
// leaving room for the "leave empty" choice of an optional field.
const formMaxOptions = 11

type formChoice struct{ label, value string }

type formField struct {
	key, kind  string // kind: text, number, integer, boolean, one, many
	question   Question
	choices    []formChoice
	typedOnly  []string // allowed values when a long list is typed instead of picked
	isRequired bool
}

type formFieldSchema struct {
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Enum        []string `json:"enum"`
	EnumNames   []string `json:"enumNames"`
	OneOf       []struct {
		Const string `json:"const"`
		Title string `json:"title"`
	} `json:"oneOf"`
	Items *struct {
		Enum  []string `json:"enum"`
		AnyOf []struct {
			Const string `json:"const"`
			Title string `json:"title"`
		} `json:"anyOf"`
		OneOf []struct {
			Const string `json:"const"`
			Title string `json:"title"`
		} `json:"oneOf"`
	} `json:"items"`
}

// AskForm shows an MCP server's form as the chat's question card and returns
// the answer to send back. A dismissed or expired card cancels; a form the
// card cannot show is declined, so the server never waits forever.
func AskForm(ctx context.Context, provider, server, message string, schema json.RawMessage) FormResult {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	fields, err := formFields(server, message, schema)
	if err != nil {
		log.Printf("[NATIVE_FORM] provider=%s server=%s action=decline reason=%s", provider, server, err)
		return FormResult{Action: "decline"}
	}
	if len(fields) == 0 {
		// Nothing to fill in: the server only wants a yes or no.
		return confirmForm(ctx, provider, server, message)
	}
	questions := make([]Question, len(fields))
	for i, f := range fields {
		questions[i] = f.question
	}
	answers, err := AskWithKind(ctx, FormRequest, questions)
	if err != nil {
		log.Printf("[NATIVE_FORM] provider=%s server=%s fields=%d action=cancel reason=not_answered", provider, server, len(fields))
		return FormResult{Action: "cancel"}
	}
	content := map[string]any{}
	for i, f := range fields {
		value, set, err := f.value(answers[i])
		if err != nil {
			log.Printf("[NATIVE_FORM] provider=%s server=%s fields=%d action=cancel reason=invalid_answer field=%s", provider, server, len(fields), f.key)
			return FormResult{Action: "cancel"}
		}
		if set {
			content[f.key] = value
		}
	}
	log.Printf("[NATIVE_FORM] provider=%s server=%s fields=%d filled=%d action=accept", provider, server, len(fields), len(content))
	return FormResult{Action: "accept", Content: content}
}

func confirmForm(ctx context.Context, provider, server, message string) FormResult {
	answers, err := AskWithKind(ctx, FormRequest, []Question{{ID: "confirm", Header: formHeader(server), Question: strings.TrimSpace(message),
		Options: []Option{{Label: "Decline"}, {Label: "Accept"}}}})
	if err != nil {
		log.Printf("[NATIVE_FORM] provider=%s server=%s fields=0 action=cancel reason=not_answered", provider, server)
		return FormResult{Action: "cancel"}
	}
	if len(answers) == 1 && len(answers[0].SelectedLabels) == 1 && answers[0].SelectedLabels[0] == "Accept" {
		log.Printf("[NATIVE_FORM] provider=%s server=%s fields=0 action=accept", provider, server)
		return FormResult{Action: "accept", Content: map[string]any{}}
	}
	log.Printf("[NATIVE_FORM] provider=%s server=%s fields=0 action=decline", provider, server)
	return FormResult{Action: "decline"}
}

// AskURL asks the person to open a link the MCP server needs them to visit
// (usually a sign-in). Accept means they agreed to open it.
func AskURL(ctx context.Context, provider, server, message, url string) FormResult {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	text := strings.TrimSpace(message)
	if text != "" {
		text += "\n\n"
	}
	text += "Open this link to continue:\n" + strings.TrimSpace(url)
	answers, err := AskWithKind(ctx, FormRequest, []Question{{ID: "open_link", Header: formHeader(server), Question: text,
		Options: []Option{{Label: "Decline"}, {Label: "I opened it"}}}})
	if err != nil {
		log.Printf("[NATIVE_FORM] provider=%s server=%s mode=url action=cancel reason=not_answered", provider, server)
		return FormResult{Action: "cancel"}
	}
	if len(answers) == 1 && len(answers[0].SelectedLabels) == 1 && answers[0].SelectedLabels[0] == "I opened it" {
		log.Printf("[NATIVE_FORM] provider=%s server=%s mode=url action=accept", provider, server)
		return FormResult{Action: "accept"}
	}
	log.Printf("[NATIVE_FORM] provider=%s server=%s mode=url action=decline", provider, server)
	return FormResult{Action: "decline"}
}

func formHeader(server string) string {
	if server = strings.TrimSpace(server); server != "" {
		return "Form from " + server
	}
	return "Form from a tool"
}

// formFields turns a flat MCP form schema into card questions, in the order
// the server listed them.
func formFields(server, message string, raw json.RawMessage) ([]formField, error) {
	var schema struct {
		Type       string          `json:"type"`
		Properties json.RawMessage `json:"properties"`
		Required   []string        `json:"required"`
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("unreadable_schema")
	}
	keys, props, err := orderedProperties(schema.Properties)
	if err != nil {
		return nil, err
	}
	if len(keys) > 12 {
		return nil, fmt.Errorf("too_many_fields")
	}
	required := map[string]bool{}
	for _, k := range schema.Required {
		required[k] = true
	}
	fields := make([]formField, 0, len(keys))
	for i, key := range keys {
		var p formFieldSchema
		if err := json.Unmarshal(props[key], &p); err != nil {
			return nil, fmt.Errorf("unreadable_field")
		}
		f, err := newFormField(key, p, required[key])
		if err != nil {
			return nil, err
		}
		if i == 0 {
			f.question.Header = formHeader(server)
			if m := strings.TrimSpace(message); m != "" {
				f.question.Question = m + "\n\n" + f.question.Question
			}
		}
		fields = append(fields, f)
	}
	return fields, nil
}

func orderedProperties(raw json.RawMessage) ([]string, map[string]json.RawMessage, error) {
	props := map[string]json.RawMessage{}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, props, nil
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, nil, fmt.Errorf("unreadable_schema")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil {
		return nil, nil, fmt.Errorf("unreadable_schema")
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, fmt.Errorf("unreadable_schema")
		}
		key, _ := tok.(string)
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, nil, fmt.Errorf("unreadable_schema")
		}
	}
	return keys, props, nil
}

func newFormField(key string, p formFieldSchema, isRequired bool) (formField, error) {
	label := strings.TrimSpace(p.Title)
	if label == "" {
		label = key
	}
	text := label
	if d := strings.TrimSpace(p.Description); d != "" {
		text += "\n" + d
	}
	if !isRequired {
		text += "\n(optional)"
	}
	f := formField{key: key, isRequired: isRequired, question: Question{ID: key, Question: text}}
	switch p.Type {
	case "boolean":
		f.kind = "boolean"
		f.choices = []formChoice{{"Yes", "true"}, {"No", "false"}}
	case "array":
		if p.Items == nil {
			return f, fmt.Errorf("unsupported_field")
		}
		f.kind = "many"
		f.choices = enumChoices(p.Items.Enum, nil, append(p.Items.AnyOf, p.Items.OneOf...))
		if len(f.choices) == 0 {
			return f, fmt.Errorf("unsupported_field")
		}
		f.question.MultiSelect = true
	case "string", "":
		f.kind = "text"
		if choices := enumChoices(p.Enum, p.EnumNames, p.OneOf); len(choices) > 0 {
			f.kind, f.choices = "one", choices
		}
	case "number", "integer":
		f.kind = p.Type
		f.question.AllowOther = true
	default:
		return f, fmt.Errorf("unsupported_field")
	}
	if len(f.choices) > formMaxOptions {
		if f.kind == "many" {
			return f, fmt.Errorf("too_many_choices")
		}
		// Too many to show as buttons: type one of the listed values instead.
		values := make([]string, len(f.choices))
		for i, c := range f.choices {
			values[i] = c.value
		}
		f.kind, f.typedOnly, f.choices = "text", values, nil
		f.question.Question += "\nType one of: " + strings.Join(values, ", ")
	}
	if len(f.choices) == 0 {
		f.question.AllowOther = true
	}
	for _, c := range f.choices {
		f.question.Options = append(f.question.Options, Option{Label: c.label})
	}
	if !isRequired {
		f.question.Options = append(f.question.Options, Option{Label: formLeaveEmpty})
	}
	return f, nil
}

func enumChoices(enum, names []string, titled []struct {
	Const string `json:"const"`
	Title string `json:"title"`
}) []formChoice {
	var out []formChoice
	seen := map[string]bool{}
	add := func(label, value string) {
		label = strings.TrimSpace(label)
		if label == "" {
			label = value
		}
		if seen[label] {
			label += " (" + value + ")"
		}
		seen[label] = true
		out = append(out, formChoice{label, value})
	}
	for _, t := range titled {
		add(t.Title, t.Const)
	}
	for i, v := range enum {
		name := ""
		if i < len(names) {
			name = names[i]
		}
		add(name, v)
	}
	return out
}

// value converts one card answer into the form's typed value. set is false
// when an optional field was left empty.
func (f formField) value(a Answer) (any, bool, error) {
	var picked []string
	empty := false
	for _, label := range a.SelectedLabels {
		if label == formLeaveEmpty {
			empty = true
			continue
		}
		matched := false
		for _, c := range f.choices {
			if c.label == label {
				picked = append(picked, c.value)
				matched = true
				break
			}
		}
		if !matched {
			return nil, false, fmt.Errorf("unknown choice")
		}
	}
	typed := strings.TrimSpace(a.OtherText)
	if empty && len(picked) == 0 && typed == "" {
		if f.isRequired {
			return nil, false, fmt.Errorf("required field left empty")
		}
		return nil, false, nil
	}
	switch f.kind {
	case "many":
		return picked, true, nil
	case "one":
		if len(picked) != 1 {
			return nil, false, fmt.Errorf("pick one")
		}
		return picked[0], true, nil
	case "boolean":
		if len(picked) != 1 {
			return nil, false, fmt.Errorf("pick one")
		}
		return picked[0] == "true", true, nil
	case "number":
		n, err := strconv.ParseFloat(typed, 64)
		return n, err == nil, err
	case "integer":
		n, err := strconv.ParseInt(typed, 10, 64)
		return n, err == nil, err
	}
	if f.typedOnly != nil {
		for _, v := range f.typedOnly {
			if v == typed {
				return typed, true, nil
			}
		}
		return nil, false, fmt.Errorf("not one of the listed values")
	}
	if typed == "" && f.isRequired {
		return nil, false, fmt.Errorf("required field left empty")
	}
	return typed, typed != "", nil
}
