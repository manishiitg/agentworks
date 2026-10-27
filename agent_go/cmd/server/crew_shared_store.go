package server

import (
	"context"
	"path/filepath"
	"sort"
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
	ProfileID    string
}

// listSharedCrewManifestPaths lists the <root>/<folder>/product.json paths at the shared Crew root whose Crew
// is registered to ownerID (every owner's when ownerID is empty). A Crew with no registry entry belongs to nobody and
// is skipped; hidden folders (bookkeeping such as Crew/.migrating) are skipped.
func listSharedCrewManifestPaths(ctx context.Context, store productProjectStore, ownerID string, profileID ...string) ([]string, error) {
	entries, err := listSharedCrewEntries(ctx, store)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if len(profileID) > 0 && profileID[0] != entry.ProfileID {
			continue
		}
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
	// The registry owns identity. Platform manifests no longer live in the
	// working tree and must not be discovered from person-editable files.
	records, err := defaultProjectOwners().All()
	if err != nil {
		return nil, err
	}
	var out []sharedCrewEntry
	for _, rec := range records {
		if rec.Product != crewProfileID || validateProjectStorageRecord(rec) != nil {
			continue
		}
		out = append(out, sharedCrewEntry{ManifestPath: projectStorageRoot(rec) + "/product.json", OwnerID: rec.OwnerID, ProfileID: rec.Product})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ManifestPath < out[j].ManifestPath })
	return out, nil
}

// listProjectManifestPaths lists the product.json paths of userID's projects of profile: the projects root in the
// user's own tree and, for a Crew, the Crew/ shared root filtered by manifest owner. exists is false only when
// neither holds anything. The paths are as the workspace lists them (full workspace paths).
func listProjectManifestPaths(ctx context.Context, store productProjectStore, userID string, profile agentprofiles.Profile) (paths []string, exists bool, err error) {
	if isProjectProfileID(profile.ID) {
		paths, err := listSharedCrewManifestPaths(ctx, store, userID, profile.ID)
		return paths, len(paths) > 0, err
	}
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
			if !strings.HasPrefix(candidate, rootPrefix) || !strings.HasSuffix(candidate, "/product.json") ||
				!projectDiscoveryRootIsCurrent(strings.TrimSuffix(candidate, "/product.json")) {
				continue
			}
			if _, dup := seen[candidate]; dup {
				continue
			}
			seen[candidate] = struct{}{}
			paths = append(paths, candidate)
		}
	}
	if isProjectProfileID(profile.ID) {
		shared, sharedErr := listSharedCrewManifestPaths(ctx, store, userID, profile.ID)
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
	return resolveProjectOwner(context.Background(), workspaceref.SharedProjectPath(folder))
}
