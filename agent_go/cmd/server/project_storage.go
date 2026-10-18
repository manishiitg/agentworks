package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

func init() {
	common.SetCodeProjectRootResolver(func(userID, path string) string {
		ref, ok := resolveCrewPath(context.Background(), userID, path)
		if !ok {
			return ""
		}
		product, _, known := projectProductForPath(ref.Root)
		if !known || product.ProfileID != "code" {
			return ""
		}
		return ref.Root
	})
}

// The existing owner registry also owns storage routing. Origins, owners and
// resource/session IDs survive a folder move; editable manifests are settings.
func projectStorageRoot(rec projectOwnerRecord) string {
	if rec.CanonicalRoot != "" {
		return rec.CanonicalRoot
	}
	if rec.Shared {
		return workspaceref.SharedProjectPath(rec.Folder)
	}
	product, ok := projectProductForProfile(rec.Product)
	if !ok {
		return ""
	}
	return workspaceref.PhysicalPathOf(rec.OwnerID, product.ProjectsRoot, rec.Folder)
}

func validateProjectStorageRecord(rec projectOwnerRecord) error {
	if !isProjectProfileID(rec.Product) {
		return fmt.Errorf("unknown project origin")
	}
	product, _ := projectProductForProfile(rec.Product)
	legacy := workspaceref.PhysicalPathOf(rec.OwnerID, product.ProjectsRoot, rec.Folder)
	shared := workspaceref.SharedProjectPath(rec.Folder)
	if rec.CanonicalRoot != "" && rec.CanonicalRoot != legacy && rec.CanonicalRoot != shared {
		return fmt.Errorf("project location does not match its registered identity")
	}
	for _, alias := range rec.Aliases {
		if alias != legacy && alias != shared {
			return fmt.Errorf("project alias does not match its registered identity")
		}
	}
	return nil
}

func checkProjectStorageClaim(records map[string]projectOwnerRecord, key string, rec projectOwnerRecord) error {
	root := projectStorageRoot(rec)
	for otherKey, other := range records {
		if key == otherKey {
			continue
		}
		if root == projectStorageRoot(other) || registryListHas(other.Aliases, root) {
			return fmt.Errorf("project location is already registered")
		}
		for _, alias := range rec.Aliases {
			if alias == projectStorageRoot(other) || registryListHas(other.Aliases, alias) {
				return fmt.Errorf("project alias is already registered")
			}
		}
	}
	return nil
}

// sharedRootRecord recognizes both origins, including a Crew spelling retained
// as an alias after rollback. Ambiguous legacy records never select an owner.
func (r *projectOwnerRegistry) sharedRootRecord(folder string) (projectOwnerRecord, bool) {
	root := workspaceref.SharedProjectPath(folder)
	var found projectOwnerRecord
	n := 0
	// Folder names survive movement. Two keyed lookups use the registry's
	// change-aware read cache; listing projects never scans/copies it per row.
	for _, product := range projectProducts {
		rec, exists := r.Lookup(product.ProfileID, folder)
		if exists && (projectStorageRoot(rec) == root || registryListHas(rec.Aliases, root)) {
			found, n = rec, n+1
		}
	}
	return found, n == 1
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
