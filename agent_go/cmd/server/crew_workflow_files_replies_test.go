package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// describe without an operation must stay small: all schemas together were
// about 73k characters and left the model guessing operation names.
func TestCrewDescribeListsNamesAndSendsSchemaPerOperation(t *testing.T) {
	all, err := crewDescribeOperations("", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) > 4000 {
		t.Fatalf("describe without an operation is %d characters, want a few KB", len(all))
	}
	for _, name := range crewPlanOperations {
		if !strings.Contains(all, `"`+name+`"`) {
			t.Fatalf("describe does not list %s", name)
		}
	}
	if strings.Contains(all, "input_schema") {
		t.Fatal("the operation list must not carry schemas")
	}
	one, err := crewDescribeOperations("update_step", "")
	if err != nil || !strings.Contains(one, "input_schema") {
		t.Fatalf("an operation must return its own schema: %v", err)
	}
	if _, err := crewDescribeOperations("add_stp", ""); err == nil || !strings.Contains(err.Error(), "add_step") {
		t.Fatalf("an unknown operation must name the valid ones: %v", err)
	}
}

func TestCrewReadPlanReturnsNamedStepsAndPagesTheRest(t *testing.T) {
	steps := []map[string]interface{}{}
	for i := 0; i < 40; i++ {
		steps = append(steps, map[string]interface{}{"id": fmt.Sprintf("step-%d", i), "description": strings.Repeat("x", 1500)})
	}
	steps[3]["routes"] = []interface{}{map[string]interface{}{"id": "nested-route", "steps": []interface{}{map[string]interface{}{"id": "nested-step"}}}}
	raw, _ := json.Marshal(map[string]interface{}{"steps": steps})
	content := string(raw)
	if len(content) < 3*crewPlanReadLimit {
		t.Fatal("test plan is too small to page")
	}

	var reply struct {
		Steps   []map[string]interface{} `json:"steps"`
		Missing []string                 `json:"missing_step_ids"`
		Total   int                      `json:"total_chars"`
	}
	out, err := crewReadReply("planning/plan.json", content, true, map[string]interface{}{"step_ids": []interface{}{"step-7", "nested-step", "nope"}})
	if err != nil || json.Unmarshal([]byte(out), &reply) != nil {
		t.Fatalf("read steps: %v %s", err, out)
	}
	if len(reply.Steps) != 2 || len(reply.Missing) != 1 || reply.Missing[0] != "nope" || reply.Total != len(content) || len(out) > 4000 {
		t.Fatalf("named steps wrong: %d steps, missing %v, total %d, reply %d chars", len(reply.Steps), reply.Missing, reply.Total, len(out))
	}

	var rebuilt strings.Builder
	offset := 0
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("paging did not finish")
		}
		out, err := crewReadReply("planning/plan.json", content, true, map[string]interface{}{"offset": float64(offset)})
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Content    string   `json:"content"`
			Truncated  bool     `json:"truncated"`
			NextOffset int      `json:"next_offset"`
			Total      int      `json:"total_chars"`
			StepIDs    []string `json:"step_ids"`
		}
		if json.Unmarshal([]byte(out), &page) != nil || page.Total != len(content) || len(page.StepIDs) != 40 {
			t.Fatalf("page %d malformed: %.200s", pages, out)
		}
		rebuilt.WriteString(page.Content)
		if !page.Truncated {
			break
		}
		offset = page.NextOffset
	}
	if rebuilt.String() != content {
		t.Fatal("pages do not add up to the file")
	}

	other, _ := crewReadReply("planning/config.json", content, true, map[string]interface{}{"step_id": "step-1"})
	if !strings.Contains(other, `"content":`) || strings.Contains(other, "truncated") || len(other) < len(content) {
		t.Fatal("other files must come back whole and unchanged")
	}
}

// add_step is one schema branch per step type (about 29k characters together);
// describe returns the type list first and one type's fields on request.
func TestCrewDescribeAddStepByType(t *testing.T) {
	list, err := crewDescribeOperations("add_step", "")
	if err != nil || len(list) > 2500 || strings.Contains(list, "oneOf") {
		t.Fatalf("add_step without a type must list the types only: %v, %d chars", err, len(list))
	}
	var parsed []struct {
		Schema struct {
			Note string `json:"note"`
		} `json:"input_schema"`
	}
	if json.Unmarshal([]byte(list), &parsed) != nil || len(parsed) != 1 {
		t.Fatalf("unexpected reply: %.200s", list)
	}
	whole := 0
	for _, d := range crewPlanDefinitions() {
		if d.Name == "add_step" {
			whole = len(d.InputSchema)
		}
	}
	covered := 0
	for _, kind := range []string{"agent", "scripted", "routing", "branch"} {
		if !strings.Contains(parsed[0].Schema.Note, kind) {
			continue
		}
		covered++
		one, err := crewDescribeOperations("add_step", kind)
		if err != nil || !strings.Contains(one, `"const":"`+kind+`"`) || len(one) >= whole {
			t.Fatalf("%s: %v, %d chars against %d for all types", kind, err, len(one), whole)
		}
	}
	if covered < 2 {
		t.Fatalf("the type list names too few step types: %s", parsed[0].Schema.Note)
	}
	if _, err := crewDescribeOperations("add_step", "nope"); err == nil || !strings.Contains(err.Error(), "step_type") {
		t.Fatalf("an unknown type must say how to choose: %v", err)
	}
}
