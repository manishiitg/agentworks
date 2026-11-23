package picli

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"testing"
)

// PLAT-872-21: never confirm an unanswered/cancelled dialog; preserve typed replies.
func TestPiNativeDialogResponses(t *testing.T) {
	for _, method := range []string{"select", "confirm", "input", "editor"} {
		raw, _ := json.Marshal(map[string]any{"id": "native", "method": method, "title": "Choose", "options": []string{"Yes", "No"}})
		answer := "text"
		if method == "confirm" || method == "select" {
			answer = "No"
		}
		ctx := llmtypes.WithCodingAgentQuestionHandler(context.Background(), "pi-cli", "owner", func(context.Context, map[string]any) (string, error) {
			a := map[string]any{"id": "input", "other_text": answer}
			if method == "confirm" || method == "select" {
				a = map[string]any{"id": "input", "selected_labels": []string{answer}}
			}
			b, _ := json.Marshal(map[string]any{"status": "answered", "answers": []any{a}})
			return string(b), nil
		})
		result, err := piNativeQuestion(ctx, raw)
		if err != nil {
			t.Fatal(err)
		}
		if method == "confirm" {
			if result["confirmed"] != false {
				t.Fatal("negative answer became approval")
			}
		} else if result["value"] != answer {
			t.Fatal(result)
		}
		result, err = piNativeQuestion(context.Background(), raw)
		if err != nil || result["cancelled"] != true {
			t.Fatalf("unattended dialog: %v %v", result, err)
		}
	}
}
