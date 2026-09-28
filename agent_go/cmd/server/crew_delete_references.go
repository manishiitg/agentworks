package server

import (
	"context"
	"encoding/json"
	"log"
	"path"
	"strings"
)

// pruneDeletedCrewReferences removes a deleted crew from every crew's and
// workflow's saved attachments (workflow.json workflow_context_paths), so no
// other crew keeps pointing at it. Best effort: failures are logged, and turns
// skip an attachment that no longer exists anyway.
//
// A crew is attached by its physical path (_users/<owner>/Chats/Work/projects/
// <dir>) from anyone, and by its logical path (Chats/Work/projects/<dir>) from
// its owner's own crews and workflows; the logical form in another user's
// config names that user's own crew and is left alone.
func pruneDeletedCrewReferences(ctx context.Context, ownerID, workspacePath string) {
	dir := path.Base(strings.Trim(workspacePath, "/"))
	if dir == "" || dir == "." || dir == "/" {
		return
	}
	owner := sanitizeUserIDForPath(ownerID)
	logical := "Chats/Work/projects/" + dir
	physical := strings.Trim(agentProfileRuntimeWorkspace(owner, logical), "/")
	prune := func(paths []string, ownedByOwner bool) ([]string, bool) {
		kept := make([]string, 0, len(paths))
		changed := false
		for _, raw := range paths {
			clean := strings.Trim(strings.TrimSpace(raw), "/")
			if clean == physical || (ownedByOwner && clean == logical) {
				changed = true
				continue
			}
			kept = append(kept, raw)
		}
		return kept, changed
	}
	// A crew's workflow.json is rewritten as raw JSON, changing only
	// workflow_context_paths: its other fields are the crew's, and the
	// workflow writer's validation rejects crew attachments.
	saveCrew := func(workspace string, ownedByOwner bool) {
		raw, exists, err := readFileFromWorkspace(ctx, manifestPath(workspace))
		if err != nil || !exists {
			return
		}
		var doc map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &doc) != nil {
			return
		}
		var paths []string
		if json.Unmarshal(doc["workflow_context_paths"], &paths) != nil || len(paths) == 0 {
			return
		}
		kept, changed := prune(paths, ownedByOwner)
		if !changed {
			return
		}
		encoded, _ := json.Marshal(kept)
		doc["workflow_context_paths"] = encoded
		out, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return
		}
		if err := writeFileToWorkspace(ctx, manifestPath(workspace), string(out)+"\n"); err != nil {
			log.Printf("[CREW_DELETE] could not remove %s from %s: %v", physical, workspace, err)
			return
		}
		log.Printf("[CREW_DELETE] removed deleted crew %s from %s", physical, workspace)
	}
	save := func(workspace string, ownedByOwner bool) {
		manifest, exists, err := ReadWorkflowManifest(ctx, workspace)
		if err != nil || !exists || manifest == nil || len(manifest.WorkflowContextPaths) == 0 {
			return
		}
		kept, changed := prune(manifest.WorkflowContextPaths, ownedByOwner)
		if !changed {
			return
		}
		manifest.WorkflowContextPaths = kept
		if err := WriteWorkflowManifest(ctx, workspace, manifest); err != nil {
			log.Printf("[CREW_DELETE] could not remove %s from %s: %v", physical, workspace, err)
			return
		}
		log.Printf("[CREW_DELETE] removed deleted crew %s from %s", physical, workspace)
	}

	// Workflows.
	if workflows, err := DiscoverWorkflowManifests(ctx); err == nil {
		for _, workflow := range workflows {
			ownedByOwner := workflow.Manifest != nil && sanitizeUserIDForPath(workflow.Manifest.CreatedBy) == owner
			save(workflow.WorkspacePath, ownedByOwner)
		}
	} else {
		log.Printf("[CREW_DELETE] could not list workflows: %v", err)
	}

	// Crews of every owner.
	owners := append([]string{owner}, crewProjectOwnerCandidates(owner)...)
	store := defaultProductProjectStore()
	for _, crewOwner := range owners {
		root := strings.Trim(agentProfileRuntimeWorkspace(crewOwner, "Chats/Work/projects"), "/")
		files, exists, err := store.listPaths(ctx, root)
		if err != nil || !exists {
			continue
		}
		seen := map[string]bool{}
		for _, file := range files {
			rel := strings.TrimPrefix(strings.Trim(file, "/"), root+"/")
			crewDir := strings.SplitN(rel, "/", 2)[0]
			if crewDir == "" || (crewDir == dir && crewOwner == owner) || seen[crewDir] {
				continue
			}
			seen[crewDir] = true
			saveCrew(root+"/"+crewDir, sanitizeUserIDForPath(crewOwner) == owner)
		}
	}
}
