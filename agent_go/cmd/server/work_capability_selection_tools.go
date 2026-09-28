package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

const updateProjectSkillSelectionTool = "update_project_skill_selection"

// registerWorkSkillSelectionTool provides the Crew-scoped selection half of
// AgentWorks' shared skill lifecycle. Discovery, installation, import, reading,
// and removal continue to use the same platform tools as AgentWorks.
func (api *StreamingAPI) registerWorkSkillSelectionTool(registrar definitionToolRegistrar, userID, workspacePath string) error {
	cleanWorkspace, err := cleanAgentProfileWorkspace(workspacePath, userID)
	if err != nil || cleanWorkspace != workspacePath || !isActiveWorkProjectWorkspace(userID, cleanWorkspace) {
		return fmt.Errorf("Crew skill selection requires an active Crew project")
	}

	return registrar.RegisterCustomTool(updateProjectSkillSelectionTool, "Select or deselect one account-level or project-local skill for the active Crew project. For account-level skills, call list_skills first and pass the exact folder name; a project-local skill may be selected by its folder name under this Crew's skills/ directory. Selection is durable in workflow.json and causes matching skills to be attached automatically on later Crew turns. This does not install or uninstall a skill.", map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"action", "skill"},
		"properties": map[string]interface{}{
			"action": map[string]interface{}{"type": "string", "enum": []string{"select", "deselect"}},
			"skill":  map[string]interface{}{"type": "string", "description": "Exact installed skill folder name returned by list_skills."},
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		action := strings.ToLower(strings.TrimSpace(fmt.Sprint(args["action"])))
		requested := strings.TrimSpace(fmt.Sprint(args["skill"]))
		if action != "select" && action != "deselect" {
			return "", fmt.Errorf("action must be select or deselect")
		}
		if requested == "" || requested == "<nil>" {
			return "", fmt.Errorf("skill is required")
		}

		canonical := requested
		if action == "select" && !skills.IsBuiltinSkill(requested) {
			installed, err := skills.GetSkillIn(getWorkspaceAPIURL(), workspacePath, requested)
			if err != nil {
				return "", fmt.Errorf("skill %q is not installed in this Crew or the account-level skills library; use an exact folder name", requested)
			}
			canonical = installed.FolderName
		} else if selected, err := productSelectedSkills(ctx, "work", workspacePath); err == nil {
			for _, name := range selected {
				if strings.EqualFold(name, requested) {
					canonical = name
					break
				}
			}
		}

		if err := updateProductSelectedSkills(ctx, "work", workspacePath, func(current []string) []string {
			next := make([]string, 0, len(current)+1)
			for _, name := range current {
				if !strings.EqualFold(strings.TrimSpace(name), canonical) {
					next = append(next, name)
				}
			}
			if action == "select" {
				next = append(next, canonical)
			}
			return next
		}); err != nil {
			return "", fmt.Errorf("update Crew skill selection: %w", err)
		}
		selected, err := productSelectedSkills(ctx, "work", workspacePath)
		if err != nil {
			return "", fmt.Errorf("read updated Crew skill selection: %w", err)
		}
		result := map[string]interface{}{
			"action":          action,
			"skill":           canonical,
			"selected_skills": selected,
			"status":          "updated",
			"takes_effect":    "next_user_message",
		}
		if action == "select" {
			result["message"] = fmt.Sprintf("%s is selected for this Crew project and will attach automatically from the next user message.", canonical)
		} else {
			result["message"] = fmt.Sprintf("%s is no longer selected for this Crew project.", canonical)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}, "work_skills")
}
