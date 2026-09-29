package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// crewUsedByWorkflow checks saved workflow attachments and Crew steps before
// deleting a Crew. A workflow owner must remove both references first.
func crewUsedByWorkflow(ctx context.Context, profileID, projectID, workspacePath string) (bool, error) {
	targetRoot := workflowtypes.CanonicalCrewAttachmentRoot(workspacePath)
	if targetRoot == "" {
		return false, fmt.Errorf("Crew workspace path is missing")
	}
	folders, err := listWorkspaceFolders(ctx)
	if err != nil {
		return false, fmt.Errorf("list workflows: %w", err)
	}
	for _, folder := range folders {
		content, exists, err := readFileFromWorkspace(ctx, manifestPath(folder))
		if err != nil {
			return false, fmt.Errorf("inspect workflow %s: %w", folder, err)
		}
		if exists {
			var manifest struct {
				CrewAttachments []workflowtypes.CrewAttachment `json:"crew_attachments"`
			}
			if err := json.Unmarshal([]byte(content), &manifest); err != nil {
				return false, fmt.Errorf("parse workflow %s: %w", folder, err)
			}
			for _, attachment := range manifest.CrewAttachments {
				if strings.EqualFold(strings.TrimSpace(attachment.CrewProfileID), strings.TrimSpace(profileID)) &&
					strings.TrimSpace(attachment.CrewProjectID) == strings.TrimSpace(projectID) &&
					workflowtypes.CanonicalCrewAttachmentRoot(attachment.CrewWorkspacePath) == targetRoot {
					return true, nil
				}
			}
		}
		content, exists, err = readFileFromWorkspace(ctx, folder+"/planning/plan.json")
		if err != nil {
			return false, fmt.Errorf("inspect workflow plan %s: %w", folder, err)
		}
		if !exists {
			continue
		}
		var plan struct {
			Steps []struct {
				Type          string `json:"type"`
				CrewProfileID string `json:"crew_profile_id"`
				CrewProjectID string `json:"crew_project_id"`
			} `json:"steps"`
		}
		if err := json.Unmarshal([]byte(content), &plan); err != nil {
			return false, fmt.Errorf("parse workflow plan %s: %w", folder, err)
		}
		for _, step := range plan.Steps {
			if step.Type == "crew" && strings.EqualFold(strings.TrimSpace(step.CrewProfileID), strings.TrimSpace(profileID)) &&
				strings.TrimSpace(step.CrewProjectID) == strings.TrimSpace(projectID) {
				return true, nil
			}
		}
	}
	return false, nil
}
