package step_based_workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// requiredDescriptionLayoutHeadings are the sections every step description
// must carry. Rules and Guides may be omitted when there is nothing to say.
var requiredDescriptionLayoutHeadings = []string{"Goal", "Inputs", "Output", "Done when"}

const descriptionLayoutDriftCheckID = "description_layout"

// MissingDescriptionLayoutHeadings returns the required layout headings the
// description lacks, in layout order. A heading counts when a line is exactly
// "## <name>" (case-insensitive, an optional trailing colon allowed).
func MissingDescriptionLayoutHeadings(description string) []string {
	present := map[string]bool{}
	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, "## ")), ":")
		present[strings.ToLower(strings.Join(strings.Fields(name), " "))] = true
	}
	var missing []string
	for _, heading := range requiredDescriptionLayoutHeadings {
		if !present[strings.ToLower(heading)] {
			missing = append(missing, heading)
		}
	}
	return missing
}

// descriptionLayoutStepTypes are the agent steps, whose description is the task
// an agent works from. Scripted, routing, branch, human-input and Crew steps are
// driven by code, routes, a question to a person or a Crew, so the layout does
// not apply to them (owner, 2026-10-06; a routing step with an empty description
// blocked the 1.0.46 stamp).
var descriptionLayoutStepTypes = map[string]bool{
	string(StepTypeAgent): true,
	"message_sequence":    true,
	"orchestrator":        true,
	"todo_task":           true,
}

// planStepDescriptionsFromPlanJSON returns every live agent step's description by
// id, recursing into predefined_routes sub-agent steps like planStepIDsFromPlanJSON.
// orphan_steps are not live plan surface and are left out.
func planStepDescriptionsFromPlanJSON(planContent []byte) (map[string]string, error) {
	var document struct {
		Steps []json.RawMessage `json:"steps"`
	}
	if err := json.Unmarshal(planContent, &document); err != nil {
		return nil, fmt.Errorf("parse planning/plan.json: %w", err)
	}
	out := map[string]string{}
	var walk func(json.RawMessage)
	walk = func(raw json.RawMessage) {
		var step struct {
			ID               string `json:"id"`
			Type             string `json:"type"`
			Description      string `json:"description"`
			PredefinedRoutes []struct {
				SubAgentStep json.RawMessage `json:"sub_agent_step"`
			} `json:"predefined_routes"`
		}
		if json.Unmarshal(raw, &step) != nil {
			return
		}
		if id := strings.TrimSpace(step.ID); id != "" && descriptionLayoutStepTypes[strings.TrimSpace(step.Type)] {
			out[id] = step.Description
		}
		for _, route := range step.PredefinedRoutes {
			if len(route.SubAgentStep) > 0 && string(route.SubAgentStep) != "null" {
				walk(route.SubAgentStep)
			}
		}
	}
	for _, raw := range document.Steps {
		walk(raw)
	}
	return out, nil
}

// checkDescriptionLayout is the Plan Drift check for one step's description.
func checkDescriptionLayout(descriptions map[string]string, stepID string) StepDriftCheck {
	description, agentStep := descriptions[stepID]
	if !agentStep {
		return StepDriftCheck{CheckID: descriptionLayoutDriftCheckID, Status: stepDriftCheckStatusPass,
			Evidence: "The description layout applies to agent steps (agent, orchestrator) only."}
	}
	missing := MissingDescriptionLayoutHeadings(description)
	if len(missing) == 0 {
		return StepDriftCheck{CheckID: descriptionLayoutDriftCheckID, Status: stepDriftCheckStatusPass,
			Evidence: "The description carries the ## Goal, ## Inputs, ## Output and ## Done when layout headings."}
	}
	return StepDriftCheck{CheckID: descriptionLayoutDriftCheckID, Status: stepDriftCheckStatusFail,
		Evidence: fmt.Sprintf("The description is missing the layout headings %s; convert it to the standard layout (references/step-description.md) without changing behaviour, then run check_plan_no_loss.", formatLayoutHeadings(missing))}
}

func formatLayoutHeadings(headings []string) string {
	parts := make([]string, len(headings))
	for i, h := range headings {
		parts[i] = "## " + h
	}
	return strings.Join(parts, ", ")
}

// validateStepDescriptionLayoutStamp refuses the 1.0.46 stamp while any agent step
// (nested sub-agent steps included) lacks a required layout heading. Like the
// other stamp gates it lives in the stamp executor, not only in the prompt.
func validateStepDescriptionLayoutStamp(version, workflowDir string) error {
	if version != StepDescriptionLayoutContractVersion {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(workflowDir, PlanningFolderName, "plan.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no plan: no step descriptions to convert
		}
		return fmt.Errorf("could not read planning/plan.json: %w", err)
	}
	descriptions, err := planStepDescriptionsFromPlanJSON(raw)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(descriptions))
	for id := range descriptions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var failing []string
	for _, id := range ids {
		if missing := MissingDescriptionLayoutHeadings(descriptions[id]); len(missing) > 0 {
			failing = append(failing, fmt.Sprintf("- %s: missing %s", id, formatLayoutHeadings(missing)))
		}
	}
	if len(failing) == 0 {
		return nil
	}
	return fmt.Errorf("%d of %d plan steps are not in the description layout:\n%s\nConvert each (one step at a time, check_plan_no_loss after each) before stamping %s",
		len(failing), len(ids), strings.Join(failing, "\n"), version)
}
