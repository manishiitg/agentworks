package server

import (
	"context"
	"fmt"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"strings"
)

type crewWorkflowResources struct {
	crews  *ProjectActionDispatcher
	userID string
}

func newCrewWorkflowResources(crews *ProjectActionDispatcher, userID string) *crewWorkflowResources {
	return &crewWorkflowResources{crews: crews, userID: userID}
}
func crewResourcesForRun(ctx context.Context, crews *ProjectActionDispatcher) *stepworkflow.CrewResources {
	if ctx == nil || crews == nil {
		return nil
	}
	userID := GetUserIDFromContext(ctx)
	if strings.TrimSpace(userID) == "" {
		return nil
	}
	resources := newCrewWorkflowResources(crews, userID)
	return &stepworkflow.CrewResources{Agents: resources, Scripts: resources}
}

// Attached aliases still require current access even without a Crew invocation node.
func preflightCrewAttachments(ctx context.Context, crews *ProjectActionDispatcher, userID, workspacePath string) error {
	if crews == nil {
		return nil
	}
	manifest, exists, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil {
		return err
	}
	if !exists || manifest == nil {
		return nil
	}
	for _, attachment := range manifest.CrewAttachments {
		if err := workflowtypes.ValidateCrewAttachmentBinding(attachment); err != nil {
			return fmt.Errorf("crew attachment %q cannot run: %w; re-attach it read-only with manage_crew_attachment before running", attachment.Alias, err)
		}
		profileID := normalizeInternalProfileID(attachment.CrewProfileID)
		_, binding, _, _, err := crews.projectManifestAnyOwner(ctx, userID, profileID, strings.TrimSpace(attachment.CrewProjectID))
		if err != nil {
			return fmt.Errorf("crew attachment %q cannot run: crew project %q is unavailable or access was revoked: %w", attachment.Alias, strings.TrimSpace(attachment.CrewProjectID), err)
		}
		// The stored root must equal the freshly authorized binding. Shape
		// checks alone accept another owner's same-named project path, so
		// authorizing the project without comparing roots would grant a
		// workspace the check never authorized.
		authorized := workflowtypes.CanonicalCrewAttachmentRoot(binding.WorkspacePath)
		if authorized == "" || workflowtypes.CanonicalCrewAttachmentRoot(attachment.CrewWorkspacePath) != authorized {
			return fmt.Errorf("crew attachment %q cannot run: its stored workspace no longer matches crew project %q; re-attach it read-only with manage_crew_attachment before running", attachment.Alias, strings.TrimSpace(attachment.CrewProjectID))
		}
	}
	return nil
}

func crewAgentChatID(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 48 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}
