package llmtypes

import (
	"encoding/json"
	"fmt"
	"strings"
)

const StreamChunkTypeCodingAgentPlan StreamChunkType = "coding_agent_plan"

// CodingAgentPlan is a full native checklist snapshot, never inferred from prose.
// An explicit empty Items slice clears the checklist.
type CodingAgentPlan struct {
	PlanID       string                `json:"plan_id"`
	NativeTurnID string                `json:"native_turn_id,omitempty"`
	Provider     string                `json:"provider"`
	Items        []CodingAgentPlanItem `json:"items"`
	Explanation  string                `json:"explanation,omitempty"`
}
type CodingAgentPlanItem struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Status string `json:"status"` // pending | running | completed
}

// ParseCodingAgentPlan accepts native structured lists only. Unknown schemas,
// statuses and malformed/partial JSON do not replace the last known list.
func ParseCodingAgentPlan(raw []byte, field string) (*CodingAgentPlan, bool) {
	var body map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil {
		return nil, false
	}
	list, present := body[field]
	if !present || string(list) == "null" {
		return nil, false
	}
	var rows []struct {
		ID      string `json:"id"`
		Text    string `json:"text"`
		Content string `json:"content"`
		Step    string `json:"step"`
		Status  string `json:"status"`
	}
	if json.Unmarshal(list, &rows) != nil {
		return nil, false
	}
	plan := &CodingAgentPlan{Items: make([]CodingAgentPlanItem, 0, len(rows))}
	_ = json.Unmarshal(body["explanation"], &plan.Explanation)
	for i, row := range rows {
		if row.Status == "cancelled" || row.Status == "canceled" {
			continue
		}
		text := row.Text
		if text == "" {
			text = row.Content
		}
		if text == "" {
			text = row.Step
		}
		text = strings.TrimSpace(text)
		status := ""
		switch row.Status {
		case "pending":
			status = "pending"
		case "in_progress", "inProgress", "running":
			status = "running"
		case "completed":
			status = "completed"
		default:
			return nil, false
		}
		if text == "" {
			continue
		} // Native lists can include blank placeholder rows.
		id := row.ID
		if id == "" {
			id = fmt.Sprintf("step-%d", i+1)
		}
		plan.Items = append(plan.Items, CodingAgentPlanItem{ID: id, Text: text, Status: status})
	}
	return plan, true
}

// CodingAgentToolPlan recognizes the checklist tools used by native CLI lanes.
// It deliberately ignores freeform createPlan/plan markdown and unrelated tools.
func CodingAgentToolPlan(name, args string) (*CodingAgentPlan, bool) {
	name = strings.TrimPrefix(name, "functions.")
	field := "todos"
	switch name {
	case "TodoWrite", "write_todos", "updateTodos", "todo_write":
	case "update_plan":
		field = "plan"
	default:
		return nil, false
	}
	if name == "updateTodos" {
		var update struct {
			Merge bool `json:"merge"`
		}
		if json.Unmarshal([]byte(args), &update) != nil || update.Merge {
			return nil, false
		}
	}
	return ParseCodingAgentPlan([]byte(args), field)
}
