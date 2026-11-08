package server

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// crewPlanReadLimit caps one read of planning/plan.json. A large plan came back
// whole (about 73k characters) and drowned the chat; a read now returns a page
// with the total size and the offset to continue from, or just the named steps.
const crewPlanReadLimit = 20000

// crewDescribeOperations answers describe. Without an operation it lists the
// operation names with one line each; the full schema of an operation is sent
// only when that operation is asked for (all of them together were about 73k
// characters, and a client that cut the reply left the model guessing names).
func crewDescribeOperations(operation string) (string, error) {
	definitions := crewPlanDefinitions()
	if operation != "" {
		definitions = slices.DeleteFunc(definitions, func(d workflow.ExternalPlanToolDefinition) bool { return d.Name != operation })
		if len(definitions) == 0 {
			return "", fmt.Errorf("choose a supported editing operation: %s", strings.Join(crewPlanOperations, ", "))
		}
		encoded, err := json.Marshal(definitions)
		return string(encoded), err
	}
	type summary struct {
		Name    string `json:"name"`
		Summary string `json:"summary"`
	}
	list := make([]summary, 0, len(definitions))
	for _, d := range definitions {
		list = append(list, summary{d.Name, crewFirstSentence(d.Description)})
	}
	encoded, err := json.Marshal(map[string]interface{}{
		"operations": list,
		"next":       "Call describe again with operation=<name> to get that operation's exact schema before editing.",
	})
	return string(encoded), err
}

func crewFirstSentence(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.Index(text, ". "); i >= 0 {
		text = text[:i+1]
	}
	if utf8.RuneCountInString(text) > 160 {
		text = string([]rune(text)[:157]) + "..."
	}
	return text
}

// crewReadReply answers read. Only planning/plan.json is shaped: step_id or
// step_ids return those steps (nested ones included), otherwise the file is
// paged. Every other file is returned whole, as before.
func crewReadReply(file, content string, found bool, args map[string]interface{}) (string, error) {
	reply := map[string]interface{}{"file": file, "exists": found}
	if file != "planning/plan.json" || !found {
		reply["content"] = content
		encoded, err := json.Marshal(reply)
		return string(encoded), err
	}
	total := utf8.RuneCountInString(content)
	reply["total_chars"] = total
	var ids []string
	if id, _ := args["step_id"].(string); strings.TrimSpace(id) != "" {
		ids = append(ids, strings.TrimSpace(id))
	}
	if list, ok := args["step_ids"].([]interface{}); ok {
		for _, value := range list {
			if id, _ := value.(string); strings.TrimSpace(id) != "" {
				ids = append(ids, strings.TrimSpace(id))
			}
		}
	}
	var plan interface{}
	parsed := json.Unmarshal([]byte(content), &plan) == nil
	if len(ids) > 0 {
		if !parsed {
			return "", fmt.Errorf("planning/plan.json is not valid JSON; read it without step_id")
		}
		steps := map[string]interface{}{}
		crewCollectSteps(plan, ids, steps)
		found := make([]interface{}, 0, len(ids))
		var missing []string
		for _, id := range ids {
			if step, ok := steps[id]; ok {
				found = append(found, step)
			} else {
				missing = append(missing, id)
			}
		}
		reply["steps"] = found
		if len(missing) > 0 {
			reply["missing_step_ids"] = missing
		}
		encoded, err := json.Marshal(reply)
		return string(encoded), err
	}
	if parsed {
		if object, ok := plan.(map[string]interface{}); ok {
			if list, ok := object["steps"].([]interface{}); ok {
				outline := make([]string, 0, len(list))
				for _, value := range list {
					if step, ok := value.(map[string]interface{}); ok {
						if id, _ := step["id"].(string); id != "" {
							outline = append(outline, id)
						}
					}
				}
				reply["step_ids"] = outline
			}
		}
	}
	offset := 0
	if value, ok := args["offset"].(float64); ok && value > 0 {
		offset = int(value)
	}
	runes := []rune(content)
	if offset > len(runes) {
		offset = len(runes)
	}
	end := offset + crewPlanReadLimit
	if end >= len(runes) {
		end = len(runes)
	} else {
		reply["truncated"] = true
		reply["next_offset"] = end
		reply["note"] = "Large plan: pass offset=next_offset to continue, or step_id / step_ids to read only those steps."
	}
	reply["offset"] = offset
	reply["content"] = string(runes[offset:end])
	encoded, err := json.Marshal(reply)
	return string(encoded), err
}

// crewCollectSteps finds the objects whose id is wanted, at any depth of the plan.
func crewCollectSteps(node interface{}, wanted []string, out map[string]interface{}) {
	switch value := node.(type) {
	case map[string]interface{}:
		if id, _ := value["id"].(string); id != "" && slices.Contains(wanted, id) {
			if _, seen := out[id]; !seen {
				out[id] = value
			}
		}
		for _, child := range value {
			crewCollectSteps(child, wanted, out)
		}
	case []interface{}:
		for _, child := range value {
			crewCollectSteps(child, wanted, out)
		}
	}
}
