package nativeinput

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func formHandler(t *testing.T, answer func(questions []Question) []Answer) context.Context {
	return llmtypes.WithCodingAgentQuestionHandler(context.Background(), "example-provider", "chat", func(_ context.Context, args map[string]any) (string, error) {
		if args["native_request_kind"] != string(FormRequest) {
			return "", fmt.Errorf("form shown as %v", args["native_request_kind"])
		}
		b, _ := json.Marshal(args["questions"])
		var questions []Question
		_ = json.Unmarshal(b, &questions)
		answers := answer(questions)
		if answers == nil {
			return `{"status":"cancelled"}`, nil
		}
		out, _ := json.Marshal(map[string]any{"status": "answered", "answers": answers})
		return string(out), nil
	})
}

// #3133: an MCP form becomes one card question per field, in the server's
// order, and the answers go back typed.
func TestMCPFormBecomesCardAndTypedAnswer(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{
		"name":{"type":"string","title":"Your name"},
		"size":{"type":"string","oneOf":[{"const":"s","title":"Small"},{"const":"l","title":"Large"}]},
		"agree":{"type":"boolean","title":"Agree?"},
		"count":{"type":"integer"},
		"tags":{"type":"array","items":{"enum":["a","b","c"]}}
	},"required":["name","size","agree"]}`)
	var seen []Question
	ctx := formHandler(t, func(q []Question) []Answer {
		seen = q
		return []Answer{
			{ID: "name", OtherText: "Ada"},
			{ID: "size", SelectedLabels: []string{"Large"}},
			{ID: "agree", SelectedLabels: []string{"Yes"}},
			{ID: "count", SelectedLabels: []string{formLeaveEmpty}},
			{ID: "tags", SelectedLabels: []string{"a", "c"}},
		}
	})
	got := AskForm(ctx, "example-provider", "example-server", "Please fill this in", schema)
	if got.Action != "accept" {
		t.Fatalf("action %q", got.Action)
	}
	want := map[string]any{"name": "Ada", "size": "l", "agree": true, "tags": []string{"a", "c"}}
	if !reflect.DeepEqual(got.Content, want) {
		t.Fatalf("content %#v", got.Content)
	}
	ids := []string{}
	for _, q := range seen {
		ids = append(ids, q.ID)
	}
	if !reflect.DeepEqual(ids, []string{"name", "size", "agree", "count", "tags"}) {
		t.Fatalf("field order %v", ids)
	}
	if seen[0].Header != "Form from example-server" || !seen[0].AllowOther || !seen[4].MultiSelect {
		t.Fatalf("card shape %#v", seen)
	}

	// Typed numbers are checked; a bad one cancels instead of sending junk.
	ctx = formHandler(t, func(q []Question) []Answer {
		return []Answer{{ID: "name", OtherText: "Ada"}, {ID: "size", SelectedLabels: []string{"Small"}}, {ID: "agree", SelectedLabels: []string{"No"}},
			{ID: "count", OtherText: "three"}, {ID: "tags", SelectedLabels: []string{"b"}}}
	})
	if got := AskForm(ctx, "example-provider", "example-server", "", schema); got.Action != "cancel" {
		t.Fatalf("bad number sent: %#v", got)
	}

	// Closing the card cancels; a form the card cannot show is declined.
	ctx = formHandler(t, func([]Question) []Answer { return nil })
	if got := AskForm(ctx, "example-provider", "example-server", "", schema); got.Action != "cancel" {
		t.Fatalf("dismissed form %#v", got)
	}
	if got := AskForm(ctx, "example-provider", "example-server", "", json.RawMessage(`{"type":"object","properties":{"x":{"type":"object"}}}`)); got.Action != "decline" {
		t.Fatalf("unsupported form %#v", got)
	}

	// A link request: accept only when the person says they opened it.
	ctx = formHandler(t, func(q []Question) []Answer {
		return []Answer{{ID: "open_link", SelectedLabels: []string{"I opened it"}}}
	})
	if got := AskURL(ctx, "example-provider", "example-server", "Sign in", "https://example.com/login"); got.Action != "accept" {
		t.Fatalf("url %#v", got)
	}
}
