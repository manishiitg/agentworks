package server

import (
	"context"
	"fmt"
	"strings"
)

// Conversation routing is based on the two projects' recorded owners. The
// executing user may be a reader/editor, so their identity is not an owner
// fallback. Missing or ambiguous ownership keeps the call isolated.
func projectsShareOwner(source, target []string) bool {
	for _, a := range source {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		for _, b := range target {
			if a == strings.TrimSpace(b) {
				return true
			}
		}
	}
	return false
}

// A queued call must not enter a main chat using an ownership decision that
// became stale while it waited. Reject a changed destination; the caller can
// submit a fresh call into the correct conversation/queue.
func (s *ProductScheduleService) validateProjectCallConversation(ctx context.Context, job productScheduleJob) error {
	if job.ProjectCaller == nil {
		return nil
	}
	owners := s.projectCallerOwners(ctx, job.UserID, triggerLinkCaller{Stamp: *job.ProjectCaller, Path: job.ProjectCallerPath})
	owner, ok := crewProjectOwnerID(job.WorkspacePath)
	shared := ok && projectsShareOwner(owners, []string{owner})
	if job.Schedule.Isolated == shared {
		return fmt.Errorf("project ownership changed while the call was queued; submit a new call")
	}
	return nil
}

func (s *ProductScheduleService) projectCallerOwners(ctx context.Context, userID string, caller triggerLinkCaller) []string {
	switch caller.Stamp.Type {
	case triggerCallerWorkflow:
		if caller.Path != "" {
			manifest, exists, err := ReadWorkflowManifest(ctx, caller.Path)
			if err == nil && exists && manifest != nil && manifest.ID == caller.Stamp.ID {
				return manifest.effectiveOwners()
			}
			return nil
		}
		// Workflow plan steps carry an ID rather than a workspace path.
		workflows, err := DiscoverWorkflowManifests(ctx)
		if err != nil {
			return nil
		}
		var owners []string
		found := false
		for _, workflow := range workflows {
			if workflow.Manifest != nil && workflow.Manifest.ID == caller.Stamp.ID {
				if found {
					return nil
				}
				owners, found = workflow.Manifest.effectiveOwners(), true
			}
		}
		return owners
	case triggerCallerCrew:
		if caller.Path != "" {
			stamp, err := crewWorkflowRunCaller(ctx, caller.Path)
			if err != nil || stamp.ID != caller.Stamp.ID || stamp.ProfileID != caller.Stamp.ProfileID {
				return nil
			}
			owner, ok := crewProjectOwnerID(caller.Path)
			if ok {
				return []string{owner}
			}
			return nil
		}
		if s == nil {
			return nil
		}
		_, binding, _, _, err := s.projectManifestAnyOwner(ctx, userID, caller.Stamp.ProfileID, caller.Stamp.ID)
		if err == nil {
			if owner, ok := crewProjectOwnerID(binding.WorkspacePath); ok {
				return []string{owner}
			}
		}
	}
	return nil
}
