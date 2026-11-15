package llmtypes

import (
	"encoding/json"
	"regexp"
	"strings"
)

// ClaudeTaskPlan folds successful native task results into a full checklist.
// The caller owns one instance per turn; child-agent calls must not enter it.
// It never derives tasks from assistant prose or a failed tool invocation.
type ClaudeTaskPlan struct{ items []CodingAgentPlanItem }

var claudeCreatedTask = regexp.MustCompile(`(?s)^Task #(\S+) created successfully: (.+)$`)

func (s *ClaudeTaskPlan) Apply(name, args, result string) (*CodingAgentPlan, bool) {
	if strings.HasPrefix(strings.TrimSpace(result), "[ERROR]") {
		return nil, false
	}
	var textResult string
	if json.Unmarshal([]byte(result), &textResult) == nil {
		result = textResult
	}
	if name == "TodoWrite" {
		p, ok := CodingAgentToolPlan(name, args)
		if !ok {
			return nil, false
		}
		s.items = p.Items
		return s.Snapshot(), true
	}
	var input struct {
		TaskID  string `json:"taskId"`
		Subject string `json:"subject"`
		Status  string `json:"status"`
	}
	if json.Unmarshal([]byte(args), &input) != nil {
		return nil, false
	}
	var output struct {
		Success *bool `json:"success"`
		Task    *struct {
			ID      string `json:"id"`
			Subject string `json:"subject"`
		} `json:"task"`
		Tasks *[]struct {
			ID      string `json:"id"`
			Subject string `json:"subject"`
			Status  string `json:"status"`
		} `json:"tasks"`
	}
	structured := json.Unmarshal([]byte(result), &output) == nil
	if output.Success != nil && !*output.Success {
		return nil, false
	}
	switch name {
	case "TaskCreate":
		id, text := "", input.Subject
		if structured && output.Task != nil {
			id = output.Task.ID
			if output.Task.Subject != "" {
				text = output.Task.Subject
			}
		} else if m := claudeCreatedTask.FindStringSubmatch(strings.TrimSpace(result)); m != nil {
			id, text = m[1], m[2]
		}
		if id == "" || strings.TrimSpace(text) == "" {
			return nil, false
		}
		for _, item := range s.items {
			if item.ID == id {
				return nil, false
			}
		}
		s.items = append(s.items, CodingAgentPlanItem{ID: id, Text: strings.TrimSpace(text), Status: "pending"})
	case "TaskUpdate":
		if !structured && !strings.HasPrefix(strings.TrimSpace(result), "Updated task #"+input.TaskID+" ") {
			return nil, false
		}
		if input.TaskID == "" {
			return nil, false
		}
		found := false
		for i, item := range s.items {
			if item.ID != input.TaskID {
				continue
			}
			found = true
			if input.Status == "deleted" {
				s.items = append(s.items[:i], s.items[i+1:]...)
				break
			}
			if input.Status != "" {
				status := claudePlanStatus(input.Status)
				if status == "" {
					return nil, false
				}
				item.Status = status
			}
			if strings.TrimSpace(input.Subject) != "" {
				item.Text = strings.TrimSpace(input.Subject)
			}
			s.items[i] = item
			break
		}
		if !found {
			return nil, false
		}
	case "TaskList":
		if output.Tasks == nil {
			return nil, false
		}
		next := make([]CodingAgentPlanItem, 0, len(*output.Tasks))
		for _, item := range *output.Tasks {
			status := claudePlanStatus(item.Status)
			if item.ID == "" || strings.TrimSpace(item.Subject) == "" || status == "" {
				return nil, false
			}
			next = append(next, CodingAgentPlanItem{ID: item.ID, Text: strings.TrimSpace(item.Subject), Status: status})
		}
		s.items = next
	default:
		return nil, false
	}
	return s.Snapshot(), true
}
func claudePlanStatus(status string) string {
	switch status {
	case "pending", "completed":
		return status
	case "in_progress":
		return "running"
	}
	return ""
}
func (s *ClaudeTaskPlan) Snapshot() *CodingAgentPlan {
	return &CodingAgentPlan{Provider: "claude-code", Items: append([]CodingAgentPlanItem{}, s.items...)}
}

// Replace adopts an authoritative native full snapshot before later tool updates.
func (s *ClaudeTaskPlan) Replace(plan *CodingAgentPlan) {
	s.items = append([]CodingAgentPlanItem{}, plan.Items...)
}
