package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

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
