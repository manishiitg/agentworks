package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// crewUsedByWorkflow checks saved workflow attachments before deleting a Crew.
// The workflow owner must remove attachments first.
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
	}

	return false, nil
}
