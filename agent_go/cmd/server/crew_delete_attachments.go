package server

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// detachDeletedCrewFromWorkflows removes a deleted Crew's first-class workflow
// attachments. Crew steps remain in the plan for an owner to repair; deleting
// them silently would discard workflow logic. Each successful manifest change
// also revokes the attachment from live shell sessions.
func detachDeletedCrewFromWorkflows(ctx context.Context, profileID, projectID, workspacePath string) (int, error) {
	targetRoot := workflowtypes.CanonicalCrewAttachmentRoot(workspacePath)
	if targetRoot == "" {
		return 0, fmt.Errorf("Crew workspace path is missing")
	}
	folders, err := listWorkspaceFolders(ctx)
	if err != nil {
		return 0, fmt.Errorf("list workflows: %w", err)
	}
	detached := 0
	for _, folder := range folders {
		manifest, exists, err := ReadWorkflowManifest(ctx, folder)
		if err != nil {
			return detached, fmt.Errorf("inspect workflow %s: %w", folder, err)
		}
		if !exists || manifest == nil {
			continue
		}
		kept := make([]workflowtypes.CrewAttachment, 0, len(manifest.CrewAttachments))
		for _, attachment := range manifest.CrewAttachments {
			if strings.EqualFold(strings.TrimSpace(attachment.CrewProfileID), strings.TrimSpace(profileID)) &&
				strings.TrimSpace(attachment.CrewProjectID) == strings.TrimSpace(projectID) &&
				workflowtypes.CanonicalCrewAttachmentRoot(attachment.CrewWorkspacePath) == targetRoot {
				continue
			}
			kept = append(kept, attachment)
		}
		if len(kept) == len(manifest.CrewAttachments) {
			continue
		}
		previousRoots := crewAttachmentStoredRoots(manifest.CrewAttachments)
		manifest.CrewAttachments = kept
		if err := WriteWorkflowManifest(ctx, folder, manifest); err != nil {
			return detached, fmt.Errorf("detach Crew from workflow %s: %w", folder, err)
		}
		live := liveCrewAttachmentBindings(kept)
		common.ReconcileSessionCrewAttachments(folder, previousRoots, crewAttachmentStoredRoots(live), workflowtypes.CrewAttachmentEnvKeys(live))
		log.Printf("[CREW_DELETE] detached project %s from workflow %s", projectID, folder)
		detached++
	}
	return detached, nil
}
