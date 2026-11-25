package server

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestChatVisualToolAuthorsCanonicalBlockAndReturnsFieldErrors(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("the chat formatter uses the deployed Node runtime")
	}
	reg := &singleToolCaptureRegistrar{}
	if err := registerChatVisualTool(reg); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		input string
		field string
	}{
		{`{"diagram":{"title":"Handoff","nodes":[{"id":"ask","title":"Ask"}],"edges":[{"from":"ask","to":"done"}]}}`, "diagram.edges.0.to"},
		{`{"timeline":{"title":"Handoff","items":[]}}`, "timeline.items"},
		{`{"sequence":{"title":"Handoff","participants":[{"id":"lead","label":"Lead"}],"messages":[{"from":"lead","to":"lead","label":"Ask"}]}}`, "sequence.participants.0.title"},
		{`{"chart":{"title":"Handoff","style":"bar","data":[{"label":"Mon","value":1}],"actions":[]}}`, "actions"},
		{`{"diagram":{"title":"Handoff","nodes":[{"id":"ask","title":"Ask"}],"edges":[]}}`, ""},
	} {
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.input), &args); err != nil {
			t.Fatal(err)
		}
		out, err := reg.exec(context.Background(), args)
		if tc.field != "" {
			if out != "" || err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("expected a field error for %s, got %q, %v", tc.field, out, err)
			}
			continue
		}
		if err != nil || !strings.HasPrefix(out, "```chat-ui\n") || !strings.Contains(out, `"/elements/visual/props/nodes/-"`) {
			t.Fatalf("expected a canonical diagram, got %q, %v", out, err)
		}
	}
}
