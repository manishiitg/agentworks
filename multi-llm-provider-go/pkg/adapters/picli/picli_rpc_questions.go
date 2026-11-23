package picli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/nativeinput"
	"strings"
	"time"
)

func piNativeQuestion(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var req struct {
		ID      string   `json:"id"`
		Method  string   `json:"method"`
		Title   string   `json:"title"`
		Message string   `json:"message"`
		Prefill string   `json:"prefill"`
		Options []string `json:"options"`
		Timeout int      `json:"timeout"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	response := map[string]any{"type": "extension_ui_response", "id": req.ID, "cancelled": true}
	q := nativeinput.Question{ID: "input", Question: strings.TrimSpace(req.Title + "\n" + req.Message), Options: []nativeinput.Option{}}
	switch req.Method {
	case "select":
		for _, label := range req.Options {
			q.Options = append(q.Options, nativeinput.Option{Label: label})
		}
	case "confirm":
		q.Options = []nativeinput.Option{{Label: "Yes"}, {Label: "No"}}
	case "input", "editor":
		q.AllowOther = true
		if req.Prefill != "" {
			q.Question += "\n\nCurrent text:\n" + req.Prefill
		}
	default:
		return nil, fmt.Errorf("not a dialog")
	}
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.Timeout)*time.Millisecond)
		defer cancel()
	}
	answers, err := nativeinput.Ask(ctx, []nativeinput.Question{q})
	if err != nil {
		return response, nil
	}
	values := nativeinput.Values(answers[0])
	if len(values) != 1 {
		return response, nil
	}
	delete(response, "cancelled")
	if req.Method == "confirm" {
		response["confirmed"] = values[0] == "Yes"
	} else {
		response["value"] = values[0]
	}
	return response, nil
}
