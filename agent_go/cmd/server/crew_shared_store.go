package server

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// PLAT-442 step 4: where the projects of one owner live. A Crew is in its owner's tree
// (_users/<owner>/Chats/Work/projects/<f>) until it is migrated, then at Crew/<f>; a server is half-migrated for
// as long as the move takes, so every place that enumerates "this owner's Crews" asks both locations. Reading
// the shared root never depends on the creation flag.

// sharedCrewEntry is one Crew at the shared root: its product.json path and its owner (the server's registry).
type sharedCrewEntry struct {
	ManifestPath string
	OwnerID      string
}

// listSharedCrewManifestPaths lists the <root>/<folder>/product.json paths at the shared Crew root whose Crew
// is registered to ownerID (every owner's when ownerID is empty). A Crew with no registry entry belongs to nobody and
// is skipped; hidden folders (bookkeeping such as Crew/.migrating) are skipped.
func listSharedCrewManifestPaths(ctx context.Context, store productProjectStore, ownerID string) ([]string, error) {
	entries, err := listSharedCrewEntries(ctx, store)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if ownerID != "" && entry.OwnerID != sanitizeUserIDForPath(ownerID) {
			continue
		}
		out = append(out, entry.ManifestPath)
	}
	return out, nil
}

// listSharedCrewEntries lists every owned Crew at the shared root with its manifest owner (one listing, one
// manifest read per Crew).
func listSharedCrewEntries(ctx context.Context, store productProjectStore) ([]sharedCrewEntry, error) {
	paths, exists, err := store.listPaths(ctx, workspaceref.SharedCrewRoot)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	prefix := workspaceref.SharedCrewRoot + "/"
	seen := map[string]struct{}{}
	var out []sharedCrewEntry
	for _, candidate := range paths {
		candidate = filepath.ToSlash(strings.TrimSpace(candidate))
		if !strings.HasPrefix(candidate, prefix) || !strings.HasSuffix(candidate, "/product.json") {
			continue
		}
		folder := strings.TrimSuffix(strings.TrimPrefix(candidate, prefix), "/product.json")
		if folder == "" || strings.Contains(folder, "/") || strings.HasPrefix(folder, ".") {
			continue
		}
		if _, dup := seen[candidate]; dup {
			continue
		}
		seen[candidate] = struct{}{}
		// The owner is the server's registry entry for the folder (PLAT-449), not what the manifest says; a Crew
		// without one belongs to nobody and is not listed.
		owner := sharedCrewOwner(folder)
		if owner == "" {
			continue
		}
		out = append(out, sharedCrewEntry{ManifestPath: candidate, OwnerID: owner})
	}
	return out, nil
}

// listProjectManifestPaths lists the product.json paths of userID's projects of profile: the projects root in the
// user's own tree and, for a Crew, the Crew/ shared root filtered by manifest owner. exists is false only when
// neither holds anything. The paths are as the workspace lists them (full workspace paths).
func listProjectManifestPaths(ctx context.Context, store productProjectStore, userID string, profile agentprofiles.Profile) (paths []string, exists bool, err error) {
	projectsRoot, err := cleanAgentProfileWorkspace(profile.Runtime.Workspace.ProjectsRoot, userID)
	if err != nil {
		return nil, false, err
	}
	runtimeRoot := agentProfileRuntimeWorkspace(userID, projectsRoot)
	own, ownExists, err := store.listPaths(ctx, runtimeRoot)
	if err != nil {
		return nil, false, err
	}
	rootPrefix := strings.TrimSuffix(filepath.ToSlash(runtimeRoot), "/") + "/"
	seen := map[string]struct{}{}
	if ownExists {
		for _, candidate := range own {
			candidate = filepath.ToSlash(strings.TrimSpace(candidate))
			if !strings.HasPrefix(candidate, rootPrefix) || !strings.HasSuffix(candidate, "/product.json") {
				continue
			}
			if _, dup := seen[candidate]; dup {
				continue
			}
			seen[candidate] = struct{}{}
			paths = append(paths, candidate)
		}
	}
	if strings.EqualFold(strings.TrimSpace(profile.ID), crewProfileID) {
		shared, sharedErr := listSharedCrewManifestPaths(ctx, store, userID)
		if sharedErr != nil {
			return nil, false, sharedErr
		}
		paths = append(paths, shared...)
		if len(shared) > 0 {
			ownExists = true
		}
	}
	return paths, ownExists, nil
}

// sharedCrewOwner is the registered owner of the Crew at Crew/<folder> ("" for a Crew nobody registered).
func sharedCrewOwner(folder string) string {
	return resolveProjectOwnerOf(projectIdentity{Product: "work", Folder: folder, Shared: true})
}
