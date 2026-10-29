package server

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

func init() {
	services.SetConnectionWorkspaceResolver(projectBindingRoot)
	chathistory.SetWorkflowStateRootResolver(projectBindingRoot)

}

// The existing owner registry also owns storage routing. Origins, owners and
// resource/session IDs survive a folder move; editable manifests are settings.
func projectStorageRoot(rec projectOwnerRecord) string {
	return workspaceref.SharedProjectPath(rec.Folder)
}

// Discovery and project-ID resolution use only the registered location. An old
// filesystem link is not another project and must never become a delete target.
func projectDiscoveryRootIsCurrent(root string) bool {
	id, ok := projectIdentityOf(root)
	if !ok {
		return true
	}
	rec, registered := defaultProjectOwners().Lookup(id.Product, id.Folder)
	return !registered || projectStorageRoot(rec) == root
}

func validateProjectStorageRecord(rec projectOwnerRecord) error {
	if rec.LocalRoot != "" && (!filepath.IsAbs(rec.LocalRoot) || filepath.Clean(rec.LocalRoot) != rec.LocalRoot || rec.ProjectID == "") {
		return fmt.Errorf("invalid local Crew folder binding")
	}
	if rec.Product != crewProfileID || rec.Folder == "" || strings.ContainsAny(rec.Folder, "/\\") || strings.HasPrefix(rec.Folder, ".") {
		return fmt.Errorf("invalid Crew identity")
	}
	if rec.CanonicalRoot != "" && rec.CanonicalRoot != projectStorageRoot(rec) {
		return fmt.Errorf("invalid Crew location")
	}
	// BindingRoot is an opaque, server-owned private-state namespace. It never
	// becomes a file route. Validate its owner/folder before using it as a key.
	if rec.BindingRoot != "" && rec.BindingRoot != projectStorageRoot(rec) {
		ref, ok := workspaceref.Parse(rec.BindingRoot)
		_, folder, project := ref.ProjectRoot()
		if !ok || !project || ref.Owner() != rec.OwnerID || folder != rec.Folder {
			return fmt.Errorf("invalid private state identity")
		}
	}
	return nil
}

// Path-keyed state keeps its original namespace, independently of file routing.
// Only an exact trusted project root/alias may select that namespace.
func projectBindingRoot(root string) string {
	root = canonicalCrewWorkspaceRoot(root)
	if id, ok := projectIdentityOf(root); ok {
		if rec, exists := defaultProjectOwners().Lookup(id.Product, id.Folder); exists && rec.BindingRoot != "" && validateProjectStorageRecord(rec) == nil &&
			root == projectStorageRoot(rec) {
			return rec.BindingRoot
		}
	}
	return root
}

// compareAndSetStorageLocation is internal migration machinery, not a public
// settings writer. It preserves audience and pins private state keys exactly once.
// The one-off migration separately moves folders and removes old-path links.
func (r *projectOwnerRegistry) compareAndSetStorageLocation(product, folder, expected, next string) error {
	err := r.modify(func(projects map[string]projectOwnerRecord) (bool, error) {
		key := projectOwnerKey(product, folder)
		rec, exists := projects[key]
		if !exists || userAccessHash(rec) != expected {
			return false, fmt.Errorf("project changed since the storage snapshot")
		}
		before := crewStorageSourceRoot(rec)
		if before == next {
			return false, nil
		}
		if rec.BindingRoot == "" {
			rec.BindingRoot = before
		}
		rec.CanonicalRoot, rec.Shared = next, projectRootIsShared(next)

		if err := validateProjectStorageRecord(rec); err != nil {
			return false, err
		}
		if err := checkProjectStorageClaim(projects, key, rec); err != nil {
			return false, err
		}
		projects[key] = rec
		return true, nil
	})
	if err == nil {
		crewOwners.mu.Lock()
		crewOwners.entries = map[string]crewOwnerEntry{}
		crewOwners.mu.Unlock()
	}
	return err
}

func checkProjectStorageClaim(records map[string]projectOwnerRecord, key string, rec projectOwnerRecord) error {
	root := projectStorageRoot(rec)
	for otherKey, other := range records {
		if key == otherKey {
			continue
		}
		if root == projectStorageRoot(other) {
			return fmt.Errorf("project location is already registered")
		}
		if rec.LocalRoot != "" && other.LocalRoot != "" && (crewLocalPathsOverlap(rec.LocalRoot, other.LocalRoot)) {
			return fmt.Errorf("this folder overlaps another Crew's folder; choose a separate folder")
		}
		for _, alias := range rec.Aliases {
			if alias == projectStorageRoot(other) || registryListHas(other.Aliases, alias) {
				return fmt.Errorf("project alias is already registered")
			}
		}
	}
	return nil
}

// sharedRootRecord returns the Crew owner record for a shared-root folder.
func (r *projectOwnerRegistry) sharedRootRecord(folder string) (projectOwnerRecord, bool) {
	root := workspaceref.SharedProjectPath(folder)
	// Folder names survive movement. The keyed lookup uses the registry's
	// change-aware read cache; listing projects never scans/copies it per row.
	rec, exists := r.Lookup(crewProfileID, folder)
	if !exists || projectStorageRoot(rec) != root {
		return projectOwnerRecord{}, false
	}
	return rec, true
}

func projectRootIsShared(root string) bool {
	return strings.HasPrefix(root, workspaceref.SharedCrewRoot+"/")
}

func projectStorageOwner(root string) string {
	id, ok := projectIdentityOf(root)
	if !ok {
		return ""
	}
	owner := resolveProjectOwnerOf(id)
	if id.PathOwner != "" && id.PathOwner != owner {
		return ""
	}
	return owner
}

// Project review remains distinct from ordinary sharing and private chat access.
func crewAdminInspectionAllowed(root string) bool {
	id, ok := projectIdentityOf(root)
	if !ok {
		return false
	}
	rec, ok := defaultProjectOwners().Lookup(id.Product, id.Folder)
	return ok && rec.AdminInspection
}
