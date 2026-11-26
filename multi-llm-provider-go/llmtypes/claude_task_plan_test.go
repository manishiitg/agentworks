package llmtypes

import "testing"

// PLAT-746-984: task tools emit deltas, unlike the legacy full TodoWrite list.
func TestClaudeTaskPlanSuccessfulResultsReplaceAndUpdate(t *testing.T) {
	var state ClaudeTaskPlan
	for _, tc := range []struct {
		name, args, result string
		count              int
		status             string
		ok                 bool
	}{
		{"TaskCreate", `{"subject":"Inspect"}`, `{"task":{"id":"1","subject":"Inspect"}}`, 1, "pending", true},
		{"TaskCreate", `{"subject":"Fix"}`, `Task #2 created successfully: Fix`, 2, "pending", true},
		{"TaskUpdate", `{"taskId":"1","status":"completed"}`, `{"success":false}`, 0, "", false},
		{"TaskUpdate", `{"taskId":"1","status":"completed"}`, `Updated task #1 status`, 2, "completed", true},
		{"TaskList", `{}`, `{"tasks":[{"id":"3","subject":"Verify","status":"in_progress"}]}`, 1, "running", true},
		{"TaskList", `{}`, `{"tasks":[{"id":"3","subject":"Verify","status":"unknown"}]}`, 0, "", false},
		{"TaskUpdate", `{"taskId":"3","status":"completed"}`, `{"success":true}`, 1, "completed", true},
		{"TaskUpdate", `{"taskId":"3","status":"deleted"}`, `{"success":true}`, 0, "", true},
		{"TodoWrite", `{"todos":[{"content":"Done","status":"completed"}]}`, `Updated`, 1, "completed", true},
		{"TaskList", `{}`, `{"tasks":[]}`, 0, "", true},
	} {
		p, ok := state.Apply(tc.name, tc.args, tc.result)
		if ok != tc.ok {
			t.Fatalf("%s ok=%v want %v", tc.name, ok, tc.ok)
		}
		if !ok {
			continue
		}
		if p.Provider != "claude-code" || len(p.Items) != tc.count || (tc.count > 0 && p.Items[0].Status != tc.status) {
			t.Fatalf("%s snapshot=%+v", tc.name, p)
		}
	}
}
