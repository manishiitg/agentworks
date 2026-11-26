package llmtypes

import "testing"

// PLAT-746: full structured snapshots replace; partial/prose inputs must not clear progress.
func TestCodingAgentPlanNativeSchemas(t *testing.T) {
	for _, tc := range []struct{ name, field, raw string }{
		{"Claude", "todos", `{"todos":[{"content":"Inspect","status":"in_progress"}]}`},
		{"Codex", "plan", `{"plan":[{"step":"Inspect","status":"in_progress"}]}`},
		{"Cursor", "todos", `{"todos":[{"id":"native","content":"Inspect","status":"inProgress"}]}`},
		{"Muse", "items", `{"items":[{"text":"Inspect","status":"inProgress"},{"text":"Discarded","status":"cancelled"},{"text":"  ","status":"pending"}]}`},
		{"ACP", "entries", `{"entries":[{"content":"Inspect","status":"in_progress"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := ParseCodingAgentPlan([]byte(tc.raw), tc.field)
			if !ok || len(p.Items) != 1 || p.Items[0].Text != "Inspect" || p.Items[0].Status != "running" {
				t.Fatalf("native list lost: %+v %v", p, ok)
			}
		})
	}
	for _, raw := range []string{`{}`, `{"todos":null}`, `{"todos":[`, `{"todos":[{"content":"Inspect","status":"unknown"}]}`, `{"todos":"- [x] Done"}`} {
		if _, ok := ParseCodingAgentPlan([]byte(raw), "todos"); ok {
			t.Fatalf("accepted incomplete/non-native list: %s", raw)
		}
	}
	p, ok := CodingAgentToolPlan("TodoWrite", `{"todos":[]}`)
	if !ok || p.Items == nil || len(p.Items) != 0 {
		t.Fatal("explicit empty list did not clear")
	}
	p, ok = CodingAgentToolPlan("write_todos", `{"todos":[{"content":"Done","status":"completed"}]}`)
	if !ok || p.Items[0].Status != "completed" {
		t.Fatal("completed item lost")
	}
	if _, ok := CodingAgentToolPlan("read_file", `{"todos":[]}`); ok {
		t.Fatal("unrelated tool treated as progress")
	}
}
